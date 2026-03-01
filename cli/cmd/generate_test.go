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

// ────────────────────────────────────────────────────────────────────────────
// detectAgentFrameworks
// ────────────────────────────────────────────────────────────────────────────

func TestDetectAgentFrameworks_CrewAI(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "crewai==0.28.0\nlangchain>=0.1",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if !contains(frameworks, "CrewAI") {
		t.Errorf("should detect CrewAI, got %v", frameworks)
	}
	if !contains(frameworks, "LangChain") {
		t.Errorf("should detect LangChain, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_LangGraph(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"graph.py": `from langgraph.graph import StateGraph
graph = StateGraph(AgentState)`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if !contains(frameworks, "LangGraph") {
		t.Errorf("should detect LangGraph, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_OpenAIAgents(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"pyproject.toml": `[project]
dependencies = ["openai-agents>=0.1"]`,
		},
		sourceSnippets: map[string]string{
			"main.py": `from openai.agents import Agent, Runner`,
		},
		dockerfiles: make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if !contains(frameworks, "OpenAI Agents SDK") {
		t.Errorf("should detect OpenAI Agents SDK, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_Anthropic(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"app.py": `from anthropic import Anthropic
client = Anthropic()`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if !contains(frameworks, "Anthropic Claude SDK") {
		t.Errorf("should detect Anthropic Claude SDK, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_AutoGen(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "pyautogen>=0.2",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if !contains(frameworks, "AutoGen") {
		t.Errorf("should detect AutoGen, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_Strands(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "strands-agents>=0.1",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if !contains(frameworks, "Strands Agents") {
		t.Errorf("should detect Strands Agents, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_None(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `package main
func main() { http.ListenAndServe(":8080", nil) }`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	if len(frameworks) != 0 {
		t.Errorf("should detect no frameworks, got %v", frameworks)
	}
}

func TestDetectAgentFrameworks_NoDuplicates(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"a.py": `from langchain import LLMChain`,
			"b.py": `from langchain.agents import AgentExecutor`,
		},
		depFiles: map[string]string{
			"requirements.txt": "langchain>=0.1",
		},
		dockerfiles: make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	count := 0
	for _, f := range frameworks {
		if f == "LangChain" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("LangChain should appear exactly once, got %d in %v", count, frameworks)
	}
}

func TestDetectAgentFrameworks_Sorted(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "crewai\nlangchain\nlanggraph",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	frameworks := detectAgentFrameworks(ctx)
	sorted := make([]string, len(frameworks))
	copy(sorted, frameworks)
	sort.Strings(sorted)
	for i := range frameworks {
		if frameworks[i] != sorted[i] {
			t.Errorf("frameworks not sorted: got %v", frameworks)
			break
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectMCPServers
// ────────────────────────────────────────────────────────────────────────────

func TestDetectMCPServers_ConfigFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{"servers": {}}`), 0644)

	ctx := &repoContext{
		tree: "mcp.json\napp.py\n",
		depFiles: map[string]string{
			"mcp.json": `{"servers": {}}`,
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	servers := detectMCPServers(dir, ctx)
	found := false
	for _, s := range servers {
		if strings.Contains(s, "mcp.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect mcp.json, got %v", servers)
	}
}

func TestDetectMCPServers_PythonDecorator(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		tree: "server.py\n",
		sourceSnippets: map[string]string{
			"server.py": `from mcp.server import FastMCP

app = FastMCP("my-tools")

@app.tool()
def search(query: str) -> str:
    return "result"`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	servers := detectMCPServers(dir, ctx)
	if len(servers) == 0 {
		t.Error("should detect MCP server patterns")
	}
	foundFastMCP := false
	foundImport := false
	for _, s := range servers {
		if strings.Contains(s, "FastMCP") {
			foundFastMCP = true
		}
		if strings.Contains(s, "MCP server Python import") {
			foundImport = true
		}
	}
	if !foundFastMCP {
		t.Errorf("should detect FastMCP, got %v", servers)
	}
	if !foundImport {
		t.Errorf("should detect MCP server import, got %v", servers)
	}
}

func TestDetectMCPServers_NodeSDK(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		tree: "server.ts\n",
		sourceSnippets: map[string]string{
			"server.ts": `import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	servers := detectMCPServers(dir, ctx)
	foundStdio := false
	foundPkg := false
	for _, s := range servers {
		if strings.Contains(s, "stdio") {
			foundStdio = true
		}
		if strings.Contains(s, "@modelcontextprotocol") {
			foundPkg = true
		}
	}
	if !foundStdio {
		t.Errorf("should detect StdioServerTransport, got %v", servers)
	}
	if !foundPkg {
		t.Errorf("should detect @modelcontextprotocol, got %v", servers)
	}
}

func TestDetectMCPServers_None(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		tree: "main.go\n",
		sourceSnippets: map[string]string{
			"main.go": `package main; func main() {}`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	servers := detectMCPServers(dir, ctx)
	if len(servers) != 0 {
		t.Errorf("should detect no MCP servers, got %v", servers)
	}
}

func TestDetectMCPServers_TreeDetection(t *testing.T) {
	dir := t.TempDir()
	ctx := &repoContext{
		tree:           "src/app.py\ntools/mcp.json\n",
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	servers := detectMCPServers(dir, ctx)
	found := false
	for _, s := range servers {
		if strings.Contains(s, "mcp.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect mcp.json in tree, got %v", servers)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectVectorStores
// ────────────────────────────────────────────────────────────────────────────

func TestDetectVectorStores_ChromaDB(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "chromadb>=0.4\nlangchain",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	stores := detectVectorStores(ctx)
	if !contains(stores, "ChromaDB") {
		t.Errorf("should detect ChromaDB, got %v", stores)
	}
}

func TestDetectVectorStores_PGVector(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"db.py": `from pgvector.sqlalchemy import Vector`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	stores := detectVectorStores(ctx)
	if !contains(stores, "pgvector") {
		t.Errorf("should detect pgvector, got %v", stores)
	}
}

func TestDetectVectorStores_Pinecone(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"package.json": `{"dependencies": {"@pinecone-database/pinecone": "^1.0"}}`,
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	stores := detectVectorStores(ctx)
	if !contains(stores, "Pinecone") {
		t.Errorf("should detect Pinecone, got %v", stores)
	}
}

func TestDetectVectorStores_Multiple(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "chromadb\npgvector\nqdrant-client",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	stores := detectVectorStores(ctx)
	if !contains(stores, "ChromaDB") {
		t.Errorf("should detect ChromaDB, got %v", stores)
	}
	if !contains(stores, "pgvector") {
		t.Errorf("should detect pgvector, got %v", stores)
	}
	if !contains(stores, "Qdrant") {
		t.Errorf("should detect Qdrant, got %v", stores)
	}
}

func TestDetectVectorStores_None(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `package main; func main() {}`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	stores := detectVectorStores(ctx)
	if len(stores) != 0 {
		t.Errorf("should detect no vector stores, got %v", stores)
	}
}

func TestDetectVectorStores_Sorted(t *testing.T) {
	ctx := &repoContext{
		depFiles: map[string]string{
			"requirements.txt": "qdrant-client\nchromadb\npgvector",
		},
		sourceSnippets: make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	stores := detectVectorStores(ctx)
	sorted := make([]string, len(stores))
	copy(sorted, stores)
	sort.Strings(sorted)
	for i := range stores {
		if stores[i] != sorted[i] {
			t.Errorf("stores not sorted: got %v", stores)
			break
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// detectWorkerProcesses
// ────────────────────────────────────────────────────────────────────────────

func TestDetectWorkerProcesses_Celery(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"tasks.py": `from celery import Celery

app = celery.Celery('myapp')

@app.task
def process_data(data):
    return data`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	if len(workers) == 0 {
		t.Error("should detect Celery worker")
	}
	found := false
	for _, w := range workers {
		if strings.Contains(w, "Celery") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect Celery, got %v", workers)
	}
}

func TestDetectWorkerProcesses_Kafka(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"consumer.py": `from kafka import KafkaConsumer
consumer = KafkaConsumer('my-topic')`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	found := false
	for _, w := range workers {
		if strings.Contains(w, "Kafka") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect Kafka consumer, got %v", workers)
	}
}

func TestDetectWorkerProcesses_RabbitMQ(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"worker.py": `import pika
connection = pika.BlockingConnection(pika.ConnectionParameters('localhost'))`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	found := false
	for _, w := range workers {
		if strings.Contains(w, "RabbitMQ") || strings.Contains(w, "pika") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect RabbitMQ consumer, got %v", workers)
	}
}

func TestDetectWorkerProcesses_Sidekiq(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"worker.rb": `class HardWorker
  include Sidekiq::Worker
  def perform(name)
    puts "Working on #{name}"
  end
end`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	found := false
	for _, w := range workers {
		if strings.Contains(w, "Sidekiq") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect Sidekiq worker, got %v", workers)
	}
}

func TestDetectWorkerProcesses_BullMQ(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"worker.ts": `import { Worker } from 'bullmq';
const worker = new Worker('queue', async job => {});`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	foundBullMQ := false
	foundWorker := false
	for _, w := range workers {
		if strings.Contains(w, "BullMQ") && strings.Contains(w, "worker") {
			foundWorker = true
		}
		if w == "BullMQ" {
			foundBullMQ = true
		}
	}
	if !foundBullMQ && !foundWorker {
		t.Errorf("should detect BullMQ worker, got %v", workers)
	}
}

func TestDetectWorkerProcesses_ComposeWorker(t *testing.T) {
	ctx := &repoContext{
		composeFile: `services:
  web:
    build: .
  celery-worker:
    build: .
    command: celery -A myapp worker`,
		sourceSnippets: make(map[string]string),
		depFiles:       make(map[string]string),
		dockerfiles:    make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	if len(workers) == 0 {
		t.Error("should detect workers from compose file")
	}
}

func TestDetectWorkerProcesses_None(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `package main; func main() { http.ListenAndServe(":8080", nil) }`,
		},
		depFiles:    make(map[string]string),
		dockerfiles: make(map[string]string),
	}
	workers := detectWorkerProcesses(ctx)
	if len(workers) != 0 {
		t.Errorf("should detect no workers, got %v", workers)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// mergeAllContent
// ────────────────────────────────────────────────────────────────────────────

func TestMergeAllContent(t *testing.T) {
	ctx := &repoContext{
		dockerfiles: map[string]string{"Dockerfile": "FROM node:18"},
		depFiles:    map[string]string{"package.json": `{"name":"app"}`},
		sourceSnippets: map[string]string{
			"app.js": "console.log('hello')",
		},
		composeFile: "services:\n  web:\n    build: .",
	}
	merged := mergeAllContent(ctx)
	if len(merged) != 4 {
		t.Errorf("expected 4 entries, got %d", len(merged))
	}
	if merged["Dockerfile"] != "FROM node:18" {
		t.Error("should include Dockerfile content")
	}
	if merged["docker-compose.yml"] != ctx.composeFile {
		t.Error("should include compose file")
	}
}

func TestMergeAllContent_NoCompose(t *testing.T) {
	ctx := &repoContext{
		dockerfiles:    map[string]string{"Dockerfile": "FROM go:1.21"},
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
	}
	merged := mergeAllContent(ctx)
	if _, ok := merged["docker-compose.yml"]; ok {
		t.Error("should not include compose key when no compose file")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// buildGeneratePrompt with multi-agent context
// ────────────────────────────────────────────────────────────────────────────

func TestBuildGeneratePrompt_WithAgentFrameworks(t *testing.T) {
	ctx := &repoContext{
		name:            "agent-app",
		branch:          "main",
		tree:            "main.py\nDockerfile\n",
		agentFrameworks: []string{"CrewAI", "LangChain"},
		vectorStores:    []string{"ChromaDB", "pgvector"},
		mcpServers:      []string{"MCP config file: mcp.json"},
		workerProcesses: []string{"Celery worker"},
		dockerfiles:     make(map[string]string),
		depFiles:        make(map[string]string),
		sourceSnippets:  make(map[string]string),
	}

	system, user := buildGeneratePrompt(ctx, ci.Default())

	// System prompt should contain multi-agent guidance
	if !strings.Contains(system, "Multi-agent architecture") {
		t.Error("system prompt should contain multi-agent architecture guidance")
	}
	if !strings.Contains(system, "MCP") {
		t.Error("system prompt should mention MCP")
	}

	// User prompt should contain all detected patterns
	if !strings.Contains(user, "multi-agent architecture") {
		t.Error("user prompt should contain multi-agent section")
	}
	if !strings.Contains(user, "CrewAI") {
		t.Error("user prompt should list CrewAI")
	}
	if !strings.Contains(user, "LangChain") {
		t.Error("user prompt should list LangChain")
	}
	if !strings.Contains(user, "ChromaDB") {
		t.Error("user prompt should list ChromaDB")
	}
	if !strings.Contains(user, "pgvector") {
		t.Error("user prompt should list pgvector")
	}
	if !strings.Contains(user, "mcp.json") {
		t.Error("user prompt should list MCP config")
	}
	if !strings.Contains(user, "Celery") {
		t.Error("user prompt should list Celery worker")
	}
}

func TestBuildGeneratePrompt_NoAgentArch(t *testing.T) {
	ctx := &repoContext{
		name:           "plain-app",
		branch:         "main",
		tree:           "main.go\n",
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
	}

	_, user := buildGeneratePrompt(ctx, ci.Default())

	if strings.Contains(user, "multi-agent architecture") {
		t.Error("user prompt should NOT contain multi-agent section for plain apps")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// scanRepo integration: multi-agent detection
// ────────────────────────────────────────────────────────────────────────────

func TestScanRepo_DetectsAgentFrameworks(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.py"), []byte(`from crewai import Agent, Task, Crew
from langchain.llms import OpenAI`), 0644)
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("crewai\nlangchain"), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if !contains(ctx.agentFrameworks, "CrewAI") {
		t.Errorf("should detect CrewAI, got %v", ctx.agentFrameworks)
	}
}

func TestScanRepo_DetectsMCPServer(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{"servers":{"search":{}}}`), 0644)
	os.WriteFile(filepath.Join(dir, "server.py"), []byte(`from mcp.server import FastMCP
app = FastMCP("search")
@app.tool()
def search(q: str): pass`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if len(ctx.mcpServers) == 0 {
		t.Error("should detect MCP servers")
	}
}

func TestScanRepo_DetectsVectorStores(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.py"), []byte(`import chromadb
client = chromadb.Client()`), 0644)
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("chromadb"), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if !contains(ctx.vectorStores, "ChromaDB") {
		t.Errorf("should detect ChromaDB, got %v", ctx.vectorStores)
	}
}

func TestScanRepo_DetectsWorkerProcesses(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "tasks.py"), []byte(`from celery import Celery
app = celery.Celery('myapp')
@app.task
def add(x, y): return x + y`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if len(ctx.workerProcesses) == 0 {
		t.Error("should detect Celery worker processes")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// scanDepFiles includes MCP config files
// ────────────────────────────────────────────────────────────────────────────

func TestScanDepFiles_IncludesMCPConfig(t *testing.T) {
	if !scanDepFiles["mcp.json"] {
		t.Error("scanDepFiles should include mcp.json")
	}
	if !scanDepFiles["mcp.config.json"] {
		t.Error("scanDepFiles should include mcp.config.json")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// scanDepFiles includes Procfile
// ────────────────────────────────────────────────────────────────────────────

func TestScanDepFiles_IncludesProcfile(t *testing.T) {
	if !scanDepFiles["Procfile"] {
		t.Error("scanDepFiles should include Procfile")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Inter-service call detection
// ────────────────────────────────────────────────────────────────────────────

func TestDetectInterServiceCalls_HTTPLocalhost(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"app.py": `resp = requests.get("http://localhost:8080/api/orders")`,
		},
	}
	result := detectInterServiceCalls(ctx)
	if len(result) == 0 {
		t.Error("should detect HTTP localhost call")
	}
	found := false
	for _, r := range result {
		if strings.Contains(r, "localhost") || strings.Contains(r, "requests.get") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect localhost or requests.get pattern, got %v", result)
	}
}

func TestDetectInterServiceCalls_GoHTTP(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `resp, err := http.Get("http://inventory:3000/items")`,
		},
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "http.Get") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect Go http.Get, got %v", result)
	}
}

func TestDetectInterServiceCalls_GRPCDial(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"client.go": `conn, err := grpc.Dial("orders:50051", grpc.WithInsecure())`,
		},
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "gRPC") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect gRPC dial, got %v", result)
	}
}

func TestDetectInterServiceCalls_PythonGRPC(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"client.py": `channel = grpc.insecure_channel("mcp-server:50051")`,
		},
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "gRPC") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect Python gRPC channel, got %v", result)
	}
}

func TestDetectInterServiceCalls_ServiceEnvVar(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"config.py": `ORDERS_SERVICE_URL = os.getenv("ORDERS_SERVICE_URL")`,
		},
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "Service URL") || strings.Contains(r, "Endpoint") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect service URL env var, got %v", result)
	}
}

func TestDetectInterServiceCalls_ComposeDepends(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: make(map[string]string),
		composeFile: `services:
  api:
    depends_on:
      - orders
      - inventory`,
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "depends_on") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect compose depends_on, got %v", result)
	}
}

func TestDetectInterServiceCalls_None(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"main.go": `func main() { fmt.Println("hello") }`,
		},
	}
	result := detectInterServiceCalls(ctx)
	if len(result) != 0 {
		t.Errorf("should detect no inter-service calls, got %v", result)
	}
}

func TestDetectInterServiceCalls_FetchJS(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"api.ts": `const res = await fetch("http://mcp-server:8080/tools")`,
		},
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "fetch") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect JS fetch, got %v", result)
	}
}

func TestDetectInterServiceCalls_AxiosPost(t *testing.T) {
	ctx := &repoContext{
		sourceSnippets: map[string]string{
			"service.ts": `await axios.post("http://worker:3001/enqueue", data)`,
		},
	}
	result := detectInterServiceCalls(ctx)
	found := false
	for _, r := range result {
		if strings.Contains(r, "Axios") {
			found = true
		}
	}
	if !found {
		t.Errorf("should detect Axios POST, got %v", result)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Vector store API key credential detection
// ────────────────────────────────────────────────────────────────────────────

func TestCredentialExactNames_VectorStoreKeys(t *testing.T) {
	keys := []string{
		"PINECONE_API_KEY", "WEAVIATE_API_KEY", "QDRANT_API_KEY",
		"MILVUS_API_KEY", "COHERE_API_KEY", "HUGGINGFACE_API_KEY",
		"HF_TOKEN", "GROQ_API_KEY", "MISTRAL_API_KEY",
		"TOGETHER_API_KEY", "REPLICATE_API_TOKEN",
	}
	for _, k := range keys {
		if !credentialExactNames[k] {
			t.Errorf("credentialExactNames should include %s", k)
		}
	}
}

func TestIsExternalCredential_VectorStoreKeys(t *testing.T) {
	keys := []string{
		"PINECONE_API_KEY", "WEAVIATE_API_KEY", "QDRANT_API_KEY",
		"COHERE_API_KEY", "GROQ_API_KEY",
	}
	for _, k := range keys {
		if !isExternalCredential(k) {
			t.Errorf("isExternalCredential(%q) should return true", k)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Directive prompt output
// ────────────────────────────────────────────────────────────────────────────

func TestBuildGeneratePrompt_DirectiveMCP(t *testing.T) {
	ctx := &repoContext{
		name:           "mcp-app",
		branch:         "main",
		tree:           "server.py\nDockerfile\nmcp.json\n",
		mcpServers:     []string{"MCP config file: mcp.json", "MCP tool decorator (@mcp.tool)"},
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
	}
	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "DIRECTIVE") {
		t.Error("user prompt should contain DIRECTIVE for MCP servers")
	}
	if !strings.Contains(user, "separate build+deploy") {
		t.Error("user prompt should instruct separate build+deploy for MCP servers")
	}
}

func TestBuildGeneratePrompt_DirectiveWorkers(t *testing.T) {
	ctx := &repoContext{
		name:            "celery-app",
		branch:          "main",
		tree:            "app.py\nDockerfile\n",
		workerProcesses: []string{"Celery worker (celery -A)"},
		dockerfiles:     make(map[string]string),
		depFiles:        make(map[string]string),
		sourceSnippets:  make(map[string]string),
	}
	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "DIRECTIVE") {
		t.Error("user prompt should contain DIRECTIVE for workers")
	}
	if !strings.Contains(user, "separate deploy step") {
		t.Error("user prompt should instruct separate deploy for workers")
	}
	if !strings.Contains(user, "broker dependency") {
		t.Error("user prompt should mention broker dependency wiring")
	}
}

func TestBuildGeneratePrompt_DirectiveVectorStores(t *testing.T) {
	ctx := &repoContext{
		name:           "rag-app",
		branch:         "main",
		tree:           "main.py\nDockerfile\n",
		vectorStores:   []string{"Pinecone", "pgvector"},
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
	}
	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "DIRECTIVE") {
		t.Error("user prompt should contain DIRECTIVE for vector stores")
	}
	if !strings.Contains(user, "do NOT auto-add local dependencies") {
		t.Error("user prompt should instruct not to auto-add local deps")
	}
	if !strings.Contains(user, "secretKeyRef") {
		t.Error("user prompt should mention secretKeyRef for API keys")
	}
}

func TestBuildGeneratePrompt_DirectiveInterService(t *testing.T) {
	ctx := &repoContext{
		name:              "multi-svc",
		branch:            "main",
		tree:              "api/main.go\nworker/main.go\n",
		interServiceCalls: []string{"Go http.Get (potential inter-service call)"},
		dockerfiles:       make(map[string]string),
		depFiles:          make(map[string]string),
		sourceSnippets:    make(map[string]string),
	}
	_, user := buildGeneratePrompt(ctx, ci.Default())

	if !strings.Contains(user, "DIRECTIVE") {
		t.Error("user prompt should contain DIRECTIVE for inter-service calls")
	}
	if !strings.Contains(user, "Kubernetes DNS") {
		t.Error("user prompt should mention Kubernetes DNS for inter-service calls")
	}
}

func TestBuildGeneratePrompt_NoInterServiceWithoutDetection(t *testing.T) {
	ctx := &repoContext{
		name:           "simple-app",
		branch:         "main",
		tree:           "main.go\n",
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
	}
	_, user := buildGeneratePrompt(ctx, ci.Default())

	if strings.Contains(user, "Inter-service communication") {
		t.Error("user prompt should NOT contain inter-service section when nothing detected")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// scanRepo integration: inter-service calls
// ────────────────────────────────────────────────────────────────────────────

func TestScanRepo_DetectsInterServiceCalls(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import "net/http"
func main() {
	resp, _ := http.Get("http://orders:3000/api")
	_ = resp
}`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if len(ctx.interServiceCalls) == 0 {
		t.Error("should detect inter-service HTTP call")
	}
}

func TestScanRepo_DetectsProcfile(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "Procfile"), []byte(`web: gunicorn app:app
worker: celery -A app worker
`), 0644)

	ctx, err := scanRepo(dir)
	if err != nil {
		t.Fatalf("scanRepo() error = %v", err)
	}

	if _, ok := ctx.depFiles["Procfile"]; !ok {
		t.Error("scanRepo should collect Procfile as a dependency manifest")
	}
}
