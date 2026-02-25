package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

var exposeCmd = &cobra.Command{
	Use:   "expose",
	Short: "Expose the local cluster via a public HTTPS tunnel",
	Long: `Creates a secure tunnel from a public HTTPS URL to the Kind cluster's
ingress controller, enabling external OAuth/OIDC providers (Auth0, Okta,
Firebase Auth, etc.) to call back into local services.

The tunnel runs in the background — you get your terminal back immediately.

Multiple tunnels can run simultaneously — use --name to label each one.
This is useful when services need separate public URLs (e.g. an Auth0 SPA
callback for the frontend + a separate API audience URL for the backend).

Supported providers:
  cloudflared  — Cloudflare Tunnel (free, no account required for quick tunnels)
  ngrok        — ngrok tunnel (requires free account + auth token)

Examples:
  kindling expose                          # auto-detect provider, expose port 80
  kindling expose --provider cloudflared   # use cloudflared explicitly
  kindling expose --port 443               # expose a different port
  kindling expose --stop                   # stop all running tunnels
  kindling expose --stop --name frontend   # stop only the "frontend" tunnel

  # Multiple tunnels for Auth0 SPA + API:
  kindling expose --name frontend --service my-spa-ingress
  kindling expose --name api --service auth-api-ingress

  kindling expose --list                   # show all running tunnels

The public URLs are saved to .kindling/tunnels.json so that other commands
(kindling generate) can reference them.`,
	RunE: runExpose,
}

var (
	exposeProvider string
	exposePort     int
	exposeStop     bool
	exposeService  string
	exposeName     string
	exposeList     bool
)

func init() {
	exposeCmd.Flags().StringVar(&exposeProvider, "provider", "", "Tunnel provider: cloudflared or ngrok (auto-detected if omitted)")
	exposeCmd.Flags().IntVar(&exposePort, "port", 80, "Local port to expose (default: 80, the ingress controller)")
	exposeCmd.Flags().BoolVar(&exposeStop, "stop", false, "Stop running tunnel(s)")
	exposeCmd.Flags().StringVar(&exposeService, "service", "", "Ingress name to route tunnel traffic to (default: first ingress found)")
	exposeCmd.Flags().StringVar(&exposeName, "name", "default", "Tunnel name (use different names for multiple simultaneous tunnels)")
	exposeCmd.Flags().BoolVar(&exposeList, "list", false, "List all running tunnels")
	rootCmd.AddCommand(exposeCmd)
}

