package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jeffvincent/kindling/pkg/ci"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a CI workflow for a repo using AI",
	Long: `Scans a local repository and uses a GenAI model (bring your own API
key) to generate a kindling-compatible CI workflow.

The command analyzes Dockerfiles, dependency manifests, and source code
to detect services and backing dependencies, then produces a
dev-deploy workflow that uses the reusable kindling-build and
kindling-deploy composite actions.

Supports OpenAI-compatible and Anthropic APIs.
Supports GitHub Actions and GitLab CI via --ci-provider.

Examples:
  kindling generate --api-key sk-... --repo-path /path/to/my-app
  kindling generate -k sk-... -r . --ai-provider openai --model o3
  kindling generate -k sk-... -r . --ci-provider gitlab
  kindling generate -k sk-ant-... -r . --ai-provider anthropic
  kindling generate -k sk-... -r . --dry-run`,
	RunE: runGenerate,
}

var (
	genAPIKey     string
	genRepoPath   string
	genProvider   string
	genModel      string
	genOutput     string
	genBranch     string
	genDryRun     bool
	genCIProvider string
)

func init() {
	generateCmd.Flags().StringVarP(&genAPIKey, "api-key", "k", "", "GenAI API key (required)")
	generateCmd.Flags().StringVarP(&genRepoPath, "repo-path", "r", ".", "Path to the local repository to analyze")
	generateCmd.Flags().StringVar(&genProvider, "ai-provider", "openai", "AI provider: openai or anthropic")
	generateCmd.Flags().StringVar(&genModel, "model", "", "Model name (default: o3 for openai, claude-sonnet-4-20250514 for anthropic)")
	generateCmd.Flags().StringVarP(&genOutput, "output", "o", "", "Output path (default: <repo-path>/.github/workflows/dev-deploy.yml)")
	generateCmd.Flags().StringVarP(&genBranch, "branch", "b", "", "Branch to trigger on (default: auto-detect from git, fallback to 'main')")
	generateCmd.Flags().BoolVar(&genDryRun, "dry-run", false, "Print the generated workflow to stdout instead of writing a file")
	generateCmd.Flags().StringVar(&genCIProvider, "ci-provider", "", "CI platform to generate for (github, gitlab; default: github)")
	_ = generateCmd.MarkFlagRequired("api-key")
	rootCmd.AddCommand(generateCmd)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	// ── Resolve and validate inputs ─────────────────────────────
	repoPath, err := filepath.Abs(genRepoPath)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return fmt.Errorf("repo path does not exist or is not a directory: %s", repoPath)
	}

	if genModel == "" {
		switch genProvider {
		case "anthropic":
			genModel = "claude-sonnet-4-20250514"
		default:
			genModel = "o3"
		}
	}

	// ── Resolve CI provider ──────────────────────────────────────
	ciProv, err := resolveProvider(genCIProvider)
	if err != nil {
		return err
	}

	if genOutput == "" {
		wfGen := ciProv.Workflow()
		genOutput = filepath.Join(repoPath, wfGen.DefaultOutputPath())
	}

	// Auto-detect default branch from git if not specified
	if genBranch == "" {
		out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD").Output()
		if err == nil {
			genBranch = strings.TrimSpace(string(out))
		} else {
			genBranch = "main"
		}
	}

	// ── Scan the repository ─────────────────────────────────────
	header("Analyzing repository")
	step("📂", repoPath)

	repoCtx, err := scanRepo(repoPath)
	if err != nil {
		return fmt.Errorf("repo scan failed: %w", err)
	}
	repoCtx.branch = genBranch

	success(fmt.Sprintf("Found %d Dockerfile(s), %d dependency manifest(s), %d source file(s)",
		repoCtx.dockerfileCount, repoCtx.depFileCount, len(repoCtx.sourceSnippets)))

	if repoCtx.dockerfileCount == 0 {
		warn("No Dockerfile found — the AI will attempt to infer a build strategy")
	}

	if len(repoCtx.externalSecrets) > 0 {
		step("🔑", fmt.Sprintf("Detected %d external credential reference(s): %s",
			len(repoCtx.externalSecrets), strings.Join(repoCtx.externalSecrets, ", ")))
		step("💡", "Run 'kindling secrets set <NAME> <VALUE>' to configure these before deploying")
	}

	if repoCtx.needsPublicExpose {
		fmt.Fprintln(os.Stderr)
		step("🔐", fmt.Sprintf("Detected %s%d OAuth/OIDC indicator(s)%s in source code:",
			colorBold, len(repoCtx.oauthHints), colorReset))
		for _, hint := range repoCtx.oauthHints {
			fmt.Fprintf(os.Stderr, "       • %s\n", hint)
		}
		fmt.Fprintln(os.Stderr)
		step("💡", fmt.Sprintf("Run %skindling expose%s to create a public HTTPS tunnel for OAuth callbacks",
			colorCyan, colorReset))
	}

	// ── Call the AI ──────────────────────────────────────────────
	header("Generating workflow with AI")
	step("🤖", fmt.Sprintf("Provider: %s, Model: %s", genProvider, genModel))

	systemPrompt, userPrompt := buildGeneratePrompt(repoCtx, ciProv)

	step("⏳", "Calling API (this may take a moment)...")
	workflow, err := callGenAI(genProvider, genAPIKey, genModel, systemPrompt, userPrompt)
	if err != nil {
		return fmt.Errorf("AI generation failed: %w", err)
	}

	// Strip markdown fences if the model wrapped the output
	workflow = cleanYAMLResponse(workflow)

	if genDryRun {
		header("Generated workflow (dry-run)")
		fmt.Fprintln(os.Stderr)
		fmt.Println(workflow)
		return nil
	}

	// ── Write the workflow file ─────────────────────────────────
	header("Writing workflow")

	outDir := filepath.Dir(genOutput)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("cannot create output directory: %w", err)
	}

	if err := os.WriteFile(genOutput, []byte(workflow+"\n"), 0644); err != nil {
		return fmt.Errorf("cannot write workflow file: %w", err)
	}

	relPath, _ := filepath.Rel(repoPath, genOutput)
	if relPath == "" {
		relPath = genOutput
	}
	success(fmt.Sprintf("Workflow written to %s", relPath))

	// ── Write canonical agent context ───────────────────────────
	contextDoc := buildContextDocument(repoPath)
	kindlingDir := filepath.Join(repoPath, ".kindling")
	if err := os.MkdirAll(kindlingDir, 0755); err == nil {
		contextPath := filepath.Join(kindlingDir, "context.md")
		if err := os.WriteFile(contextPath, []byte(contextDoc), 0644); err == nil {
			step("📄", fmt.Sprintf("Wrote %s.kindling/context.md%s (agent context)", colorCyan, colorReset))
		}
	}

	fmt.Println()
	fmt.Printf("  %sNext steps:%s\n", colorBold, colorReset)
	fmt.Printf("    1. Review the generated workflow at %s%s%s\n", colorCyan, relPath, colorReset)
	fmt.Printf("    2. Run %skindling intel on%s to give your coding agent full kindling context\n", colorCyan, colorReset)
	fmt.Printf("    3. Commit and push to trigger a deploy\n")
	fmt.Printf("    4. Access your app at %shttp://<username>-<app>.localhost%s\n", colorCyan, colorReset)
	fmt.Println()

	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Repo Scanner
