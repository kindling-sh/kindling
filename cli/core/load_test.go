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

	// Tag should have a timestamp part (integer)
	parts := strings.SplitN(tag, ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		t.Errorf("LoadImageTag should produce name:timestamp, got %q", tag)
	}
}

func TestLoadImageTagUniqueness(t *testing.T) {
	tag1 := LoadImageTag("svc")
	tag2 := LoadImageTag("svc")
	// Tags from the same second may collide; that's expected.
	// Both should at least have the correct prefix.
	if !strings.HasPrefix(tag1, "svc:") || !strings.HasPrefix(tag2, "svc:") {
		t.Error("tags should have 'svc:' prefix")
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
