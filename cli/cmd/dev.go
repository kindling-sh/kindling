package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

var (
	devDeployment string
	devNamespace  string
)

func init() {
	devCmd.Flags().StringVarP(&devDeployment, "deployment", "d", "", "Frontend deployment name (required)")
	devCmd.Flags().StringVarP(&devNamespace, "namespace", "n", "default", "Kubernetes namespace")
	devCmd.Flags().BoolVar(&debugStop, "stop", false, "Stop the dev session")
	devCmd.MarkFlagRequired("deployment")
	rootCmd.AddCommand(devCmd)
}

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Run a frontend dev server locally with cluster API access",
	Long: `Start a local frontend development session. This command:

  • Port-forwards backend API services from the cluster to localhost
  • Detects OAuth/OIDC in your source and starts an HTTPS tunnel for callbacks
  • Patches your dev server config to allow the tunnel hostname
  • Guides you to start your local dev server (vite, next dev, etc.)

Use this for frontend deployments (nginx, caddy, etc.) where you want to
run the dev server locally and hit APIs in the cluster.

  kindling dev -d my-frontend
  kindling dev -d my-frontend --stop`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if debugStop {
			return stopDev(devDeployment)
		}
		return runDev(devDeployment, devNamespace)
	},
}

// ── Run dev session ─────────────────────────────────────────────

