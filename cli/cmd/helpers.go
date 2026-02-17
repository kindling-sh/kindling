package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// ── Pretty-print helpers ────────────────────────────────────────

func header(msg string) {
	fmt.Printf("\n%s%s▸ %s%s\n", colorBold, colorCyan, msg, colorReset)
}

func step(emoji, msg string) {
	fmt.Printf("  %s  %s\n", emoji, msg)
}

func success(msg string) {
	fmt.Printf("  %s✅ %s%s\n", colorGreen, msg, colorReset)
}

func warn(msg string) {
	fmt.Printf("  %s⚠️  %s%s\n", colorYellow, msg, colorReset)
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
	return strings.TrimSpace(stdout.String()), err
}

// commandExists checks if a binary is on PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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
		// Already cloned — pull latest
		step("📂", fmt.Sprintf("Using cached project at %s", cachedDir))
		_ = runDir(cachedDir, "git", "pull", "--ff-only", "-q")
		return cachedDir, nil
	}

	// Clone
	step("📥", "Cloning kindling project to ~/.kindling")
	if err := run("git", "clone", "--depth=1",
		"https://github.com/jeff-vincent/kindling.git", cachedDir); err != nil {
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
