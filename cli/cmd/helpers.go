package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jeffvincent/kindling/pkg/ci"
)

// ── ANSI colours ────────────────────────────────────────────────
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

// ── Shared helpers ──────────────────────────────────────────────

// kindContext returns the kubectl context string for the active Kind cluster.
func kindContext() string {
	return "kind-" + clusterName
}

// labelSession labels a deployment with kindling.dev/mode and kindling.dev/runtime
// on the Deployment metadata (NOT the pod template) so it doesn't trigger a rollout.
// These labels let `kindling status` and kubectl queries discover active sessions.
func labelSession(deployment, namespace, mode, runtime string) {
	_ = run("kubectl", "label", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(), "--overwrite",
		"kindling.dev/mode="+mode,
		"kindling.dev/runtime="+runtime)
}

// unlabelSession removes the kindling.dev/mode and kindling.dev/runtime labels.
func unlabelSession(deployment, namespace string) {
	_ = run("kubectl", "label", fmt.Sprintf("deployment/%s", deployment),
		"-n", namespace, "--context", kindContext(),
		"kindling.dev/mode-",
		"kindling.dev/runtime-")
}

// resolveProvider returns the CI provider for the given name, or the default
// if name is empty. This centralises the 5-line pattern that was duplicated
// across runners, reset, status, generate, and the dashboard API.
func resolveProvider(name string) (ci.Provider, error) {
	if name == "" {
		return ci.Default(), nil
	}
	p, err := ci.Get(name)
	if err != nil {
		return nil, fmt.Errorf("unknown provider %q (available: github, gitlab)", name)
	}
	return p, nil
}

// skipDirNames is the canonical list of directories that should be skipped
// during repo scanning (generate, intel, sync, etc.). Individual features
// can extend this with their own entries or file-pattern globs.
var skipDirNames = []string{
	".git", "node_modules", "vendor", "__pycache__", ".venv", "venv", "env",
	".tox", ".mypy_cache", ".ruff_cache", "dist", "build", ".next", ".nuxt",
	".svelte-kit", "target", ".terraform", ".idea", ".vscode", ".github",
	"bin", "obj", "_output", ".cache", "_build", "deps", "zig-cache", "zig-out",
	".gradle", ".m2", ".elixir_ls", "coverage", ".nyc_output", "htmlcov",
}

// skipDirSet returns skipDirNames as a map for O(1) lookup.
func skipDirSet() map[string]bool {
	m := make(map[string]bool, len(skipDirNames))
	for _, d := range skipDirNames {
		m[d] = true
	}
	return m
}

// ── Pretty-print helpers ────────────────────────────────────────

func header(msg string) {
	fmt.Fprintf(os.Stderr, "\n%s%s▸ %s%s\n", colorBold, colorCyan, msg, colorReset)
}

func step(emoji, msg string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", emoji, msg)
}

func success(msg string) {
	fmt.Fprintf(os.Stderr, "  %s✅ %s%s\n", colorGreen, msg, colorReset)
}

func warn(msg string) {
	fmt.Fprintf(os.Stderr, "  %s⚠️  %s%s\n", colorYellow, msg, colorReset)
}

func fail(msg string) {
	fmt.Printf("  %s❌ %s%s\n", colorRed, msg, colorReset)
}

func dimText(msg string) string {
	return fmt.Sprintf("%s%s%s", colorDim, msg, colorReset)
}

// ── Command execution helpers ───────────────────────────────────

// run executes a command, streaming stdout/stderr to the terminal.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runDir executes a command in a specific directory.
func runDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runSilent executes a command and returns combined output.
func runSilent(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// runCapture executes a command and returns stdout only.
func runCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil && stderr.Len() > 0 {
		// Include stderr in the output so callers see the actual error message
		errMsg := strings.TrimSpace(stderr.String())
		if out == "" {
			out = errMsg
		} else {
			out = out + "\n" + errMsg
		}
	}
	return out, err
}

// commandExists checks if a binary is on PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// fileExists returns true if path exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// resolveProjectDir returns the project directory.
//
// Resolution order:
//  1. Explicit --project-dir flag
//  2. Current working directory (if kind-config.yaml exists there)
//  3. ~/.kindling (auto-cloned from GitHub if missing)
func resolveProjectDir() (string, error) {
	// 1. Explicit flag
	if projectDir != "" {
		return projectDir, nil
	}

	// 2. Current directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot determine working directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "kind-config.yaml")); err == nil {
		return cwd, nil
	}

	// 3. ~/.kindling (clone if absent)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	cachedDir := filepath.Join(home, ".kindling")

	if _, err := os.Stat(filepath.Join(cachedDir, "kind-config.yaml")); err == nil {
		// Already cloned — fix remote if pointing to old org, then pull latest
		step("📂", fmt.Sprintf("Using cached project at %s", cachedDir))
		remote, _ := runCapture("git", "-C", cachedDir, "remote", "get-url", "origin")
		if strings.Contains(remote, "jeff-vincent/kindling") {
			_ = runDir(cachedDir, "git", "remote", "set-url", "origin",
				"https://github.com/kindling-sh/kindling.git")
		}
		_ = runDir(cachedDir, "git", "pull", "--ff-only", "-q")
		return cachedDir, nil
	}

	// Clone
	step("📥", "Cloning kindling project to ~/.kindling")
	if err := run("git", "clone", "--depth=1",
		"https://github.com/kindling-sh/kindling.git", cachedDir); err != nil {
		return "", fmt.Errorf("failed to clone kindling repo to %s: %w", cachedDir, err)
	}
	return cachedDir, nil
}

// clusterExists checks whether a Kind cluster with the given name exists.
func clusterExists(name string) bool {
	out, err := runCapture("kind", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// runStdin executes a command with the given string piped to stdin.
func runStdin(input, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ensureKustomize downloads kustomize to <dir>/bin/ if not already present
// and returns the path to the binary.
func ensureKustomize(dir string) (string, error) {
	binDir := filepath.Join(dir, "bin")
	kustomizePath := filepath.Join(binDir, "kustomize")

	if info, err := os.Stat(kustomizePath); err == nil && !info.IsDir() {
		return kustomizePath, nil
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create bin dir: %w", err)
	}

	osName := runtime.GOOS // linux or darwin
	arch := runtime.GOARCH // amd64 or arm64

	version := "v5.5.0"
	url := fmt.Sprintf(
		"https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%%2F%s/kustomize_%s_%s_%s.tar.gz",
		version, version, osName, arch,
	)

	step("📥", fmt.Sprintf("Downloading kustomize %s", version))
	// Download and extract in one shot: curl | tar
	tarCmd := exec.Command("bash", "-c",
		fmt.Sprintf("curl -sL '%s' | tar xz -C '%s'", url, binDir))
	tarCmd.Stdout = os.Stdout
	tarCmd.Stderr = os.Stderr
	if err := tarCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download kustomize: %w", err)
	}

	return kustomizePath, nil
}
