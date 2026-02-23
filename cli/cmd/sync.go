package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

// ════════════════════════════════════════════════════════════════════
// Runtime profiles — one entry per language / framework the sync
// command knows how to handle.
// ════════════════════════════════════════════════════════════════════

// restartMode describes how `--restart` brings up the new code.
type restartMode int

const (
	// modeKill — wrap the process in a restart loop, sync files, kill the
	// child PID so the loop respawns it with new code.
	// Used for: Node, Python, Ruby, Perl, Lua, Julia, R, Elixir (mix).
	modeKill restartMode = iota

	// modeSignal — send a signal (usually SIGHUP) to PID 1 so it
	// gracefully reloads.  No wrapper patch is needed.
	// Used for: uvicorn, gunicorn, nginx, puma.
	modeSignal

	// modeNone — the runtime re-reads source files on every request so
	// syncing files is sufficient; no restart is required.
	// Used for: PHP (mod_php / php-fpm), nodemon.
	modeNone

	// modeRebuild — the app is a compiled binary. Source-file syncing
	// alone is useless; we need to rebuild inside the container (or warn
	// the user to `kindling push`).
	// Used for: Go, Rust, Java, Kotlin, C#, C/C++, Zig, Crystal, Nim.
	modeRebuild
)

type runtimeProfile struct {
	Name          string        // Human-friendly label ("Node.js", "Python (uvicorn)")
	Mode          restartMode   // How to restart
	Signal        string        // Signal name for modeSignal (e.g. "HUP")
	BuildCmd      string        // In-container build command for modeRebuild
	LocalBuildFmt string        // Local cross-compile command (fmt template: %s=GOOS, %s=GOARCH, %s=output path)
	WaitAfter     time.Duration // Grace period after restart
	Interpreted   bool          // True if source-file sync alone is useful
}

