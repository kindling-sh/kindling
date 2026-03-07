package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// ════════════════════════════════════════════════════════════════════
// kindling debug — attach a debugger to a running deployment
//
// This command:
// 1. Detects the runtime (Node/Go/Python/Ruby) from the running pod
// 2. Patches the deployment command to inject the debug wrapper
// 3. Port-forwards the debug port to localhost
// 4. Writes a VS Code launch.json attach config
// 5. Cleans up on --stop or Ctrl-C
// ════════════════════════════════════════════════════════════════════

// debugProfile holds per-runtime debug configuration.
type debugProfile struct {
	Name       string                 // "Node.js", "Go (Delve)", "Python (debugpy)", "Ruby (rdbg)"
	Port       int                    // conventional debug port
	WrapFmt    string                 // fmt template to wrap the original command — %s = original cmd
	LaunchType string                 // VS Code launch.json type
	Request    string                 // "attach"
	Extra      map[string]interface{} // additional launch.json fields
	InstallCmd string                 // command to install debug tools (empty if built-in)
}

var debugProfiles = map[string]debugProfile{
	"node": {
		Name: "Node.js", Port: 9229,
		WrapFmt:    `node --inspect=0.0.0.0:9229 %s`,
		LaunchType: "node", Request: "attach",
		Extra: map[string]interface{}{
			"address":    "localhost",
			"port":       9229,
			"restart":    true,
			"localRoot":  "${workspaceFolder}",
			"remoteRoot": "/app",
		},
	},
	"deno": {
		Name: "Deno", Port: 9229,
		WrapFmt:    `deno run --inspect=0.0.0.0:9229 %s`,
		LaunchType: "node", Request: "attach",
		Extra: map[string]interface{}{
			"address": "localhost",
			"port":    9229,
			"restart": true,
		},
	},
	"bun": {
		Name: "Bun", Port: 6499,
		WrapFmt:    `bun --inspect=0.0.0.0:6499 %s`,
		LaunchType: "bun", Request: "attach",
		Extra: map[string]interface{}{
			"url": "ws://localhost:6499",
		},
	},
	"python": {
		Name: "Python (debugpy)", Port: 5678,
		WrapFmt:    `python -m debugpy --listen 0.0.0.0:5678 --wait-for-client %s`,
		LaunchType: "debugpy", Request: "attach",
		InstallCmd: "pip install debugpy",
		Extra: map[string]interface{}{
			"connect": map[string]interface{}{
				"host": "localhost",
				"port": 5678,
			},
			"pathMappings": []map[string]string{
				{"localRoot": "${workspaceFolder}", "remoteRoot": "/app"},
			},
			"justMyCode": false,
		},
	},
	"ruby": {
		Name: "Ruby (rdbg)", Port: 12345,
		WrapFmt:    `rdbg --open --host 0.0.0.0 --port 12345 -- ruby %s`,
		LaunchType: "rdbg", Request: "attach",
		InstallCmd: "gem install debug",
		Extra: map[string]interface{}{
			"debugPort":  "12345",
			"localfsMap": "/app:${workspaceFolder}",
		},
	},
	"go": {
		Name: "Go (Delve)", Port: 2345,
		WrapFmt:    `dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --continue %s`,
		LaunchType: "go", Request: "attach",
		InstallCmd: "go install github.com/go-delve/delve/cmd/dlv@latest",
		Extra: map[string]interface{}{
			"mode": "remote",
			"host": "localhost",
			"port": 2345,
			"substitutePath": []map[string]string{
				{"from": "${workspaceFolder}", "to": "/app"},
			},
		},
	},
}

// debugState tracks an active debug session.
type debugState struct {
	Deployment string `json:"deployment"`
	Namespace  string `json:"namespace"`
	Runtime    string `json:"runtime"`
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	OrigCmd    string `json:"origCmd"`
	HadCommand bool   `json:"hadCommand"` // true if deployment spec had .command before debug
	HadProbes  bool   `json:"hadProbes"`  // true if probes were removed during debug
	PfPid      int    `json:"pfPid"`
	TunnelPid  int    `json:"tunnelPid,omitempty"` // HTTPS tunnel PID for OAuth
	DevPid     int    `json:"devPid,omitempty"`    // Frontend dev server PID
	SrcDir     string `json:"srcDir,omitempty"`    // Absolute path to frontend source
}

// debugStateFile returns the path to the debug state file for a deployment.
// Uses ~/.kindling/ so state is always findable regardless of which directory
// the user runs from (unlike kindlingDir() which depends on git rev-parse).
func debugStateFile(deployment string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(kindlingDir(), fmt.Sprintf("debug-%s.json", deployment))
	}
	dir := filepath.Join(home, ".kindling")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, fmt.Sprintf("debug-%s.json", deployment))
}

func saveDebugState(state debugState) error {
	data, _ := json.MarshalIndent(state, "", "  ")
	return os.WriteFile(debugStateFile(state.Deployment), data, 0644)
}

func loadDebugState(deployment string) (*debugState, error) {
	data, err := os.ReadFile(debugStateFile(deployment))
	if err != nil {
		return nil, err
	}
	var state debugState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func clearDebugState(deployment string) {
	os.Remove(debugStateFile(deployment))
}

// ── CLI command ─────────────────────────────────────────────────

var (
	debugDeployment string
	debugNamespace  string
	debugStop       bool
	debugPort       int
	debugNoLaunch   bool
)

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Attach a debugger to a running deployment",
	Long: `Automatically detects the runtime, patches the deployment command
to enable debugging, port-forwards the debug port, and generates a
VS Code launch.json attach configuration.

  kindling debug -d my-api           # start debugging
  kindling debug --stop -d my-api    # stop and restore original command

Works great with 'kindling sync' — the debugger reconnects automatically
when files are synced and the process restarts.`,
	RunE: runDebug,
}

func init() {
	debugCmd.Flags().StringVarP(&debugDeployment, "deployment", "d", "", "Deployment to debug (required)")
	debugCmd.Flags().StringVarP(&debugNamespace, "namespace", "n", "default", "Kubernetes namespace")
	debugCmd.Flags().BoolVar(&debugStop, "stop", false, "Stop debug session and restore original command")
	debugCmd.Flags().IntVar(&debugPort, "port", 0, "Override the local debug port (default: auto-detect)")
	debugCmd.Flags().BoolVar(&debugNoLaunch, "no-launch", false, "Skip writing .vscode/launch.json")
	debugCmd.MarkFlagRequired("deployment")
	rootCmd.AddCommand(debugCmd)
}

func runDebug(cmd *cobra.Command, args []string) error {
	if debugStop {
		return stopDebug(debugDeployment, debugNamespace)
	}
	return startDebug(debugDeployment, debugNamespace)
}

// ── Start debug session ─────────────────────────────────────────

