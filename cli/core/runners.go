package core

import (
	"fmt"
	"strings"

	"github.com/jeffvincent/kindling/pkg/ci"
)

// BuildAgentEnvVar is one secretKeyRef-backed env var to inject into the
// build-agent sidecar container (spec.buildAgentEnv) -- e.g. registry
// credentials for an authenticated --registry push during
// `kindling snapshot --deploy`, or any env var a --creds-config entry's
// fromEnv references. Never a literal value: the build-agent sidecar is a
// long-running container with its own environment, so this is the only way
// to get a credential into the process that actually runs `kindling
// snapshot` -- it does not inherit anything from the triggering workflow.
type BuildAgentEnvVar struct {
	Name       string // env var name, e.g. "KINDLING_REGISTRY_PASSWORD"
	SecretName string // name of an existing Secret in the cluster
	SecretKey  string // key within that Secret
}

// RunnerPoolConfig holds the parameters for creating a CI runner pool.
type RunnerPoolConfig struct {
	ClusterName string
	Username    string
	Repo        string
	Token       string
	Namespace   string // defaults to "default"
	Provider    string // ci provider name ("github", "gitlab"); empty = default

	// EnableSnapshotDeploy opts this pool into the snapshot-deploy-capable
	// build-agent sidecar (helm + crane + kindling CLI installed, plus a
	// .snapshot-deploy signal handler) so `kindling snapshot --deploy` can
	// run from a workflow via the kindling-snapshot-deploy composite
	// action. LocalClusterName is set automatically from ClusterName --
	// the sidecar needs it to match exactly, since it's always this same
	// developer's own Kind cluster.
	EnableSnapshotDeploy bool

	// BuildAgentEnv is passed through to spec.buildAgentEnv verbatim.
	BuildAgentEnv []BuildAgentEnvVar

	// BuildAgentImage, if set, overrides spec.buildAgentImage -- e.g. a
	// locally built and `kind load docker-image`'d tag for testing
	// hack/build-agent/Dockerfile changes before they're published.
	BuildAgentImage string
}

func (c *RunnerPoolConfig) namespace() string {
	if c.Namespace == "" {
		return "default"
	}
	return c.Namespace
}

// CreateRunnerPool creates the CI token secret and applies a
// runner pool CR for the selected provider. Returns a slice of output messages.
func CreateRunnerPool(cfg RunnerPoolConfig) ([]string, error) {
	ns := cfg.namespace()
	var outputs []string

	provider := ci.Default()
	if cfg.Provider != "" {
		if p, err := ci.Get(cfg.Provider); err == nil {
			provider = p
		}
	}
	labels := provider.CLILabels()

	// 1. Create/update token secret
	Kubectl(cfg.ClusterName, "delete", "secret", labels.SecretName,
		"-n", ns, "--ignore-not-found")

	secretYAML, err := RunCapture("kubectl", "create", "secret", "generic", labels.SecretName,
		"--from-literal="+provider.Runner().DefaultTokenKey()+"="+cfg.Token,
		"--dry-run=client", "-o", "yaml",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate secret YAML: %w", err)
	}

	out, err := KubectlApplyStdin(cfg.ClusterName, secretYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to apply token secret: %s", out)
	}
	outputs = append(outputs, fmt.Sprintf("Secret %s ready", labels.SecretName))

	// 2. Apply runner pool CR
	// Determine platform URL and runner image from the provider so CRD
	// defaults (which are GitHub-specific) don't override them.
	platformURL := "https://github.com"
	runnerImage := provider.Runner().DefaultImage()
	switch provider.Name() {
	case "gitlab":
		platformURL = "https://gitlab.com"
	}

	// Sanitize the username for use in K8s resource names (RFC 1123).
	// The original username is preserved in spec.githubUsername.
	safeName := ci.SanitizeDNS(cfg.Username)

	crYAML := buildRunnerPoolCRYAML(cfg, labels.CRDKind, labels.SecretName,
		provider.Runner().DefaultTokenKey(), platformURL, runnerImage, safeName, ns)

	out, err = KubectlApplyStdin(cfg.ClusterName, crYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to apply runner pool: %s", out)
	}
	poolName := fmt.Sprintf("%s-runner-pool", safeName)
	outputs = append(outputs, fmt.Sprintf("%s runner pool %s created", provider.DisplayName(), poolName))

	return outputs, nil
}