func runDev(deployment, namespace string) error {
	// Check if already running
	if state, err := loadDebugState(deployment); err == nil && state.Runtime == "frontend" {
		fmt.Printf("⚠️  Dev session already active for %s\n", deployment)
		fmt.Println("   Run 'kindling dev --stop -d " + deployment + "' to stop it first")
		return nil
	}

	// Resolve source directory
	srcDir := localSourceDirForDeployment(deployment)
	if srcDir == "" || !isFrontendProject(srcDir) {
		return fmt.Errorf("no frontend project found for %s — expected a directory with package.json and a build script", deployment)
	}

	step("🖥️", "Starting local dev mode for "+deployment)
	step("📂", "Source: "+srcDir)
	fmt.Println()

	// Discover API services this frontend talks to
	apiServices := discoverAPIServices(deployment, namespace)

	var pfCmds []*exec.Cmd
	var pfPids []int

	if len(apiServices) == 0 {
		fmt.Println("  No API service URLs found in deployment env vars.")
		fmt.Println("  You can manually port-forward services:")
		fmt.Println("    kubectl port-forward svc/<service-name> <local-port>:<remote-port> --context kind-dev")
		fmt.Println()
	} else {
		// Port-forward each discovered API service
		fmt.Println("  📡 Port-forwarding API services to localhost:")
		fmt.Println()
		for _, svc := range apiServices {
			localPort := svc.Port
			if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort)); err != nil {
				ln2, err2 := net.Listen("tcp", "127.0.0.1:0")
				if err2 != nil {
					fmt.Printf("  ⚠️  Cannot find free port for %s: %v\n", svc.Host, err2)
					continue
				}
				localPort = ln2.Addr().(*net.TCPAddr).Port
				ln2.Close()
			} else {
				ln.Close()
			}

			fmt.Printf("     %s → localhost:%d  (%s)\n", svc.Host, localPort, svc.EnvVar)
			pfCmd := exec.Command("kubectl", "port-forward",
				fmt.Sprintf("svc/%s", svc.Host),
				fmt.Sprintf("%d:%d", localPort, svc.Port),
				"-n", namespace,
				"--context", kindContext())
			pfCmd.Stdout = nil
			pfCmd.Stderr = nil
			if err := pfCmd.Start(); err != nil {
				fmt.Printf("  ⚠️  Failed to port-forward %s: %v\n", svc.Host, err)
				continue
			}
			pfCmds = append(pfCmds, pfCmd)
			pfPids = append(pfPids, pfCmd.Process.Pid)
		}
	}

	// ── OAuth detection ──────────────────────────────────────────
	var tunnelURL string
	var tunnelPid int
	devPort := detectDevServerPort(srcDir)

	if needsOAuth := detectFrontendOAuth(srcDir); needsOAuth {
		fmt.Println()
		step("🔐", "OAuth/OIDC detected in frontend source")
		step("ℹ️", fmt.Sprintf("Starting HTTPS tunnel → localhost:%d for OAuth callbacks", devPort))

		provider := core.DetectTunnelProvider()
		if provider == "" {
			step("⚠️", "No tunnel provider found (install cloudflared or ngrok)")
			fmt.Println("  OAuth callbacks won't work without a public URL.")
			fmt.Println("  Install: brew install cloudflare/cloudflare/cloudflared")
			fmt.Println()
		} else {
			switch provider {
			case "cloudflared":
				result, err := core.StartCloudflaredTunnel(devPort, 30, false)
				if err != nil {
					step("⚠️", fmt.Sprintf("Tunnel failed: %v", err))
				} else {
					tunnelURL = result.PublicURL
					tunnelPid = result.PID
				}
			case "ngrok":
				result, err := core.StartNgrokTunnel(devPort)
				if err != nil {
					step("⚠️", fmt.Sprintf("Tunnel failed: %v", err))
				} else {
					tunnelURL = result.PublicURL
					tunnelPid = result.PID
				}
			}
			if tunnelURL != "" {
				step("🌐", fmt.Sprintf("Public URL: %s", tunnelURL))

				// Patch the dev server config to allow the tunnel hostname
				if patched := patchDevServerAllowedHost(srcDir, tunnelURL); patched {
					step("✅", "Patched dev server config to allow tunnel host")
				}

				fmt.Println()
				fmt.Println("  Set this as your OAuth callback/redirect URL:")
				fmt.Printf("     %s/auth/callback\n", tunnelURL)
				fmt.Printf("     %s/api/auth/callback\n", tunnelURL)
				fmt.Println()
				fmt.Println("  Environment variables for your dev server:")
				fmt.Printf("     NEXTAUTH_URL=%s\n", tunnelURL)
				fmt.Printf("     REDIRECT_URI=%s/auth/callback\n", tunnelURL)
			}
		}
	}

	// Save state for --stop
	state := debugState{
		Deployment: deployment,
		Namespace:  namespace,
		Runtime:    "frontend",
		OrigCmd:    "frontend-dev-mode",
	}
	if len(pfPids) > 0 {
		state.PfPid = pfPids[0]
	}
	if tunnelPid > 0 {
		state.TunnelPid = tunnelPid
	}
	// Label the deployment so it shows up in `kindling status`
	labelSession(deployment, namespace, "dev", "frontend")

	// ── Launch the frontend dev server ─────────────────────────
	pm := detectPackageManager(srcDir)
	devCmd := exec.Command(pm, "run", "dev")
	devCmd.Dir = srcDir
	devCmd.Stdout = os.Stdout
	devCmd.Stderr = os.Stderr
	// Start in its own process group so we can kill the entire tree
	// (npm spawns vite as a child process).
	devCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Pass through the current environment so Node/npm/pnpm are found,
	// plus any tunnel URL the user might reference.
	devCmd.Env = os.Environ()
	if tunnelURL != "" {
		devCmd.Env = append(devCmd.Env, "KINDLING_TUNNEL_URL="+tunnelURL)
	}

	fmt.Printf("\n  🚀 Starting dev server: %s run dev  (in %s)\n\n", pm, srcDir)
	if err := devCmd.Start(); err != nil {
		return fmt.Errorf("failed to start dev server: %w", err)
	}

	// Save state with dev server PID so --stop can clean up
	state.DevPid = devCmd.Process.Pid
	state.SrcDir = srcDir
	saveDebugState(state)

	fmt.Printf("  🛑 Press Ctrl-C or run 'kindling dev --stop -d %s' to stop\n\n", deployment)

	// ── Wait for either Ctrl-C or the dev server to exit ────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	devDone := make(chan error, 1)
	go func() { devDone <- devCmd.Wait() }()

	select {
	case <-sigCh:
		// User hit Ctrl-C — kill the dev server process group
		fmt.Println("\n  Stopping...")
		if devCmd.Process != nil {
			// Kill the entire process group (npm + vite child)
			syscall.Kill(-devCmd.Process.Pid, syscall.SIGTERM)
			// Give it a moment to exit gracefully
			go func() {
				<-devDone
			}()
		}
	case err := <-devDone:
		// Dev server exited on its own
		if err != nil {
			fmt.Printf("\n  ⚠️  Dev server exited: %v\n", err)
		} else {
			fmt.Println("\n  Dev server exited.")
		}
	}

	// ── Cleanup ────────────────────────────────────────────────
	for _, cmd := range pfCmds {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	if tunnelPid > 0 {
		if proc, err := os.FindProcess(tunnelPid); err == nil {
			proc.Kill()
		}
	}
	if tunnelURL != "" {
		restoreDevServerConfig(srcDir)
	}
	unlabelSession(deployment, namespace)
	clearDebugState(deployment)
	step("✅", "Dev mode stopped")
	return nil
}

