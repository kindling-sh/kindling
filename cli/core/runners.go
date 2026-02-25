package core

import (
	"fmt"
	"strings"

	"github.com/jeffvincent/kindling/pkg/ci"
)

// RunnerPoolConfig holds the parameters for creating a GitHub Actions runner pool.
type RunnerPoolConfig struct {
	ClusterName string
	Username    string
	Repo        string
	Token       string
	Namespace   string // defaults to "default"
}

func (c *RunnerPoolConfig) namespace() string {
	if c.Namespace == "" {
		return "default"
	}
	return c.Namespace
}

// CreateRunnerPool creates the github-runner-token secret and applies a
// GithubActionRunnerPool CR. Returns a slice of output messages.
func CreateRunnerPool(cfg RunnerPoolConfig) ([]string, error) {
	ns := cfg.namespace()
	var outputs []string
	labels := ci.Default().CLILabels()

	// 1. Create/update token secret
	Kubectl(cfg.ClusterName, "delete", "secret", labels.SecretName,
		"-n", ns, "--ignore-not-found")

	secretYAML, err := RunCapture("kubectl", "create", "secret", "generic", labels.SecretName,
		"--from-literal="+ci.Default().Runner().DefaultTokenKey()+"="+cfg.Token,
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
	crYAML := fmt.Sprintf(`apiVersion: apps.example.com/v1alpha1
kind: %s
metadata:
  name: %%s-runner-pool
  namespace: %%s
spec:
  githubUsername: "%%s"
  repository: "%%s"
  tokenSecretRef:
    name: %s
    key: %s
  replicas: 1
  labels:
    - kindling
`, labels.CRDKind, labels.SecretName, ci.Default().Runner().DefaultTokenKey())
	crYAML = fmt.Sprintf(crYAML, cfg.Username, ns, cfg.Username, cfg.Repo)

	out, err = KubectlApplyStdin(cfg.ClusterName, crYAML)
	if err != nil {
		return nil, fmt.Errorf("failed to apply runner pool: %s", out)
	}
	outputs = append(outputs, out)

	return outputs, nil
}

// ResetRunners deletes all GithubActionRunnerPool CRs and the token secret.
// Returns a slice of output messages.
func ResetRunners(clusterName, namespace string) ([]string, error) {
	if namespace == "" {
		namespace = "default"
	}
	var outputs []string
	labels := ci.Default().CLILabels()

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
func ListRunnerPools(clusterName string) (string, error) {
	return Kubectl(clusterName, "get", ci.Default().CLILabels().CRDPlural,
		"-o", "custom-columns=NAME:.metadata.name,REPO:.spec.repository,USER:.spec.githubUsername",
		"--no-headers")
}

// RunnerPoolsExist returns true if any runner pools exist.
func RunnerPoolsExist(clusterName string) bool {
	out, err := ListRunnerPools(clusterName)
	return err == nil && strings.TrimSpace(out) != ""
}