// ────────────────────────────────────────────────────────────────────────────

// repoContext holds all the information gathered from scanning a repository
// that will be sent to the AI as context.
type repoContext struct {
	name              string
	branch            string
	tree              string
	dockerfiles       map[string]string // relative path → content
	depFiles          map[string]string // relative path → content
	composeFile       string            // docker-compose.yml content (if found)
	sourceSnippets    map[string]string // relative path → truncated content
	dockerfileCount   int
	depFileCount      int
	externalSecrets   []string // detected external credential env var names
	needsPublicExpose bool     // true if OAuth/OIDC patterns detected
	oauthHints        []string // descriptions of detected OAuth indicators
	hostArch          string   // host CPU architecture (arm64, amd64)
}

// Directories to skip during scanning (built from the shared skip list).
var scanSkipDirs = skipDirSet()

// Dependency manifests worth reading.
var scanDepFiles = map[string]bool{
	// Environment configuration templates (required env vars)
	".env.sample":      true,
	".env.example":     true,
	".env.development": true,
	".env.template":    true,
	// Go
	"go.mod": true,
	// JavaScript / TypeScript
	"package.json": true,
	// Python
	"requirements.txt": true,
	"Pipfile":          true,
	"pyproject.toml":   true,
	"setup.py":         true,
	"setup.cfg":        true,
	// Rust
	"Cargo.toml": true,
	// Java / Kotlin
	"pom.xml":          true,
	"build.gradle":     true,
	"build.gradle.kts": true,
	// Ruby
	"Gemfile": true,
	// PHP
	"composer.json": true,
	// Elixir
	"mix.exs": true,
	// .NET / C# / F#
	"Directory.Build.props": true,
	"global.json":           true,

	// Zig
	"build.zig.zon": true,
	// C / C++
	"CMakeLists.txt": true,
	"conanfile.txt":  true,
	"conanfile.py":   true,
	"vcpkg.json":     true,
	"meson.build":    true,
	// Perl
	"cpanfile":    true,
	"Makefile.PL": true,
	// R
	"DESCRIPTION": true,
	"renv.lock":   true,
	// Julia
	"Project.toml": true,
	// Lua
	"rockspec": true,
	// Deno
	"deno.json":  true,
	"deno.jsonc": true,
	// Bun
	"bun.lockb": true,
}

