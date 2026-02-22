package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a GitHub Actions workflow for a repo using AI",
	Long: `Scans a local repository and uses a GenAI model (bring your own API
key) to generate a kindling-compatible GitHub Actions workflow.

The command analyzes Dockerfiles, dependency manifests, and source code
to detect services and backing dependencies, then produces a
dev-deploy.yml that uses the reusable kindling-build and kindling-deploy
composite actions.

Supports OpenAI-compatible and Anthropic APIs.

Examples:
  kindling generate --api-key sk-... --repo-path /path/to/my-app
  kindling generate -k sk-... -r . --provider openai --model o3
  kindling generate -k sk-... -r . --model gpt-4o
  kindling generate -k sk-ant-... -r . --provider anthropic
  kindling generate -k sk-... -r . --dry-run`,
	RunE: runGenerate,
}

var (
	genAPIKey   string
	genRepoPath string
	genProvider string
	genModel    string
	genOutput   string
	genBranch   string
	genDryRun   bool
)

func init() {
	generateCmd.Flags().StringVarP(&genAPIKey, "api-key", "k", "", "GenAI API key (required)")
	generateCmd.Flags().StringVarP(&genRepoPath, "repo-path", "r", ".", "Path to the local repository to analyze")
	generateCmd.Flags().StringVar(&genProvider, "provider", "openai", "AI provider: openai or anthropic")
	generateCmd.Flags().StringVar(&genModel, "model", "", "Model name (default: o3 for openai, claude-sonnet-4-20250514 for anthropic)")
	generateCmd.Flags().StringVarP(&genOutput, "output", "o", "", "Output path (default: <repo-path>/.github/workflows/dev-deploy.yml)")
	generateCmd.Flags().StringVarP(&genBranch, "branch", "b", "", "Branch to trigger on (default: auto-detect from git, fallback to 'main')")
	generateCmd.Flags().BoolVar(&genDryRun, "dry-run", false, "Print the generated workflow to stdout instead of writing a file")
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

	if genOutput == "" {
		genOutput = filepath.Join(repoPath, ".github", "workflows", "dev-deploy.yml")
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

	systemPrompt, userPrompt := buildGeneratePrompt(repoCtx)

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

	fmt.Println()
	fmt.Printf("  %sNext steps:%s\n", colorBold, colorReset)
	fmt.Printf("    1. Review the generated workflow at %s%s%s\n", colorCyan, relPath, colorReset)
	fmt.Printf("    2. Commit and push to trigger a deploy\n")
	fmt.Printf("    3. Access your app at %shttp://<username>-<app>.localhost%s\n", colorCyan, colorReset)
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
}

// Directories to skip during scanning.
var scanSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"env":          true,
	".tox":         true,
	".mypy_cache":  true,
	".ruff_cache":  true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".nuxt":        true,
	".svelte-kit":  true,
	"target":       true, // Rust, Java/Maven
	".terraform":   true,
	".idea":        true,
	".vscode":      true,
	".github":      true, // don't confuse the AI with existing workflows
	"bin":          true,
	"obj":          true, // .NET build output
	"_output":      true,
	".cache":       true,
	"_build":       true, // Elixir/Mix
	"deps":         true, // Elixir/Mix
	"zig-cache":    true,
	"zig-out":      true,
	".gradle":      true,
	".m2":          true,
	".elixir_ls":   true,
	"coverage":     true,
	".nyc_output":  true,
	"htmlcov":      true,
}

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

