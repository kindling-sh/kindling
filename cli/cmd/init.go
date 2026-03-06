package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Bootstrap a Kind cluster with the kindling operator",
	Long: `Creates a Kind cluster, installs the in-cluster image registry and
Traefik ingress controller, pulls the pre-built kindling operator image from
GHCR (or builds it from source with --build), and deploys it into the cluster.

By default the operator image is pulled from:
  ghcr.io/kindling-sh/kindling-operator:latest

Use --build to build from source instead (requires Go and Make).
Use --operator-image to specify a different pre-built image.

Optional flags are passed through to "kind create cluster":
  --image        Node image to use (e.g. kindest/node:v1.29.0)
  --kubeconfig   Path to write kubeconfig (default: $KUBECONFIG or ~/.kube/config)
  --wait         Wait for control plane to be ready (e.g. 60s, 5m)
  --retain       Retain nodes for debugging if cluster creation fails`,
	RunE: runInit,
}

// DefaultOperatorImage is the pre-built operator image published to GHCR.
// At release time this is overridden via ldflags to pin the semver tag,
// e.g. ghcr.io/kindling-sh/kindling-operator:0.5.0
var DefaultOperatorImage = "ghcr.io/kindling-sh/kindling-operator:latest"

var (
	skipCluster    bool
	kindNodeImage  string
	kindKubeconfig string
	kindWait       string
	kindRetain     bool
	initExpose     bool
	buildOperator  bool
	operatorImage  string
)

