package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/jeffvincent/kindling/pkg/ci"
	"github.com/spf13/cobra"
)

var runnersCmd = &cobra.Command{
	Use:   "runners",
	Short: "Create a CI runner pool in the cluster",
	Long: `Creates the CI token secret and applies a runner pool CR
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
	labels := ci.Default().CLILabels()
	runnersCmd.Flags().StringVarP(&ghUsername, "username", "u", "", labels.Username)
	runnersCmd.Flags().StringVarP(&ghRepo, "repo", "r", "", labels.Repository)
	runnersCmd.Flags().StringVarP(&ghPAT, "token", "t", "", labels.Token)
	rootCmd.AddCommand(runnersCmd)
}

func runRunners(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)
	labels := ci.Default().CLILabels()

	// ── Collect missing values interactively ────────────────────
	if ghUsername == "" {
		ghUsername = prompt(reader, labels.Username)
	}
	if ghRepo == "" {
		ghRepo = prompt(reader, labels.Repository)
	}
	if ghPAT == "" {
		ghPAT = prompt(reader, labels.Token)
	}

	if ghUsername == "" || ghRepo == "" || ghPAT == "" {
		return fmt.Errorf("all three values (username, repo, token) are required")
	}

	// ── Create secret + runner pool CR ──────────────────────────
	header("Setting up CI runner")

	step("🔑", fmt.Sprintf("Creating %s secret", labels.SecretName))
	step("🚀", fmt.Sprintf("Applying %s CR", labels.CRDKind))

	outputs, err := core.CreateRunnerPool(core.RunnerPoolConfig{
		ClusterName: clusterName,
		Username:    ghUsername,
		Repo:        ghRepo,
		Token:       ghPAT,
	})
	if err != nil {
		return err
	}
	for _, o := range outputs {
		success(o)
	}

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
	fmt.Printf("  Trigger a workflow at: %s%s%s\n", colorCyan, fmt.Sprintf(labels.ActionsURLFmt, ghRepo), colorReset)
	fmt.Println()

	return nil
}

// prompt asks the user for input with a label.
func prompt(reader *bufio.Reader, label string) string {
	fmt.Printf("  %s%s:%s ", colorBold, label, colorReset)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
