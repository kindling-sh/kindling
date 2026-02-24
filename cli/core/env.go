package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SetEnv sets one or more environment variables on a deployment.
// pairs should be in "KEY=VALUE" format.
func SetEnv(clusterName, deployment, namespace string, pairs []string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	args := []string{"set", "env", "deployment/" + deployment, "-n", namespace}
	args = append(args, pairs...)
	return Kubectl(clusterName, args...)
}

// UnsetEnv removes one or more environment variables from a deployment.
// keys should be bare key names (e.g. "DEBUG"); the trailing "-" is added automatically.
func UnsetEnv(clusterName, deployment, namespace string, keys []string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	args := []string{"set", "env", "deployment/" + deployment, "-n", namespace}
	for _, k := range keys {
		args = append(args, k+"-")
	}
	return Kubectl(clusterName, args...)
}

// EnvVar represents a single environment variable.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ListEnv returns the environment variables set on a deployment's first container.
func ListEnv(clusterName, deployment, namespace string) ([]EnvVar, error) {
	if namespace == "" {
		namespace = "default"
	}

	out, err := Kubectl(clusterName, "get", "deployment", deployment,
		"-n", namespace, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("deployment not found: %s", deployment)
	}

	var dep map[string]interface{}
	if err := json.Unmarshal([]byte(out), &dep); err != nil {
		return nil, fmt.Errorf("failed to parse deployment JSON: %w", err)
	}

	var envVars []EnvVar

	if spec, ok := dep["spec"].(map[string]interface{}); ok {
		if tmpl, ok := spec["template"].(map[string]interface{}); ok {
			if tspec, ok := tmpl["spec"].(map[string]interface{}); ok {
				if containers, ok := tspec["containers"].([]interface{}); ok && len(containers) > 0 {
					if c, ok := containers[0].(map[string]interface{}); ok {
						if env, ok := c["env"].([]interface{}); ok {
							for _, e := range env {
								if ev, ok := e.(map[string]interface{}); ok {
									name, _ := ev["name"].(string)
									value, _ := ev["value"].(string)
									envVars = append(envVars, EnvVar{Name: name, Value: value})
								}
							}
						}
					}
				}
			}
		}
	}

	return envVars, nil
}

// DeploymentExists checks whether a deployment exists in the given namespace.
func DeploymentExists(clusterName, deployment, namespace string) bool {
	if namespace == "" {
		namespace = "default"
	}
	_, err := Kubectl(clusterName, "get", "deployment/"+deployment, "-n", namespace)
	return err == nil
}

// RestartDeployment triggers a rolling restart of a deployment.
func RestartDeployment(clusterName, deployment, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "rollout", "restart", "deployment/"+deployment, "-n", namespace)
}

// ScaleDeployment scales a deployment to the specified number of replicas.
func ScaleDeployment(clusterName, deployment, namespace string, replicas int) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "scale", "deployment/"+deployment, "-n", namespace,
		fmt.Sprintf("--replicas=%d", replicas))
}

// DeletePod deletes a pod by name.
func DeletePod(clusterName, pod, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "delete", "pod", pod, "-n", namespace)
}

// DeleteDSE deletes a DevStagingEnvironment by name.
func DeleteDSE(clusterName, name, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "delete", "devstagingenvironment", name, "-n", namespace)
}

// ApplyYAML runs kubectl apply with the given YAML content.
func ApplyYAML(clusterName, yaml string) (string, error) {
	return KubectlApplyStdin(clusterName, yaml)
}

// GetEnvJSONPath returns env vars from a deployment using jsonpath (for CLI display).
func GetEnvJSONPath(clusterName, deployment, namespace string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}
	return Kubectl(clusterName, "get", "deployment/"+deployment,
		"-n", namespace,
		"-o", "jsonpath={range .spec.template.spec.containers[0].env[*]}{.name}={.value}{\"\\n\"}{end}")
}

// ValidateEnvPairs checks that all strings are in KEY=VALUE format.
func ValidateEnvPairs(pairs []string) error {
	for _, p := range pairs {
		if !strings.Contains(p, "=") {
			return fmt.Errorf("invalid format %q — expected KEY=VALUE", p)
		}
	}
	return nil
}
