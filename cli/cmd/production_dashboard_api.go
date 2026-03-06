package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jeffvincent/kindling/cli/core"
)

// ── Production context & kubectl ────────────────────────────────

// prodContext is set by the --prod-context flag on the dashboard command.
// If empty, production API routes will attempt auto-detection.
var prodContext string

// prodKubectlJSON runs kubectl against the production cluster context.
func prodKubectlJSON(args ...string) (string, error) {
	if prodContext == "" {
		return "", fmt.Errorf("no production context configured")
	}
	full := append([]string{"--context", prodContext}, args...)
	return core.RunCapture("kubectl", full...)
}

// ── Generic resource handler (production) ───────────────────────

func prodResourceHandler(resource string, opts resourceOpts) http.HandlerFunc {
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

		out, err := prodKubectlJSON(args...)
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

// Pre-built production resource handlers.
var (
	handleProdNodes               = prodResourceHandler("nodes", resourceOpts{})
	handleProdNamespaces          = prodResourceHandler("namespaces", resourceOpts{})
	handleProdDeployments         = prodResourceHandler("deployments", resourceOpts{namespaced: true})
	handleProdPods                = prodResourceHandler("pods", resourceOpts{namespaced: true, selectable: true})
	handleProdServices            = prodResourceHandler("services", resourceOpts{namespaced: true})
	handleProdIngresses           = prodResourceHandler("ingresses", resourceOpts{namespaced: true})
	handleProdEvents              = prodResourceHandler("events", resourceOpts{namespaced: true, emptyOnError: true, extraArgs: []string{"--sort-by=.lastTimestamp"}})
	handleProdSecrets             = prodResourceHandler("secrets", resourceOpts{namespaced: true, emptyOnError: true})
	handleProdStatefulSets        = prodResourceHandler("statefulsets", resourceOpts{namespaced: true, emptyOnError: true})
	handleProdDaemonSets          = prodResourceHandler("daemonsets", resourceOpts{namespaced: true, emptyOnError: true})
	handleProdReplicaSets         = prodResourceHandler("replicasets", resourceOpts{namespaced: true, selectable: true})
	handleProdClusterRoles        = prodResourceHandler("clusterroles", resourceOpts{emptyOnError: true})
	handleProdClusterRoleBindings = prodResourceHandler("clusterrolebindings", resourceOpts{emptyOnError: true})
)

// ── /api/prod/cluster — production cluster overview ─────────────

func handleProdCluster(w http.ResponseWriter, r *http.Request) {
	type prodClusterInfo struct {
		Context    string      `json:"context"`
		Connected  bool        `json:"connected"`
		Provider   string      `json:"provider"`
		Version    string      `json:"version"`
		Nodes      int         `json:"nodes"`
		Prometheus bool        `json:"prometheus"`
		CertMgr    bool        `json:"cert_manager"`
		Traefik    interface{} `json:"traefik,omitempty"`
	}

	info := prodClusterInfo{Context: prodContext}

	// Check connectivity
	_, err := prodKubectlJSON("cluster-info")
	if err != nil {
		info.Connected = false
		jsonResponse(w, info)
		return
	}
	info.Connected = true

	// Server version
	if out, err := prodKubectlJSON("version", "-o", "json"); err == nil {
		var v struct {
			ServerVersion struct {
				GitVersion string `json:"gitVersion"`
			} `json:"serverVersion"`
		}
		if json.Unmarshal([]byte(out), &v) == nil {
			info.Version = v.ServerVersion.GitVersion
		}
	}

	// Detect provider from context name
	ctx := strings.ToLower(prodContext)
	switch {
	case strings.Contains(ctx, "do-"):
		info.Provider = "DigitalOcean"
	case strings.Contains(ctx, "gke_"):
		info.Provider = "Google GKE"
	case strings.Contains(ctx, "arn:aws"):
		info.Provider = "AWS EKS"
	case strings.Contains(ctx, "aks"):
		info.Provider = "Azure AKS"
	case strings.Contains(ctx, "kind-"):
		info.Provider = "Kind"
	default:
		info.Provider = "Kubernetes"
	}

	// Node count
	if out, err := prodKubectlJSON("get", "nodes", "-o", "json"); err == nil {
		var list struct {
			Items []interface{} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil {
			info.Nodes = len(list.Items)
		}
	}

	// Detect Prometheus
	_, promErr := prodKubectlJSON("get", "svc", "-n", "monitoring", "prometheus-server", "--ignore-not-found")
	if promErr == nil {
		// Also check prometheus-operated (kube-prometheus-stack)
		if out, _ := prodKubectlJSON("get", "svc", "-n", "monitoring", "--ignore-not-found", "-o", "json"); out != "" {
			if strings.Contains(out, "prometheus") {
				info.Prometheus = true
			}
		}
	}
	// Try prometheus namespace too
	if !info.Prometheus {
		if out, _ := prodKubectlJSON("get", "svc", "-n", "prometheus", "--ignore-not-found", "-o", "json"); out != "" {
			if strings.Contains(out, "prometheus") {
				info.Prometheus = true
			}
		}
	}
	// Try default namespace
	if !info.Prometheus {
		if out, _ := prodKubectlJSON("get", "svc", "--all-namespaces", "-l", "app=prometheus", "--ignore-not-found", "-o", "json"); out != "" {
			if strings.Contains(out, "prometheus") {
				info.Prometheus = true
			}
		}
	}

	// Detect cert-manager
	_, certErr := prodKubectlJSON("get", "namespace", "cert-manager", "--ignore-not-found")
	if certErr == nil {
		if out, _ := prodKubectlJSON("get", "deployment", "-n", "cert-manager", "cert-manager", "--ignore-not-found"); out != "" {
			info.CertMgr = true
		}
	}

	// Detect Traefik
	if out, err := prodKubectlJSON("get", "pods", "-n", "traefik",
		"-l", "app.kubernetes.io/name=traefik", "-o", "json"); err == nil {
		var pods interface{}
		json.Unmarshal([]byte(out), &pods)
		info.Traefik = pods
	}

	jsonResponse(w, info)
}

// ── /api/prod/contexts — list available kubeconfig contexts ─────

func handleProdContexts(w http.ResponseWriter, r *http.Request) {
	out, err := core.RunCapture("kubectl", "config", "get-contexts", "-o", "name")
	if err != nil {
		jsonError(w, "failed to list contexts", 500)
		return
	}
	var contexts []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "kind-") {
			contexts = append(contexts, line)
		}
	}
	jsonResponse(w, contexts)
}

