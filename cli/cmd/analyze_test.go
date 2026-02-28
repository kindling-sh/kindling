package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// detectPrimaryLanguage
// ────────────────────────────────────────────────────────────────────────────

func TestDetectPrimaryLanguage_Python(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{"requirements.txt": "flask\n"}}
	if got := detectPrimaryLanguage(ctx); got != "Python" {
		t.Errorf("got %q, want Python", got)
	}
}

func TestDetectPrimaryLanguage_NodeJS(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{"package.json": `{"name":"x"}`}}
	if got := detectPrimaryLanguage(ctx); got != "Node.js" {
		t.Errorf("got %q, want Node.js", got)
	}
}

func TestDetectPrimaryLanguage_Go(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{"go.mod": "module x"}}
	if got := detectPrimaryLanguage(ctx); got != "Go" {
		t.Errorf("got %q, want Go", got)
	}
}

func TestDetectPrimaryLanguage_Rust(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{"Cargo.toml": "[package]"}}
	if got := detectPrimaryLanguage(ctx); got != "Rust" {
		t.Errorf("got %q, want Rust", got)
	}
}

func TestDetectPrimaryLanguage_Empty(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{}}
	if got := detectPrimaryLanguage(ctx); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// checkKanikoCompat
// ────────────────────────────────────────────────────────────────────────────

func TestCheckKanikoCompat_BuildKitArgs(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM ubuntu\nARG TARGETARCH\nRUN echo $TARGETARCH")
	if len(results) == 0 {
		t.Fatal("expected Kaniko warning for TARGETARCH")
	}
	if results[0].status != checkWarn {
		t.Errorf("expected checkWarn, got %d", results[0].status)
	}
}

func TestCheckKanikoCompat_PoetryNoRoot(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM python:3.12\nRUN poetry install")
	if len(results) == 0 {
		t.Fatal("expected Kaniko warning for poetry install without --no-root")
	}
}

func TestCheckKanikoCompat_PoetryWithNoRoot(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM python:3.12\nRUN poetry install --no-root")
	if len(results) != 0 {
		t.Fatal("expected no warning for poetry install --no-root")
	}
}

func TestCheckKanikoCompat_NpmNoCache(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM node:20\nRUN npm install")
	if len(results) == 0 {
		t.Fatal("expected Kaniko warning for npm without cache redirect")
	}
}

func TestCheckKanikoCompat_GoBuildNoVCS(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM golang:1.22\nRUN go build .")
	if len(results) == 0 {
		t.Fatal("expected Kaniko warning for go build without -buildvcs=false")
	}
}

func TestCheckKanikoCompat_GoBuildWithVCS(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM golang:1.22\nRUN go build -buildvcs=false .")
	if len(results) != 0 {
		t.Errorf("expected no warning for go build with -buildvcs=false, got %d", len(results))
	}
}

