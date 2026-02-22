package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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
  kindling expose                          # auto-detect provider, expose port 80
  kindling expose --provider cloudflared   # use cloudflared explicitly
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
	exposeCmd.Flags().StringVar(&exposeProvider, "provider", "", "Tunnel provider: cloudflared or ngrok (auto-detected if omitted)")
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
	if info, _ := readTunnelInfo(); info != nil && info.PID > 0 {
		if processAlive(info.PID) {
			success(fmt.Sprintf("Tunnel already running → %s%s%s (pid %d)", colorBold, info.URL, colorReset, info.PID))
			fmt.Println()
			fmt.Printf("  Stop with: %skindling expose --stop%s\n", colorCyan, colorReset)
			fmt.Println()
			return nil
		}
		// Stale PID — clean up and start fresh
		cleanupTunnel()
	}

	// ── Resolve provider ────────────────────────────────────────
	provider := exposeProvider
	if provider == "" {
		provider = detectTunnelProvider()
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
	if !clusterExists(clusterName) {
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

// detectTunnelProvider checks for available tunnel binaries.
func detectTunnelProvider() string {
	if commandExists("cloudflared") {
		return "cloudflared"
	}
	if commandExists("ngrok") {
		return "ngrok"
	}
	return ""
}

// ── Cloudflared ─────────────────────────────────────────────────

func runCloudflaredTunnel() error {
	step("⏳", "Starting cloudflared tunnel...")

	tunnelCmd := exec.Command("cloudflared", "tunnel",
		"--url", fmt.Sprintf("http://localhost:%d", exposePort),
	)

	// Capture stderr silently for URL parsing — no noise on the terminal.
	var stderrBuf bytes.Buffer
	var mu sync.Mutex
	pr, pw := io.Pipe()
	tunnelCmd.Stdout = nil
	tunnelCmd.Stderr = pw

	// Detach from parent process group so it survives CLI exit.
	tunnelCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Read stderr into buffer in background.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				mu.Lock()
				stderrBuf.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	if err := tunnelCmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("failed to start cloudflared: %w", err)
	}

	// Poll the captured stderr for the tunnel URL.
	var publicURL string
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		mu.Lock()
		data := stderrBuf.String()
		mu.Unlock()
		for _, line := range strings.Split(data, "\n") {
			if strings.Contains(line, ".trycloudflare.com") {
				for _, word := range strings.Fields(line) {
					if strings.HasPrefix(word, "https://") && strings.Contains(word, ".trycloudflare.com") {
						publicURL = strings.TrimRight(word, "|, ")
						break
					}
				}
			}
		}
		if publicURL != "" {
			break
		}
	}

	if publicURL == "" {
		// Kill the process if we couldn't get a URL — no point leaving it around.
		if tunnelCmd.Process != nil {
			_ = tunnelCmd.Process.Kill()
		}
		pw.Close()
		return fmt.Errorf("could not detect public URL — try running cloudflared manually")
	}

	// Wait for DNS to propagate. This is best-effort — the tunnel is
	// already functional; DNS just may not have reached public resolvers yet.
	step("🔍", "Waiting for DNS propagation...")
	dnsOK := waitForDNS(publicURL, 30*time.Second)

	// Save PID so we can stop it later, then let it run.
	saveTunnelInfo(publicURL, "cloudflared", tunnelCmd.Process.Pid)
	patchIngressesForTunnel(publicURL)
	printTunnelRunning(publicURL, tunnelCmd.Process.Pid)

	if !dnsOK {
		fmt.Printf("  %s⚠  DNS hasn't propagated yet — the tunnel is running but may take a moment to become reachable.%s\n\n", colorYellow, colorReset)
	}

	// Release the child — we don't wait on it; it runs in the background.
	go func() {
		_ = tunnelCmd.Wait()
		pw.Close()
	}()

	return nil
}

// ── Ngrok ───────────────────────────────────────────────────────