// stopDev stops a running dev session.
func stopDev(deployment string) error {
	state, err := loadDebugState(deployment)
	if err != nil {
		step("ℹ️", "No active dev session found for "+deployment)
		return nil
	}
	if state.Runtime != "frontend" {
		step("ℹ️", "Active session for "+deployment+" is a debug session, not dev mode")
		fmt.Println("   Use 'kindling debug --stop -d " + deployment + "' instead")
		return nil
	}

	// Kill the dev server process group (npm + child vite/next/etc.)
	if state.DevPid > 0 {
		// Try killing the process group first, fall back to single process
		if err := syscall.Kill(-state.DevPid, syscall.SIGTERM); err != nil {
			if proc, err := os.FindProcess(state.DevPid); err == nil {
				proc.Signal(syscall.SIGTERM)
			}
		}
	}
	if state.PfPid > 0 {
		if proc, err := os.FindProcess(state.PfPid); err == nil {
			proc.Kill()
		}
	}
	if state.TunnelPid > 0 {
		if proc, err := os.FindProcess(state.TunnelPid); err == nil {
			proc.Kill()
		}
	}

	// Restore dev server config if tunnel was active
	srcDir := state.SrcDir
	if srcDir == "" {
		srcDir = localSourceDirForDeployment(deployment)
	}
	if srcDir != "" {
		restoreDevServerConfig(srcDir)
	}

	unlabelSession(deployment, state.Namespace)
	clearDebugState(deployment)
	step("✅", "Dev mode stopped")
	return nil
}

// ── Frontend detection helpers ──────────────────────────────────

// isFrontendDeployment checks if the container command looks like a static
// file server (nginx, httpd, caddy, serve, etc.) rather than an app server.
func isFrontendDeployment(cmdline string) bool {
	if cmdline == "" {
		return false
	}
	fields := strings.Fields(cmdline)
	staticServers := []string{"nginx", "httpd", "apache2", "caddy", "serve", "http-server"}
	for _, f := range fields {
		base := filepath.Base(f)
		for _, s := range staticServers {
			if base == s || strings.HasPrefix(base, s+":") {
				return true
			}
		}
	}
	return false
}

