package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze a repository for kindling readiness",
	Long: `Scans a local repository and checks everything needed before running
'kindling generate' or 'kindling deploy'. No API key required — this is
pure deterministic analysis.

The output is a prescriptive checklist: what's ready, what's missing,
and exactly what to do about each item.

Examples:
  kindling analyze                   # Analyze current directory
  kindling analyze -r /path/to/repo  # Analyze a specific repo
  kindling analyze --fix             # Show copy-pasteable fix commands`,
	RunE: runAnalyze,
}

var (
	analyzeRepoPath string
	analyzeFix      bool
)

func init() {
	analyzeCmd.Flags().StringVarP(&analyzeRepoPath, "repo-path", "r", ".", "Path to the repository to analyze")
	analyzeCmd.Flags().BoolVar(&analyzeFix, "fix", false, "Show copy-pasteable fix commands for each issue")
	rootCmd.AddCommand(analyzeCmd)
}

// ── Check result types ──────────────────────────────────────────

type checkStatus int

const (
	checkPass checkStatus = iota
	checkWarn
	checkFail
	checkInfo
)

type checkResult struct {
	status  checkStatus
	message string
	fix     string // optional fix command/instruction
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	repoPath, err := filepath.Abs(analyzeRepoPath)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	if info, err := os.Stat(repoPath); err != nil || !info.IsDir() {
		return fmt.Errorf("repo path does not exist or is not a directory: %s", repoPath)
	}

	fmt.Fprintf(os.Stderr, "\n  %s%s kindling analyze %s— %s%s\n\n",
		colorBold, colorCyan, colorReset, repoPath, colorReset)

	// Reuse the generate pipeline's repo scanner
	repoCtx, err := scanRepo(repoPath)
	if err != nil {
		return fmt.Errorf("repo scan failed: %w", err)
	}

	var checks []checkResult

	// ── 1. Git state ────────────────────────────────────────────
	checks = append(checks, checkGitState(repoPath)...)

	// ── 2. Dockerfiles ──────────────────────────────────────────
	checks = append(checks, checkDockerfiles(repoPath, repoCtx)...)

	// ── 3. Dependencies & language detection ────────────────────
	checks = append(checks, checkDependencies(repoCtx)...)

	// ── 4. Multi-agent architecture ─────────────────────────────
	checks = append(checks, checkAgentArchitecture(repoCtx)...)

	// ── 5. Secrets & credentials ────────────────────────────────
	checks = append(checks, checkSecrets(repoCtx)...)

	// ── 6. Kindling cluster readiness ───────────────────────────
	checks = append(checks, checkCluster()...)

	// ── Print results ───────────────────────────────────────────
	printChecklist(checks)

	return nil
}

// ── Check functions ─────────────────────────────────────────────

func checkGitState(repoPath string) []checkResult {
	var results []checkResult

	// Check if it's a git repo
	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		results = append(results, checkResult{
			status:  checkFail,
			message: "Not a git repository",
			fix:     "cd " + repoPath + " && git init && git add -A && git commit -m 'initial commit'",
		})
		return results
	}
	results = append(results, checkResult{
		status: checkPass, message: "Git repository initialized",
	})

	// Check for commits
	out, err := exec.Command("git", "-C", repoPath, "log", "--oneline", "-1").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		results = append(results, checkResult{
			status:  checkFail,
			message: "No commits — kindling CI needs at least one commit",
			fix:     "cd " + repoPath + " && git add -A && git commit -m 'initial commit'",
		})
	} else {
		results = append(results, checkResult{
			status: checkPass, message: "Has commits",
		})
	}

	// Check for remote
	out, err = exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		results = append(results, checkResult{
			status:  checkFail,
			message: "No git remote — kindling CI requires a GitHub/GitLab remote",
			fix:     "git remote add origin https://github.com/<user>/<repo>.git",
		})
	} else {
		remote := strings.TrimSpace(string(out))
		results = append(results, checkResult{
			status: checkPass, message: fmt.Sprintf("Remote: %s", remote),
		})

		// Detect provider
		if strings.Contains(remote, "github.com") {
			results = append(results, checkResult{
				status: checkInfo, message: "CI provider: GitHub Actions",
			})
		} else if strings.Contains(remote, "gitlab") {
			results = append(results, checkResult{
				status: checkInfo, message: "CI provider: GitLab CI",
			})
		}
	}

	// Check for .gitignore
	if _, err := os.Stat(filepath.Join(repoPath, ".gitignore")); os.IsNotExist(err) {
		results = append(results, checkResult{
			status:  checkWarn,
			message: "No .gitignore — recommend adding one to keep the repo clean",
			fix:     "echo '__pycache__/\\n*.pyc\\n.env\\nvenv/\\nnode_modules/\\n.DS_Store' > .gitignore",
		})
	}

	return results
}

