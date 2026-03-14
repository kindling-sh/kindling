package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Config tests ────────────────────────────────────────────────

func TestDefaultHardenConfig(t *testing.T) {
	cfg := defaultHardenConfig()

	if cfg.Severity != severityModerate {
		t.Errorf("default severity = %q, want %q", cfg.Severity, severityModerate)
	}
	if cfg.GateDeploy {
		t.Error("default gate-deploy should be false")
	}
	if !cfg.Categories.Security {
		t.Error("security category should be enabled by default")
	}
	if !cfg.Categories.Containers {
		t.Error("containers category should be enabled by default")
	}
	if !cfg.Categories.Scalability {
		t.Error("scalability category should be enabled by default")
	}
	if !cfg.Categories.Performance {
		t.Error("performance category should be enabled by default")
	}
	if !cfg.Categories.Observability {
		t.Error("observability category should be enabled by default")
	}
	if !cfg.Categories.CIHygiene {
		t.Error("ci-hygiene category should be enabled by default")
	}
	if cfg.Overrides == nil {
		t.Error("overrides map should be initialized")
	}
}

func TestLoadHardenConfig_Missing(t *testing.T) {
	dir := t.TempDir()
	cfg := loadHardenConfig(dir)

	if cfg.Severity != severityModerate {
		t.Errorf("missing config should return defaults, got severity %q", cfg.Severity)
	}
}

func TestLoadHardenConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	kindlingDir := filepath.Join(dir, ".kindling")
	os.MkdirAll(kindlingDir, 0755)

	content := `severity: strict
gate-deploy: true
categories:
  security: true
  scalability: false
  performance: true
  containers: true
  observability: false
  ci-hygiene: true
overrides:
  no-root-container: "off"
  pin-base-images: info
`
	os.WriteFile(filepath.Join(kindlingDir, "harden.yaml"), []byte(content), 0644)

	cfg := loadHardenConfig(dir)

	if cfg.Severity != severityStrict {
		t.Errorf("severity = %q, want strict", cfg.Severity)
	}
	if !cfg.GateDeploy {
		t.Error("gate-deploy should be true")
	}
	if cfg.Categories.Scalability {
		t.Error("scalability should be disabled")
	}
	if cfg.Categories.Observability {
		t.Error("observability should be disabled")
	}
	if cfg.Overrides["no-root-container"] != "off" {
		t.Errorf("override for no-root-container = %q, want off", cfg.Overrides["no-root-container"])
	}
	if cfg.Overrides["pin-base-images"] != "info" {
		t.Errorf("override for pin-base-images = %q, want info", cfg.Overrides["pin-base-images"])
	}
}

func TestLoadHardenConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	kindlingDir := filepath.Join(dir, ".kindling")
	os.MkdirAll(kindlingDir, 0755)
	os.WriteFile(filepath.Join(kindlingDir, "harden.yaml"), []byte("{{invalid yaml"), 0644)

	cfg := loadHardenConfig(dir)
	if cfg.Severity != severityModerate {
		t.Errorf("invalid YAML should return defaults, got severity %q", cfg.Severity)
	}
}

func TestWriteDefaultHardenConfig(t *testing.T) {
	dir := t.TempDir()
	err := writeDefaultHardenConfig(dir)
	if err != nil {
		t.Fatalf("writeDefaultHardenConfig() error: %v", err)
	}

	cfgPath := filepath.Join(dir, ".kindling", "harden.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "severity: moderate") {
		t.Error("config should contain 'severity: moderate'")
	}
	if !strings.Contains(content, "gate-deploy: false") {
		t.Error("config should contain 'gate-deploy: false'")
	}
	if !strings.Contains(content, "security: true") {
		t.Error("config should contain 'security: true'")
	}
}

func TestWriteDefaultHardenConfig_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	kindlingDir := filepath.Join(dir, ".kindling")
	os.MkdirAll(kindlingDir, 0755)
	os.WriteFile(filepath.Join(kindlingDir, "harden.yaml"), []byte("existing"), 0644)

	err := writeDefaultHardenConfig(dir)
	if err != nil {
		t.Fatalf("should not error when file exists: %v", err)
	}

	// Should not overwrite
	data, _ := os.ReadFile(filepath.Join(kindlingDir, "harden.yaml"))
	if string(data) != "existing" {
		t.Error("should not overwrite existing config")
	}
}

// ── Category gating tests ───────────────────────────────────────

