package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/jeffvincent/kindling/pkg/ci"
	"github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Remove the runner pool so you can point it at a new repo",
	Long: `Deletes the runner pool CR and its token secret, leaving
the cluster, operator, and any DevStagingEnvironments intact.

After reset, run runners again to register a runner for a different repo:

  kindling reset
  kindling runners -u <user> -r <new-repo> -t <pat>`,
	RunE: runReset,
}

var (
	resetForce    bool
	resetProvider string
)

func init() {
	resetCmd.Flags().BoolVarP(&resetForce, "force", "y", false, "Skip confirmation prompt")
	resetCmd.Flags().StringVar(&resetProvider, "provider", "", "CI provider (github, gitlab, circleci)")
	rootCmd.AddCommand(resetCmd)
}

func runReset(cmd *cobra.Command, args []string) error {
	header("Resetting runner pool")

	if !clusterExists(clusterName) {
		fail(fmt.Sprintf("Kind cluster %q not found", clusterName))
		return nil
	}

	// ── Resolve provider ─────────────────────────────────────────
	provider := ci.Default()
	if resetProvider != "" {
		p, err := ci.Get(resetProvider)
		if err != nil {
			return fmt.Errorf("unknown provider %q (available: github, gitlab, circleci)", resetProvider)
		}
		provider = p
	}
	labels := provider.CLILabels()

	// ── Find existing runner pools ──────────────────────────────
	poolsOut, err := core.ListRunnerPools(clusterName, resetProvider)
	if err != nil || strings.TrimSpace(poolsOut) == "" {
		warn("No runner pools found — nothing to reset")
		return nil
	}

	fmt.Println()
	step("📋", "Current runner pool(s):")
	for _, line := range strings.Split(strings.TrimSpace(poolsOut), "\n") {
		fmt.Printf("       %s\n", strings.TrimSpace(line))
	}
	fmt.Println()

	// ── Confirm ─────────────────────────────────────────────────
	if !resetForce {
		fmt.Printf("  %s⚠️  This will delete the runner pool(s) and token secret.%s\n", colorYellow, colorReset)
		fmt.Printf("  The cluster, operator, and environments will be kept.\n")
		fmt.Printf("  Continue? [y/N] ")

		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("  Aborted.")
			return nil
		}
	}

	// ── Delete all runner pool CRs ─────────────────────────────
	step("🗑️ ", fmt.Sprintf("Deleting %s runner pools", provider.DisplayName()))
	step("🔑", fmt.Sprintf("Removing %s secret", labels.SecretName))
	outputs, _ := core.ResetRunners(clusterName, "default", resetProvider)
	for _, o := range outputs {
		success(o)
	}

	// ── Wait for runner pods to terminate ────────────────────────
	step("⏳", "Waiting for runner pods to terminate...")
	for i := 0; i < 30; i++ {
		out, err := runSilent("kubectl", "get", "pods",
			"-l", "app.kubernetes.io/component="+labels.RunnerComponent,
			"--no-headers", "--ignore-not-found")
		if err != nil || strings.TrimSpace(out) == "" {
			break
		}
		time.Sleep(2 * time.Second)
	}

	success("Runner pool removed")
	fmt.Println()
	fmt.Printf("  Re-register with a new repo:\n")
	fmt.Printf("  %skindling runners -u <user> -r <new-repo> -t <pat>%s\n", colorCyan, colorReset)
	fmt.Println()

	return nil
}
