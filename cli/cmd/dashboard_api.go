package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ── Generic resource handlers ───────────────────────────────────

// resourceHandler returns an http.HandlerFunc that fetches a Kubernetes
// resource type via kubectl. Options control namespace filtering and error
// behaviour. This replaces 13 near-identical handler functions.
type resourceOpts struct {
	// If true, reads ?namespace from the query and scopes to it, otherwise
	// uses --all-namespaces. Cluster-scoped resources leave this false.
	namespaced bool
	// If true, also reads ?selector for label filtering.
	selectable bool
	// Extra args appended before -o json (e.g. "--sort-by=.lastTimestamp").
	extraArgs []string
	// If true, return {"items":[]} on error instead of a 500.
	emptyOnError bool
}

func resourceHandler(resource string, opts resourceOpts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		args := []string{"get", resource}
		args = append(args, opts.extraArgs...)
		args = append(args, "-o", "json")

		if opts.namespaced {
			if ns := r.URL.Query().Get("namespace"); ns != "" {
				args = append(args, "-n", ns)
			} else {
				args = append(args, "--all-namespaces")
			}
		}
		if opts.selectable {
			if sel := r.URL.Query().Get("selector"); sel != "" {
				args = append(args, "-l", sel)
			}
		}

		out, err := kubectlJSON(args...)
		if err != nil {
			if opts.emptyOnError {
				jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
			} else {
				jsonError(w, "failed to get "+resource+": "+err.Error(), 500)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, out)
	}
}

// parsePathParams extracts path segments after a prefix.
// e.g. parsePathParams("/api/logs/default/my-pod", "/api/logs/", 2)
// returns ["default", "my-pod"], nil.
func parsePathParams(path, prefix string, minCount int) ([]string, error) {
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < minCount {
		return nil, fmt.Errorf("expected at least %d path segments", minCount)
	}
	return parts, nil
}