// ── /api/prod/logs/{namespace}/{pod} ────────────────────────────

func handleProdLogs(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/prod/logs/", 2)
	if err != nil {
		jsonError(w, "usage: /api/prod/logs/{namespace}/{pod}", 400)
		return
	}
	ns, pod := parts[0], parts[1]
	container := r.URL.Query().Get("container")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "200"
	}

	args := []string{"logs", pod, "-n", ns, "--tail=" + tail}
	if container != "" {
		args = append(args, "-c", container)
	}

	out, err := prodKubectlJSON(args...)
	if err != nil {
		jsonError(w, "failed to get logs: "+err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]string{"logs": out})
}

// ── /api/prod/restart/{namespace}/{deployment} ──────────────────

func handleProdRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/prod/restart/", 2)
	if err != nil {
		jsonError(w, "usage: POST /api/prod/restart/{namespace}/{deployment}", 400)
		return
	}
	ns, dep := parts[0], parts[1]
	_, err = prodKubectlJSON("rollout", "restart", "deployment/"+dep, "-n", ns)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	jsonResponse(w, map[string]interface{}{"ok": true, "output": fmt.Sprintf("Restarted %s/%s", ns, dep)})
}

// ── /api/prod/scale/{namespace}/{deployment} ────────────────────

func handleProdScale(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/prod/scale/", 2)
	if err != nil {
		jsonError(w, "usage: POST /api/prod/scale/{namespace}/{deployment}", 400)
		return
	}
	ns, dep := parts[0], parts[1]

	var body struct {
		Replicas int `json:"replicas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}

	_, err = prodKubectlJSON("scale", "deployment/"+dep, "-n", ns,
		fmt.Sprintf("--replicas=%d", body.Replicas))
	if err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	jsonResponse(w, map[string]interface{}{"ok": true, "output": fmt.Sprintf("Scaled %s to %d", dep, body.Replicas)})
}

// ── /api/prod/delete-pod/{namespace}/{pod} ──────────────────────

func handleProdDeletePod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, "method not allowed", 405)
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/prod/delete-pod/", 2)
	if err != nil {
		jsonError(w, "usage: DELETE /api/prod/delete-pod/{namespace}/{pod}", 400)
		return
	}
	ns, pod := parts[0], parts[1]
	_, err = prodKubectlJSON("delete", "pod", pod, "-n", ns)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	jsonResponse(w, map[string]interface{}{"ok": true, "output": fmt.Sprintf("Deleted pod %s/%s", ns, pod)})
}

// ── /api/prod/rollout-history/{namespace}/{deployment} ──────────

func handleProdRolloutHistory(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/prod/rollout-history/", 2)
	if err != nil {
		jsonError(w, "usage: /api/prod/rollout-history/{namespace}/{deployment}", 400)
		return
	}
	ns, dep := parts[0], parts[1]

	out, err := prodKubectlJSON("rollout", "history", "deployment/"+dep, "-n", ns)
	if err != nil {
		jsonError(w, "rollout history failed: "+err.Error(), 500)
		return
	}

	// Parse the history output into structured data
	type revision struct {
		Revision string `json:"revision"`
		Change   string `json:"change_cause"`
	}
	var revisions []revision
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "REVISION") || strings.HasPrefix(line, "deployment") {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 0 {
			// Try space split
			fields = strings.Fields(line)
		}
		rev := revision{Revision: strings.TrimSpace(fields[0])}
		if len(fields) > 1 {
			rev.Change = strings.TrimSpace(fields[1])
		}
		if rev.Revision != "" {
			revisions = append(revisions, rev)
		}
	}

	jsonResponse(w, map[string]interface{}{"items": revisions, "raw": out})
}

// ── /api/prod/rollback/{namespace}/{deployment} ─────────────────

func handleProdRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/prod/rollback/", 2)
	if err != nil {
		jsonError(w, "usage: POST /api/prod/rollback/{namespace}/{deployment}", 400)
		return
	}
	ns, dep := parts[0], parts[1]

	var body struct {
		Revision int `json:"revision"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	args := []string{"rollout", "undo", "deployment/" + dep, "-n", ns}
	if body.Revision > 0 {
		args = append(args, fmt.Sprintf("--to-revision=%d", body.Revision))
	}

	out, err := prodKubectlJSON(args...)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	jsonResponse(w, map[string]interface{}{"ok": true, "output": strings.TrimSpace(out)})
}

// ── /api/prod/rollout-status/{namespace}/{deployment} ───────────

func handleProdRolloutStatus(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/prod/rollout-status/", 2)
	if err != nil {
		jsonError(w, "usage: /api/prod/rollout-status/{namespace}/{deployment}", 400)
		return
	}
	ns, dep := parts[0], parts[1]

	out, err := prodKubectlJSON("rollout", "status", "deployment/"+dep, "-n", ns, "--timeout=2s")
	status := "progressing"
	if err == nil && strings.Contains(out, "successfully rolled out") {
		status = "complete"
	} else if err != nil {
		status = "error"
	}

	jsonResponse(w, map[string]interface{}{
		"status": status,
		"output": strings.TrimSpace(out),
	})
}

// ── /api/prod/exec — execute a command in a pod ─────────────────

func handleProdExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		Namespace string `json:"namespace"`
		Pod       string `json:"pod"`
		Container string `json:"container"`
		Command   string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Pod == "" || body.Command == "" {
		jsonError(w, "missing pod or command", 400)
		return
	}
	if body.Namespace == "" {
		body.Namespace = "default"
	}

	args := []string{"exec", body.Pod, "-n", body.Namespace}
	if body.Container != "" {
		args = append(args, "-c", body.Container)
	}
	args = append(args, "--", "sh", "-c", body.Command)

	out, err := prodKubectlJSON(args...)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"ok":     false,
			"error":  err.Error(),
			"output": strings.TrimSpace(out),
		})
		return
	}
	jsonResponse(w, map[string]interface{}{
		"ok":     true,
		"output": strings.TrimSpace(out),
	})
}

