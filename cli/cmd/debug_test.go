package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// matchDebugProfile
// ────────────────────────────────────────────────────────────────────────────

func TestMatchDebugProfile(t *testing.T) {
	tests := []struct {
		runtime string
		cmdline string
		wantKey string
		wantNil bool
	}{
		{"Python 3.12", "python main.py", "python", false},
		{"python3", "python3 app.py", "python", false},
		{"Node.js v20", "node index.js", "node", false},
		{"node", "node server.js", "node", false},
		{"Deno 1.40", "deno run main.ts", "deno", false},
		{"Bun 1.0", "bun run index.ts", "bun", false},
		{"Go", "go run main.go", "go", false},
		{"Ruby 3.2", "ruby app.rb", "ruby", false},
		{"unknown", "", "", true},
		{"", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.runtime+"_"+tt.cmdline, func(t *testing.T) {
			prof, key := matchDebugProfile(tt.runtime, tt.cmdline)
			if tt.wantNil {
				if prof != nil {
					t.Errorf("matchDebugProfile(%q, %q) = %v, want nil", tt.runtime, tt.cmdline, prof.Name)
				}
				return
			}
			if prof == nil {
				t.Fatalf("matchDebugProfile(%q, %q) = nil, want %q", tt.runtime, tt.cmdline, tt.wantKey)
			}
			if key != tt.wantKey {
				t.Errorf("matchDebugProfile(%q, %q) key = %q, want %q", tt.runtime, tt.cmdline, key, tt.wantKey)
			}
		})
	}
}