// Convenience constructors used by the route table in dashboard.go.
var (
	handleNodes               = resourceHandler("nodes", resourceOpts{})
	handleNamespaces          = resourceHandler("namespaces", resourceOpts{})
	handleClusterRoles        = resourceHandler("clusterroles", resourceOpts{emptyOnError: true})
	handleClusterRoleBindings = resourceHandler("clusterrolebindings", resourceOpts{emptyOnError: true})
	handleDeployments         = resourceHandler("deployments", resourceOpts{namespaced: true})
	handleServices            = resourceHandler("services", resourceOpts{namespaced: true})
	handleIngresses           = resourceHandler("ingresses", resourceOpts{namespaced: true})
	handleServiceAccounts     = resourceHandler("serviceaccounts", resourceOpts{namespaced: true})
	handleRoles               = resourceHandler("roles", resourceOpts{namespaced: true, emptyOnError: true})
	handleRoleBindings        = resourceHandler("rolebindings", resourceOpts{namespaced: true, emptyOnError: true})
	handleReplicaSets         = resourceHandler("replicasets", resourceOpts{namespaced: true, selectable: true})
	handlePods                = resourceHandler("pods", resourceOpts{namespaced: true, selectable: true})
	handleEvents              = resourceHandler("events", resourceOpts{namespaced: true, emptyOnError: true, extraArgs: []string{"--sort-by=.lastTimestamp"}})
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
		Context: kindContext(),
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

// ── /api/runners — CIRunnerPools ──────────────────────

func handleRunners(w http.ResponseWriter, r *http.Request) {
	prov, _ := resolveProvider(r.URL.Query().Get("ci-provider"))
	labels := prov.CLILabels()
	out, err := kubectlJSON("get", labels.CRDPlural, "-o", "json")
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
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

// ── /api/logs/{namespace}/{pod} ─────────────────────────────────

func handleLogs(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/logs/", 2)
	if err != nil {
		jsonError(w, "usage: /api/logs/{namespace}/{pod}", 400)
		return
	}
	ns, pod := parts[0], parts[1]
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

// ── /api/runtime/{namespace}/{deployment} ───────────────────────
// Detects the language/runtime of a deployment by inspecting the running
// container and returns whether hot-sync is supported.

func handleRuntimeDetect(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/runtime/", 2)
	if err != nil {
		jsonError(w, "usage: /api/runtime/{namespace}/{deployment}", 400)
		return
	}
	ns, deployment := parts[0], parts[1]

	// Optional query param: src directory for local language detection
	srcDir := r.URL.Query().Get("src")

	type runtimeInfo struct {
		Runtime       string `json:"runtime"`
		Mode          string `json:"mode"`
		SyncSupported bool   `json:"sync_supported"`
		Strategy      string `json:"strategy"`
		Language      string `json:"language"`
		IsFrontend    bool   `json:"is_frontend"`
		Container     string `json:"container"`
		DefaultDest   string `json:"default_dest"`
	}

	info := runtimeInfo{
		Runtime:     "unknown",
		Mode:        "unknown",
		DefaultDest: "/app",
	}

	// Try to find the pod for this deployment
	pod, err := findPodForDeployment(deployment, ns)
	if err != nil {
		// Can't find pod — still return partial info from source detection
		if srcDir != "" {
			info.Language = detectLanguageFromSource(srcDir)
			info.IsFrontend = isFrontendProject(srcDir)
		}
		jsonResponse(w, info)
		return
	}

	// Detect which container to target (first non-init container)
	containerName := ""
	depJSON, _ := kubectlJSON("get", "deployment", deployment, "-n", ns, "-o", "json")
	if depJSON != "" {
		var dep struct {
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name string `json:"name"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		if json.Unmarshal([]byte(depJSON), &dep) == nil && len(dep.Spec.Template.Spec.Containers) > 0 {
			containerName = dep.Spec.Template.Spec.Containers[0].Name
		}
	}
	info.Container = containerName

	// Detect runtime from the running container
	profile, cmdline := detectRuntime(pod, ns, containerName)
	info.Runtime = profile.Name

	switch profile.Mode {
	case modeSignal:
		info.Mode = "signal"
		info.Strategy = "Send SIG" + profile.Signal + " to reload"
		info.SyncSupported = true
	case modeKill:
		info.Mode = "kill"
		info.Strategy = "Restart process via wrapper script"
		info.SyncSupported = true
	case modeNone:
		info.Mode = "none"
		info.Strategy = "Sync files — no restart needed"
		info.SyncSupported = true
	case modeRebuild:
		info.Mode = "compiled"
		info.Strategy = "Cross-compile locally, sync binary"
		info.SyncSupported = true
	}

	// Detect source language if src provided
	if srcDir != "" {
		absSrc, _ := filepath.Abs(srcDir)
		info.Language = detectLanguageFromSource(absSrc)
		info.IsFrontend = isFrontendProject(absSrc)
		if info.IsFrontend {
			info.Strategy = "Build locally, sync static assets to nginx"
			info.SyncSupported = true
		}
	}

	// If runtime is still unknown, try to auto-detect from local source directories
	// by matching the deployment name to a subdirectory of cwd.
	if info.Runtime == "unknown" || strings.HasPrefix(info.Runtime, "unknown (") {
		if info.Language == "" {
			cwd, _ := os.Getwd()

			// Build a set of name variants to match against directories.
			// DSE names are often prefixed (e.g. "jeff-vincent-gateway") while
			// the local directory is just "gateway".
			depLower := strings.ToLower(deployment)
			nameVariants := []string{depLower}
			// Add all dash-suffix segments: "jeff-vincent-gateway" → also try "vincent-gateway", "gateway"
			for i := 0; i < len(depLower); i++ {
				if depLower[i] == '-' && i+1 < len(depLower) {
					nameVariants = append(nameVariants, depLower[i+1:])
				}
			}
			// Also use the process name from the cmdline if available (e.g. "gateway" from the wrapper)
			if cmdline != "" {
				// Extract the inner command's basename
				innerFields := strings.Fields(cmdline)
				if len(innerFields) > 0 {
					procBase := filepath.Base(innerFields[0])
					nameVariants = append(nameVariants, strings.ToLower(procBase))
				}
			}

			// Scan cwd entries for matches
			var candidates []string
			if entries, err := os.ReadDir(cwd); err == nil {
				for _, e := range entries {
					if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
						continue
					}
					dirLower := strings.ToLower(e.Name())
					for _, variant := range nameVariants {
						if dirLower == variant || strings.Contains(dirLower, variant) || strings.Contains(variant, dirLower) {
							candidates = append(candidates, filepath.Join(cwd, e.Name()))
							break
						}
					}
				}
			}

			for _, dir := range candidates {
				if lang := detectLanguageFromSource(dir); lang != "" {
					info.Language = lang
					info.IsFrontend = isFrontendProject(dir)
					break
				}
			}
		}

		// If we found a language from source, apply the corresponding profile
		if info.Language != "" {
			if p, ok := runtimeTable[info.Language]; ok {
				info.Runtime = p.Name
				switch p.Mode {
				case modeSignal:
					info.Mode = "signal"
					info.Strategy = "Send SIG" + p.Signal + " to reload"
					info.SyncSupported = true
				case modeKill:
					info.Mode = "kill"
					info.Strategy = "Restart process via wrapper script"
					info.SyncSupported = true
				case modeNone:
					info.Mode = "none"
					info.Strategy = "Sync files — no restart needed"
					info.SyncSupported = true
				case modeRebuild:
					info.Mode = "compiled"
					info.Strategy = "Cross-compile locally, sync binary"
					info.SyncSupported = true
				}
			} else {
				// Language detected but no runtimeTable entry — still mark as syncable
				info.SyncSupported = true
				info.Strategy = "Sync files to container"
			}
		}

		if info.IsFrontend {
			info.Strategy = "Build locally, sync static assets to nginx"
			info.SyncSupported = true
		}
	}

	// Detect nginx → frontend likely
	if strings.Contains(cmdline, "nginx") {
		info.DefaultDest = "/usr/share/nginx/html"
		if info.Strategy == "" {
			info.Strategy = "Sync static files to nginx html root"
			info.SyncSupported = true
		}
	}

	// Default dest for compiled languages
	if profile.Name == "go" || profile.Name == "rust" || profile.Name == "cargo" {
		info.DefaultDest = "/app"
	}

	// If runtime is truly unknown and no source hints, mark unsupported
	if info.Runtime == "unknown" && info.Language == "" && !info.IsFrontend {
		info.SyncSupported = false
		info.Strategy = "Runtime not detected — use Load instead"
	}

	jsonResponse(w, info)
}

// ── /api/load-context — resolve local source directories ────────
// Returns a list of subdirectories in the working directory that look
// like service source roots (contain Dockerfile, go.mod, package.json, etc.)

func handleLoadContext(w http.ResponseWriter, r *http.Request) {
	type serviceDir struct {
		Name          string `json:"name"`
		Path          string `json:"path"`
		HasDockerfile bool   `json:"has_dockerfile"`
		Language      string `json:"language"`
	}

	cwd, _ := os.Getwd()
	entries, err := os.ReadDir(cwd)
	if err != nil {
		jsonError(w, "cannot read working directory", 500)
		return
	}

	var dirs []serviceDir
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirPath := filepath.Join(cwd, e.Name())

		// Check for Dockerfile
		_, hasDF := os.Stat(filepath.Join(dirPath, "Dockerfile"))

		// Detect language
		lang := detectLanguageFromSource(dirPath)

		if hasDF == nil || lang != "" {
			dirs = append(dirs, serviceDir{
				Name:          e.Name(),
				Path:          dirPath,
				HasDockerfile: hasDF == nil,
				Language:      lang,
			})
		}
	}

	jsonResponse(w, dirs)
}