// ── /api/prod/describe/{kind}/{namespace}/{name} ────────────────

func handleProdDescribe(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/prod/describe/", 3)
	if err != nil {
		jsonError(w, "usage: /api/prod/describe/{kind}/{namespace}/{name}", 400)
		return
	}
	kind, ns, name := parts[0], parts[1], parts[2]

	out, err := prodKubectlJSON("describe", kind, name, "-n", ns)
	if err != nil {
		jsonError(w, "describe failed: "+err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]string{"output": out})
}

// ── /api/prod/certificates — cert-manager certificates ──────────

func handleProdCertificates(w http.ResponseWriter, r *http.Request) {
	out, err := prodKubectlJSON("get", "certificates", "--all-namespaces", "-o", "json")
	if err != nil {
		// cert-manager CRD might not exist
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/prod/clusterissuers — cert-manager ClusterIssuers ──────

func handleProdClusterIssuers(w http.ResponseWriter, r *http.Request) {
	out, err := prodKubectlJSON("get", "clusterissuers", "-o", "json")
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, out)
}

// ── /api/prod/node-metrics — kubectl top nodes ──────────────────

func handleProdNodeMetrics(w http.ResponseWriter, r *http.Request) {
	out, err := prodKubectlJSON("top", "nodes", "--no-headers")
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}, "error": "metrics-server not available"})
		return
	}

	type nodeMetric struct {
		Name     string `json:"name"`
		CPUCores string `json:"cpu_cores"`
		CPUPct   string `json:"cpu_pct"`
		MemBytes string `json:"mem_bytes"`
		MemPct   string `json:"mem_pct"`
	}

	var metrics []nodeMetric
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			metrics = append(metrics, nodeMetric{
				Name:     fields[0],
				CPUCores: fields[1],
				CPUPct:   fields[2],
				MemBytes: fields[3],
				MemPct:   fields[4],
			})
		}
	}
	jsonResponse(w, map[string]interface{}{"items": metrics})
}