// scanDepExts matches dependency manifests by extension (e.g. .csproj, .fsproj).
var scanDepExts = map[string]bool{
	".csproj": true, // C#
	".fsproj": true, // F#
	".vbproj": true, // VB.NET
	".sln":    true, // .NET solution
}

// Source extensions to sample.
var scanSourceExts = map[string]bool{
	".go":     true, // Go
	".py":     true, // Python
	".js":     true, // JavaScript
	".ts":     true, // TypeScript
	".jsx":    true, // React JSX
	".tsx":    true, // React TSX
	".java":   true, // Java
	".kt":     true, // Kotlin
	".kts":    true, // Kotlin Script
	".rs":     true, // Rust
	".rb":     true, // Ruby
	".php":    true, // PHP
	".cs":     true, // C#
	".fs":     true, // F#
	".ex":     true, // Elixir
	".exs":    true, // Elixir Script
	".zig":    true, // Zig
	".c":      true, // C
	".cpp":    true, // C++
	".cc":     true, // C++
	".h":      true, // C/C++ header
	".pl":     true, // Perl
	".r":      true, // R
	".R":      true, // R
	".jl":     true, // Julia
	".lua":    true, // Lua
	".cr":     true, // Crystal
	".nim":    true, // Nim
	".vue":    true, // Vue.js SFC
	".svelte": true, // Svelte
}

// envVarAccessPatterns are code-level patterns that indicate a file reads
// configuration from environment variables. Files containing these patterns
// are boosted in source-file prioritization so the LLM sees how the app is
// configured (ports, env var names, health paths, etc.).
var envVarAccessPatterns = []string{
	// Go
	"os.Getenv", "os.LookupEnv",
	// Node.js / Deno / Bun
	"process.env.", "process.env[", "Deno.env.get", "Bun.env.",
	// Python
	"os.environ", "os.getenv",
	// Java / Kotlin
	"System.getenv",
	// C# / .NET
	"Environment.GetEnvironmentVariable",
	// Rust
	"env::var", "std::env",
	// Ruby
	"ENV[", "ENV.fetch",
	// PHP
	"getenv(", "$_ENV[", "$_SERVER[",
	// Elixir
	"System.get_env", "Application.get_env",
	// C / C++
	"std::getenv",
}

// hasEnvVarPatterns does a quick scan of a source file and returns true if it
// contains any env-var-access pattern. This is used to boost config-bearing
// files into the sample window sent to the LLM.
func hasEnvVarPatterns(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	for _, p := range envVarAccessPatterns {
		if strings.Contains(content, p) {
			return true
		}
	}
	return false
}

