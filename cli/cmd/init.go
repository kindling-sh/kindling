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
ingress-nginx controller, builds the kindling operator image, and deploys
it into the cluster.

This is the equivalent of running:
  kind create cluster --name dev --config kind-config.yaml
  ./setup-ingress.sh
  make docker-build IMG=controller:latest
  kind load docker-image controller:latest --name dev
  make install deploy IMG=controller:latest

Optional flags are passed through to "kind create cluster":
  --image        Node image to use (e.g. kindest/node:v1.29.0)
  --kubeconfig   Path to write kubeconfig (default: $KUBECONFIG or ~/.kube/config)
  --wait         Wait for control plane to be ready (e.g. 60s, 5m)
  --retain       Retain nodes for debugging if cluster creation fails`,
	RunE: runInit,
}

var (
	skipCluster    bool
	kindNodeImage  string
	kindKubeconfig string
	kindWait       string
	kindRetain     bool
	initExpose     bool
)

func init() {
	initCmd.Flags().BoolVar(&skipCluster, "skip-cluster", false, "Skip Kind cluster creation (use existing cluster)")
	initCmd.Flags().StringVar(&kindNodeImage, "image", "", "Node Docker image for Kind (e.g. kindest/node:v1.29.0)")
	initCmd.Flags().StringVar(&kindKubeconfig, "kubeconfig", "", "Path to write kubeconfig instead of default location")
	initCmd.Flags().StringVar(&kindWait, "wait", "", "Wait for control plane to be ready (e.g. 60s, 5m)")
	initCmd.Flags().BoolVar(&kindRetain, "retain", false, "Retain cluster nodes for debugging on creation failure")
	initCmd.Flags().BoolVar(&initExpose, "expose", false, "Start a public HTTPS tunnel after bootstrap (runs kindling expose)")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir, err := resolveProjectDir()
	if err != nil {
		return err
	}

	// ── Preflight checks ────────────────────────────────────────
	header("Preflight checks")

	missing := []string{}
	for _, tool := range []string{"kind", "kubectl", "docker"} {
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
	ctx := fmt.Sprintf("kind-%s", clusterName)
	step("🔗", fmt.Sprintf("Switching kubectl context to %s", ctx))
	if err := run("kubectl", "cluster-info", "--context", ctx); err != nil {
		return fmt.Errorf("cannot reach cluster %q: %w", ctx, err)
	}

	// ── Setup ingress + registry ────────────────────────────────
	header("Installing ingress-nginx + in-cluster registry")

	ingressScript := filepath.Join(dir, "setup-ingress.sh")
	if _, err := os.Stat(ingressScript); os.IsNotExist(err) {
		return fmt.Errorf("setup-ingress.sh not found in %s", dir)
	}

	if err := runDir(dir, "bash", ingressScript); err != nil {
		return fmt.Errorf("setup-ingress.sh failed: %w", err)
	}
	success("Ingress and registry ready")

	// ── Build the operator image ────────────────────────────────
	header("Building kindling operator image")

	step("🏗️ ", "docker build -t controller:latest")
	if err := runDir(dir, "docker", "build", "-t", "controller:latest", "."); err != nil {
		return fmt.Errorf("operator image build failed: %w", err)
	}
	success("Operator image built")

	// ── Load image into Kind ────────────────────────────────────
	step("📦", "Loading image into Kind cluster")
	if err := run("kind", "load", "docker-image", "controller:latest", "--name", clusterName); err != nil {
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
	fmt.Printf("    %skindling runners -u <github-user> -r <owner/repo> -t <pat>%s\n", colorCyan, colorReset)
	fmt.Printf("    %skindling deploy -f examples/sample-app/dev-environment.yaml%s\n", colorCyan, colorReset)
	fmt.Printf("    %skindling status%s\n", colorCyan, colorReset)
	fmt.Println()

	// ── Optional: start tunnel ──────────────────────────────────
	if initExpose {
		return runExpose(cmd, nil)
	}

	return nil
}