// ── /api/prod/pod-metrics — kubectl top pods ────────────────────

func handleProdPodMetrics(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	args := []string{"top", "pods", "--no-headers"}
	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "--all-namespaces")
	}

	out, err := prodKubectlJSON(args...)
	if err != nil {
		jsonResponse(w, map[string]interface{}{"items": []interface{}{}, "error": "metrics-server not available"})
		return
	}

	type podMetric struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		CPU       string `json:"cpu"`
		Memory    string `json:"memory"`
	}

	var metrics []podMetric
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if ns != "" && len(fields) >= 3 {
			metrics = append(metrics, podMetric{
				Namespace: ns,
				Name:      fields[0],
				CPU:       fields[1],
				Memory:    fields[2],
			})
		} else if len(fields) >= 4 {
			metrics = append(metrics, podMetric{
				Namespace: fields[0],
				Name:      fields[1],
				CPU:       fields[2],
				Memory:    fields[3],
			})
		}
	}
	jsonResponse(w, map[string]interface{}{"items": metrics})
}

// ── Prometheus Integration ──────────────────────────────────────
//
// We auto-detect Prometheus in the production cluster and proxy queries
// through a kubectl port-forward tunnel.

var (
	promMu      sync.Mutex
	promPort    int
	promCmd     *exec.Cmd
	promReady   bool
	promSvcName string
	promSvcNS   string
)

// detectPrometheus finds the Prometheus service in the production cluster.
func detectPrometheus() (namespace, service string, port int) {
	// Common Prometheus service patterns
	searches := []struct {
		ns   string
		name string
	}{
		{"monitoring", "prometheus-server"},
		{"monitoring", "prometheus-kube-prometheus-prometheus"},
		{"monitoring", "kube-prometheus-stack-prometheus"},
		{"prometheus", "prometheus-server"},
		{"default", "prometheus-server"},
	}

	for _, s := range searches {
		out, err := prodKubectlJSON("get", "svc", s.name, "-n", s.ns, "-o", "json")
		if err == nil && out != "" {
			var svc struct {
				Spec struct {
					Ports []struct {
						Port int `json:"port"`
					} `json:"ports"`
				} `json:"spec"`
			}
			if json.Unmarshal([]byte(out), &svc) == nil && len(svc.Spec.Ports) > 0 {
				return s.ns, s.name, svc.Spec.Ports[0].Port
			}
		}
	}

	// Fallback: search by label
	out, err := prodKubectlJSON("get", "svc", "--all-namespaces",
		"-l", "app.kubernetes.io/name=prometheus",
		"-o", "json")
	if err == nil && out != "" {
		var list struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Ports []struct {
						Port int `json:"port"`
					} `json:"ports"`
				} `json:"spec"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil && len(list.Items) > 0 {
			svc := list.Items[0]
			svcPort := 9090
			if len(svc.Spec.Ports) > 0 {
				svcPort = svc.Spec.Ports[0].Port
			}
			return svc.Metadata.Namespace, svc.Metadata.Name, svcPort
		}
	}

	// Also try app=prometheus label
	out, err = prodKubectlJSON("get", "svc", "--all-namespaces",
		"-l", "app=prometheus",
		"-o", "json")
	if err == nil && out != "" {
		var list struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Ports []struct {
						Port int `json:"port"`
					} `json:"ports"`
				} `json:"spec"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil && len(list.Items) > 0 {
			svc := list.Items[0]
			svcPort := 9090
			if len(svc.Spec.Ports) > 0 {
				svcPort = svc.Spec.Ports[0].Port
			}
			return svc.Metadata.Namespace, svc.Metadata.Name, svcPort
		}
	}

	return "", "", 0
}

