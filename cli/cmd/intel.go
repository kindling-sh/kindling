package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ── Agent configuration ─────────────────────────────────────────

// agentTarget describes where a coding agent reads its system prompt.
type agentTarget struct {
	Name string // display name (e.g. "GitHub Copilot")
	File string // relative path from repo root
}

// knownAgents lists every coding agent we know how to configure.
var knownAgents = []agentTarget{
	{Name: "GitHub Copilot", File: ".github/copilot-instructions.md"},
	{Name: "Claude Code", File: "CLAUDE.md"},
	{Name: "Cursor", File: ".cursor/rules/kindling.mdc"},
	{Name: "Windsurf", File: ".windsurfrules"},
}

// intelStateFile is the metadata file that tracks what we backed up.
const intelStateFile = ".kindling/intel-state.json"

// intelDisabledFile is written by `kindling intel off` to prevent auto-
// reactivation until the next explicit `kindling intel on`.
const intelDisabledFile = ".kindling/intel-disabled"

// intelSessionTimeout is how long intel stays active after the last kindling
// interaction. After this duration, the next kindling command will restore the
// originals before re-activating (fresh session).
const intelSessionTimeout = 1 * time.Hour

// intelState is persisted to .kindling/intel-state.json while intel is on.
type intelState struct {
	Active          bool              `json:"active"`
	Backups         map[string]string `json:"backups"`          // agent file → backup path (relative)
	Written         []string          `json:"written"`          // agent files we wrote
	LastInteraction string            `json:"last_interaction"` // RFC3339 timestamp
}

func (s *intelState) lastInteractionTime() time.Time {
	t, err := time.Parse(time.RFC3339, s.LastInteraction)
	if err != nil {
		return time.Time{} // zero → always stale
	}
	return t
}

func (s *intelState) touchInteraction() {
	s.LastInteraction = time.Now().Format(time.RFC3339)
}

// ── Commands ────────────────────────────────────────────────────

var intelCmd = &cobra.Command{
	Use:   "intel",
	Short: "Give your coding agent full kindling context",
	Long: `Manages your coding agent's system prompt so it understands kindling.

Intel activates automatically on any kindling command and restores your
original agent config after an hour of inactivity. You can also control
it manually:

  kindling intel on       Activate now (clears any manual disable)
  kindling intel off      Restore originals and disable auto-activation
  kindling intel status   Show whether intel is active`,
}

var intelOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Activate kindling context for your coding agent",
	RunE:  runIntelOn,
}

var intelOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Restore original agent config and disable auto-activation",
	RunE:  runIntelOff,
}

var intelStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether intel is active",
	RunE:  runIntelStatus,
}

func init() {
	intelCmd.AddCommand(intelOnCmd)
	intelCmd.AddCommand(intelOffCmd)
	intelCmd.AddCommand(intelStatusCmd)
	rootCmd.AddCommand(intelCmd)
}

// ── Auto-lifecycle (called by PersistentPreRun) ─────────────────

// ensureIntel is called before every kindling command. It manages the
// intel lifecycle automatically:
//  1. If manually disabled → skip
//  2. If active but stale → restore originals, then re-activate (new session)
//  3. If not active → activate
//  4. Touch the last-interaction timestamp
func ensureIntel(cmd *cobra.Command) {
	if shouldSkipIntel(cmd) {
		return
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return // not in a repo, nothing to do
	}

	// Respect manual disable
	if _, err := os.Stat(filepath.Join(repoRoot, intelDisabledFile)); err == nil {
		return
	}

	state, _ := loadIntelState(repoRoot)

	if state != nil && state.Active {
		// Check staleness
		if time.Since(state.lastInteractionTime()) > intelSessionTimeout {
			// Session expired — restore originals silently, then re-activate
			restoreIntel(repoRoot, state)
			activateIntel(repoRoot, false)
		} else {
			// Still fresh — just touch the timestamp
			state.touchInteraction()
			saveIntelState(repoRoot, state)
		}
		return
	}

	// Not active — activate silently
	activateIntel(repoRoot, false)
}