func scanRepo(repoPath string) (*repoContext, error) {
	ctx := &repoContext{
		name:           filepath.Base(repoPath),
		dockerfiles:    make(map[string]string),
		depFiles:       make(map[string]string),
		sourceSnippets: make(map[string]string),
		hostArch:       runtime.GOARCH, // detect host CPU arch for Kaniko patches
	}

	var treeLines []string
	var sourceFiles []string

	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}

		rel, _ := filepath.Rel(repoPath, path)
		if rel == "." {
			return nil
		}

		// Skip ignored directories
		if d.IsDir() {
			if scanSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			depth := strings.Count(rel, string(filepath.Separator))
			if depth >= 4 {
				return filepath.SkipDir
			}
			return nil
		}

		depth := strings.Count(rel, string(filepath.Separator))
		if depth <= 3 {
			treeLines = append(treeLines, rel)
		}

		name := d.Name()
		nameLower := strings.ToLower(name)

		// Collect Dockerfiles
		if nameLower == "dockerfile" || strings.HasPrefix(nameLower, "dockerfile.") {
			content, err := readFileCapped(path, 80)
			if err == nil {
				ctx.dockerfiles[rel] = content
				ctx.dockerfileCount++
			}
		}

		// Collect dependency manifests (by name or by extension)
		ext := strings.ToLower(filepath.Ext(name))
		if scanDepFiles[name] || scanDepExts[ext] {
			content, err := readFileCapped(path, 120)
			if err == nil {
				ctx.depFiles[rel] = content
				ctx.depFileCount++
			}
		}

		// Collect docker-compose
		if nameLower == "docker-compose.yml" || nameLower == "docker-compose.yaml" ||
			nameLower == "compose.yml" || nameLower == "compose.yaml" {
			content, err := readFileCapped(path, 150)
			if err == nil {
				ctx.composeFile = content
			}
		}

		// Collect source files for analysis (top 2 levels only)
		if scanSourceExts[ext] && depth <= 2 {
			sourceFiles = append(sourceFiles, path)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Build tree string
	sort.Strings(treeLines)
	var sb strings.Builder
	for _, line := range treeLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	ctx.tree = sb.String()

	// Pre-scan source files for env var access patterns so config-bearing
	// files get boosted into the sample window.
	envVarFiles := make(map[string]bool)
	for _, path := range sourceFiles {
		if hasEnvVarPatterns(path) {
			envVarFiles[path] = true
		}
	}

	// Sample entry-point source files (up to 6)
	mainFiles := prioritizeSourceFiles(sourceFiles, envVarFiles)
	for i, path := range mainFiles {
		if i >= 6 {
			break
		}
		content, err := readFileCapped(path, 80)
		if err == nil {
			rel, _ := filepath.Rel(repoPath, path)
			ctx.sourceSnippets[rel] = content
		}
	}

	// Detect external credential references
	ctx.externalSecrets = detectExternalSecrets(repoPath, ctx)

	// Detect OAuth/OIDC patterns that need public exposure
	ctx.oauthHints, ctx.needsPublicExpose = detectOAuthRequirements(ctx)

	return ctx, nil
}

// readFileCapped reads up to maxLines lines from a file and truncates with a
// note if the file is longer.
func readFileCapped(path string, maxLines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		total := len(lines)
		lines = lines[:maxLines]
		lines = append(lines, fmt.Sprintf("... (%d more lines truncated)", total-maxLines))
	}
	return strings.Join(lines, "\n"), nil
}

