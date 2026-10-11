package cmd

import (
	"testing"

	"github.com/jeffvincent/kindling/cli/core"
)

// ════════════════════════════════════════════════════════════════
// parseBuildAgentEnvFlag
// ════════════════════════════════════════════════════════════════

func TestParseBuildAgentEnvFlag_Valid(t *testing.T) {
	got, err := parseBuildAgentEnvFlag([]string{
		"KINDLING_REGISTRY_USERNAME=registry-credentials:username",
		"KINDLING_REGISTRY_PASSWORD=registry-credentials:password",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []core.BuildAgentEnvVar{
		{Name: "KINDLING_REGISTRY_USERNAME", SecretName: "registry-credentials", SecretKey: "username"},
		{Name: "KINDLING_REGISTRY_PASSWORD", SecretName: "registry-credentials", SecretKey: "password"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseBuildAgentEnvFlag_Empty(t *testing.T) {
	got, err := parseBuildAgentEnvFlag(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d entries, want 0", len(got))
	}
}

func TestParseBuildAgentEnvFlag_Malformed(t *testing.T) {
	tests := []string{
		"no-equals-sign",
		"NAME=",
		"NAME=secret-no-colon",
		"NAME=:key-no-secret",
		"NAME=secret:",
		"=secret:key",
	}
	for _, in := range tests {
		if _, err := parseBuildAgentEnvFlag([]string{in}); err == nil {
			t.Errorf("parseBuildAgentEnvFlag(%q) expected an error, got none", in)
		}
	}
}
