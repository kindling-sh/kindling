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


