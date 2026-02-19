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
	Short: "kindling — local Kubernetes dev environments powered by Kind",
	Long: `kindling is a CLI that bootstraps and manages a local Kind cluster
running the kindling operator, in-cluster image registry, and
self-hosted GitHub Actions runners.

Common workflow:

  kindling init                           # create cluster + deploy operator
  kindling runners -u <user> -r <repo> -t <pat>      # register a runner
  kindling generate -k <api-key> -r .     # AI-generate a dev-deploy.yml
  kindling secrets set STRIPE_KEY sk_...  # store an external secret
  kindling deploy -f dev-environment.yaml # spin up a staging environment
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