func startDebug(deployment, namespace string) error {
	// Check if already debugging
	if state, err := loadDebugState(deployment); err == nil {
		fmt.Printf("⚠️  Debug session already active for %s (port %d)\n", deployment, state.LocalPort)
		fmt.Println("   Run 'kindling debug --stop -d " + deployment + "' to stop it first")
		// Print the "Debugger ready" marker so VS Code's preLaunchTask
		// problem matcher can complete — otherwise F5 hangs silently
		// waiting for the pattern that will never come.
		fmt.Printf("  🔧 Debugger ready on localhost:%d\n", state.LocalPort)
		return nil
	}

	step("🔍", "Detecting runtime for "+deployment)

	// Find the pod
	pod, err := findPodForDeployment(deployment, namespace)
	if err != nil {
		return fmt.Errorf("cannot find pod for %s: %w", deployment, err)
	}

	// Detect container name
	container := containerNameForDeployment(deployment, namespace, "")

	// Detect runtime
	profile, cmdline := detectRuntime(pod, namespace, container)

	// ── Frontend detection (early exit) ─────────────────────────
	// Frontend deployments (nginx/caddy serving a built SPA) should use
	// `kindling dev` instead of `kindling debug`.
	srcDir := localSourceDirForDeployment(deployment)
	if isFrontendDeployment(cmdline) && srcDir != "" && isFrontendProject(srcDir) {
		step("🖥️", "This is a frontend deployment (static file server)")
		fmt.Printf("\n  Use 'kindling dev -d %s' to run your frontend dev server locally\n", deployment)
		fmt.Println("  with API services port-forwarded from the cluster.")
		fmt.Println()
		return nil
	}

	// Map runtime to debug profile
	debugProf, runtimeKey := matchDebugProfile(profile.Name, cmdline)

	// If process-based detection fails (common for compiled languages where the
	// binary has a custom name like "gateway" instead of "go"), fall back to
	// inspecting local source files for language markers (go.mod, package.json, etc.)
	if debugProf == nil {
		localRoot := "."
		if projectDir != "" {
			localRoot = projectDir
		}
		// In monorepo setups, prefer the subdirectory matching the deployment
		// suffix (e.g. "jeff-vincent-gateway" → check "gateway/"). This
		// prevents a root-level package.json from overriding a service-specific
		// language marker like go.mod.
		parts := strings.Split(deployment, "-")
		if len(parts) > 0 {
			subDir := parts[len(parts)-1]
			candidate := filepath.Join(localRoot, subDir)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				if lang := detectLanguageFromSource(candidate); lang != "" {
					debugProf, runtimeKey = matchDebugProfile(lang, cmdline)
				}
			}
		}
		// Fall back to root directory (single-service repos)
		if debugProf == nil {
			if lang := detectLanguageFromSource(localRoot); lang != "" {
				debugProf, runtimeKey = matchDebugProfile(lang, cmdline)
			}
		}
	}

	if debugProf == nil {
		return fmt.Errorf("unsupported runtime %q for debugging — supported: node, python, go, ruby", profile.Name)
	}

	step("🎯", fmt.Sprintf("Detected runtime: %s", debugProf.Name))

	// Detect the container's working directory for path mappings.
	// This lets VS Code map local breakpoints to the correct remote file paths.
	remoteRoot := "/app" // sensible default
	if wd, err := runCapture("kubectl", "exec", pod, "-n", namespace,
		"--context", kindContext(), "-c", container, "--", "pwd"); err == nil {
		if trimmed := strings.TrimSpace(wd); trimmed != "" {
			remoteRoot = trimmed
		}
	}
	step("📂", fmt.Sprintf("Remote working directory: %s", remoteRoot))

	// Check if the deployment spec has an explicit command override.
	// If not, the container uses the image's CMD/ENTRYPOINT and on stop
	// we must remove the command field entirely, not replace it.
	specCmd, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"-o", "jsonpath={.spec.template.spec.containers[0].command}")
	hadCommand := strings.TrimSpace(specCmd) != "" && specCmd != "[]"

	// Read the original command before patching
	var origCmd string
	if hadCommand {
		origCmd = readContainerCommand(deployment, pod, namespace, container)
	} else {
		// No spec command — read from /proc/1/cmdline or image CMD
		origCmd = readContainerCommand(deployment, pod, namespace, container)
	}
	if origCmd == "" {
		return fmt.Errorf("cannot determine container command for %s", deployment)
	}

	// If already wrapped by kindling sync, extract the inner command
	if strings.Contains(origCmd, ".kindling-sync-wrapper") {
		inner := extractInnerCommand(origCmd)
		if inner != "" {
			origCmd = inner
		}
	}

	// If already wrapped by a previous debug session (e.g. a stale debugpy
	// inject that was never cleaned up), strip the debug wrapper to get the
	// original application command. Prevents double-wrapping like:
	//   python -m debugpy --listen ... -m debugpy --listen ... -m uvicorn ...
	if stripped := stripDebugWrapper(origCmd); stripped != origCmd {
		if stripped != "" {
			origCmd = stripped
		} else {
			// Go's wait-loop wrapper destroys the original command — it can't
			// be recovered from the string. Rollback to the pre-debug revision,
			// wait for rollout, then re-read the original command.
			step("⚠️", "Stale debug wrapper detected — rolling back deployment")
			_ = run("kubectl", "rollout", "undo", fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace, "--context", kindContext())
			_ = run("kubectl", "rollout", "status", fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace, "--context", kindContext(), "--timeout=60s")
			time.Sleep(2 * time.Second)
			clearDebugState(deployment)

			newPod, err := findPodForDeployment(deployment, namespace)
			if err != nil {
				return fmt.Errorf("cannot find pod after rollback: %w", err)
			}
			origCmd = readContainerCommand(deployment, newPod, namespace, container)
			if origCmd == "" {
				return fmt.Errorf("cannot determine container command after rollback")
			}
			// Re-check for spec command after rollback
			specCmd, _ = runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace, "--context", kindContext(),
				"-o", "jsonpath={.spec.template.spec.containers[0].command}")
			hadCommand = strings.TrimSpace(specCmd) != "" && specCmd != "[]"
		}
	}

	step("📝", fmt.Sprintf("Original command: %s", origCmd))
	if !hadCommand {
		step("ℹ️", "No command override in deployment spec — will remove on stop")
	}

	// Build the debug-wrapped command
	// For Node: strip "node" from origCmd, inject --inspect
	// For others: wrap the full command
	debugCmd := buildDebugCommand(debugProf, runtimeKey, origCmd)
	step("🔧", fmt.Sprintf("Debug command: %s", debugCmd))

	// Patch the deployment — also disable health probes so breakpoints
	// don't cause Kubernetes to kill the pod.

	// Check if probes exist
	livenessRaw, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"-o", "jsonpath={.spec.template.spec.containers[0].livenessProbe}")
	readinessRaw, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"-o", "jsonpath={.spec.template.spec.containers[0].readinessProbe}")
	hadProbes := strings.TrimSpace(livenessRaw) != "" || strings.TrimSpace(readinessRaw) != ""

	// Use JSON patch to set the command and remove probes atomically.
	// Check if annotations key exists — if not, create it first.
	var patchOps []string
	annotationsRaw, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"-o", "jsonpath={.spec.template.metadata.annotations}")
	if strings.TrimSpace(annotationsRaw) == "" {
		patchOps = append(patchOps,
			`{"op":"add","path":"/spec/template/metadata/annotations","value":{"kindling.dev/debug":"true"}}`)
	} else {
		patchOps = append(patchOps,
			`{"op":"add","path":"/spec/template/metadata/annotations/kindling.dev~1debug","value":"true"}`)
	}
	patchOps = append(patchOps,
		fmt.Sprintf(`{"op":"add","path":"/spec/template/spec/containers/0/command","value":["sh","-c","%s"]}`,
			strings.ReplaceAll(debugCmd, `"`, `\"`)),
	)
	if strings.TrimSpace(livenessRaw) != "" {
		patchOps = append(patchOps, `{"op":"remove","path":"/spec/template/spec/containers/0/livenessProbe"}`)
	}
	if strings.TrimSpace(readinessRaw) != "" {
		patchOps = append(patchOps, `{"op":"remove","path":"/spec/template/spec/containers/0/readinessProbe"}`)
	}
	patch := "[" + strings.Join(patchOps, ",") + "]"

	step("🔧", "Patching deployment with debug command")
	if hadProbes {
		step("🔧", "Disabling health probes for debug session")
	}
	if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"--type=json", "-p", patch); err != nil {
		return fmt.Errorf("failed to patch deployment: %w", err)
	}

	// ── Save preliminary state immediately after patching ──────
	// This ensures `kindling debug --stop` can restore the deployment
	// even if a later step (e.g. Go inject, rollout wait, or broken pipe)
	// kills this process before we finish.
	state := debugState{
		Deployment: deployment,
		Namespace:  namespace,
		Runtime:    runtimeKey,
		RemotePort: debugProf.Port,
		OrigCmd:    origCmd,
		HadCommand: hadCommand,
		HadProbes:  hadProbes,
	}
	if err := saveDebugState(state); err != nil {
		fmt.Printf("⚠️  Could not save debug state: %v\n", err)
	}

	// Wait for rollout
	step("⏳", "Waiting for debugger pod to start...")
	_ = run("kubectl", "rollout", "status", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(), "--timeout=120s")
	time.Sleep(3 * time.Second)

	// Find the new pod
	newPod, err := findPodForDeployment(deployment, namespace)
	if err != nil {
		return fmt.Errorf("pod not ready after patching: %w", err)
	}
	step("✅", "Debug pod ready: "+newPod)

	// For Go: inject locally-built debug binary + Delve into the container.
	// The patched command is waiting for /tmp/dlv to appear.
	if runtimeKey == "go" {
		if err := injectGoDebugTools(deployment, newPod, namespace, container); err != nil {
			// Inject failed — automatically restore the deployment so it
			// doesn't stay stuck with the Delve-waiting command.
			step("⚠️", "Inject failed, restoring deployment...")
			stopDebug(deployment, namespace)
			return fmt.Errorf("failed to inject Go debug tools: %w", err)
		}
		// Brief wait for dlv to start up
		time.Sleep(2 * time.Second)
	}

	// Determine local port
	localPort := debugProf.Port
	if debugPort > 0 {
		localPort = debugPort
	}
	// Check if the port is free, otherwise pick a free one
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort)); err != nil {
		// Port in use — find a free one
		ln2, err2 := net.Listen("tcp", "127.0.0.1:0")
		if err2 != nil {
			return fmt.Errorf("cannot find free port: %w", err2)
		}
		localPort = ln2.Addr().(*net.TCPAddr).Port
		ln2.Close()
		step("ℹ️", fmt.Sprintf("Port %d in use, using %d instead", debugProf.Port, localPort))
	} else {
		ln.Close()
	}

	// Start port-forward in background
	step("🔗", fmt.Sprintf("Port-forwarding localhost:%d → %s:%d", localPort, deployment, debugProf.Port))
	pfCmd := exec.Command("kubectl", "port-forward",
		fmt.Sprintf("deployment/%s", deployment),
		fmt.Sprintf("%d:%d", localPort, debugProf.Port),
		"-n", namespace,
		"--context", kindContext())
	pfCmd.Stdout = os.Stdout
	pfCmd.Stderr = os.Stderr
	if err := pfCmd.Start(); err != nil {
		return fmt.Errorf("port-forward failed to start: %w", err)
	}

	// Update state with port-forward details now that everything is running
	state.LocalPort = localPort
	state.PfPid = pfCmd.Process.Pid
	if err := saveDebugState(state); err != nil {
		fmt.Printf("⚠️  Could not save debug state: %v\n", err)
	}

	// Label the deployment so it shows up in `kindling status`
	labelSession(deployment, namespace, "debug", runtimeKey)

	// Detect whether the service source lives in a subdirectory of the
	// workspace (common in monorepo / multi-service repos like
	// kindling-debug-demo/orders/).  This ensures pathMappings use
	// ${workspaceFolder}/orders instead of ${workspaceFolder}.
	sourceSubdir := ""
	if !debugNoLaunch {
		sourceSubdir = detectLocalSourceDir(deployment, newPod, namespace, container, remoteRoot)
		if sourceSubdir != "" {
			step("📂", fmt.Sprintf("Local source subdirectory: %s/", sourceSubdir))
		}
	}

	// Write launch.json
	if !debugNoLaunch {
		writeLaunchConfig(deployment, debugProf, localPort, remoteRoot, sourceSubdir)
	}

	fmt.Println()
	fmt.Printf("  🔧 Debugger ready on localhost:%d\n", localPort)
	fmt.Printf("  📎 Press F5 in VS Code to attach\n")
	fmt.Printf("  🛑 Press Ctrl-C or run 'kindling debug --stop -d %s' to stop\n", deployment)
	fmt.Println()

	// Wait for Ctrl-C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\n  Stopping debug session...")
	pfCmd.Process.Kill()
	return stopDebug(deployment, namespace)
}