// prioritizeSourceFiles sorts files so entry-point files (main.go, app.py, etc.)
// appear first, since they usually reveal ports, routes, and dependencies.
// Files in envVarFiles are boosted to tier 1 even if their filename isn't a
// known entry point, because they contain env-var-access patterns that reveal
// how the app is configured.
func prioritizeSourceFiles(files []string, envVarFiles map[string]bool) []string {
	priority := map[string]int{
		// Tier 1: primary entry points
		"main.go": 1, "main.py": 1, "app.py": 1, "app.js": 1, "app.ts": 1,
		"main.rs": 1, "main.java": 1, "main.kt": 1,
		"main.cs": 1, "Program.cs": 1, "Startup.cs": 1,
		"main.zig": 1, "main.c": 1, "main.cpp": 1,
		"lib.rs":         1,                 // Rust lib entry
		"application.ex": 1, "router.ex": 1, // Elixir/Phoenix
		"Main.jl": 1, // Julia
		// Tier 2: common server/app files
		"index.js": 2, "index.ts": 2, "index.tsx": 2,
		"server.go": 2, "server.js": 2, "server.ts": 2,
		"app.rb": 2, "config.ru": 2, // Ruby/Rails
		"manage.py": 2, "wsgi.py": 2, "asgi.py": 2, // Django
		"artisan": 2, "index.php": 2, // PHP/Laravel
		"Application.java": 2, "Application.kt": 2, // Spring Boot
		"mix.exs": 2, "endpoint.ex": 2, // Elixir/Phoenix
		"App.vue": 2, "App.svelte": 2, // Frontend SPA
		"nuxt.config.ts": 2, "next.config.js": 2, "next.config.ts": 2,
		"vite.config.ts": 2, "vite.config.js": 2,
		// Tier 3: secondary config/setup files
		"settings.py": 3, "urls.py": 3, // Django
		"routes.rb":  3,                  // Rails
		"startup.cs": 3, "program.cs": 3, // .NET (lowercase)
		"build.zig": 3, // Zig build
	}
	sort.Slice(files, func(i, j int) bool {
		pi := priority[filepath.Base(files[i])]
		pj := priority[filepath.Base(files[j])]

		// Boost files containing env var patterns to tier 1 (entry-point
		// level) so config files make the cut even with generic names.
		if pi == 0 && envVarFiles[files[i]] {
			pi = 1
		}
		if pj == 0 && envVarFiles[files[j]] {
			pj = 1
		}

		if pi != pj {
			if pi == 0 {
				return false
			}
			if pj == 0 {
				return true
			}
			return pi < pj
		}
		return files[i] < files[j]
	})
	return files
}

// ────────────────────────────────────────────────────────────────────────────
// Prompt Builder
// ────────────────────────────────────────────────────────────────────────────

