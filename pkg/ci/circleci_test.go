package ci

import (
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// Provider interface compliance
// ────────────────────────────────────────────────────────────────────────────

func TestCircleCIProviderInterface(t *testing.T) {
	p := &CircleCIProvider{}

	if p.Name() != "circleci" {
		t.Errorf("Name() = %q, want circleci", p.Name())
	}
	if p.DisplayName() != "CircleCI" {
		t.Errorf("DisplayName() = %q, want CircleCI", p.DisplayName())
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

func TestCircleCICLILabels(t *testing.T) {
	l := (&CircleCIProvider{}).CLILabels()

	if l.SecretName != "circleci-runner-token" {
		t.Errorf("SecretName = %q, want circleci-runner-token", l.SecretName)
	}
	if l.RunnerComponent != "circleci-runner" {
		t.Errorf("RunnerComponent = %q", l.RunnerComponent)
	}
	if !strings.Contains(l.ActionsURLFmt, "circleci.com") {
		t.Error("ActionsURLFmt should contain circleci.com")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// RunnerAdapter
// ────────────────────────────────────────────────────────────────────────────

func TestCircleCIDefaultImage(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	img := a.DefaultImage()
	if !strings.Contains(img, "runner-agent") {
		t.Errorf("DefaultImage() = %q, should contain 'runner-agent'", img)
	}
}

func TestCircleCIDefaultTokenKey(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	if got := a.DefaultTokenKey(); got != "circleci-token" {
		t.Errorf("DefaultTokenKey() = %q, want circleci-token", got)
	}
}

func TestCircleCIAPIBaseURL(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	// CircleCI always returns the runner API URL regardless of input
	tests := []string{"https://circleci.com", "", "https://anything.com"}
	for _, input := range tests {
		if got := a.APIBaseURL(input); got != "https://runner.circleci.com/api/v3" {
			t.Errorf("APIBaseURL(%q) = %q, want https://runner.circleci.com/api/v3", input, got)
		}
	}
}

func TestCircleCIRunnerEnvVars(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "jeff",
		Repository:      "myorg/my-project",
		TokenSecretName: "my-secret",
		TokenSecretKey:  "circleci-token",
		CRName:          "pool-1",
		WorkDir:         "/work",
	}

	envVars := a.RunnerEnvVars(cfg)
	m := make(map[string]ContainerEnvVar)
	for _, e := range envVars {
		m[e.Name] = e
	}

	// CIRCLECI_RUNNER_API_AUTH_TOKEN should use SecretRef
	token, ok := m["CIRCLECI_RUNNER_API_AUTH_TOKEN"]
	if !ok {
		t.Fatal("missing CIRCLECI_RUNNER_API_AUTH_TOKEN")
	}
	if token.SecretRef == nil {
		t.Fatal("CIRCLECI_RUNNER_API_AUTH_TOKEN should use SecretRef")
	}

	// CIRCLECI_RUNNER_NAME
	if m["CIRCLECI_RUNNER_NAME"].Value != "jeff-pool-1" {
		t.Errorf("CIRCLECI_RUNNER_NAME = %q", m["CIRCLECI_RUNNER_NAME"].Value)
	}

	// CIRCLECI_RUNNER_WORK_DIR
	if m["CIRCLECI_RUNNER_WORK_DIR"].Value != "/work" {
		t.Errorf("CIRCLECI_RUNNER_WORK_DIR = %q", m["CIRCLECI_RUNNER_WORK_DIR"].Value)
	}

	// CIRCLECI_USERNAME
	if m["CIRCLECI_USERNAME"].Value != "jeff" {
		t.Errorf("CIRCLECI_USERNAME = %q", m["CIRCLECI_USERNAME"].Value)
	}

	// Should NOT have old env vars
	for _, removed := range []string{"CIRCLECI_PAT", "CIRCLECI_API_URL", "CIRCLECI_RESOURCE_CLASS", "CIRCLECI_ORG_SLUG"} {
		if _, exists := m[removed]; exists {
			t.Errorf("should not have %s env var (removed in simplification)", removed)
		}
	}
}

func TestCircleCIRunnerEnvVarsMinimal(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	cfg := RunnerEnvConfig{
		Username:        "jeff",
		Repository:      "standalone-project",
		TokenSecretName: "s",
		TokenSecretKey:  "k",
		CRName:          "p",
	}

	envVars := a.RunnerEnvVars(cfg)
	if len(envVars) != 4 {
		t.Errorf("expected 4 env vars, got %d", len(envVars))
	}
}

func TestCircleCIRunnerLabels(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	labels := a.RunnerLabels("jeff", "my-pool")

	if labels["app.kubernetes.io/component"] != "circleci-runner" {
		t.Errorf("component = %q", labels["app.kubernetes.io/component"])
	}
	if labels["apps.example.com/circleci-username"] != "jeff" {
		t.Errorf("username label = %q", labels["apps.example.com/circleci-username"])
	}

	// Email-style username must be sanitized for K8s label values
	emailLabels := a.RunnerLabels("Jeff.D.Vincent@gmail.com", "my-pool")
	if emailLabels["apps.example.com/circleci-username"] != "jeff.d.vincent-gmail.com" {
		t.Errorf("email username label = %q, want jeff.d.vincent-gmail.com", emailLabels["apps.example.com/circleci-username"])
	}
}

// ────────────────────────────────────────────────────────────────────────────
// StartupScript
// ────────────────────────────────────────────────────────────────────────────

func TestCircleCIStartupScript(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	script := a.StartupScript()

	if !strings.HasPrefix(script, "#!/bin/bash") {
		t.Error("script should start with #!/bin/bash")
	}

	checks := []string{
		"CIRCLECI_RUNNER_API_AUTH_TOKEN",
		"CIRCLECI_RUNNER_NAME",
		"cleanup",
		"trap cleanup",
		"SIGTERM",
		"/opt/circleci/bin/circleci-runner machine",
	}
	for _, c := range checks {
		if !strings.Contains(script, c) {
			t.Errorf("startup script missing %q", c)
		}
	}

	// Should NOT reference old PAT exchange flow
	for _, old := range []string{"CIRCLECI_PAT", "resource-class", "runner/token"} {
		if strings.Contains(script, old) {
			t.Errorf("startup script should not contain %q (removed PAT exchange)", old)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// WorkflowGenerator
// ────────────────────────────────────────────────────────────────────────────

func TestCircleCIDefaultOutputPath(t *testing.T) {
	g := &CircleCIWorkflowGenerator{}
	if got := g.DefaultOutputPath(); got != ".circleci/config.yml" {
		t.Errorf("DefaultOutputPath() = %q", got)
	}
}

func TestCircleCISystemPrompt(t *testing.T) {
	g := &CircleCIWorkflowGenerator{}
	prompt := g.SystemPrompt("arm64")

	checks := []string{
		"CircleCI",
		".circleci/config.yml",
		"registry:5000",
		"resource class",
		"CIRCLE_USERNAME",
		"$BASH_ENV",
		"Do NOT put TAG in the environment: block",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Errorf("SystemPrompt missing %q", c)
		}
	}

	if strings.Contains(prompt, "HOSTARCH") {
		t.Error("HOSTARCH placeholder not replaced")
	}
}

func TestCircleCIPromptContext(t *testing.T) {
	g := &CircleCIWorkflowGenerator{}
	ctx := g.PromptContext()

	if ctx.PlatformName != "CircleCI" {
		t.Errorf("PlatformName = %q", ctx.PlatformName)
	}
	if ctx.WorkflowNoun != "config" {
		t.Errorf("WorkflowNoun = %q", ctx.WorkflowNoun)
	}
	if !strings.Contains(ctx.ActorExpr, "CIRCLE_USERNAME") {
		t.Errorf("ActorExpr = %q", ctx.ActorExpr)
	}
	if ctx.WorkspaceExpr != "~/project" {
		t.Errorf("WorkspaceExpr = %q", ctx.WorkspaceExpr)
	}
	if ctx.TriggerBlock == nil {
		t.Fatal("TriggerBlock is nil")
	}

	// Test TriggerBlock
	trigger := ctx.TriggerBlock("main")
	if !strings.Contains(trigger, "main") {
		t.Error("TriggerBlock should contain branch name")
	}
	if !strings.Contains(trigger, "workflows") {
		t.Error("TriggerBlock should contain 'workflows'")
	}
}

func TestCircleCIExampleWorkflows(t *testing.T) {
	g := &CircleCIWorkflowGenerator{}
	single, multi := g.ExampleWorkflows()

	if single == "" || multi == "" {
		t.Fatal("examples should not be empty")
	}

	if !strings.Contains(single, "version: 2.1") {
		t.Error("single should have version 2.1")
	}
	if !strings.Contains(single, "CIRCLE_USERNAME") {
		t.Error("single should use CIRCLE_USERNAME")
	}
	if !strings.Contains(multi, "workflows") {
		t.Error("multi should have workflows section")
	}
	if !strings.Contains(multi, "requires") {
		t.Error("multi should use requires for job ordering")
	}

	// TAG must NOT be in environment: block (CircleCI can't evaluate bash substrings there)
	for _, ex := range []struct{ name, content string }{{"single", single}, {"multi", multi}} {
		if strings.Contains(ex.content, `TAG: "${CIRCLE_USERNAME}`) {
			t.Errorf("%s example still has TAG in environment: block — use $BASH_ENV instead", ex.name)
		}
		// Must use $BASH_ENV to compute TAG
		if !strings.Contains(ex.content, `$BASH_ENV`) {
			t.Errorf("%s example missing $BASH_ENV pattern for computed TAG", ex.name)
		}
		// Must NOT use broad wildcard rm -f /builds/* (races with parallel jobs)
		if strings.Contains(ex.content, `rm -f /builds/*`) {
			t.Errorf("%s example uses broad 'rm -f /builds/*' — scope per service", ex.name)
		}
	}
}

func TestCircleCIStripTemplateExpressions(t *testing.T) {
	g := &CircleCIWorkflowGenerator{}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"circle_username_braces",
			"name: ${CIRCLE_USERNAME}-app",
			"name: ACTOR-app",
		},
		{
			"circle_username_dollar",
			"name: $CIRCLE_USERNAME-app",
			"name: ACTOR-app",
		},
		{
			"sha_short",
			"tag: ${CIRCLE_SHA1:0:8}",
			"tag: SHA",
		},
		{
			"sha_full_braces",
			"tag: ${CIRCLE_SHA1}",
			"tag: SHA",
		},
		{
			"sha_full_dollar",
			"tag: $CIRCLE_SHA1",
			"tag: SHA",
		},
		{
			"registry_and_tag",
			"${REGISTRY}/app:${TAG}",
			"REGISTRY/app:TAG",
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

// ────────────────────────────────────────────────────────────────────────────
// Naming inheritance
// ────────────────────────────────────────────────────────────────────────────

func TestCircleCINamingInheritance(t *testing.T) {
	a := &CircleCIRunnerAdapter{}
	user := "charlie"
	expected := "charlie-runner"

	if a.DeploymentName(user) != expected {
		t.Errorf("DeploymentName = %q", a.DeploymentName(user))
	}
	if a.ServiceAccountName(user) != expected {
		t.Errorf("ServiceAccountName = %q", a.ServiceAccountName(user))
	}
}
