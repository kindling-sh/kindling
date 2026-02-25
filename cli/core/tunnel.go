package core

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
)

// TunnelInfo represents the persisted state of a running tunnel.
type TunnelInfo struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	URL      string `json:"url"`
	PID      int    `json:"pid"`
	Service  string `json:"service,omitempty"` // ingress name this tunnel is bound to
	Port     int    `json:"port,omitempty"`
}

// TunnelResult is returned by StartCloudflaredTunnel with the public URL
// and PID of the backgrounded process.
type TunnelResult struct {
	PublicURL string
	PID       int
	DNSOK     bool
}

// StartCloudflaredTunnel starts a cloudflared quick tunnel on the given port.
// It waits up to maxWaitURL seconds for the public URL to appear in stderr.
// If waitDNS is true, it also waits for DNS propagation.
func StartCloudflaredTunnel(port int, maxWaitURL int, waitDNS bool) (*TunnelResult, error) {
	tunnelCmd := exec.Command("cloudflared", "tunnel",
		"--url", fmt.Sprintf("http://localhost:%d", port),
	)

	var stderrBuf bytes.Buffer
	var mu sync.Mutex
	pr, pw := io.Pipe()
	tunnelCmd.Stdout = nil
	tunnelCmd.Stderr = pw
	tunnelCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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
		return nil, fmt.Errorf("failed to start cloudflared: %w", err)
	}

	// Poll stderr for the tunnel URL.
	var publicURL string
	for i := 0; i < maxWaitURL; i++ {
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
		if tunnelCmd.Process != nil {
			_ = tunnelCmd.Process.Kill()
		}
		pw.Close()
		return nil, fmt.Errorf("could not detect public URL from cloudflared")
	}

	result := &TunnelResult{
		PublicURL: publicURL,
		PID:       tunnelCmd.Process.Pid,
	}

	if waitDNS {
		result.DNSOK = WaitForDNS(publicURL, 30*time.Second)
	}

	// Release the child — runs in background.
	go func() {
		_ = tunnelCmd.Wait()
		pw.Close()
	}()

	return result, nil
}

// StartNgrokTunnel starts an ngrok tunnel on the given port.
func StartNgrokTunnel(port int) (*TunnelResult, error) {
	tunnelCmd := exec.Command("ngrok", "http",
		fmt.Sprintf("%d", port),
		"--log", "stdout",
		"--log-format", "json",
	)
	tunnelCmd.Stdout = nil
	tunnelCmd.Stderr = nil
	tunnelCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := tunnelCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start ngrok: %w", err)
	}

	var publicURL string
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		u, err := getNgrokPublicURL()
		if err == nil && u != "" {
			publicURL = u
			break
		}
	}

	if publicURL == "" {
		if tunnelCmd.Process != nil {
			_ = tunnelCmd.Process.Kill()
		}
		return nil, fmt.Errorf("could not detect public URL — check ngrok dashboard at http://localhost:4040")
	}

	result := &TunnelResult{
		PublicURL: publicURL,
		PID:       tunnelCmd.Process.Pid,
	}

	go func() { _ = tunnelCmd.Wait() }()
	return result, nil
}

