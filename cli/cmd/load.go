package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// loadImageTag returns a deterministic image name:tag for the service.
// Uses a unix-timestamp tag so each build is unique and Kubernetes
// always sees a new image reference.
func loadImageTag(service string) string {
	ts := time.Now().Unix()
	return fmt.Sprintf("%s:%d", service, ts)
}

func runLoad(cmd *cobra.Command, args []string) error {
	// ── Validate ────────────────────────────────────────────────
	service := strings.TrimSpace(loadService)
	if service == "" {
		return fmt.Errorf("--service is required")
	}

	ctx := loadContext
	if ctx == "" {
		ctx = "."
	}
	ctx, err := filepath.Abs(ctx)
	if err != nil {
		return fmt.Errorf("cannot resolve context path: %w", err)
	}
	if _, err := os.Stat(ctx); os.IsNotExist(err) {
		return fmt.Errorf("context directory does not exist: %s", ctx)
	}

	// Resolve Dockerfile
	dockerfile := loadDockerfile
	if dockerfile == "" {
		dockerfile = filepath.Join(ctx, "Dockerfile")
	}
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		return fmt.Errorf("Dockerfile not found: %s", dockerfile)
	}

	// Check cluster exists
	if !clusterExists(clusterName) {
		return fmt.Errorf("Kind cluster %q not found — run: kindling init", clusterName)
	}

	imageTag := loadImageTag(service)

	// ── 1. Docker build ─────────────────────────────────────────
	header("Building image")
	step("🐳", fmt.Sprintf("%s → %s", dockerfile, imageTag))

	dockerArgs := []string{"build", "-t", imageTag, "-f", dockerfile}
	if loadPlatform != "" {
		dockerArgs = append(dockerArgs, "--platform", loadPlatform)
	}
	dockerArgs = append(dockerArgs, ctx)

	if err := run("docker", dockerArgs...); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	success(fmt.Sprintf("Image built: %s", imageTag))

	// ── 2. Load into Kind ───────────────────────────────────────
	header("Loading into Kind cluster")
	step("📦", fmt.Sprintf("kind load docker-image %s --name %s", imageTag, clusterName))

	if err := run("kind", "load", "docker-image", imageTag, "--name", clusterName); err != nil {
		return fmt.Errorf("failed to load image into Kind cluster %q: %w", clusterName, err)
	}
	success("Image loaded into cluster")

	// ── 3. Patch DSE (optional) ─────────────────────────────────
	if loadNoDeploy {
		fmt.Println()
		step("⏭️ ", "--no-deploy: skipping DSE patch")
		fmt.Printf("\n  Image is available in the cluster as: %s%s%s\n", colorCyan, imageTag, colorReset)
		fmt.Printf("  To deploy manually:\n")
		fmt.Printf("    %skubectl patch dse %s -n %s --type=merge -p '{\"spec\":{\"deployment\":{\"image\":\"%s\"}}}'%s\n",
			colorCyan, service, loadNamespace, imageTag, colorReset)
		fmt.Println()
		return nil
	}

	header("Patching DevStagingEnvironment")

	// Check that the DSE exists
	if _, err := runCapture("kubectl", "get", "dse", service, "-n", loadNamespace); err != nil {
		return fmt.Errorf("DSE %q not found in namespace %q — deploy it first with: kindling deploy -f <file>",
			service, loadNamespace)
	}

	// Patch the image
	patch := fmt.Sprintf(`{"spec":{"deployment":{"image":"%s"}}}`, imageTag)
	step("🔄", fmt.Sprintf("Patching %s → %s", service, imageTag))

	if err := run("kubectl", "patch", "dse", service,
		"-n", loadNamespace,
		"--type=merge",
		"-p", patch,
	); err != nil {
		return fmt.Errorf("failed to patch DSE %q: %w", service, err)
	}
	success(fmt.Sprintf("DSE %s patched with %s", service, imageTag))

	// ── 4. Wait for rollout ─────────────────────────────────────
	step("⏳", "Waiting for rollout...")

	if err := run("kubectl", "rollout", "status",
		fmt.Sprintf("deployment/%s", service),
		"-n", loadNamespace,
		"--timeout=120s",
	); err != nil {
		warn("Rollout timed out — check with: kindling status")
		warn(fmt.Sprintf("  kubectl logs -n %s -l app=%s --tail=20", loadNamespace, service))
	} else {
		success("Rollout complete")
	}

	// ── Done ────────────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("  %s🎉 %s is live with your latest changes%s\n", colorGreen+colorBold, service, colorReset)
	fmt.Println()
	fmt.Printf("  Check status:  %skindling status%s\n", colorCyan, colorReset)
	fmt.Printf("  View logs:     %skubectl logs -n %s -l app=%s -f%s\n",
		colorCyan, loadNamespace, service, colorReset)
	fmt.Println()

	return nil
}