// ── Stop debug session ──────────────────────────────────────────

func stopDebug(deployment, namespace string) error {
	state, err := loadDebugState(deployment)
	if err != nil {
		// No saved state — try to clean up the annotation anyway
		step("ℹ️", "No active debug session found for "+deployment)
		return nil
	}

	// Kill port-forward process
	if state.PfPid > 0 {
		if proc, err := os.FindProcess(state.PfPid); err == nil {
			proc.Kill()
		}
	}

	// Frontend dev mode — delegate to `kindling dev --stop`
	if state.Runtime == "frontend" {
		return stopDev(deployment)
	}

	// Restore original command and probes
	step("🔧", "Restoring original command for "+deployment)

	if state.HadProbes {
		// Probes were removed — the cleanest restore is to undo the rollout,
		// which reverts to the pre-debug revision (command + probes + annotation).
		step("🔧", "Restoring deployment via rollout undo")
		if err := run("kubectl", "rollout", "undo", fmt.Sprintf("deployment/%s", deployment),
			"-n", namespace, "--context", kindContext()); err != nil {
			fmt.Printf("⚠️  Failed to rollout undo: %v\n", err)
		}
	} else if !state.HadCommand {
		// The deployment originally had no command override — remove it entirely
		// so the image's CMD/ENTRYPOINT takes effect again.
		specCmd, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
			"-n", namespace, "--context", kindContext(),
			"-o", "jsonpath={.spec.template.spec.containers[0].command}")
		if strings.TrimSpace(specCmd) != "" && specCmd != "[]" {
			step("ℹ️", "Removing command override (restoring image defaults)")
			var ops []string
			ops = append(ops, `{"op":"remove","path":"/spec/template/spec/containers/0/command"}`)
			ops = append(ops, `{"op":"remove","path":"/spec/template/metadata/annotations/kindling.dev~1debug"}`)
			p := "[" + strings.Join(ops, ",") + "]"
			if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace, "--context", kindContext(),
				"--type=json", "-p", p); err != nil {
				fmt.Printf("⚠️  Failed to remove command: %v\n", err)
			}
		} else {
			step("ℹ️", "No command override present — already using image defaults")
		}
	} else {
		origCmd := state.OrigCmd
		cName := containerNameForDeployment(deployment, namespace, "")
		patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kindling.dev/debug":null}},"spec":{"containers":[{"name":"%s","command":["sh","-c","%s"]}]}}}}`,
			cName, strings.ReplaceAll(origCmd, `"`, `\"`))
		if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
			"-n", namespace, "--context", kindContext(),
			"--type=strategic", "-p", patch); err != nil {
			fmt.Printf("⚠️  Failed to restore command: %v\n", err)
		}
	}

	// Wait for rollout
	step("⏳", "Waiting for pod to restart with original command...")
	_ = run("kubectl", "rollout", "status", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(), "--timeout=60s")

	unlabelSession(deployment, namespace)
	clearDebugState(deployment)
	step("✅", "Debug session stopped — deployment restored")
	return nil
}

