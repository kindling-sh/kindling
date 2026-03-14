package cmd

import (
	"strings"
	"testing"
)

func TestBuildHardenAgentPrompt_Structure(t *testing.T) {
	dir := t.TempDir()

	findings := []hardenFinding{
		{
			RuleID:   "no-root-container",
			Category: catContainers,
			Severity: ruleSevError,
			File:     "Dockerfile",
			Message:  "Runs as root — add a USER directive",
			Fix:      "RUN adduser -D appuser && USER appuser",
		},
		{
			RuleID:   "no-hardcoded-secrets",
			Category: catSecurity,
			Severity: ruleSevError,
			File:     "config.py",
			Message:  "Possible OpenAI API key inline",
			Fix:      "kindling secrets set OPENAI_API_KEY <value>",
		},
		{
			RuleID:   "structured-logging",
			Category: catObservability,
			Severity: ruleSevInfo,
			Message:  "Using print for logging",
		},
	}

	ctx := &repoContext{
		dockerfiles: map[string]string{
			"Dockerfile": "FROM python:3.11\nCOPY . .\nCMD [\"python\", \"app.py\"]",
		},
		sourceSnippets: map[string]string{
			"config.py": "API_KEY = \"sk-abc123\"",
		},
		depFiles: map[string]string{
			"requirements.txt": "flask==2.3.0\nrequests==2.31.0",
		},
	}

	prompt := buildHardenAgentPrompt(dir, findings, ctx)

	// Should contain all findings grouped by category
	if !strings.Contains(prompt, "## Security") {
		t.Error("prompt should contain Security section")
	}
	if !strings.Contains(prompt, "## Container Best Practices") {
		t.Error("prompt should contain Container Best Practices section")
	}
	if !strings.Contains(prompt, "## Observability") {
		t.Error("prompt should contain Observability section")
	}

	// Should contain rule IDs
	if !strings.Contains(prompt, "no-root-container") {
		t.Error("prompt should contain no-root-container rule ID")
	}
	if !strings.Contains(prompt, "no-hardcoded-secrets") {
		t.Error("prompt should contain no-hardcoded-secrets rule ID")
	}

	// Should contain file references
	if !strings.Contains(prompt, "Dockerfile") {
		t.Error("prompt should reference Dockerfile")
	}
	if !strings.Contains(prompt, "config.py") {
		t.Error("prompt should reference config.py")
	}

	// Should contain relevant source files section
	if !strings.Contains(prompt, "# Relevant source files") {
		t.Error("prompt should contain source files section")
	}

	// Should contain actual file contents
	if !strings.Contains(prompt, "FROM python:3.11") {
		t.Error("prompt should include Dockerfile content")
	}
	if !strings.Contains(prompt, "flask==2.3.0") {
		t.Error("prompt should include dependency file content")
	}
}

func TestBuildHardenAgentPrompt_EmptyFindings(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		dockerfiles:    map[string]string{},
		sourceSnippets: map[string]string{},
		depFiles:       map[string]string{},
	}

	prompt := buildHardenAgentPrompt(dir, nil, ctx)

	if !strings.Contains(prompt, "kindling harden findings") {
		t.Error("prompt should still have the header")
	}
	// No category sections
	if strings.Contains(prompt, "## Security") {
		t.Error("empty findings should produce no Security section")
	}
}

func TestBuildHardenAgentPrompt_SuggestedFix(t *testing.T) {
	dir := t.TempDir()

	findings := []hardenFinding{
		{
			RuleID:   "pin-base-images",
			Category: catContainers,
			Severity: ruleSevWarning,
			File:     "Dockerfile",
			Message:  "Unpinned base image",
			Fix:      "Use a specific tag like python:3.11-slim",
		},
	}

	ctx := &repoContext{
		dockerfiles:    map[string]string{"Dockerfile": "FROM python\nCMD python"},
		sourceSnippets: map[string]string{},
		depFiles:       map[string]string{},
	}

	prompt := buildHardenAgentPrompt(dir, findings, ctx)

	if !strings.Contains(prompt, "Suggested fix:") {
		t.Error("prompt should include suggested fix text")
	}
	if !strings.Contains(prompt, "python:3.11-slim") {
		t.Error("prompt should include the actual fix suggestion")
	}
}

func TestBuildHardenAgentPrompt_LargeFilesTruncated(t *testing.T) {
	dir := t.TempDir()

	findings := []hardenFinding{
		{
			RuleID:   "no-eval",
			Category: catSecurity,
			Severity: ruleSevWarning,
			File:     "big.py",
			Message:  "eval usage",
		},
	}

	// Source snippets won't have "big.py" so it tries to read from disk,
	// which won't exist — that path just gets skipped gracefully
	ctx := &repoContext{
		dockerfiles:    map[string]string{},
		sourceSnippets: map[string]string{},
		depFiles:       map[string]string{},
	}

	// Should not panic
	prompt := buildHardenAgentPrompt(dir, findings, ctx)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestHardenAgentSystemPrompt_Content(t *testing.T) {
	// Verify the system prompt contains key knowledge pack sections
	checks := []struct {
		name    string
		content string
	}{
		{"kindling context", "Kaniko"},
		{"non-root fix pattern", "USER directive"},
		{"base image guidance", "Pinning base images"},
		{"healthcheck caution", "start-period"},
		{"multi-stage warning", "shared libs"},
		{"output format", "rule-id"},
		{"rollback awareness", "Risk of fixing"},
		{"kindling secrets", "kindling secrets set"},
		{"false positive guidance", "harden.yaml override"},
	}

	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(hardenAgentSystemPrompt, tt.content) {
				t.Errorf("system prompt should mention %q", tt.content)
			}
		})
	}
}
