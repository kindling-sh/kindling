package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jeffvincent/kindling/pkg/ci"
)

// ────────────────────────────────────────────────────────────────────────────
// cleanYAMLResponse
// ────────────────────────────────────────────────────────────────────────────

func TestCleanYAMLResponse_StripsFences(t *testing.T) {
	input := "```yaml\nname: test\nkey: value\n```"
	got := cleanYAMLResponse(input)
	want := "name: test\nkey: value"
	if got != want {
		t.Errorf("cleanYAMLResponse() = %q, want %q", got, want)
	}
}

func TestCleanYAMLResponse_StripsPlainFences(t *testing.T) {
	input := "```\nname: test\n```"
	got := cleanYAMLResponse(input)
	want := "name: test"
	if got != want {
		t.Errorf("cleanYAMLResponse() = %q, want %q", got, want)
	}
}

func TestCleanYAMLResponse_PassthroughPlainYAML(t *testing.T) {
	input := "name: test\nkey: value"
	got := cleanYAMLResponse(input)
	if got != input {
		t.Errorf("cleanYAMLResponse() = %q, want %q", got, input)
	}
}

func TestCleanYAMLResponse_TrimsWhitespace(t *testing.T) {
	input := "  \n\n  name: test  \n\n  "
	got := cleanYAMLResponse(input)
	want := "name: test"
	if got != want {
		t.Errorf("cleanYAMLResponse() = %q, want %q", got, want)
	}
}

func TestCleanYAMLResponse_EmptyInput(t *testing.T) {
	got := cleanYAMLResponse("")
	if got != "" {
		t.Errorf("cleanYAMLResponse() = %q, want empty", got)
	}
}