func runNgrokTunnel() error {
	step("⏳", "Starting ngrok tunnel...")

	tunnelCmd := exec.Command("ngrok", "http",
		fmt.Sprintf("%d", exposePort),
		"--log", "stdout",
		"--log-format", "json",
	)
	tunnelCmd.Stdout = nil
	tunnelCmd.Stderr = nil

	// Detach from parent process group so it survives CLI exit.
	tunnelCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := tunnelCmd.Start(); err != nil {
		return fmt.Errorf("failed to start ngrok: %w", err)
	}

	// Poll the ngrok local API for the public URL
	var publicURL string
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		url, err := getNgrokPublicURL()
		if err == nil && url != "" {
			publicURL = url
			break
		}
	}

	if publicURL == "" {
		if tunnelCmd.Process != nil {
			_ = tunnelCmd.Process.Kill()
		}
		return fmt.Errorf("could not detect public URL — check ngrok dashboard at http://localhost:4040")
	}

	saveTunnelInfo(publicURL, "ngrok", tunnelCmd.Process.Pid)
	patchIngressesForTunnel(publicURL)
	printTunnelRunning(publicURL, tunnelCmd.Process.Pid)

	// Release the child — runs in background.
	go func() { _ = tunnelCmd.Wait() }()

	return nil
}