// ── Helper functions ────────────────────────────────────────────

// matchDebugProfile maps a detected runtime name to a debug profile.
// Uses a deterministic priority order to avoid random map iteration.
func matchDebugProfile(runtimeName, cmdline string) (*debugProfile, string) {
	lower := strings.ToLower(runtimeName)

	// Check runtime name against known keys in a fixed priority order.
	// More specific keys ("deno", "bun") come before shorter ones ("go", "node")
	// to prevent false substring matches.
	priority := []string{"python", "ruby", "deno", "bun", "node", "go"}
	for _, key := range priority {
		if strings.Contains(lower, key) {
			prof := debugProfiles[key]
			return &prof, key
		}
	}

	// Aliases for runtimes whose display name doesn't contain the debug key
	// (e.g. "TypeScript (tsx)" doesn't contain "node").
	aliases := []struct{ pattern, key string }{
		{"typescript", "node"},
		{"tsx", "node"},
	}
	for _, a := range aliases {
		if strings.Contains(lower, a.pattern) {
			prof := debugProfiles[a.key]
			return &prof, a.key
		}
	}

	// Check command line for clues
	if cmdline != "" {
		fields := strings.Fields(cmdline)
		if len(fields) > 0 {
			base := filepath.Base(fields[0])
			base = strings.TrimSuffix(base, filepath.Ext(base))
			// Normalize versioned names: python3.12 → python, ruby3.2 → ruby
			for _, prefix := range priority {
				if strings.HasPrefix(base, prefix) {
					prof := debugProfiles[prefix]
					return &prof, prefix
				}
			}
			// Check for TypeScript runners and Node.js tools in command line
			nodeTools := []string{"ts-node", "tsx", "npx", "npm", "yarn", "pnpm"}
			for _, tool := range nodeTools {
				if base == tool {
					prof := debugProfiles["node"]
					return &prof, "node"
				}
			}
		}
	}

	return nil, ""
}

// buildDebugCommand builds the debug-wrapped command for a given runtime.
func buildDebugCommand(prof *debugProfile, runtimeKey, origCmd string) string {
	fields := strings.Fields(origCmd)
	if len(fields) == 0 {
		return origCmd
	}

	switch runtimeKey {
	case "node", "deno", "bun":
		// 1. Check for TypeScript runners (ts-node, tsx) — they accept --inspect directly.
		for _, ts := range []string{"ts-node", "tsx"} {
			for i, f := range fields {
				if filepath.Base(f) == ts {
					rest := strings.Join(fields[i+1:], " ")
					return fmt.Sprintf("%s --inspect=0.0.0.0:%d %s", ts, prof.Port, rest)
				}
			}
		}

		// 2. Check for the actual runtime binary (node, deno, bun), which may
		//    be preceded by entrypoint wrappers (docker-entrypoint.sh node server.js).
		runtimeIdx := findRuntimeBinary(fields, runtimeKey)
		foundRuntime := false
		if runtimeIdx > 0 {
			foundRuntime = true
		} else {
			base := filepath.Base(fields[0])
			base = strings.TrimSuffix(base, filepath.Ext(base))
			foundRuntime = (base == runtimeKey || strings.HasPrefix(base, runtimeKey))
		}

		if foundRuntime {
			rest := strings.Join(fields[runtimeIdx+1:], " ")
			return fmt.Sprintf("%s --inspect=0.0.0.0:%d %s", runtimeKey, prof.Port, rest)
		}

		// 3. Framework/tool CLI (npm start, npx next dev, yarn dev, etc.).
		//    These spawn their own node processes, so we inject --inspect via
		//    NODE_OPTIONS which propagates to child processes.
		step("ℹ️", "Using NODE_OPTIONS for framework CLI (debugger attaches to spawned node process)")
		return fmt.Sprintf("NODE_OPTIONS='--inspect=0.0.0.0:%d' %s", prof.Port, strings.Join(fields, " "))

	case "python":
		// Find the python binary, skipping entrypoint wrappers.
		// For tools like uvicorn/gunicorn, wrap the entire original command
		// with debugpy so it can debug into the application.
		runtimeIdx := findRuntimeBinary(fields, runtimeKey)
		if runtimeIdx == 0 && !isPythonBinary(fields[0]) {
			// The command starts with a Python tool (uvicorn, gunicorn, etc.)
			// Normalize multi-worker flags, then wrap with debugpy.
			normalized := normalizePythonForDebug(fields)
			return fmt.Sprintf("pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:%d --wait-for-client -m %s",
				prof.Port, strings.Join(normalized, " "))
		}
		restFields := fields[runtimeIdx+1:]
		// Handle "python -m uvicorn ..." pattern — normalize tool args.
		if len(restFields) >= 2 && restFields[0] == "-m" {
			toolArgs := normalizePythonForDebug(restFields[1:])
			return fmt.Sprintf("pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:%d --wait-for-client -m %s",
				prof.Port, strings.Join(toolArgs, " "))
		}
		rest := strings.Join(restFields, " ")
		return fmt.Sprintf("pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:%d --wait-for-client %s", prof.Port, rest)

	case "ruby":
		// Find the ruby binary, skipping entrypoint wrappers.
		// Use -c (command mode) so rdbg treats the args as a command to run,
		// not as a script filename (e.g. "ruby app.rb" not "app.rb").
		// Use -n (nonstop) so the app starts immediately — without it rdbg
		// blocks until a debugger attaches, which causes health probe failures.
		runtimeIdx := findRuntimeBinary(fields, runtimeKey)
		appCmd := strings.Join(fields[runtimeIdx:], " ")
		return fmt.Sprintf("gem install debug --no-document -q 2>/dev/null; rdbg -n -c --open --host 0.0.0.0 --port %d -- %s", prof.Port, appCmd)

	case "go":
		// Go debug uses locally cross-compiled binary + Delve, both injected
		// into the container via kubectl cp after rollout (sync-inspired approach).
		// The command waits for /tmp/dlv to appear, then launches the debug binary.
		return fmt.Sprintf(
			"echo 'Waiting for debug tools...'; "+
				"while [ ! -f /tmp/dlv ]; do sleep 0.5; done; "+
				"echo 'Starting Delve debugger'; "+
				"/tmp/dlv exec --headless --listen=:%d --api-version=2 --accept-multiclient --continue /tmp/_debug_bin",
			prof.Port)

	default:
		return origCmd
	}
}

