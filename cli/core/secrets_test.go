package core

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────────────────
// KindlingSecretName
// ────────────────────────────────────────────────────────────────────────────

func TestKindlingSecretName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"STRIPE_API_KEY", "kindling-secret-stripe-api-key"},
		{"DATABASE_URL", "kindling-secret-database-url"},
		{"simple", "kindling-secret-simple"},
		{"UPPER", "kindling-secret-upper"},
		{"already_lower", "kindling-secret-already-lower"},
		{"A_B_C", "kindling-secret-a-b-c"},
		{"SINGLE", "kindling-secret-single"},
		{"", "kindling-secret-"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := KindlingSecretName(tt.input); got != tt.want {
				t.Errorf("KindlingSecretName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// ParseSecretKeys
// ────────────────────────────────────────────────────────────────────────────

func TestParseSecretKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			"typical",
			"map[API_KEY:c29tZXZhbHVl value:c29tZXZhbHVl]",
			[]string{"API_KEY", "value"},
		},
		{
			"single_key",
			"map[token:YWJj]",
			[]string{"token"},
		},
		{
			"empty",
			"map[]",
			nil,
		},
		{
			"really_empty",
			"",
			nil,
		},
		{
			"three_keys",
			"map[a:Zm9v b:YmFy c:YmF6]",
			[]string{"a", "b", "c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSecretKeys(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSecretKeys(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.want, len(tt.want))
			}
			for i, k := range tt.want {
				if got[i] != k {
					t.Errorf("ParseSecretKeys(%q)[%d] = %q, want %q",
						tt.input, i, got[i], k)
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// SecretConfig.namespace()
// ────────────────────────────────────────────────────────────────────────────

func TestSecretConfigNamespace(t *testing.T) {
	tests := []struct {
		ns   string
		want string
	}{
		{"", "default"},
		{"kube-system", "kube-system"},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		cfg := SecretConfig{Namespace: tt.ns}
		if got := cfg.namespace(); got != tt.want {
			t.Errorf("SecretConfig{Namespace: %q}.namespace() = %q, want %q", tt.ns, got, tt.want)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Constants
// ────────────────────────────────────────────────────────────────────────────

func TestSecretsConstants(t *testing.T) {
	if SecretsLabelKey != "app.kubernetes.io/managed-by" {
		t.Errorf("SecretsLabelKey = %q", SecretsLabelKey)
	}
	if SecretsLabelValue != "kindling" {
		t.Errorf("SecretsLabelValue = %q", SecretsLabelValue)
	}
}