func runExpose(cmd *cobra.Command, args []string) error {
	// ── List mode ───────────────────────────────────────────────
	if exposeList {
		return listTunnels()
	}

	// ── Stop mode ───────────────────────────────────────────────
	if exposeStop {
		return stopTunnel()
	}

	header("Public HTTPS tunnel")

	// ── Check for already-running tunnel with this name ─────────
	if info, _ := core.ReadTunnelInfo(exposeName); info != nil && info.PID > 0 {
		if core.ProcessAlive(info.PID) {
			success(fmt.Sprintf("Tunnel %q already running → %s%s%s (pid %d)", exposeName, colorBold, info.URL, colorReset, info.PID))
			fmt.Println()
			fmt.Printf("  Stop with: %skindling expose --stop --name %s%s\n", colorCyan, exposeName, colorReset)
			fmt.Println()
			return nil
		}
		// Stale PID — clean up this specific tunnel and start fresh
		core.CleanupTunnel(clusterName, exposeName)
	}

	// ── Resolve provider ────────────────────────────────────────
	provider := exposeProvider
	if provider == "" {
		provider = core.DetectTunnelProvider()
	}
	if provider == "" {
		fail("No tunnel provider found")
		fmt.Println()
		fmt.Println("  Install one of:")
		fmt.Printf("    brew install cloudflare/cloudflare/cloudflared\n")
		fmt.Printf("    brew install ngrok/ngrok/ngrok\n")
		fmt.Println()
		return fmt.Errorf("install cloudflared or ngrok and try again")
	}

	// ── Verify cluster is running ───────────────────────────────
	if !core.ClusterExists(clusterName) {
		return fmt.Errorf("Kind cluster %q not found — run 'kindling init' first", clusterName)
	}

	// ── Start tunnel ────────────────────────────────────────────
	switch provider {
	case "cloudflared":
		return runCloudflaredTunnel()
	case "ngrok":
		return runNgrokTunnel()
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
}

// ── Cloudflared ─────────────────────────────────────────────────

func runCloudflaredTunnel() error {
	step("⏳", fmt.Sprintf("Starting cloudflared tunnel %q...", exposeName))

	result, err := core.StartCloudflaredTunnel(exposePort, 30, true)
	if err != nil {
		return err
	}

	core.SaveTunnelInfo(clusterName, &core.TunnelInfo{
		Name:     exposeName,
		Provider: "cloudflared",
		URL:      result.PublicURL,
		PID:      result.PID,
		Service:  exposeService,
		Port:     exposePort,
	})
	patchIngressForTunnel(exposeName, result.PublicURL, exposeService)
	printTunnelRunning(exposeName, result.PublicURL, result.PID)

	if !result.DNSOK {
		fmt.Printf("  %s⚠  DNS hasn't propagated yet — the tunnel is running but may take a moment to become reachable.%s\n\n", colorYellow, colorReset)
	}

	return nil
}

// ── Ngrok ───────────────────────────────────────────────────────

func runNgrokTunnel() error {
	step("⏳", fmt.Sprintf("Starting ngrok tunnel %q...", exposeName))

	result, err := core.StartNgrokTunnel(exposePort)
	if err != nil {
		return err
	}

	core.SaveTunnelInfo(clusterName, &core.TunnelInfo{
		Name:     exposeName,
		Provider: "ngrok",
		URL:      result.PublicURL,
		PID:      result.PID,
		Service:  exposeService,
		Port:     exposePort,
	})
	patchIngressForTunnel(exposeName, result.PublicURL, exposeService)
	printTunnelRunning(exposeName, result.PublicURL, result.PID)

	return nil
}

// ── Shared helpers ──────────────────────────────────────────────

// printTunnelRunning shows the success output after backgrounding.
func printTunnelRunning(name, publicURL string, pid int) {
	fmt.Println()
	if name != "default" {
		success(fmt.Sprintf("[%s] %s%s%s", name, colorBold, publicURL, colorReset))
	} else {
		success(fmt.Sprintf("%s%s%s", colorBold, publicURL, colorReset))
	}
	fmt.Println()
	fmt.Printf("  Tunnel running in background %s(pid %d)%s\n", colorDim, pid, colorReset)
	fmt.Printf("  Stop with: %skindling expose --stop", colorCyan)
	if name != "default" {
		fmt.Printf(" --name %s", name)
	}
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("  List all:  %skindling expose --list%s\n", colorCyan, colorReset)
	fmt.Println()
}

// stopTunnel kills running tunnel(s) and cleans up.
// If --name is set, only that tunnel is stopped; otherwise all are stopped.
func stopTunnel() error {
	// Determine whether to stop a specific tunnel or all.
	stopName := ""
	if exposeName != "default" {
		stopName = exposeName
	}

	tunnels := core.ReadAllTunnels()
	if len(tunnels) == 0 {
		fmt.Println("  No tunnels are currently running.")
		return nil
	}

	if stopName != "" {
		// Stop a specific tunnel.
		info, _ := core.ReadTunnelInfo(stopName)
		if info == nil {
			fmt.Printf("  No tunnel named %q found.\n", stopName)
			return nil
		}
		if !core.ProcessAlive(info.PID) {
			core.CleanupTunnel(clusterName, stopName)
			restoreIngressByService(info.Service)
			fmt.Printf("  Tunnel %q already exited — cleaned up.\n", stopName)
			return nil
		}
		step("🛑", fmt.Sprintf("Stopping %s tunnel %q (pid %d)...", info.Provider, stopName, info.PID))
		core.StopTunnelProcess(stopName)
		restoreIngressByService(info.Service)
		success(fmt.Sprintf("Tunnel %q stopped", stopName))
	} else {
		// Stop all tunnels.
		for _, t := range tunnels {
			if t.PID > 0 && core.ProcessAlive(t.PID) {
				step("🛑", fmt.Sprintf("Stopping %s tunnel %q (pid %d)...", t.Provider, t.Name, t.PID))
			}
		}
		core.StopTunnelProcess()
		core.CleanupTunnel(clusterName)
		restoreIngresses()
		success("All tunnels stopped")
	}
	return nil
}

// listTunnels prints all currently running tunnels.
func listTunnels() error {
	tunnels := core.ReadAllTunnels()
	if len(tunnels) == 0 {
		fmt.Println("  No tunnels are currently running.")
		return nil
	}

	header("Active tunnels")
	for _, t := range tunnels {
		alive := core.ProcessAlive(t.PID)
		status := fmt.Sprintf("%s●%s", colorGreen, colorReset)
		if !alive {
			status = fmt.Sprintf("%s●%s", colorRed, colorReset)
		}
		svc := t.Service
		if svc == "" {
			svc = "(auto)"
		}
		fmt.Printf("  %s  %s%-12s%s  %s%s%s  → %s  pid %d\n",
			status,
			colorBold, t.Name, colorReset,
			colorCyan, t.URL, colorReset,
			svc, t.PID,
		)
	}
	fmt.Println()
	return nil
}

// ── Ingress patching ──────────────────────────────────────────

const originalHostAnnotation = "kindling.dev/original-host"
const originalTLSAnnotation = "kindling.dev/original-tls"
const tunnelNameAnnotation = "kindling.dev/tunnel-name"

// patchIngressForTunnel replaces the host on the target ingress with the
// tunnel hostname. If service is specified, only that ingress is patched;
// otherwise the first available ingress is used.
func patchIngressForTunnel(tunnelName, publicURL, service string) {
	// Always restore any orphaned ingresses first — self-heals if a previous
	// tunnel died without cleanup (e.g. machine sleep, force-kill).
	if service != "" {
		restoreIngressByService(service)
	} else {
		restoreIngresses()
	}

	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}

	names, err := getIngressNames()
	if err != nil || len(names) == 0 {
		return
	}

	// If service was specified, only patch that one.
	if service != "" {
		found := false
		for _, n := range names {
			if n == service {
				found = true
				break
			}
		}
		if found {
			names = []string{service}
		} else {
			return
		}
	}

	for _, name := range names {
		// Read current host
		currentHost, err := runSilent("kubectl", "get", "ingress", name,
			"-o", "jsonpath={.spec.rules[0].host}")
		if err != nil || strings.TrimSpace(currentHost) == "" {
			continue
		}
		currentHost = strings.TrimSpace(currentHost)

		// Skip if already set to tunnel host
		if currentHost == hostname {
			continue
		}

		// Build the JSON-patch operations:
		// 1. Save original host as annotation
		// 2. Replace ingress rule host with tunnel hostname
		// 3. Save tunnel name as annotation so we know which tunnel owns it
		ops := []map[string]interface{}{
			{"op": "add", "path": "/metadata/annotations/" + strings.ReplaceAll(originalHostAnnotation, "/", "~1"), "value": currentHost},
			{"op": "add", "path": "/metadata/annotations/" + strings.ReplaceAll(tunnelNameAnnotation, "/", "~1"), "value": tunnelName},
			{"op": "replace", "path": "/spec/rules/0/host", "value": hostname},
		}

		// 4. If the ingress has a TLS block (cert-manager, etc.), save it as
		//    an annotation and remove it — cloudflared terminates TLS at the edge.
		tlsJSON, _ := runSilent("kubectl", "get", "ingress", name,
			"-o", "jsonpath={.spec.tls}")
		tlsJSON = strings.TrimSpace(tlsJSON)
		if tlsJSON != "" && tlsJSON != "[]" {
			ops = append(ops,
				map[string]interface{}{"op": "add", "path": "/metadata/annotations/" + strings.ReplaceAll(originalTLSAnnotation, "/", "~1"), "value": tlsJSON},
				map[string]interface{}{"op": "remove", "path": "/spec/tls"},
			)
		}

		patchBytes, _ := json.Marshal(ops)
		if _, err := runSilent("kubectl", "patch", "ingress", name,
			"--type=json", "-p="+string(patchBytes)); err == nil {
			step("🔀", fmt.Sprintf("Routing tunnel %q → ingress/%s", tunnelName, name))
			// Only one ingress can own a given host+path in nginx,
			// so stop after the first successful patch.
			break
		}
	}
}

