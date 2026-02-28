package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestBuildContextDocument(t *testing.T) {
	// Create a minimal repo structure in a temp dir
	tmp := t.TempDir()

	// Create a .git dir so findRepoRoot works
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	// Create some project files
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/app\n"), 0644)
	os.MkdirAll(filepath.Join(tmp, ".github", "workflows"), 0755)
	os.WriteFile(filepath.Join(tmp, "Dockerfile"), []byte("FROM golang:1.21\n"), 0644)

	doc := buildContextDocument(tmp)

	// Should have all three sections
	if !strings.Contains(doc, "## Architectural Principles") {
		t.Error("missing Architectural Principles section")
	}
	if !strings.Contains(doc, "## CLI Reference") {
		t.Error("missing CLI Reference section")
	}
	if !strings.Contains(doc, "## This Project") {
		t.Error("missing This Project section")
	}
	if !strings.Contains(doc, "## Kaniko Compatibility Notes") {
		t.Error("missing Kaniko section")
	}

	// Should detect Go
	if !strings.Contains(doc, "Go") {
		t.Error("should detect Go from go.mod")
	}

	// Should mention kindling deploy
	if !strings.Contains(doc, "kindling deploy") {
		t.Error("should mention kindling deploy")
	}

	// Should mention dependency auto-injection
	if !strings.Contains(doc, "DATABASE_URL") {
		t.Error("should list auto-injected env vars")
	}
}

func TestDetectProjectContext(t *testing.T) {
	tmp := t.TempDir()

	// Python project
	os.WriteFile(filepath.Join(tmp, "requirements.txt"), []byte("flask\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "Dockerfile"), []byte("FROM python:3.11\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "docker-compose.yml"), []byte("version: '3'\n"), 0644)

	ctx := detectProjectContext(tmp)

	if !strings.Contains(ctx, "Python") {
		t.Error("should detect Python")
	}
	if !strings.Contains(ctx, "Dockerfiles found") {
		t.Error("should count Dockerfiles")
	}
	if !strings.Contains(ctx, "Docker Compose") {
		t.Error("should detect docker-compose.yml")
	}
}

func TestIntelOnOff(t *testing.T) {
	tmp := t.TempDir()

	// Create a .git dir
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	// Create a pre-existing copilot instructions file
	ghDir := filepath.Join(tmp, ".github")
	os.MkdirAll(ghDir, 0755)
	originalContent := "# My existing copilot instructions\nBe nice.\n"
	os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte(originalContent), 0644)

	// Simulate intel on
	contextDoc := buildContextDocument(tmp)
	state := &intelState{
		Active:  true,
		Backups: make(map[string]string),
	}

	backupDir := filepath.Join(tmp, ".kindling", "intel-backups")
	os.MkdirAll(backupDir, 0755)

	for _, agent := range knownAgents {
		agentPath := filepath.Join(tmp, agent.File)

		// Back up if exists
		if data, err := os.ReadFile(agentPath); err == nil {
			backupName := strings.ReplaceAll(agent.File, "/", "__")
			backupPath := filepath.Join(backupDir, backupName)
			os.WriteFile(backupPath, data, 0644)
			state.Backups[agent.File] = filepath.Join(".kindling", "intel-backups", backupName)
		}

		// Write kindling context
		dir := filepath.Dir(agentPath)
		os.MkdirAll(dir, 0755)
		content := formatForAgent(agent, contextDoc)
		os.WriteFile(agentPath, []byte(content), 0644)
		state.Written = append(state.Written, agent.File)
	}

	// Save state
	if err := saveIntelState(tmp, state); err != nil {
		t.Fatalf("saveIntelState failed: %v", err)
	}

	// Verify copilot file was replaced
	newContent, _ := os.ReadFile(filepath.Join(tmp, ".github", "copilot-instructions.md"))
	if string(newContent) == originalContent {
		t.Error("copilot-instructions.md should have been replaced")
	}
	if !strings.Contains(string(newContent), "kindling") {
		t.Error("new content should mention kindling")
	}

	// Verify CLAUDE.md was created
	claudeContent, err := os.ReadFile(filepath.Join(tmp, "CLAUDE.md"))
	if err != nil {
		t.Fatal("CLAUDE.md should have been created")
	}
	if !strings.Contains(string(claudeContent), "kindling") {
		t.Error("CLAUDE.md should mention kindling")
	}

	// Verify Cursor rules
	cursorContent, err := os.ReadFile(filepath.Join(tmp, ".cursor", "rules", "kindling.mdc"))
	if err != nil {
		t.Fatal("cursor rules should have been created")
	}
	if !strings.Contains(string(cursorContent), "alwaysApply: true") {
		t.Error("cursor rules should have frontmatter")
	}

	// Verify state was saved
	loaded, err := loadIntelState(tmp)
	if err != nil {
		t.Fatalf("loadIntelState failed: %v", err)
	}
	if !loaded.Active {
		t.Error("state should be active")
	}
	if len(loaded.Backups) != 1 {
		t.Errorf("expected 1 backup (copilot), got %d", len(loaded.Backups))
	}
	if len(loaded.Written) != len(knownAgents) {
		t.Errorf("expected %d written files, got %d", len(knownAgents), len(loaded.Written))
	}

	// Now simulate intel off — restore backups
	for agentFile, backupRel := range loaded.Backups {
		backupPath := filepath.Join(tmp, backupRel)
		agentPath := filepath.Join(tmp, agentFile)
		data, _ := os.ReadFile(backupPath)
		os.WriteFile(agentPath, data, 0644)
	}

	// Remove files with no backup
	for _, agentFile := range loaded.Written {
		if _, hasBackup := loaded.Backups[agentFile]; hasBackup {
			continue
		}
		os.Remove(filepath.Join(tmp, agentFile))
	}

	// Verify copilot was restored
	restored, _ := os.ReadFile(filepath.Join(tmp, ".github", "copilot-instructions.md"))
	if string(restored) != originalContent {
		t.Error("copilot-instructions.md should have been restored to original")
	}

	// Verify CLAUDE.md was removed (had no original)
	if _, err := os.Stat(filepath.Join(tmp, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("CLAUDE.md should have been removed (no original existed)")
	}
}

