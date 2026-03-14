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
	Short: "kindling — dev on your laptop, deploy to production, one tool",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		ensureIntel(cmd)
	},
	Long: `kindling is a development lifecycle tool. It takes your project from
first commit to production deployment — CI pipeline, live dev loop,
and production-readiness guardrails, all in one CLI.

CI works out of the box. Guardrails are opt-in and configurable.

Core workflow:

  kindling init                           # create cluster + deploy operator
  kindling runners -u <user> -r <repo> -t <pat>      # register a runner
  kindling generate -k <api-key> -r .     # AI-generate a dev-deploy.yml
  kindling deploy -f dev-environment.yaml # spin up a staging environment

Dev loop:

  kindling load -s orders --context .     # build, load into Kind, roll out
  kindling sync -d orders                 # live-sync files into running pod
  kindling debug -d orders                # attach a debugger to a service
  kindling dev -d frontend                # local frontend dev + cluster APIs
  kindling push -s orders                 # git push, rebuild orders only
  kindling expose                         # public HTTPS tunnel for OAuth

Production readiness (opt-in):

  kindling harden                         # security, scalability, container checks
  kindling harden --strict                # treat all issues as errors
  kindling docs                           # generate engineering artifacts

Operations:

  kindling status                         # view everything at a glance
  kindling diagnose                       # find and explain runtime issues
  kindling secrets set STRIPE_KEY sk_...  # store an external secret
  kindling env set LOG_LEVEL=debug        # live env var management
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