func buildGeneratePrompt(ctx *repoContext) (system, user string) {
	system = `You are an expert at generating GitHub Actions workflow files for kindling, a Kubernetes operator that provides local dev/staging environments on Kind clusters.

You generate dev-deploy.yml workflow files that use two reusable composite actions:

1. kindling-build — builds a container image via Kaniko sidecar
   Uses: kindling-sh/kindling/.github/actions/kindling-build@main
   Inputs: name (required), context (required), image (required), exclude (optional), timeout (optional)
   IMPORTANT: kindling-build runs the Dockerfile found at <context>/Dockerfile as-is
   using Kaniko inside the cluster. It does NOT modify or generate Dockerfiles.
   Every service in the workflow MUST have a working Dockerfile already in the repo.
   If the Dockerfile doesn't build locally (e.g. docker build), it won't build
   in kindling either. The "context" input must point to the directory containing
   the service's Dockerfile.

2. kindling-deploy — deploys a DevStagingEnvironment CR via sidecar
   Uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
   Inputs: name (required), image (required), port (required),
           labels, env, dependencies, ingress-host, ingress-class,
           health-check-path, replicas, service-type, wait

Key conventions you MUST follow:
- Registry: registry:5000 (in-cluster)
- Image tag: ${{ github.actor }}-${{ github.sha }}
- Runner: runs-on: [self-hosted, "${{ github.actor }}"]
- Ingress host pattern: ${{ github.actor }}-<service>.localhost
- DSE name pattern: ${{ github.actor }}-<service>
- Always trigger on push to the default branch (specified below) + workflow_dispatch
- Always include a "Checkout code" step with actions/checkout@v4
- Always include a "Clean builds directory" step immediately after checkout
- For multi-service repos, build all images first, then deploy in dependency order
- Include health-check-path when you can detect the endpoint from source code
- For Java/Spring Boot services, use health-check-path: "/actuator/health"
- If a service (like an API gateway) depends on other services via env vars,
  deploy it LAST so its upstreams are already running
- Add comment separators between build and deploy sections for readability:
  "# -- Build all images --" before the first build step
  "# -- Deploy in dependency order --" before the first deploy step

kindling-deploy field ordering (follow this order exactly):
  name, image, port, ingress-host, health-check-path, labels, env, dependencies,
  replicas, service-type, ingress-class, wait

Supported dependency types for the "dependencies" input (YAML list under the input):
  postgres, redis, mysql, mongodb, rabbitmq, minio, elasticsearch,
  kafka, nats, memcached, cassandra, consul, vault, influxdb, jaeger

Detect which dependencies to include by analyzing imports, packages, and env var
references across ALL common languages:

- Go:       "github.com/lib/pq"/"database/sql" → postgres, "github.com/go-redis" → redis,
            "go.mongodb.org/mongo-driver" → mongodb, "github.com/streadway/amqp" → rabbitmq,
            "github.com/segmentio/kafka-go" → kafka, "github.com/nats-io/nats.go" → nats,
            "github.com/minio/minio-go" → minio, "github.com/elastic/go-elasticsearch" → elasticsearch,
            "github.com/hashicorp/vault" → vault, "github.com/hashicorp/consul" → consul
- Node/TS:  "pg"/"pg-promise" → postgres, "ioredis"/"redis" → redis, "mysql2" → mysql,
            "mongoose"/"mongodb" → mongodb, "amqplib" → rabbitmq, "kafkajs" → kafka,
            "nats" → nats, "memcached"/"memjs" → memcached, "@elastic/elasticsearch" → elasticsearch,
            "minio" → minio, "cassandra-driver" → cassandra
- Python:   "psycopg2"/"asyncpg"/"sqlalchemy" → postgres, "redis"/"aioredis" → redis,
            "pymysql"/"mysqlclient" → mysql, "pymongo"/"motor" → mongodb,
            "pika"/"aio-pika" → rabbitmq, "kafka-python"/"confluent-kafka" → kafka,
            "nats-py" → nats, "pymemcache" → memcached, "elasticsearch" → elasticsearch,
            "boto3"/"minio" → minio, "cassandra-driver" → cassandra, "hvac" → vault
- Java/Kotlin: "org.postgresql" → postgres, "jedis"/"lettuce" → redis, "mysql-connector" → mysql,
            "mongo-java-driver" → mongodb, "spring-boot-starter-amqp" → rabbitmq,
            "spring-kafka" → kafka, "spring-data-elasticsearch" → elasticsearch,
            "spring-cloud-vault" → vault, "spring-cloud-consul" → consul
- Rust:     "tokio-postgres"/"diesel" → postgres, "redis" → redis, "sqlx" + mysql feature → mysql,
            "mongodb" → mongodb, "lapin" → rabbitmq, "rdkafka" → kafka
- Ruby:     "pg" gem → postgres, "redis" gem → redis, "mysql2" gem → mysql,
            "mongo"/"mongoid" → mongodb, "bunny" → rabbitmq, "sidekiq" → redis
- PHP:      "predis"/"phpredis" → redis, "doctrine/dbal" → postgres or mysql,
            "php-amqplib" → rabbitmq, "mongodb/mongodb" → mongodb
- C#/.NET:  "Npgsql" → postgres, "StackExchange.Redis" → redis,
            "MySqlConnector" → mysql, "MongoDB.Driver" → mongodb,
            "RabbitMQ.Client" → rabbitmq, "Confluent.Kafka" → kafka,
            "NATS.Client" → nats, "Elasticsearch.Net" → elasticsearch
- Elixir:   "postgrex"/"ecto" → postgres, "redix" → redis, "amqp" → rabbitmq,
            "kafka_ex" → kafka, "mongodb_driver" → mongodb
- docker-compose.yml service names (postgres, redis, mysql, mongo, rabbitmq, etc.)
- Environment variable references in code (DATABASE_URL, REDIS_URL, MONGO_URL, etc.)

CRITICAL — Dependency connection URLs are auto-injected:
When you declare a dependency in the "dependencies" input, the kindling operator
AUTOMATICALLY injects the corresponding connection URL environment variable into the
application container. You MUST NOT include these env vars in the "env" input — they
will be duplicated, and any secretKeyRef will fail because no such secret exists.

Auto-injected env vars by dependency type:
  postgres       → DATABASE_URL  (e.g. postgres://devuser:devpass@<name>-postgres:5432/devdb?sslmode=disable)
  redis          → REDIS_URL     (e.g. redis://<name>-redis:6379/0)
  mysql          → DATABASE_URL  (e.g. mysql://devuser:devpass@<name>-mysql:3306/devdb)
  mongodb        → MONGO_URL     (e.g. mongodb://devuser:devpass@<name>-mongodb:27017)
  rabbitmq       → AMQP_URL      (e.g. amqp://devuser:devpass@<name>-rabbitmq:5672)
  minio          → S3_ENDPOINT   (e.g. http://<name>-minio:9000)
  elasticsearch  → ELASTICSEARCH_URL (e.g. http://<name>-elasticsearch:9200)
  kafka          → KAFKA_BROKER_URL  (e.g. <name>-kafka:9092)
  nats           → NATS_URL      (e.g. nats://<name>-nats:4222)
  memcached      → MEMCACHED_URL (e.g. <name>-memcached:11211)
  cassandra      → CASSANDRA_URL (e.g. <name>-cassandra:9042)
  consul         → CONSUL_HTTP_ADDR (e.g. http://<name>-consul:8500)
  vault          → VAULT_ADDR    (e.g. http://<name>-vault:8200)
  influxdb       → INFLUXDB_URL  (e.g. http://<name>-influxdb:8086)
  jaeger         → JAEGER_ENDPOINT (e.g. http://<name>-jaeger:16686)

So if you write "dependencies: postgres, redis", do NOT also write:
  env: |
    - name: DATABASE_URL
      valueFrom:
        secretKeyRef: ...     ← WRONG, will fail
    - name: REDIS_URL
      value: "redis://..."    ← WRONG, duplicates auto-injected value

The ONLY env vars that belong in the "env" input are:
  1. Truly external credentials (API keys, tokens, third-party DSNs) as secretKeyRef
  2. App configuration that is NOT a dependency connection URL (e.g. NODE_ENV, LOG_LEVEL)
  3. Env vars that reference an auto-injected URL via variable expansion, e.g.:
       - name: ADDITIONAL_DB
         value: "$(DATABASE_URL)&options=extra"
  4. When the app uses a DIFFERENT env var name than the auto-injected one, map it
     using variable expansion. For example, if the app expects CELERY_BROKER_URL but
     the dependency is rabbitmq (which auto-injects AMQP_URL):
       - name: CELERY_BROKER_URL
         value: "$(AMQP_URL)"
     Similarly for CELERY_RESULT_BACKEND when redis auto-injects REDIS_URL:
       - name: CELERY_RESULT_BACKEND
         value: "$(REDIS_URL)"
     Check the app's source code, docker-compose environment, and .env files to
     discover what env var names the app actually uses for each dependency connection.
     If the app's name differs from the auto-injected name, add the mapping.

  IMPORTANT: If an env var uses $(VARIABLE) expansion referencing an auto-injected
  URL, the corresponding dependency MUST be declared in that service's dependencies
  block. The variable will not exist unless the dependency is declared. For example,
  if a service has env "CELERY_BROKER_URL: $(AMQP_URL)", that service MUST declare
  "- type: rabbitmq" in its dependencies. This applies to EVERY service that uses
  the variable, not just one of them.

Build timeout guidance:
The default kindling-build timeout is 300 seconds (5 minutes). This is sufficient for
interpreted languages and lightweight compiled languages (Go, TypeScript, Python, Ruby,
PHP). However, languages with heavy compilation toolchains need a longer timeout.
Set timeout: "900" (15 minutes) on the kindling-build step for services written in:
  - Rust (cargo builds from scratch without a cache layer)
  - Java/Kotlin (Maven/Gradle dependency download + compilation)
  - C#/.NET (dotnet restore + publish)
  - Elixir (Mix deps.get + compilation of dependencies)
Only add the timeout input when it differs from the default 300.

CRITICAL — Kaniko vs Docker BuildKit compatibility:
kindling-build uses Kaniko, NOT Docker BuildKit. Kaniko does NOT support:
  - Automatic BuildKit platform ARGs: BUILDPLATFORM, TARGETPLATFORM, TARGETARCH, TARGETOS, TARGETVARIANT
  - FROM --platform=${BUILDPLATFORM} syntax
If a Dockerfile uses any of these (common in .NET, cross-compiled Go, and multi-arch images),
the build WILL fail because the ARG values will be empty.

IMPORTANT: Only patch what is specifically broken. Do NOT touch ARG lines that are NOT
BuildKit platform ARGs. Application-level ARGs like ARG APP_PATH, ARG BASE_IMAGE,
ARG ENVIRONMENT, ARG CDN_URL, etc. are perfectly valid in Kaniko and MUST be left alone.
Kaniko fully supports ARG, ENV, FROM ${VAR}, and multi-stage builds.
If a Dockerfile has NO known Kaniko issues (no BuildKit platform ARGs, no poetry,
no npm, no go build without -buildvcs=false), do NOT generate a patch step at all.

When you detect a Dockerfile that uses BuildKit platform ARGs, you MUST generate a
"Patch Dockerfile for Kaniko" step BEFORE the kindling-build step for that service.
The patch step should:
  1. Remove "--platform=${BUILDPLATFORM}" from any FROM line
  2. Remove the ARG TARGETPLATFORM, ARG TARGETARCH, ARG BUILDPLATFORM, ARG TARGETOS declarations
  3. Replace any usage of $TARGETARCH or ${TARGETARCH} with the concrete architecture (amd64)
  4. Replace any usage of $TARGETPLATFORM or ${TARGETPLATFORM} with linux/amd64
  5. Replace any usage of $BUILDPLATFORM or ${BUILDPLATFORM} with linux/amd64

Example patch step for a .NET worker with BuildKit ARGs:
  - name: Patch worker Dockerfile for Kaniko
    shell: bash
    run: |
      cd ${{ github.workspace }}/worker
      # Remove --platform=${BUILDPLATFORM} from FROM lines
      sed -i 's/FROM --platform=\${BUILDPLATFORM} /FROM /g' Dockerfile
      # Remove BuildKit ARG declarations
      sed -i '/^ARG TARGETPLATFORM$/d' Dockerfile
      sed -i '/^ARG TARGETARCH$/d' Dockerfile
      sed -i '/^ARG BUILDPLATFORM$/d' Dockerfile
      sed -i '/^ARG TARGETOS$/d' Dockerfile
      sed -i '/^ARG TARGETVARIANT$/d' Dockerfile
      # Replace architecture variables with concrete amd64 values
      sed -i 's/\$TARGETARCH/amd64/g; s/\${TARGETARCH}/amd64/g' Dockerfile
      sed -i 's/\$TARGETPLATFORM/linux\/amd64/g; s/\${TARGETPLATFORM}/linux\/amd64/g' Dockerfile
      sed -i 's/\$BUILDPLATFORM/linux\/amd64/g; s/\${BUILDPLATFORM}/linux\/amd64/g' Dockerfile
      sed -i 's/\$TARGETOS/linux/g; s/\${TARGETOS}/linux/g' Dockerfile

Additional Kaniko compatibility issues that require Dockerfile patching:

Go VCS stamping:
Kaniko does NOT have a .git directory. Go 1.18+ embeds VCS info by default, which
causes "error obtaining VCS status: exit status 128" and fails the build.
When a Go Dockerfile contains "go build" WITHOUT "-buildvcs=false", you MUST add a
patch step to inject it. Use sed to replace "go build" with "go build -buildvcs=false"
in the Dockerfile:
  sed -i 's/go build /go build -buildvcs=false /g' Dockerfile

Poetry install without --no-root:
When ANY Dockerfile in the repo uses "poetry install" WITHOUT "--no-root", Poetry tries to
install the current project as a package — this fails if README.md or other metadata
files are missing from the build context. You MUST ALWAYS patch "poetry install" to
"poetry install --no-root" in EVERY Dockerfile that uses poetry. This is NOT optional.
ALWAYS add a patch step for this before the build step:
  sed -i 's/poetry install/poetry install --no-root/g' Dockerfile
If the Dockerfile is specified via the "dockerfile" input (not at context root), adjust the path:
  sed -i 's/poetry install/poetry install --no-root/g' path/to/Dockerfile

RUN --mount=type=cache:
Kaniko ignores --mount=type=cache flags (they're BuildKit-only cache mounts).
The build will still work but without caching. No patching is needed for this —
it's safe to leave as-is.

npm cache permissions:
Kaniko's filesystem snapshotting changes ownership of /root/.npm between layers,
causing "EACCES: permission denied" errors on npm install, npm run build, etc.
When ANY Dockerfile uses npm (npm install, npm run build, npm ci, etc.), you MUST
patch it to redirect the npm cache to /tmp/.npm by inserting an ENV line after the
FROM line. Use sed to insert it:
  sed -i '/^FROM /a ENV npm_config_cache=/tmp/.npm' Dockerfile
This MUST be included in the patch step for every Node.js/npm-based service.

ONLY generate a Kaniko patch step when the Dockerfile has one or more of these specific issues:
  1. BuildKit platform ARGs (TARGETARCH, BUILDPLATFORM, etc.)
  2. "poetry install" without "--no-root"
  3. npm usage (needs npm_config_cache redirect)
  4. "go build" without "-buildvcs=false"
If NONE of these apply, do NOT generate a patch step — the Dockerfile works as-is.

Combine all Kaniko patches for a service into a SINGLE "Patch <service> Dockerfile for Kaniko"
step BEFORE the corresponding build step. Examples:

Python service with poetry:
  - name: Patch backend Dockerfile for Kaniko
    shell: bash
    run: |
      cd ${{ github.workspace }}/backend
      sed -i 's/poetry install/poetry install --no-root/g' Dockerfile

Python service where Dockerfile is NOT at context root (uses "dockerfile" input):
  - name: Patch daily-job Dockerfile for Kaniko
    shell: bash
    run: |
      cd ${{ github.workspace }}/backend
      sed -i 's/poetry install/poetry install --no-root/g' jobs/daily/Dockerfile

Go service with VCS issues:
  - name: Patch api Dockerfile for Kaniko
    shell: bash
    run: |
      cd ${{ github.workspace }}/api
      sed -i 's/go build /go build -buildvcs=false /g' Dockerfile

Node.js/npm service:
  - name: Patch frontend Dockerfile for Kaniko
    shell: bash
    run: |
      cd ${{ github.workspace }}/frontend
      sed -i '/^FROM /a ENV npm_config_cache=/tmp/.npm' Dockerfile

Common Dockerfile pitfalls to be aware of when reviewing repo structure:
  - Go: if go.sum is missing, the Dockerfile must run "go mod tidy" before "go build"
  - Node/TS: if package-lock.json is missing, "npm ci" will fail — use "npm install" instead
  - Rust: use "rust:1-alpine" (latest stable) to avoid MSRV (minimum supported Rust version) breakage
  - PHP: composer.json "name" field must use vendor/package format (e.g. "myapp/service") or composer install fails
  - Elixir: use "elixir:1.16-alpine" or newer; runtime image needs libstdc++, openssl, ncurses-libs
These are Dockerfile concerns, not workflow concerns — but if you detect these languages,
be aware that build failures are often caused by these issues.

For multi-service repos (multiple Dockerfiles in subdirectories), generate one
build step per service and one deploy step per service, with inter-service env
vars wired up (e.g. API_URL pointing to the other service's cluster-internal DNS name).

CRITICAL — docker-compose.yml is the source of truth for multi-service repos:
When a docker-compose.yml exists, you MUST use it to determine the following for
EVERY service (not just the main one):

  a) Build context and Dockerfile path:
     Check the "build" section for each service. Use "context" as the kindling-build
     context and "dockerfile" (relative to context) as the dockerfile input.
     If context is "." (repo root), use ${{ github.workspace }} as context and
     set the dockerfile input to the path of the Dockerfile relative to the root.
     Example — docker-compose says:
       api:
         build:
           context: .
           dockerfile: api/Dockerfile
     Then kindling-build MUST use:
       context: ${{ github.workspace }}
       dockerfile: api/Dockerfile
     NOT context: ${{ github.workspace }}/api (wrong — Dockerfile COPYs from root).

  b) Dependencies (depends_on):
     Map each docker-compose depends_on entry to the corresponding kindling
     dependency type. Apply this to EVERY service, not just the main one.
     Example — if worker_add depends_on rabbitmq and redis, it needs:
       dependencies: |
         - type: rabbitmq
         - type: redis

  c) Environment variables:
     Check the "environment" section for EVERY service. If the app uses different
     env var names than the auto-injected ones (e.g. CELERY_BROKER_URL instead of
     AMQP_URL), add env var mappings to ALL services that need them, not just one.
     Example — if docker-compose shows that worker_add, worker_multiply, AND api
     all use CELERY_BROKER_URL and CELERY_RESULT_BACKEND, then ALL THREE deploy
     steps need those env var mappings.

The kindling-build action supports a "dockerfile" input — use it whenever
the Dockerfile is not at the root of the build context:
  - name: Build daily job image
    uses: kindling-sh/kindling/.github/actions/kindling-build@main
    with:
      name: daily-job
      context: ${{ github.workspace }}/backend
      dockerfile: jobs/daily/Dockerfile
      image: "${{ env.REGISTRY }}/daily-job:${{ env.TAG }}"

CRITICAL — Dev staging environment philosophy:
This generates a LOCAL DEV environment, NOT production. The goal is for the app
to start and be usable with ZERO manual secret setup. Follow these rules:

1. Dependency connection URLs and passwords are ALREADY handled by the operator.
   When you declare a dependency (postgres, redis, etc.), the operator auto-injects
   the connection URL (DATABASE_URL, REDIS_URL, etc.) AND manages the dependency
   container's credentials internally. Do NOT add any of these to the env block:
   - DATABASE_URL, REDIS_URL, MONGO_URL, AMQP_URL, S3_ENDPOINT, etc.
   - POSTGRES_PASSWORD, DATABASE_PASSWORD, REDIS_PASSWORD, MYSQL_PASSWORD, etc.
   - POSTGRES_USER, POSTGRES_DB, MYSQL_USER, MYSQL_DATABASE, etc.

2. App-level secrets (SECRET_KEY, SESSION_SECRET, JWT_SECRET, UTILS_SECRET,
   ENCRYPTION_KEY, etc.) should be set as plain env vars with a generated
   dev-safe random hex value. Example:
     - name: SECRET_KEY
       value: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
     - name: UTILS_SECRET
       value: "f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5"
   Generate a DIFFERENT 64-char hex string for each secret.

3. Optional external integrations should be OMITTED entirely from the env block.
   These are not needed for local dev and including them as secretKeyRef will
   cause the pod to fail with CreateContainerConfigError. Skip:
   - Cloud storage (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, GCS_*, AZURE_STORAGE_*)
   - Monitoring/APM (DD_API_KEY, SENTRY_DSN, NEW_RELIC_*, DATADOG_*)
   - Email/SMS (SMTP_*, SENDGRID_*, TWILIO_*, MAILGUN_*)
   - OAuth providers (SLACK_CLIENT_*, GOOGLE_CLIENT_*, GITHUB_CLIENT_*, AUTH0_*)
   - Analytics (SEGMENT_*, MIXPANEL_*, AMPLITUDE_*)
   Instead, if the app has a config option to disable these features, set it.
   For example: FILE_STORAGE=local instead of FILE_STORAGE=s3.

4. Only use valueFrom.secretKeyRef for credentials that are BOTH:
   (a) absolutely required for the app to start (it will crash without them), AND
   (b) truly external (not provided by an in-cluster dependency)
   This should be RARE in a dev environment.

5. ALWAYS check .env.sample, .env.example, .env.development, and similar files
   for REQUIRED configuration. These files list every env var the app expects.
   For each variable found:
   - Skip it if it's an auto-injected dependency URL (DATABASE_URL, REDIS_URL, etc.)
   - Skip it if it's a dependency credential (POSTGRES_PASSWORD, etc.)
   - Skip it if it's an optional external integration (AWS_*, DD_*, SENTRY_*, etc.)
   - For app secrets (SECRET_KEY, SESSION_SECRET, etc.) → set a random 64-char hex value
   - For URL vars that reference the app itself (URL, BASE_URL, APP_URL, COLLABORATION_URL,
     etc.) → set to "http://${{ github.actor }}-<name>.localhost"
   - For feature flags / storage config → set the local/dev option (e.g. FILE_STORAGE=local)
   - For remaining config → set a sensible dev default
   Missing a required env var is the #1 cause of pods crashing on startup. When in doubt,
   include it with a dev-safe default rather than omitting it.

The "env" input is a YAML block that maps directly to a Kubernetes []EnvVar list.
You MUST use standard Kubernetes EnvVar list format (NOT a map/dict).

Correct format for env vars:
  env: |
    - name: SECRET_KEY
      value: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
    - name: NODE_ENV
      value: "production"
    - name: FILE_STORAGE
      value: "local"

WRONG format (this will cause a CRD validation error):
  env: |
    SECRET_KEY:
      value: "some-value"

If any secretKeyRef IS used, those secrets are managed by
"kindling secrets set <NAME> <VALUE>" and stored as Kubernetes Secrets.
Include a YAML comment noting which secrets need to be set.

OAuth / public exposure:
If the repository uses OAuth or OIDC (Auth0, Okta, Firebase Auth, NextAuth, etc.),
services that handle OAuth callbacks need a publicly accessible URL instead of
*.localhost. When the user indicates a public URL is available, use it for the
ingress-host of the auth-handling service. If no public URL is specified but OAuth
patterns are detected, add a YAML comment noting:
  # NOTE: OAuth detected — run 'kindling expose' for a public HTTPS URL

FINAL VALIDATION — before outputting the YAML, verify:
  1. Every deploy step that uses $(AMQP_URL) in its env MUST have "- type: rabbitmq"
     in its dependencies. Every step using $(REDIS_URL) MUST have "- type: redis".
     Every step using $(DATABASE_URL) MUST have "- type: postgres" (or mysql).
     A $(VAR) reference without the matching dependency will cause a runtime crash.
  2. Every build step's "context" matches the docker-compose "build.context" for that
     service. If context is "." or the repo root, use ${{ github.workspace }}.

Return ONLY the raw YAML content of the workflow file. No markdown code fences,
no explanation text, no commentary. Just the YAML.`

	// ── Build user prompt ───────────────────────────────────────
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Generate a kindling dev-deploy.yml GitHub Actions workflow for this repository named %q.\n\n", ctx.name))
	b.WriteString(fmt.Sprintf("Default branch: %s (use this in the 'on: push: branches:' trigger)\n\n", ctx.branch))

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

	// Reference examples
	b.WriteString("## Reference: single-service workflow example\n```yaml\n")
	b.WriteString(singleServiceExample)
	b.WriteString("\n```\n\n")

	b.WriteString("## Reference: multi-service workflow example\n```yaml\n")
	b.WriteString(multiServiceExample)
	b.WriteString("\n```\n\n")

	b.WriteString("Now generate the dev-deploy.yml workflow YAML for this repository. Return ONLY the YAML.\n")

	user = b.String()
	return system, user
}

