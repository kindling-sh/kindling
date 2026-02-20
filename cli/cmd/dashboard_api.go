package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ── /api/cluster — cluster overview ─────────────────────────────

func handleCluster(w http.ResponseWriter, r *http.Request) {
	type clusterInfo struct {
		Name     string      `json:"name"`
		Exists   bool        `json:"exists"`
		Context  string      `json:"context"`
		Operator interface{} `json:"operator,omitempty"`
		Registry interface{} `json:"registry,omitempty"`
	}

	info := clusterInfo{
		Name:    clusterName,
		Context: "kind-" + clusterName,
	}

	// Check cluster exists
	out, err := runCapture("kind", "get", "clusters")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) == clusterName {
				info.Exists = true
				break
			}
		}
	}

	if !info.Exists {
		jsonResponse(w, info)
		return
	}

	// Operator status
	opJSON, err := kubectlJSON("get", "deployment", "kindling-controller-manager",
		"-n", "kindling-system", "-o", "json")
	if err == nil {
		var dep interface{}
		json.Unmarshal([]byte(opJSON), &dep)
		info.Operator = dep
	}

	// Registry status
	regJSON, err := kubectlJSON("get", "deployment", "registry",
		"-n", "default", "-o", "json")
	if err == nil {
		var dep interface{}
		json.Unmarshal([]byte(regJSON), &dep)
		info.Registry = dep
	}

	jsonResponse(w, info)
}

// ── /api/nodes ──────────────────────────────────────────────────

func handleNodes(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "nodes", "-o", "json")
	if err != nil {
		jsonError(w, "failed to get nodes: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/operator ───────────────────────────────────────────────

func handleOperator(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "deployment", "kindling-controller-manager",
		"-n", "kindling-system", "-o", "json")
	if err != nil {
		jsonError(w, "operator not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/registry ───────────────────────────────────────────────

func handleRegistry(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "deployment", "registry",
		"-n", "default", "-o", "json")
	if err != nil {
		jsonError(w, "registry not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/ingress-controller ─────────────────────────────────────

func handleIngressController(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "pods", "-n", "ingress-nginx",
		"-l", "app.kubernetes.io/component=controller", "-o", "json")
	if err != nil {
		jsonError(w, "ingress controller not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/dses — DevStagingEnvironments ──────────────────────────

func handleDSEs(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "default"
	}

	out, err := kubectlJSON("get", "devstagingenvironments", "-n", ns, "-o", "json")
	if err != nil {
		// CRD might not exist yet
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/runners — GithubActionRunnerPools ──────────────────────

func handleRunners(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "githubactionrunnerpools", "-o", "json")
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/deployments ────────────────────────────────────────────

func handleDeployments(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	args := []string{"get", "deployments", "-o", "json"}
	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "--all-namespaces")
	}

	out, err := kubectlJSON(args...)
	if err != nil {
		jsonError(w, "failed to get deployments: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/pods ───────────────────────────────────────────────────

func handlePods(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	labelSelector := r.URL.Query().Get("selector")
	args := []string{"get", "pods", "-o", "json"}

	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "--all-namespaces")
	}
	if labelSelector != "" {
		args = append(args, "-l", labelSelector)
	}

	out, err := kubectlJSON(args...)
	if err != nil {
		jsonError(w, "failed to get pods: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/services ───────────────────────────────────────────────

func handleServices(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	args := []string{"get", "services", "-o", "json"}
	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "--all-namespaces")
	}

	out, err := kubectlJSON(args...)
	if err != nil {
		jsonError(w, "failed to get services: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/ingresses ──────────────────────────────────────────────

func handleIngresses(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	args := []string{"get", "ingresses", "-o", "json"}
	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "--all-namespaces")
	}

	out, err := kubectlJSON(args...)
	if err != nil {
		jsonError(w, "failed to get ingresses: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/secrets ────────────────────────────────────────────────

func handleSecrets(w http.ResponseWriter, r *http.Request) {
	// Only show kindling-managed secrets (never expose values)
	out, err := kubectlJSON("get", "secrets", "-n", "default",
		"-l", "app.kubernetes.io/managed-by=kindling", "-o", "json")
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
		return
	}

	// Strip data values for security — only show keys
	var secretList map[string]interface{}
	if err := json.Unmarshal([]byte(out), &secretList); err == nil {
		if items, ok := secretList["items"].([]interface{}); ok {
			for _, item := range items {
				if secret, ok := item.(map[string]interface{}); ok {
					if data, ok := secret["data"].(map[string]interface{}); ok {
						redacted := make(map[string]interface{})
						for k := range data {
							redacted[k] = "••••••••"
						}
						secret["data"] = redacted
					}
				}
			}
		}
	}

	jsonResponse(w, secretList)
}

// ── /api/events ─────────────────────────────────────────────────

func handleEvents(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	args := []string{"get", "events", "--sort-by=.lastTimestamp", "-o", "json"}
	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "--all-namespaces")
	}

	out, err := kubectlJSON(args...)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/namespaces ─────────────────────────────────────────────

func handleNamespaces(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "namespaces", "-o", "json")
	if err != nil {
		jsonError(w, "failed to get namespaces: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/logs/{namespace}/{pod} ─────────────────────────────────

func handleLogs(w http.ResponseWriter, r *http.Request) {
	// URL format: /api/logs/{namespace}/{pod}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/logs/"), "/")
	if len(parts) < 2 {
		jsonError(w, "usage: /api/logs/{namespace}/{pod}", 400)
		return
	}
	ns := parts[0]
	pod := parts[1]
	container := r.URL.Query().Get("container")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}

	args := []string{"logs", pod, "-n", ns, "--tail=" + tail}
	if container != "" {
		args = append(args, "-c", container)
	}

	out, err := kubectlJSON(args...)
	if err != nil {
		jsonError(w, "failed to get logs: "+err.Error(), 500)
		return
	}

	jsonResponse(w, map[string]string{"logs": out})
}
