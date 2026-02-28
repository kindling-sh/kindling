package ci

import (
	"sort"
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// Registry — Get, Default, Names
// ────────────────────────────────────────────────────────────────────────────

// The providers auto-register via init(), so they must be present.

func TestGetKnownProviders(t *testing.T) {
	for _, name := range []string{"github", "gitlab"} {
		t.Run(name, func(t *testing.T) {
			p, err := Get(name)
			if err != nil {
				t.Fatalf("Get(%q) returned error: %v", name, err)
			}
			if p.Name() != name {
				t.Errorf("Get(%q).Name() = %q, want %q", name, p.Name(), name)
			}
		})
	}
}

func TestGetUnknownProvider(t *testing.T) {
	_, err := Get("nonexistent")
	if err == nil {
		t.Fatal("Get(nonexistent) should return error")
	}
	if !strings.Contains(err.Error(), "unknown CI provider") {
		t.Errorf("error message should mention 'unknown CI provider', got: %v", err)
	}
}

func TestDefaultReturnsGitHub(t *testing.T) {
	p := Default()
	if p.Name() != "github" {
		t.Errorf("Default().Name() = %q, want \"github\"", p.Name())
	}
}

func TestNamesContainsAll(t *testing.T) {
	names := Names()
	sort.Strings(names)

	expected := []string{"github", "gitlab"}
	if len(names) != len(expected) {
		t.Fatalf("Names() = %v, want %v", names, expected)
	}
	for i, n := range expected {
		if names[i] != n {
			t.Errorf("Names()[%d] = %q, want %q", i, names[i], n)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Register — duplicate registration overwrites
// ────────────────────────────────────────────────────────────────────────────

type dummyProvider struct{ name string }

func (d *dummyProvider) Name() string                { return d.name }
func (d *dummyProvider) DisplayName() string         { return "Dummy" }
func (d *dummyProvider) Runner() RunnerAdapter       { return nil }
func (d *dummyProvider) Workflow() WorkflowGenerator { return nil }
func (d *dummyProvider) CLILabels() CLILabels        { return CLILabels{} }

func TestRegisterOverwrite(t *testing.T) {
	// Register a dummy provider, then overwrite it
	Register(&dummyProvider{name: "test-overwrite"})
	p1, _ := Get("test-overwrite")
	if p1.DisplayName() != "Dummy" {
		t.Fatal("first registration failed")
	}

	// Overwrite with a new one
	Register(&dummyProvider{name: "test-overwrite"})
	p2, _ := Get("test-overwrite")
	if p2.DisplayName() != "Dummy" {
		t.Fatal("overwrite registration failed")
	}

	// Clean up
	mu.Lock()
	delete(providers, "test-overwrite")
	mu.Unlock()
}
