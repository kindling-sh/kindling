package cmd

import (
	"testing"
)

// ════════════════════════════════════════════════════════════════
// parseGitHubOwnerRepo
// ════════════════════════════════════════════════════════════════

func TestParseGitHubOwnerRepo(t *testing.T) {
	tests := []struct {
		name      string
		remote    string
		wantOwner string
		wantRepo  string
		wantOK    bool
	}{
		{"https with .git", "https://github.com/kindling-sh/kindling.git", "kindling-sh", "kindling", true},
		{"https without .git", "https://github.com/kindling-sh/kindling", "kindling-sh", "kindling", true},
		{"ssh with .git", "git@github.com:kindling-sh/kindling.git", "kindling-sh", "kindling", true},
		{"ssh without .git", "git@github.com:kindling-sh/kindling", "kindling-sh", "kindling", true},
		{"http (no s)", "http://github.com/kindling-sh/kindling.git", "kindling-sh", "kindling", true},
		{"not github", "https://gitlab.com/owner/repo.git", "", "", false},
		{"malformed, no repo", "https://github.com/kindling-sh", "", "", false},
		{"empty string", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, ok := parseGitHubOwnerRepo(tt.remote)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Errorf("parseGitHubOwnerRepo(%q) = (%q, %q), want (%q, %q)", tt.remote, owner, repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════
// splitOwnerRepo
// ════════════════════════════════════════════════════════════════

func TestSplitOwnerRepo(t *testing.T) {
	if owner, repo, ok := splitOwnerRepo("myorg/gitops"); !ok || owner != "myorg" || repo != "gitops" {
		t.Errorf("splitOwnerRepo(\"myorg/gitops\") = (%q, %q, %v), want (\"myorg\", \"gitops\", true)", owner, repo, ok)
	}
	if _, _, ok := splitOwnerRepo("no-slash"); ok {
		t.Error("splitOwnerRepo(\"no-slash\") should fail")
	}
	if _, _, ok := splitOwnerRepo("/missing-owner"); ok {
		t.Error("splitOwnerRepo(\"/missing-owner\") should fail")
	}
	if _, _, ok := splitOwnerRepo("missing-repo/"); ok {
		t.Error("splitOwnerRepo(\"missing-repo/\") should fail")
	}
}

// ════════════════════════════════════════════════════════════════
// latestArtifactForPR
// ════════════════════════════════════════════════════════════════

func TestLatestArtifactForPR(t *testing.T) {
	artifacts := []ghArtifact{
		{Name: "values-42", CreatedAt: "2026-01-01T00:00:00Z", Expired: false},
		{Name: "values-42", CreatedAt: "2026-01-02T00:00:00Z", Expired: false}, // newer, same PR
		{Name: "values-42", CreatedAt: "2026-01-03T00:00:00Z", Expired: true},  // newest but expired
		{Name: "values-7", CreatedAt: "2026-01-05T00:00:00Z", Expired: false},  // different PR
	}

	got := latestArtifactForPR(artifacts, 42)
	if got == nil {
		t.Fatal("expected an artifact for PR 42, got nil")
	}
	if got.CreatedAt != "2026-01-02T00:00:00Z" {
		t.Errorf("CreatedAt = %q, want the latest non-expired one (2026-01-02T00:00:00Z)", got.CreatedAt)
	}

	if got := latestArtifactForPR(artifacts, 999); got != nil {
		t.Errorf("expected nil for a PR with no artifact, got %+v", got)
	}
}

func TestLatestArtifactForPR_AllExpired(t *testing.T) {
	artifacts := []ghArtifact{
		{Name: "values-5", CreatedAt: "2026-01-01T00:00:00Z", Expired: true},
	}
	if got := latestArtifactForPR(artifacts, 5); got != nil {
		t.Errorf("expected nil when every matching artifact is expired, got %+v", got)
	}
}

// ════════════════════════════════════════════════════════════════
// diffLines
// ════════════════════════════════════════════════════════════════

func TestDiffLines_Identical(t *testing.T) {
	text := "a: 1\nb: 2\n"
	lines := diffLines(text, text)
	for _, l := range lines {
		if l.Type != "same" {
			t.Errorf("identical input produced a non-same line: %+v", l)
		}
	}
	if len(lines) != 2 {
		t.Errorf("len(lines) = %d, want 2", len(lines))
	}
}

func TestDiffLines_Addition(t *testing.T) {
	old := "a: 1\n"
	new := "a: 1\nb: 2\n"
	lines := diffLines(old, new)

	var adds, sames int
	for _, l := range lines {
		switch l.Type {
		case "add":
			adds++
			if l.Text != "b: 2" {
				t.Errorf("added line = %q, want %q", l.Text, "b: 2")
			}
		case "same":
			sames++
		case "remove":
			t.Errorf("unexpected removed line for a pure addition: %+v", l)
		}
	}
	if adds != 1 || sames != 1 {
		t.Errorf("adds=%d sames=%d, want adds=1 sames=1", adds, sames)
	}
}

func TestDiffLines_RemovalAndChange(t *testing.T) {
	old := "a: 1\nb: 2\nc: 3\n"
	new := "a: 1\nb: 99\n"
	lines := diffLines(old, new)

	var removes, adds, sames int
	for _, l := range lines {
		switch l.Type {
		case "remove":
			removes++
		case "add":
			adds++
		case "same":
			sames++
		}
	}
	// "a: 1" same, "b: 2" removed + "b: 99" added, "c: 3" removed.
	if sames != 1 {
		t.Errorf("sames = %d, want 1", sames)
	}
	if removes != 2 {
		t.Errorf("removes = %d, want 2", removes)
	}
	if adds != 1 {
		t.Errorf("adds = %d, want 1", adds)
	}
}

func TestDiffLines_EmptyOld(t *testing.T) {
	lines := diffLines("", "a: 1\nb: 2\n")
	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(lines))
	}
	for _, l := range lines {
		if l.Type != "add" {
			t.Errorf("expected every line to be an addition against an empty old value, got %+v", l)
		}
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines(""); got != nil {
		t.Errorf("splitLines(\"\") = %v, want nil", got)
	}
	if got := splitLines("a\nb\nc"); len(got) != 3 {
		t.Errorf("splitLines(\"a\\nb\\nc\") = %v, want 3 lines", got)
	}
	if got := splitLines("a\nb\nc\n"); len(got) != 3 {
		t.Errorf("splitLines with trailing newline = %v, want 3 lines (trailing newline not a 4th empty line)", got)
	}
}