// getNgrokPublicURL queries the ngrok local API for the tunnel URL.
func getNgrokPublicURL() (string, error) {
	out, err := runSilent("curl", "-s", "http://localhost:4040/api/tunnels")
	if err != nil {
		return "", err
	}
	// Parse the JSON response
	var resp struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
			Proto     string `json:"proto"`
		} `json:"tunnels"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", err
	}
	// Prefer HTTPS
	for _, t := range resp.Tunnels {
		if t.Proto == "https" {
			return t.PublicURL, nil
		}
	}
	if len(resp.Tunnels) > 0 {
		return resp.Tunnels[0].PublicURL, nil
	}
	return "", fmt.Errorf("no tunnels found")
}

// ── Shared helpers ──────────────────────────────────────────────

// tunnelInfo represents the persisted state of a running tunnel.
type tunnelInfo struct {
	Provider string
	URL      string
	PID      int
}

// printTunnelRunning shows the success output after backgrounding.
func printTunnelRunning(publicURL string, pid int) {
	fmt.Println()
	success(fmt.Sprintf("%s%s%s", colorBold, publicURL, colorReset))
	fmt.Println()
	fmt.Printf("  Tunnel running in background %s(pid %d)%s\n", colorDim, pid, colorReset)
	fmt.Printf("  Stop with: %skindling expose --stop%s\n", colorCyan, colorReset)
	fmt.Println()
}

// saveTunnelInfo persists the tunnel URL and PID to .kindling/tunnel.yaml
// and creates a ConfigMap in the cluster so the deploy action can discover it.
func saveTunnelInfo(publicURL, provider string, pid int) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	kindlingDir := filepath.Join(cwd, ".kindling")
	_ = os.MkdirAll(kindlingDir, 0755)

	tunnelFile := filepath.Join(kindlingDir, "tunnel.yaml")
	content := fmt.Sprintf("# Generated by kindling expose — do not edit\nprovider: %s\nurl: %s\npid: %d\ncreated: %s\n",
		provider, publicURL, pid, time.Now().Format(time.RFC3339))

	_ = os.WriteFile(tunnelFile, []byte(content), 0644)

	// Ensure .kindling/ is gitignored
	ensureTunnelGitignored(cwd)

	// Create/update ConfigMap in the cluster so the deploy action can auto-detect the tunnel.
	saveTunnelConfigMap(publicURL)
}

// saveTunnelConfigMap creates a ConfigMap with the tunnel URL + hostname.
func saveTunnelConfigMap(publicURL string) {
	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}
	// Pipe through apply so it's idempotent (create or update).
	yaml, err := runSilent("kubectl", "create", "configmap", "kindling-tunnel",
		"--from-literal=url="+publicURL,
		"--from-literal=hostname="+hostname,
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		return
	}
	applyCmd := exec.Command("kubectl", "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(yaml)
	_ = applyCmd.Run()
}

// readTunnelInfo loads tunnel state from .kindling/tunnel.yaml.
func readTunnelInfo() (*tunnelInfo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(cwd, ".kindling", "tunnel.yaml"))
	if err != nil {
		return nil, err
	}

	info := &tunnelInfo{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "provider:") {
			info.Provider = strings.TrimSpace(strings.TrimPrefix(line, "provider:"))
		} else if strings.HasPrefix(line, "url:") {
			info.URL = strings.TrimSpace(strings.TrimPrefix(line, "url:"))
		} else if strings.HasPrefix(line, "pid:") {
			info.PID, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid:")))
		}
	}
	return info, nil
}

// processAlive checks if a process with the given PID is still running.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 checks if the process exists.
	return proc.Signal(syscall.Signal(0)) == nil
}

// stopTunnel kills a running tunnel and cleans up.
func stopTunnel() error {
	info, err := readTunnelInfo()
	if err != nil || info == nil || info.PID == 0 {
		fmt.Println("  No tunnel is currently running.")
		return nil
	}

	if !processAlive(info.PID) {
		cleanupTunnel()
		fmt.Println("  Tunnel process already exited — cleaned up.")
		return nil
	}

	step("🛑", fmt.Sprintf("Stopping %s tunnel (pid %d)...", info.Provider, info.PID))

	// Kill the entire process group so child processes are cleaned up too.
	// cloudflared may spawn helpers that hold the tunnel connection open.
	_ = syscall.Kill(-info.PID, syscall.SIGTERM)

	// Wait for the process to exit, with a force-kill fallback.
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if !processAlive(info.PID) {
			break
		}
	}
	if processAlive(info.PID) {
		_ = syscall.Kill(-info.PID, syscall.SIGKILL)
		time.Sleep(500 * time.Millisecond)
	}

	cleanupTunnel()
	success("Tunnel stopped")
	return nil
}

// cleanupTunnel restores ingress hosts, removes tunnel.yaml, and deletes the ConfigMap.
func cleanupTunnel() {
	restoreIngresses()
	cwd, _ := os.Getwd()
	_ = os.Remove(filepath.Join(cwd, ".kindling", "tunnel.yaml"))
	_, _ = runSilent("kubectl", "delete", "configmap", "kindling-tunnel", "--ignore-not-found")
}

// waitForDNS polls until the tunnel hostname resolves in DNS.
// Cloudflare quick tunnels need a moment for DNS propagation —
// the URL appears in cloudflared's output BEFORE the DNS record
// is created, so we:
//  1. Wait a few seconds before the first lookup (avoid caching NXDOMAIN)
//  2. Use a custom resolver (1.1.1.1 / 8.8.8.8) to bypass local negative caching
//  3. Set a per-lookup timeout so a single slow lookup can't eat the entire budget
func waitForDNS(publicURL string, maxWait time.Duration) bool {
	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}

	// Give cloudflared time to register the tunnel before we start querying.
	time.Sleep(3 * time.Second)

	// Use Cloudflare + Google public DNS to avoid local NXDOMAIN caching.
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			// Alternate between 1.1.1.1 and 8.8.8.8
			for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
				conn, err := d.DialContext(ctx, "udp", server)
				if err == nil {
					return conn, nil
				}
			}
			return d.DialContext(ctx, network, address)
		},
	}

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addrs, err := resolver.LookupHost(ctx, hostname)
		cancel()
		if err == nil && len(addrs) > 0 {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// checkDNSOnce does a single DNS lookup via public resolvers (1.1.1.1 / 8.8.8.8)
// to see if the tunnel hostname resolves yet. Used by the dashboard status endpoint
// so we don't send the browser to an NXDOMAIN that gets cached.
func checkDNSOnce(publicURL string) bool {
	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
				conn, err := d.DialContext(ctx, "udp", server)
				if err == nil {
					return conn, nil
				}
			}
			return d.DialContext(ctx, network, address)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := resolver.LookupHost(ctx, hostname)
	return err == nil && len(addrs) > 0
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
			// Only one ingress can own a given host+path in nginx,
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

// ensureTunnelGitignored makes sure .kindling/ is in .gitignore.
func ensureTunnelGitignored(repoRoot string) {
	gitignorePath := filepath.Join(repoRoot, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return
	}

	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ".kindling/" || trimmed == ".kindling" {
			return // already ignored
		}
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(".kindling/\n")
}
