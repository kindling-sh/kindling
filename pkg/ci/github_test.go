package ci

import (
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// Provider interface compliance
// ────────────────────────────────────────────────────────────────────────────

func TestGitHubProviderInterface(t *testing.T) {
	p := &GitHubProvider{}

	if p.Name() != "github" {
		t.Errorf("Name() = %q, want github", p.Name())
	}
	if p.DisplayName() != "GitHub Actions" {
		t.Errorf("DisplayName() = %q, want GitHub Actions", p.DisplayName())
	}
	if p.Runner() == nil {
		t.Fatal("Runner() returned nil")
	}
	if p.Workflow() == nil {
		t.Fatal("Workflow() returned nil")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CLILabels
// ────────────────────────────────────────────────────────────────────────────

func TestGitHubCLILabels(t *testing.T) {
	l := (&GitHubProvider{}).CLILabels()

	checks := map[string]string{
		"Username":        l.Username,
		"Repository":      l.Repository,
		"Token":           l.Token,
		"SecretName":      l.SecretName,
		"CRDKind":         l.CRDKind,
		"CRDPlural":       l.CRDPlural,
		"RunnerComponent": l.RunnerComponent,
		"CRDAPIVersion":   l.CRDAPIVersion,
	}
	for field, value := range checks {
		if value == "" {
			t.Errorf("CLILabels.%s is empty", field)
		}
	}

	if l.SecretName != "github-runner-token" {
		t.Errorf("SecretName = %q, want github-runner-token", l.SecretName)
	}
	if l.RunnerComponent != "github-actions-runner" {
		t.Errorf("RunnerComponent = %q, want github-actions-runner", l.RunnerComponent)
	}
	if !strings.Contains(l.ActionsURLFmt, "%s") {
		t.Error("ActionsURLFmt missing percent-s placeholder")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// RunnerAdapter
// ────────────────────────────────────────────────────────────────────────────

func TestGitHubDefaultImage(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	img := a.DefaultImage()
	if !strings.Contains(img, "actions-runner") {
		t.Errorf("DefaultImage() = %q, should contain 'actions-runner'", img)
	}
}

func TestGitHubDefaultTokenKey(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	if got := a.DefaultTokenKey(); got != "github-token" {
		t.Errorf("DefaultTokenKey() = %q, want github-token", got)
	}
}

func TestGitHubAPIBaseURL(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com", "https://api.github.com"},
		{"https://github.com/", "https://api.github.com"},
		{"", "https://api.github.com"},
		{"https://git.corp.com", "https://git.corp.com/api/v3"},
		{"https://git.corp.com/", "https://git.corp.com/api/v3"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := a.APIBaseURL(tt.input); got != tt.want {
				t.Errorf("APIBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGitHubRunnerEnvVars(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "jeff",
		Repository:      "jeff/my-repo",
		PlatformURL:     "https://github.com",
		TokenSecretName: "my-secret",
		TokenSecretKey:  "github-token",
		Labels:          []string{"kindling"},
		CRName:          "pool-1",
		WorkDir:         "/work",
	}

	envVars := a.RunnerEnvVars(cfg)

	// Build a map for easier lookup
	m := make(map[string]ContainerEnvVar)
	for _, e := range envVars {
		m[e.Name] = e
	}

	// GITHUB_PAT should use SecretRef
	pat, ok := m["GITHUB_PAT"]
	if !ok {
		t.Fatal("missing GITHUB_PAT env var")
	}
	if pat.SecretRef == nil {
		t.Fatal("GITHUB_PAT should use SecretRef")
	}
	if pat.SecretRef.Name != "my-secret" {
		t.Errorf("GITHUB_PAT SecretRef.Name = %q, want my-secret", pat.SecretRef.Name)
	}
	if pat.SecretRef.Key != "github-token" {
		t.Errorf("GITHUB_PAT SecretRef.Key = %q, want github-token", pat.SecretRef.Key)
	}

	// RUNNER_REPOSITORY_URL
	if m["RUNNER_REPOSITORY_URL"].Value != "https://github.com/jeff/my-repo" {
		t.Errorf("RUNNER_REPOSITORY_URL = %q", m["RUNNER_REPOSITORY_URL"].Value)
	}

	// GITHUB_API_URL
	if m["GITHUB_API_URL"].Value != "https://api.github.com" {
		t.Errorf("GITHUB_API_URL = %q", m["GITHUB_API_URL"].Value)
	}

	// RUNNER_LABELS should contain self-hosted, username, and extra labels
	labels := m["RUNNER_LABELS"].Value
	if !strings.Contains(labels, "self-hosted") {
		t.Error("RUNNER_LABELS missing 'self-hosted'")
	}
	if !strings.Contains(labels, "jeff") {
		t.Error("RUNNER_LABELS missing username")
	}
	if !strings.Contains(labels, "kindling") {
		t.Error("RUNNER_LABELS missing extra label")
	}

	// GITHUB_USERNAME
	if m["GITHUB_USERNAME"].Value != "jeff" {
		t.Errorf("GITHUB_USERNAME = %q, want jeff", m["GITHUB_USERNAME"].Value)
	}

	// RUNNER_EPHEMERAL
	if m["RUNNER_EPHEMERAL"].Value != "false" {
		t.Errorf("RUNNER_EPHEMERAL = %q, want false", m["RUNNER_EPHEMERAL"].Value)
	}
}

func TestGitHubRunnerEnvVarsDefaultPlatform(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "user1",
		Repository:      "user1/repo",
		PlatformURL:     "", // should default to github.com
		TokenSecretName: "s",
		TokenSecretKey:  "k",
	}

	envVars := a.RunnerEnvVars(cfg)
	m := make(map[string]ContainerEnvVar)
	for _, e := range envVars {
		m[e.Name] = e
	}

	if !strings.Contains(m["RUNNER_REPOSITORY_URL"].Value, "https://github.com") {
		t.Error("empty PlatformURL should default to github.com")
	}
}

func TestGitHubRunnerEnvVarsWithRunnerGroup(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "user1",
		Repository:      "user1/repo",
		TokenSecretName: "s",
		TokenSecretKey:  "k",
		RunnerGroup:     "my-group",
	}

	envVars := a.RunnerEnvVars(cfg)
	m := make(map[string]ContainerEnvVar)
	for _, e := range envVars {
		m[e.Name] = e
	}

	if m["RUNNER_GROUP"].Value != "my-group" {
		t.Errorf("RUNNER_GROUP = %q, want my-group", m["RUNNER_GROUP"].Value)
	}
}

func TestGitHubRunnerLabels(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	labels := a.RunnerLabels("jeff", "my-pool")

	expectedKeys := []string{
		"app.kubernetes.io/name",
		"app.kubernetes.io/component",
		"app.kubernetes.io/managed-by",
		"app.kubernetes.io/instance",
		"apps.example.com/github-username",
	}
	for _, k := range expectedKeys {
		if _, ok := labels[k]; !ok {
			t.Errorf("missing label %q", k)
		}
	}
	if labels["app.kubernetes.io/name"] != "my-pool" {
		t.Errorf("name label = %q, want my-pool", labels["app.kubernetes.io/name"])
	}
	if labels["apps.example.com/github-username"] != "jeff" {
		t.Errorf("username label = %q, want jeff", labels["apps.example.com/github-username"])
	}
	if labels["app.kubernetes.io/component"] != "github-actions-runner" {
		t.Errorf("component label = %q", labels["app.kubernetes.io/component"])
	}
}

// ────────────────────────────────────────────────────────────────────────────
// StartupScript
// ────────────────────────────────────────────────────────────────────────────

func TestGitHubStartupScript(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	script := a.StartupScript()

	// Should start with shebang
	if !strings.HasPrefix(script, "#!/bin/bash") {
		t.Error("script should start with #!/bin/bash")
	}

	// Key elements
	checks := []string{
		"GITHUB_PAT",
		"registration-token",
		"config.sh",
		"run.sh",
		"cleanup",
		"trap cleanup",
		"SIGTERM",
		"RUNNER_LABELS",
	}
	for _, c := range checks {
		if !strings.Contains(script, c) {
			t.Errorf("startup script missing %q", c)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// WorkflowGenerator
// ────────────────────────────────────────────────────────────────────────────

func TestGitHubDefaultOutputPath(t *testing.T) {
	g := &GitHubWorkflowGenerator{}
	if got := g.DefaultOutputPath(); got != ".github/workflows/dev-deploy.yml" {
		t.Errorf("DefaultOutputPath() = %q", got)
	}
}

func TestGitHubSystemPrompt(t *testing.T) {
	g := &GitHubWorkflowGenerator{}
	prompt := g.SystemPrompt("arm64")

	// Should contain GitHub-specific content
	checks := []string{
		"GitHub Actions",
		"kindling-build",
		"kindling-deploy",
		"runs-on:",
		"github.actor",
		"actions/checkout@v4",
		"registry:5000",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Errorf("SystemPrompt missing %q", c)
		}
	}

	// Should have arch substituted
	if strings.Contains(prompt, "HOSTARCH") {
		t.Error("HOSTARCH placeholder not replaced")
	}
	if !strings.Contains(prompt, "arm64") {
		t.Error("arm64 not substituted into prompt")
	}
}

func TestGitHubSystemPromptAmd64(t *testing.T) {
	g := &GitHubWorkflowGenerator{}
	prompt := g.SystemPrompt("amd64")
	if !strings.Contains(prompt, "amd64") {
		t.Error("amd64 not substituted into prompt")
	}
}

func TestGitHubPromptContext(t *testing.T) {
	g := &GitHubWorkflowGenerator{}
	ctx := g.PromptContext()

	if ctx.PlatformName != "GitHub Actions" {
		t.Errorf("PlatformName = %q", ctx.PlatformName)
	}
	if ctx.WorkflowNoun != "workflow" {
		t.Errorf("WorkflowNoun = %q", ctx.WorkflowNoun)
	}
	if !strings.Contains(ctx.ActorExpr, "github.actor") {
		t.Errorf("ActorExpr = %q, should contain github.actor", ctx.ActorExpr)
	}
	if !strings.Contains(ctx.SHAExpr, "github.sha") {
		t.Errorf("SHAExpr = %q", ctx.SHAExpr)
	}
	if !strings.Contains(ctx.WorkspaceExpr, "github.workspace") {
		t.Errorf("WorkspaceExpr = %q", ctx.WorkspaceExpr)
	}
	if ctx.RunnerSpec == "" {
		t.Error("RunnerSpec is empty")
	}
	if ctx.EnvTagExpr == "" {
		t.Error("EnvTagExpr is empty")
	}
	if ctx.TriggerBlock == nil {
		t.Fatal("TriggerBlock is nil")
	}
	if ctx.WorkflowFileDescription == "" {
		t.Error("WorkflowFileDescription is empty")
	}

	// Test TriggerBlock
	trigger := ctx.TriggerBlock("main")
	if !strings.Contains(trigger, "main") {
		t.Errorf("TriggerBlock(main) = %q, should contain 'main'", trigger)
	}
	if !strings.Contains(trigger, "workflow_dispatch") {
		t.Errorf("TriggerBlock should include workflow_dispatch")
	}
}

func TestGitHubExampleWorkflows(t *testing.T) {
	g := &GitHubWorkflowGenerator{}
	single, multi := g.ExampleWorkflows()

	if single == "" {
		t.Error("single service example is empty")
	}
	if multi == "" {
		t.Error("multi service example is empty")
	}
	if !strings.Contains(single, "kindling-build") {
		t.Error("single example missing kindling-build")
	}
	if !strings.Contains(multi, "Build API") || !strings.Contains(multi, "Build UI") {
		t.Error("multi example should have Build API and Build UI")
	}
	if !strings.Contains(single, "github.actor") {
		t.Error("single example missing github.actor")
	}
}

func TestGitHubStripTemplateExpressions(t *testing.T) {
	g := &GitHubWorkflowGenerator{}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"actor",
			"image: ${{ github.actor }}-app",
			"image: ACTOR-app",
		},
		{
			"sha",
			"tag: ${{ github.sha }}",
			"tag: SHA",
		},
		{
			"workspace",
			"dir: ${{ github.workspace }}/src",
			"dir: WORKSPACE/src",
		},
		{
			"registry",
			"${{ env.REGISTRY }}/app:${{ env.TAG }}",
			"REGISTRY/app:TAG",
		},
		{
			"multiple",
			"${{ github.actor }}-${{ github.sha }}",
			"ACTOR-SHA",
		},
		{
			"no_expressions",
			"plain text without expressions",
			"plain text without expressions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.StripTemplateExpressions(tt.input)
			if got != tt.want {
				t.Errorf("StripTemplateExpressions(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Verify naming methods are inherited from BaseRunnerAdapter
func TestGitHubNamingInheritance(t *testing.T) {
	a := &GitHubRunnerAdapter{}
	user := "alice"
	expected := "alice-runner"

	if a.DeploymentName(user) != expected {
		t.Errorf("DeploymentName = %q", a.DeploymentName(user))
	}
	if a.ServiceAccountName(user) != expected {
		t.Errorf("ServiceAccountName = %q", a.ServiceAccountName(user))
	}
	if a.ClusterRoleName(user) != expected {
		t.Errorf("ClusterRoleName = %q", a.ClusterRoleName(user))
	}
	if a.ClusterRoleBindingName(user) != expected {
		t.Errorf("ClusterRoleBindingName = %q", a.ClusterRoleBindingName(user))
	}
}
