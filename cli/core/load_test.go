package core

import (
	"strings"
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// LoadImageTag
// ────────────────────────────────────────────────────────────────────────────

func TestLoadImageTag(t *testing.T) {
	tag := LoadImageTag("my-service")
	if !strings.HasPrefix(tag, "my-service:") {
		t.Errorf("LoadImageTag(my-service) = %q, should start with my-service:", tag)
	}

	// Tag should have a timestamp-seq part
	parts := strings.SplitN(tag, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		t.Errorf("LoadImageTag should produce name:timestamp-seq, got %q", tag)
	}
	if !strings.Contains(parts[1], "-") {
		t.Errorf("tag suffix should contain '-' separator, got %q", parts[1])
	}
}

func TestLoadImageTagUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tag := LoadImageTag("svc")
		if seen[tag] {
			t.Fatalf("duplicate tag on iteration %d: %q", i, tag)
		}
		seen[tag] = true
	}
}

// ────────────────────────────────────────────────────────────────────────────
// LoadConfig.namespace()
// ────────────────────────────────────────────────────────────────────────────

func TestLoadConfigNamespace(t *testing.T) {
	tests := []struct {
		ns   string
		want string
	}{
		{"", "default"},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		cfg := LoadConfig{Namespace: tt.ns}
		if got := cfg.namespace(); got != tt.want {
			t.Errorf("LoadConfig{Namespace: %q}.namespace() = %q, want %q", tt.ns, got, tt.want)
		}
	}
}