func buildGeneratePrompt(ctx *repoContext, provider ci.Provider) (system, user string) {
	wfGen := provider.Workflow()

	// The system prompt is now owned by the CI provider — it assembles
	// shared kindling domain knowledge (Kaniko, deps, deploy philosophy)
	// with its CI-platform-specific syntax instructions.
	system = wfGen.SystemPrompt(ctx.hostArch)

	// ── Build user prompt ───────────────────────────────────────
	var b strings.Builder

	pctx := wfGen.PromptContext()

	b.WriteString(fmt.Sprintf("Generate a kindling dev-deploy.yml %s %s for this repository named %q.\n\n", pctx.PlatformName, pctx.WorkflowNoun, ctx.name))
	b.WriteString(fmt.Sprintf("Default branch: %s (use this in the 'on: push: branches:' trigger)\n\n", ctx.branch))
	b.WriteString(fmt.Sprintf("Target architecture: %s (use this in all Kaniko Dockerfile patches)\n\n", ctx.hostArch))

	// Directory tree
	b.WriteString("## Repository structure\n```\n")
	b.WriteString(ctx.tree)
	b.WriteString("```\n\n")

	// Dockerfiles
	if len(ctx.dockerfiles) > 0 {
		b.WriteString("## Dockerfiles\n\n")
		for path, content := range ctx.dockerfiles {
			b.WriteString(fmt.Sprintf("### %s\n```dockerfile\n%s\n```\n\n", path, content))
		}
	}

	// Dependency manifests
	if len(ctx.depFiles) > 0 {
		b.WriteString("## Dependency manifests\n\n")
		for path, content := range ctx.depFiles {
			b.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", path, content))
		}
	}

	// Docker Compose
	if ctx.composeFile != "" {
		b.WriteString("## docker-compose.yml\n```yaml\n")
		b.WriteString(ctx.composeFile)
		b.WriteString("\n```\n\n")
	}

	// Source snippets
	if len(ctx.sourceSnippets) > 0 {
		b.WriteString("## Key source files (entry points)\n\n")
		keys := make([]string, 0, len(ctx.sourceSnippets))
		for k := range ctx.sourceSnippets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, path := range keys {
			ext := strings.TrimPrefix(filepath.Ext(path), ".")
			b.WriteString(fmt.Sprintf("### %s\n```%s\n%s\n```\n\n", path, ext, ctx.sourceSnippets[path]))
		}
	}

	// Detected external credentials
	if len(ctx.externalSecrets) > 0 {
		b.WriteString("## Detected credential-like environment variables\n\n")
		b.WriteString("The following environment variables were detected in source code.\n")
		b.WriteString("Apply the dev staging philosophy from the system prompt to decide how to handle each:\n")
		b.WriteString("- If it is an app-level secret (SECRET_KEY, JWT_SECRET, etc.), set a random hex dev value\n")
		b.WriteString("- If it is an optional integration (AWS, Datadog, SMTP, OAuth), OMIT it entirely\n")
		b.WriteString("- If it is truly required AND external, use secretKeyRef with name kindling-secret-<name>\n\n")
		for _, name := range ctx.externalSecrets {
			b.WriteString(fmt.Sprintf("- %s\n", name))
		}
		b.WriteString("\n")
	}

	// OAuth / OIDC indicators
	if ctx.needsPublicExpose && len(ctx.oauthHints) > 0 {
		b.WriteString("## Detected OAuth / OIDC indicators\n\n")
		b.WriteString("This repository appears to use external authentication. Detected:\n\n")
		for _, hint := range ctx.oauthHints {
			b.WriteString(fmt.Sprintf("- %s\n", hint))
		}
		b.WriteString("\nThe user may not have a public URL yet. Add a YAML comment noting that\n")
		b.WriteString("`kindling expose` should be run if OAuth callbacks need to reach the cluster.\n\n")
	}

	singleExample, multiExample := wfGen.ExampleWorkflows()

	// Reference examples
	b.WriteString("## Reference: single-service workflow example\n```yaml\n")
	b.WriteString(singleExample)
	b.WriteString("\n```\n\n")

	b.WriteString("## Reference: multi-service workflow example\n```yaml\n")
	b.WriteString(multiExample)
	b.WriteString("\n```\n\n")

	b.WriteString("Now generate the dev-deploy.yml workflow YAML for this repository. Return ONLY the YAML.\n")

	user = b.String()
	return system, user
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// cleanYAMLResponse strips markdown code fences from the AI response in case
// the model wraps the output despite instructions.
func cleanYAMLResponse(s string) string {
	s = strings.TrimSpace(s)

	// Remove ```yaml ... ``` or ``` ... ``` wrapping
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		// Remove first line (```yaml or ```)
		if len(lines) > 1 {
			lines = lines[1:]
		}
		// Remove last line if it's ```
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		s = strings.Join(lines, "\n")
	}

	return strings.TrimSpace(s)
}

// ── External credential detection ───────────────────────────────

// credentialPatterns are suffixes that indicate an env var is an external credential.
var credentialSuffixes = []string{
	"_API_KEY", "_APIKEY", "_SECRET", "_SECRET_KEY",
	"_TOKEN", "_ACCESS_TOKEN", "_AUTH_TOKEN", "_REFRESH_TOKEN",
	"_DSN", "_CONNECTION_STRING", "_CONN_STR",
	"_PASSWORD", "_PASSWD",
	"_CLIENT_ID", "_CLIENT_SECRET",
	"_PRIVATE_KEY", "_SIGNING_KEY",
	"_WEBHOOK_SECRET",
}

