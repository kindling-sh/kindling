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

Supported providers:
  cloudflared  — Cloudflare Tunnel (free, no account required for quick tunnels)
  ngrok        — ngrok tunnel (requires free account + auth token)

Examples:
  kindling expose                          # auto-detect tunnel, expose port 80
  kindling expose --tunnel cloudflared     # use cloudflared explicitly
  kindling expose --port 443               # expose a different port
  kindling expose --stop                   # stop a running tunnel

The public URL is saved to .kindling/tunnel.yaml so that other commands
(kindling generate) can reference it.`,
	RunE: runExpose,
}

var (
	exposeProvider string
	exposePort     int
	exposeStop     bool
	exposeService  string
)

func init() {
	exposeCmd.Flags().StringVar(&exposeProvider, "tunnel", "", "Tunnel provider: cloudflared or ngrok (auto-detected if omitted)")
	exposeCmd.Flags().IntVar(&exposePort, "port", 80, "Local port to expose (default: 80, the ingress controller)")
	exposeCmd.Flags().BoolVar(&exposeStop, "stop", false, "Stop a running tunnel")
	exposeCmd.Flags().StringVar(&exposeService, "service", "", "Ingress name to route tunnel traffic to (default: first ingress found)")
	rootCmd.AddCommand(exposeCmd)
}

func runExpose(cmd *cobra.Command, args []string) error {
	// ── Stop mode ───────────────────────────────────────────────
	if exposeStop {
		return stopTunnel()
	}

	header("Public HTTPS tunnel")

	// ── Check for already-running tunnel ────────────────────────
	if info, _ := core.ReadTunnelInfo(); info != nil && info.PID > 0 {
		if core.ProcessAlive(info.PID) {
			success(fmt.Sprintf("Tunnel already running → %s%s%s (pid %d)", colorBold, info.URL, colorReset, info.PID))
			fmt.Println()
			fmt.Printf("  Stop with: %skindling expose --stop%s\n", colorCyan, colorReset)
			fmt.Println()
			return nil
		}
		// Stale PID — clean up and start fresh
		core.CleanupTunnel(clusterName)
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
	step("⏳", "Starting cloudflared tunnel...")

	result, err := core.StartCloudflaredTunnel(exposePort, 30, true)
	if err != nil {
		return err
	}

	core.SaveTunnelInfo(clusterName, result.PublicURL, "cloudflared", result.PID)
	patchIngressesForTunnel(result.PublicURL)
	printTunnelRunning(result.PublicURL, result.PID)

	if !result.DNSOK {
		fmt.Printf("  %s⚠  DNS hasn't propagated yet — the tunnel is running but may take a moment to become reachable.%s\n\n", colorYellow, colorReset)
	}

	return nil
}

// ── Ngrok ───────────────────────────────────────────────────────

func runNgrokTunnel() error {
	step("⏳", "Starting ngrok tunnel...")

	result, err := core.StartNgrokTunnel(exposePort)
	if err != nil {
		return err
	}

	core.SaveTunnelInfo(clusterName, result.PublicURL, "ngrok", result.PID)
	patchIngressesForTunnel(result.PublicURL)
	printTunnelRunning(result.PublicURL, result.PID)

	return nil
}

// ── Shared helpers ──────────────────────────────────────────────

// printTunnelRunning shows the success output after backgrounding.
func printTunnelRunning(publicURL string, pid int) {
	fmt.Println()
	success(fmt.Sprintf("%s%s%s", colorBold, publicURL, colorReset))
	fmt.Println()
	fmt.Printf("  Tunnel running in background %s(pid %d)%s\n", colorDim, pid, colorReset)
	fmt.Printf("  Stop with: %skindling expose --stop%s\n", colorCyan, colorReset)
	fmt.Println()
}

// stopTunnel kills a running tunnel and cleans up.
func stopTunnel() error {
	info, err := core.ReadTunnelInfo()
	if err != nil || info == nil || info.PID == 0 {
		fmt.Println("  No tunnel is currently running.")
		return nil
	}

	if !core.ProcessAlive(info.PID) {
		core.CleanupTunnel(clusterName)
		restoreIngresses()
		fmt.Println("  Tunnel process already exited — cleaned up.")
		return nil
	}

	step("🛑", fmt.Sprintf("Stopping %s tunnel (pid %d)...", info.Provider, info.PID))
	core.StopTunnelProcess()
	core.CleanupTunnel(clusterName)
	restoreIngresses()
	success("Tunnel stopped")
	return nil
}

// ── Ingress patching ──────────────────────────────────────────

const originalHostAnnotation = "kindling.dev/original-host"
const originalTLSAnnotation = "kindling.dev/original-tls"

// patchIngressesForTunnel replaces the host on every Ingress in the default
// namespace with the tunnel hostname, saving the original host as an annotation
// so it can be restored later.
func patchIngressesForTunnel(publicURL string) {
	// Always restore any orphaned ingresses first — self-heals if a previous
	// tunnel died without cleanup (e.g. machine sleep, force-kill).
	restoreIngresses()

	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}

	names, err := getIngressNames()
	if err != nil || len(names) == 0 {
		return
	}

	// If --service was specified, only patch that one.
	if exposeService != "" {
		found := false
		for _, n := range names {
			if n == exposeService {
				found = true
				break
			}
		}
		if found {
			names = []string{exposeService}
		} else {
			return
		}
	}

	patched := 0
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
		ops := []map[string]interface{}{
			{"op": "add", "path": "/metadata/annotations/" + strings.ReplaceAll(originalHostAnnotation, "/", "~1"), "value": currentHost},
			{"op": "replace", "path": "/spec/rules/0/host", "value": hostname},
		}

		// 3. If the ingress has a TLS block (cert-manager, etc.), save it as
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
			step("🔀", fmt.Sprintf("Routing tunnel → ingress/%s", name))
			patched++
			// Only one ingress can own a given host+path in Traefik,
			// so stop after the first successful patch.
			break
		}
	}
}

// restoreIngresses reverts any ingresses that were patched by patchIngressesForTunnel,
// restoring the original host from the saved annotation.
func restoreIngresses() {
	names, err := getIngressNames()
	if err != nil || len(names) == 0 {
		return
	}

	restored := 0
	for _, name := range names {
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
		// 2. Remove the host annotation
		ops := []map[string]interface{}{
			{"op": "replace", "path": "/spec/rules/0/host", "value": originalHost},
			{"op": "remove", "path": "/metadata/annotations/" + strings.ReplaceAll(originalHostAnnotation, "/", "~1")},
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
