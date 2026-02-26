package ci

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// ContainerEnvVar — Value vs SecretRef
// ────────────────────────────────────────────────────────────────────────────

func TestContainerEnvVarPlainValue(t *testing.T) {
	e := ContainerEnvVar{
		Name:  "MY_VAR",
		Value: "my-value",
	}
	if e.Name != "MY_VAR" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.Value != "my-value" {
		t.Errorf("Value = %q", e.Value)
	}
	if e.SecretRef != nil {
		t.Error("SecretRef should be nil for plain values")
	}
}

func TestContainerEnvVarSecretRef(t *testing.T) {
	e := ContainerEnvVar{
		Name: "GITHUB_PAT",
		SecretRef: &SecretRef{
			Name: "github-runner-token",
			Key:  "github-token",
		},
	}
	if e.Value != "" {
		t.Error("Value should be empty when using SecretRef")
	}
	if e.SecretRef == nil {
		t.Fatal("SecretRef should not be nil")
	}
	if e.SecretRef.Name != "github-runner-token" {
		t.Errorf("SecretRef.Name = %q", e.SecretRef.Name)
	}
	if e.SecretRef.Key != "github-token" {
		t.Errorf("SecretRef.Key = %q", e.SecretRef.Key)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// RunnerEnvConfig
// ────────────────────────────────────────────────────────────────────────────

func TestRunnerEnvConfigFields(t *testing.T) {
	cfg := RunnerEnvConfig{
		Username:        "jeff",
		Repository:      "jeff/kindling",
		PlatformURL:     "https://github.com",
		TokenSecretName: "gh-secret",
		TokenSecretKey:  "token",
		Labels:          []string{"self-hosted", "kindling"},
		RunnerGroup:     "my-group",
		WorkDir:         "/builds",
		CRName:          "pool-1",
	}

	if cfg.Username != "jeff" {
		t.Errorf("Username = %q", cfg.Username)
	}
	if cfg.Repository != "jeff/kindling" {
		t.Errorf("Repository = %q", cfg.Repository)
	}
	if len(cfg.Labels) != 2 {
		t.Errorf("Labels len = %d, want 2", len(cfg.Labels))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CLILabels
// ────────────────────────────────────────────────────────────────────────────

func TestCLILabelsAllFields(t *testing.T) {
	l := CLILabels{
		Username:        "GitHub username",
		Repository:      "GitHub repository (owner/repo)",
		Token:           "GitHub PAT (repo scope)",
		SecretName:      "github-runner-token",
		CRDKind:         "GithubActionRunnerPool",
		CRDPlural:       "githubactionrunnerpools",
		CRDListHeader:   "GitHub Actions Runner Pools",
		RunnerComponent: "github-actions-runner",
		ActionsURLFmt:   "https://github.com/%s/actions",
		CRDAPIVersion:   "apps.example.com/v1alpha1",
	}

	if l.Username == "" || l.Repository == "" || l.Token == "" ||
		l.SecretName == "" || l.CRDKind == "" || l.CRDPlural == "" ||
		l.CRDListHeader == "" || l.RunnerComponent == "" ||
		l.ActionsURLFmt == "" || l.CRDAPIVersion == "" {
		t.Error("all CLILabels fields should be non-empty")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// PromptContext
// ────────────────────────────────────────────────────────────────────────────

func TestPromptContextFields(t *testing.T) {
	ctx := PromptContext{
		PlatformName:            "GitHub Actions",
		WorkflowNoun:            "workflow",
		BuildActionRef:          "kindling-sh/kindling/.github/actions/kindling-build@main",
		DeployActionRef:         "kindling-sh/kindling/.github/actions/kindling-deploy@main",
		CheckoutAction:          "actions/checkout@v4",
		ActorExpr:               "${{ github.actor }}",
		SHAExpr:                 "${{ github.sha }}",
		WorkspaceExpr:           "${{ github.workspace }}",
		RunnerSpec:              `[self-hosted, "${{ github.actor }}"]`,
		EnvTagExpr:              "${{ github.actor }}-${{ github.sha }}",
		TriggerBlock:            func(branch string) string { return "on:\n  push:\n    branches: [" + branch + "]" },
		WorkflowFileDescription: "GitHub Actions workflow",
	}

	if ctx.PlatformName == "" || ctx.WorkflowNoun == "" ||
		ctx.ActorExpr == "" || ctx.SHAExpr == "" || ctx.WorkspaceExpr == "" {
		t.Error("PromptContext required fields should be non-empty")
	}

	trigger := ctx.TriggerBlock("main")
	if trigger == "" {
		t.Error("TriggerBlock should return non-empty string")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// SecretRef
// ────────────────────────────────────────────────────────────────────────────

func TestSecretRefFields(t *testing.T) {
	ref := SecretRef{
		Name: "my-secret",
		Key:  "token",
	}
	if ref.Name != "my-secret" || ref.Key != "token" {
		t.Error("SecretRef fields mismatch")
	}
}