// findRuntimeBinary locates the index of the actual runtime binary in args,
// skipping entrypoint wrapper scripts like "docker-entrypoint.sh".
// Falls back to 0 if the binary isn't found.
func findRuntimeBinary(fields []string, runtimeKey string) int {
	for i, f := range fields {
		base := filepath.Base(f)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		// Match exact binary name or versioned variants (python3, node18, etc.)
		if base == runtimeKey || strings.HasPrefix(base, runtimeKey) {
			return i
		}
	}
	return 0
}

// isPythonBinary checks if a binary name is a Python interpreter
// (as opposed to a Python tool like uvicorn, gunicorn, daphne, etc.).
func isPythonBinary(name string) bool {
	base := filepath.Base(name)
	return strings.HasPrefix(base, "python")
}

// normalizePythonForDebug adjusts Python server flags for single-process debugging.
// Multi-worker servers (gunicorn, uvicorn) need --workers 1 because debugpy only
// attaches to one process. Gunicorn also needs --timeout 0 to prevent the master
// from killing a worker that's paused at a breakpoint.
func normalizePythonForDebug(args []string) []string {
	result := make([]string, 0, len(args)+2)
	workerPatched := false
	timeoutPatched := false

	// Detect if gunicorn is involved (needs --timeout 0)
	isGunicorn := false
	for _, a := range args {
		base := filepath.Base(a)
		if base == "gunicorn" {
			isGunicorn = true
			break
		}
	}

	for i := 0; i < len(args); i++ {
		switch {
		case (args[i] == "--workers" || args[i] == "-w") && i+1 < len(args):
			orig := args[i+1]
			result = append(result, args[i], "1")
			workerPatched = true
			if orig != "1" {
				step("⚠️", fmt.Sprintf("Forcing %s 1 for debugging (was %s — debugpy attaches to one process)", args[i], orig))
			}
			i++ // skip the original value
		case isGunicorn && args[i] == "--timeout" && i+1 < len(args):
			result = append(result, "--timeout", "0")
			timeoutPatched = true
			step("⚠️", "Forcing --timeout 0 for gunicorn (prevents worker kill during debugging)")
			i++ // skip the original value
		default:
			result = append(result, args[i])
		}
	}

	// For gunicorn without explicit --timeout, add it
	if isGunicorn && !timeoutPatched {
		result = append(result, "--timeout", "0")
		step("⚠️", "Adding --timeout 0 for gunicorn (prevents worker kill during debugging)")
	}

	// Only warn about workers if we actually patched them; if --workers wasn't
	// specified, the server defaults to 1 anyway (safe for debugging).
	_ = workerPatched

	return result
}

// ── Go debug helpers (sync-inspired) ────────────────────────────

// injectGoDebugTools cross-compiles the Go binary with debug symbols,
// downloads a Delve binary for the target arch, and kubectl cp's both
// into the running container. The patched deployment command is a wait
// loop that starts dlv as soon as /tmp/dlv appears.
func injectGoDebugTools(deployment, pod, namespace, container string) error {
	// ── 1. Determine source directory ─────────────────────────
	srcDir := findGoSourceDir(deployment)
	if srcDir == "" {
		return fmt.Errorf("cannot find Go source directory (looked for go.mod in CWD and subdirectories)")
	}
	step("📂", fmt.Sprintf("Go source: %s", srcDir))

	// ── 2. Cross-compile with debug symbols ───────────────────
	goos, goarch := detectNodeArch()
	debugBin := filepath.Join(os.TempDir(), "_kindling_debug_bin")
	buildCmd := fmt.Sprintf("CGO_ENABLED=0 GOOS=%s GOARCH=%s go build -gcflags='all=-N -l' -buildvcs=false -o %s .",
		goos, goarch, debugBin)

	step("🔨", "Cross-compiling with debug symbols")
	build := exec.Command("sh", "-c", buildCmd)
	build.Dir = srcDir
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("local build failed:\n%s\n%w", strings.TrimSpace(string(out)), err)
	}

	// ── 3. Get Delve binary ───────────────────────────────────
	dlvPath, err := ensureDelve(goos, goarch)
	if err != nil {
		return fmt.Errorf("cannot obtain Delve: %w", err)
	}

	// ── 4. Copy binaries into the container ──────────────────
	// kubectl cp requires tar in the container, which many minimal Go images
	// lack (alpine without tar, distroless, etc.). Use kubectl exec + sh to
	// pipe the binary via stdin, which only requires sh (already present
	// since the wait-loop command uses it).
	step("📦", "Injecting debug binary + Delve into container")

	copyBinary := func(localPath, remotePath string) error {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", localPath, err)
		}

		execArgs := []string{"exec", "-i", pod, "-n", namespace, "--context", kindContext()}
		if container != "" {
			execArgs = append(execArgs, "-c", container)
		}
		execArgs = append(execArgs, "--", "sh", "-c",
			fmt.Sprintf("cat > %s && chmod +x %s", remotePath, remotePath))

		cmd := exec.Command("kubectl", execArgs...)
		cmd.Stdin = bytes.NewReader(data)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s\n%w", strings.TrimSpace(stderr.String()), err)
		}
		return nil
	}

	// Copy debug binary
	if err := copyBinary(debugBin, "/tmp/_debug_bin"); err != nil {
		return fmt.Errorf("failed to copy debug binary: %w", err)
	}

	// Copy Delve to a temp name first, then rename — the wait loop watches
	// for /tmp/dlv, so writing directly could trigger execution of a partial binary.
	if err := copyBinary(dlvPath, "/tmp/.dlv.tmp"); err != nil {
		return fmt.Errorf("failed to copy Delve: %w", err)
	}
	// Atomic rename to trigger the wait loop
	renameArgs := []string{"exec", pod, "-n", namespace, "--context", kindContext()}
	if container != "" {
		renameArgs = append(renameArgs, "-c", container)
	}
	renameArgs = append(renameArgs, "--", "mv", "/tmp/.dlv.tmp", "/tmp/dlv")
	if out, err := runCapture("kubectl", renameArgs...); err != nil {
		return fmt.Errorf("failed to finalize Delve: %s", strings.TrimSpace(out))
	}

	success("Debug tools injected")
	return nil
}

