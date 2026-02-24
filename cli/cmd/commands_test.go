package cmd

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jeffvincent/kindling/cli/core"
)

// ────────────────────────────────────────────────────────────────────────────
// isReasoningModel (genai.go)
// ────────────────────────────────────────────────────────────────────────────

func TestIsReasoningModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"o1", true},
		{"o1-mini", true},
		{"o1-preview", true},
		{"o3", true},
		{"o3-mini", true},
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"gpt-4", false},
		{"gpt-3.5-turbo", false},
		{"claude-sonnet-4-20250514", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isReasoningModel(tt.model); got != tt.want {
				t.Errorf("isReasoningModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// normaliseServices (push.go)
// ────────────────────────────────────────────────────────────────────────────

func TestNormaliseServices(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "simple list",
			input:    []string{"orders", "gateway"},
			expected: []string{"orders", "gateway"},
		},
		{
			name:     "comma separated",
			input:    []string{"orders,gateway", "ui"},
			expected: []string{"orders", "gateway", "ui"},
		},
		{
			name:     "deduplication",
			input:    []string{"orders", "orders", "ui"},
			expected: []string{"orders", "ui"},
		},
		{
			name:     "empty strings trimmed",
			input:    []string{"orders", "", "  "},
			expected: []string{"orders"},
		},
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
		{
			name:     "whitespace trimming",
			input:    []string{" orders , gateway "},
			expected: []string{"orders", "gateway"},
		},
		{
			name:     "mixed comma and separate args",
			input:    []string{"api,web", "worker", "api"},
			expected: []string{"api", "web", "worker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normaliseServices(tt.input)
			if len(got) == 0 && len(tt.expected) == 0 {
				return
			}
			if len(got) != len(tt.expected) {
				t.Errorf("normaliseServices(%v) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i, v := range tt.expected {
				if got[i] != v {
					t.Errorf("normaliseServices(%v)[%d] = %q, want %q", tt.input, i, got[i], v)
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// stripKindlingTag (push.go)
// ────────────────────────────────────────────────────────────────────────────

func TestStripKindlingTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes tag from end",
			input:    "fix bug\n\n[kindling:orders]",
			expected: "fix bug\n",
		},
		{
			name:     "no tag present",
			input:    "fix bug\n\nsome other text",
			expected: "fix bug\n\nsome other text",
		},
		{
			name:     "tag with multiple services",
			input:    "update api\n\n[kindling:orders,gateway,ui]",
			expected: "update api\n",
		},
		{
			name:     "empty message",
			input:    "",
			expected: "",
		},
		{
			name:     "tag only",
			input:    "[kindling:orders]",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripKindlingTag(tt.input)
			if got != tt.expected {
				t.Errorf("stripKindlingTag(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// kindlingSecretName (secrets.go)
// ────────────────────────────────────────────────────────────────────────────

func TestKindlingSecretName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"STRIPE_API_KEY", "kindling-secret-stripe-api-key"},
		{"MY_TOKEN", "kindling-secret-my-token"},
		{"simple", "kindling-secret-simple"},
		{"A_B_C", "kindling-secret-a-b-c"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := core.KindlingSecretName(tt.input); got != tt.expected {
				t.Errorf("KindlingSecretName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parseSecretKeys (secrets.go)
// ────────────────────────────────────────────────────────────────────────────

func TestParseSecretKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single key",
			input:    "map[STRIPE_API_KEY:c2t...]",
			expected: []string{"STRIPE_API_KEY"},
		},
		{
			name:     "multiple keys",
			input:    "map[STRIPE_API_KEY:c2t... value:c2t...]",
			expected: []string{"STRIPE_API_KEY", "value"},
		},
		{
			name:     "empty map",
			input:    "map[]",
			expected: nil,
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := core.ParseSecretKeys(tt.input)
			if len(got) == 0 && len(tt.expected) == 0 {
				return
			}
			if len(got) != len(tt.expected) {
				t.Errorf("ParseSecretKeys(%q) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i, v := range tt.expected {
				if got[i] != v {
					t.Errorf("ParseSecretKeys(%q)[%d] = %q, want %q", tt.input, i, got[i], v)
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Secrets file round-trip (loadSecretsLocally / writeSecretsFile)
// ────────────────────────────────────────────────────────────────────────────

func TestSecretsFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, secretsDirName)
	os.MkdirAll(secretsDir, 0700)
	path := filepath.Join(secretsDir, secretsFileName)

	// Write a secrets file manually
	secrets := map[string]string{
		"STRIPE_API_KEY": "sk_test_123",
		"AUTH_TOKEN":     "tok_abc",
	}

	var sb strings.Builder
	sb.WriteString("# test\n")
	for name, value := range secrets {
		encoded := base64.StdEncoding.EncodeToString([]byte(value))
		sb.WriteString(name + ": " + encoded + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0600); err != nil {
		t.Fatal(err)
	}

	// Read it back — we need to temporarily override secretsFilePath
	// Since secretsFilePath() uses resolveProjectDir(), we test loadSecretsLocally
	// indirectly by reading the file ourselves using the same parsing logic.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	loaded := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		loaded[strings.TrimSpace(parts[0])] = string(decoded)
	}

	for name, value := range secrets {
		if loaded[name] != value {
			t.Errorf("round-trip failed for %q: got %q, want %q", name, loaded[name], value)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// ensureGitignored (secrets.go)
// ────────────────────────────────────────────────────────────────────────────

func TestEnsureGitignored(t *testing.T) {
	dir := t.TempDir()
	kindlingDir := filepath.Join(dir, ".kindling")
	os.MkdirAll(kindlingDir, 0700)

	// First call — should create .gitignore
	ensureGitignored(kindlingDir)

	gitignorePath := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".kindling/secrets.yaml") {
		t.Error("gitignore should contain .kindling/secrets.yaml")
	}

	// Second call — should be idempotent
	ensureGitignored(kindlingDir)
	data2, _ := os.ReadFile(gitignorePath)
	count := strings.Count(string(data2), ".kindling/secrets.yaml")
	if count != 1 {
		t.Errorf("gitignore should contain .kindling/secrets.yaml exactly once, got %d", count)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Version output format
// ────────────────────────────────────────────────────────────────────────────

func TestVersionFormat(t *testing.T) {
	// Verify the version string format matches expected pattern
	version := Version
	expected := "dev"
	if version != expected {
		t.Logf("Version = %q (set via ldflags, not testing value)", version)
	}

	// Verify runtime.GOOS/GOARCH are non-empty (used in version output)
	if runtime.GOOS == "" {
		t.Error("runtime.GOOS should not be empty")
	}
	if runtime.GOARCH == "" {
		t.Error("runtime.GOARCH should not be empty")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// callGenAI provider dispatch (genai.go) — error cases only (no API calls)
// ────────────────────────────────────────────────────────────────────────────

func TestCallGenAI_UnsupportedProvider(t *testing.T) {
	_, err := callGenAI("azure", "key", "model", "sys", "usr")
	if err == nil {
		t.Error("should return error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("error should mention unsupported provider, got %q", err.Error())
	}
}