func checkDockerfiles(repoPath string, ctx *repoContext) []checkResult {
	var results []checkResult

	if ctx.dockerfileCount == 0 {
		// Detect language to give a specific Dockerfile recommendation
		lang := detectPrimaryLanguage(ctx)
		results = append(results, checkResult{
			status:  checkFail,
			message: "No Dockerfile found — kindling builds with Kaniko and requires a Dockerfile",
			fix:     dockerfileFixForLanguage(lang, repoPath),
		})
	} else {
		results = append(results, checkResult{
			status:  checkPass,
			message: fmt.Sprintf("Found %d Dockerfile(s)", ctx.dockerfileCount),
		})

		// Check each Dockerfile for Kaniko compatibility issues
		for path, content := range ctx.dockerfiles {
			issues := checkKanikoCompat(path, content)
			results = append(results, issues...)
		}
	}

	return results
}

func checkDependencies(ctx *repoContext) []checkResult {
	var results []checkResult

	if ctx.depFileCount == 0 {
		results = append(results, checkResult{
			status:  checkWarn,
			message: "No dependency manifests found (requirements.txt, package.json, go.mod, etc.)",
		})
	} else {
		results = append(results, checkResult{
			status:  checkPass,
			message: fmt.Sprintf("Found %d dependency manifest(s)", ctx.depFileCount),
		})
	}

	// Report detected languages from dep files
	lang := detectPrimaryLanguage(ctx)
	if lang != "" {
		results = append(results, checkResult{
			status: checkInfo, message: fmt.Sprintf("Primary language: %s", lang),
		})
	}

	return results
}

func checkAgentArchitecture(ctx *repoContext) []checkResult {
	var results []checkResult

	hasAgentArch := len(ctx.agentFrameworks) > 0 || len(ctx.mcpServers) > 0 ||
		len(ctx.vectorStores) > 0 || len(ctx.workerProcesses) > 0 ||
		len(ctx.interServiceCalls) > 0

	if !hasAgentArch {
		return results
	}

	results = append(results, checkResult{
		status: checkInfo, message: "Multi-agent architecture detected",
	})

	if len(ctx.agentFrameworks) > 0 {
		results = append(results, checkResult{
			status:  checkInfo,
			message: fmt.Sprintf("Agent frameworks: %s", strings.Join(ctx.agentFrameworks, ", ")),
		})
	}

	if len(ctx.mcpServers) > 0 {
		results = append(results, checkResult{
			status: checkInfo, message: fmt.Sprintf("MCP servers: %d detected", len(ctx.mcpServers)),
		})
		// Each MCP server needs its own Dockerfile
		for _, s := range ctx.mcpServers {
			if strings.Contains(s, "MCP config file") {
				results = append(results, checkResult{
					status: checkInfo, message: fmt.Sprintf("  • %s", s),
				})
			}
		}
	}

	if len(ctx.vectorStores) > 0 {
		results = append(results, checkResult{
			status:  checkInfo,
			message: fmt.Sprintf("Vector stores: %s — API keys will be surfaced as secrets", strings.Join(ctx.vectorStores, ", ")),
		})
	}

	if len(ctx.workerProcesses) > 0 {
		results = append(results, checkResult{
			status:  checkInfo,
			message: fmt.Sprintf("Background workers: %d pattern(s) — will be deployed as separate services", len(ctx.workerProcesses)),
		})
		for _, w := range ctx.workerProcesses {
			results = append(results, checkResult{
				status: checkInfo, message: fmt.Sprintf("  • %s", w),
			})
		}
	}

	if len(ctx.interServiceCalls) > 0 {
		results = append(results, checkResult{
			status:  checkInfo,
			message: fmt.Sprintf("Inter-service calls: %d pattern(s) — K8s DNS will be configured", len(ctx.interServiceCalls)),
		})
	}

	return results
}

