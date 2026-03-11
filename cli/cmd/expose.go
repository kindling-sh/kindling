package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
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

  # Stable callback URL — routes through an external Cloudflare tunnel:
  kindling expose --domain dev.myapp.com --route /auth=gateway --route /webhooks=gateway
  kindling expose --domain dev.myapp.com --route /=ui   # catch-all
  kindling expose --domain dev.myapp.com --list         # show routes
  kindling expose --domain dev.myapp.com --remove /auth # remove a route

When --domain is used, kindling creates a callback Ingress in the cluster
that routes paths under the stable domain to the correct services. This
works with any externally-managed Cloudflare tunnel that routes the domain
to localhost:80. Configure your OAuth callback URL once — it survives
across dev sessions.

The public URL is saved to .kindling/tunnel.yaml so that other commands
(kindling generate) can reference it.`,
	RunE: runExpose,
}

var (
	exposeProvider string
	exposePort     int
	exposeStop     bool
	exposeService  string
	exposeDomain   string
	exposeRoutes   []string
	exposeList     bool
	exposeRemove   string
)

func init() {
	exposeCmd.Flags().StringVar(&exposeProvider, "tunnel", "", "Tunnel provider: cloudflared or ngrok (auto-detected if omitted)")
	exposeCmd.Flags().IntVar(&exposePort, "port", 80, "Local port to expose (default: 80, the ingress controller)")
	exposeCmd.Flags().BoolVar(&exposeStop, "stop", false, "Stop a running tunnel")
	exposeCmd.Flags().StringVar(&exposeService, "service", "", "Ingress name to route tunnel traffic to (default: first ingress found)")
	exposeCmd.Flags().StringVar(&exposeDomain, "domain", "", "Stable callback domain (e.g. dev.myapp.com) — creates a callback Ingress for path-based routing")
	exposeCmd.Flags().StringArrayVar(&exposeRoutes, "route", nil, "Add a callback route: --route /path=service (e.g. --route /auth=gateway)")
	exposeCmd.Flags().BoolVar(&exposeList, "list", false, "List current callback routes for --domain")
	exposeCmd.Flags().StringVar(&exposeRemove, "remove", "", "Remove a callback route by path (e.g. --remove /auth)")
	rootCmd.AddCommand(exposeCmd)
}

func runExpose(cmd *cobra.Command, args []string) error {
	// ── Stop mode ───────────────────────────────────────────────
	if exposeStop {
		return stopTunnel()
	}

	// ── Callback domain mode ────────────────────────────────────
	// --domain creates/manages a stable callback Ingress in the cluster.
	// This is independent of the quick tunnel lifecycle.
	if exposeDomain != "" {
		return runCallbackDomain()
	}

	header("Public HTTPS tunnel")

	// Reuse existing tunnel if one is still running.
	if reused := checkRunningTunnel(); reused {
		return nil
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
	return startQuickTunnel(provider)
}

// checkRunningTunnel checks if a tunnel is already running. If alive, it
// re-patches ingresses and returns true. If stale, cleans up and returns false.
func checkRunningTunnel() bool {
	info, _ := core.ReadTunnelInfo()
	if info == nil || info.PID == 0 {
		return false
	}
	if core.ProcessAlive(info.PID) {
		patchIngressesForTunnel(info.URL)
		success(fmt.Sprintf("Tunnel already running → %s%s%s (pid %d)", colorBold, info.URL, colorReset, info.PID))
		fmt.Println()
		fmt.Printf("  Stop with: %skindling expose --stop%s\n", colorCyan, colorReset)
		fmt.Println()
		return true
	}
	// Stale PID — clean up.
	core.CleanupTunnel(clusterName)
	return false
}

// startQuickTunnel starts a tunnel with the given provider, saves state,
// patches ingresses, and prints the result. Used by both the CLI and dashboard.
func startQuickTunnel(provider string) error {
	step("⏳", fmt.Sprintf("Starting %s tunnel...", provider))

	var result *core.TunnelResult
	var err error

	switch provider {
	case "cloudflared":
		result, err = core.StartCloudflaredTunnel(exposePort, 30, true)
	case "ngrok":
		result, err = core.StartNgrokTunnel(exposePort)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
	if err != nil {
		return err
	}

	core.SaveTunnelInfo(clusterName, result.PublicURL, provider, result.PID)
	patchIngressesForTunnel(result.PublicURL)
	printTunnelRunning(result.PublicURL, result.PID)

	if !result.DNSOK {
		fmt.Printf("  %s⚠  DNS hasn't propagated yet — the tunnel is running but may take a moment to become reachable.%s\n\n", colorYellow, colorReset)
	}

	return nil
}

// ── Callback domain ─────────────────────────────────────────────
// Creates/manages a stable Ingress in the cluster for a user-defined domain.
// Traffic flows: domain → Cloudflare tunnel (externally managed) → localhost:80
// → Traefik → callback Ingress → correct service.

const callbackIngressName = "kindling-callback"

func runCallbackDomain() error {
	// ── Verify cluster ──────────────────────────────────────────
	if !core.ClusterExists(clusterName) {
		return fmt.Errorf("Kind cluster %q not found — run 'kindling init' first", clusterName)
	}

	// ── List mode ───────────────────────────────────────────────
	if exposeList {
		return listCallbackRoutes()
	}

	// ── Remove mode ─────────────────────────────────────────────
	if exposeRemove != "" {
		return removeCallbackRoute(exposeRemove)
	}

	// ── Add routes ──────────────────────────────────────────────
	if len(exposeRoutes) == 0 {
		// No --route flags — show current state or hint
		cfg, _ := core.ReadStableTunnelConfig()
		if cfg != nil && len(cfg.Routes) > 0 {
			return listCallbackRoutes()
		}
		fmt.Println()
		fmt.Printf("  Usage: %skindling expose --domain %s --route /path=service%s\n", colorCyan, exposeDomain, colorReset)
		fmt.Println()
		fmt.Printf("  Examples:\n")
		fmt.Printf("    kindling expose --domain %s --route /auth=gateway --route /webhooks=gateway\n", exposeDomain)
		fmt.Printf("    kindling expose --domain %s --route /=ui\n", exposeDomain)
		fmt.Println()
		return nil
	}

	header("Callback domain")

	// Parse --route flags: /path=service
	newRoutes := make(map[string]string)
	for _, r := range exposeRoutes {
		parts := strings.SplitN(r, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid --route %q — expected /path=service (e.g. /auth=gateway)", r)
		}
		path := parts[0]
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		newRoutes[path] = parts[1]
	}

	// Load existing config, merge routes
	cfg, _ := core.ReadStableTunnelConfig()
	if cfg == nil {
		cfg = &core.StableTunnelConfig{}
	}
	cfg.Domain = exposeDomain
	if cfg.Routes == nil {
		cfg.Routes = make(map[string]string)
	}
	for path, svc := range newRoutes {
		cfg.Routes[path] = svc
	}

	// Resolve service names — match against existing services in the cluster
	resolvedRoutes := make(map[string]core.CallbackRoute)
	for path, svcShort := range cfg.Routes {
		svcName, svcPort, err := resolveServiceByName(svcShort)
		if err != nil {
			warn(fmt.Sprintf("Route %s=%s: %v", path, svcShort, err))
			continue
		}
		resolvedRoutes[path] = core.CallbackRoute{
			Service: svcName,
			Port:    svcPort,
		}
		step("🔗", fmt.Sprintf("%s%s%s → %s:%d", colorBold, path, colorReset, svcName, svcPort))
	}

	if len(resolvedRoutes) == 0 {
		return fmt.Errorf("no valid routes resolved — check service names")
	}

	// Apply the callback ingress
	if err := applyCallbackIngress(exposeDomain, resolvedRoutes); err != nil {
		return err
	}

	// Save config
	if err := core.SaveStableTunnelConfig(cfg); err != nil {
		warn(fmt.Sprintf("Could not save config: %v", err))
	}

	fmt.Println()
	success(fmt.Sprintf("Callback ingress ready: %shttps://%s%s", colorBold, exposeDomain, colorReset))
	fmt.Println()
	fmt.Printf("  Routes active on %s%s%s:\n", colorBold, exposeDomain, colorReset)
	for path, route := range resolvedRoutes {
		fmt.Printf("    %s → %s:%d\n", path, route.Service, route.Port)
	}
	fmt.Println()
	fmt.Printf("  Configure this domain as your OAuth callback URL — it survives across sessions.\n")
	fmt.Printf("  List routes: %skindling expose --domain %s --list%s\n", colorCyan, exposeDomain, colorReset)
	fmt.Println()

	return nil
}

// resolveServiceByName finds a K8s Service matching the short name.
// If the exact name matches, use it. Otherwise, find a service whose
// name ends with "-<shortName>" (e.g. "jeff-vincent-gateway" for "gateway").
func resolveServiceByName(shortName string) (string, int32, error) {
	ctx := core.ClusterContext(clusterName)
	out, err := runSilent("kubectl", "--context", ctx, "get", "services",
		"-o", "jsonpath={range .items[*]}{.metadata.name},{.spec.ports[0].port}{\"\\n\"}{end}")
	if err != nil {
		return "", 0, fmt.Errorf("failed to list services: %w", err)
	}

	type svcInfo struct {
		name string
		port int32
	}
	var exact *svcInfo
	var suffix *svcInfo

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]
		port, _ := strconv.Atoi(parts[1])
		if port == 0 {
			continue
		}

		if name == shortName {
			exact = &svcInfo{name: name, port: int32(port)}
		}
		if strings.HasSuffix(name, "-"+shortName) {
			suffix = &svcInfo{name: name, port: int32(port)}
		}
	}

	if exact != nil {
		return exact.name, exact.port, nil
	}
	if suffix != nil {
		return suffix.name, suffix.port, nil
	}
	return "", 0, fmt.Errorf("no service matching %q found in cluster", shortName)
}

// applyCallbackIngress creates or updates the kindling-callback Ingress
// with path-based routing rules under the stable domain.
func applyCallbackIngress(domain string, routes map[string]core.CallbackRoute) error {
	// Build the Ingress YAML
	paths := ""
	for path, route := range routes {
		paths += fmt.Sprintf(`
        - path: %s
          pathType: Prefix
          backend:
            service:
              name: %s
              port:
                number: %d`, path, route.Service, route.Port)
	}

	yaml := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: default
  labels:
    app.kubernetes.io/managed-by: kindling-cli
  annotations:
    kindling.dev/callback-domain: "%s"
spec:
  ingressClassName: traefik
  rules:
  - host: %s
    http:
      paths:%s
`, callbackIngressName, domain, domain, paths)

	// Apply via kubectl
	out, err := core.KubectlApplyStdin(clusterName, yaml)
	if err != nil {
		return fmt.Errorf("failed to apply callback ingress: %s", out)
	}

	return nil
}