// dependencyManagedNames are env vars managed by the operator's dependency system.
// These should NEVER be flagged as external credentials.
var dependencyManagedNames = map[string]bool{
	// Connection URLs (auto-injected when dependency is declared)
	"DATABASE_URL":      true,
	"REDIS_URL":         true,
	"MONGO_URL":         true,
	"MONGODB_URI":       true,
	"MONGODB_URL":       true,
	"AMQP_URL":          true,
	"RABBITMQ_URL":      true,
	"KAFKA_BROKER_URL":  true,
	"KAFKA_BROKERS":     true,
	"ELASTICSEARCH_URL": true,
	"S3_ENDPOINT":       true,
	"NATS_URL":          true,
	"MEMCACHED_URL":     true,
	"CASSANDRA_URL":     true,
	"CONSUL_HTTP_ADDR":  true,
	"VAULT_ADDR":        true,
	"INFLUXDB_URL":      true,
	"JAEGER_ENDPOINT":   true,
	// Dependency credentials (managed by operator defaults)
	"POSTGRES_PASSWORD":          true,
	"POSTGRES_USER":              true,
	"POSTGRES_DB":                true,
	"DATABASE_PASSWORD":          true,
	"MYSQL_PASSWORD":             true,
	"MYSQL_ROOT_PASSWORD":        true,
	"MYSQL_USER":                 true,
	"MYSQL_DATABASE":             true,
	"REDIS_PASSWORD":             true,
	"MONGO_INITDB_ROOT_USERNAME": true,
	"MONGO_INITDB_ROOT_PASSWORD": true,
}

// credentialExactNames are full env var names that indicate external credentials.
var credentialExactNames = map[string]bool{
	"STRIPE_KEY":            true,
	"SENDGRID_API_KEY":      true,
	"TWILIO_AUTH_TOKEN":     true,
	"AWS_SECRET_ACCESS_KEY": true,
	"AWS_ACCESS_KEY_ID":     true,
	"GITHUB_TOKEN":          true,
	"SENTRY_DSN":            true,
	"AUTH0_DOMAIN":          true,
	"AUTH0_CLIENT_ID":       true,
	"AUTH0_CLIENT_SECRET":   true,
	"OKTA_DOMAIN":           true,
	"FIREBASE_API_KEY":      true,
	"OPENAI_API_KEY":        true,
	"ANTHROPIC_API_KEY":     true,
}

