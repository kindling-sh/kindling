package cmd

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// deploymentFromPod
// ────────────────────────────────────────────────────────────────────────────

func TestDeploymentFromPod(t *testing.T) {
	tests := []struct {
		podName string
		want    string
		wantErr bool
	}{
		{"orders-abc12-xyz34", "orders", false},
		{"my-service-abc12-xyz34", "my-service", false},
		{"gateway-api-v2-abc12-xyz34", "gateway-api-v2", false},
		{"single-abc12-xyz34", "single", false},
		// Too few segments
		{"short-pod", "", true},
		{"single", "", true},
		// Edge: hyphenated deployment
		{"a-b-c-d-e", "a-b-c", false},
	}
	for _, tt := range tests {
		t.Run(tt.podName, func(t *testing.T) {
			got, err := deploymentFromPod(tt.podName)
			if tt.wantErr {
				if err == nil {
					t.Errorf("deploymentFromPod(%q) = %q, want error", tt.podName, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("deploymentFromPod(%q) error = %v", tt.podName, err)
			}
			if got != tt.want {
				t.Errorf("deploymentFromPod(%q) = %q, want %q", tt.podName, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// dimText
// ────────────────────────────────────────────────────────────────────────────

func TestDimText(t *testing.T) {
	result := dimText("hello")
	if result != "\033[2mhello\033[0m" {
		t.Errorf("dimText(%q) = %q, want ANSI dim wrapped", "hello", result)
	}
}

func TestDimText_Empty(t *testing.T) {
	result := dimText("")
	if result != "\033[2m\033[0m" {
		t.Errorf("dimText(%q) = %q", "", result)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// defaultExcludes data verification
// ────────────────────────────────────────────────────────────────────────────

func TestDefaultExcludesNotEmpty(t *testing.T) {
	if len(defaultExcludes) == 0 {
		t.Fatal("defaultExcludes should not be empty")
	}
}

func TestDefaultExcludesContainsCommonPatterns(t *testing.T) {
	want := []string{
		".git",
		"node_modules",
		"__pycache__",
		".venv",
		"vendor",
		".DS_Store",
	}
	set := map[string]bool{}
	for _, e := range defaultExcludes {
		set[e] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("defaultExcludes missing %q", w)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// runtimeTable data verification
// ────────────────────────────────────────────────────────────────────────────

func TestRuntimeTableNotEmpty(t *testing.T) {
	if len(runtimeTable) == 0 {
		t.Fatal("runtimeTable should not be empty")
	}
}

func TestRuntimeTableKnownEntries(t *testing.T) {
	knownKeys := []string{
		"node", "python", "python3", "ruby", "php", "go",
		"uvicorn", "gunicorn", "puma", "nginx", "deno", "bun",
	}
	for _, key := range knownKeys {
		profile, ok := runtimeTable[key]
		if !ok {
			t.Errorf("runtimeTable missing key %q", key)
			continue
		}
		if profile.Name == "" {
			t.Errorf("runtimeTable[%q].Name is empty", key)
		}
	}
}

func TestRuntimeTableInterpretedConsistency(t *testing.T) {
	compiledKeys := []string{"go", "java", "kotlin", "dotnet", "cargo", "rustc", "gcc", "zig"}
	for _, key := range compiledKeys {
		profile, ok := runtimeTable[key]
		if !ok {
			continue
		}
		if profile.Mode != modeRebuild {
			t.Errorf("runtimeTable[%q].Mode = %v, want modeRebuild", key, profile.Mode)
		}
		if profile.Interpreted {
			t.Errorf("runtimeTable[%q].Interpreted = true, want false for compiled language", key)
		}
	}
}

func TestRuntimeTableSignalRuntimes(t *testing.T) {
	signalKeys := []string{"uvicorn", "gunicorn", "puma", "unicorn", "nginx", "caddy"}
	for _, key := range signalKeys {
		profile, ok := runtimeTable[key]
		if !ok {
			continue
		}
		if profile.Mode != modeSignal {
			t.Errorf("runtimeTable[%q].Mode = %v, want modeSignal", key, profile.Mode)
		}
		if profile.Signal == "" {
			t.Errorf("runtimeTable[%q].Signal is empty for modeSignal runtime", key)
		}
	}
}

func TestRuntimeTableNoneRuntimes(t *testing.T) {
	noneKeys := []string{"php", "php-fpm", "nodemon"}
	for _, key := range noneKeys {
		profile, ok := runtimeTable[key]
		if !ok {
			continue
		}
		if profile.Mode != modeNone {
			t.Errorf("runtimeTable[%q].Mode = %v, want modeNone", key, profile.Mode)
		}
	}
}