func init() {
	initCmd.Flags().BoolVar(&skipCluster, "skip-cluster", false, "Skip Kind cluster creation (use existing cluster)")
	initCmd.Flags().StringVar(&kindNodeImage, "image", "", "Node Docker image for Kind (e.g. kindest/node:v1.29.0)")
	initCmd.Flags().StringVar(&kindKubeconfig, "kubeconfig", "", "Path to write kubeconfig instead of default location")
	initCmd.Flags().StringVar(&kindWait, "wait", "", "Wait for control plane to be ready (e.g. 60s, 5m)")
	initCmd.Flags().BoolVar(&kindRetain, "retain", false, "Retain cluster nodes for debugging on creation failure")
	initCmd.Flags().BoolVar(&initExpose, "expose", false, "Start a public HTTPS tunnel after bootstrap (runs kindling expose)")
	initCmd.Flags().BoolVar(&buildOperator, "build", false, "Build the operator image from source instead of pulling the pre-built image")
	initCmd.Flags().StringVar(&operatorImage, "operator-image", DefaultOperatorImage, "Operator image to pull (ignored when --build is set)")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir, err := resolveProjectDir()
	if err != nil {
		return err
	}

	// ── Preflight checks ────────────────────────────────────────
	header("Preflight checks")

	requiredTools := []string{"kind", "kubectl", "docker"}
	if buildOperator {
		requiredTools = append(requiredTools, "go", "make")
	}

	missing := []string{}
	for _, tool := range requiredTools {
		if commandExists(tool) {
			step("✓", fmt.Sprintf("%s found", tool))
		} else {
			missing = append(missing, tool)
			fail(fmt.Sprintf("%s not found on PATH", tool))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required tools: %v", missing)
	}

	configPath := filepath.Join(dir, "kind-config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("kind-config.yaml not found in %s — are you in the kindling project root?", dir)
	}

	// ── Create Kind cluster ─────────────────────────────────────
	if skipCluster {
		header("Skipping cluster creation (--skip-cluster)")
	} else {
		header("Creating Kind cluster")

		if clusterExists(clusterName) {
			warn(fmt.Sprintf("Cluster %q already exists — skipping creation", clusterName))
		} else {
			kindArgs := []string{
				"create", "cluster",
				"--name", clusterName,
				"--config", configPath,
			}
			if kindNodeImage != "" {
				kindArgs = append(kindArgs, "--image", kindNodeImage)
			}
			if kindKubeconfig != "" {
				kindArgs = append(kindArgs, "--kubeconfig", kindKubeconfig)
			}
			if kindWait != "" {
				kindArgs = append(kindArgs, "--wait", kindWait)
			}
			if kindRetain {
				kindArgs = append(kindArgs, "--retain")
			}

			step("🔧", fmt.Sprintf("kind %s", strings.Join(kindArgs, " ")))
			if err := runDir(dir, "kind", kindArgs...); err != nil {
				return fmt.Errorf("failed to create Kind cluster: %w", err)
			}
			success("Kind cluster created")
		}
	}

	// ── Set kubectl context ─────────────────────────────────────
	ctx := kindContext()
	step("🔗", fmt.Sprintf("Switching kubectl context to %s", ctx))
	if err := run("kubectl", "cluster-info", "--context", ctx); err != nil {
		return fmt.Errorf("cannot reach cluster %q: %w", ctx, err)
	}

	// ── Setup ingress + registry ────────────────────────────────
	header("Installing Traefik + in-cluster registry")

	ingressScript := filepath.Join(dir, "setup-ingress.sh")
	if _, err := os.Stat(ingressScript); os.IsNotExist(err) {
		return fmt.Errorf("setup-ingress.sh not found in %s", dir)
	}

	if err := runDir(dir, "bash", ingressScript); err != nil {
		return fmt.Errorf("setup-ingress.sh failed: %w", err)
	}
	success("Ingress and registry ready")

	// ── Get the operator image ───────────────────────────────────
	imgTag := "controller:latest"

	if buildOperator {
		// Build from source — requires Go, Make, and the project checkout
		header("Building kindling operator image from source")

		for _, tool := range []string{"go", "make"} {
			if !commandExists(tool) {
				return fmt.Errorf("%s is required for --build but was not found on PATH", tool)
			}
		}

		step("🏗️ ", "docker build -t controller:latest .")
		if err := runDir(dir, "docker", "build", "-t", "controller:latest", "."); err != nil {
			return fmt.Errorf("operator image build failed: %w", err)
		}
		success("Operator image built")
	} else {
		// Pull the pre-built image from GHCR
		header("Pulling kindling operator image")

		step("⬇️ ", fmt.Sprintf("docker pull %s", operatorImage))
		if err := run("docker", "pull", operatorImage); err != nil {
			return fmt.Errorf("failed to pull operator image %s: %w\n\nTo build from source instead, run: kindling init --build", operatorImage, err)
		}

		// Tag as controller:latest so kustomize config works as-is
		if err := run("docker", "tag", operatorImage, imgTag); err != nil {
			return fmt.Errorf("failed to tag operator image: %w", err)
		}
		success(fmt.Sprintf("Operator image ready (%s)", operatorImage))
	}

	// ── Load image into Kind ────────────────────────────────────
	step("📦", "Loading image into Kind cluster")
	if err := run("kind", "load", "docker-image", imgTag, "--name", clusterName); err != nil {
		return fmt.Errorf("failed to load image into Kind: %w", err)
	}
	success("Image loaded")

	// ── Ensure kustomize is available ───────────────────────────
	kustomizeBin, err := ensureKustomize(dir)
	if err != nil {
		return fmt.Errorf("failed to set up kustomize: %w", err)
	}

	// ── Install CRDs ────────────────────────────────────────────
	header("Installing CRDs + deploying operator")

	step("📜", "Applying CRDs")
	crdDir := filepath.Join(dir, "config", "crd", "bases")
	if err := run("kubectl", "apply", "-f", crdDir); err != nil {
		return fmt.Errorf("CRD installation failed: %w", err)
	}
	success("CRDs installed")

	// ── Deploy operator ─────────────────────────────────────────
	step("🚀", "Deploying operator via kustomize")

	// Set the image in kustomization before building
	managerDir := filepath.Join(dir, "config", "manager")
	if err := runDir(managerDir, kustomizeBin, "edit", "set", "image", "controller=controller:latest"); err != nil {
		return fmt.Errorf("kustomize edit set image failed: %w", err)
	}

	// Build kustomize output and pipe to kubectl apply
	kustomizeOut, err := runCapture(kustomizeBin, "build", filepath.Join(dir, "config", "default"))
	if err != nil {
		return fmt.Errorf("kustomize build failed: %w", err)
	}
	if err := runStdin(kustomizeOut, "kubectl", "apply", "-f", "-"); err != nil {
		return fmt.Errorf("operator deployment failed: %w", err)
	}
	success("Operator deployed")

	// ── Wait for operator ───────────────────────────────────────
	step("⏳", "Waiting for controller-manager rollout")
	if err := run("kubectl", "rollout", "status",
		"deployment/kindling-controller-manager",
		"-n", "kindling-system",
		"--timeout=120s",
	); err != nil {
		warn("Controller deployment rollout timed out — check with: kindling logs")
	} else {
		success("Controller is running")
	}

	// ── Done ────────────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("  %s🎉 kindling is ready!%s\n", colorGreen+colorBold, colorReset)
	fmt.Println()
	fmt.Println("  Next steps:")
	fmt.Printf("    %skindling runners -u <user> -r <repo> -t <token>%s\n", colorCyan, colorReset)
	fmt.Printf("    %skindling deploy -f examples/sample-app/dev-environment.yaml%s\n", colorCyan, colorReset)
	fmt.Printf("    %skindling status%s\n", colorCyan, colorReset)
	fmt.Println()

	// ── Optional: start tunnel ──────────────────────────────────
	if initExpose {
		return runExpose(cmd, nil)
	}

	return nil
}
