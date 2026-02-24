package core

import (
	"fmt"
	"strings"
)

const (
	// SecretsLabelKey marks secrets as managed by kindling.
	SecretsLabelKey = "app.kubernetes.io/managed-by"
	// SecretsLabelValue is the label value for kindling-managed secrets.
	SecretsLabelValue = "kindling"
)

// SecretConfig holds parameters for creating a Kubernetes secret.
type SecretConfig struct {
	ClusterName string
	Name        string // logical name (e.g. "STRIPE_KEY")
	Value       string
	Namespace   string // defaults to "default"
}

func (c *SecretConfig) namespace() string {
	if c.Namespace == "" {
		return "default"
	}
	return c.Namespace
}

// KindlingSecretName returns the K8s Secret name for a given logical secret name.
// e.g. "STRIPE_API_KEY" → "kindling-secret-stripe-api-key"
func KindlingSecretName(name string) string {
	clean := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	return "kindling-secret-" + clean
}

// CreateSecret creates or updates a Kubernetes Secret in the cluster.
// It uses the kindling naming convention (kindling-secret-<name>) and labels
// the secret with app.kubernetes.io/managed-by=kindling.
func CreateSecret(cfg SecretConfig) (string, error) {
	ns := cfg.namespace()
	k8sName := KindlingSecretName(cfg.Name)

	// Delete existing if present (kubectl create secret doesn't support update)
	Kubectl(cfg.ClusterName, "delete", "secret", k8sName,
		"-n", ns, "--ignore-not-found")

	// Create the secret
	out, err := Kubectl(cfg.ClusterName, "create", "secret", "generic", k8sName,
		"--from-literal="+cfg.Name+"="+cfg.Value,
		"--from-literal=value="+cfg.Value,
		"-n", ns)
	if err != nil {
		return out, fmt.Errorf("failed to create K8s secret: %w", err)
	}

	// Label it
	Kubectl(cfg.ClusterName, "label", "secret", k8sName,
		"-n", ns,
		SecretsLabelKey+"="+SecretsLabelValue,
		"--overwrite")

	return out, nil
}

// DeleteSecret removes a Kubernetes Secret from the cluster.
func DeleteSecret(clusterName, name, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	k8sName := KindlingSecretName(name)
	return Kubectl(clusterName, "delete", "secret", k8sName,
		"-n", namespace, "--ignore-not-found")
}

// DeleteSecretByK8sName removes a secret by its raw Kubernetes name.
func DeleteSecretByK8sName(clusterName, k8sName, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "delete", "secret", k8sName, "-n", namespace)
}

// ListSecrets returns the names of all kindling-managed secrets in the cluster.
func ListSecrets(clusterName, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "get", "secrets",
		"-n", namespace,
		"-l", SecretsLabelKey+"="+SecretsLabelValue,
		"-o", "custom-columns=NAME:.metadata.name,KEYS:.data,AGE:.metadata.creationTimestamp",
		"--no-headers")
}

// GetSecretKeys returns the key names from a secret's data field.
func GetSecretKeys(clusterName, secretName, namespace string) ([]string, error) {
	if namespace == "" {
		namespace = "default"
	}
	keys, err := Kubectl(clusterName, "get", "secret", secretName,
		"-n", namespace,
		"-o", "jsonpath={.data}")
	if err != nil {
		return nil, err
	}
	return ParseSecretKeys(keys), nil
}

// ParseSecretKeys extracts key names from a kubectl JSON data output like
// map[KEY1:base64... KEY2:base64...]
func ParseSecretKeys(jsonData string) []string {
	jsonData = strings.TrimPrefix(jsonData, "map[")
	jsonData = strings.TrimSuffix(jsonData, "]")
	if jsonData == "" {
		return nil
	}
	var keys []string
	for _, pair := range strings.Fields(jsonData) {
		if idx := strings.Index(pair, ":"); idx > 0 {
			keys = append(keys, pair[:idx])
		}
	}
	return keys
}