// findGoSourceDir locates the Go source directory for a deployment.
// Checks CWD (or --project-dir), then subdirectories matching the
// deployment's last name segment (e.g. "jeff-vincent-gateway" → "gateway/").
func findGoSourceDir(deployment string) string {
	root := "."
	if projectDir != "" {
		root = projectDir
	}

	// In monorepo setups, prefer the subdirectory matching the deployment
	// suffix (e.g. "jeff-vincent-gateway" → "gateway/go.mod").
	parts := strings.Split(deployment, "-")
	if len(parts) > 0 {
		subDir := parts[len(parts)-1]
		candidate := filepath.Join(root, subDir)
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
	}

	// Fall back to root (single-service repos)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		return root
	}

	// Scan immediate subdirectories
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			candidate := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
				return candidate
			}
		}
	}
	return ""
}

// ensureDelve returns the path to a cached Delve binary for the given
// OS/arch. Cross-compiles from source if not already cached.
func ensureDelve(goos, goarch string) (string, error) {
	cacheDir := filepath.Join(kindlingDir(), "dlv-cache")
	os.MkdirAll(cacheDir, 0755)

	cached := filepath.Join(cacheDir, fmt.Sprintf("dlv-%s-%s", goos, goarch))
	if _, err := os.Stat(cached); err == nil {
		step("📦", "Using cached Delve binary")
		return cached, nil
	}

	step("🔨", fmt.Sprintf("Building Delve for %s/%s (first time only)", goos, goarch))

	// Create a temp GOPATH so `go install` writes the binary there.
	// We can't use GOBIN with cross-compilation, but GOPATH/bin/{GOOS}_{GOARCH}/
	// works for cross-compiled installs.
	tmpGopath, err := os.MkdirTemp("", "kindling-dlv-gopath-*")
	if err != nil {
		return "", err
	}
	defer func() {
		// Go module cache files are read-only — chmod before removing
		exec.Command("chmod", "-R", "u+w", tmpGopath).Run()
		os.RemoveAll(tmpGopath)
	}()

	installCmd := fmt.Sprintf("GOPATH=%s CGO_ENABLED=0 GOOS=%s GOARCH=%s go install github.com/go-delve/delve/cmd/dlv@latest",
		tmpGopath, goos, goarch)
	cmd := exec.Command("sh", "-c", installCmd)
	cmd.Env = os.Environ()
	// Override GOPATH in env to avoid conflicts
	cmd.Env = append(cmd.Env, "GOPATH="+tmpGopath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to build Delve:\n%s\n%w", strings.TrimSpace(string(out)), err)
	}

	// Cross-compiled binaries go to $GOPATH/bin/{GOOS}_{GOARCH}/dlv
	dlvBin := filepath.Join(tmpGopath, "bin", fmt.Sprintf("%s_%s", goos, goarch), "dlv")
	if _, err := os.Stat(dlvBin); err != nil {
		// Some Go versions put it directly in bin/
		dlvBin = filepath.Join(tmpGopath, "bin", "dlv")
		if _, err := os.Stat(dlvBin); err != nil {
			return "", fmt.Errorf("dlv binary not found after install (checked bin/ and bin/%s_%s/)", goos, goarch)
		}
	}

	// Copy to cache
	data, err := os.ReadFile(dlvBin)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(cached, data, 0755); err != nil {
		return "", err
	}

	success("Delve built and cached")
	return cached, nil
}

// extractInnerCommand pulls the original app command from a kindling sync wrapper.
func extractInnerCommand(wrappedCmd string) string {
	const marker = "while true; do "
	normalized := strings.ReplaceAll(wrappedCmd, `\u0026`, `&`)
	idx := strings.Index(normalized, marker)
	if idx < 0 {
		return ""
	}
	rest := normalized[idx+len(marker):]
	if ampIdx := strings.Index(rest, " &"); ampIdx > 0 {
		return strings.TrimSpace(rest[:ampIdx])
	}
	return ""
}

// detectLocalSourceDir figures out which local directory maps to the container's
// working directory. In monorepo / multi-service setups the service source may
// live in a subdirectory (e.g. "orders/") rather than at the workspace root.
//
// It lists files in the container's remoteRoot and checks whether those files
// exist locally in CWD (or --project-dir). If they don't, it searches immediate
// subdirectories and returns the best-matching one (e.g. "orders").
// Returns "" when the root directory is already the best match.
func detectLocalSourceDir(deployment, pod, namespace, container, remoteRoot string) string {
	root := "."
	if projectDir != "" {
		root = projectDir
	}

	// Extract the deployment suffix (e.g. "jeff-vincent-inventory" → "inventory").
	// In monorepo / multi-service repos, this matches the service subdirectory.
	deploymentSuffix := ""
	parts := strings.Split(deployment, "-")
	if len(parts) > 0 {
		deploymentSuffix = parts[len(parts)-1]
	}

	// Fast path: deployment suffix matches a local subdirectory with source files.
	// This is the most reliable signal — avoids ambiguity when multiple services
	// share the same language (e.g. two Node.js services in the same monorepo).
	if deploymentSuffix != "" {
		candidate := filepath.Join(root, deploymentSuffix)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if hasSourceFiles(candidate) {
				return deploymentSuffix
			}
		}
	}

	// Slow path: list files in the container's working directory and try to
	// match them against local directories.
	output, err := runCapture("kubectl", "exec", pod, "-n", namespace,
		"--context", kindContext(), "-c", container, "--",
		"ls", "-1", remoteRoot)
	if err != nil || strings.TrimSpace(output) == "" {
		return ""
	}

	remoteFiles := strings.Split(strings.TrimSpace(output), "\n")
	if len(remoteFiles) == 0 {
		return ""
	}

	// Count how many remote files exist directly in root.
	rootHits := 0
	for _, f := range remoteFiles {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			rootHits++
		}
	}

	// If most files match at the root level, no offset needed.
	if rootHits > len(remoteFiles)/2 {
		return ""
	}

	// Search immediate subdirectories for a better match.
	// Boost the deployment-suffix directory to break ties.
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}

	bestDir := ""
	bestHits := 0
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		hits := 0
		for _, f := range remoteFiles {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, entry.Name(), f)); err == nil {
				hits++
			}
		}
		// Prefer the deployment-matched subdirectory when scores are close.
		if entry.Name() == deploymentSuffix {
			hits += 2
		}
		if hits > bestHits {
			bestHits = hits
			bestDir = entry.Name()
		}
	}

	if bestHits > rootHits && bestHits > 0 {
		return bestDir
	}
	return ""
}

