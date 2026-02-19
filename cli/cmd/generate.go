package cmd

import (
	"fmt"
	"io/fs"
	"os"
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
  kindling generate -k sk-... -r . --provider openai --model gpt-4o
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
	genDryRun   bool
)

func init() {
	generateCmd.Flags().StringVarP(&genAPIKey, "api-key", "k", "", "GenAI API key (required)")
	generateCmd.Flags().StringVarP(&genRepoPath, "repo-path", "r", ".", "Path to the local repository to analyze")
	generateCmd.Flags().StringVar(&genProvider, "provider", "openai", "AI provider: openai or anthropic")
	generateCmd.Flags().StringVar(&genModel, "model", "", "Model name (default: gpt-4o for openai, claude-sonnet-4-20250514 for anthropic)")
	generateCmd.Flags().StringVarP(&genOutput, "output", "o", "", "Output path (default: <repo-path>/.github/workflows/dev-deploy.yml)")
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
			genModel = "gpt-4o"
		}
	}

	if genOutput == "" {
		genOutput = filepath.Join(repoPath, ".github", "workflows", "dev-deploy.yml")
	}

	// ── Scan the repository ─────────────────────────────────────
	header("Analyzing repository")
	step("📂", repoPath)

	repoCtx, err := scanRepo(repoPath)
	if err != nil {
		return fmt.Errorf("repo scan failed: %w", err)
	}

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
		fmt.Println()
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
	name            string
	tree            string
	dockerfiles     map[string]string // relative path → content
	depFiles        map[string]string // relative path → content
	composeFile     string            // docker-compose.yml content (if found)
	sourceSnippets  map[string]string // relative path → truncated content
	dockerfileCount int
	depFileCount    int
	externalSecrets []string // detected external credential env var names
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

	// Sample entry-point source files (up to 6)
	mainFiles := prioritizeSourceFiles(sourceFiles)
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
func prioritizeSourceFiles(files []string) []string {
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
   Uses: jeff-vincent/kindling/.github/actions/kindling-build@main
   Inputs: name (required), context (required), image (required), exclude (optional), timeout (optional)
   IMPORTANT: kindling-build runs the Dockerfile found at <context>/Dockerfile as-is
   using Kaniko inside the cluster. It does NOT modify or generate Dockerfiles.
   Every service in the workflow MUST have a working Dockerfile already in the repo.
   If the Dockerfile doesn't build locally (e.g. docker build), it won't build
   in kindling either. The "context" input must point to the directory containing
   the service's Dockerfile.

2. kindling-deploy — deploys a DevStagingEnvironment CR via sidecar
   Uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
   Inputs: name (required), image (required), port (required),
           labels, env, dependencies, ingress-host, ingress-class,
           health-check-path, replicas, service-type, wait

Key conventions you MUST follow:
- Registry: registry:5000 (in-cluster)
- Image tag: ${{ github.actor }}-${{ github.sha }}
- Runner: runs-on: [self-hosted, "${{ github.actor }}"]
- Ingress host pattern: ${{ github.actor }}-<service>.localhost
- DSE name pattern: ${{ github.actor }}-<service>
- Always trigger on push to main + workflow_dispatch
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

External credentials and secrets:
When the user's code references external credentials (API keys, tokens, DSNs, etc.),
wire them into the deploy step's "env" input as Kubernetes Secret references.
Use this pattern for each detected credential:
  <VAR_NAME>:
    secretKeyRef:
      name: kindling-secret-<lowercase-var-name>
      key: value
These secrets are managed by "kindling secrets set <NAME> <VALUE>" and stored as
Kubernetes Secrets with the label app.kubernetes.io/managed-by=kindling.
Include a YAML comment above the env block noting which secrets need to be set:
  # Requires: kindling secrets set <NAME> <VALUE>
Do NOT hardcode placeholder values for secrets. Always use secretKeyRef.

Return ONLY the raw YAML content of the workflow file. No markdown code fences,
no explanation text, no commentary. Just the YAML.`

	// ── Build user prompt ───────────────────────────────────────
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Generate a kindling dev-deploy.yml GitHub Actions workflow for this repository named %q.\n\n", ctx.name))

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
		b.WriteString("## Detected external credentials\n\n")
		b.WriteString("The following environment variables appear to be external credentials.\n")
		b.WriteString("Wire each one into the deploy step(s) using secretKeyRef:\n\n")
		for _, name := range ctx.externalSecrets {
			b.WriteString(fmt.Sprintf("- %s → kindling-secret-%s\n", name, strings.ToLower(strings.ReplaceAll(name, "_", "-"))))
		}
		b.WriteString("\n")
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
        uses: jeff-vincent/kindling/.github/actions/kindling-build@main
        with:
          name: sample-app
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/sample-app:${{ env.TAG }}"

      - name: Deploy
        uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
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
        uses: jeff-vincent/kindling/.github/actions/kindling-build@main
        with:
          name: api
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/api:${{ env.TAG }}"
          exclude: "./ui"

      - name: Build UI image
        uses: jeff-vincent/kindling/.github/actions/kindling-build@main
        with:
          name: ui
          context: "${{ github.workspace }}/ui"
          image: "${{ env.REGISTRY }}/ui:${{ env.TAG }}"

      - name: Deploy API
        uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
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
        uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
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

// credentialExactNames are full env var names that indicate external credentials.
var credentialExactNames = map[string]bool{
	"DATABASE_URL":          true,
	"REDIS_URL":             true,
	"MONGO_URL":             true,
	"MONGODB_URI":           true,
	"AMQP_URL":              true,
	"RABBITMQ_URL":          true,
	"KAFKA_BROKERS":         true,
	"ELASTICSEARCH_URL":     true,
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