func checkSecrets(ctx *repoContext) []checkResult {
	var results []checkResult

	// Merge explicit detections with framework-implied secrets
	allSecrets := make(map[string]bool)
	for _, s := range ctx.externalSecrets {
		allSecrets[s] = true
	}
	for _, s := range inferFrameworkSecrets(ctx) {
		allSecrets[s] = true
	}

	if len(allSecrets) == 0 {
		return results
	}

	results = append(results, checkResult{
		status:  checkWarn,
		message: fmt.Sprintf("Found %d credential(s) that need to be set before deploy:", len(allSecrets)),
	})

	for name := range allSecrets {
		results = append(results, checkResult{
			status:  checkWarn,
			message: fmt.Sprintf("  • %s", name),
			fix:     fmt.Sprintf("kindling secrets set %s <value>", name),
		})
	}

	return results
}

// inferFrameworkSecrets returns secret names implied by detected frameworks.
// For example, LangChain with OpenAI always needs OPENAI_API_KEY even if
// the code never calls os.getenv() directly.
func inferFrameworkSecrets(ctx *repoContext) []string {
	var secrets []string
	seen := make(map[string]bool)

	// Check dep files for framework+provider combos
	allDeps := ""
	for _, content := range ctx.depFiles {
		allDeps += "\n" + content
	}

	// LangChain + OpenAI
	hasLangChain := false
	for _, f := range ctx.agentFrameworks {
		if strings.Contains(strings.ToLower(f), "langchain") {
			hasLangChain = true
		}
	}
	if hasLangChain {
		if strings.Contains(allDeps, "langchain-openai") || strings.Contains(allDeps, "openai") {
			if !seen["OPENAI_API_KEY"] {
				secrets = append(secrets, "OPENAI_API_KEY")
				seen["OPENAI_API_KEY"] = true
			}
		}
		if strings.Contains(allDeps, "langchain-anthropic") || strings.Contains(allDeps, "anthropic") {
			if !seen["ANTHROPIC_API_KEY"] {
				secrets = append(secrets, "ANTHROPIC_API_KEY")
				seen["ANTHROPIC_API_KEY"] = true
			}
		}
		if strings.Contains(allDeps, "langchain-google") {
			if !seen["GOOGLE_API_KEY"] {
				secrets = append(secrets, "GOOGLE_API_KEY")
				seen["GOOGLE_API_KEY"] = true
			}
		}
	}

	// CrewAI typically needs OpenAI
	for _, f := range ctx.agentFrameworks {
		if strings.Contains(strings.ToLower(f), "crewai") {
			if !seen["OPENAI_API_KEY"] {
				secrets = append(secrets, "OPENAI_API_KEY")
				seen["OPENAI_API_KEY"] = true
			}
		}
	}

	// Vector store API keys
	for _, vs := range ctx.vectorStores {
		vsLower := strings.ToLower(vs)
		switch {
		case strings.Contains(vsLower, "pinecone") && !seen["PINECONE_API_KEY"]:
			secrets = append(secrets, "PINECONE_API_KEY")
			seen["PINECONE_API_KEY"] = true
		case strings.Contains(vsLower, "weaviate") && !seen["WEAVIATE_API_KEY"]:
			secrets = append(secrets, "WEAVIATE_API_KEY")
			seen["WEAVIATE_API_KEY"] = true
		case strings.Contains(vsLower, "qdrant") && !seen["QDRANT_API_KEY"]:
			secrets = append(secrets, "QDRANT_API_KEY")
			seen["QDRANT_API_KEY"] = true
		}
	}

	return secrets
}