func TestFormatForAgent(t *testing.T) {
	doc := "# Test Context\nSome content."

	tests := []struct {
		agent    agentTarget
		contains string
	}{
		{agentTarget{Name: "GitHub Copilot", File: ".github/copilot-instructions.md"}, "# Test Context"},
		{agentTarget{Name: "Claude Code", File: "CLAUDE.md"}, "# Test Context"},
		{agentTarget{Name: "Cursor", File: ".cursor/rules/kindling.mdc"}, "alwaysApply: true"},
		{agentTarget{Name: "Windsurf", File: ".windsurfrules"}, "# Test Context"},
	}

	for _, tt := range tests {
		t.Run(tt.agent.Name, func(t *testing.T) {
			result := formatForAgent(tt.agent, doc)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("formatForAgent(%s) should contain %q", tt.agent.Name, tt.contains)
			}
		})
	}
}

func TestFindRepoRoot(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)
	subdir := filepath.Join(tmp, "a", "b", "c")
	os.MkdirAll(subdir, 0755)

	// Change to the nested subdir
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(subdir)

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot failed: %v", err)
	}

	// Resolve symlinks for comparison (macOS /var → /private/var)
	expectedRoot, _ := filepath.EvalSymlinks(tmp)
	actualRoot, _ := filepath.EvalSymlinks(root)

	if actualRoot != expectedRoot {
		t.Errorf("expected repo root %s, got %s", expectedRoot, actualRoot)
	}
}

func TestIntelStateIdempotency(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	// First activation
	state := &intelState{
		Active:  true,
		Backups: map[string]string{},
		Written: []string{".github/copilot-instructions.md"},
	}
	if err := saveIntelState(tmp, state); err != nil {
		t.Fatalf("first save failed: %v", err)
	}

	// Verify it loads back correctly
	loaded, err := loadIntelState(tmp)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !loaded.Active {
		t.Error("should be active")
	}
	if len(loaded.Written) != 1 {
		t.Error("should have 1 written file")
	}
}

func TestIntelTimestampTracking(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	state := &intelState{
		Active:  true,
		Backups: map[string]string{},
		Written: []string{},
	}

	// Touch interaction and verify it's recent
	state.touchInteraction()
	ts := state.lastInteractionTime()
	if time.Since(ts) > 2*time.Second {
		t.Error("touchInteraction should set a recent timestamp")
	}

	// Save and reload — timestamp should survive
	saveIntelState(tmp, state)
	loaded, _ := loadIntelState(tmp)
	loadedTs := loaded.lastInteractionTime()
	if loadedTs.IsZero() {
		t.Error("timestamp should survive save/load")
	}
	if ts.Unix() != loadedTs.Unix() {
		t.Errorf("timestamp mismatch: saved %v, loaded %v", ts, loadedTs)
	}
}

func TestIntelStalenessDetection(t *testing.T) {
	state := &intelState{Active: true}

	// A timestamp from 2 hours ago should be stale
	state.LastInteraction = time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	if time.Since(state.lastInteractionTime()) <= intelSessionTimeout {
		t.Error("2-hour-old interaction should be stale")
	}

	// A timestamp from 5 minutes ago should be fresh
	state.LastInteraction = time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
	if time.Since(state.lastInteractionTime()) > intelSessionTimeout {
		t.Error("5-minute-old interaction should be fresh")
	}

	// Empty timestamp should be stale (zero time)
	state.LastInteraction = ""
	if time.Since(state.lastInteractionTime()) <= intelSessionTimeout {
		t.Error("empty timestamp should be stale")
	}
}