// shouldSkipIntel returns true for commands that shouldn't trigger auto-intel.
func shouldSkipIntel(cmd *cobra.Command) bool {
	name := cmd.Name()
	// Skip for intel subcommands (they manage their own lifecycle),
	// version, help, and completion
	switch name {
	case "on", "off", "status", "version", "help", "completion":
		return true
	}
	// Also skip if the parent is the intel command
	if cmd.Parent() != nil && cmd.Parent().Name() == "intel" {
		return true
	}
	return false
}

// ── On ──────────────────────────────────────────────────────────

func runIntelOn(cmd *cobra.Command, args []string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	// Clear the disabled flag if it exists
	os.Remove(filepath.Join(repoRoot, intelDisabledFile))

	// Check if already active
	state, _ := loadIntelState(repoRoot)
	if state != nil && state.Active {
		// Touch timestamp and confirm
		state.touchInteraction()
		saveIntelState(repoRoot, state)
		fmt.Fprintf(os.Stderr, "  %s⚡ kindling intel is already active%s (timestamp refreshed)\n", colorGreen+colorBold, colorReset)
		return nil
	}

	return activateIntel(repoRoot, true)
}

// activateIntel backs up existing agent configs and writes the kindling
// context document. If verbose is true, prints progress to stderr.
func activateIntel(repoRoot string, verbose bool) error {
	if verbose {
		header("Activating kindling intel")
	}

	contextDoc := buildContextDocument(repoRoot)

	newState := &intelState{
		Active:  true,
		Backups: make(map[string]string),
	}
	newState.touchInteraction()

	backupDir := filepath.Join(repoRoot, ".kindling", "intel-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("cannot create backup directory: %w", err)
	}

	for _, agent := range knownAgents {
		agentPath := filepath.Join(repoRoot, agent.File)

		// Check if file exists and already has kindling content (don't re-backup our own file)
		if data, err := os.ReadFile(agentPath); err == nil {
			if !strings.Contains(string(data), "Kindling — Coding Agent Context") {
				// It's an original file — back it up
				backupName := strings.ReplaceAll(agent.File, "/", "__")
				backupPath := filepath.Join(backupDir, backupName)
				if err := os.WriteFile(backupPath, data, 0644); err != nil {
					return fmt.Errorf("cannot back up %s: %w", agent.File, err)
				}
				newState.Backups[agent.File] = filepath.Join(".kindling", "intel-backups", backupName)
				if verbose {
					step("💾", fmt.Sprintf("Backed up %s%s%s", colorDim, agent.File, colorReset))
				}
			}
			// If it already contains kindling content, skip backup (stale re-activation)
		}

		// Write the kindling context
		dir := filepath.Dir(agentPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create directory for %s: %w", agent.File, err)
		}

		content := formatForAgent(agent, contextDoc)
		if err := os.WriteFile(agentPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("cannot write %s: %w", agent.File, err)
		}
		newState.Written = append(newState.Written, agent.File)
		if verbose {
			step("✍️ ", fmt.Sprintf("Wrote %s%s%s (%s)", colorCyan, agent.File, colorReset, agent.Name))
		}
	}

	// Also write the canonical copy
	canonicalPath := filepath.Join(repoRoot, ".kindling", "context.md")
	if err := os.WriteFile(canonicalPath, []byte(contextDoc), 0644); err != nil {
		return fmt.Errorf("cannot write .kindling/context.md: %w", err)
	}
	if verbose {
		step("📄", fmt.Sprintf("Wrote %s.kindling/context.md%s (canonical)", colorCyan, colorReset))
	}

	if err := saveIntelState(repoRoot, newState); err != nil {
		return fmt.Errorf("cannot save intel state: %w", err)
	}

	if verbose {
		fmt.Println()
		fmt.Printf("  %s⚡ kindling intel is active%s\n", colorGreen+colorBold, colorReset)
		fmt.Printf("  Your coding agent now has full kindling context.\n")
		fmt.Printf("  It will auto-restore after %s of inactivity.\n\n", intelSessionTimeout)
	}

	return nil
}