func TestShouldRunCategory(t *testing.T) {
	cfg := defaultHardenConfig()
	cfg.Categories.Performance = false

	tests := []struct {
		name     string
		category hardenCategory
		want     bool
	}{
		{"security enabled", catSecurity, true},
		{"containers enabled", catContainers, true},
		{"performance disabled", catPerformance, false},
		{"observability enabled", catObservability, true},
	}

	// Clear the global flag for this test
	oldFlag := hardenCategoryFlag
	hardenCategoryFlag = ""
	defer func() { hardenCategoryFlag = oldFlag }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunCategory(cfg, tt.category)
			if got != tt.want {
				t.Errorf("shouldRunCategory(%s) = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}

func TestShouldRunCategory_CLIOverride(t *testing.T) {
	cfg := defaultHardenConfig()

	oldFlag := hardenCategoryFlag
	hardenCategoryFlag = "security"
	defer func() { hardenCategoryFlag = oldFlag }()

	if !shouldRunCategory(cfg, catSecurity) {
		t.Error("CLI flag 'security' should enable security category")
	}
	if shouldRunCategory(cfg, catContainers) {
		t.Error("CLI flag 'security' should disable containers category")
	}
}

// ── Override application tests ──────────────────────────────────

func TestApplyOverrides_RuleOff(t *testing.T) {
	findings := []hardenFinding{
		{RuleID: "no-root-container", Category: catContainers, Severity: ruleSevError, Message: "runs as root"},
		{RuleID: "pin-base-images", Category: catContainers, Severity: ruleSevWarning, Message: "unpinned"},
	}
	cfg := defaultHardenConfig()
	cfg.Overrides["no-root-container"] = "off"

	result := applyOverrides(findings, cfg)
	if len(result) != 1 {
		t.Fatalf("expected 1 finding after override, got %d", len(result))
	}
	if result[0].RuleID != "pin-base-images" {
		t.Errorf("remaining finding should be pin-base-images, got %s", result[0].RuleID)
	}
}

func TestApplyOverrides_SeverityChange(t *testing.T) {
	findings := []hardenFinding{
		{RuleID: "cors-wildcard", Category: catSecurity, Severity: ruleSevInfo, Message: "cors *"},
	}
	cfg := defaultHardenConfig()
	cfg.Overrides["cors-wildcard"] = "error"

	result := applyOverrides(findings, cfg)
	if result[0].Severity != ruleSevError {
		t.Errorf("severity should be overridden to error, got %s", result[0].Severity)
	}
}

func TestApplyOverrides_Gentle(t *testing.T) {
	findings := []hardenFinding{
		{RuleID: "no-root-container", Severity: ruleSevError},
		{RuleID: "pin-base-images", Severity: ruleSevWarning},
	}
	cfg := defaultHardenConfig()
	cfg.Severity = severityGentle

	result := applyOverrides(findings, cfg)
	for _, f := range result {
		if f.Severity != ruleSevInfo {
			t.Errorf("gentle mode should demote all to info, %s has %s", f.RuleID, f.Severity)
		}
	}
}

func TestApplyOverrides_Strict(t *testing.T) {
	findings := []hardenFinding{
		{RuleID: "pin-base-images", Severity: ruleSevWarning},
		{RuleID: "cors-wildcard", Severity: ruleSevInfo},
	}
	cfg := defaultHardenConfig()
	cfg.Severity = severityStrict

	result := applyOverrides(findings, cfg)
	if result[0].Severity != ruleSevError {
		t.Errorf("strict should promote warnings to error, got %s", result[0].Severity)
	}
	// Info stays info in strict mode
	if result[1].Severity != ruleSevInfo {
		t.Errorf("strict should keep info as info, got %s", result[1].Severity)
	}
}

// ── Security rule tests ─────────────────────────────────────────

func TestHardenHardcodedSecrets(t *testing.T) {
	tests := []struct {
		name    string
		content map[string]string
		wantAny bool
	}{
		{
			"clean code",
			map[string]string{"main.py": "import os\napi_key = os.environ['API_KEY']"},
			false,
		},
		{
			"openai key",
			map[string]string{"config.py": `API_KEY = "sk-abc123def456"`},
			true,
		},
		{
			"aws key",
			map[string]string{"deploy.sh": `export AWS_KEY=AKIAIOSFODNN7EXAMPLE`},
			true,
		},
		{
			"github token",
			map[string]string{"script.sh": `TOKEN=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`},
			true,
		},
		{
			"private key",
			map[string]string{"key.txt": "-----BEGIN RSA PRIVATE KEY-----\ndata\n-----END RSA PRIVATE KEY-----"},
			true,
		},
		{
			"env files skipped",
			map[string]string{".env": "sk-abc123def456"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := hardenHardcodedSecrets(tt.content)
			if tt.wantAny && len(findings) == 0 {
				t.Error("expected findings but got none")
			}
			if !tt.wantAny && len(findings) > 0 {
				t.Errorf("expected no findings but got %d: %s", len(findings), findings[0].Message)
			}
		})
	}
}

func TestHardenGitignoreHygiene(t *testing.T) {
	t.Run("no gitignore", func(t *testing.T) {
		dir := t.TempDir()
		findings := hardenGitignoreHygiene(dir)
		if len(findings) == 0 {
			t.Error("expected finding for missing .gitignore")
		}
		if findings[0].Severity != ruleSevWarning {
			t.Errorf("severity = %s, want warning", findings[0].Severity)
		}
	})

	t.Run("gitignore without env", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n*.log\n"), 0644)
		findings := hardenGitignoreHygiene(dir)
		if len(findings) == 0 {
			t.Error("expected finding for .gitignore missing .env")
		}
	})

	t.Run("gitignore with env", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\nnode_modules/\n"), 0644)
		findings := hardenGitignoreHygiene(dir)
		if len(findings) != 0 {
			t.Errorf("expected no findings, got %d", len(findings))
		}
	})
}

func TestHardenDependencyPinning(t *testing.T) {
	tests := []struct {
		name    string
		depFile string
		content string
		wantAny bool
	}{
		{
			"pinned requirements.txt",
			"requirements.txt",
			"flask==2.3.0\nrequests==2.31.0\n",
			false,
		},
		{
			"unpinned requirements.txt",
			"requirements.txt",
			"flask\nrequests\nnumpy\n",
			true,
		},
		{
			"package.json with wildcard",
			"package.json",
			`{"dependencies": {"express": "*"}}`,
			true,
		},
		{
			"package.json with latest",
			"package.json",
			`{"dependencies": {"express": "latest"}}`,
			true,
		},
		{
			"package.json pinned",
			"package.json",
			`{"dependencies": {"express": "^4.18.0"}}`,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &repoContext{
				depFiles: map[string]string{tt.depFile: tt.content},
			}
			findings := hardenDependencyPinning(ctx)
			if tt.wantAny && len(findings) == 0 {
				t.Error("expected findings but got none")
			}
			if !tt.wantAny && len(findings) > 0 {
				t.Errorf("expected no findings but got %d", len(findings))
			}
		})
	}
}