func checkCluster() []checkResult {
	var results []checkResult

	// Check if Kind cluster exists
	out, err := exec.Command("kind", "get", "clusters").Output()
	if err != nil {
		results = append(results, checkResult{
			status:  checkFail,
			message: "kind is not installed or not in PATH",
			fix:     "brew install kind  # or: go install sigs.k8s.io/kind@latest",
		})
		return results
	}

	clusters := strings.Split(strings.TrimSpace(string(out)), "\n")
	found := false
	for _, c := range clusters {
		if strings.TrimSpace(c) == clusterName {
			found = true
			break
		}
	}

	if !found {
		results = append(results, checkResult{
			status:  checkWarn,
			message: fmt.Sprintf("Kind cluster '%s' not found", clusterName),
			fix:     "kindling init",
		})
	} else {
		results = append(results, checkResult{
			status: checkPass, message: fmt.Sprintf("Kind cluster '%s' is running", clusterName),
		})

		// Check for operator
		opOut, err := exec.Command("kubectl", "--context", kindContext(),
			"get", "deployment", "kindling-controller-manager",
			"-n", "kindling-system", "--no-headers").Output()
		if err != nil || len(strings.TrimSpace(string(opOut))) == 0 {
			results = append(results, checkResult{
				status:  checkWarn,
				message: "Kindling operator not deployed",
				fix:     "kindling init",
			})
		} else {
			results = append(results, checkResult{
				status: checkPass, message: "Kindling operator is running",
			})
		}
	}

	return results
}

// ── Helper functions ────────────────────────────────────────────

func detectPrimaryLanguage(ctx *repoContext) string {
	for name := range ctx.depFiles {
		base := filepath.Base(name)
		switch {
		case base == "requirements.txt" || base == "pyproject.toml" || base == "Pipfile" || base == "setup.py":
			return "Python"
		case base == "package.json":
			return "Node.js"
		case base == "go.mod":
			return "Go"
		case base == "Cargo.toml":
			return "Rust"
		case base == "Gemfile":
			return "Ruby"
		case base == "pom.xml" || base == "build.gradle" || base == "build.gradle.kts":
			return "Java/Kotlin"
		case base == "mix.exs":
			return "Elixir"
		case base == "composer.json":
			return "PHP"
		case strings.HasSuffix(base, ".csproj") || strings.HasSuffix(base, ".fsproj"):
			return ".NET"
		}
	}
	return ""
}

func dockerfileFixForLanguage(lang, repoPath string) string {
	switch lang {
	case "Python":
		return fmt.Sprintf(`Create a Dockerfile in %s. Example for Python:

  FROM python:3.12-slim
  WORKDIR /app
  COPY requirements.txt .
  RUN pip install --no-cache-dir -r requirements.txt
  COPY . .
  CMD ["python", "-m", "agent.worker"]`, repoPath)
	case "Node.js":
		return fmt.Sprintf(`Create a Dockerfile in %s. Example for Node.js:

  FROM node:20-slim
  WORKDIR /app
  ENV npm_config_cache=/tmp/.npm
  COPY package*.json ./
  RUN npm ci --only=production
  COPY . .
  CMD ["node", "index.js"]`, repoPath)
	case "Go":
		return fmt.Sprintf(`Create a Dockerfile in %s. Example for Go:

  FROM golang:1.22-alpine AS builder
  WORKDIR /app
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  RUN CGO_ENABLED=0 go build -buildvcs=false -o /app/server .

  FROM alpine:3.19
  COPY --from=builder /app/server /server
  CMD ["/server"]`, repoPath)
	default:
		return "Create a Dockerfile in " + repoPath + " appropriate for your language"
	}
}