// runtimeTable maps the process name (basename of PID 1) to a profile.
var runtimeTable = map[string]runtimeProfile{
	// ── Node.js ─────────────────────────────────────────────
	"node": {
		Name: "Node.js", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"deno": {
		Name: "Deno", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"bun": {
		Name: "Bun", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"nodemon": {
		Name: "Node.js (nodemon)", Mode: modeNone, Interpreted: true,
	},
	"ts-node": {
		Name: "TypeScript (ts-node)", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"tsx": {
		Name: "TypeScript (tsx)", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},

	// ── Python ──────────────────────────────────────────────
	"python": {
		Name: "Python", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"python3": {
		Name: "Python 3", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"uvicorn": {
		Name: "Python (uvicorn)", Mode: modeSignal, Signal: "HUP",
		Interpreted: true, WaitAfter: 1 * time.Second,
	},
	"gunicorn": {
		Name: "Python (gunicorn)", Mode: modeSignal, Signal: "HUP",
		Interpreted: true, WaitAfter: 2 * time.Second,
	},
	"flask": {
		Name: "Python (Flask)", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"celery": {
		Name: "Python (Celery)", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	},

	// ── Ruby ────────────────────────────────────────────────
	"ruby": {
		Name: "Ruby", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"rails": {
		Name: "Ruby on Rails", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	},
	"puma": {
		Name: "Ruby (Puma)", Mode: modeSignal, Signal: "USR2",
		Interpreted: true, WaitAfter: 2 * time.Second,
	},
	"unicorn": {
		Name: "Ruby (Unicorn)", Mode: modeSignal, Signal: "USR2",
		Interpreted: true, WaitAfter: 2 * time.Second,
	},
	"bundle": {
		Name: "Ruby (Bundler)", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	},

	// ── PHP ─────────────────────────────────────────────────
	"php": {
		Name: "PHP", Mode: modeNone, Interpreted: true,
	},
	"php-fpm": {
		Name: "PHP-FPM", Mode: modeNone, Interpreted: true,
	},
	"apache2": {
		Name: "Apache (PHP)", Mode: modeSignal, Signal: "USR1",
		Interpreted: true, WaitAfter: 1 * time.Second,
	},

	// ── Elixir / Erlang ─────────────────────────────────────
	"mix": {
		Name: "Elixir (Mix)", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	},
	"elixir": {
		Name: "Elixir", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	},
	"iex": {
		Name: "Elixir (IEx)", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},

	// ── Other interpreted ───────────────────────────────────
	"perl": {
		Name: "Perl", Mode: modeKill, Interpreted: true,
		WaitAfter: 1 * time.Second,
	},
	"lua": {
		Name: "Lua", Mode: modeKill, Interpreted: true,
		WaitAfter: 1 * time.Second,
	},
	"luajit": {
		Name: "LuaJIT", Mode: modeKill, Interpreted: true,
		WaitAfter: 1 * time.Second,
	},
	"Rscript": {
		Name: "R", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"R": {
		Name: "R", Mode: modeKill, Interpreted: true,
		WaitAfter: 2 * time.Second,
	},
	"julia": {
		Name: "Julia", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	},

	// ── Static / reverse proxy ──────────────────────────────
	"nginx": {
		Name: "Nginx", Mode: modeSignal, Signal: "HUP",
		Interpreted: false, WaitAfter: 1 * time.Second,
	},
	"caddy": {
		Name: "Caddy", Mode: modeSignal, Signal: "USR1",
		Interpreted: false, WaitAfter: 1 * time.Second,
	},

	// ── Compiled languages ──────────────────────────────────
	"go": {
		Name: "Go", Mode: modeRebuild, Interpreted: false,
		BuildCmd:      "go build -o /tmp/_kindling_bin . && cp /tmp/_kindling_bin /app/main",
		LocalBuildFmt: "CGO_ENABLED=0 GOOS=%s GOARCH=%s go build -o %s .",
		WaitAfter:     3 * time.Second,
	},
	"java": {
		Name: "Java", Mode: modeRebuild, Interpreted: false,
		WaitAfter: 5 * time.Second,
	},
	"kotlin": {
		Name: "Kotlin", Mode: modeRebuild, Interpreted: false,
		WaitAfter: 5 * time.Second,
	},
	"dotnet": {
		Name: ".NET", Mode: modeRebuild, Interpreted: false,
		BuildCmd:      "dotnet build -o /app/out",
		LocalBuildFmt: "dotnet publish -r linux-%s -o %s",
		WaitAfter:     4 * time.Second,
	},
	"cargo": {
		Name: "Rust (cargo)", Mode: modeRebuild, Interpreted: false,
		BuildCmd:      "cargo build --release",
		LocalBuildFmt: "cargo build --release --target %s-%s-unknown-linux-gnu",
		WaitAfter:     3 * time.Second,
	},
	"rustc": {
		Name: "Rust", Mode: modeRebuild, Interpreted: false,
		WaitAfter: 3 * time.Second,
	},
	"gcc": {
		Name: "C/C++", Mode: modeRebuild, Interpreted: false,
		WaitAfter: 2 * time.Second,
	},
	"zig": {
		Name: "Zig", Mode: modeRebuild, Interpreted: false,
		BuildCmd:  "zig build",
		WaitAfter: 2 * time.Second,
	},
}

// ════════════════════════════════════════════════════════════════════
// Cobra command definition
// ════════════════════════════════════════════════════════════════════

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Live-sync local files into a running pod",
	Long: `Watches a local directory for changes and copies updated files into
a running Kubernetes pod via kubectl cp — giving you hot-reload
without a full image rebuild.

The restart strategy is automatically chosen based on the detected
runtime:

  INTERPRETED (Node, Python, Ruby, Elixir, Perl, Lua, Julia, R):
    Files are synced and the process is restarted via a wrapper loop.

  SIGNAL-RELOAD (uvicorn, gunicorn, Puma, Nginx):
    Files are synced and a reload signal (SIGHUP/USR2) is sent —
    zero-downtime hot-reload.

  PHP (mod_php / php-fpm):
    Files are synced — no restart needed. PHP re-reads on every
    request.

  FRONTEND BUILD (React, Vue, Svelte, Angular + Nginx/Caddy):
    Auto-detected when Nginx/Caddy serves a project with a "build"
    script in package.json.  Runs the build locally, then syncs the
    built assets (dist/) into the container — no restart needed.

  COMPILED (Go, Rust, Java, C#, C/C++, Zig):
    Auto-detected: builds locally with cross-compilation, syncs the
    binary into the container, and restarts the process.
    Use --build-cmd / --build-output to override the build step.

Examples:
  # Sync current directory into the "orders" deployment at /app
  kindling sync -d orders

  # Sync with auto-detected restart strategy
  kindling sync -d orders --restart

  # One-shot sync + restart
  kindling sync -d orders --restart --once

  # Override language detection
  kindling sync -d orders --restart --language node

  # Go service — auto-detected local cross-compile
  kindling sync -d gateway --restart --language go

  # Custom build command for compiled languages
  kindling sync -d gateway --restart \
    --build-cmd 'CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/gateway .' \
    --build-output ./bin/gateway

  # Sync a specific source directory
  kindling sync -d orders --src ./services/orders

  # Sync into a custom container path and restart
  kindling sync -d orders --dest /opt/app/src --restart

  # Target a specific container in a multi-container pod
  kindling sync -d orders --container app --restart`,
	RunE: runSync,
}

var (
	syncDeployment  string
	syncContainer   string
	syncSrc         string
	syncDest        string
	syncNamespace   string
	syncRestart     bool
	syncOnce        bool
	syncExclude     []string
	syncDebounce    time.Duration
	syncLanguage    string
	syncBuildCmd    string
	syncBuildOutput string
)

// Default patterns to exclude from sync.
var defaultExcludes = []string{
	".git",
	"node_modules",
	".DS_Store",
	"__pycache__",
	"*.pyc",
	".venv",
	"vendor",
	".idea",
	".vscode",
	"target",     // Rust/Java
	"bin",        // Go/.NET
	"obj",        // .NET
	"_build",     // Elixir
	"deps",       // Elixir
	"*.class",    // Java
	"*.o",        // C/C++
	"*.so",       // shared objects
	".zig-cache", // Zig
	"dist",       // Frontend build output (Vite, Webpack)
	".next",      // Next.js build output
	"out",        // Static export output
}

func init() {
	syncCmd.Flags().StringVarP(&syncDeployment, "deployment", "d", "",
		"Target deployment name (required)")
	syncCmd.Flags().StringVar(&syncContainer, "container", "",
		"Container name (for multi-container pods)")
	syncCmd.Flags().StringVar(&syncSrc, "src", ".",
		"Local source directory to watch")
	syncCmd.Flags().StringVar(&syncDest, "dest", "/app",
		"Destination path inside the container")
	syncCmd.Flags().StringVarP(&syncNamespace, "namespace", "n", "default",
		"Kubernetes namespace")
	syncCmd.Flags().BoolVar(&syncRestart, "restart", false,
		"Restart the app process after each sync batch (strategy auto-detected)")
	syncCmd.Flags().BoolVar(&syncOnce, "once", false,
		"Sync once and exit (no file watching)")
	syncCmd.Flags().StringArrayVar(&syncExclude, "exclude", nil,
		"Additional patterns to exclude (repeatable)")
	syncCmd.Flags().DurationVar(&syncDebounce, "debounce", 500*time.Millisecond,
		"Debounce interval for batching rapid file changes")
	syncCmd.Flags().StringVar(&syncLanguage, "language", "",
		"Override auto-detected runtime (node, python, ruby, php, go, rust, java, dotnet, elixir, ...)")
	syncCmd.Flags().StringVar(&syncBuildCmd, "build-cmd", "",
		"Local build command for compiled languages (e.g. 'go build -o ./bin/app .')")
	syncCmd.Flags().StringVar(&syncBuildOutput, "build-output", "",
		"Path to built artifact to sync (e.g. './bin/app')")
	_ = syncCmd.MarkFlagRequired("deployment")
	rootCmd.AddCommand(syncCmd)
}

// ════════════════════════════════════════════════════════════════════
// Runtime detection
// ════════════════════════════════════════════════════════════════════

// detectRuntime reads PID 1's command line from the container and matches
// it against runtimeTable.  Returns the profile and the original command
// string (e.g. "node server.js").
func detectRuntime(pod, namespace, container string) (runtimeProfile, string) {
	defaultProfile := runtimeProfile{
		Name: "unknown", Mode: modeKill, Interpreted: true,
		WaitAfter: 3 * time.Second,
	}

	args := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--", "cat", "/proc/1/cmdline")
	raw, _ := runCapture("kubectl", args...)
	cmdline := strings.TrimSpace(strings.ReplaceAll(raw, "\x00", " "))

	if cmdline == "" {
		return defaultProfile, ""
	}

	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return defaultProfile, cmdline
	}

	// If it's already our wrapper (sh -c 'touch /tmp/.kindling-sync-wrapper ...'),
	// detect from the inner command instead.
	if strings.Contains(cmdline, ".kindling-sync-wrapper") {
		if idx := strings.Index(cmdline, "while true; do "); idx > 0 {
			inner := cmdline[idx+len("while true; do "):]
			if ampIdx := strings.Index(inner, " & PID"); ampIdx > 0 {
				inner = inner[:ampIdx]
			}
			innerFields := strings.Fields(inner)
			if len(innerFields) > 0 {
				proc := filepath.Base(innerFields[0])
				if p, ok := matchRuntime(proc, innerFields); ok {
					return p, inner
				}
			}
			return defaultProfile, inner
		}
	}

	proc := filepath.Base(fields[0])
	if p, ok := matchRuntime(proc, fields); ok {
		return p, cmdline
	}

	return runtimeProfile{
		Name: fmt.Sprintf("unknown (%s)", proc), Mode: modeKill,
		Interpreted: true, WaitAfter: 3 * time.Second,
	}, cmdline
}

// normalizeProcName normalises versioned runtime basenames:
//
//	python3.12 → python3, python3.11 → python3, ruby3.2 → ruby
//	nginx: → nginx  (from "nginx: master process" cmdline)
func normalizeProcName(proc string) string {
	// Strip trailing colon (nginx: master process → nginx)
	proc = strings.TrimSuffix(proc, ":")

	// Strip trailing version digits: python3.12 → python3, python3 → python3
	if strings.HasPrefix(proc, "python") {
		if strings.HasPrefix(proc, "python3") {
			return "python3"
		}
		return "python"
	}
	// Strip ".X.Y" suffix for any other runtime (e.g. ruby3.2)
	if idx := strings.Index(proc, "."); idx > 0 {
		return proc[:idx]
	}
	return proc
}

// isPythonProc returns true if the basename looks like any python variant.
func isPythonProc(proc string) bool {
	return proc == "python" || proc == "python3" ||
		strings.HasPrefix(proc, "python3.") ||
		strings.HasPrefix(proc, "python2.")
}

// matchRuntime tries to match a process name + full args against runtimeTable.
// It checks framework names in args first (e.g. "python -m uvicorn" → uvicorn),
// then falls back to the process basename.
func matchRuntime(proc string, fields []string) (runtimeProfile, bool) {
	norm := normalizeProcName(proc)

	// "python[3[.X]] -m <framework>" → match the framework (uvicorn, gunicorn, flask, celery)
	if isPythonProc(proc) && len(fields) >= 3 && fields[1] == "-m" {
		framework := filepath.Base(fields[2])
		if p, ok := runtimeTable[framework]; ok {
			return p, true
		}
	}

	// "python[3[.X]] /usr/local/bin/<framework>" → match the framework by arg basename
	if isPythonProc(proc) && len(fields) >= 2 {
		for _, arg := range fields[1:] {
			argBase := filepath.Base(arg)
			if p, ok := runtimeTable[argBase]; ok {
				return p, true
			}
		}
	}

	// "bundle exec <framework>" → match the framework (puma, rails, unicorn)
	if proc == "bundle" && len(fields) >= 3 && fields[1] == "exec" {
		framework := filepath.Base(fields[2])
		if p, ok := runtimeTable[framework]; ok {
			return p, true
		}
	}

	// "npx <tool>" → match the tool (ts-node, tsx, nodemon)
	if proc == "npx" && len(fields) >= 2 {
		framework := filepath.Base(fields[1])
		if p, ok := runtimeTable[framework]; ok {
			return p, true
		}
	}

	// "php artisan serve" → PHP (Laravel)
	if proc == "php" && len(fields) >= 2 && fields[1] == "artisan" {
		return runtimeTable["php"], true
	}

	// "dotnet run" or "dotnet <dll>"
	if proc == "dotnet" {
		if p, ok := runtimeTable["dotnet"]; ok {
			return p, true
		}
	}

	// Direct lookup by process basename
	if p, ok := runtimeTable[proc]; ok {
		return p, true
	}

	// Try normalized name (e.g. python3.12 → python3)
	if norm != proc {
		if p, ok := runtimeTable[norm]; ok {
			return p, true
		}
	}

	return runtimeProfile{}, false
}

// detectLanguageFromSource scans a local source directory for language marker
// files (go.mod, package.json, Cargo.toml, etc.) and returns the runtimeTable
// key if found.  Returns "" if no language marker is detected.
func detectLanguageFromSource(srcDir string) string {
	markers := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "cargo"},
		{"package.json", "node"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"build.gradle.kts", "kotlin"},
		{"requirements.txt", "python3"},
		{"setup.py", "python3"},
		{"pyproject.toml", "python3"},
		{"Gemfile", "ruby"},
		{"mix.exs", "elixir"},
		{"composer.json", "php"},
	}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(srcDir, m.file)); err == nil {
			return m.lang
		}
	}
	return ""
}

// ════════════════════════════════════════════════════════════════════
// Frontend build detection
// ════════════════════════════════════════════════════════════════════

// isFrontendProject checks if the source directory looks like a JavaScript/TypeScript
// frontend project with a build step (React, Vue, Svelte, Angular, etc.).
func isFrontendProject(srcDir string) bool {
	if srcDir == "" {
		return false
	}
	pkgPath := filepath.Join(srcDir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	_, hasBuild := pkg.Scripts["build"]
	return hasBuild
}

// detectPackageManager returns the package manager for a frontend project.
func detectPackageManager(srcDir string) string {
	if _, err := os.Stat(filepath.Join(srcDir, "pnpm-lock.yaml")); err == nil {
		return "pnpm"
	}
	if _, err := os.Stat(filepath.Join(srcDir, "yarn.lock")); err == nil {
		return "yarn"
	}
	return "npm"
}

// detectFrontendOutputDir returns the build output subdirectory for a frontend project.
func detectFrontendOutputDir(srcDir string) string {
	// Vite → dist/
	for _, f := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts"} {
		if _, err := os.Stat(filepath.Join(srcDir, f)); err == nil {
			return "dist"
		}
	}
	// Next.js → out/ (static export)
	for _, f := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		if _, err := os.Stat(filepath.Join(srcDir, f)); err == nil {
			return "out"
		}
	}
	// Angular → dist/
	if _, err := os.Stat(filepath.Join(srcDir, "angular.json")); err == nil {
		return "dist"
	}
	// SvelteKit → build/
	if _, err := os.Stat(filepath.Join(srcDir, "svelte.config.js")); err == nil {
		return "build"
	}
	// Default — check what exists after build, otherwise assume dist/
	if _, err := os.Stat(filepath.Join(srcDir, "dist")); err == nil {
		return "dist"
	}
	if _, err := os.Stat(filepath.Join(srcDir, "build")); err == nil {
		return "build"
	}
	return "dist"
}

// detectNginxHtmlRoot tries to determine the nginx document root from the
// container's configuration.  Falls back to /usr/share/nginx/html.
func detectNginxHtmlRoot(pod, namespace, container string) string {
	args := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--", "sh", "-c", `nginx -T 2>/dev/null | grep -m1 'root ' | awk '{print $2}' | tr -d ';'`)
	out, err := runCapture("kubectl", args...)
	if err == nil {
		root := strings.TrimSpace(out)
		if root != "" && strings.HasPrefix(root, "/") {
			return root
		}
	}
	return "/usr/share/nginx/html"
}

// restartViaFrontendBuild builds a frontend project locally and syncs the
// built assets into the container's static file directory.
// No process restart is needed — static file servers serve new content immediately.
func restartViaFrontendBuild(pod, namespace, container, srcDir string, profile runtimeProfile) (string, error) {
	pkgMgr := detectPackageManager(srcDir)
	outputDir := detectFrontendOutputDir(srcDir)

	step("\U0001f3d7\ufe0f", fmt.Sprintf("Frontend project detected — building with %s", pkgMgr))

	// Install dependencies if node_modules doesn't exist
	nmPath := filepath.Join(srcDir, "node_modules")
	if _, err := os.Stat(nmPath); err != nil {
		step("📦", "Installing dependencies...")
		installCmd := pkgMgr + " install"
		installExec := exec.Command("sh", "-c", installCmd)
		installExec.Dir = srcDir
		if out, err := installExec.CombinedOutput(); err != nil {
			warn(fmt.Sprintf("Dependency install failed:\n%s", strings.TrimSpace(string(out))))
			return pod, fmt.Errorf("dependency install failed: %w", err)
		}
		success("Dependencies installed")
	}

	// Build
	buildCmd := pkgMgr + " run build"
	step("🔨", fmt.Sprintf("Building: %s", buildCmd))
	buildExec := exec.Command("sh", "-c", buildCmd)
	buildExec.Dir = srcDir
	out, err := buildExec.CombinedOutput()
	if err != nil {
		warn(fmt.Sprintf("Build failed:\n%s", strings.TrimSpace(string(out))))
		return pod, fmt.Errorf("frontend build failed: %w", err)
	}
	success("Build complete")

	// Verify the build output exists
	absOutputDir := filepath.Join(srcDir, outputDir)
	if _, err := os.Stat(absOutputDir); err != nil {
		// Try alternative output dirs
		for _, alt := range []string{"dist", "build", "out"} {
			altPath := filepath.Join(srcDir, alt)
			if _, errAlt := os.Stat(altPath); errAlt == nil {
				absOutputDir = altPath
				outputDir = alt
				break
			}
		}
	}

	if _, err := os.Stat(absOutputDir); err != nil {
		return pod, fmt.Errorf("build output not found at %s — check your build configuration", absOutputDir)
	}

	// Detect the static file root in the container
	htmlRoot := detectNginxHtmlRoot(pod, namespace, container)

	// Sync the built output
	step("📦", fmt.Sprintf("Syncing %s/ → %s:%s", outputDir, pod, htmlRoot))
	if err := syncDir(pod, namespace, absOutputDir, htmlRoot, container); err != nil {
		return pod, fmt.Errorf("sync failed: %w", err)
	}

	success(fmt.Sprintf("Frontend assets deployed — %s serving new content immediately", profile.Name))
	return pod, nil
}

// resolveProfile returns the runtime profile to use — either from --language
// flag or auto-detected from the container.
func resolveProfile(pod, namespace, container, langOverride string) (runtimeProfile, string) {
	if langOverride != "" {
		if p, ok := runtimeTable[langOverride]; ok {
			step("🔧", fmt.Sprintf("Using language override: %s%s%s", colorCyan, p.Name, colorReset))
			// Still read the cmdline for display / original command
			_, cmdline := detectRuntime(pod, namespace, container)
			return p, cmdline
		}
		warn(fmt.Sprintf("Unknown language %q — falling back to auto-detect", langOverride))
	}

	return detectRuntime(pod, namespace, container)
}

// ════════════════════════════════════════════════════════════════════
// File-level helpers
// ════════════════════════════════════════════════════════════════════

// shouldExclude returns true if the relative path matches any exclude pattern.
func shouldExclude(relPath string, excludes []string) bool {
	parts := strings.Split(relPath, string(os.PathSeparator))
	for _, pattern := range excludes {
		for _, part := range parts {
			if matched, _ := filepath.Match(pattern, part); matched {
				return true
			}
		}
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
	}
	return false
}

// addWatchDirRecursive adds a directory and all its subdirectories to the watcher.
func addWatchDirRecursive(watcher *fsnotify.Watcher, root string, excludes []string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		if relPath != "." && shouldExclude(relPath, excludes) {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// ════════════════════════════════════════════════════════════════════
// Pod & deployment helpers
// ════════════════════════════════════════════════════════════════════

// findPodForDeployment returns the name of a running pod for the deployment.
func findPodForDeployment(deployment, namespace string) (string, error) {
	selectors := []string{
		fmt.Sprintf("app.kubernetes.io/name=%s", deployment),
		fmt.Sprintf("app=%s", deployment),
	}
	for _, sel := range selectors {
		out, err := runCapture("kubectl", "get", "pods",
			"-n", namespace,
			"-l", sel,
			"--field-selector=status.phase=Running",
			"-o", "jsonpath={.items[0].metadata.name}",
			"--context", "kind-"+clusterName,
		)
		if err == nil && strings.TrimSpace(out) != "" {
			return strings.TrimSpace(out), nil
		}
	}
	// Last resort: prefix match on pod names
	out, err := runCapture("kubectl", "get", "pods",
		"-n", namespace,
		"--field-selector=status.phase=Running",
		"-o", "jsonpath={.items[*].metadata.name}",
		"--context", "kind-"+clusterName,
	)
	if err == nil {
		for _, name := range strings.Fields(out) {
			if strings.HasPrefix(name, deployment+"-") {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("no running pod found for deployment %q in namespace %q", deployment, namespace)
}

// getDeploymentRevision returns the current revision annotation for a deployment.
// Used to snapshot the revision before sync so we can rollback on stop.
func getDeploymentRevision(deployment, namespace string) string {
	out, err := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName,
		"-o", "jsonpath={.metadata.annotations.deployment\\.kubernetes\\.io/revision}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// deploymentFromPod extracts the deployment name from a pod name.
// Pod name format: <deployment>-<rs-hash>-<pod-hash>
func deploymentFromPod(podName string) (string, error) {
	parts := strings.Split(podName, "-")
	if len(parts) < 3 {
		return "", fmt.Errorf("cannot determine deployment from pod name %q", podName)
	}
	return strings.Join(parts[:len(parts)-2], "-"), nil
}

// containerNameForDeployment returns the container name to use in patch operations.
func containerNameForDeployment(deployment, namespace, containerOverride string) string {
	if containerOverride != "" {
		return containerOverride
	}
	name, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName,
		"-o", "jsonpath={.spec.template.spec.containers[0].name}")
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return deployment
}

// ════════════════════════════════════════════════════════════════════
// Sync primitives
// ════════════════════════════════════════════════════════════════════

// syncFile copies a single file into the pod via kubectl cp.
func syncFile(pod, namespace, localPath, containerDest, container string) error {
	args := []string{"cp", localPath, fmt.Sprintf("%s:%s", pod, containerDest),
		"-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	_, err := runSilent("kubectl", args...)
	return err
}

// syncDir copies the contents of a local directory into a container path.
// Appends "/." to source so kubectl cp copies contents, not the directory itself.
func syncDir(pod, namespace, localDir, containerDest, container string) error {
	src := strings.TrimRight(localDir, "/") + "/."
	args := []string{"cp", src, fmt.Sprintf("%s:%s", pod, containerDest),
		"-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	_, err := runSilent("kubectl", args...)
	return err
}

// ════════════════════════════════════════════════════════════════════
// Restart strategies
// ════════════════════════════════════════════════════════════════════

// restartViaSignal sends a signal to PID 1 for graceful reload.
// Used by: uvicorn, gunicorn, Puma, Nginx, Apache, Caddy.
func restartViaSignal(pod, namespace, container, sig string) error {
	step("📡", fmt.Sprintf("Sending SIG%s to PID 1 for graceful reload", sig))
	args := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--", "kill", fmt.Sprintf("-%s", sig), "1")
	_, err := runSilent("kubectl", args...)
	return err
}

// patchDeploymentWrapper patches the deployment command to use a shell
// restart-loop wrapper.  Returns the new pod name after rollout.
func patchDeploymentWrapper(deployment, pod, namespace, container string) (string, error) {
	step("🔧", "Patching deployment with restart wrapper")

	origCmd := readContainerCommand(deployment, pod, namespace, container)
	if origCmd == "" {
		return pod, fmt.Errorf("cannot determine container command for deployment/%s", deployment)
	}
	step("📝", fmt.Sprintf("Original command: %s", origCmd))

	wrapperScript := fmt.Sprintf(
		`touch /tmp/.kindling-sync-wrapper && echo 1 > /tmp/.kindling-sync-wrapper && while true; do %s & PID=$!; echo $PID > /tmp/.kindling-app-pid; wait $PID; echo "Process exited, restarting..."; sleep 1; done`,
		origCmd)

	cName := containerNameForDeployment(deployment, namespace, container)
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"%s","command":["sh","-c","%s"]}]}}}}`,
		cName, strings.ReplaceAll(wrapperScript, `"`, `\"`))

	if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName,
		"--type=strategic", "-p", patch); err != nil {
		return pod, fmt.Errorf("failed to patch deployment: %w", err)
	}

	step("⏳", "Waiting for patched pod to roll out...")
	_ = run("kubectl", "rollout", "status", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName, "--timeout=90s")

	// Brief wait for old pod termination to avoid stale pod lookup
	time.Sleep(2 * time.Second)

	newPod, err := findPodForDeployment(deployment, namespace)
	if err != nil {
		return pod, err
	}
	step("🎯", fmt.Sprintf("New pod: %s", newPod))
	return newPod, nil
}

// killAppChild kills the app child process (not PID 1 sh) so the wrapper
// loop respawns it with the updated files.
func killAppChild(pod, namespace, container string) {
	args := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--", "sh", "-c", "kill $(cat /tmp/.kindling-app-pid) 2>/dev/null || true")
	_, _ = runSilent("kubectl", args...)
}

// isAlreadyPatched checks if the deployment has our wrapper marker file.
func isAlreadyPatched(pod, namespace string) bool {
	out, _ := runCapture("kubectl", "exec", pod, "-n", namespace,
		"--context", "kind-"+clusterName, "--", "cat", "/tmp/.kindling-sync-wrapper")
	return strings.TrimSpace(out) == "1"
}

// restartViaWrapper patches the deployment with a wrapper shell loop, syncs
// files, then kills the child process so the loop restarts it.
// Used by: Node, Python, Ruby, Perl, Lua, Elixir, etc.
func restartViaWrapper(pod, namespace, container, srcDir, dest string) (string, error) {
	deployment, err := deploymentFromPod(pod)
	if err != nil {
		return pod, err
	}

	if !isAlreadyPatched(pod, namespace) {
		newPod, err := patchDeploymentWrapper(deployment, pod, namespace, container)
		if err != nil {
			return pod, err
		}
		pod = newPod
	}

	// Sync files
	if srcDir != "" {
		step("📦", "Syncing files into container")
		if err := syncDir(pod, namespace, srcDir, dest, container); err != nil {
			return pod, fmt.Errorf("sync failed: %w", err)
		}

		step("🔄", "Restarting app process")
		killAppChild(pod, namespace, container)
	}

	return pod, nil
}

// restartViaRebuild builds locally (cross-compiled), syncs the binary into
// the container, and restarts via the wrapper loop.
// Used by: Go, Rust, Java, Kotlin, C#, C/C++, Zig.
func restartViaRebuild(pod, namespace, container, srcDir, dest string, profile runtimeProfile) (string, error) {
	deployment, err := deploymentFromPod(pod)
	if err != nil {
		return pod, err
	}

	// ── Determine build command and output path ────────────────
	buildCmd := syncBuildCmd
	buildOutput := syncBuildOutput

	if buildCmd == "" {
		// Auto-detect local build command
		buildCmd, buildOutput = autoLocalBuild(profile, srcDir)
	}

	if buildCmd == "" {
		// No local build possible — fall back to source sync with warning
		if srcDir != "" {
			step("📦", "Syncing source files into container (no build)")
			if err := syncDir(pod, namespace, srcDir, dest, container); err != nil {
				return pod, fmt.Errorf("sync failed: %w", err)
			}
		}
		fmt.Println()
		warn(fmt.Sprintf("%s is a compiled language — source files were synced but", profile.Name))
		warn("the running binary is unchanged.")
		fmt.Println()
		fmt.Printf("  %sOptions:%s\n", colorBold, colorReset)
		fmt.Printf("    1. Pass %s--build-cmd%s and %s--build-output%s for local cross-compilation\n", colorCyan, colorReset, colorCyan, colorReset)
		fmt.Printf("       e.g.: %s--build-cmd 'CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/app .' --build-output ./bin/app%s\n", colorCyan, colorReset)
		fmt.Printf("    2. Use %skindling push%s to rebuild + redeploy the full image\n", colorCyan, colorReset)
		fmt.Println()
		return pod, nil
	}

	if buildOutput == "" {
		return pod, fmt.Errorf("--build-output is required when using --build-cmd (path to the built binary)")
	}

	// ── Build locally ──────────────────────────────────────────
	step("🔨", fmt.Sprintf("Building locally: %s", buildCmd))
	buildExec := exec.Command("sh", "-c", buildCmd)
	buildExec.Dir = srcDir
	buildExec.Env = os.Environ() // inherit env; command itself sets GOOS/GOARCH
	out, err := buildExec.CombinedOutput()
	if err != nil {
		warn(fmt.Sprintf("Local build failed:\n%s", strings.TrimSpace(string(out))))
		return pod, fmt.Errorf("local build failed: %w", err)
	}
	success("Build complete")

	// Verify the binary exists
	absOutput := buildOutput
	if !filepath.IsAbs(absOutput) {
		absOutput = filepath.Join(srcDir, buildOutput)
	}
	if _, err := os.Stat(absOutput); err != nil {
		return pod, fmt.Errorf("build output not found at %s — check --build-output", absOutput)
	}

	// ── Handle distroless / scratch images ─────────────────────
	// These containers have no shell or tar, so kubectl cp and the
	// wrapper script won't work.  We inject a busybox init container
	// that copies sh/tar/etc into a shared volume, and apply the
	// wrapper at the same time (single rollout) using /debug-tools/sh.
	if !isAlreadyPatched(pod, namespace) && isDistroless(pod, namespace, container) {
		step("🐛", "Distroless image detected — injecting debug tools + wrapper")
		origCmd := readContainerCommand(deployment, pod, namespace, container)
		if origCmd == "" {
			return pod, fmt.Errorf("cannot determine container command for deployment/%s", deployment)
		}
		newPod, err := patchDistrolessWithWrapper(deployment, namespace, container, origCmd)
		if err != nil {
			return pod, fmt.Errorf("failed to patch distroless deployment: %w", err)
		}
		pod = newPod
	} else if !isAlreadyPatched(pod, namespace) {
		// Normal container — just apply the wrapper
		newPod, err := patchDeploymentWrapper(deployment, pod, namespace, container)
		if err != nil {
			return pod, err
		}
		pod = newPod
	}

	// ── Detect binary destination inside the container ─────────
	binDest := dest
	// Try to find the actual binary path.
	// If the wrapper is applied, extract the inner command name and resolve it.
	origCmd := readContainerCommand(deployment, pod, namespace, container)
	if origCmd != "" {
		innerBin := extractInnerBinaryFromWrapper(origCmd)
		if innerBin != "" {
			if strings.HasPrefix(innerBin, "/") {
				// Already an absolute path
				binDest = innerBin
			} else {
				// Resolve via `command -v` inside the container (more portable than `which`)
				resolveArgs := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
				if container != "" {
					resolveArgs = append(resolveArgs, "-c", container)
				}
				resolveArgs = append(resolveArgs, "--", "sh", "-c", fmt.Sprintf("command -v %s", innerBin))
				if resolved, err := runCapture("kubectl", resolveArgs...); err == nil && strings.TrimSpace(resolved) != "" {
					binDest = strings.TrimSpace(resolved)
				} else {
					// Last resort: assume binary is under dest dir
					binDest = filepath.Join(dest, innerBin)
				}
			}
		}
	}

	// ── Copy the binary into the container ─────────────────────
	step("📦", fmt.Sprintf("Syncing binary → %s:%s", pod, binDest))
	cpArgs := []string{"cp", absOutput, fmt.Sprintf("%s:%s", pod, binDest),
		"-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		cpArgs = append(cpArgs, "-c", container)
	}
	cpOut, err := runCapture("kubectl", cpArgs...)
	if err != nil {
		return pod, fmt.Errorf("failed to copy binary: %s", strings.TrimSpace(cpOut))
	}

	// Make it executable
	chmodArgs := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		chmodArgs = append(chmodArgs, "-c", container)
	}
	chmodArgs = append(chmodArgs, "--", "chmod", "+x", binDest)
	_, _ = runCapture("kubectl", chmodArgs...)

	// ── Restart via wrapper ────────────────────────────────────
	step("🔄", "Restarting with new binary")
	killAppChild(pod, namespace, container)

	time.Sleep(profile.WaitAfter)
	success(fmt.Sprintf("Rebuilt + restarted (%s)", profile.Name))
	return pod, nil
}

// isDistroless returns true if the container appears to be a distroless or
// scratch image (no shell available).
func isDistroless(pod, namespace, container string) bool {
	args := []string{"exec", pod, "-n", namespace, "--context", "kind-" + clusterName}
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--", "sh", "-c", "echo ok")
	out, err := runCapture("kubectl", args...)
	if err != nil || strings.TrimSpace(out) != "ok" {
		return true // no shell → distroless/scratch
	}
	return false
}

// patchDistrolessWithWrapper injects busybox debug tools AND the restart
// wrapper into a distroless deployment in a single patch (single rollout).
// The wrapper uses /debug-tools/sh (absolute path) since distroless images
// don't have sh in their default PATH.
func patchDistrolessWithWrapper(deployment, namespace, container, origCmd string) (string, error) {
	cName := containerNameForDeployment(deployment, namespace, container)

	step("📝", fmt.Sprintf("Original command: %s", origCmd))

	wrapperScript := fmt.Sprintf(
		`touch /tmp/.kindling-sync-wrapper && echo 1 > /tmp/.kindling-sync-wrapper && while true; do %s & PID=$!; echo $PID > /tmp/.kindling-app-pid; wait $PID; echo "Process exited, restarting..."; sleep 1; done`,
		origCmd)
	// Escape double quotes for JSON embedding
	escapedWrapper := strings.ReplaceAll(wrapperScript, `"`, `\"`)

	step("🔧", "Injecting debug tools + wrapper into distroless container")

	patch := fmt.Sprintf(`{
  "spec": {
    "template": {
      "spec": {
        "initContainers": [{
          "name": "kindling-debug-init",
          "image": "busybox:stable-musl",
          "command": ["sh", "-c", "for cmd in sh tar cat kill chmod echo touch sleep ls; do cp /bin/busybox /debug-tools/$cmd; done"],
          "volumeMounts": [{"name": "debug-tools", "mountPath": "/debug-tools"}]
        }],
        "containers": [{
          "name": "%s",
          "command": ["/debug-tools/sh", "-c", "%s"],
          "env": [{"name": "PATH", "value": "/debug-tools:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}],
          "volumeMounts": [{"name": "debug-tools", "mountPath": "/debug-tools"}]
        }],
        "volumes": [{"name": "debug-tools", "emptyDir": {}}]
      }
    }
  }
}`, cName, escapedWrapper)

	if err := run("kubectl", "patch", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName,
		"--type=strategic", "-p", patch); err != nil {
		return "", fmt.Errorf("failed to patch distroless deployment: %w", err)
	}

	step("⏳", "Waiting for patched pod to roll out...")
	_ = run("kubectl", "rollout", "status", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName, "--timeout=90s")

	// Brief wait for old pod termination to avoid stale pod lookup
	time.Sleep(2 * time.Second)

	newPod, err := findPodForDeployment(deployment, namespace)
	if err != nil {
		return "", err
	}
	step("🎯", fmt.Sprintf("New pod: %s", newPod))
	return newPod, nil
}

// ── Local build helpers ────────────────────────────────────────────

// detectNodeArch returns (GOOS, GOARCH) of the Kind cluster's node.
func detectNodeArch() (string, string) {
	goos, _ := runCapture("kubectl", "get", "nodes", "--context", "kind-"+clusterName,
		"-o", "jsonpath={.items[0].status.nodeInfo.operatingSystem}")
	goarch, _ := runCapture("kubectl", "get", "nodes", "--context", "kind-"+clusterName,
		"-o", "jsonpath={.items[0].status.nodeInfo.architecture}")
	goos = strings.TrimSpace(goos)
	goarch = strings.TrimSpace(goarch)
	if goos == "" {
		goos = "linux"
	}
	if goarch == "" {
		goarch = runtime.GOARCH // best guess from host
	}
	return goos, goarch
}

// autoLocalBuild returns a (buildCmd, outputPath) pair for known compiled
// languages.  Returns ("", "") if the language isn't auto-detectable.
func autoLocalBuild(profile runtimeProfile, srcDir string) (string, string) {
	goos, goarch := detectNodeArch()

	switch profile.Name {
	case "Go":
		outPath := filepath.Join(os.TempDir(), "_kindling_go_bin")
		cmd := fmt.Sprintf("CGO_ENABLED=0 GOOS=%s GOARCH=%s go build -o %s .", goos, goarch, outPath)
		// Check if go.mod exists to validate it's a Go project
		if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err == nil {
			return cmd, outPath
		}
		// Also check for .go files directly
		matches, _ := filepath.Glob(filepath.Join(srcDir, "*.go"))
		if len(matches) > 0 {
			return cmd, outPath
		}
		return "", ""

	case "Rust", "Rust (cargo)":
		target := fmt.Sprintf("%s-unknown-%s-gnu", goarchToRust(goarch), goos)
		outPath := filepath.Join(srcDir, "target", target, "release")
		cmd := fmt.Sprintf("cargo build --release --target %s", target)
		if _, err := os.Stat(filepath.Join(srcDir, "Cargo.toml")); err == nil {
			return cmd, outPath
		}
		return "", ""

	case ".NET":
		rid := fmt.Sprintf("%s-%s", goos, goarchToDotnet(goarch))
		outDir := filepath.Join(os.TempDir(), "_kindling_dotnet_out")
		cmd := fmt.Sprintf("dotnet publish -r %s -c Release -o %s --self-contained", rid, outDir)
		if _, err := os.Stat(filepath.Join(srcDir, "*.csproj")); err == nil {
			return cmd, outDir
		}
		return "", ""

	case "Java":
		gradleCmd := "gradle"
		if _, err := os.Stat(filepath.Join(srcDir, "gradlew")); err == nil {
			gradleCmd = "./gradlew"
		}
		if _, err := os.Stat(filepath.Join(srcDir, "pom.xml")); err == nil {
			outJar := filepath.Join(srcDir, "target", "app.jar")
			return "mvn package -DskipTests -q", outJar
		}
		if _, err := os.Stat(filepath.Join(srcDir, "build.gradle")); err == nil {
			outDir := filepath.Join(srcDir, "build", "install")
			return fmt.Sprintf("%s installDist -x test -q", gradleCmd), outDir
		}
		return "", ""

	case "Kotlin":
		gradleCmd := "gradle"
		if _, err := os.Stat(filepath.Join(srcDir, "gradlew")); err == nil {
			gradleCmd = "./gradlew"
		}
		if _, err := os.Stat(filepath.Join(srcDir, "build.gradle.kts")); err == nil {
			outDir := filepath.Join(srcDir, "build", "install")
			return fmt.Sprintf("%s installDist -x test -q", gradleCmd), outDir
		}
		return "", ""

	case "C/C++":
		if _, err := os.Stat(filepath.Join(srcDir, "Makefile")); err == nil {
			return "make", ""
		}
		if _, err := os.Stat(filepath.Join(srcDir, "CMakeLists.txt")); err == nil {
			return "cmake --build build", ""
		}
		return "", ""

	case "Zig":
		outPath := filepath.Join(srcDir, "zig-out", "bin")
		if _, err := os.Stat(filepath.Join(srcDir, "build.zig")); err == nil {
			return "zig build", outPath
		}
		return "", ""
	}

	return "", ""
}

// goarchToRust maps Go arch names to Rust target triples.
func goarchToRust(goarch string) string {
	switch goarch {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	default:
		return goarch
	}
}

// goarchToDotnet maps Go arch names to .NET RID arch names.
func goarchToDotnet(goarch string) string {
	switch goarch {
	case "arm64":
		return "arm64"
	case "amd64":
		return "x64"
	default:
		return goarch
	}
}

// ════════════════════════════════════════════════════════════════════
// Unified sync + restart dispatcher
// ════════════════════════════════════════════════════════════════════

// syncAndRestart detects the runtime and routes to the appropriate restart
// strategy.  Returns the (possibly new) pod name.
func syncAndRestart(pod, namespace, container, srcDir, dest string, excludes []string) (string, error) {
	profile, cmdline := resolveProfile(pod, namespace, container, syncLanguage)

	// If runtime detection returned "unknown" and we have a source directory,
	// fall back to scanning local source files for language markers.
	// This is critical for distroless/scratch containers where /proc/1/cmdline
	// returns an opaque binary name like "/src/server".
	if strings.HasPrefix(profile.Name, "unknown") && srcDir != "" {
		if detected := detectLanguageFromSource(srcDir); detected != "" {
			if p, ok := runtimeTable[detected]; ok {
				step("🔍", fmt.Sprintf("Auto-detected language from source: %s%s%s", colorCyan, p.Name, colorReset))
				profile = p
			}
		}
	}

	// ── Frontend build detection ────────────────────────────────
	// If the runtime is a static file server (Nginx, Caddy) and the source
	// directory is a frontend project with a build step, run the build locally
	// and sync the built assets instead of raw source files.
	if srcDir != "" && profile.Mode == modeSignal && !profile.Interpreted && isFrontendProject(srcDir) {
		step("🔍", fmt.Sprintf("Detected runtime: %s%s + Frontend Build%s  →  strategy: %slocal build + asset sync%s",
			colorCyan, profile.Name, colorReset,
			colorGreen, colorReset))
		if cmdline != "" {
			step("📝", fmt.Sprintf("Process: %s", cmdline))
		}
		return restartViaFrontendBuild(pod, namespace, container, srcDir, profile)
	}

	// Print detected runtime info
	modeLabel := ""
	switch profile.Mode {
	case modeKill:
		modeLabel = "wrapper + kill"
	case modeSignal:
		modeLabel = fmt.Sprintf("signal (SIG%s)", profile.Signal)
	case modeNone:
		modeLabel = "no restart needed"
	case modeRebuild:
		modeLabel = "local build + binary sync"
	}
	step("🔍", fmt.Sprintf("Detected runtime: %s%s%s  →  strategy: %s%s%s",
		colorCyan, profile.Name, colorReset,
		colorGreen, modeLabel, colorReset))
	if cmdline != "" {
		step("📝", fmt.Sprintf("Process: %s", cmdline))
	}

	switch profile.Mode {
	case modeNone:
		// PHP, nodemon — just sync, no restart needed
		if srcDir != "" {
			step("📦", "Syncing files (no restart needed — runtime reloads automatically)")
			if err := syncDir(pod, namespace, srcDir, dest, container); err != nil {
				return pod, fmt.Errorf("sync failed: %w", err)
			}
			success(fmt.Sprintf("Files synced — %s will pick them up automatically", profile.Name))
		}
		return pod, nil

	case modeSignal:
		// uvicorn, gunicorn, puma, nginx — sync then send reload signal
		if srcDir != "" {
			step("📦", "Syncing files into container")
			if err := syncDir(pod, namespace, srcDir, dest, container); err != nil {
				return pod, fmt.Errorf("sync failed: %w", err)
			}
		}
		if err := restartViaSignal(pod, namespace, container, profile.Signal); err != nil {
			warn(fmt.Sprintf("Signal reload failed: %v — falling back to wrapper restart", err))
			return restartViaWrapper(pod, namespace, container, srcDir, dest)
		}
		time.Sleep(profile.WaitAfter)
		success(fmt.Sprintf("Graceful reload complete (%s)", profile.Name))
		return pod, nil

	case modeRebuild:
		// Compiled languages — sync + rebuild
		newPod, err := restartViaRebuild(pod, namespace, container, srcDir, dest, profile)
		if err != nil {
			return newPod, err
		}
		time.Sleep(profile.WaitAfter)
		return newPod, nil

	default:
		// modeKill — interpreted languages, default path
		newPod, err := restartViaWrapper(pod, namespace, container, srcDir, dest)
		if err != nil {
			return newPod, err
		}
		time.Sleep(profile.WaitAfter)
		success(fmt.Sprintf("App restarted with new code (%s)", profile.Name))
		return newPod, nil
	}
}

// restartContainer is a convenience wrapper used by the dashboard API.
func restartContainer(pod, namespace, container string) error {
	_, err := syncAndRestart(pod, namespace, container, "", "", nil)
	return err
}

// ════════════════════════════════════════════════════════════════════
// Command helpers
// ════════════════════════════════════════════════════════════════════

// readContainerCommand returns the original entrypoint/cmd for the deployment.
func readContainerCommand(deployment, pod, namespace, container string) string {
	currentCmd, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName,
		"-o", "jsonpath={.spec.template.spec.containers[0].command}")
	currentArgs, _ := runCapture("kubectl", "get", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", "kind-"+clusterName,
		"-o", "jsonpath={.spec.template.spec.containers[0].args}")

	if strings.TrimSpace(currentCmd) != "" && currentCmd != "[]" {
		return parseJSONStringArray(currentCmd)
	}
	if strings.TrimSpace(currentArgs) != "" && currentArgs != "[]" {
		return parseJSONStringArray(currentArgs)
	}

	// Fall back: read PID 1's cmdline
	cmdline, _ := runCapture("kubectl", "exec", pod, "-n", namespace,
		"--context", "kind-"+clusterName, "--",
		"cat", "/proc/1/cmdline")
	if trimmed := strings.TrimSpace(strings.ReplaceAll(cmdline, "\x00", " ")); trimmed != "" {
		return trimmed
	}

	// Fall back: read entrypoint from image via crictl on the Kind node
	// (needed for distroless / scratch images where exec is not possible)
	cName := container
	if cName == "" {
		cName = containerNameForDeployment(deployment, namespace, "")
	}
	cID, _ := runCapture("docker", "exec", clusterName+"-control-plane",
		"crictl", "ps", "--name", cName, "-q")
	cID = strings.TrimSpace(cID)
	if cID != "" {
		// crictl inspect outputs JSON; extract args (entrypoint) via grep
		inspectOut, _ := runCapture("docker", "exec", clusterName+"-control-plane",
			"crictl", "inspect", cID)
		// Parse the "args" field from the process info — it's the first "args" in the JSON
		for _, line := range strings.Split(inspectOut, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, `"args"`) {
				// next lines have the actual args array; let's use a simpler approach
				break
			}
		}
		// Use crictl inspect with jsonpath-like grep for the entrypoint
		argsOut, _ := runCapture("docker", "exec", clusterName+"-control-plane",
			"sh", "-c", fmt.Sprintf(`crictl inspect %s | grep -A1 '"args"' | tail -1 | tr -d ' "[],'`, cID))
		if ep := strings.TrimSpace(argsOut); ep != "" {
			return ep
		}
	}

	return ""
}