// ensurePromForward starts a kubectl port-forward to Prometheus if needed.
func ensurePromForward() (int, error) {
	promMu.Lock()
	defer promMu.Unlock()

	if promReady && promCmd != nil && promCmd.Process != nil {
		// Verify still alive
		if promCmd.ProcessState == nil || !promCmd.ProcessState.Exited() {
			return promPort, nil
		}
		promReady = false
	}

	ns, svc, svcPort := detectPrometheus()
	if svc == "" {
		return 0, fmt.Errorf("prometheus not found in production cluster")
	}
	promSvcName = svc
	promSvcNS = ns

	// Find a free local port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Start port-forward
	cmd := exec.Command("kubectl", "--context", prodContext,
		"port-forward", fmt.Sprintf("svc/%s", svc),
		fmt.Sprintf("%d:%d", localPort, svcPort),
		"-n", ns)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("port-forward failed: %w", err)
	}

	promCmd = cmd
	promPort = localPort

	// Wait for port to be ready
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			promReady = true
			return localPort, nil
		}
	}

	return 0, fmt.Errorf("port-forward to prometheus timed out")
}

// cleanupPromForward kills the port-forward process.
func cleanupPromForward() {
	promMu.Lock()
	defer promMu.Unlock()
	if promCmd != nil && promCmd.Process != nil {
		promCmd.Process.Kill()
		promCmd.Wait()
		promCmd = nil
		promReady = false
	}
}

// ── /api/prod/prometheus/status ─────────────────────────────────

func handlePromStatus(w http.ResponseWriter, r *http.Request) {
	ns, svc, port := detectPrometheus()
	jsonResponse(w, map[string]interface{}{
		"detected":  svc != "",
		"namespace": ns,
		"service":   svc,
		"port":      port,
		"connected": promReady,
	})
}

// ── /api/prod/prometheus/query — instant query ──────────────────

func handlePromQuery(w http.ResponseWriter, r *http.Request) {
	port, err := ensurePromForward()
	if err != nil {
		jsonError(w, err.Error(), 503)
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		jsonError(w, "missing query parameter", 400)
		return
	}

	promURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/query?query=%s",
		port, r.URL.Query().Get("query"))
	if t := r.URL.Query().Get("time"); t != "" {
		promURL += "&time=" + t
	}

	resp, err := http.Get(promURL)
	if err != nil {
		jsonError(w, "prometheus query failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ── /api/prod/prometheus/query_range — range query ──────────────

func handlePromQueryRange(w http.ResponseWriter, r *http.Request) {
	port, err := ensurePromForward()
	if err != nil {
		jsonError(w, err.Error(), 503)
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		jsonError(w, "missing query parameter", 400)
		return
	}

	promURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/query_range?query=%s",
		port, r.URL.Query().Get("query"))
	for _, param := range []string{"start", "end", "step"} {
		if v := r.URL.Query().Get(param); v != "" {
			promURL += "&" + param + "=" + v
		}
	}

	resp, err := http.Get(promURL)
	if err != nil {
		jsonError(w, "prometheus query_range failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ── /api/prod/apply — kubectl apply raw YAML ────────────────────

func handleProdApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.YAML == "" {
		jsonError(w, "missing yaml field", 400)
		return
	}

	cmd := exec.Command("kubectl", "--context", prodContext, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(body.YAML)
	out, err := cmd.CombinedOutput()
	if err != nil {
		jsonResponse(w, map[string]interface{}{"ok": false, "error": strings.TrimSpace(string(out))})
		return
	}
	jsonResponse(w, map[string]interface{}{"ok": true, "output": strings.TrimSpace(string(out))})
}