// localSourceDirForDeployment returns the absolute local source directory for a
// deployment.  Checks the deployment-suffix subdirectory first (monorepo), then root.
func localSourceDirForDeployment(deployment string) string {
	root := "."
	if projectDir != "" {
		root = projectDir
	}
	// Resolve to absolute so commands work regardless of cwd.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	parts := strings.Split(deployment, "-")
	if len(parts) > 0 {
		subDir := parts[len(parts)-1]
		candidate := filepath.Join(root, subDir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return root
}

// serviceURL represents a cluster service URL extracted from deployment env vars.
type serviceURL struct {
	EnvVar string // e.g. "GATEWAY_URL"
	Host   string // e.g. "jeff-vincent-gateway"
	Port   int    // e.g. 9090
}

// discoverAPIServices reads the deployment's environment variables to find
// references to other in-cluster services (e.g. GATEWAY_URL=http://svc:9090).
func discoverAPIServices(deployment, namespace string) []serviceURL {
	output, err := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"-o", "jsonpath={range .spec.template.spec.containers[0].env[*]}{.name}={.value}{'\\n'}{end}")
	if err != nil || strings.TrimSpace(output) == "" {
		return nil
	}

	var services []serviceURL
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			continue
		}
		envVar := line[:eqIdx]
		value := line[eqIdx+1:]

		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			continue
		}
		urlBody := value
		for _, prefix := range []string{"https://", "http://"} {
			urlBody = strings.TrimPrefix(urlBody, prefix)
		}
		if slashIdx := strings.Index(urlBody, "/"); slashIdx >= 0 {
			urlBody = urlBody[:slashIdx]
		}

		host := urlBody
		port := 80
		if colonIdx := strings.LastIndex(urlBody, ":"); colonIdx >= 0 {
			host = urlBody[:colonIdx]
			if p, err := fmt.Sscanf(urlBody[colonIdx+1:], "%d", &port); p == 1 && err == nil {
				// port parsed
			}
		}
		services = append(services, serviceURL{EnvVar: envVar, Host: host, Port: port})
	}
	return services
}

// ── OAuth and tunnel helpers ────────────────────────────────────