func TestHardenCodePatterns(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		ruleID string
	}{
		{
			"eval usage",
			"result = eval(user_input)",
			"no-eval",
		},
		{
			"shell injection",
			"os.system(cmd)",
			"no-shell-injection",
		},
		{
			"sql concatenation",
			`query = "SELECT * FROM users WHERE id=" + user_id`,
			"no-sql-concatenation",
		},
		{
			"cors wildcard with cors keyword",
			`cors_origin = "*"\n# cors config`,
			"cors-wildcard",
		},
		{
			"debug mode",
			"app.debug = true",
			"debug-mode-off",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := map[string]string{"app.py": tt.code}
			findings := hardenCodePatterns(content)

			found := false
			for _, f := range findings {
				if f.RuleID == tt.ruleID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected finding with rule %s", tt.ruleID)
			}
		})
	}

	t.Run("clean code", func(t *testing.T) {
		content := map[string]string{"app.py": "import os\nprint('hello')"}
		findings := hardenCodePatterns(content)
		if len(findings) != 0 {
			t.Errorf("expected no findings on clean code, got %d", len(findings))
		}
	})
}

// ── Container rule tests ────────────────────────────────────────

func TestHardenCheckContainers(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		wantRules  []string // rule IDs expected
	}{
		{
			"runs as root",
			"FROM node:18\nCOPY . .\nCMD [\"node\", \"app.js\"]",
			[]string{"no-root-container", "healthcheck-present"},
		},
		{
			"non-root user",
			"FROM node:18\nRUN adduser -D appuser\nUSER appuser\nCOPY . .\nHEALTHCHECK CMD curl -f http://localhost/ || exit 1\nCMD [\"node\", \"app.js\"]",
			[]string{}, // no root or healthcheck findings
		},
		{
			"unpinned base image",
			"FROM node\nUSER app\nHEALTHCHECK CMD true\nCOPY . .\nCMD [\"node\", \"app.js\"]",
			[]string{"pin-base-images"},
		},
		{
			"latest tag",
			"FROM python:latest\nUSER app\nHEALTHCHECK CMD true\nCMD [\"python\", \"app.py\"]",
			[]string{"pin-base-images"},
		},
		{
			"full OS base",
			"FROM ubuntu:22.04\nUSER app\nHEALTHCHECK CMD true\nCMD [\"/app\"]",
			[]string{"minimal-base-image"},
		},
		{
			"secrets in copy",
			"FROM alpine:3.18\nUSER app\nHEALTHCHECK CMD true\nCOPY .env /app/.env\nCMD [\"/app\"]",
			[]string{"no-secrets-in-layers"},
		},
		{
			"single stage with build tools",
			"FROM golang:1.21\nRUN go build -o app .\nUSER app\nHEALTHCHECK CMD true\nCMD [\"/app\"]",
			[]string{"multi-stage-build"},
		},
	}

	cfg := defaultHardenConfig()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &repoContext{
				dockerfiles: map[string]string{"Dockerfile": tt.dockerfile},
			}
			findings := hardenCheckContainers(ctx, cfg)

			foundRules := make(map[string]bool)
			for _, f := range findings {
				foundRules[f.RuleID] = true
			}

			for _, want := range tt.wantRules {
				if !foundRules[want] {
					t.Errorf("expected rule %s in findings", want)
				}
			}
		})
	}
}

