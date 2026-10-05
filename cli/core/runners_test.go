package core

import (
	"strings"
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

// ────────────────────────────────────────────────────────────────────────────
// buildRunnerPoolCRYAML
// ────────────────────────────────────────────────────────────────────────────

func TestBuildRunnerPoolCRYAML_Defaults(t *testing.T) {
	cfg := RunnerPoolConfig{ClusterName: "dev", Username: "jeff", Repo: "jeff/repo"}
	yaml := buildRunnerPoolCRYAML(cfg, "CIRunnerPool", "github-runner-token", "github-token",
		"https://github.com", "ghcr.io/actions/actions-runner:latest", "jeff", "default")

	for _, want := range []string{
		`kind: CIRunnerPool`,
		`name: jeff-runner-pool`,
		`namespace: default`,
		`githubUsername: "jeff"`,
		`repository: "jeff/repo"`,
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("buildRunnerPoolCRYAML() missing %q in:\n%s", want, yaml)
		}
	}
	for _, notWant := range []string{"enableSnapshotDeploy", "localClusterName", "buildAgentEnv", "ciProvider"} {
		if strings.Contains(yaml, notWant) {
			t.Errorf("buildRunnerPoolCRYAML() unexpectedly contains %q (should be omitted by default):\n%s", notWant, yaml)
		}
	}
}

func TestBuildRunnerPoolCRYAML_SnapshotDeployAndBuildAgentEnv(t *testing.T) {
	cfg := RunnerPoolConfig{
		ClusterName:          "dev",
		Username:             "jeff",
		Repo:                 "jeff/repo",
		Provider:             "gitlab",
		EnableSnapshotDeploy: true,
		BuildAgentEnv: []BuildAgentEnvVar{
			{Name: "KINDLING_REGISTRY_USERNAME", SecretName: "registry-credentials", SecretKey: "username"},
			{Name: "KINDLING_REGISTRY_PASSWORD", SecretName: "registry-credentials", SecretKey: "password"},
		},
	}
	yaml := buildRunnerPoolCRYAML(cfg, "CIRunnerPool", "github-runner-token", "github-token",
		"https://gitlab.com", "image:latest", "jeff", "default")

	for _, want := range []string{
		`ciProvider: "gitlab"`,
		`enableSnapshotDeploy: true`,
		`localClusterName: "dev"`,
		"buildAgentEnv:\n    - name: KINDLING_REGISTRY_USERNAME",
		"          name: registry-credentials\n          key: username",
		"    - name: KINDLING_REGISTRY_PASSWORD",
		"          name: registry-credentials\n          key: password",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("buildRunnerPoolCRYAML() missing %q in:\n%s", want, yaml)
		}
	}

	// localClusterName must always equal ClusterName, not some other field --
	// the sidecar's kind-<name> context synthesis depends on this matching
	// exactly the cluster kindling is actually running against.
	if cfg.ClusterName != "dev" {
		t.Fatalf("test setup invariant broken")
	}
}