func TestIntelRestoreFunction(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	// Set up original files
	ghDir := filepath.Join(tmp, ".github")
	os.MkdirAll(ghDir, 0755)
	originalContent := "# My original instructions\n"
	os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte(originalContent), 0644)

	// Simulate activation: back up + write kindling content
	backupDir := filepath.Join(tmp, ".kindling", "intel-backups")
	os.MkdirAll(backupDir, 0755)

	backupName := ".github__copilot-instructions.md"
	os.WriteFile(filepath.Join(backupDir, backupName), []byte(originalContent), 0644)
	os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte("# Kindling context\n"), 0644)

	// Also create a file that had no original
	os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte("# Kindling context\n"), 0644)

	state := &intelState{
		Active:  true,
		Backups: map[string]string{".github/copilot-instructions.md": ".kindling/intel-backups/" + backupName},
		Written: []string{".github/copilot-instructions.md", "CLAUDE.md"},
	}
	state.touchInteraction()
	saveIntelState(tmp, state)

	// Call restoreIntel
	restoreIntel(tmp, state)

	// Original should be restored
	restored, _ := os.ReadFile(filepath.Join(ghDir, "copilot-instructions.md"))
	if string(restored) != originalContent {
		t.Errorf("expected original content restored, got: %s", string(restored))
	}

	// CLAUDE.md (no original) should be removed
	if _, err := os.Stat(filepath.Join(tmp, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("CLAUDE.md should be removed since it had no original")
	}

	// Backups dir should be removed
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Error("backup dir should be cleaned up")
	}

	// State file should be removed
	if _, err := os.Stat(filepath.Join(tmp, intelStateFile)); !os.IsNotExist(err) {
		t.Error("state file should be removed")
	}
}

func TestIntelDisabledFlag(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	// Set disabled flag
	setIntelDisabled(tmp)

	// Verify the file exists
	disabledPath := filepath.Join(tmp, intelDisabledFile)
	if _, err := os.Stat(disabledPath); os.IsNotExist(err) {
		t.Fatal("disabled flag file should exist")
	}

	// Read contents — should be a timestamp
	data, _ := os.ReadFile(disabledPath)
	content := strings.TrimSpace(string(data))
	if _, err := time.Parse(time.RFC3339, content); err != nil {
		t.Errorf("disabled flag should contain RFC3339 timestamp, got: %s", content)
	}

	// Clearing disabled flag (simulating `kindling intel on`)
	os.Remove(disabledPath)
	if _, err := os.Stat(disabledPath); !os.IsNotExist(err) {
		t.Error("disabled flag should be removed")
	}
}

func TestActivateIntelBackupDedup(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git"), 0755)

	// Write a file that already has kindling content
	ghDir := filepath.Join(tmp, ".github")
	os.MkdirAll(ghDir, 0755)
	kindlingContent := "# Kindling — Coding Agent Context\nSome kindling stuff\n"
	os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte(kindlingContent), 0644)

	// Activate — should NOT back up the file with kindling content
	err := activateIntel(tmp, false)
	if err != nil {
		t.Fatalf("activateIntel failed: %v", err)
	}

	loaded, _ := loadIntelState(tmp)
	if _, hasBackup := loaded.Backups[".github/copilot-instructions.md"]; hasBackup {
		t.Error("should not back up a file that already has kindling content")
	}
}

func TestShouldSkipIntel(t *testing.T) {
	// Create test commands mimicking the real structure
	root := &cobra.Command{Use: "kindling"}
	intel := &cobra.Command{Use: "intel"}
	on := &cobra.Command{Use: "on"}
	off := &cobra.Command{Use: "off"}
	status := &cobra.Command{Use: "status"}
	version := &cobra.Command{Use: "version"}
	deploy := &cobra.Command{Use: "deploy"}
	generate := &cobra.Command{Use: "generate"}

	intel.AddCommand(on, off, status)
	root.AddCommand(intel, version, deploy, generate)

	tests := []struct {
		cmd  *cobra.Command
		skip bool
	}{
		{on, true},
		{off, true},
		{status, true},
		{version, true},
		{deploy, false},
		{generate, false},
	}

	for _, tt := range tests {
		t.Run(tt.cmd.Name(), func(t *testing.T) {
			got := shouldSkipIntel(tt.cmd)
			if got != tt.skip {
				t.Errorf("shouldSkipIntel(%s) = %v, want %v", tt.cmd.Name(), got, tt.skip)
			}
		})
	}
}