// ── Off ─────────────────────────────────────────────────────────

func runIntelOff(cmd *cobra.Command, args []string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	state, err := loadIntelState(repoRoot)
	if err != nil || state == nil || !state.Active {
		fmt.Fprintf(os.Stderr, "  kindling intel is not active — nothing to restore.\n")
		// Still set the disabled flag so auto-activation doesn't kick in
		setIntelDisabled(repoRoot)
		return nil
	}

	header("Deactivating kindling intel")
	restoreIntel(repoRoot, state)

	// Set disabled flag to prevent auto-reactivation
	setIntelDisabled(repoRoot)

	fmt.Println()
	fmt.Printf("  %s✅ Original agent config restored.%s\n", colorGreen+colorBold, colorReset)
	fmt.Printf("  Auto-activation disabled. Run %skindling intel on%s to re-enable.\n\n", colorCyan, colorReset)

	return nil
}

// restoreIntel restores backed-up agent config files and cleans up state.
func restoreIntel(repoRoot string, state *intelState) {
	// Restore backups
	for agentFile, backupRel := range state.Backups {
		backupPath := filepath.Join(repoRoot, backupRel)
		agentPath := filepath.Join(repoRoot, agentFile)

		data, err := os.ReadFile(backupPath)
		if err != nil {
			// Backup missing — just remove the kindling version
			os.Remove(agentPath)
			continue
		}

		os.WriteFile(agentPath, data, 0644)
	}

	// Remove agent files that had no backup (we created them fresh)
	for _, agentFile := range state.Written {
		if _, hasBackup := state.Backups[agentFile]; hasBackup {
			continue
		}
		os.Remove(filepath.Join(repoRoot, agentFile))
	}

	// Clean up backups and state
	os.RemoveAll(filepath.Join(repoRoot, ".kindling", "intel-backups"))
	os.Remove(filepath.Join(repoRoot, intelStateFile))
}

// setIntelDisabled writes the disabled flag file.
func setIntelDisabled(repoRoot string) {
	dir := filepath.Join(repoRoot, ".kindling")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(repoRoot, intelDisabledFile), []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)
}

// ── Status ──────────────────────────────────────────────────────

func runIntelStatus(cmd *cobra.Command, args []string) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	// Check disabled flag
	if _, err := os.Stat(filepath.Join(repoRoot, intelDisabledFile)); err == nil {
		fmt.Printf("  kindling intel is %sdisabled%s (auto-activation off).\n", colorDim, colorReset)
		fmt.Printf("  Run %skindling intel on%s to re-enable.\n", colorCyan, colorReset)
		return nil
	}

	state, _ := loadIntelState(repoRoot)
	if state != nil && state.Active {
		fmt.Printf("  %s⚡ kindling intel is active%s\n", colorGreen+colorBold, colorReset)

		// Show last interaction
		if ts := state.lastInteractionTime(); !ts.IsZero() {
			ago := time.Since(ts).Truncate(time.Second)
			fmt.Printf("  Last interaction: %s ago\n", ago)
		}

		fmt.Println()
		for _, f := range state.Written {
			_, hasBackup := state.Backups[f]
			if hasBackup {
				fmt.Printf("    %s%s%s (original backed up)\n", colorCyan, f, colorReset)
			} else {
				fmt.Printf("    %s%s%s (created new)\n", colorCyan, f, colorReset)
			}
		}
		fmt.Println()
		fmt.Printf("  Auto-restores after %s of inactivity.\n", intelSessionTimeout)
		fmt.Printf("  Run %skindling intel off%s to restore now.\n", colorCyan, colorReset)
	} else {
		fmt.Printf("  kindling intel is not active.\n")
		fmt.Printf("  It will activate automatically on the next kindling command.\n")
	}

	return nil
}

// ── State persistence ───────────────────────────────────────────

func loadIntelState(repoRoot string) (*intelState, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, intelStateFile))
	if err != nil {
		return nil, err
	}
	var s intelState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveIntelState(repoRoot string, s *intelState) error {
	dir := filepath.Dir(filepath.Join(repoRoot, intelStateFile))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(repoRoot, intelStateFile), data, 0644)
}

