package core

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// ClusterContext
// ────────────────────────────────────────────────────────────────────────────

func TestClusterContext(t *testing.T) {
	tests := []struct {
		cluster string
		want    string
	}{
		{"dev", "kind-dev"},
		{"staging", "kind-staging"},
		{"my-cluster", "kind-my-cluster"},
		{"", "kind-"},
	}
	for _, tt := range tests {
		t.Run(tt.cluster, func(t *testing.T) {
			if got := ClusterContext(tt.cluster); got != tt.want {
				t.Errorf("ClusterContext(%q) = %q, want %q", tt.cluster, got, tt.want)
			}
		})
	}
}