// hasSourceFiles checks if a directory contains typical source project files.
func hasSourceFiles(dir string) bool {
	markers := []string{
		"package.json", "go.mod", "requirements.txt", "pyproject.toml",
		"Gemfile", "setup.py", "Cargo.toml", "pom.xml", "Dockerfile",
		"main.go", "main.py", "app.py", "app.rb", "server.js", "index.js",
		"tsconfig.json", "mix.exs", "composer.json",
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// stripDebugWrapper recovers the original app command from a debug-wrapped
// startup command. For example:
//
//	"pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:5678 app.py"
//	  → "python app.py"
//	"node --inspect=0.0.0.0:9229 server.js"
//	  → "node server.js"
//	"NODE_OPTIONS='--inspect=0.0.0.0:9229' npm start"
//	  → "npm start"
//	"echo 'Waiting for debug tools...'; while ...; /tmp/dlv exec ... /tmp/_debug_bin"
//	  → "" (Go wrapper destroys original — caller must use saved state)
//	"dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --continue ./app"
//	  → "./app"
func stripDebugWrapper(cmd string) string {
	if cmd == "" {
		return ""
	}

	// Go wait-loop: "echo 'Waiting for debug tools...'; while [ ! -f /tmp/dlv ] ..."
	// The original app command is not embedded — it was replaced entirely.
	// Return empty so the caller falls back to the saved state.
	if strings.Contains(cmd, "/tmp/dlv") && strings.Contains(cmd, "while") {
		return ""
	}

	// NODE_OPTIONS wrapper: "NODE_OPTIONS='--inspect=...' <original command>"
	if strings.HasPrefix(cmd, "NODE_OPTIONS=") {
		// Strip the NODE_OPTIONS=... prefix (first field)
		fields := strings.Fields(cmd)
		if len(fields) > 1 {
			return strings.Join(fields[1:], " ")
		}
		return ""
	}

	// Strip "pip install ... ;" or "gem install ... ;" or "command -v ... ;" prefix
	if idx := strings.Index(cmd, "; "); idx >= 0 {
		rest := strings.TrimSpace(cmd[idx+2:])
		if rest != "" {
			cmd = rest
		}
	}

	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return cmd
	}

	switch {
	case strings.Contains(cmd, "-m debugpy"):
		// "python -m debugpy --listen 0.0.0.0:5678 app.py args..." → "python app.py args..."
		for i, f := range fields {
			if f == "-m" && i+1 < len(fields) && fields[i+1] == "debugpy" {
				// Skip: -m debugpy --listen X [--wait-for-client]
				j := i + 2
				for j < len(fields) && strings.HasPrefix(fields[j], "--") {
					j++
					// --listen takes a value
					if j-1 < len(fields) && fields[j-1] == "--listen" && j < len(fields) {
						j++
					}
				}
				result := append([]string{}, fields[:i]...)
				result = append(result, fields[j:]...)
				return strings.Join(result, " ")
			}
		}

	case strings.Contains(cmd, "--inspect="):
		// "node --inspect=0.0.0.0:9229 server.js" → "node server.js"
		var result []string
		for _, f := range fields {
			if !strings.HasPrefix(f, "--inspect=") && !strings.HasPrefix(f, "--inspect-brk=") {
				result = append(result, f)
			}
		}
		return strings.Join(result, " ")

	case strings.HasPrefix(fields[0], "dlv") || strings.HasPrefix(fields[0], "/tmp/dlv"):
		// "dlv exec --headless ... --continue ./app" → "./app"
		if len(fields) > 0 {
			return fields[len(fields)-1]
		}

	case strings.HasPrefix(fields[0], "rdbg"):
		// "rdbg --open --host 0.0.0.0 --port 12345 -- ruby app.rb" → "ruby app.rb"
		for i, f := range fields {
			if f == "--" && i+1 < len(fields) {
				return strings.Join(fields[i+1:], " ")
			}
		}
	}

	return cmd
}

// writeLaunchConfig writes VS Code launch.json + tasks.json for one-click F5 debugging.
//
// It creates two launch configurations:
//  1. "kindling: debug <deployment>" — a one-click config with a preLaunchTask that
//     runs `kindling debug`, waits for "Debugger ready", then auto-attaches.
//  2. "kindling: attach <deployment>" — attach-only config for re-attaching to an
//     already-running debug session.
//
// The background task uses a VS Code problem matcher to detect when the debugger
// port-forward is ready, so F5 is all you need.
func writeLaunchConfig(deployment string, prof *debugProfile, localPort int, remoteRoot, sourceSubdir string) {
	// Always write to CWD/.vscode/ — that's the VS Code workspace root.
	// The sourceSubdir already adjusts localRoot in pathMappings
	// (e.g. ${workspaceFolder}/orders), so there's no need to write
	// launch.json into the service subdirectory.
	vsDir := filepath.Join(".", ".vscode")
	os.MkdirAll(vsDir, 0755)

	// ── tasks.json ───────────────────────────────────────────────
	writeTasksJSON(vsDir, deployment, localPort)

	// ── launch.json ──────────────────────────────────────────────
	launchPath := filepath.Join(vsDir, "launch.json")

	taskLabel := fmt.Sprintf("kindling: start debug %s", deployment)
	f5ConfigName := fmt.Sprintf("kindling: debug %s", deployment)
	attachConfigName := fmt.Sprintf("kindling: attach %s", deployment)

	// Compute the localRoot. If the service source lives in a subdirectory
	// (e.g. "orders/" in a monorepo), use ${workspaceFolder}/orders so
	// pathMappings resolve correctly.
	localRoot := "${workspaceFolder}"
	if sourceSubdir != "" {
		localRoot = "${workspaceFolder}/" + sourceSubdir
	}

	// Build the attach config (shared by both entries)
	attachConfig := map[string]interface{}{
		"type":    prof.LaunchType,
		"request": prof.Request,
	}
	for k, v := range prof.Extra {
		attachConfig[k] = v
	}
	// Fix port to match actual local port
	if _, ok := attachConfig["port"]; ok {
		attachConfig["port"] = localPort
	}
	if connect, ok := attachConfig["connect"].(map[string]interface{}); ok {
		connect["port"] = localPort
		attachConfig["connect"] = connect
	}

	// Fix remoteRoot in path mappings to match the actual container workdir,
	// and localRoot to match the detected source subdirectory.
	if pm, ok := attachConfig["pathMappings"].([]map[string]string); ok {
		for i := range pm {
			pm[i]["localRoot"] = localRoot
			if remoteRoot != "" {
				pm[i]["remoteRoot"] = remoteRoot
			}
		}
		attachConfig["pathMappings"] = pm
	}
	if _, ok := attachConfig["remoteRoot"].(string); ok {
		attachConfig["remoteRoot"] = remoteRoot
		attachConfig["localRoot"] = localRoot
	}
	if sp, ok := attachConfig["substitutePath"].([]map[string]string); ok {
		if prof.LaunchType == "go" {
			// Go binaries cross-compiled locally have local absolute paths
			// baked into debug symbols — no path translation needed.
			// Remove substitutePath entirely so Delve uses local paths as-is.
			delete(attachConfig, "substitutePath")
		} else {
			for i := range sp {
				if _, has := sp[i]["from"]; has {
					sp[i]["from"] = localRoot
				}
				if _, has := sp[i]["to"]; has && remoteRoot != "" {
					sp[i]["to"] = remoteRoot
				}
			}
			attachConfig["substitutePath"] = sp
		}
	}
	if _, ok := attachConfig["localfsMap"].(string); ok {
		rem := remoteRoot
		if rem == "" {
			rem = "/app"
		}
		attachConfig["localfsMap"] = rem + ":" + localRoot
	}

	// Full F5 config = attach + preLaunchTask
	f5Config := map[string]interface{}{}
	for k, v := range attachConfig {
		f5Config[k] = v
	}
	f5Config["name"] = f5ConfigName
	f5Config["preLaunchTask"] = taskLabel

	// Attach-only config (no task, for re-attaching)
	attachOnly := map[string]interface{}{}
	for k, v := range attachConfig {
		attachOnly[k] = v
	}
	attachOnly["name"] = attachConfigName

	// Read existing launch.json or create new
	type launchJSON struct {
		Version        string                   `json:"version"`
		Configurations []map[string]interface{} `json:"configurations"`
	}

	var launch launchJSON
	if data, err := os.ReadFile(launchPath); err == nil {
		json.Unmarshal(data, &launch)
	}
	if launch.Version == "" {
		launch.Version = "0.2.0"
	}
	if launch.Configurations == nil {
		launch.Configurations = []map[string]interface{}{}
	}

	// Remove any existing kindling debug/attach configs for this deployment
	filtered := make([]map[string]interface{}, 0, len(launch.Configurations))
	for _, c := range launch.Configurations {
		name, _ := c["name"].(string)
		if name != f5ConfigName && name != attachConfigName {
			filtered = append(filtered, c)
		}
	}
	// F5 config first so it's the default when user hits F5
	filtered = append([]map[string]interface{}{f5Config}, filtered...)
	filtered = append(filtered, attachOnly)
	launch.Configurations = filtered

	data, _ := json.MarshalIndent(launch, "", "  ")
	if err := os.WriteFile(launchPath, data, 0644); err != nil {
		fmt.Printf("⚠️  Could not write launch.json: %v\n", err)
		return
	}

	step("📎", "Wrote .vscode/launch.json — press F5 to start debugging")
}

// writeTasksJSON writes a VS Code tasks.json with a background task for kindling debug.
// The task uses a problem matcher to signal VS Code when the debugger is ready,
// so the attach step only proceeds after the port-forward is up.
func writeTasksJSON(vsDir, deployment string, localPort int) {
	tasksPath := filepath.Join(vsDir, "tasks.json")
	taskLabel := fmt.Sprintf("kindling: start debug %s", deployment)
	stopLabel := fmt.Sprintf("kindling: stop debug %s", deployment)

	// Build the start task with a background problem matcher
	startTask := map[string]interface{}{
		"label":        taskLabel,
		"type":         "shell",
		"command":      fmt.Sprintf("kindling debug -d %s --port %d", deployment, localPort),
		"isBackground": true,
		"problemMatcher": []map[string]interface{}{
			{
				"pattern": []map[string]interface{}{
					{
						"regexp":  "^__never_match__$",
						"file":    1,
						"line":    2,
						"message": 3,
					},
				},
				"background": map[string]interface{}{
					"activeOnStart": true,
					"beginsPattern": "Detecting runtime",
					"endsPattern":   "Debugger ready",
				},
			},
		},
		"presentation": map[string]interface{}{
			"reveal": "silent",
			"panel":  "dedicated",
			"close":  false,
		},
	}

	// Stop task (for postDebugTask or manual use)
	stopTask := map[string]interface{}{
		"label":   stopLabel,
		"type":    "shell",
		"command": fmt.Sprintf("kindling debug --stop -d %s", deployment),
		"presentation": map[string]interface{}{
			"reveal": "silent",
			"panel":  "shared",
		},
	}

	// Read existing tasks.json or create new
	type tasksJSON struct {
		Version string                   `json:"version"`
		Tasks   []map[string]interface{} `json:"tasks"`
	}

	var tasks tasksJSON
	if data, err := os.ReadFile(tasksPath); err == nil {
		json.Unmarshal(data, &tasks)
	}
	if tasks.Version == "" {
		tasks.Version = "2.0.0"
	}
	if tasks.Tasks == nil {
		tasks.Tasks = []map[string]interface{}{}
	}

	// Remove any existing kindling debug tasks for this deployment
	filtered := make([]map[string]interface{}, 0, len(tasks.Tasks))
	for _, t := range tasks.Tasks {
		label, _ := t["label"].(string)
		if label != taskLabel && label != stopLabel {
			filtered = append(filtered, t)
		}
	}
	filtered = append(filtered, startTask, stopTask)
	tasks.Tasks = filtered

	data, _ := json.MarshalIndent(tasks, "", "  ")
	if err := os.WriteFile(tasksPath, data, 0644); err != nil {
		fmt.Printf("⚠️  Could not write tasks.json: %v\n", err)
		return
	}

	step("📎", "Wrote .vscode/tasks.json — background debug task configured")
}