// Ensure deterministic priority: "deno" should NOT match "node", etc.
func TestMatchDebugProfilePriority(t *testing.T) {
	// "deno" contains "no" but should not match "node"
	prof, key := matchDebugProfile("deno", "deno run app.ts")
	if key != "deno" {
		t.Errorf("expected deno, got %q (profile: %v)", key, prof)
	}

	// "bun" should not match "node" despite both being JS runtimes
	prof, key = matchDebugProfile("bun", "bun run index.ts")
	if key != "bun" {
		t.Errorf("expected bun, got %q (profile: %v)", key, prof)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildDebugCommand
// ────────────────────────────────────────────────────────────────────────────

func TestBuildDebugCommand(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		origCmd string
		want    string // substring that must be in the result
		notWant string // substring that must NOT be in the result
	}{
		{
			name:    "python wraps with debugpy",
			key:     "python",
			origCmd: "python app.py",
			want:    "debugpy --listen 0.0.0.0:5678",
		},
		{
			name:    "python installs debugpy",
			key:     "python",
			origCmd: "python app.py",
			want:    "pip install debugpy",
		},
		{
			name:    "node injects inspect flag",
			key:     "node",
			origCmd: "node server.js",
			want:    "--inspect=0.0.0.0:9229",
		},
		{
			name:    "go uses dlv",
			key:     "go",
			origCmd: "./myapp",
			want:    "dlv exec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prof := debugProfiles[tt.key]
			result := buildDebugCommand(&prof, tt.key, tt.origCmd)
			if !strings.Contains(result, tt.want) {
				t.Errorf("buildDebugCommand(%q, %q) = %q, want to contain %q", tt.key, tt.origCmd, result, tt.want)
			}
			if tt.notWant != "" && strings.Contains(result, tt.notWant) {
				t.Errorf("buildDebugCommand(%q, %q) = %q, should NOT contain %q", tt.key, tt.origCmd, result, tt.notWant)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// stripDebugWrapper
// ────────────────────────────────────────────────────────────────────────────

func TestStripDebugWrapper(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "python debugpy wrapper",
			cmd:  "pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:5678 /usr/local/bin/uvicorn main:app --host 0.0.0.0 --port 5000",
			want: "python /usr/local/bin/uvicorn main:app --host 0.0.0.0 --port 5000",
		},
		{
			name: "node inspect wrapper",
			cmd:  "node --inspect=0.0.0.0:9229 server.js",
			want: "node server.js",
		},
		{
			name: "plain command (no wrapper)",
			cmd:  "uvicorn main:app --host 0.0.0.0 --port 5000",
			want: "uvicorn main:app --host 0.0.0.0 --port 5000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripDebugWrapper(tt.cmd)
			if got != tt.want {
				t.Errorf("stripDebugWrapper(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// writeLaunchConfig + writeTasksJSON — JSON structure validation
// ────────────────────────────────────────────────────────────────────────────

func TestWriteLaunchConfig(t *testing.T) {
	// Use a temp directory to avoid polluting the workspace
	tmpDir := t.TempDir()
	origProjectDir := projectDir
	projectDir = tmpDir
	defer func() { projectDir = origProjectDir }()

	prof := debugProfiles["python"]
	writeLaunchConfig("my-api", &prof, 5678, "/app")

	// ── Validate launch.json ──
	launchPath := filepath.Join(tmpDir, ".vscode", "launch.json")
	data, err := os.ReadFile(launchPath)
	if err != nil {
		t.Fatalf("launch.json not created: %v", err)
	}

	var launch struct {
		Version string                   `json:"version"`
		Configs []map[string]interface{} `json:"configurations"`
	}
	if err := json.Unmarshal(data, &launch); err != nil {
		t.Fatalf("launch.json is invalid JSON: %v\nContent: %s", err, data)
	}

	if launch.Version != "0.2.0" {
		t.Errorf("launch.json version = %q, want %q", launch.Version, "0.2.0")
	}

	if len(launch.Configs) != 2 {
		t.Fatalf("launch.json has %d configs, want 2", len(launch.Configs))
	}

	// First config should be the F5 config (with preLaunchTask)
	f5 := launch.Configs[0]
	if f5["name"] != "kindling: debug my-api" {
		t.Errorf("first config name = %q, want %q", f5["name"], "kindling: debug my-api")
	}
	if f5["preLaunchTask"] == nil {
		t.Error("first config missing preLaunchTask")
	}
	if f5["type"] != "debugpy" {
		t.Errorf("first config type = %q, want %q", f5["type"], "debugpy")
	}
	if f5["request"] != "attach" {
		t.Errorf("first config request = %q, want %q", f5["request"], "attach")
	}

	// Check pathMappings are present with correct remoteRoot
	pm, ok := f5["pathMappings"].([]interface{})
	if !ok || len(pm) == 0 {
		t.Fatal("first config missing pathMappings")
	}
	mapping, _ := pm[0].(map[string]interface{})
	if mapping["remoteRoot"] != "/app" {
		t.Errorf("pathMappings remoteRoot = %q, want %q", mapping["remoteRoot"], "/app")
	}
	if mapping["localRoot"] != "${workspaceFolder}" {
		t.Errorf("pathMappings localRoot = %q, want %q", mapping["localRoot"], "${workspaceFolder}")
	}

	// Check connect.port matches localPort
	connect, ok := f5["connect"].(map[string]interface{})
	if !ok {
		t.Fatal("first config missing connect field")
	}
	if port, _ := connect["port"].(float64); int(port) != 5678 {
		t.Errorf("first config connect.port = %v, want 5678", connect["port"])
	}

	// Second config should be attach-only (no preLaunchTask)
	attach := launch.Configs[1]
	if attach["name"] != "kindling: attach my-api" {
		t.Errorf("second config name = %q, want %q", attach["name"], "kindling: attach my-api")
	}
	if attach["preLaunchTask"] != nil {
		t.Error("second config should NOT have preLaunchTask")
	}

	// ── Validate tasks.json ──
	tasksPath := filepath.Join(tmpDir, ".vscode", "tasks.json")
	tdata, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatalf("tasks.json not created: %v", err)
	}

	var tasks struct {
		Version string                   `json:"version"`
		Tasks   []map[string]interface{} `json:"tasks"`
	}
	if err := json.Unmarshal(tdata, &tasks); err != nil {
		t.Fatalf("tasks.json is invalid JSON: %v\nContent: %s", err, tdata)
	}

	if tasks.Version != "2.0.0" {
		t.Errorf("tasks.json version = %q, want %q", tasks.Version, "2.0.0")
	}

	if len(tasks.Tasks) != 2 {
		t.Fatalf("tasks.json has %d tasks, want 2", len(tasks.Tasks))
	}

	// Start task
	start := tasks.Tasks[0]
	if start["label"] != "kindling: start debug my-api" {
		t.Errorf("start task label = %q, want %q", start["label"], "kindling: start debug my-api")
	}
	if start["isBackground"] != true {
		t.Error("start task should be a background task")
	}
	cmd, _ := start["command"].(string)
	if !strings.Contains(cmd, "kindling debug -d my-api") {
		t.Errorf("start task command = %q, want to contain 'kindling debug -d my-api'", cmd)
	}
	if !strings.Contains(cmd, "--port 5678") {
		t.Errorf("start task command = %q, want to contain '--port 5678'", cmd)
	}

	// Verify problem matcher structure
	matchers, ok := start["problemMatcher"].([]interface{})
	if !ok || len(matchers) == 0 {
		t.Fatal("start task missing problemMatcher")
	}
	matcher := matchers[0].(map[string]interface{})
	bg, ok := matcher["background"].(map[string]interface{})
	if !ok {
		t.Fatal("problem matcher missing background field")
	}
	if bg["activeOnStart"] != true {
		t.Error("background.activeOnStart should be true")
	}
	if !strings.Contains(bg["beginsPattern"].(string), "Detecting runtime") {
		t.Errorf("beginsPattern = %q, should contain 'Detecting runtime'", bg["beginsPattern"])
	}
	if !strings.Contains(bg["endsPattern"].(string), "Debugger ready") {
		t.Errorf("endsPattern = %q, should contain 'Debugger ready'", bg["endsPattern"])
	}

	// Stop task
	stop := tasks.Tasks[1]
	if stop["label"] != "kindling: stop debug my-api" {
		t.Errorf("stop task label = %q, want %q", stop["label"], "kindling: stop debug my-api")
	}
	stopCmd, _ := stop["command"].(string)
	if !strings.Contains(stopCmd, "kindling debug --stop -d my-api") {
		t.Errorf("stop task command = %q, want to contain 'kindling debug --stop -d my-api'", stopCmd)
	}
}

// Test that writeLaunchConfig preserves existing non-kindling configs
func TestWriteLaunchConfigPreservesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	origProjectDir := projectDir
	projectDir = tmpDir
	defer func() { projectDir = origProjectDir }()

	// Create an existing launch.json with a user config
	vsDir := filepath.Join(tmpDir, ".vscode")
	os.MkdirAll(vsDir, 0755)
	existing := `{
		"version": "0.2.0",
		"configurations": [
			{"name": "My Custom Config", "type": "node", "request": "launch", "program": "index.js"}
		]
	}`
	os.WriteFile(filepath.Join(vsDir, "launch.json"), []byte(existing), 0644)

	prof := debugProfiles["python"]
	writeLaunchConfig("my-api", &prof, 5678, "/app")

	data, _ := os.ReadFile(filepath.Join(vsDir, "launch.json"))
	var launch struct {
		Configs []map[string]interface{} `json:"configurations"`
	}
	json.Unmarshal(data, &launch)

	// Should have 3 configs: kindling F5, user's custom, kindling attach
	if len(launch.Configs) != 3 {
		t.Fatalf("expected 3 configs (F5 + custom + attach), got %d", len(launch.Configs))
	}

	// F5 config should be first
	if launch.Configs[0]["name"] != "kindling: debug my-api" {
		t.Errorf("first config should be kindling F5, got %q", launch.Configs[0]["name"])
	}

	// User's custom config should be preserved
	if launch.Configs[1]["name"] != "My Custom Config" {
		t.Errorf("second config should be user's custom, got %q", launch.Configs[1]["name"])
	}

	// Attach config should be last
	if launch.Configs[2]["name"] != "kindling: attach my-api" {
		t.Errorf("third config should be kindling attach, got %q", launch.Configs[2]["name"])
	}
}

// Test that re-running writeLaunchConfig replaces (not duplicates) kindling configs
func TestWriteLaunchConfigIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	origProjectDir := projectDir
	projectDir = tmpDir
	defer func() { projectDir = origProjectDir }()

	prof := debugProfiles["python"]

	// Write twice
	writeLaunchConfig("my-api", &prof, 5678, "/app")
	writeLaunchConfig("my-api", &prof, 9999, "/srv")

	data, _ := os.ReadFile(filepath.Join(tmpDir, ".vscode", "launch.json"))
	var launch struct {
		Configs []map[string]interface{} `json:"configurations"`
	}
	json.Unmarshal(data, &launch)

	// Should still be exactly 2 configs (not 4)
	if len(launch.Configs) != 2 {
		t.Fatalf("expected 2 configs after idempotent write, got %d", len(launch.Configs))
	}

	// Port should be updated to the latest (9999)
	f5 := launch.Configs[0]
	connect, _ := f5["connect"].(map[string]interface{})
	if port, _ := connect["port"].(float64); int(port) != 9999 {
		t.Errorf("port should be updated to 9999, got %v", connect["port"])
	}

	// Tasks should also be idempotent
	tdata, _ := os.ReadFile(filepath.Join(tmpDir, ".vscode", "tasks.json"))
	var tasks struct {
		Tasks []map[string]interface{} `json:"tasks"`
	}
	json.Unmarshal(tdata, &tasks)

	if len(tasks.Tasks) != 2 {
		t.Fatalf("expected 2 tasks after idempotent write, got %d", len(tasks.Tasks))
	}
}

// Test all debug profiles have required fields
func TestDebugProfilesComplete(t *testing.T) {
	required := []string{"node", "deno", "bun", "python", "ruby", "go"}
	for _, key := range required {
		prof, ok := debugProfiles[key]
		if !ok {
			t.Errorf("missing debug profile for %q", key)
			continue
		}
		if prof.Name == "" {
			t.Errorf("profile %q has empty Name", key)
		}
		if prof.Port == 0 {
			t.Errorf("profile %q has zero Port", key)
		}
		if prof.WrapFmt == "" {
			t.Errorf("profile %q has empty WrapFmt", key)
		}
		if prof.LaunchType == "" {
			t.Errorf("profile %q has empty LaunchType", key)
		}
		if prof.Request != "attach" {
			t.Errorf("profile %q Request = %q, want 'attach'", key, prof.Request)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// debugState serialization round-trip
// ────────────────────────────────────────────────────────────────────────────

func TestDebugStateSerialization(t *testing.T) {
	tmpDir := t.TempDir()

	state := debugState{
		Deployment: "my-api",
		Namespace:  "default",
		Runtime:    "python",
		LocalPort:  5678,
		RemotePort: 5678,
		OrigCmd:    "uvicorn main:app --host 0.0.0.0 --port 5000",
		HadCommand: false,
		PfPid:      12345,
	}

	// Override kindlingDir to temp dir
	stateFile := filepath.Join(tmpDir, "debug-my-api.json")
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(stateFile, data, 0644)

	// Read it back
	read, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	var loaded debugState
	if err := json.Unmarshal(read, &loaded); err != nil {
		t.Fatalf("failed to unmarshal state: %v", err)
	}

	if loaded.Deployment != state.Deployment {
		t.Errorf("Deployment = %q, want %q", loaded.Deployment, state.Deployment)
	}
	if loaded.HadCommand != state.HadCommand {
		t.Errorf("HadCommand = %v, want %v", loaded.HadCommand, state.HadCommand)
	}
	if loaded.OrigCmd != state.OrigCmd {
		t.Errorf("OrigCmd = %q, want %q", loaded.OrigCmd, state.OrigCmd)
	}
	if loaded.LocalPort != state.LocalPort {
		t.Errorf("LocalPort = %d, want %d", loaded.LocalPort, state.LocalPort)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// extractInnerCommand
// ────────────────────────────────────────────────────────────────────────────

func TestExtractInnerCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "sync wrapper",
			cmd:  "while true; do uvicorn main:app --host 0.0.0.0 --port 5000 & CHILD=$!; inotifywait -r /app; kill $CHILD; done",
			want: "uvicorn main:app --host 0.0.0.0 --port 5000",
		},
		{
			name: "no wrapper",
			cmd:  "uvicorn main:app --host 0.0.0.0 --port 5000",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInnerCommand(tt.cmd)
			if got != tt.want {
				t.Errorf("extractInnerCommand(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}