func TestCleanYAMLResponse_OnlyFences(t *testing.T) {
	input := "```yaml\n```"
	got := cleanYAMLResponse(input)
	if got != "" {
		t.Errorf("cleanYAMLResponse() = %q, want empty", got)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// extractEnvVarNames
// ────────────────────────────────────────────────────────────────────────────

func TestExtractEnvVarNames(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected []string
	}{
		{
			name:     "Go os.Getenv",
			line:     `val := os.Getenv("STRIPE_API_KEY")`,
			expected: []string{"STRIPE_API_KEY"},
		},
		{
			name:     "Node process.env",
			line:     `const key = process.env.DATABASE_URL;`,
			expected: []string{"DATABASE_URL"},
		},
		{
			name:     "multiple vars on one line",
			line:     `host := os.Getenv("DB_HOST") + os.Getenv("DB_PORT")`,
			expected: []string{"DB_HOST", "DB_PORT"},
		},
		{
			name:     "no env vars",
			line:     `fmt.Println("hello world")`,
			expected: nil,
		},
		{
			name:     "short names ignored (less than 4 chars)",
			line:     `A_B = 1`,
			expected: nil,
		},
		{
			name:     "env var with digits",
			line:     `ENV AUTH0_CLIENT_ID=something`,
			expected: []string{"AUTH0_CLIENT_ID"},
		},
		{
			name:     "Dockerfile ENV",
			line:     `ENV NODE_ENV=production`,
			expected: []string{"NODE_ENV"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEnvVarNames(tt.line)
			if len(got) == 0 && len(tt.expected) == 0 {
				return
			}
			if len(got) != len(tt.expected) {
				t.Errorf("extractEnvVarNames(%q) = %v, want %v", tt.line, got, tt.expected)
				return
			}
			for i, name := range tt.expected {
				if got[i] != name {
					t.Errorf("extractEnvVarNames(%q)[%d] = %q, want %q", tt.line, i, got[i], name)
				}
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// isUpperOrUnderscore / isDigit
// ────────────────────────────────────────────────────────────────────────────

func TestIsUpperOrUnderscore(t *testing.T) {
	tests := []struct {
		input byte
		want  bool
	}{
		{'A', true}, {'Z', true}, {'_', true},
		{'a', false}, {'z', false}, {'0', false}, {' ', false},
	}
	for _, tt := range tests {
		if got := isUpperOrUnderscore(tt.input); got != tt.want {
			t.Errorf("isUpperOrUnderscore(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsDigit(t *testing.T) {
	tests := []struct {
		input byte
		want  bool
	}{
		{'0', true}, {'9', true}, {'5', true},
		{'a', false}, {'A', false}, {'_', false},
	}
	for _, tt := range tests {
		if got := isDigit(tt.input); got != tt.want {
			t.Errorf("isDigit(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// isExternalCredential
// ────────────────────────────────────────────────────────────────────────────

func TestIsExternalCredential(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		// Should match (credential suffixes / exact names)
		{"STRIPE_API_KEY", true},
		{"MY_SECRET_KEY", true},
		{"JWT_TOKEN", true},
		{"GITHUB_CLIENT_SECRET", true},
		{"AUTH0_CLIENT_ID", true},
		{"SENTRY_DSN", true},
		{"OPENAI_API_KEY", true},
		{"SERVICE_PASSWORD", true},
		{"MY_WEBHOOK_SECRET", true},

		// Should NOT match (dependency-managed)
		{"DATABASE_URL", false},
		{"REDIS_URL", false},
		{"MONGO_URL", false},
		{"POSTGRES_PASSWORD", false},
		{"MYSQL_ROOT_PASSWORD", false},

		// Should NOT match (normal config)
		{"NODE_ENV", false},
		{"PORT", false},
		{"LOG_LEVEL", false},
		{"APP_NAME", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExternalCredential(tt.name); got != tt.want {
				t.Errorf("isExternalCredential(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// readFileCapped
// ────────────────────────────────────────────────────────────────────────────

func TestReadFileCapped_ShortFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	content := "line1\nline2\nline3"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := readFileCapped(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Errorf("readFileCapped() = %q, want %q", got, content)
	}
}

func TestReadFileCapped_LongFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")

	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line content here")
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := readFileCapped(path, 80)
	if err != nil {
		t.Fatal(err)
	}
	gotLines := strings.Split(got, "\n")
	// 80 content lines + 1 truncation notice = 81
	if len(gotLines) != 81 {
		t.Errorf("readFileCapped() returned %d lines, want 81", len(gotLines))
	}
	lastLine := gotLines[len(gotLines)-1]
	if !strings.HasPrefix(lastLine, "...") {
		t.Errorf("last line should be truncation notice, got %q", lastLine)
	}
}

func TestReadFileCapped_MissingFile(t *testing.T) {
	_, err := readFileCapped("/nonexistent/file.txt", 10)
	if err == nil {
		t.Error("readFileCapped() should return error for missing file")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// prioritizeSourceFiles
// ────────────────────────────────────────────────────────────────────────────

func TestPrioritizeSourceFiles(t *testing.T) {
	files := []string{
		"/repo/utils.go",
		"/repo/main.go",
		"/repo/handler.go",
		"/repo/config.go",
	}
	envVarFiles := map[string]bool{"/repo/config.go": true}

	sorted := prioritizeSourceFiles(files, envVarFiles)

	// Both main.go (tier 1) and config.go (boosted to tier 1) are equal
	// priority, so they sort alphabetically: config.go before main.go.
	if sorted[0] != "/repo/config.go" {
		t.Errorf("first file should be config.go (boosted tier 1, alphabetically first), got %s", sorted[0])
	}

	if sorted[1] != "/repo/main.go" {
		t.Errorf("second file should be main.go (tier 1 entry point), got %s", sorted[1])
	}
}

func TestPrioritizeSourceFiles_ServerFiles(t *testing.T) {
	files := []string{
		"/repo/readme_test.go",
		"/repo/server.go",
		"/repo/main.go",
	}

	sorted := prioritizeSourceFiles(files, nil)

	if sorted[0] != "/repo/main.go" {
		t.Errorf("main.go should come first, got %s", sorted[0])
	}
	if sorted[1] != "/repo/server.go" {
		t.Errorf("server.go should come second, got %s", sorted[1])
	}
}

func TestPrioritizeSourceFiles_EmptyList(t *testing.T) {
	sorted := prioritizeSourceFiles(nil, nil)
	if len(sorted) != 0 {
		t.Errorf("expected empty result, got %v", sorted)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// hasEnvVarPatterns
// ────────────────────────────────────────────────────────────────────────────

func TestHasEnvVarPatterns(t *testing.T) {
	dir := t.TempDir()

	t.Run("detects Go os.Getenv", func(t *testing.T) {
		path := filepath.Join(dir, "main.go")
		os.WriteFile(path, []byte(`package main
import "os"
func main() {
	port := os.Getenv("PORT")
	_ = port
}`), 0644)
		if !hasEnvVarPatterns(path) {
			t.Error("should detect os.Getenv")
		}
	})

	t.Run("detects Node process.env", func(t *testing.T) {
		path := filepath.Join(dir, "app.js")
		os.WriteFile(path, []byte(`const port = process.env.PORT;`), 0644)
		if !hasEnvVarPatterns(path) {
			t.Error("should detect process.env")
		}
	})

	t.Run("no patterns returns false", func(t *testing.T) {
		path := filepath.Join(dir, "utils.go")
		os.WriteFile(path, []byte(`package main
func add(a, b int) int { return a + b }`), 0644)
		if hasEnvVarPatterns(path) {
			t.Error("should not detect env var patterns")
		}
	})

	t.Run("missing file returns false", func(t *testing.T) {
		if hasEnvVarPatterns("/nonexistent/file.go") {
			t.Error("missing file should return false")
		}
	})
}

// ────────────────────────────────────────────────────────────────────────────
// detectOAuthRequirements
// ────────────────────────────────────────────────────────────────────────────

func TestDetectOAuthRequirements_WithOAuth(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"app.js": `import NextAuth from "next-auth"`,
		},
	}
	hints, needsExpose := detectOAuthRequirements(ctx)
	if !needsExpose {
		t.Error("should detect OAuth requirement")
	}
	if len(hints) == 0 {
		t.Error("should return hints")
	}

	found := false
	for _, h := range hints {
		if strings.Contains(h, "NextAuth") {
			found = true
		}
	}
	if !found {
		t.Errorf("hints should contain NextAuth, got %v", hints)
	}
}

func TestDetectOAuthRequirements_NoOAuth(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `package main
func main() { http.ListenAndServe(":8080", nil) }`,
		},
	}
	hints, needsExpose := detectOAuthRequirements(ctx)
	if needsExpose {
		t.Error("should not detect OAuth requirement")
	}
	if len(hints) != 0 {
		t.Errorf("should return no hints, got %v", hints)
	}
}

func TestDetectOAuthRequirements_InDockerfile(t *testing.T) {
	ctx := &repoContext{
		dockerfiles: map[string]string{
			"Dockerfile": `FROM node:18
ENV AUTH0_DOMAIN=example.auth0.com`,
		},
	}
	hints, needsExpose := detectOAuthRequirements(ctx)
	if !needsExpose {
		t.Error("should detect Auth0 in Dockerfile")
	}
	if len(hints) == 0 {
		t.Error("should return hints")
	}
}

func TestDetectOAuthRequirements_InComposeFile(t *testing.T) {
	ctx := &repoContext{
		composeFile: `services:
  app:
    environment:
      - NEXTAUTH_URL=http://localhost:3000`,
	}
	hints, needsExpose := detectOAuthRequirements(ctx)
	if !needsExpose {
		t.Error("should detect NextAuth in compose file")
	}
	_ = hints
}

func TestDetectOAuthRequirements_NoDuplicates(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"a.js": `import "next-auth"`,
			"b.js": `import "next-auth"`,
		},
	}
	hints, _ := detectOAuthRequirements(ctx)
	// Check no duplicate descriptions
	seen := map[string]bool{}
	for _, h := range hints {
		if seen[h] {
			t.Errorf("duplicate hint: %q", h)
		}
		seen[h] = true
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectExternalSecrets
// ────────────────────────────────────────────────────────────────────────────

func TestDetectExternalSecrets(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `key := os.Getenv("STRIPE_API_KEY")
url := os.Getenv("DATABASE_URL")
token := os.Getenv("JWT_TOKEN")`,
		},
		dockerfiles: make(map[string]string),
		depFiles:    make(map[string]string),
	}

	secrets := detectExternalSecrets(dir, ctx)

	// Should include STRIPE_API_KEY and JWT_TOKEN
	if !contains(secrets, "STRIPE_API_KEY") {
		t.Error("should detect STRIPE_API_KEY")
	}
	if !contains(secrets, "JWT_TOKEN") {
		t.Error("should detect JWT_TOKEN")
	}

	// Should NOT include DATABASE_URL (dependency-managed)
	if contains(secrets, "DATABASE_URL") {
		t.Error("should not include DATABASE_URL (dependency-managed)")
	}
}

func TestDetectExternalSecrets_FromEnvFile(t *testing.T) {
	dir := t.TempDir()

	// Create .env.example file
	os.WriteFile(filepath.Join(dir, ".env.example"), []byte(
		"STRIPE_API_KEY=sk_test_xxx\nDATABASE_URL=postgres://...\nNODE_ENV=development\n",
	), 0644)

	ctx := &repoContext{
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
	}

	secrets := detectExternalSecrets(dir, ctx)

	if !contains(secrets, "STRIPE_API_KEY") {
		t.Error("should detect STRIPE_API_KEY from .env.example")
	}
	if contains(secrets, "DATABASE_URL") {
		t.Error("should not include DATABASE_URL")
	}
	if contains(secrets, "NODE_ENV") {
		t.Error("should not include NODE_ENV (not a credential)")
	}
}

func TestDetectExternalSecrets_Sorted(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `
SENTRY_DSN = "https://..."
OPENAI_API_KEY = "sk-..."
AWS_SECRET_ACCESS_KEY = "aws..."`,
		},
		dockerfiles: make(map[string]string),
		depFiles:    make(map[string]string),
	}

	secrets := detectExternalSecrets(dir, ctx)
	sorted := make([]string, len(secrets))
	copy(sorted, secrets)
	sort.Strings(sorted)

	for i := range secrets {
		if secrets[i] != sorted[i] {
			t.Errorf("secrets not sorted: got %v", secrets)
			break
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildGeneratePrompt
// ────────────────────────────────────────────────────────────────────────────

func TestBuildGeneratePrompt(t *testing.T) {
	ctx := &repoContext{
		name:   "my-app",
		branch: "main",
		tree:   "main.go\nDockerfile\ngo.mod\n",
		dockerfiles: map[string]string{
			"Dockerfile": "FROM golang:1.21\nCOPY . .\nRUN go build -o /app",
		},
		depFiles: map[string]string{
			"go.mod": "module my-app\ngo 1.21",
		},
		sourceSnippets: map[string]string{
			"main.go": `package main
import "net/http"
func main() { http.ListenAndServe(":8080", nil) }`,
		},
	}

	system, user := buildGeneratePrompt(ctx, ci.Default())

	// System prompt should be non-empty and contain key instructions
	if system == "" {
		t.Error("system prompt should not be empty")
	}
	if !strings.Contains(system, "kindling") {
		t.Error("system prompt should mention kindling")
	}
	if !strings.Contains(system, "kindling-build") {
		t.Error("system prompt should mention kindling-build")
	}
	if !strings.Contains(system, "kindling-deploy") {
		t.Error("system prompt should mention kindling-deploy")
	}

	// User prompt should contain the repo context
	if !strings.Contains(user, "my-app") {
		t.Error("user prompt should contain repo name")
	}
	if !strings.Contains(user, "main") {
		t.Error("user prompt should contain branch name")
	}
	if !strings.Contains(user, "main.go") {
		t.Error("user prompt should contain tree entries")
	}
	if !strings.Contains(user, "FROM golang:1.21") {
		t.Error("user prompt should contain Dockerfile content")
	}
	if !strings.Contains(user, "module my-app") {
		t.Error("user prompt should contain dep file content")
	}
}

func TestBuildGeneratePrompt_WithSecrets(t *testing.T) {
	ctx := &repoContext{
		name:            "secret-app",
		branch:          "develop",
		tree:            "main.go\n",
		externalSecrets: []string{"STRIPE_API_KEY", "SENTRY_DSN"},
		dockerfiles:     make(map[string]string),
		depFiles:        make(map[string]string),
		sourceSnippets:  make(map[string]string),
	}

	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "STRIPE_API_KEY") {
		t.Error("user prompt should list detected credentials")
	}
	if !strings.Contains(user, "SENTRY_DSN") {
		t.Error("user prompt should list detected credentials")
	}
}

func TestBuildGeneratePrompt_WithOAuth(t *testing.T) {
	ctx := &repoContext{
		name:              "oauth-app",
		branch:            "main",
		tree:              "app.js\n",
		needsPublicExpose: true,
		oauthHints:        []string{"NextAuth.js", "OAuth callback endpoint"},
		dockerfiles:       make(map[string]string),
		depFiles:          make(map[string]string),
		sourceSnippets:    make(map[string]string),
	}

	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "NextAuth.js") {
		t.Error("user prompt should include OAuth hints")
	}
	if !strings.Contains(user, "kindling expose") {
		t.Error("user prompt should mention kindling expose")
	}
}

func TestBuildGeneratePrompt_WithComposeFile(t *testing.T) {
	ctx := &repoContext{
		name:   "compose-app",
		branch: "main",
		tree:   "docker-compose.yml\n",
		composeFile: `services:
  web:
    build: .
    ports:
      - "8080:8080"`,
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
	}

	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "docker-compose.yml") {
		t.Error("user prompt should contain compose file reference")
	}
	if !strings.Contains(user, "8080:8080") {
		t.Error("user prompt should contain compose file content")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// scanRepo (integration test using temp directory)
// ────────────────────────────────────────────────────────────────────────────

func TestScanRepo(t *testing.T) {
	dir := t.TempDir()

	// Create project structure
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM golang:1.21\nRUN go build"), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test-app\ngo 1.21"), 0644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import "os"
func main() { _ = os.Getenv("PORT") }`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if ctx.name != filepath.Base(dir) {
		t.Errorf("name = %q, want %q", ctx.name, filepath.Base(dir))
	}
	if ctx.dockerfileCount != 1 {
		t.Errorf("dockerfileCount = %d, want 1", ctx.dockerfileCount)
	}
	if ctx.depFileCount != 1 {
		t.Errorf("depFileCount = %d, want 1", ctx.depFileCount)
	}
	if len(ctx.sourceSnippets) < 1 {
		t.Error("should find at least one source snippet")
	}
}

func TestScanRepo_SkipsDirs(t *testing.T) {
	dir := t.TempDir()

	// Create node_modules (should be skipped)
	nmDir := filepath.Join(dir, "node_modules", "some-pkg")
	os.MkdirAll(nmDir, 0755)
	os.WriteFile(filepath.Join(nmDir, "index.js"), []byte("module.exports = {}"), 0644)

	// Create a real source file
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("const x = 1;"), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	// tree should NOT contain node_modules
	if strings.Contains(ctx.tree, "node_modules") {
		t.Error("tree should not contain node_modules")
	}
}

func TestScanRepo_DetectsCompose(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(`services:
  web:
    build: .`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if ctx.composeFile == "" {
		t.Error("should detect docker-compose.yml")
	}
}

func TestScanRepo_DetectsCredentials(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import "os"
func main() {
	key := os.Getenv("STRIPE_API_KEY")
	_ = key
}`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if !contains(ctx.externalSecrets, "STRIPE_API_KEY") {
		t.Error("should detect STRIPE_API_KEY as external secret")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// scanSkipDirs / scanDepFiles / scanSourceExts data verification
// ────────────────────────────────────────────────────────────────────────────

func TestScanSkipDirs_ContainsCommonDirs(t *testing.T) {
	expected := []string{".git", "node_modules", "vendor", "__pycache__", ".venv", "dist", "build"}
	for _, d := range expected {
		if !scanSkipDirs[d] {
			t.Errorf("scanSkipDirs missing %q", d)
		}
	}
}

func TestScanDepFiles_ContainsCommonFiles(t *testing.T) {
	expected := []string{"go.mod", "package.json", "requirements.txt", "Cargo.toml", "pom.xml", "Gemfile"}
	for _, f := range expected {
		if !scanDepFiles[f] {
			t.Errorf("scanDepFiles missing %q", f)
		}
	}
}

func TestScanSourceExts_ContainsCommonExts(t *testing.T) {
	expected := []string{".go", ".py", ".js", ".ts", ".rs", ".java", ".rb", ".php"}
	for _, e := range expected {
		if !scanSourceExts[e] {
			t.Errorf("scanSourceExts missing %q", e)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