// extractInnerBinaryFromWrapper pulls the actual app command name out of the
// kindling restart wrapper.  The wrapper looks like:
//
//	sh -c touch /tmp/.kindling-sync-wrapper && ... while true; do <CMD> & PID=$!; ...
//
// We want <CMD>'s first token (the binary name/path).
// Note: the deployment JSON may encode & as \u0026, so we handle both.
func extractInnerBinaryFromWrapper(cmd string) string {
	// Normalize JSON unicode escapes
	normalized := strings.ReplaceAll(cmd, `\u0026`, `&`)
	normalized = strings.ReplaceAll(normalized, `\u003e`, `>`)

	// Look for "while true; do " — everything between that and " &" is the inner cmd.
	const marker = "while true; do "
	idx := strings.Index(normalized, marker)
	if idx < 0 {
		// Not a wrapper — return the first token of the command
		fields := strings.Fields(cmd)
		if len(fields) > 0 {
			return fields[0]
		}
		return ""
	}
	rest := normalized[idx+len(marker):]
	// The inner command ends at " & PID=$!" or " &"
	if ampIdx := strings.Index(rest, " &"); ampIdx > 0 {
		inner := strings.TrimSpace(rest[:ampIdx])
		fields := strings.Fields(inner)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// parseJSONStringArray converts ["node","server.js"] → "node server.js".
func parseJSONStringArray(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return ""
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			result = append(result, p)
		}
	}
	return strings.Join(result, " ")
}