// buildRunnerPoolCRYAML renders the CIRunnerPool CR YAML for CreateRunnerPool.
// Pulled out as its own function (rather than inline string formatting) so
// it's unit-testable without touching a real cluster, and so the optional,
// variable-length buildAgentEnv block can be appended safely.
func buildRunnerPoolCRYAML(cfg RunnerPoolConfig, crdKind, tokenSecretName, tokenSecretKey, platformURL, runnerImage, safeName, ns string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: apps.example.com/v1alpha1\n")
	fmt.Fprintf(&b, "kind: %s\n", crdKind)
	fmt.Fprintf(&b, "metadata:\n")
	fmt.Fprintf(&b, "  name: %s-runner-pool\n", safeName)
	fmt.Fprintf(&b, "  namespace: %s\n", ns)
	fmt.Fprintf(&b, "spec:\n")
	fmt.Fprintf(&b, "  githubUsername: %q\n", cfg.Username)
	fmt.Fprintf(&b, "  repository: %q\n", cfg.Repo)
	fmt.Fprintf(&b, "  githubURL: %q\n", platformURL)
	fmt.Fprintf(&b, "  runnerImage: %q\n", runnerImage)
	fmt.Fprintf(&b, "  tokenSecretRef:\n")
	fmt.Fprintf(&b, "    name: %s\n", tokenSecretName)
	fmt.Fprintf(&b, "    key: %s\n", tokenSecretKey)
	fmt.Fprintf(&b, "  replicas: 1\n")
	if cfg.Provider != "" {
		fmt.Fprintf(&b, "  ciProvider: %q\n", cfg.Provider)
	}
	if cfg.EnableSnapshotDeploy {
		fmt.Fprintf(&b, "  enableSnapshotDeploy: true\n")
		fmt.Fprintf(&b, "  localClusterName: %q\n", cfg.ClusterName)
	}
	if cfg.BuildAgentImage != "" {
		fmt.Fprintf(&b, "  buildAgentImage: %q\n", cfg.BuildAgentImage)
	}
	if len(cfg.BuildAgentEnv) > 0 {
		fmt.Fprintf(&b, "  buildAgentEnv:\n")
		for _, e := range cfg.BuildAgentEnv {
			fmt.Fprintf(&b, "    - name: %s\n", e.Name)
			fmt.Fprintf(&b, "      valueFrom:\n")
			fmt.Fprintf(&b, "        secretKeyRef:\n")
			fmt.Fprintf(&b, "          name: %s\n", e.SecretName)
			fmt.Fprintf(&b, "          key: %s\n", e.SecretKey)
		}
	}
	fmt.Fprintf(&b, "  labels:\n")
	fmt.Fprintf(&b, "    - kindling\n")
	return b.String()
}

// ResetRunners deletes all CIRunnerPool CRs and the CI token secret.
// Returns a slice of output messages.
func ResetRunners(clusterName, namespace, providerName string) ([]string, error) {
	if namespace == "" {
		namespace = "default"
	}
	var outputs []string

	provider := ci.Default()
	if providerName != "" {
		if p, err := ci.Get(providerName); err == nil {
			provider = p
		}
	}
	labels := provider.CLILabels()

	out, err := Kubectl(clusterName, "delete", labels.CRDPlural, "--all", "-n", namespace)
	if err == nil {
		outputs = append(outputs, out)
	}
	out2, _ := Kubectl(clusterName, "delete", "secret", labels.SecretName,
		"-n", namespace, "--ignore-not-found")
	outputs = append(outputs, out2)

	return outputs, nil
}

// WaitForRunnerDeployment polls until the runner deployment appears and rolls out.
// deployName should be like "deployment/<username>-runner".
func WaitForRunnerDeployment(clusterName, deployName string, timeoutSeconds int) error {
	for i := 0; i < timeoutSeconds/2; i++ {
		if _, err := Kubectl(clusterName, "get", deployName); err == nil {
			break
		}
		if i == timeoutSeconds/2-1 {
			return fmt.Errorf("timed out waiting for %s to be created", deployName)
		}
	}

	_, err := Kubectl(clusterName, "rollout", "status", deployName,
		"--timeout="+fmt.Sprintf("%ds", timeoutSeconds))
	if err != nil {
		return fmt.Errorf("runner rollout failed: %w", err)
	}
	return nil
}

// ListRunnerPools returns the output of listing runner pools.
func ListRunnerPools(clusterName, providerName string) (string, error) {
	provider := ci.Default()
	if providerName != "" {
		if p, err := ci.Get(providerName); err == nil {
			provider = p
		}
	}
	return Kubectl(clusterName, "get", provider.CLILabels().CRDPlural,
		"-o", "custom-columns=NAME:.metadata.name,REPO:.spec.repository,USER:.spec.githubUsername",
		"--no-headers")
}

// RunnerPoolsExist returns true if any runner pools exist.
func RunnerPoolsExist(clusterName string) bool {
	out, err := ListRunnerPools(clusterName, "")
	return err == nil && strings.TrimSpace(out) != ""
}
