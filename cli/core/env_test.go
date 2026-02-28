package core

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// ValidateEnvPairs
// ────────────────────────────────────────────────────────────────────────────

func TestValidateEnvPairsValid(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
	}{
		{"single", []string{"KEY=VALUE"}},
		{"multiple", []string{"A=1", "B=2", "C=3"}},
		{"with_equals", []string{"URL=http://localhost:8080"}},
		{"empty_value", []string{"KEY="}},
		{"complex_value", []string{"DSN=postgres://user:pass@host:5432/db?ssl=true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateEnvPairs(tt.pairs); err != nil {
				t.Errorf("ValidateEnvPairs(%v) = %v, want nil", tt.pairs, err)
			}
		})
	}
}

func TestValidateEnvPairsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
	}{
		{"no_equals", []string{"JUSTKEY"}},
		{"mixed", []string{"GOOD=val", "BADKEY"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateEnvPairs(tt.pairs); err == nil {
				t.Errorf("ValidateEnvPairs(%v) = nil, want error", tt.pairs)
			}
		})
	}
}

func TestValidateEnvPairsEmpty(t *testing.T) {
	if err := ValidateEnvPairs(nil); err != nil {
		t.Errorf("ValidateEnvPairs(nil) = %v, want nil", err)
	}
	if err := ValidateEnvPairs([]string{}); err != nil {
		t.Errorf("ValidateEnvPairs([]) = %v, want nil", err)
	}
}
