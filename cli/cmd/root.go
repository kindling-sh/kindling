package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// clusterName is the Kind cluster name used by all commands.
	clusterName string

	// projectDir is the root of the kindling project (defaults to cwd).
	projectDir string
)

var rootCmd = &cobra.Command{
	Use:   "kindling",
	Short: "kindling — set up CI in minutes, stay for everything else",
	Long: `kindling is a development engine that wires up your CI pipeline
in minutes — then keeps working for you. It bootstraps a local Kind
cluster with an operator, in-cluster registry, and CI runners, then
gives you live sync, a visual dashboard, and everything you need to
keep building.

Common workflow:

  kindling init                           # create cluster + deploy operator
  kindling runners -u <user> -r <repo> -t <pat>      # register a runner
  kindling generate -k <api-key> -r .     # AI-generate a dev-deploy.yml
  kindling secrets set STRIPE_KEY sk_...  # store an external secret
  kindling deploy -f dev-environment.yaml # spin up a staging environment
  kindling load -s orders --context .     # build, load into Kind, roll out
  kindling sync -d orders                 # live-sync files into running pod
  kindling push -s orders                 # git push, rebuild orders only
  kindling expose                         # public HTTPS tunnel for OAuth
  kindling status                         # view everything at a glance
  kindling logs                           # tail the controller
  kindling reset                          # remove runner pool, keep cluster
  kindling destroy                        # tear it all down`,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&clusterName, "cluster", "c", "dev", "Kind cluster name")
	rootCmd.PersistentFlags().StringVarP(&projectDir, "project-dir", "p", "", "Path to kindling project root (default: current directory)")
}

// Execute runs the root command.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		return fmt.Errorf("cli error: %w", err)
	}
	return nil
}
