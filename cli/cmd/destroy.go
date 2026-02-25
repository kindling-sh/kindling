package cmd

import (
	"fmt"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Delete the Kind cluster and all resources",
	Long: `Deletes the Kind cluster, removing all pods, services, and data.
This is irreversible — all DevStagingEnvironments and runner pools
will be destroyed.`,
	RunE: runDestroy,
}

var destroyForce bool

func init() {
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "y", false, "Skip confirmation prompt")
	rootCmd.AddCommand(destroyCmd)
}

func runDestroy(cmd *cobra.Command, args []string) error {
	header("Destroying Kind cluster")

	if !core.ClusterExists(clusterName) {
		warn(fmt.Sprintf("Cluster %q does not exist — nothing to do", clusterName))
		return nil
	}

	if !destroyForce {
		fmt.Printf("\n  %s⚠️  This will permanently delete cluster %q and all its data.%s\n", colorYellow, clusterName, colorReset)
		fmt.Printf("  Type the cluster name to confirm: ")

		var confirm string
		fmt.Scanln(&confirm)
		if confirm != clusterName {
			fmt.Println("  Aborted.")
			return nil
		}
	}

	// Stop any running tunnels before tearing down the cluster.
	tunnels := core.ReadAllTunnels()
	if len(tunnels) > 0 {
		step("🛑", fmt.Sprintf("Stopping %d tunnel(s)...", len(tunnels)))
		core.StopTunnelProcess()
		core.CleanupTunnel(clusterName)
	}

	step("💥", fmt.Sprintf("kind delete cluster --name %s", clusterName))
	_, err := core.DestroyCluster(clusterName)
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	success("Cluster deleted")
	fmt.Println()
	fmt.Printf("  Recreate with: %skindling init%s\n", colorCyan, colorReset)
	fmt.Println()

	return nil
}