func checkKanikoCompat(path, content string) []checkResult {
	var results []checkResult

	// BuildKit platform ARGs
	if strings.Contains(content, "TARGETARCH") || strings.Contains(content, "BUILDPLATFORM") ||
		strings.Contains(content, "TARGETPLATFORM") || strings.Contains(content, "TARGETOS") {
		results = append(results, checkResult{
			status:  checkWarn,
			message: fmt.Sprintf("%s uses BuildKit platform ARGs — kindling will auto-patch for Kaniko", path),
		})
	}

	// Poetry without --no-root
	if strings.Contains(content, "poetry install") && !strings.Contains(content, "--no-root") {
		results = append(results, checkResult{
			status:  checkWarn,
			message: fmt.Sprintf("%s has 'poetry install' without --no-root — kindling will auto-patch", path),
		})
	}

	// npm without cache redirect
	if (strings.Contains(content, "npm install") || strings.Contains(content, "npm ci") ||
		strings.Contains(content, "npm run")) && !strings.Contains(content, "npm_config_cache") {
		results = append(results, checkResult{
			status:  checkWarn,
			message: fmt.Sprintf("%s uses npm without cache redirect — kindling will auto-patch for Kaniko", path),
		})
	}

	// Go build without -buildvcs=false
	if strings.Contains(content, "go build") && !strings.Contains(content, "-buildvcs=false") {
		results = append(results, checkResult{
			status:  checkWarn,
			message: fmt.Sprintf("%s has 'go build' without -buildvcs=false — kindling will auto-patch for Kaniko", path),
		})
	}

	return results
}

// ── Output formatting ───────────────────────────────────────────

func printChecklist(checks []checkResult) {
	passCount, warnCount, failCount := 0, 0, 0

	for _, c := range checks {
		var prefix string
		switch c.status {
		case checkPass:
			prefix = fmt.Sprintf("  %s✅%s", colorGreen, colorReset)
			passCount++
		case checkWarn:
			prefix = fmt.Sprintf("  %s⚠️ %s", colorYellow, colorReset)
			warnCount++
		case checkFail:
			prefix = fmt.Sprintf("  %s❌%s", colorRed, colorReset)
			failCount++
		case checkInfo:
			prefix = fmt.Sprintf("  %sℹ️ %s", colorCyan, colorReset)
		}

		fmt.Fprintf(os.Stderr, "%s %s\n", prefix, c.message)

		if analyzeFix && c.fix != "" {
			fmt.Fprintf(os.Stderr, "     %s→ %s%s\n", colorDim, c.fix, colorReset)
		}
	}

	// Summary
	fmt.Fprintln(os.Stderr)
	if failCount > 0 {
		fmt.Fprintf(os.Stderr, "  %s%d blocker(s)%s to fix before running 'kindling generate'\n",
			colorRed, failCount, colorReset)
	}
	if warnCount > 0 {
		fmt.Fprintf(os.Stderr, "  %s%d warning(s)%s to review\n",
			colorYellow, warnCount, colorReset)
	}
	if failCount == 0 {
		fmt.Fprintf(os.Stderr, "  %s✅ Ready for 'kindling generate'%s\n", colorGreen, colorReset)
	}

	// Next step guidance
	fmt.Fprintln(os.Stderr)
	if failCount > 0 {
		fmt.Fprintf(os.Stderr, "  %sNext:%s Fix the blockers above, then re-run 'kindling analyze'\n",
			colorBold, colorReset)
	} else {
		fmt.Fprintf(os.Stderr, "  %sNext:%s kindling generate -k <api-key> -r <repo-path>\n",
			colorBold, colorReset)
	}
	fmt.Fprintln(os.Stderr)
}
