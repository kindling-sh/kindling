package ci

import (
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// Prompt constants are non-empty
// ────────────────────────────────────────────────────────────────────────────

func TestPromptConstantsNonEmpty(t *testing.T) {
	constants := map[string]string{
		"PromptDockerfileExistence":     PromptDockerfileExistence,
		"PromptDeployInputs":            PromptDeployInputs,
		"PromptBuildInputs":             PromptBuildInputs,
		"PromptHealthChecks":            PromptHealthChecks,
		"PromptDependencyDetection":     PromptDependencyDetection,
		"PromptDependencyAutoInjection": PromptDependencyAutoInjection,
		"PromptBuildTimeout":            PromptBuildTimeout,
		"PromptKanakoPatching":          PromptKanakoPatching,
		"PromptDockerCompose":           PromptDockerCompose,
		"PromptDevStagingPhilosophy":    PromptDevStagingPhilosophy,
		"PromptOAuth":                   PromptOAuth,
		"PromptFinalValidation":         PromptFinalValidation,
	}

	for name, value := range constants {
		if value == "" {
			t.Errorf("%s is empty", name)
		}
		if len(value) < 50 {
			t.Errorf("%s is suspiciously short (%d chars)", name, len(value))
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Prompt content quality checks
// ────────────────────────────────────────────────────────────────────────────

func TestPromptDeployInputsContent(t *testing.T) {
	fields := []string{
		"name", "image", "port", "labels", "env", "dependencies",
		"ingress-host", "health-check-path", "health-check-type",
		"replicas", "service-type", "wait",
	}
	for _, f := range fields {
		if !strings.Contains(PromptDeployInputs, f) {
			t.Errorf("PromptDeployInputs missing field %q", f)
		}
	}
}

func TestPromptDependencyDetectionContent(t *testing.T) {
	deps := []string{
		"postgres", "redis", "mysql", "mongodb", "rabbitmq",
		"minio", "elasticsearch", "kafka", "nats", "memcached",
		"cassandra", "consul", "vault", "influxdb", "jaeger",
	}
	for _, d := range deps {
		if !strings.Contains(PromptDependencyDetection, d) {
			t.Errorf("PromptDependencyDetection missing dependency type %q", d)
		}
	}

	// Should cover multiple languages
	languages := []string{"Go", "Node", "Python", "Java", "Rust", "Ruby", "PHP", "C#", "Elixir"}
	for _, lang := range languages {
		if !strings.Contains(PromptDependencyDetection, lang) {
			t.Errorf("PromptDependencyDetection missing language %q", lang)
		}
	}
}

func TestPromptDependencyAutoInjectionContent(t *testing.T) {
	// Should list the auto-injected env var for each dependency type
	envVars := []string{
		"DATABASE_URL", "REDIS_URL", "MONGO_URL", "AMQP_URL",
		"S3_ENDPOINT", "ELASTICSEARCH_URL", "KAFKA_BROKER_URL",
		"NATS_URL", "MEMCACHED_URL", "CASSANDRA_URL",
		"CONSUL_HTTP_ADDR", "VAULT_ADDR", "INFLUXDB_URL", "JAEGER_ENDPOINT",
	}
	for _, ev := range envVars {
		if !strings.Contains(PromptDependencyAutoInjection, ev) {
			t.Errorf("PromptDependencyAutoInjection missing env var %q", ev)
		}
	}
}

func TestPromptHealthChecksContent(t *testing.T) {
	types := []string{"http", "grpc", "none"}
	for _, hcType := range types {
		if !strings.Contains(PromptHealthChecks, hcType) {
			t.Errorf("PromptHealthChecks missing type %q", hcType)
		}
	}
}

func TestPromptKanakoPatchingContent(t *testing.T) {
	issues := []string{
		"BUILDPLATFORM", "TARGETARCH", "TARGETPLATFORM",
		"poetry install", "--no-root",
		"npm_config_cache",
		"-buildvcs=false",
	}
	for _, issue := range issues {
		if !strings.Contains(PromptKanakoPatching, issue) {
			t.Errorf("PromptKanakoPatching missing %q", issue)
		}
	}
}

func TestPromptBuildTimeoutContent(t *testing.T) {
	// Should mention heavy languages
	langs := []string{"Rust", "Java", "C#", "Elixir"}
	for _, l := range langs {
		if !strings.Contains(PromptBuildTimeout, l) {
			t.Errorf("PromptBuildTimeout missing %q", l)
		}
	}
	if !strings.Contains(PromptBuildTimeout, "900") {
		t.Error("PromptBuildTimeout should recommend 900s for heavy builds")
	}
}

func TestPromptDockerComposeContent(t *testing.T) {
	if !strings.Contains(PromptDockerCompose, "docker-compose.yml") {
		t.Error("missing docker-compose.yml reference")
	}
	if !strings.Contains(PromptDockerCompose, "depends_on") {
		t.Error("missing depends_on reference")
	}
}

func TestPromptDevStagingPhilosophyContent(t *testing.T) {
	checks := []string{
		"SECRET_KEY",
		"hex",
		"secretKeyRef",
		".env.sample",
		"kindling secrets",
	}
	for _, c := range checks {
		if !strings.Contains(PromptDevStagingPhilosophy, c) {
			t.Errorf("PromptDevStagingPhilosophy missing %q", c)
		}
	}
}

func TestPromptOAuthContent(t *testing.T) {
	if !strings.Contains(PromptOAuth, "OAuth") {
		t.Error("PromptOAuth missing 'OAuth'")
	}
	if !strings.Contains(PromptOAuth, "kindling expose") {
		t.Error("PromptOAuth missing 'kindling expose'")
	}
}

func TestPromptFinalValidationContent(t *testing.T) {
	checks := []string{
		"AMQP_URL",
		"rabbitmq",
		"docker-compose",
		"YAML",
	}
	for _, c := range checks {
		if !strings.Contains(PromptFinalValidation, c) {
			t.Errorf("PromptFinalValidation missing %q", c)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// All providers incorporate shared prompts
// ────────────────────────────────────────────────────────────────────────────

func TestAllProvidersUseSharedPrompts(t *testing.T) {
	providers := []WorkflowGenerator{
		&GitHubWorkflowGenerator{},
		&GitLabWorkflowGenerator{},
	}

	// Each shared prompt constant should appear in every provider's SystemPrompt
	sharedFragments := []string{
		// From PromptDeployInputs
		"kindling-deploy inputs",
		// From PromptHealthChecks
		"health-check-type",
		// From PromptDependencyDetection
		"postgres, redis, mysql",
		// From PromptKanakoPatching
		"Kaniko",
		// From PromptFinalValidation
		"FINAL VALIDATION",
	}

	for i, p := range providers {
		prompt := p.SystemPrompt("arm64")
		for _, frag := range sharedFragments {
			if !strings.Contains(prompt, frag) {
				t.Errorf("provider[%d] SystemPrompt missing shared fragment %q", i, frag)
			}
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Cross-provider consistency
// ────────────────────────────────────────────────────────────────────────────

func TestAllProvidersHaveNonEmptyPromptContext(t *testing.T) {
	providers := []struct {
		name string
		wfg  WorkflowGenerator
	}{
		{"github", &GitHubWorkflowGenerator{}},
		{"gitlab", &GitLabWorkflowGenerator{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			ctx := p.wfg.PromptContext()

			if ctx.PlatformName == "" {
				t.Error("PlatformName is empty")
			}
			if ctx.WorkflowNoun == "" {
				t.Error("WorkflowNoun is empty")
			}
			if ctx.ActorExpr == "" {
				t.Error("ActorExpr is empty")
			}
			if ctx.SHAExpr == "" {
				t.Error("SHAExpr is empty")
			}
			if ctx.WorkspaceExpr == "" {
				t.Error("WorkspaceExpr is empty")
			}
			if ctx.RunnerSpec == "" {
				t.Error("RunnerSpec is empty")
			}
			if ctx.EnvTagExpr == "" {
				t.Error("EnvTagExpr is empty")
			}
			if ctx.TriggerBlock == nil {
				t.Error("TriggerBlock is nil")
			}
			if ctx.WorkflowFileDescription == "" {
				t.Error("WorkflowFileDescription is empty")
			}
		})
	}
}

func TestAllProvidersReturnExampleWorkflows(t *testing.T) {
	providers := []struct {
		name string
		wfg  WorkflowGenerator
	}{
		{"github", &GitHubWorkflowGenerator{}},
		{"gitlab", &GitLabWorkflowGenerator{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			single, multi := p.wfg.ExampleWorkflows()
			if single == "" {
				t.Error("single service example is empty")
			}
			if multi == "" {
				t.Error("multi service example is empty")
			}
			// Both should have registry:5000
			if !strings.Contains(single, "registry:5000") {
				t.Error("single example missing registry:5000")
			}
			if !strings.Contains(multi, "registry:5000") {
				t.Error("multi example missing registry:5000")
			}
		})
	}
}

func TestAllProvidersHaveDefaultOutputPath(t *testing.T) {
	providers := []struct {
		name string
		wfg  WorkflowGenerator
	}{
		{"github", &GitHubWorkflowGenerator{}},
		{"gitlab", &GitLabWorkflowGenerator{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			path := p.wfg.DefaultOutputPath()
			if path == "" {
				t.Error("DefaultOutputPath is empty")
			}
			if !strings.Contains(path, ".yml") {
				t.Errorf("DefaultOutputPath %q should have .yml extension", path)
			}
		})
	}
}

func TestAllProvidersStripTemplateExpressions(t *testing.T) {
	// Each provider's StripTemplateExpressions should handle at least
	// registry and tag variables
	providers := []struct {
		name string
		wfg  WorkflowGenerator
	}{
		{"github", &GitHubWorkflowGenerator{}},
		{"gitlab", &GitLabWorkflowGenerator{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			result := p.wfg.StripTemplateExpressions("${REGISTRY}/app:${TAG}")
			if !strings.Contains(result, "REGISTRY") || !strings.Contains(result, "TAG") {
				t.Errorf("StripTemplateExpressions should replace REGISTRY and TAG, got %q", result)
			}
		})
	}
}
