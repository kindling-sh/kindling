// Package core provides shared business logic used by both the CLI commands
// and the dashboard API handlers. Functions in this package return structured
// results rather than printing to stdout/stderr, leaving output formatting
// to the caller.
package core

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ClusterContext returns the kubectl --context value for a Kind cluster.
func ClusterContext(clusterName string) string {
	return "kind-" + clusterName
}

// Kubectl runs a kubectl command against the named Kind cluster and returns
// combined stdout. The --context flag is injected automatically.
func Kubectl(clusterName string, args ...string) (string, error) {
	full := append([]string{"--context", ClusterContext(clusterName)}, args...)
	return RunCapture("kubectl", full...)
}

// KubectlApplyStdin pipes the given YAML to `kubectl apply -f -` with the
// correct cluster context.
func KubectlApplyStdin(clusterName, yaml string) (string, error) {
	cmd := exec.Command("kubectl", "--context", ClusterContext(clusterName), "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// RunCapture executes a command and returns stdout only (trimmed).
func RunCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), err
}

// RunSilent executes a command and returns combined stdout+stderr (trimmed).
func RunSilent(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// CommandExists checks if a binary is on PATH.
func CommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ClusterExists checks whether a Kind cluster with the given name exists.
func ClusterExists(name string) bool {
	out, err := RunCapture("kind", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// DestroyCluster deletes a Kind cluster by name.
func DestroyCluster(name string) (string, error) {
	if !ClusterExists(name) {
		return "", fmt.Errorf("cluster %q does not exist", name)
	}
	return RunSilent("kind", "delete", "cluster", "--name", name)
}