// ── Repo root detection ────────────────────────────────────────

// findRepoRoot walks up from cwd to find the nearest .git directory.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root — fall back to cwd
			cwd, _ := os.Getwd()
			return cwd, nil
		}
		dir = parent
	}
}

// ── Context document builder ────────────────────────────────────

func buildContextDocument(repoRoot string) string {
	var sb strings.Builder

	sb.WriteString("# Kindling — Coding Agent Context\n\n")
	sb.WriteString("This file was generated by `kindling intel on`. It gives your coding\n")
	sb.WriteString("agent the context it needs to work effectively in a kindling-powered\n")
	sb.WriteString("project. Run `kindling intel off` to restore your original config.\n\n")

	// ── Section 1: Architectural principles ──────────────────────
	sb.WriteString("## Architectural Principles\n\n")
	sb.WriteString("kindling is a Kubernetes operator that turns your laptop into a personal\n")
	sb.WriteString("CI/CD environment. Everything runs locally on a Kind cluster.\n\n")

	sb.WriteString("### How things work\n\n")
	sb.WriteString("- **Deploy with `kindling deploy`**, not `kubectl apply` or raw manifests.\n")
	sb.WriteString("- **Builds use Kaniko** inside a CI runner sidecar, NOT `docker build`.\n")
	sb.WriteString("  Never generate Dockerfiles or docker-compose for the dev environment.\n")
	sb.WriteString("- **Dependencies** (Postgres, Redis, etc.) go in `spec.dependencies[]`\n")
	sb.WriteString("  in the DSE YAML, not in Docker Compose or Helm charts.\n")
	sb.WriteString("- **The in-cluster registry** is `localhost:5001` — no DockerHub or ECR push needed.\n")
	sb.WriteString("- **Secrets** go through `kindling secrets set KEY=VALUE`, which creates K8s\n")
	sb.WriteString("  secrets referenced via `secretKeyRef` in the workflow. Never hardcode\n")
	sb.WriteString("  secrets in YAML or env files.\n")
	sb.WriteString("- **Environment variables** go through `kindling env set KEY=VALUE` or\n")
	sb.WriteString("  `spec.env[]` in the DSE YAML.\n")
	sb.WriteString("- **To expose a service externally**, use `kindling expose`, not raw Ingress.\n")
	sb.WriteString("- **To check status**, use `kindling status` and `kindling logs`,\n")
	sb.WriteString("  not raw `kubectl` commands.\n")
	sb.WriteString("- **Adding a new service**: add it to `spec.services[]` in the DSE YAML —\n")
	sb.WriteString("  the CI workflow will build and deploy it automatically.\n\n")

	sb.WriteString("### Dependency auto-injection\n\n")
	sb.WriteString("When a dependency is declared, the operator auto-injects connection URLs:\n\n")
	sb.WriteString("| Dependency | Auto-injected env var |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString("| postgres | `DATABASE_URL` |\n")
	sb.WriteString("| redis | `REDIS_URL` |\n")
	sb.WriteString("| mysql | `DATABASE_URL` |\n")
	sb.WriteString("| mongodb | `MONGO_URL` |\n")
	sb.WriteString("| rabbitmq | `AMQP_URL` |\n")
	sb.WriteString("| minio | `S3_ENDPOINT` |\n")
	sb.WriteString("| elasticsearch | `ELASTICSEARCH_URL` |\n")
	sb.WriteString("| kafka | `KAFKA_BROKER_URL` |\n")
	sb.WriteString("| nats | `NATS_URL` |\n")
	sb.WriteString("| memcached | `MEMCACHED_URL` |\n\n")
	sb.WriteString("**Never duplicate these in the env block** — they're already injected.\n\n")

	// ── Section 2: CLI reference card ────────────────────────────
	sb.WriteString("## CLI Reference\n\n")
	sb.WriteString("| Command | What it does |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString("| `kindling init` | Create Kind cluster + deploy operator |\n")
	sb.WriteString("| `kindling runners -u <user> -r <repo> -t <pat>` | Register a CI runner |\n")
	sb.WriteString("| `kindling generate -k <api-key> -r .` | AI-generate a dev-deploy workflow |\n")
	sb.WriteString("| `kindling deploy -f <dse.yaml>` | Deploy a staging environment |\n")
	sb.WriteString("| `kindling load -s <svc> --context .` | Build + load image into Kind |\n")
	sb.WriteString("| `kindling sync -d <deploy>` | Live-sync files into a running pod |\n")
	sb.WriteString("| `kindling push -s <svc>` | Git push, rebuild one service |\n")
	sb.WriteString("| `kindling env set KEY=VALUE` | Set an environment variable |\n")
	sb.WriteString("| `kindling secrets set KEY VALUE` | Store an external secret |\n")
	sb.WriteString("| `kindling expose` | Public HTTPS tunnel for OAuth |\n")
	sb.WriteString("| `kindling status` | View everything at a glance |\n")
	sb.WriteString("| `kindling logs` | Tail the controller logs |\n")
	sb.WriteString("| `kindling reset` | Remove runner pool, keep cluster |\n")
	sb.WriteString("| `kindling destroy` | Tear it all down |\n")
	sb.WriteString("| `kindling intel on/off` | Toggle this context file |\n\n")

	sb.WriteString("### Key files\n\n")
	sb.WriteString("| File | Purpose |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString("| `.github/workflows/dev-deploy.yml` | GitHub Actions CI workflow |\n")
	sb.WriteString("| `.gitlab-ci.yml` | GitLab CI workflow |\n")
	sb.WriteString("| `.kindling/dev-environment.yaml` | Environment spec (DSE) |\n")
	sb.WriteString("| `.kindling/context.md` | This context (canonical copy) |\n\n")

	sb.WriteString("### Secrets flow\n\n")
	sb.WriteString("`kindling secrets set NAME VALUE` → K8s Secret → `secretKeyRef` in workflow\n\n")

	sb.WriteString("### Build protocol\n\n")
	sb.WriteString("Source tarball → Kaniko (in runner sidecar) → `localhost:5001/<image>` → deploy\n\n")

	// ── Section 3: Project-specific context ──────────────────────
	sb.WriteString("## This Project\n\n")

	projectCtx := detectProjectContext(repoRoot)
	sb.WriteString(projectCtx)

	// ── Section 4: Kaniko compatibility ──────────────────────────
	sb.WriteString("\n## Kaniko Compatibility Notes\n\n")
	sb.WriteString("Builds use Kaniko, not Docker BuildKit. Key differences:\n\n")
	sb.WriteString("- **No BuildKit platform ARGs** (`TARGETARCH`, `BUILDPLATFORM`, etc.) — they'll be empty.\n")
	sb.WriteString("- **No `.git` directory** — Go builds need `-buildvcs=false`.\n")
	sb.WriteString("- **Poetry** must use `--no-root` flag.\n")
	sb.WriteString("- **npm** needs cache redirect: `ENV npm_config_cache=/tmp/.npm`\n")
	sb.WriteString("- `RUN --mount=type=cache` is ignored (safe, just no caching).\n\n")
	sb.WriteString("If modifying a Dockerfile, keep these constraints in mind.\n")

	return sb.String()
}

// detectProjectContext scans the repo for project-specific information
// and returns a markdown description.
func detectProjectContext(repoRoot string) string {
	var sb strings.Builder

	// Detect languages from dependency manifests
	var languages []string
	langChecks := map[string]string{
		"go.mod":           "Go",
		"package.json":     "JavaScript/TypeScript",
		"requirements.txt": "Python",
		"Pipfile":          "Python",
		"pyproject.toml":   "Python",
		"Cargo.toml":       "Rust",
		"pom.xml":          "Java",
		"build.gradle":     "Java/Kotlin",
		"build.gradle.kts": "Kotlin",
		"Gemfile":          "Ruby",
		"composer.json":    "PHP",
		"mix.exs":          "Elixir",
	}

	seen := make(map[string]bool)
	for file, lang := range langChecks {
		matches, _ := filepath.Glob(filepath.Join(repoRoot, "**", file))
		if len(matches) == 0 {
			// Try root level
			if _, err := os.Stat(filepath.Join(repoRoot, file)); err == nil {
				if !seen[lang] {
					languages = append(languages, lang)
					seen[lang] = true
				}
			}
		} else {
			if !seen[lang] {
				languages = append(languages, lang)
				seen[lang] = true
			}
		}
	}

	if len(languages) > 0 {
		sb.WriteString(fmt.Sprintf("**Languages detected:** %s\n\n", strings.Join(languages, ", ")))
	}

	// Count Dockerfiles
	var dockerfileCount int
	filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			name := ""
			if d != nil {
				name = d.Name()
			}
			if d != nil && d.IsDir() && scanSkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		nameLower := strings.ToLower(d.Name())
		if nameLower == "dockerfile" || strings.HasPrefix(nameLower, "dockerfile.") {
			dockerfileCount++
		}
		return nil
	})
	if dockerfileCount > 0 {
		sb.WriteString(fmt.Sprintf("**Dockerfiles found:** %d\n\n", dockerfileCount))
	}

	// Check for existing CI workflow
	ghWorkflow := filepath.Join(repoRoot, ".github", "workflows", "dev-deploy.yml")
	glWorkflow := filepath.Join(repoRoot, ".gitlab-ci.yml")
	if _, err := os.Stat(ghWorkflow); err == nil {
		sb.WriteString("**CI workflow:** `.github/workflows/dev-deploy.yml` (GitHub Actions)\n\n")
	} else if _, err := os.Stat(glWorkflow); err == nil {
		sb.WriteString("**CI workflow:** `.gitlab-ci.yml` (GitLab CI)\n\n")
	} else {
		sb.WriteString("**CI workflow:** Not yet generated. Run `kindling generate` to create one.\n\n")
	}

	// Check for DSE YAML
	dseGlobs := []string{
		filepath.Join(repoRoot, ".kindling", "dev-environment.yaml"),
		filepath.Join(repoRoot, "dev-environment.yaml"),
		filepath.Join(repoRoot, "*.yaml"),
	}
	for _, pattern := range dseGlobs[:2] {
		if _, err := os.Stat(pattern); err == nil {
			rel, _ := filepath.Rel(repoRoot, pattern)
			sb.WriteString(fmt.Sprintf("**Environment spec:** `%s`\n\n", rel))
			break
		}
	}

	// Check for docker-compose
	composeNames := []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}
	for _, name := range composeNames {
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err == nil {
			sb.WriteString(fmt.Sprintf("**Docker Compose:** `%s` (used as source of truth for multi-service detection)\n\n", name))
			break
		}
	}

	if sb.Len() == 0 {
		sb.WriteString("Run `kindling generate` to analyze this repo and generate a CI workflow.\n")
	}

	return sb.String()
}

// formatForAgent wraps the context document with agent-specific formatting.
func formatForAgent(agent agentTarget, contextDoc string) string {
	switch {
	case strings.Contains(agent.File, "copilot"):
		// Copilot instructions — just the raw markdown
		return contextDoc
	case strings.Contains(agent.File, "CLAUDE"):
		// Claude Code — just the raw markdown
		return contextDoc
	case strings.Contains(agent.File, "cursor"):
		// Cursor rules — use .mdc format with frontmatter
		var sb strings.Builder
		sb.WriteString("---\n")
		sb.WriteString("description: Kindling development context — architecture, CLI, and project setup\n")
		sb.WriteString("globs: \n")
		sb.WriteString("alwaysApply: true\n")
		sb.WriteString("---\n\n")
		sb.WriteString(contextDoc)
		return sb.String()
	case strings.Contains(agent.File, "windsurfrules"):
		// Windsurf — just the raw markdown
		return contextDoc
	default:
		return contextDoc
	}
}