// ════════════════════════════════════════════════════════════════════
// Main command entry point
// ════════════════════════════════════════════════════════════════════

func runSync(cmd *cobra.Command, args []string) error {
	// ── Validate ────────────────────────────────────────────────
	deployment := strings.TrimSpace(syncDeployment)
	if deployment == "" {
		return fmt.Errorf("--deployment is required")
	}

	srcDir, err := filepath.Abs(syncSrc)
	if err != nil {
		return fmt.Errorf("cannot resolve source path: %w", err)
	}
	if info, err := os.Stat(srcDir); err != nil || !info.IsDir() {
		return fmt.Errorf("source directory does not exist: %s", srcDir)
	}

	if !clusterExists(clusterName) {
		return fmt.Errorf("Kind cluster %q not found — run: kindling init", clusterName)
	}

	// Build exclude list
	excludes := append([]string{}, defaultExcludes...)
	excludes = append(excludes, syncExclude...)

	// ── Find target pod ─────────────────────────────────────────
	header("Sync")
	step("🔍", fmt.Sprintf("Finding pod for deployment/%s", deployment))

	pod, err := findPodForDeployment(deployment, syncNamespace)
	if err != nil {
		return err
	}
	success(fmt.Sprintf("Target pod: %s", pod))

	// ── Detect runtime (quiet — for display only; syncAndRestart will print details) ──
	profile, _ := detectRuntime(pod, syncNamespace, syncContainer)
	frontendMode := profile.Mode == modeSignal && !profile.Interpreted && isFrontendProject(srcDir)

	// ── Initial sync ────────────────────────────────────────────
	if syncRestart {
		newPod, syncErr := syncAndRestart(pod, syncNamespace, syncContainer, srcDir, syncDest, excludes)
		if syncErr != nil {
			return fmt.Errorf("sync+restart failed: %w", syncErr)
		}
		pod = newPod
		// Re-discover in case of rollout
		pod, err = findPodForDeployment(deployment, syncNamespace)
		if err != nil {
			return err
		}
	} else {
		step("📦", fmt.Sprintf("Syncing %s → %s:%s", srcDir, pod, syncDest))
		if err := syncDir(pod, syncNamespace, srcDir, syncDest, syncContainer); err != nil {
			return fmt.Errorf("initial sync failed: %w", err)
		}
		success("Initial sync complete")
		printSyncOnlyTips(profile)
	}

	// ── One-shot mode ───────────────────────────────────────────
	if syncOnce {
		fmt.Println()
		fmt.Printf("  %s✅ Sync complete%s\n", colorGreen, colorReset)
		fmt.Println()
		return nil
	}

	// ── Watch mode ──────────────────────────────────────────────
	header("Watching for changes")
	fmt.Printf("  📂  %s\n", srcDir)
	if frontendMode {
		htmlRoot := detectNginxHtmlRoot(pod, syncNamespace, syncContainer)
		fmt.Printf("  🎯  %s:%s\n", pod, htmlRoot)
		fmt.Printf("  🌐  Runtime: %s%s + Frontend Build%s\n", colorCyan, profile.Name, colorReset)
	} else {
		fmt.Printf("  🎯  %s:%s\n", pod, syncDest)
		fmt.Printf("  🌐  Runtime: %s%s%s\n", colorCyan, profile.Name, colorReset)
	}
	fmt.Printf("  ⏱️   Debounce: %s\n", syncDebounce)
	if syncRestart {
		modeDesc := "wrapper + kill"
		if frontendMode {
			modeDesc = "local build + asset sync"
		} else {
			switch profile.Mode {
			case modeSignal:
				modeDesc = fmt.Sprintf("SIG%s reload", profile.Signal)
			case modeNone:
				modeDesc = "auto-reload (no restart)"
			case modeRebuild:
				modeDesc = "local build + binary sync"
			}
		}
		fmt.Printf("  🔄  Restart: %s%s%s\n", colorGreen, modeDesc, colorReset)
	}
	fmt.Printf("\n  %sPress Ctrl+C to stop%s\n\n", colorDim, colorReset)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("cannot create file watcher: %w", err)
	}
	defer watcher.Close()

	if err := addWatchDirRecursive(watcher, srcDir, excludes); err != nil {
		return fmt.Errorf("cannot watch directory tree: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	var debounceTimer *time.Timer
	pendingFiles := make(map[string]bool)

	flushSync := func() {
		if len(pendingFiles) == 0 {
			return
		}

		currentPod, err := findPodForDeployment(deployment, syncNamespace)
		if err != nil {
			warn(fmt.Sprintf("Pod lookup failed: %v — retrying next change", err))
			pendingFiles = make(map[string]bool)
			return
		}
		if currentPod != pod {
			pod = currentPod
			step("🔄", fmt.Sprintf("Pod changed → %s", pod))
		}

		fileList := make([]string, 0, len(pendingFiles))
		for f := range pendingFiles {
			fileList = append(fileList, f)
		}
		pendingFiles = make(map[string]bool)

		count := len(fileList)
		ts := time.Now().Format("15:04:05")

		if count <= 3 {
			for _, f := range fileList {
				rel, _ := filepath.Rel(srcDir, f)
				fmt.Printf("  %s[%s]%s  ↑ %s\n", colorDim, ts, colorReset, rel)
			}
		} else {
			fmt.Printf("  %s[%s]%s  ↑ %d files changed\n", colorDim, ts, colorReset, count)
		}

		// For frontend builds, skip individual file sync — the full build
		// + asset sync in syncAndRestart handles everything.
		if frontendMode && syncRestart {
			newPod, err := syncAndRestart(pod, syncNamespace, syncContainer, srcDir, syncDest, excludes)
			if err != nil {
				warn(fmt.Sprintf("Build failed: %v", err))
			} else {
				pod = newPod
			}
		} else {
			var syncErrors int
			for _, localPath := range fileList {
				relPath, _ := filepath.Rel(srcDir, localPath)
				destPath := filepath.Join(syncDest, relPath)
				destPath = strings.ReplaceAll(destPath, "\\", "/")

				if err := syncFile(pod, syncNamespace, localPath, destPath, syncContainer); err != nil {
					syncErrors++
					if syncErrors <= 3 {
						warn(fmt.Sprintf("  %s: %v", relPath, err))
					}
				}
			}

			if syncErrors > 0 {
				warn(fmt.Sprintf("%d/%d files failed to sync", syncErrors, count))
			} else {
				fmt.Printf("  %s✓ %d file(s) synced%s\n", colorGreen, count, colorReset)
			}

			if syncRestart {
				newPod, err := syncAndRestart(pod, syncNamespace, syncContainer, srcDir, syncDest, excludes)
				if err != nil {
					warn(fmt.Sprintf("Restart failed: %v", err))
				} else {
					pod = newPod
				}
			}
		}
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			relPath, _ := filepath.Rel(srcDir, event.Name)
			if shouldExclude(relPath, excludes) {
				continue
			}

			if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
				if event.Has(fsnotify.Create) {
					_ = addWatchDirRecursive(watcher, event.Name, excludes)
				}
				continue
			}

			pendingFiles[event.Name] = true

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(syncDebounce, flushSync)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			warn(fmt.Sprintf("Watch error: %v", err))

		case <-sigCh:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			flushSync()
			fmt.Printf("\n  %s👋 Sync stopped%s\n\n", colorCyan, colorReset)
			return nil
		}
	}
}

// printSyncOnlyTips prints language-specific advice when syncing without --restart.
func printSyncOnlyTips(profile runtimeProfile) {
	switch profile.Mode {
	case modeNone:
		success(fmt.Sprintf("%s re-reads files automatically — no --restart needed", profile.Name))
	case modeSignal:
		fmt.Printf("  %s💡 Tip: add --restart for zero-downtime %s reload via SIG%s%s\n",
			colorDim, profile.Name, profile.Signal, colorReset)
	case modeRebuild:
		fmt.Printf("  %s💡 %s is compiled — files synced but binary unchanged.%s\n",
			colorDim, profile.Name, colorReset)
		fmt.Printf("  %s   Use --restart to attempt in-container rebuild, or kindling push for full rebuild.%s\n",
			colorDim, colorReset)
	default:
		fmt.Printf("  %s💡 Tip: add --restart to automatically restart %s after each sync%s\n",
			colorDim, profile.Name, colorReset)
	}
}
