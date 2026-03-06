package cmd

import (
	"testing"
)

// Tests for normaliseServices and stripKindlingTag live in commands_test.go.

// ────────────────────────────────────────────────────────────────────────────
// extractSecretKeyRefNames
// ────────────────────────────────────────────────────────────────────────────

func TestExtractSecretKeyRefNames(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{
			name:   "empty content",
			input:  "",
			expect: nil,
		},
		{
			name:   "no secretKeyRef",
			input:  "env:\n  - name: FOO\n    value: bar",
			expect: nil,
		},
		{
			name: "single secret",
			input: `env:
  - name: STRIPE_KEY
    valueFrom:
      secretKeyRef:
        name: stripe-api-key
        key: STRIPE_KEY`,
			expect: []string{"stripe-api-key"},
		},
		{
			name: "multiple secrets",
			input: `env:
  - name: STRIPE_KEY
    valueFrom:
      secretKeyRef:
        name: stripe-api-key
        key: STRIPE_KEY
  - name: DB_PASS
    valueFrom:
      secretKeyRef:
        name: db-password
        key: DB_PASS`,
			expect: []string{"stripe-api-key", "db-password"},
		},
		{
			name: "duplicates deduplicated",
			input: `  secretKeyRef:
    name: same-secret
  secretKeyRef:
    name: same-secret`,
			expect: []string{"same-secret"},
		},
		{
			name:   "secretKeyRef at end of file",
			input:  `  secretKeyRef:`,
			expect: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSecretKeyRefNames(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("extractSecretKeyRefNames() = %v, want %v", got, tt.expect)
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("extractSecretKeyRefNames()[%d] = %q, want %q",
						i, got[i], tt.expect[i])
				}
			}
		})
	}
}
