package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var runnersCmd = &cobra.Command{
	Use:   "runners",
	Short: "Create a GitHub Actions runner pool in the cluster",
	Long: `Creates the GitHub PAT secret and applies a GithubActionRunnerPool CR
so a self-hosted runner registers with your repo.

Flags can be provided on the command line or the CLI will prompt
interactively for any missing values.`,
	RunE: runRunners,
}

var (
	ghUsername string
	ghRepo     string
	ghPAT      string
)

func init() {
	runnersCmd.Flags().StringVarP(&ghUsername, "username", "u", "", "GitHub username")
	runnersCmd.Flags().StringVarP(&ghRepo, "repo", "r", "", "GitHub repository (owner/repo)")
	runnersCmd.Flags().StringVarP(&ghPAT, "token", "t", "", "GitHub Personal Access Token")
	rootCmd.AddCommand(runnersCmd)
}

func runRunners(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)

	// ── Collect missing values interactively ────────────────────
	if ghUsername == "" {
		ghUsername = prompt(reader, "GitHub username")
	}
	if ghRepo == "" {
		ghRepo = prompt(reader, "GitHub repository (owner/repo)")
	}
	if ghPAT == "" {
		ghPAT = prompt(reader, "GitHub PAT (repo scope)")
	}

	if ghUsername == "" || ghRepo == "" || ghPAT == "" {
		return fmt.Errorf("all three values (username, repo, token) are required")
	}

	// ── Create secret ───────────────────────────────────────────
	header("Setting up GitHub Actions runner")

	step("🔑", "Creating github-runner-token secret")

	secretYAML, err := runCapture("kubectl", "create", "secret", "generic", "github-runner-token",
		"--from-literal=github-token="+ghPAT,
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		return fmt.Errorf("failed to generate secret YAML: %w", err)
	}

	applyCmd := exec.Command("kubectl", "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(secretYAML)
	applyCmd.Stdout = os.Stdout
	applyCmd.Stderr = os.Stderr
	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("failed to apply secret: %w", err)
	}
	success("Secret github-runner-token ready")

	// ── Apply GithubActionRunnerPool CR ─────────────────────────
	step("🚀", "Applying GithubActionRunnerPool CR")

	crYAML := fmt.Sprintf(`apiVersion: apps.example.com/v1alpha1
kind: GithubActionRunnerPool
metadata:
  name: %s-runner-pool
spec:
  githubUsername: "%s"
  repository: "%s"
  tokenSecretRef:
    name: github-runner-token
    key: github-token
  replicas: 1
  labels:
    - linux
`, ghUsername, ghUsername, ghRepo)

	applyCmd2 := exec.Command("kubectl", "apply", "-f", "-")
	applyCmd2.Stdin = strings.NewReader(crYAML)
	applyCmd2.Stdout = os.Stdout
	applyCmd2.Stderr = os.Stderr
	if err := applyCmd2.Run(); err != nil {
		return fmt.Errorf("failed to apply GithubActionRunnerPool: %w", err)
	}
	success("GithubActionRunnerPool applied")

	// ── Wait for deployment ─────────────────────────────────────
	header("Waiting for runner deployment")

	deployName := fmt.Sprintf("deployment/%s-runner", ghUsername)
	step("⏳", fmt.Sprintf("Polling for %s to appear...", deployName))

	found := false
	for i := 0; i < 30; i++ {
		if _, err := runSilent("kubectl", "get", deployName); err == nil {
			found = true
			break
		}
		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}
	fmt.Println()

	if !found {
		return fmt.Errorf("timed out waiting for %s to be created", deployName)
	}

	step("⏳", "Waiting for rollout to complete...")
	if err := run("kubectl", "rollout", "status", deployName, "--timeout=120s"); err != nil {
		return fmt.Errorf("runner rollout failed: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %s🎉 Runner is ready!%s\n", colorGreen+colorBold, colorReset)
	fmt.Printf("  Trigger a workflow at: %shttps://github.com/%s/actions%s\n", colorCyan, ghRepo, colorReset)
	fmt.Println()

	return nil
}

// prompt asks the user for input with a label.
func prompt(reader *bufio.Reader, label string) string {
	fmt.Printf("  %s%s:%s ", colorBold, label, colorReset)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