// ── Scalability rule tests ──────────────────────────────────────

func TestHardenCheckScalability(t *testing.T) {
	t.Run("server without sigterm handler", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"server.py": "from aiohttp import web\napp = web.Application()\nweb.run_app(app)\n# app.run(",
			},
			dockerfiles: map[string]string{"Dockerfile": "FROM python:3.11"},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckScalability(ctx, cfg)

		found := false
		for _, f := range findings {
			if f.RuleID == "resource-limits" {
				found = true
			}
		}
		if !found {
			t.Error("expected resource-limits finding when dockerfiles present")
		}
	})

	t.Run("server with sigterm handler", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"server.go": "signal.Notify(sigs, syscall.SIGTERM)\nhttp.ListenAndServe(\":8080\", nil)",
			},
			dockerfiles: map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckScalability(ctx, cfg)

		for _, f := range findings {
			if f.RuleID == "graceful-shutdown" {
				t.Error("should not flag graceful-shutdown when signal handler exists")
			}
		}
	})
}

// ── Performance rule tests ──────────────────────────────────────

func TestHardenCheckPerformance(t *testing.T) {
	t.Run("sync io in async", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"handler.py": "import requests\nasync def fetch():\n    await something()\n    time.sleep(1)\n",
			},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckPerformance(ctx, cfg)

		found := false
		for _, f := range findings {
			if f.RuleID == "no-sync-io-in-async" {
				found = true
			}
		}
		if !found {
			t.Error("expected no-sync-io-in-async finding")
		}
	})

	t.Run("clean async", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"handler.py": "import aiohttp\nasync def fetch():\n    await asyncio.sleep(1)\n",
			},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckPerformance(ctx, cfg)

		for _, f := range findings {
			if f.RuleID == "no-sync-io-in-async" {
				t.Error("should not flag clean async code")
			}
		}
	})

	t.Run("n+1 query pattern", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"service.py": "for user in users:\n    db.execute('SELECT * FROM orders WHERE user_id=' + user.id)\n",
			},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckPerformance(ctx, cfg)

		found := false
		for _, f := range findings {
			if f.RuleID == "n-plus-one-queries" {
				found = true
			}
		}
		if !found {
			t.Error("expected n-plus-one-queries finding")
		}
	})
}

// ── Observability rule tests ────────────────────────────────────

