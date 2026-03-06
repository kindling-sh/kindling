package cmd

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Tests for ensureGitignored and secrets round-trip live in commands_test.go.

// ────────────────────────────────────────────────────────────────────────────
// Secrets file parsing edge cases
// ────────────────────────────────────────────────────────────────────────────

func TestSecretsFileParsing(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect map[string]string
	}{
		{
			name:   "comment lines skipped",
			input:  "# comment\n# another comment\n",
			expect: map[string]string{},
		},
		{
			name:   "blank lines skipped",
			input:  "\n\n\n",
			expect: map[string]string{},
		},
		{
			name:   "valid entry",
			input:  "MY_KEY: " + base64.StdEncoding.EncodeToString([]byte("myvalue")) + "\n",
			expect: map[string]string{"MY_KEY": "myvalue"},
		},
		{
			name:   "invalid base64 skipped",
			input:  "BAD_KEY: not-valid-base64!!!\n",
			expect: map[string]string{},
		},
		{
			name:   "line without colon-space skipped",
			input:  "NODELIMITER=value\n",
			expect: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSecretsContent(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("parseSecretsContent() = %v (len %d), want %v (len %d)",
					got, len(got), tt.expect, len(tt.expect))
			}
			for k, v := range tt.expect {
				if got[k] != v {
					t.Errorf("key %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// parseSecretsContent replicates the parsing logic from loadSecretsLocally
// for testing without filesystem dependency on the project directory.
func parseSecretsContent(content string) map[string]string {
	secrets := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		encoded := strings.TrimSpace(parts[1])
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		secrets[key] = string(decoded)
	}
	return secrets
}