func listCallbackRoutes() error {
	cfg, err := core.ReadStableTunnelConfig()
	if err != nil || cfg == nil || len(cfg.Routes) == 0 {
		fmt.Println("  No callback routes configured.")
		fmt.Printf("  Add with: %skindling expose --domain <domain> --route /path=service%s\n", colorCyan, colorReset)
		return nil
	}

	fmt.Println()
	fmt.Printf("  Callback domain: %s%s%s\n\n", colorBold, cfg.Domain, colorReset)
	for path, svc := range cfg.Routes {
		fmt.Printf("    %s → %s\n", path, svc)
	}
	fmt.Println()
	return nil
}

func removeCallbackRoute(path string) error {
	cfg, err := core.ReadStableTunnelConfig()
	if err != nil || cfg == nil || len(cfg.Routes) == 0 {
		fmt.Println("  No callback routes configured.")
		return nil
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if _, exists := cfg.Routes[path]; !exists {
		fmt.Printf("  Route %s not found.\n", path)
		return nil
	}

	delete(cfg.Routes, path)

	if len(cfg.Routes) == 0 {
		// No routes left — delete the ingress
		ctx := core.ClusterContext(clusterName)
		runSilent("kubectl", "--context", ctx, "delete", "ingress", callbackIngressName, "--ignore-not-found")
		step("🗑", fmt.Sprintf("Removed last route — deleted callback ingress"))
	} else {
		// Re-resolve and re-apply
		resolvedRoutes := make(map[string]core.CallbackRoute)
		for p, svcShort := range cfg.Routes {
			svcName, svcPort, err := resolveServiceByName(svcShort)
			if err != nil {
				continue
			}
			resolvedRoutes[p] = core.CallbackRoute{Service: svcName, Port: svcPort}
		}
		if len(resolvedRoutes) > 0 {
			applyCallbackIngress(cfg.Domain, resolvedRoutes)
		}
	}

	if err := core.SaveStableTunnelConfig(cfg); err != nil {
		warn(fmt.Sprintf("Could not save config: %v", err))
	}

	step("✅", fmt.Sprintf("Removed route %s", path))
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
		ctx := core.ClusterContext(clusterName)
		// Read current host
		currentHost, err := runSilent("kubectl", "--context", ctx, "get", "ingress", name,
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
		tlsJSON, _ := runSilent("kubectl", "--context", ctx, "get", "ingress", name,
			"-o", "jsonpath={.spec.tls}")
		tlsJSON = strings.TrimSpace(tlsJSON)
		if tlsJSON != "" && tlsJSON != "[]" {
			ops = append(ops,
				map[string]interface{}{"op": "add", "path": "/metadata/annotations/" + strings.ReplaceAll(originalTLSAnnotation, "/", "~1"), "value": tlsJSON},
				map[string]interface{}{"op": "remove", "path": "/spec/tls"},
			)
		}

		patchBytes, _ := json.Marshal(ops)
		if _, err := runSilent("kubectl", "--context", ctx, "patch", "ingress", name,
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
		ctx := core.ClusterContext(clusterName)
		originalHost, err := runSilent("kubectl", "--context", ctx, "get", "ingress", name,
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
		tlsJSON, _ := runSilent("kubectl", "--context", ctx, "get", "ingress", name,
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
		if _, err := runSilent("kubectl", "--context", ctx, "patch", "ingress", name,
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
	ctx := core.ClusterContext(clusterName)
	out, err := runSilent("kubectl", "--context", ctx, "get", "ingress",
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
