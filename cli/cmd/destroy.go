package cmd

import (
	"fmt"

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

	if !clusterExists(clusterName) {
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

	// Stop any running tunnel before tearing down the cluster.
	if info, _ := readTunnelInfo(); info != nil && info.PID > 0 && processAlive(info.PID) {
		step("🛑", "Stopping tunnel...")
		_ = stopTunnel()
	}

	step("💥", fmt.Sprintf("kind delete cluster --name %s", clusterName))
	if err := run("kind", "delete", "cluster", "--name", clusterName); err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	success("Cluster deleted")
	fmt.Println()
	fmt.Printf("  Recreate with: %skindling init%s\n", colorCyan, colorReset)
	fmt.Println()

	return nil
}
