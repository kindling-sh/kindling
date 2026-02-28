package core

import (
	"fmt"
	"strings"

	"github.com/jeffvincent/kindling/pkg/ci"
)

// RunnerPoolConfig holds the parameters for creating a CI runner pool.
type RunnerPoolConfig struct {
	ClusterName string
	Username    string
	Repo        string
	Token       string
	Namespace   string // defaults to "default"
	Provider    string // ci provider name ("github", "gitlab"); empty = default
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
	ciProviderField := ""
	if cfg.Provider != "" {
		ciProviderField = fmt.Sprintf("  ciProvider: %q\n", cfg.Provider)
	}

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

	crYAML := fmt.Sprintf(`apiVersion: apps.example.com/v1alpha1
kind: %s
metadata:
  name: %%s-runner-pool
  namespace: %%s
spec:
  githubUsername: "%%s"
  repository: "%%s"
  githubURL: "%s"
  runnerImage: "%s"
  tokenSecretRef:
    name: %s
    key: %s
  replicas: 1
%s  labels:
    - kindling
`, labels.CRDKind, platformURL, runnerImage, labels.SecretName, provider.Runner().DefaultTokenKey(), ciProviderField)
	crYAML = fmt.Sprintf(crYAML, safeName, ns, cfg.Username, cfg.Repo)

	out, err = KubectlApplyStdin(cfg.ClusterName, crYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to apply runner pool: %s", out)
	}
	poolName := fmt.Sprintf("%s-runner-pool", safeName)
	outputs = append(outputs, fmt.Sprintf("%s runner pool %s created", provider.DisplayName(), poolName))

	return outputs, nil
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
