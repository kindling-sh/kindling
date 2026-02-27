package ci

import (
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// Provider interface compliance
// ────────────────────────────────────────────────────────────────────────────

func TestGitLabProviderInterface(t *testing.T) {
	p := &GitLabProvider{}

	if p.Name() != "gitlab" {
		t.Errorf("Name() = %q, want gitlab", p.Name())
	}
	if p.DisplayName() != "GitLab CI" {
		t.Errorf("DisplayName() = %q, want GitLab CI", p.DisplayName())
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

func TestGitLabCLILabels(t *testing.T) {
	l := (&GitLabProvider{}).CLILabels()

	if l.SecretName != "gitlab-runner-token" {
		t.Errorf("SecretName = %q, want gitlab-runner-token", l.SecretName)
	}
	if l.RunnerComponent != "gitlab-ci-runner" {
		t.Errorf("RunnerComponent = %q, want gitlab-ci-runner", l.RunnerComponent)
	}
	if !strings.Contains(l.ActionsURLFmt, "gitlab.com") {
		t.Error("ActionsURLFmt should contain gitlab.com")
	}
	if l.Token == "" {
		t.Error("Token label is empty")
	}
	if !strings.Contains(l.Token, "create_runner") {
		t.Error("Token should mention create_runner scope")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// RunnerAdapter
// ────────────────────────────────────────────────────────────────────────────

func TestGitLabDefaultImage(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	img := a.DefaultImage()
	if !strings.Contains(img, "gitlab-runner") {
		t.Errorf("DefaultImage() = %q, should contain 'gitlab-runner'", img)
	}
}

func TestGitLabDefaultTokenKey(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	if got := a.DefaultTokenKey(); got != "gitlab-token" {
		t.Errorf("DefaultTokenKey() = %q, want gitlab-token", got)
	}
}

func TestGitLabAPIBaseURL(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	tests := []struct {
		input string
		want  string
	}{
		{"https://gitlab.com", "https://gitlab.com/api/v4"},
		{"https://gitlab.com/", "https://gitlab.com/api/v4"},
		{"", "https://gitlab.com/api/v4"},
		{"https://gitlab.corp.com", "https://gitlab.corp.com/api/v4"},
		{"https://gitlab.corp.com/", "https://gitlab.corp.com/api/v4"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := a.APIBaseURL(tt.input); got != tt.want {
				t.Errorf("APIBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGitLabRunnerEnvVars(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "jeff",
		Repository:      "jeff/my-project",
		PlatformURL:     "https://gitlab.com",
		TokenSecretName: "my-secret",
		TokenSecretKey:  "gitlab-token",
		Labels:          []string{"kindling"},
		CRName:          "pool-1",
	}

	envVars := a.RunnerEnvVars(cfg)

	m := make(map[string]ContainerEnvVar)
	for _, e := range envVars {
		m[e.Name] = e
	}

	// GITLAB_PAT should use SecretRef
	pat, ok := m["GITLAB_PAT"]
	if !ok {
		t.Fatal("missing GITLAB_PAT env var")
	}
	if pat.SecretRef == nil {
		t.Fatal("GITLAB_PAT should use SecretRef")
	}
	if pat.SecretRef.Name != "my-secret" {
		t.Errorf("SecretRef.Name = %q", pat.SecretRef.Name)
	}

	// CI_SERVER_URL
	if m["CI_SERVER_URL"].Value != "https://gitlab.com" {
		t.Errorf("CI_SERVER_URL = %q", m["CI_SERVER_URL"].Value)
	}

	// GITLAB_API_URL
	if m["GITLAB_API_URL"].Value != "https://gitlab.com/api/v4" {
		t.Errorf("GITLAB_API_URL = %q", m["GITLAB_API_URL"].Value)
	}

	// GITLAB_PROJECT_PATH
	if m["GITLAB_PROJECT_PATH"].Value != "jeff/my-project" {
		t.Errorf("GITLAB_PROJECT_PATH = %q", m["GITLAB_PROJECT_PATH"].Value)
	}

	// RUNNER_NAME
	if m["RUNNER_NAME"].Value != "jeff-pool-1" {
		t.Errorf("RUNNER_NAME = %q", m["RUNNER_NAME"].Value)
	}

	// RUNNER_TAG_LIST
	tags := m["RUNNER_TAG_LIST"].Value
	if !strings.Contains(tags, "self-hosted") {
		t.Error("RUNNER_TAG_LIST missing 'self-hosted'")
	}
	if !strings.Contains(tags, "jeff") {
		t.Error("RUNNER_TAG_LIST missing username")
	}
	if !strings.Contains(tags, "kindling") {
		t.Error("RUNNER_TAG_LIST missing extra label")
	}

	// RUNNER_EXECUTOR
	if m["RUNNER_EXECUTOR"].Value != "shell" {
		t.Errorf("RUNNER_EXECUTOR = %q, want shell", m["RUNNER_EXECUTOR"].Value)
	}

	// GITLAB_USERNAME
	if m["GITLAB_USERNAME"].Value != "jeff" {
		t.Errorf("GITLAB_USERNAME = %q", m["GITLAB_USERNAME"].Value)
	}
}

func TestGitLabRunnerEnvVarsDefaultPlatform(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "user1",
		Repository:      "user1/proj",
		PlatformURL:     "",
		TokenSecretName: "s",
		TokenSecretKey:  "k",
	}

	envVars := a.RunnerEnvVars(cfg)
	m := make(map[string]ContainerEnvVar)
	for _, e := range envVars {
		m[e.Name] = e
	}

	if m["CI_SERVER_URL"].Value != "https://gitlab.com" {
		t.Error("empty PlatformURL should default to gitlab.com")
	}
}

func TestGitLabRunnerEnvVarsWithRunnerGroup(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "user1",
		Repository:      "user1/proj",
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

func TestGitLabRunnerLabels(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	labels := a.RunnerLabels("jeff", "my-pool")

	if labels["app.kubernetes.io/name"] != "my-pool" {
		t.Errorf("name label = %q", labels["app.kubernetes.io/name"])
	}
	if labels["app.kubernetes.io/component"] != "gitlab-ci-runner" {
		t.Errorf("component label = %q", labels["app.kubernetes.io/component"])
	}
	if labels["apps.example.com/gitlab-username"] != "jeff" {
		t.Errorf("username label = %q", labels["apps.example.com/gitlab-username"])
	}

	// Email-style username must be sanitized for K8s label values
	emailLabels := a.RunnerLabels("Jeff.D.Vincent@gmail.com", "my-pool")
	if emailLabels["apps.example.com/gitlab-username"] != "jeff.d.vincent-gmail.com" {
		t.Errorf("email username label = %q, want jeff.d.vincent-gmail.com", emailLabels["apps.example.com/gitlab-username"])
	}
}

// ────────────────────────────────────────────────────────────────────────────
// StartupScript
// ────────────────────────────────────────────────────────────────────────────

func TestGitLabStartupScript(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	script := a.StartupScript()

	if !strings.HasPrefix(script, "#!/bin/bash") {
		t.Error("script should start with #!/bin/bash")
	}

	// Must NOT use 'set -e' (causes silent failures)
	if strings.Contains(script, "set -euo") {
		t.Error("script must NOT use 'set -e' (causes silent script death)")
	}
	// Should use 'set -uo pipefail'
	if !strings.Contains(script, "set -uo pipefail") {
		t.Error("script should use 'set -uo pipefail'")
	}

	// Key elements for GitLab runner v18+ approach
	checks := []string{
		"GITLAB_PAT",
		"GITLAB_PROJECT_PATH",
		"config.toml",
		"cleanup",
		"trap cleanup",
		"SIGTERM",
		"/user/runners",       // API endpoint for runner creation
		"run_untagged",        // Must run untagged jobs
		"gitlab-runner run",   // Start command
		"--working-directory", // Working dir flag
	}
	for _, c := range checks {
		if !strings.Contains(script, c) {
			t.Errorf("startup script missing %q", c)
		}
	}

	// Must NOT invoke 'gitlab-runner register' as a command (broken in v18 with auth tokens).
	// The script may mention it in comments — that's fine.
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "gitlab-runner register") {
			t.Errorf("script should NOT invoke gitlab-runner register as a command, found: %s", trimmed)
		}
	}
}

func TestGitLabStartupScriptWritesConfigToml(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	script := a.StartupScript()

	// The script should write config.toml directly
	if !strings.Contains(script, "/etc/gitlab-runner/config.toml") {
		t.Error("script should write to /etc/gitlab-runner/config.toml")
	}
	if !strings.Contains(script, "[[runners]]") {
		t.Error("script should contain [[runners]] TOML section")
	}
	if !strings.Contains(script, "concurrent") {
		t.Error("script should set concurrent in config.toml")
	}
}

func TestGitLabStartupScriptCleanup(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	script := a.StartupScript()

	// Cleanup should delete runner via API
	if !strings.Contains(script, "DELETE") {
		t.Error("cleanup should use HTTP DELETE to remove runner")
	}
	if !strings.Contains(script, "RUNNER_ID") {
		t.Error("cleanup should reference RUNNER_ID")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// WorkflowGenerator
// ────────────────────────────────────────────────────────────────────────────

func TestGitLabDefaultOutputPath(t *testing.T) {
	g := &GitLabWorkflowGenerator{}
	if got := g.DefaultOutputPath(); got != ".gitlab-ci.yml" {
		t.Errorf("DefaultOutputPath() = %q, want .gitlab-ci.yml", got)
	}
}

func TestGitLabSystemPrompt(t *testing.T) {
	g := &GitLabWorkflowGenerator{}
	prompt := g.SystemPrompt("arm64")

	checks := []string{
		"GitLab CI",
		".gitlab-ci.yml",
		"registry:5000",
		"KINDLING_USER",
		"[self-hosted, kindling]",
		"stages",
		"heredoc",           // heredoc escaping guidance
		"GITLAB_USER_LOGIN", // warning about bot usernames
		"\\$(VAR_NAME)",     // escaped variable syntax
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Errorf("SystemPrompt missing %q", c)
		}
	}

	// Architecture substitution
	if strings.Contains(prompt, "HOSTARCH") {
		t.Error("HOSTARCH placeholder not replaced")
	}
}

func TestGitLabPromptContext(t *testing.T) {
	g := &GitLabWorkflowGenerator{}
	ctx := g.PromptContext()

	if ctx.PlatformName != "GitLab CI" {
		t.Errorf("PlatformName = %q", ctx.PlatformName)
	}
	if ctx.WorkflowNoun != "pipeline" {
		t.Errorf("WorkflowNoun = %q", ctx.WorkflowNoun)
	}
	// Uses KINDLING_USER, not GITLAB_USER_LOGIN
	if !strings.Contains(ctx.ActorExpr, "KINDLING_USER") {
		t.Errorf("ActorExpr = %q, should use KINDLING_USER", ctx.ActorExpr)
	}
	if !strings.Contains(ctx.SHAExpr, "CI_COMMIT_SHORT_SHA") {
		t.Errorf("SHAExpr = %q", ctx.SHAExpr)
	}
	if !strings.Contains(ctx.WorkspaceExpr, "CI_PROJECT_DIR") {
		t.Errorf("WorkspaceExpr = %q", ctx.WorkspaceExpr)
	}
	if !strings.Contains(ctx.RunnerSpec, "kindling") {
		t.Errorf("RunnerSpec = %q, should contain 'kindling'", ctx.RunnerSpec)
	}
	if !strings.Contains(ctx.EnvTagExpr, "KINDLING_USER") {
		t.Errorf("EnvTagExpr = %q, should use KINDLING_USER", ctx.EnvTagExpr)
	}
	if ctx.TriggerBlock == nil {
		t.Fatal("TriggerBlock is nil")
	}
	if ctx.WorkflowFileDescription != "GitLab CI pipeline" {
		t.Errorf("WorkflowFileDescription = %q", ctx.WorkflowFileDescription)
	}

	// Test TriggerBlock
	trigger := ctx.TriggerBlock("develop")
	if !strings.Contains(trigger, "develop") {
		t.Errorf("TriggerBlock(develop) should contain branch name")
	}
}

func TestGitLabExampleWorkflows(t *testing.T) {
	g := &GitLabWorkflowGenerator{}
	single, multi := g.ExampleWorkflows()

	if single == "" || multi == "" {
		t.Fatal("example workflows should not be empty")
	}

	// Both should use KINDLING_USER
	if !strings.Contains(single, "KINDLING_USER") {
		t.Error("single example should use KINDLING_USER")
	}
	if !strings.Contains(multi, "KINDLING_USER") {
		t.Error("multi example should use KINDLING_USER")
	}

	// Should use kindling tag, not user-specific tag
	if !strings.Contains(single, "kindling]") {
		t.Error("single example should use [self-hosted, kindling] tags")
	}

	// Should NOT reference $GITLAB_USER_LOGIN
	if strings.Contains(single, "$GITLAB_USER_LOGIN") {
		t.Error("single example should NOT use $GITLAB_USER_LOGIN")
	}
	if strings.Contains(multi, "$GITLAB_USER_LOGIN") {
		t.Error("multi example should NOT use $GITLAB_USER_LOGIN")
	}

	// Should have stages
	if !strings.Contains(single, "stages:") {
		t.Error("single example should have stages")
	}
	if !strings.Contains(multi, "deploy-api") && !strings.Contains(multi, "deploy-ui") {
		t.Error("multi example should have deploy-api and deploy-ui")
	}
}

func TestGitLabStripTemplateExpressions(t *testing.T) {
	g := &GitLabWorkflowGenerator{}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"kindling_user_braces",
			"name: ${KINDLING_USER}-app",
			"name: ACTOR-app",
		},
		{
			"kindling_user_dollar",
			"name: $KINDLING_USER-app",
			"name: ACTOR-app",
		},
		{
			"gitlab_user_login_braces",
			"name: ${GITLAB_USER_LOGIN}-app",
			"name: ACTOR-app",
		},
		{
			"gitlab_user_login_dollar",
			"name: $GITLAB_USER_LOGIN-app",
			"name: ACTOR-app",
		},
		{
			"commit_sha",
			"tag: ${CI_COMMIT_SHORT_SHA}",
			"tag: SHA",
		},
		{
			"full_sha",
			"tag: ${CI_COMMIT_SHA}",
			"tag: SHA",
		},
		{
			"workspace",
			"dir: ${CI_PROJECT_DIR}/src",
			"dir: WORKSPACE/src",
		},
		{
			"registry_and_tag",
			"${REGISTRY}/app:${TAG}",
			"REGISTRY/app:TAG",
		},
		{
			"dollar_variants",
			"$REGISTRY/app:$TAG",
			"REGISTRY/app:TAG",
		},
		{
			"complex_combo",
			"${KINDLING_USER}-${CI_COMMIT_SHORT_SHA}",
			"ACTOR-SHA",
		},
		{
			"no_expressions",
			"plain text",
			"plain text",
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

// Verify naming methods inherited from BaseRunnerAdapter
func TestGitLabNamingInheritance(t *testing.T) {
	a := &GitLabRunnerAdapter{}
	user := "bob"
	expected := "bob-runner"

	if a.DeploymentName(user) != expected {
		t.Errorf("DeploymentName = %q", a.DeploymentName(user))
	}
	if a.ServiceAccountName(user) != expected {
		t.Errorf("ServiceAccountName = %q", a.ServiceAccountName(user))
	}
}
