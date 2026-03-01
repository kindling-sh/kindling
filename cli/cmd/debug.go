package cmd

import (
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
	PfPid      int    `json:"pfPid"`
}

// debugStateFile returns the path to the debug state file for a deployment.
func debugStateFile(deployment string) string {
	return filepath.Join(kindlingDir(), fmt.Sprintf("debug-%s.json", deployment))
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

	// Map runtime to debug profile
	debugProf, runtimeKey := matchDebugProfile(profile.Name, cmdline)
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

	step("📝", fmt.Sprintf("Original command: %s", origCmd))
	if !hadCommand {
		step("ℹ️", "No command override in deployment spec — will remove on stop")
	}

	// Build the debug-wrapped command
	// For Node: strip "node" from origCmd, inject --inspect
	// For others: wrap the full command
	debugCmd := buildDebugCommand(debugProf, runtimeKey, origCmd)
	step("🔧", fmt.Sprintf("Debug command: %s", debugCmd))

	// Patch the deployment
	cName := containerNameForDeployment(deployment, namespace, "")
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kindling.dev/debug":"true"}},"spec":{"containers":[{"name":"%s","command":["sh","-c","%s"]}]}}}}`,
		cName, strings.ReplaceAll(debugCmd, `"`, `\"`))

	step("🔧", "Patching deployment with debug command")
	if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"--type=strategic", "-p", patch); err != nil {
		return fmt.Errorf("failed to patch deployment: %w", err)
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

	// Save state
	state := debugState{
		Deployment: deployment,
		Namespace:  namespace,
		Runtime:    runtimeKey,
		LocalPort:  localPort,
		RemotePort: debugProf.Port,
		OrigCmd:    origCmd,
		HadCommand: hadCommand,
		PfPid:      pfCmd.Process.Pid,
	}
	if err := saveDebugState(state); err != nil {
		fmt.Printf("⚠️  Could not save debug state: %v\n", err)
	}

	// Write launch.json
	if !debugNoLaunch {
		writeLaunchConfig(deployment, debugProf, localPort, remoteRoot)
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

	// Restore original command
	step("🔧", "Restoring original command for "+deployment)

	if !state.HadCommand {
		// The deployment originally had no command override — remove it entirely
		// so the image's CMD/ENTRYPOINT takes effect again.
		// First check if there's actually a command to remove (avoid 422 error).
		specCmd, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
			"-n", namespace, "--context", kindContext(),
			"-o", "jsonpath={.spec.template.spec.containers[0].command}")
		if strings.TrimSpace(specCmd) != "" && specCmd != "[]" {
			step("ℹ️", "Removing command override (restoring image defaults)")
			if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace, "--context", kindContext(),
				"--type=json", "-p",
				`[{"op":"remove","path":"/spec/template/spec/containers/0/command"}]`); err != nil {
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
	case "node":
		// Replace "node script.js" → "node --inspect=0.0.0.0:9229 script.js"
		// Keep all original args after the runtime binary
		rest := strings.Join(fields[1:], " ")
		return fmt.Sprintf("node --inspect=0.0.0.0:%d %s", prof.Port, rest)

	case "deno":
		rest := strings.Join(fields[1:], " ")
		return fmt.Sprintf("deno --inspect=0.0.0.0:%d %s", prof.Port, rest)

	case "bun":
		rest := strings.Join(fields[1:], " ")
		return fmt.Sprintf("bun --inspect=0.0.0.0:%d %s", prof.Port, rest)

	case "python":
		// Install debugpy at startup + launch with debug listener
		rest := strings.Join(fields[1:], " ")
		return fmt.Sprintf("pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:%d %s", prof.Port, rest)

	case "ruby":
		// Install rdbg at startup + launch with debug listener
		return fmt.Sprintf("gem install debug --no-document -q 2>/dev/null; rdbg --open --host 0.0.0.0 --port %d -- %s", prof.Port, origCmd)

	case "go":
		// Install dlv at startup + launch headless debugger
		return fmt.Sprintf("command -v dlv >/dev/null 2>&1 || go install github.com/go-delve/delve/cmd/dlv@latest 2>/dev/null; dlv exec --headless --listen=:%d --api-version=2 --accept-multiclient --continue %s", prof.Port, fields[0])

	default:
		return origCmd
	}
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

// stripDebugWrapper recovers the original app command from a debug-wrapped
// startup command. For example:
//
//	"pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:5678 app.py"
//	  → "python app.py"
//	"node --inspect=0.0.0.0:9229 server.js"
//	  → "node server.js"
//	"dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --continue ./app"
//	  → "./app"
func stripDebugWrapper(cmd string) string {
	if cmd == "" {
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

	case strings.HasPrefix(fields[0], "dlv"):
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
func writeLaunchConfig(deployment string, prof *debugProfile, localPort int, remoteRoot string) {
	// Write to --project-dir if set, otherwise CWD.
	root := "."
	if projectDir != "" {
		root = projectDir
	}
	vsDir := filepath.Join(root, ".vscode")
	os.MkdirAll(vsDir, 0755)

	// ── tasks.json ───────────────────────────────────────────────
	writeTasksJSON(vsDir, deployment, localPort)

	// ── launch.json ──────────────────────────────────────────────
	launchPath := filepath.Join(vsDir, "launch.json")

	taskLabel := fmt.Sprintf("kindling: start debug %s", deployment)
	f5ConfigName := fmt.Sprintf("kindling: debug %s", deployment)
	attachConfigName := fmt.Sprintf("kindling: attach %s", deployment)

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

	// Fix remoteRoot in path mappings to match the actual container workdir
	if remoteRoot != "" {
		if pm, ok := attachConfig["pathMappings"].([]map[string]string); ok {
			for i := range pm {
				pm[i]["remoteRoot"] = remoteRoot
			}
			attachConfig["pathMappings"] = pm
		}
		if rr, ok := attachConfig["remoteRoot"].(string); ok && rr != "" {
			attachConfig["remoteRoot"] = remoteRoot
		}
		if sp, ok := attachConfig["substitutePath"].([]map[string]string); ok {
			for i := range sp {
				if _, has := sp[i]["to"]; has {
					sp[i]["to"] = remoteRoot
				}
			}
			attachConfig["substitutePath"] = sp
		}
		if lfm, ok := attachConfig["localfsMap"].(string); ok && lfm != "" {
			attachConfig["localfsMap"] = remoteRoot + ":${workspaceFolder}"
		}
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