func getNgrokPublicURL() (string, error) {
	out, err := RunSilent("curl", "-s", "http://localhost:4040/api/tunnels")
	if err != nil {
		return "", err
	}
	var resp struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
			Proto     string `json:"proto"`
		} `json:"tunnels"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", err
	}
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

// DetectTunnelProvider checks for available tunnel binaries.
func DetectTunnelProvider() string {
	if CommandExists("cloudflared") {
		return "cloudflared"
	}
	if CommandExists("ngrok") {
		return "ngrok"
	}
	return ""
}

// SaveTunnelInfo persists tunnel state to .kindling/tunnels.json and creates
// a ConfigMap in the cluster so the deploy action can discover it.
// Multiple tunnels are stored side by side, keyed by name.
func SaveTunnelInfo(clusterName string, info *TunnelInfo) {
	tunnels := readAllTunnels()

	// Replace existing entry with the same name, or append.
	found := false
	for i, t := range tunnels {
		if t.Name == info.Name {
			tunnels[i] = *info
			found = true
			break
		}
	}
	if !found {
		tunnels = append(tunnels, *info)
	}

	writeAllTunnels(tunnels)
	ensureTunnelGitignored(mustCwd())
	saveTunnelConfigMap(clusterName, tunnels)
}

func saveTunnelConfigMap(clusterName string, tunnels []TunnelInfo) {
	// Store every tunnel as a JSON array in the ConfigMap so workflows and
	// deploy actions can look up per-service tunnel URLs.
	data, _ := json.Marshal(tunnels)

	// Also write the primary (first) tunnel URL for backward compatibility.
	primaryURL := ""
	primaryHost := ""
	if len(tunnels) > 0 {
		primaryURL = tunnels[0].URL
		if u, err := url.Parse(primaryURL); err == nil && u.Host != "" {
			primaryHost = u.Host
		}
	}

	yaml, err := RunSilent("kubectl", "create", "configmap", "kindling-tunnel",
		"--from-literal=url="+primaryURL,
		"--from-literal=hostname="+primaryHost,
		"--from-literal=tunnels="+string(data),
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		return
	}
	KubectlApplyStdin(clusterName, yaml)
}

// ReadTunnelInfo loads the tunnel with the given name. If name is empty,
// it returns the first tunnel (backward compatible with single-tunnel usage).
func ReadTunnelInfo(name ...string) (*TunnelInfo, error) {
	tunnels := readAllTunnels()
	if len(tunnels) == 0 {
		return nil, fmt.Errorf("no tunnels found")
	}

	target := ""
	if len(name) > 0 {
		target = name[0]
	}

	if target == "" {
		// Backward compat: return the first tunnel.
		return &tunnels[0], nil
	}

	for i := range tunnels {
		if tunnels[i].Name == target {
			return &tunnels[i], nil
		}
	}
	return nil, fmt.Errorf("tunnel %q not found", target)
}

// ReadAllTunnels returns all running tunnel entries.
func ReadAllTunnels() []TunnelInfo {
	return readAllTunnels()
}

func readAllTunnels() []TunnelInfo {
	cwd := mustCwd()
	data, err := os.ReadFile(filepath.Join(cwd, ".kindling", "tunnels.json"))
	if err != nil {
		// Try legacy tunnel.yaml for migration.
		return migrateLegacyTunnel(cwd)
	}
	var tunnels []TunnelInfo
	if err := json.Unmarshal(data, &tunnels); err != nil {
		return nil
	}
	return tunnels
}

func writeAllTunnels(tunnels []TunnelInfo) {
	cwd := mustCwd()
	kindlingDir := filepath.Join(cwd, ".kindling")
	_ = os.MkdirAll(kindlingDir, 0755)

	data, _ := json.MarshalIndent(tunnels, "", "  ")
	_ = os.WriteFile(filepath.Join(kindlingDir, "tunnels.json"), data, 0644)

	// Remove legacy tunnel.yaml if it exists.
	_ = os.Remove(filepath.Join(kindlingDir, "tunnel.yaml"))
}

// migrateLegacyTunnel reads the old single-tunnel .kindling/tunnel.yaml
// and returns it as a one-element slice so existing tunnels keep working.
func migrateLegacyTunnel(cwd string) []TunnelInfo {
	data, err := os.ReadFile(filepath.Join(cwd, ".kindling", "tunnel.yaml"))
	if err != nil {
		return nil
	}

	info := TunnelInfo{Name: "default"}
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
	if info.URL == "" {
		return nil
	}
	return []TunnelInfo{info}
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// ProcessAlive checks if a process with the given PID is still running.
func ProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// StopTunnelProcess stops a tunnel by name. If name is empty, stops all tunnels.
// Returns the TunnelInfo entries that were stopped.
func StopTunnelProcess(name ...string) []*TunnelInfo {
	tunnels := readAllTunnels()
	if len(tunnels) == 0 {
		return nil
	}

	target := ""
	if len(name) > 0 {
		target = name[0]
	}

	var stopped []*TunnelInfo
	var remaining []TunnelInfo

	for i := range tunnels {
		t := &tunnels[i]
		if target != "" && t.Name != target {
			remaining = append(remaining, *t)
			continue
		}

		if t.PID > 0 && ProcessAlive(t.PID) {
			_ = syscall.Kill(-t.PID, syscall.SIGTERM)
			for j := 0; j < 10; j++ {
				time.Sleep(500 * time.Millisecond)
				if !ProcessAlive(t.PID) {
					break
				}
			}
			if ProcessAlive(t.PID) {
				_ = syscall.Kill(-t.PID, syscall.SIGKILL)
				time.Sleep(500 * time.Millisecond)
			}
		}
		stopped = append(stopped, t)
	}

	writeAllTunnels(remaining)
	return stopped
}

// CleanupTunnel removes the tunnel file and ConfigMap.
// If name is provided, only that tunnel is removed; otherwise all are removed.
func CleanupTunnel(clusterName string, name ...string) {
	target := ""
	if len(name) > 0 {
		target = name[0]
	}

	if target == "" {
		// Remove everything.
		cwd := mustCwd()
		_ = os.Remove(filepath.Join(cwd, ".kindling", "tunnels.json"))
		_ = os.Remove(filepath.Join(cwd, ".kindling", "tunnel.yaml")) // legacy
	} else {
		// Remove only the named tunnel from the list.
		tunnels := readAllTunnels()
		var remaining []TunnelInfo
		for _, t := range tunnels {
			if t.Name != target {
				remaining = append(remaining, t)
			}
		}
		writeAllTunnels(remaining)
	}

	if clusterName != "" {
		Kubectl(clusterName, "delete", "configmap", "kindling-tunnel", "--ignore-not-found")
	} else {
		RunSilent("kubectl", "delete", "configmap", "kindling-tunnel", "--ignore-not-found")
	}
}

// WaitForDNS polls until the tunnel hostname resolves in DNS.
func WaitForDNS(publicURL string, maxWait time.Duration) bool {
	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}

	time.Sleep(3 * time.Second)

	resolver := publicDNSResolver(3 * time.Second)

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

// CheckDNSOnce does a single DNS lookup via public resolvers.
func CheckDNSOnce(publicURL string) bool {
	hostname := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		hostname = u.Host
	}

	resolver := publicDNSResolver(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := resolver.LookupHost(ctx, hostname)
	return err == nil && len(addrs) > 0
}

func publicDNSResolver(timeout time.Duration) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
				conn, err := d.DialContext(ctx, "udp", server)
				if err == nil {
					return conn, nil
				}
			}
			return d.DialContext(ctx, network, address)
		},
	}
}

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
			return
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