func TestCheckKanikoCompat_Clean(t *testing.T) {
	results := checkKanikoCompat("Dockerfile", "FROM python:3.12\nCOPY . .\nCMD [\"python\", \"app.py\"]")
	if len(results) != 0 {
		t.Errorf("expected no warnings, got %d", len(results))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// inferFrameworkSecrets
// ────────────────────────────────────────────────────────────────────────────

func TestInferFrameworkSecrets_LangChainOpenAI(t *testing.T) {
	ctx := &repoContext{
		agentFrameworks: []string{"LangChain"},
		depFiles:        map[string]string{"requirements.txt": "langchain\nlangchain-openai\n"},
	}
	secrets := inferFrameworkSecrets(ctx)
	if len(secrets) != 1 || secrets[0] != "OPENAI_API_KEY" {
		t.Errorf("got %v, want [OPENAI_API_KEY]", secrets)
	}
}

func TestInferFrameworkSecrets_LangChainAnthropic(t *testing.T) {
	ctx := &repoContext{
		agentFrameworks: []string{"LangChain"},
		depFiles:        map[string]string{"requirements.txt": "langchain\nlangchain-anthropic\n"},
	}
	secrets := inferFrameworkSecrets(ctx)
	if len(secrets) != 1 || secrets[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("got %v, want [ANTHROPIC_API_KEY]", secrets)
	}
}

func TestInferFrameworkSecrets_CrewAI(t *testing.T) {
	ctx := &repoContext{
		agentFrameworks: []string{"CrewAI"},
		depFiles:        map[string]string{},
	}
	secrets := inferFrameworkSecrets(ctx)
	if len(secrets) != 1 || secrets[0] != "OPENAI_API_KEY" {
		t.Errorf("got %v, want [OPENAI_API_KEY]", secrets)
	}
}

func TestInferFrameworkSecrets_VectorStorePinecone(t *testing.T) {
	ctx := &repoContext{
		vectorStores: []string{"Pinecone"},
		depFiles:     map[string]string{},
	}
	secrets := inferFrameworkSecrets(ctx)
	if len(secrets) != 1 || secrets[0] != "PINECONE_API_KEY" {
		t.Errorf("got %v, want [PINECONE_API_KEY]", secrets)
	}
}

func TestInferFrameworkSecrets_NoFramework(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{}}
	secrets := inferFrameworkSecrets(ctx)
	if len(secrets) != 0 {
		t.Errorf("got %v, want empty", secrets)
	}
}

func TestInferFrameworkSecrets_NoDuplicates(t *testing.T) {
	ctx := &repoContext{
		agentFrameworks: []string{"LangChain", "CrewAI"},
		depFiles:        map[string]string{"requirements.txt": "langchain-openai\ncrewai\n"},
	}
	secrets := inferFrameworkSecrets(ctx)
	count := 0
	for _, s := range secrets {
		if s == "OPENAI_API_KEY" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("OPENAI_API_KEY appeared %d times, want 1", count)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// dockerfileFixForLanguage
// ────────────────────────────────────────────────────────────────────────────

func TestDockerfileFixForLanguage_Python(t *testing.T) {
	fix := dockerfileFixForLanguage("Python", "/tmp/repo")
	if fix == "" {
		t.Fatal("expected non-empty fix for Python")
	}
	if !strings.Contains(fix, "python:3.12") {
		t.Error("Python fix should reference python:3.12")
	}
}

func TestDockerfileFixForLanguage_NodeJS(t *testing.T) {
	fix := dockerfileFixForLanguage("Node.js", "/tmp/repo")
	if !strings.Contains(fix, "npm_config_cache") {
		t.Error("Node.js fix should include npm cache redirect for Kaniko")
	}
}

func TestDockerfileFixForLanguage_Go(t *testing.T) {
	fix := dockerfileFixForLanguage("Go", "/tmp/repo")
	if !strings.Contains(fix, "-buildvcs=false") {
		t.Error("Go fix should include -buildvcs=false for Kaniko")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// checkGitState
// ────────────────────────────────────────────────────────────────────────────

func TestCheckGitState_NoGitDir(t *testing.T) {
	dir := t.TempDir()
	results := checkGitState(dir)
	if results[0].status != checkFail {
		t.Error("expected checkFail for non-git directory")
	}
	if results[0].fix == "" {
		t.Error("expected fix instruction")
	}
}

func TestCheckGitState_InitializedButNoCommits(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	results := checkGitState(dir)
	// First check: git init ✅
	if results[0].status != checkPass {
		t.Errorf("expected checkPass for .git exists, got %d", results[0].status)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// checkDockerfiles
// ────────────────────────────────────────────────────────────────────────────

func TestCheckDockerfiles_None(t *testing.T) {
	ctx := &repoContext{
		dockerfileCount: 0,
		dockerfiles:     map[string]string{},
		depFiles:        map[string]string{"requirements.txt": "flask\n"},
	}
	results := checkDockerfiles("/tmp", ctx)
	if results[0].status != checkFail {
		t.Error("expected checkFail for missing Dockerfile")
	}
}

func TestCheckDockerfiles_Present(t *testing.T) {
	ctx := &repoContext{
		dockerfileCount: 1,
		dockerfiles:     map[string]string{"Dockerfile": "FROM python:3.12\nCOPY . .\n"},
		depFiles:        map[string]string{},
	}
	results := checkDockerfiles("/tmp", ctx)
	if results[0].status != checkPass {
		t.Error("expected checkPass for present Dockerfile")
	}
}

func TestCheckDockerfiles_KanikoWarnings(t *testing.T) {
	ctx := &repoContext{
		dockerfileCount: 1,
		dockerfiles:     map[string]string{"Dockerfile": "FROM golang:1.22\nRUN go build .\n"},
		depFiles:        map[string]string{},
	}
	results := checkDockerfiles("/tmp", ctx)
	// Should have pass + kaniko warning
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[1].status != checkWarn {
		t.Error("expected Kaniko compat warning")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// checkAgentArchitecture
// ────────────────────────────────────────────────────────────────────────────

func TestCheckAgentArchitecture_None(t *testing.T) {
	ctx := &repoContext{depFiles: map[string]string{}}
	results := checkAgentArchitecture(ctx)
	if len(results) != 0 {
		t.Errorf("expected no results for non-agent repo, got %d", len(results))
	}
}

func TestCheckAgentArchitecture_FullStack(t *testing.T) {
	ctx := &repoContext{
		agentFrameworks:   []string{"LangChain"},
		vectorStores:      []string{"Pinecone"},
		workerProcesses:   []string{"Celery beat scheduler"},
		interServiceCalls: []string{"requests.get(http://api-svc)"},
		depFiles:          map[string]string{},
	}
	results := checkAgentArchitecture(ctx)
	if len(results) == 0 {
		t.Fatal("expected agent architecture results")
	}
	// Should have: header + frameworks + vectors + workers + worker detail + inter-service
	if len(results) < 5 {
		t.Errorf("expected at least 5 results, got %d", len(results))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// secretK8sNames
// ────────────────────────────────────────────────────────────────────────────

func TestSecretK8sNames_OpenAI(t *testing.T) {
	names := secretK8sNames("OPENAI_API_KEY")
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "kindling-secret-openai-api-key" {
		t.Errorf("got %q, want kindling-secret-openai-api-key", names[0])
	}
	if names[1] != "openai-api-key" {
		t.Errorf("got %q, want openai-api-key", names[1])
	}
}

func TestSecretK8sNames_StripeKey(t *testing.T) {
	names := secretK8sNames("STRIPE_API_KEY")
	if names[0] != "kindling-secret-stripe-api-key" {
		t.Errorf("got %q, want kindling-secret-stripe-api-key", names[0])
	}
	if names[1] != "stripe-api-key" {
		t.Errorf("got %q, want stripe-api-key", names[1])
	}
}

// ────────────────────────────────────────────────────────────────────────────
// extractSecretKeyRefNames (push pre-flight)
// ────────────────────────────────────────────────────────────────────────────

func TestExtractSecretKeyRefNames_Single(t *testing.T) {
	workflow := `
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: openai-api-key
                  key: OPENAI_API_KEY
`
	names := extractSecretKeyRefNames(workflow)
	if len(names) != 1 {
		t.Fatalf("expected 1 secret, got %d: %v", len(names), names)
	}
	if names[0] != "openai-api-key" {
		t.Errorf("got %q, want openai-api-key", names[0])
	}
}

func TestExtractSecretKeyRefNames_Multiple(t *testing.T) {
	workflow := `
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: openai-api-key
                  key: OPENAI_API_KEY
            - name: STRIPE_KEY
              valueFrom:
                secretKeyRef:
                  name: stripe-key
                  key: STRIPE_KEY
`
	names := extractSecretKeyRefNames(workflow)
	if len(names) != 2 {
		t.Fatalf("expected 2 secrets, got %d: %v", len(names), names)
	}
}

func TestExtractSecretKeyRefNames_Dedup(t *testing.T) {
	workflow := `
                secretKeyRef:
                  name: openai-api-key
                  key: OPENAI_API_KEY
                secretKeyRef:
                  name: openai-api-key
                  key: OPENAI_API_KEY
`
	names := extractSecretKeyRefNames(workflow)
	if len(names) != 1 {
		t.Errorf("expected 1 deduped secret, got %d", len(names))
	}
}

func TestExtractSecretKeyRefNames_None(t *testing.T) {
	workflow := `
name: dev-deploy
on: push
jobs:
  build:
    runs-on: self-hosted
`
	names := extractSecretKeyRefNames(workflow)
	if len(names) != 0 {
		t.Errorf("expected 0 secrets, got %d", len(names))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectEntryPoints
// ────────────────────────────────────────────────────────────────────────────

func TestDetectEntryPoints_MultipleRootFiles(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"orchestrator.py": "import redis\ndef main():\n    pass",
			"worker.py":       "import redis\ndef run():\n    pass",
			"config.py":       "REDIS_HOST = 'localhost'",
		},
		depFiles: map[string]string{},
	}
	eps := detectEntryPoints("/tmp", ctx)
	// orchestrator and worker match entry point patterns; config does not
	if len(eps) != 2 {
		t.Errorf("expected 2 entry points, got %d: %v", len(eps), eps)
	}
}

func TestDetectEntryPoints_IfNameMain(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"foo.py": "import sys\nif __name__ == '__main__':\n    run()",
		},
		depFiles: map[string]string{},
	}
	eps := detectEntryPoints("/tmp", ctx)
	if len(eps) != 1 {
		t.Errorf("expected 1 entry point from __name__ pattern, got %d", len(eps))
	}
}

func TestDetectEntryPoints_NestedFilesIgnored(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"worker.py":          "import redis",
			"subdir/worker2.py":  "import redis",
		},
		depFiles: map[string]string{},
	}
	eps := detectEntryPoints("/tmp", ctx)
	// Only root-level worker.py should match
	if len(eps) != 1 {
		t.Errorf("expected 1 root entry point, got %d: %v", len(eps), eps)
	}
}

func TestDetectEntryPoints_Procfile(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{},
		depFiles: map[string]string{
			"Procfile": "web: python orchestrator.py\nworker: python worker.py",
		},
	}
	eps := detectEntryPoints("/tmp", ctx)
	if len(eps) != 2 {
		t.Errorf("expected 2 Procfile entry points, got %d: %v", len(eps), eps)
	}
}

func TestDetectEntryPoints_SingleService(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"app.py":    "from flask import Flask",
			"models.py": "class User:\n    pass",
			"utils.py":  "def helper():\n    pass",
		},
		depFiles: map[string]string{},
	}
	eps := detectEntryPoints("/tmp", ctx)
	// Only app.py matches
	if len(eps) != 1 {
		t.Errorf("expected 1 entry point for single-service app, got %d: %v", len(eps), eps)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// checkProjectStructure
// ────────────────────────────────────────────────────────────────────────────

func TestCheckProjectStructure_FlatMultiService(t *testing.T) {
	ctx := &repoContext{
		dockerfileCount: 1,
		dockerfiles:     map[string]string{"Dockerfile": "FROM python:3.12\nCOPY . ."},
		depFiles:        map[string]string{"requirements.txt": "langchain\nredis\n"},
		sourceSnippets: map[string]string{
			"orchestrator.py": "import redis\ndef main(): pass",
			"worker.py":       "import redis\ndef run(): pass",
			"config.py":       "REDIS_HOST = 'localhost'",
		},
		workerProcesses:   []string{"Redis queue consumer"},
		interServiceCalls: []string{"inter-service HTTP"},
		agentFrameworks:   []string{"LangChain"},
	}
	results := checkProjectStructure("/tmp", ctx)
	if len(results) == 0 {
		t.Fatal("expected structure suggestions for flat multi-service repo")
	}
	// Should contain a warning about flat layout
	hasWarn := false
	for _, r := range results {
		if r.status == checkWarn {
			hasWarn = true
			break
		}
	}
	if !hasWarn {
		t.Error("expected at least one warning about flat structure")
	}
	// Should contain structure suggestion
	hasStructure := false
	for _, r := range results {
		if strings.Contains(r.message, "Dockerfile") && strings.Contains(r.message, "├") {
			hasStructure = true
			break
		}
	}
	if !hasStructure {
		t.Error("expected directory tree suggestion in output")
	}
}

func TestCheckProjectStructure_AlreadyMultiDockerfile(t *testing.T) {
	ctx := &repoContext{
		dockerfileCount: 2,
		dockerfiles: map[string]string{
			"orchestrator/Dockerfile": "FROM python:3.12",
			"worker/Dockerfile":       "FROM python:3.12",
		},
		depFiles: map[string]string{"requirements.txt": "langchain\n"},
		sourceSnippets: map[string]string{
			"orchestrator.py": "def main(): pass",
			"worker.py":       "def run(): pass",
		},
		workerProcesses: []string{"Redis queue consumer"},
	}
	results := checkProjectStructure("/tmp", ctx)
	hasPass := false
	for _, r := range results {
		if r.status == checkPass && strings.Contains(r.message, "Multi-service layout") {
			hasPass = true
		}
	}
	if !hasPass {
		t.Error("expected pass for already-structured multi-Dockerfile project")
	}
}

func TestCheckProjectStructure_SingleService(t *testing.T) {
	ctx := &repoContext{
		dockerfileCount: 1,
		dockerfiles:     map[string]string{"Dockerfile": "FROM python:3.12"},
		depFiles:        map[string]string{"requirements.txt": "flask\n"},
		sourceSnippets: map[string]string{
			"app.py":    "from flask import Flask",
			"models.py": "class User: pass",
		},
	}
	results := checkProjectStructure("/tmp", ctx)
	// Single entry point, no multi-service signal — should return nothing
	if len(results) != 0 {
		t.Errorf("expected no suggestions for single-service app, got %d", len(results))
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildStructureSuggestion
// ────────────────────────────────────────────────────────────────────────────

func TestBuildStructureSuggestion_Python(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"orchestrator.py": "code",
			"worker.py":       "code",
			"config.py":       "code",
		},
		workerProcesses: []string{"worker"},
	}
	lines := buildStructureSuggestion(
		[]string{"orchestrator.py", "worker.py"},
		"Python",
		ctx,
	)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "orchestrator/") {
		t.Error("expected orchestrator/ directory in suggestion")
	}
	if !strings.Contains(joined, "worker/") {
		t.Error("expected worker/ directory in suggestion")
	}
	if !strings.Contains(joined, "Dockerfile") {
		t.Error("expected Dockerfile in each service directory")
	}
	if !strings.Contains(joined, "requirements.txt") {
		t.Error("expected requirements.txt for Python project")
	}
	if !strings.Contains(joined, "shared/") || !strings.Contains(joined, "config.py") {
		t.Error("expected shared/ directory with config.py")
	}
}

func TestDetectSharedModules(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"orchestrator.py": "code",
			"worker.py":       "code",
			"config.py":       "code",
			"context.py":      "code",
		},
	}
	shared := detectSharedModules(ctx, []string{"orchestrator", "worker"})
	if len(shared) != 2 {
		t.Errorf("expected 2 shared modules, got %d: %v", len(shared), shared)
	}
}