// detectFrontendOAuth scans the frontend source directory for OAuth/OIDC patterns.
func detectFrontendOAuth(srcDir string) bool {
	patterns := []string{
		"auth0", "okta", "next-auth", "nextauth", "@auth0",
		"passport-oauth", "passport-google", "passport-github",
		"clerk", "supabase/auth", "firebase/auth", "keycloak",
		"oauth2", "openid-connect", "oidc",
		"redirect_uri", "REDIRECT_URI", "CALLBACK_URL",
		"NEXTAUTH_URL", "NEXTAUTH_SECRET",
		"GOOGLE_CLIENT_ID", "GITHUB_CLIENT_ID",
		"AUTH0_CLIENT_ID", "AUTH0_DOMAIN",
	}

	// Scan package.json dependencies
	if data, err := os.ReadFile(filepath.Join(srcDir, "package.json")); err == nil {
		lower := strings.ToLower(string(data))
		for _, p := range patterns {
			if strings.Contains(lower, strings.ToLower(p)) {
				return true
			}
		}
	}

	// Scan .env files for OAuth env vars
	for _, envFile := range []string{".env", ".env.local", ".env.development"} {
		if data, err := os.ReadFile(filepath.Join(srcDir, envFile)); err == nil {
			for _, p := range patterns {
				if strings.Contains(string(data), p) {
					return true
				}
			}
		}
	}

	// Quick scan of src/ directory for import patterns
	srcSubDir := filepath.Join(srcDir, "src")
	if entries, err := os.ReadDir(srcSubDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := filepath.Ext(e.Name())
			if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
				if data, err := os.ReadFile(filepath.Join(srcSubDir, e.Name())); err == nil {
					lower := strings.ToLower(string(data))
					for _, p := range patterns {
						if strings.Contains(lower, strings.ToLower(p)) {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

// detectDevServerPort returns the default dev server port for the frontend framework.
func detectDevServerPort(srcDir string) int {
	// Vite → 5173
	for _, f := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts"} {
		if _, err := os.Stat(filepath.Join(srcDir, f)); err == nil {
			if data, err := os.ReadFile(filepath.Join(srcDir, f)); err == nil {
				content := string(data)
				if idx := strings.Index(content, "port:"); idx >= 0 {
					rest := strings.TrimSpace(content[idx+5:])
					var port int
					if n, _ := fmt.Sscanf(rest, "%d", &port); n == 1 && port > 0 {
						return port
					}
				}
			}
			return 5173
		}
	}
	// Next.js → 3000
	for _, f := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		if _, err := os.Stat(filepath.Join(srcDir, f)); err == nil {
			return 3000
		}
	}
	// Angular → 4200
	if _, err := os.Stat(filepath.Join(srcDir, "angular.json")); err == nil {
		return 4200
	}
	// SvelteKit → 5173
	if _, err := os.Stat(filepath.Join(srcDir, "svelte.config.js")); err == nil {
		return 5173
	}
	// Create React App / generic → 3000
	return 3000
}

// patchDevServerAllowedHost patches the frontend dev server config to allow
// the tunnel hostname. Backs up the original for restore on stop.
func patchDevServerAllowedHost(srcDir, tunnelURL string) bool {
	hostname := strings.TrimPrefix(tunnelURL, "https://")
	hostname = strings.TrimPrefix(hostname, "http://")
	hostname = strings.TrimSuffix(hostname, "/")

	// Vite — inject allowedHosts into server block
	for _, f := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts"} {
		configPath := filepath.Join(srcDir, f)
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		content := string(data)

		if strings.Contains(content, "allowedHosts") {
			return false
		}

		backupPath := configPath + ".kindling-backup"
		os.WriteFile(backupPath, data, 0644)

		if idx := strings.Index(content, "server:"); idx >= 0 {
			braceIdx := strings.Index(content[idx:], "{")
			if braceIdx >= 0 {
				insertAt := idx + braceIdx + 1
				patch := fmt.Sprintf("\n      allowedHosts: [\"%s\"],", hostname)
				patched := content[:insertAt] + patch + content[insertAt:]
				os.WriteFile(configPath, []byte(patched), 0644)
				return true
			}
		}

		if lastBrace := strings.LastIndex(content, "})"); lastBrace >= 0 {
			patch := fmt.Sprintf("  server: {\n    allowedHosts: [\"%s\"],\n  },\n", hostname)
			patched := content[:lastBrace] + patch + content[lastBrace:]
			os.WriteFile(configPath, []byte(patched), 0644)
			return true
		}
		return false
	}

	// Next.js — set allowedDevOrigins
	for _, f := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		configPath := filepath.Join(srcDir, f)
		data, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		content := string(data)

		if strings.Contains(content, "allowedDevOrigins") {
			return false
		}

		backupPath := configPath + ".kindling-backup"
		os.WriteFile(backupPath, data, 0644)

		for _, pattern := range []string{"= {", "({", "= defineConfig({"} {
			if idx := strings.Index(content, pattern); idx >= 0 {
				insertAt := idx + len(pattern)
				patch := fmt.Sprintf("\n  allowedDevOrigins: [\"https://%s\"],", hostname)
				patched := content[:insertAt] + patch + content[insertAt:]
				os.WriteFile(configPath, []byte(patched), 0644)
				return true
			}
		}
		return false
	}

	return false
}

// restoreDevServerConfig restores any dev server config files patched by
// patchDevServerAllowedHost.
func restoreDevServerConfig(srcDir string) {
	configs := []string{
		"vite.config.ts", "vite.config.js", "vite.config.mts",
		"next.config.js", "next.config.mjs", "next.config.ts",
	}
	for _, f := range configs {
		backupPath := filepath.Join(srcDir, f+".kindling-backup")
		configPath := filepath.Join(srcDir, f)
		if data, err := os.ReadFile(backupPath); err == nil {
			os.WriteFile(configPath, data, 0644)
			os.Remove(backupPath)
			step("🔧", fmt.Sprintf("Restored %s", f))
		}
	}
}