// detectExternalSecrets scans source files, Dockerfiles, compose files, and .env
// files for references to external credentials.
func detectExternalSecrets(repoPath string, ctx *repoContext) []string {
	seen := make(map[string]bool)

	// Scan all collected content for env var patterns
	allContent := make(map[string]string)
	for k, v := range ctx.dockerfiles {
		allContent[k] = v
	}
	for k, v := range ctx.depFiles {
		allContent[k] = v
	}
	for k, v := range ctx.sourceSnippets {
		allContent[k] = v
	}
	if ctx.composeFile != "" {
		allContent["docker-compose.yml"] = ctx.composeFile
	}

	// Also scan .env files
	envFiles := []string{".env", ".env.example", ".env.sample", ".env.development", ".env.local"}
	for _, envFile := range envFiles {
		path := filepath.Join(repoPath, envFile)
		if content, err := readFileCapped(path, 100); err == nil {
			allContent[envFile] = content
		}
	}

	for _, content := range allContent {
		for _, line := range strings.Split(content, "\n") {
			// Look for ENV VAR_NAME, os.Getenv("VAR"), process.env.VAR, etc.
			words := extractEnvVarNames(line)
			for _, w := range words {
				if isExternalCredential(w) && !seen[w] {
					seen[w] = true
				}
			}
		}
	}

	// Sort for deterministic output
	var result []string
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// extractEnvVarNames pulls env-var-like names from a line of code.
func extractEnvVarNames(line string) []string {
	var names []string

	// Match patterns like:
	//   os.Getenv("FOO_BAR")
	//   process.env.FOO_BAR
	//   ENV FOO_BAR
	//   FOO_BAR=
	//   ${FOO_BAR}
	//   getenv("FOO_BAR")
	//   env("FOO_BAR")
	//   Environment.GetEnvironmentVariable("FOO_BAR")
	//   System.getenv("FOO_BAR")
	for i := 0; i < len(line); i++ {
		// Find sequences of UPPER_CASE characters
		if isUpperOrUnderscore(line[i]) {
			start := i
			for i < len(line) && (isUpperOrUnderscore(line[i]) || isDigit(line[i])) {
				i++
			}
			candidate := line[start:i]
			// Must contain at least one underscore and be 4+ chars
			if len(candidate) >= 4 && strings.Contains(candidate, "_") {
				names = append(names, candidate)
			}
		}
	}
	return names
}

func isUpperOrUnderscore(b byte) bool {
	return (b >= 'A' && b <= 'Z') || b == '_'
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// isExternalCredential checks if an env var name matches credential patterns.
func isExternalCredential(name string) bool {
	// Never flag dependency-managed env vars
	if dependencyManagedNames[name] {
		return false
	}
	if credentialExactNames[name] {
		return true
	}
	upper := strings.ToUpper(name)
	for _, suffix := range credentialSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

// ── OAuth / OIDC detection ──────────────────────────────────────

// oauthPatterns maps search strings to human-readable descriptions.
var oauthPatterns = []struct {
	pattern string
	desc    string
}{
	// Provider SDKs / packages
	{"auth0", "Auth0 SDK or configuration"},
	{"okta", "Okta SDK or configuration"},
	{"firebase/auth", "Firebase Authentication"},
	{"firebase-admin", "Firebase Admin SDK"},
	{"@auth0", "Auth0 npm package"},
	{"passport-oauth", "Passport.js OAuth strategy"},
	{"passport-google", "Passport.js Google strategy"},
	{"passport-github", "Passport.js GitHub strategy"},
	{"next-auth", "NextAuth.js"},
	{"@nextauth", "NextAuth.js"},
	{"clerk", "Clerk authentication"},
	{"supabase/auth", "Supabase Auth"},
	{"keycloak", "Keycloak integration"},

	// OIDC / OAuth protocols
	{"openid-connect", "OpenID Connect"},
	{"oidc", "OIDC discovery/configuration"},
	{"oauth2", "OAuth 2.0 flow"},
	{"authorization_code", "OAuth authorization code flow"},
	{"/callback", "OAuth callback endpoint"},
	{"/auth/callback", "Auth callback route"},
	{"redirect_uri", "OAuth redirect URI"},
	{"REDIRECT_URI", "OAuth redirect URI env var"},
	{"CALLBACK_URL", "OAuth callback URL env var"},

	// Environment variables
	{"AUTH0_DOMAIN", "Auth0 domain config"},
	{"AUTH0_CLIENT_ID", "Auth0 client ID"},
	{"OKTA_DOMAIN", "Okta domain config"},
	{"OKTA_CLIENT_ID", "Okta client ID"},
	{"GOOGLE_CLIENT_ID", "Google OAuth client ID"},
	{"GITHUB_CLIENT_ID", "GitHub OAuth client ID"},
	{"NEXTAUTH_URL", "NextAuth.js public URL"},
	{"NEXTAUTH_SECRET", "NextAuth.js secret"},

	// Well-known endpoints
	{".well-known/openid-configuration", "OIDC discovery endpoint"},
	{"/authorize", "OAuth authorize endpoint"},
	{"/oauth/token", "OAuth token endpoint"},
}

// detectOAuthRequirements scans all collected content for OAuth/OIDC patterns.
func detectOAuthRequirements(ctx *repoContext) (hints []string, needsExpose bool) {
	// Combine all scanned content
	allContent := make(map[string]string)
	for k, v := range ctx.dockerfiles {
		allContent[k] = v
	}
	for k, v := range ctx.depFiles {
		allContent[k] = v
	}
	for k, v := range ctx.sourceSnippets {
		allContent[k] = v
	}
	if ctx.composeFile != "" {
		allContent["docker-compose.yml"] = ctx.composeFile
	}

	seen := make(map[string]bool)
	for _, content := range allContent {
		lower := strings.ToLower(content)
		for _, p := range oauthPatterns {
			if seen[p.desc] {
				continue
			}
			if strings.Contains(lower, strings.ToLower(p.pattern)) {
				seen[p.desc] = true
				hints = append(hints, p.desc)
			}
		}
	}

	sort.Strings(hints)
	needsExpose = len(hints) > 0
	return hints, needsExpose
}