func TestHardenCheckObservability(t *testing.T) {
	t.Run("print only logging", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"app.py": "print('starting server')\nprint('request received')\n",
			},
			depFiles: map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckObservability(ctx, cfg)

		foundLogging := false
		for _, f := range findings {
			if f.RuleID == "structured-logging" {
				foundLogging = true
			}
		}
		if !foundLogging {
			t.Error("expected structured-logging finding for print-only code")
		}
	})

	t.Run("structured logging present", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"app.py": "import structlog\nlogger = structlog.get_logger()\nlogger.info('started')\n",
			},
			depFiles: map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckObservability(ctx, cfg)

		for _, f := range findings {
			if f.RuleID == "structured-logging" {
				t.Error("should not flag when structured logging is present")
			}
		}
	})

	t.Run("no error tracking", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"app.py": "import logging\nlogging.info('hello')\n",
			},
			depFiles: map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckObservability(ctx, cfg)

		foundErrorReporting := false
		for _, f := range findings {
			if f.RuleID == "error-reporting" {
				foundErrorReporting = true
			}
		}
		if !foundErrorReporting {
			t.Error("expected error-reporting suggestion when no tracking integration found")
		}
	})

	t.Run("sentry present", func(t *testing.T) {
		ctx := &repoContext{
			sourceSnippets: map[string]string{
				"app.py": "import sentry_sdk\nsentry_sdk.init()\nlogging.info('hello')\n",
			},
			depFiles: map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckObservability(ctx, cfg)

		for _, f := range findings {
			if f.RuleID == "error-reporting" {
				t.Error("should not flag when sentry is present")
			}
		}
	})
}

// ── CI hygiene rule tests ───────────────────────────────────────

func TestHardenCheckCIHygiene(t *testing.T) {
	t.Run("hardcoded secrets in DSE", func(t *testing.T) {
		dir := t.TempDir()
		kindlingDir := filepath.Join(dir, ".kindling")
		os.MkdirAll(kindlingDir, 0755)

		dse := `apiVersion: kindling.dev/v1alpha1
kind: DevStagingEnvironment
spec:
  env:
    - name: API_KEY
      value: sk-abc123def456
`
		os.WriteFile(filepath.Join(kindlingDir, "dev-environment.yaml"), []byte(dse), 0644)

		ctx := &repoContext{
			depFiles:       map[string]string{},
			sourceSnippets: map[string]string{},
			dockerfiles:    map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckCIHygiene(dir, ctx, cfg)

		found := false
		for _, f := range findings {
			if f.RuleID == "secrets-via-kindling" {
				found = true
			}
		}
		if !found {
			t.Error("expected secrets-via-kindling finding for hardcoded key in DSE")
		}
	})

	t.Run("missing health check path", func(t *testing.T) {
		dir := t.TempDir()
		kindlingDir := filepath.Join(dir, ".kindling")
		os.MkdirAll(kindlingDir, 0755)

		dse := `apiVersion: kindling.dev/v1alpha1
kind: DevStagingEnvironment
spec:
  services:
    - name: api
      port: 8080
`
		os.WriteFile(filepath.Join(kindlingDir, "dev-environment.yaml"), []byte(dse), 0644)

		ctx := &repoContext{
			depFiles:       map[string]string{},
			sourceSnippets: map[string]string{},
			dockerfiles:    map[string]string{},
		}
		cfg := defaultHardenConfig()
		findings := hardenCheckCIHygiene(dir, ctx, cfg)

		found := false
		for _, f := range findings {
			if f.RuleID == "missing-health-check-path" {
				found = true
			}
		}
		if !found {
			t.Error("expected missing-health-check-path finding")
		}
	})
}

func TestFindDSEFiles(t *testing.T) {
	dir := t.TempDir()
	kindlingDir := filepath.Join(dir, ".kindling")
	os.MkdirAll(kindlingDir, 0755)

	os.WriteFile(filepath.Join(kindlingDir, "dev-environment.yaml"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(kindlingDir, "staging-environment.yml"), []byte("test"), 0644)

	files := findDSEFiles(dir)
	if len(files) < 2 {
		t.Errorf("expected at least 2 DSE files, found %d", len(files))
	}
}

// ── Full security check integration test ────────────────────────

func TestHardenCheckSecurity_Integration(t *testing.T) {
	dir := t.TempDir()

	// Create a .gitignore without .env
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0644)

	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"config.py": `API_KEY = "sk-abc123def456"`,
		},
		dockerfiles: map[string]string{},
		depFiles: map[string]string{
			"requirements.txt": "flask\nrequests\n",
		},
	}
	cfg := defaultHardenConfig()

	findings := hardenCheckSecurity(dir, ctx, cfg)

	ruleIDs := make(map[string]bool)
	for _, f := range findings {
		ruleIDs[f.RuleID] = true
	}

	if !ruleIDs["no-hardcoded-secrets"] {
		t.Error("expected no-hardcoded-secrets finding")
	}
	if !ruleIDs["gitignore-hygiene"] {
		t.Error("expected gitignore-hygiene finding")
	}
	if !ruleIDs["dependency-pinning"] {
		t.Error("expected dependency-pinning finding")
	}
}