// restoreIngressByService restores a specific ingress that was patched for a tunnel.
func restoreIngressByService(service string) {
	if service == "" {
		return
	}
	restoreIngressesByFilter(func(name string) bool {
		return name == service
	})
}

// restoreIngresses reverts all ingresses that were patched by any tunnel.
func restoreIngresses() {
	restoreIngressesByFilter(func(_ string) bool { return true })
}

// restoreIngressesByFilter reverts ingresses matching the filter.
func restoreIngressesByFilter(match func(name string) bool) {
	names, err := getIngressNames()
	if err != nil || len(names) == 0 {
		return
	}

	restored := 0
	for _, name := range names {
		if !match(name) {
			continue
		}

		originalHost, err := runSilent("kubectl", "get", "ingress", name,
			"-o", `go-template={{index .metadata.annotations "kindling.dev/original-host"}}`,
		)
		if err != nil {
			continue
		}
		originalHost = strings.TrimSpace(originalHost)
		if originalHost == "" || strings.Contains(originalHost, "no value") {
			continue
		}

		// Build restore operations:
		// 1. Put the original host back
		// 2. Remove the host and tunnel-name annotations
		ops := []map[string]interface{}{
			{"op": "replace", "path": "/spec/rules/0/host", "value": originalHost},
			{"op": "remove", "path": "/metadata/annotations/" + strings.ReplaceAll(originalHostAnnotation, "/", "~1")},
		}
		// Remove tunnel-name annotation if present
		tunnelOwner, _ := runSilent("kubectl", "get", "ingress", name,
			"-o", `go-template={{index .metadata.annotations "kindling.dev/tunnel-name"}}`,
		)
		if tn := strings.TrimSpace(tunnelOwner); tn != "" && !strings.Contains(tn, "no value") {
			ops = append(ops,
				map[string]interface{}{"op": "remove", "path": "/metadata/annotations/" + strings.ReplaceAll(tunnelNameAnnotation, "/", "~1")},
			)
		}

		// 3. If a saved TLS block exists, restore it and remove the annotation
		tlsJSON, _ := runSilent("kubectl", "get", "ingress", name,
			"-o", `go-template={{index .metadata.annotations "kindling.dev/original-tls"}}`,
		)
		tlsJSON = strings.TrimSpace(tlsJSON)
		if tlsJSON != "" && !strings.Contains(tlsJSON, "no value") {
			var tlsBlock interface{}
			if json.Unmarshal([]byte(tlsJSON), &tlsBlock) == nil {
				ops = append(ops,
					map[string]interface{}{"op": "add", "path": "/spec/tls", "value": tlsBlock},
					map[string]interface{}{"op": "remove", "path": "/metadata/annotations/" + strings.ReplaceAll(originalTLSAnnotation, "/", "~1")},
				)
			}
		}

		patchBytes, _ := json.Marshal(ops)
		if _, err := runSilent("kubectl", "patch", "ingress", name,
			"--type=json", "-p="+string(patchBytes)); err == nil {
			restored++
		}
	}

	if restored > 0 {
		step("🔀", fmt.Sprintf("Restored %d ingress(es) to original hosts", restored))
	}
}

// getIngressNames returns the names of all Ingresses in the default namespace.
func getIngressNames() ([]string, error) {
	out, err := runSilent("kubectl", "get", "ingress",
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	return strings.Fields(out), nil
}
