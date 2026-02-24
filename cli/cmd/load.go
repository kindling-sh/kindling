package cmd

import (
	"fmt"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

var loadCmd = &cobra.Command{
	Use:   "load",
	Short: "Build a service image, load into Kind, and roll out",
	Long: `Builds a Docker image for a service, loads it into the Kind cluster,
and patches the running DevStagingEnvironment to use the new image —
triggering an immediate rollout.

This is the inner-loop dev command: edit code, kindling load, see it
running. No CI, no push, no waiting.

The image is tagged with a timestamp so Kubernetes always pulls the
new version (even with imagePullPolicy: IfNotPresent, the tag changes).

Examples:
  # Build + load from a Dockerfile in the current directory
  kindling load -s orders

  # Build from a specific context directory
  kindling load -s orders --context ./services/orders

  # Build with a specific Dockerfile
  kindling load -s orders --context ./services/orders -f Dockerfile.dev

  # Load into a specific namespace
  kindling load -s orders -n staging

  # Just build + load (skip the DSE patch / rollout)
  kindling load -s orders --no-deploy`,
	RunE: runLoad,
}

var (
	loadService    string
	loadContext    string
	loadDockerfile string
	loadNamespace  string
	loadNoDeploy   bool
	loadPlatform   string
)

func init() {
	loadCmd.Flags().StringVarP(&loadService, "service", "s", "",
		"Name of the service / DSE to load (required)")
	loadCmd.Flags().StringVar(&loadContext, "context", ".",
		"Docker build context directory")
	loadCmd.Flags().StringVarP(&loadDockerfile, "dockerfile", "f", "",
		"Path to Dockerfile (default: <context>/Dockerfile)")
	loadCmd.Flags().StringVarP(&loadNamespace, "namespace", "n", "default",
		"Kubernetes namespace of the DSE")
	loadCmd.Flags().BoolVar(&loadNoDeploy, "no-deploy", false,
		"Build and load only — don't patch the DSE")
	loadCmd.Flags().StringVar(&loadPlatform, "platform", "",
		"Docker build --platform (e.g. linux/amd64)")
	_ = loadCmd.MarkFlagRequired("service")
	rootCmd.AddCommand(loadCmd)
}

func runLoad(cmd *cobra.Command, args []string) error {
	header("Building & loading image")

	outputs, err := core.BuildAndLoad(core.LoadConfig{
		ClusterName: clusterName,
		Service:     loadService,
		Context:     loadContext,
		Dockerfile:  loadDockerfile,
		Namespace:   loadNamespace,
		NoDeploy:    loadNoDeploy,
		Platform:    loadPlatform,
	})
	if err != nil {
		return err
	}

	for _, o := range outputs {
		success(o)
	}

	fmt.Println()
	fmt.Printf("  %s🎉 %s is live with your latest changes%s\n", colorGreen+colorBold, loadService, colorReset)
	fmt.Println()
	fmt.Printf("  Check status:  %skindling status%s\n", colorCyan, colorReset)
	fmt.Printf("  View logs:     %skubectl logs -n %s -l app=%s -f%s\n",
		colorCyan, loadNamespace, loadService, colorReset)
	fmt.Println()

	return nil
}