// ────────────────────────────────────────────────────────────────────────────
// Reference examples embedded in the prompt
// ────────────────────────────────────────────────────────────────────────────

const singleServiceExample = `name: Dev Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

env:
  REGISTRY: "registry:5000"
  TAG: "${{ github.actor }}-${{ github.sha }}"

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Clean builds directory
        shell: bash
        run: |
          rm -f /builds/*.done /builds/*.request /builds/*.processing \
                /builds/*.apply /builds/*.apply-done /builds/*.apply-log \
                /builds/*.apply-exitcode /builds/*.exitcode \
                /builds/*.log /builds/*.dest /builds/*.tar.gz \
                /builds/*.yaml /builds/*.sh

      - name: Build image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: sample-app
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/sample-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-sample-app"
          image: "${{ env.REGISTRY }}/sample-app:${{ env.TAG }}"
          port: "8080"
          labels: |
            app.kubernetes.io/part-of: sample-app
            apps.example.com/github-username: ${{ github.actor }}
          ingress-host: "${{ github.actor }}-sample-app.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis

      - name: Summary
        run: |
          echo "🎉 Deploy complete!"
          echo "🌐 http://${{ github.actor }}-sample-app.localhost"`

const multiServiceExample = `name: Dev Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

env:
  REGISTRY: "registry:5000"
  TAG: "${{ github.actor }}-${{ github.sha }}"

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Clean builds directory
        shell: bash
        run: |
          rm -f /builds/*.done /builds/*.request /builds/*.processing \
                /builds/*.apply /builds/*.apply-done /builds/*.apply-log \
                /builds/*.apply-exitcode /builds/*.exitcode \
                /builds/*.log /builds/*.dest /builds/*.tar.gz \
                /builds/*.yaml /builds/*.sh

      - name: Build API image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: api
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/api:${{ env.TAG }}"
          exclude: "./ui"

      - name: Build UI image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: ui
          context: "${{ github.workspace }}/ui"
          image: "${{ env.REGISTRY }}/ui:${{ env.TAG }}"

      - name: Deploy API
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-api"
          image: "${{ env.REGISTRY }}/api:${{ env.TAG }}"
          port: "8080"
          labels: |
            app.kubernetes.io/part-of: my-app
            app.kubernetes.io/component: api
            apps.example.com/github-username: ${{ github.actor }}
          ingress-host: "${{ github.actor }}-api.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis

      - name: Deploy UI
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-ui"
          image: "${{ env.REGISTRY }}/ui:${{ env.TAG }}"
          port: "80"
          health-check-path: "/"
          labels: |
            app.kubernetes.io/part-of: my-app
            app.kubernetes.io/component: ui
            apps.example.com/github-username: ${{ github.actor }}
          env: |
            - name: API_URL
              value: "http://${{ github.actor }}-api:8080"
          ingress-host: "${{ github.actor }}-ui.localhost"

      - name: Summary
        run: |
          echo "🎉 Deploy complete!"
          echo "🌐 UI:  http://${{ github.actor }}-ui.localhost"
          echo "🌐 API: http://${{ github.actor }}-api.localhost"`

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
