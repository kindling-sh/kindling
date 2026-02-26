package core

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// RunnerPoolConfig.namespace()
// ────────────────────────────────────────────────────────────────────────────

func TestRunnerPoolConfigNamespace(t *testing.T) {
	tests := []struct {
		ns   string
		want string
	}{
		{"", "default"},
		{"custom-ns", "custom-ns"},
	}
	for _, tt := range tests {
		cfg := RunnerPoolConfig{Namespace: tt.ns}
		if got := cfg.namespace(); got != tt.want {
			t.Errorf("RunnerPoolConfig{Namespace: %q}.namespace() = %q, want %q", tt.ns, got, tt.want)
		}
	}
}
