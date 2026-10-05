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
	ghUsername            string
	ghRepo                string
	ghPAT                 string
	ciProvider            string
	runnersEnableSnapshot bool
	runnersBuildAgentEnv  []string
)

func init() {
	runnersCmd.Flags().StringVarP(&ghUsername, "username", "u", "", "CI platform username")
	runnersCmd.Flags().StringVarP(&ghRepo, "repo", "r", "", "Repository (owner/repo or group/project)")
	runnersCmd.Flags().StringVarP(&ghPAT, "token", "t", "", "CI platform access token")
	runnersCmd.Flags().StringVar(&ciProvider, "ci-provider", "", "CI provider (github, gitlab)")
	runnersCmd.Flags().BoolVar(&runnersEnableSnapshot, "enable-snapshot-deploy", false,
		"Swap the build-agent sidecar for one that can also run `kindling snapshot --deploy` (helm+crane+kindling CLI) via the kindling-snapshot-deploy action -- sets spec.localClusterName to this command's --cluster automatically")
	runnersCmd.Flags().StringArrayVar(&runnersBuildAgentEnv, "build-agent-env", nil,
		"Env var to inject into the build-agent sidecar specifically (spec.buildAgentEnv), as NAME=SECRET:KEY referencing an existing Secret -- repeatable. Needed for KINDLING_REGISTRY_PASSWORD/KINDLING_REGISTRY_USERNAME (authenticated --registry pushes) and any --creds-config fromEnv target; spec.env does not reach this container")
	rootCmd.AddCommand(runnersCmd)
}

// parseBuildAgentEnvFlag parses repeated --build-agent-env NAME=SECRET:KEY
// values into core.BuildAgentEnvVar entries, failing fast on anything
// malformed rather than silently dropping it or applying a half-specified
// secretKeyRef to the cluster.
func parseBuildAgentEnvFlag(raw []string) ([]core.BuildAgentEnvVar, error) {
	var out []core.BuildAgentEnvVar
	for _, entry := range raw {
		name, rest, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--build-agent-env %q: expected NAME=SECRET:KEY", entry)
		}
		secretName, secretKey, ok := strings.Cut(rest, ":")
		if !ok || secretName == "" || secretKey == "" {
			return nil, fmt.Errorf("--build-agent-env %q: expected NAME=SECRET:KEY (missing SECRET:KEY after '=')", entry)
		}
		out = append(out, core.BuildAgentEnvVar{Name: name, SecretName: secretName, SecretKey: secretKey})
	}
	return out, nil
}

func runRunners(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)

	// ── Resolve provider ──────────────────────────────────────────
	provider, err := resolveProvider(ciProvider)
	if err != nil {
		return err
	}
	labels := provider.CLILabels()

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

	buildAgentEnv, err := parseBuildAgentEnvFlag(runnersBuildAgentEnv)
	if err != nil {
		return err
	}
	if runnersEnableSnapshot {
		step("🚀", fmt.Sprintf("Enabling snapshot-deploy (localClusterName: %s)", clusterName))
	}

	// ── Create secret + runner pool CR ──────────────────────────
	header(fmt.Sprintf("Setting up %s runner", provider.DisplayName()))

	step("🔑", fmt.Sprintf("Creating %s secret", labels.SecretName))
	step("🚀", fmt.Sprintf("Applying %s runner pool", provider.DisplayName()))

	outputs, err := core.CreateRunnerPool(core.RunnerPoolConfig{
		ClusterName:          clusterName,
		Username:             ghUsername,
		Repo:                 ghRepo,
		Token:                ghPAT,
		Provider:             ciProvider,
		EnableSnapshotDeploy: runnersEnableSnapshot,
		BuildAgentEnv:        buildAgentEnv,
	})
	if err != nil {
		return err
	}
	for _, o := range outputs {
		success(o)
	}

	// ── Wait for deployment ─────────────────────────────────────
	header("Waiting for runner deployment")

	deployName := "deployment/" + provider.Runner().DeploymentName(ci.SanitizeDNS(ghUsername))
	step("⏳", fmt.Sprintf("Polling for %s to appear...", deployName))

	ctx := core.ClusterContext(clusterName)
	found := false
	for i := 0; i < 30; i++ {
		if _, err := runSilent("kubectl", "--context", ctx, "get", deployName); err == nil {
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
	if err := run("kubectl", "--context", ctx, "rollout", "status", deployName, "--timeout=120s"); err != nil {
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
