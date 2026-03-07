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

// ── /api/prod/ingress-controller — detect ingress controller + external IP ──

func handleProdIngressController(w http.ResponseWriter, r *http.Request) {
	type icPort struct {
		Port     int    `json:"port"`
		NodePort int    `json:"nodePort,omitempty"`
		Protocol string `json:"protocol"`
		Name     string `json:"name,omitempty"`
	}
	type icInfo struct {
		Found     bool     `json:"found"`
		Name      string   `json:"name"`
		Namespace string   `json:"namespace"`
		Type      string   `json:"type"`
		Class     string   `json:"class"` // traefik, nginx, etc.
		ExternalIP string  `json:"external_ip"`
		Hostname   string  `json:"hostname"`
		ClusterIP  string  `json:"cluster_ip"`
		Ports      []icPort `json:"ports"`
	}

	info := icInfo{}

	// Search common ingress controller namespaces and labels
	searches := []struct {
		ns    string
		label string
		class string
	}{
		{"traefik", "app.kubernetes.io/name=traefik", "traefik"},
		{"traefik-system", "app.kubernetes.io/name=traefik", "traefik"},
		{"ingress-nginx", "app.kubernetes.io/name=ingress-nginx", "nginx"},
		{"nginx-ingress", "app.kubernetes.io/name=ingress-nginx", "nginx"},
		{"kube-system", "app.kubernetes.io/name=traefik", "traefik"},
		{"kube-system", "app.kubernetes.io/name=ingress-nginx", "nginx"},
	}

	for _, s := range searches {
		out, err := prodKubectlJSON("get", "svc", "-n", s.ns, "-l", s.label, "-o", "json")
		if err != nil {
			continue
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Type      string `json:"type"`
					ClusterIP string `json:"clusterIP"`
					Ports     []struct {
						Port     int    `json:"port"`
						NodePort int    `json:"nodePort"`
						Protocol string `json:"protocol"`
						Name     string `json:"name"`
					} `json:"ports"`
				} `json:"spec"`
				Status struct {
					LoadBalancer struct {
						Ingress []struct {
							IP       string `json:"ip"`
							Hostname string `json:"hostname"`
						} `json:"ingress"`
					} `json:"loadBalancer"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) != nil || len(list.Items) == 0 {
			continue
		}
		// Pick the first service that isn't headless
		for _, svc := range list.Items {
			if svc.Spec.ClusterIP == "None" {
				continue
			}
			info.Found = true
			info.Name = svc.Metadata.Name
			info.Namespace = svc.Metadata.Namespace
			info.Type = svc.Spec.Type
			info.Class = s.class
			info.ClusterIP = svc.Spec.ClusterIP
			for _, p := range svc.Spec.Ports {
				info.Ports = append(info.Ports, icPort{Port: p.Port, NodePort: p.NodePort, Protocol: p.Protocol, Name: p.Name})
			}
			if len(svc.Status.LoadBalancer.Ingress) > 0 {
				info.ExternalIP = svc.Status.LoadBalancer.Ingress[0].IP
				info.Hostname = svc.Status.LoadBalancer.Ingress[0].Hostname
			}
			break
		}
		if info.Found {
			break
		}
	}

	jsonResponse(w, info)
}

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
		{"monitoring", "vmsingle"},
		{"monitoring", "victoria-metrics"},
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
	for _, label := range []string{"app=prometheus", "app=vmsingle", "app.kubernetes.io/name=vmsingle"} {
		out, err = prodKubectlJSON("get", "svc", "--all-namespaces",
			"-l", label,
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

// ── /api/prod/advisor — rule-based cluster advisor ──────────────

type advisory struct {
	Severity string `json:"severity"` // critical, warning, info
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Action   string `json:"action"`
	Resource string `json:"resource,omitempty"` // e.g. "pod/my-app-xyz"
}

func handleProdAdvisor(w http.ResponseWriter, r *http.Request) {
	var advisories []advisory

	// ── Collect cluster state ───────────────────────────────────
	type podItem struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				RestartCount int    `json:"restartCount"`
				Ready        bool   `json:"ready"`
				State        struct {
					Waiting *struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"waiting"`
					Terminated *struct {
						Reason   string `json:"reason"`
						ExitCode int    `json:"exitCode"`
					} `json:"terminated"`
				} `json:"state"`
				LastState struct {
					Terminated *struct {
						Reason   string `json:"reason"`
						ExitCode int    `json:"exitCode"`
					} `json:"terminated"`
				} `json:"lastState"`
			} `json:"containerStatuses"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
				Reason string `json:"reason"`
			} `json:"conditions"`
		} `json:"status"`
	}

	type nodeItem struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	}

	type deployItem struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas       int `json:"readyReplicas"`
			UnavailableReplicas int `json:"unavailableReplicas"`
		} `json:"status"`
	}

	type eventItem struct {
		Type           string `json:"type"`
		Reason         string `json:"reason"`
		Message        string `json:"message"`
		Count          int    `json:"count"`
		InvolvedObject struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"involvedObject"`
	}

	// Fetch pods
	var podList struct{ Items []podItem }
	if out, err := prodKubectlJSON("get", "pods", "--all-namespaces", "-o", "json"); err == nil {
		json.Unmarshal([]byte(out), &podList)
	}

	// Fetch nodes
	var nodeList struct{ Items []nodeItem }
	if out, err := prodKubectlJSON("get", "nodes", "-o", "json"); err == nil {
		json.Unmarshal([]byte(out), &nodeList)
	}

	// Fetch deployments
	var depList struct{ Items []deployItem }
	if out, err := prodKubectlJSON("get", "deployments", "--all-namespaces", "-o", "json"); err == nil {
		json.Unmarshal([]byte(out), &depList)
	}

	// Fetch recent warning events
	var eventList struct{ Items []eventItem }
	if out, err := prodKubectlJSON("get", "events", "--all-namespaces",
		"--field-selector", "type=Warning", "--sort-by=.lastTimestamp",
		"-o", "json"); err == nil {
		json.Unmarshal([]byte(out), &eventList)
	}

	// ── Node rules ──────────────────────────────────────────────
	for _, n := range nodeList.Items {
		for _, c := range n.Status.Conditions {
			switch c.Type {
			case "MemoryPressure":
				if c.Status == "True" {
					advisories = append(advisories, advisory{
						Severity: "critical",
						Title:    "Node memory pressure",
						Detail:   fmt.Sprintf("Node %s is under memory pressure — pod eviction is imminent.", n.Metadata.Name),
						Action:   "Drain non-critical workloads, reduce memory limits, or add a node to the cluster.",
						Resource: "node/" + n.Metadata.Name,
					})
				}
			case "DiskPressure":
				if c.Status == "True" {
					advisories = append(advisories, advisory{
						Severity: "critical",
						Title:    "Node disk pressure",
						Detail:   fmt.Sprintf("Node %s is running low on disk — kubelet may start evicting pods.", n.Metadata.Name),
						Action:   "Clean up unused images (kubectl node debug), expand disk, or delete old PVCs.",
						Resource: "node/" + n.Metadata.Name,
					})
				}
			case "PIDPressure":
				if c.Status == "True" {
					advisories = append(advisories, advisory{
						Severity: "warning",
						Title:    "Node PID pressure",
						Detail:   fmt.Sprintf("Node %s is running low on process IDs.", n.Metadata.Name),
						Action:   "Check for runaway processes or fork bombs in pods on this node.",
						Resource: "node/" + n.Metadata.Name,
					})
				}
			case "Ready":
				if c.Status != "True" {
					advisories = append(advisories, advisory{
						Severity: "critical",
						Title:    "Node not ready",
						Detail:   fmt.Sprintf("Node %s is in NotReady state — it cannot schedule or run pods.", n.Metadata.Name),
						Action:   "Check kubelet logs on the node, verify network connectivity, and check cloud provider status.",
						Resource: "node/" + n.Metadata.Name,
					})
				}
			}
		}
	}

	// ── Pod rules ───────────────────────────────────────────────
	for _, p := range podList.Items {
		podRef := fmt.Sprintf("%s/%s", p.Metadata.Namespace, p.Metadata.Name)

		for _, cs := range p.Status.ContainerStatuses {
			// CrashLoopBackOff
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				reason := "unknown"
				if cs.LastState.Terminated != nil {
					reason = cs.LastState.Terminated.Reason
					if reason == "" {
						reason = fmt.Sprintf("exit code %d", cs.LastState.Terminated.ExitCode)
					}
				}
				advisories = append(advisories, advisory{
					Severity: "critical",
					Title:    "Container in CrashLoopBackOff",
					Detail:   fmt.Sprintf("Container %q in pod %s keeps crashing (last exit: %s, %d restarts).", cs.Name, podRef, reason, cs.RestartCount),
					Action:   "Check logs with 'kubectl logs --previous'. Common causes: OOMKilled, bad entrypoint, missing config/secrets.",
					Resource: "pod/" + podRef,
				})
			}

			// ImagePullBackOff / ErrImagePull
			if cs.State.Waiting != nil && (cs.State.Waiting.Reason == "ImagePullBackOff" || cs.State.Waiting.Reason == "ErrImagePull") {
				advisories = append(advisories, advisory{
					Severity: "critical",
					Title:    "Image pull failure",
					Detail:   fmt.Sprintf("Container %q in pod %s cannot pull its image: %s", cs.Name, podRef, cs.State.Waiting.Message),
					Action:   "Verify the image tag exists, check registry credentials (imagePullSecrets), and confirm network access to the registry.",
					Resource: "pod/" + podRef,
				})
			}

			// OOMKilled (last terminated state)
			if cs.LastState.Terminated != nil && cs.LastState.Terminated.Reason == "OOMKilled" {
				advisories = append(advisories, advisory{
					Severity: "warning",
					Title:    "Container OOMKilled",
					Detail:   fmt.Sprintf("Container %q in pod %s was killed by the OOM killer (%d restarts).", cs.Name, podRef, cs.RestartCount),
					Action:   "Increase the container's memory limit in the deployment spec, or investigate the memory leak.",
					Resource: "pod/" + podRef,
				})
			}

			// CreateContainerConfigError
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CreateContainerConfigError" {
				advisories = append(advisories, advisory{
					Severity: "critical",
					Title:    "Container config error",
					Detail:   fmt.Sprintf("Container %q in pod %s has a configuration error: %s", cs.Name, podRef, cs.State.Waiting.Message),
					Action:   "Check that referenced Secrets and ConfigMaps exist and have the expected keys.",
					Resource: "pod/" + podRef,
				})
			}

			// High restart count (not crash-looping yet, but concerning)
			if cs.RestartCount >= 5 && (cs.State.Waiting == nil || cs.State.Waiting.Reason != "CrashLoopBackOff") {
				advisories = append(advisories, advisory{
					Severity: "warning",
					Title:    "High restart count",
					Detail:   fmt.Sprintf("Container %q in pod %s has restarted %d times.", cs.Name, podRef, cs.RestartCount),
					Action:   "Check resource limits, liveness probes, and application logs for intermittent failures.",
					Resource: "pod/" + podRef,
				})
			}
		}

		// Pending pods (unschedulable)
		if p.Status.Phase == "Pending" {
			for _, c := range p.Status.Conditions {
				if c.Type == "PodScheduled" && c.Status != "True" {
					action := "Check resource requests vs available node capacity."
					if strings.Contains(c.Reason, "Unschedulable") {
						action = "No node matches the pod's requirements. Check resource requests, node selectors, tolerations, and anti-affinity rules."
					}
					advisories = append(advisories, advisory{
						Severity: "warning",
						Title:    "Pod stuck pending",
						Detail:   fmt.Sprintf("Pod %s cannot be scheduled: %s", podRef, c.Reason),
						Action:   action,
						Resource: "pod/" + podRef,
					})
				}
			}
		}
	}

	// ── Deployment rules ────────────────────────────────────────
	for _, d := range depList.Items {
		depRef := fmt.Sprintf("%s/%s", d.Metadata.Namespace, d.Metadata.Name)

		// Zero ready replicas
		if d.Spec.Replicas > 0 && d.Status.ReadyReplicas == 0 {
			advisories = append(advisories, advisory{
				Severity: "critical",
				Title:    "Deployment has no ready replicas",
				Detail:   fmt.Sprintf("Deployment %s has 0/%d ready replicas — the service is down.", depRef, d.Spec.Replicas),
				Action:   "Check pod status and events. A failed rollout, bad image, or resource constraint is likely.",
				Resource: "deployment/" + depRef,
			})
		} else if d.Status.UnavailableReplicas > 0 && d.Status.ReadyReplicas > 0 {
			// Partial availability
			advisories = append(advisories, advisory{
				Severity: "warning",
				Title:    "Deployment partially unavailable",
				Detail:   fmt.Sprintf("Deployment %s has %d unavailable replica(s) out of %d desired.", depRef, d.Status.UnavailableReplicas, d.Spec.Replicas),
				Action:   "A rollout may be in progress, or some pods are failing. Check pod events for details.",
				Resource: "deployment/" + depRef,
			})
		}
	}

	// ── Event-based rules ───────────────────────────────────────
	// Look for frequently recurring warning events
	seen := map[string]bool{}
	for _, e := range eventList.Items {
		key := e.Reason + "/" + e.InvolvedObject.Name

		if seen[key] {
			continue
		}
		seen[key] = true

		if e.Reason == "FailedScheduling" && e.Count >= 3 {
			advisories = append(advisories, advisory{
				Severity: "warning",
				Title:    "Repeated scheduling failures",
				Detail:   fmt.Sprintf("%s %s/%s has failed scheduling %d times: %s", e.InvolvedObject.Kind, e.InvolvedObject.Namespace, e.InvolvedObject.Name, e.Count, e.Message),
				Action:   "Cluster may be at capacity. Consider scaling the node pool or reducing resource requests.",
				Resource: strings.ToLower(e.InvolvedObject.Kind) + "/" + e.InvolvedObject.Namespace + "/" + e.InvolvedObject.Name,
			})
		}

		if e.Reason == "FailedMount" || e.Reason == "FailedAttachVolume" {
			advisories = append(advisories, advisory{
				Severity: "warning",
				Title:    "Volume mount failure",
				Detail:   fmt.Sprintf("Pod %s/%s cannot mount a volume: %s", e.InvolvedObject.Namespace, e.InvolvedObject.Name, e.Message),
				Action:   "Check PVC status, storage class availability, and that the volume exists in the correct AZ.",
				Resource: "pod/" + e.InvolvedObject.Namespace + "/" + e.InvolvedObject.Name,
			})
		}

		if e.Reason == "Unhealthy" && e.Count >= 5 {
			advisories = append(advisories, advisory{
				Severity: "warning",
				Title:    "Repeated probe failures",
				Detail:   fmt.Sprintf("Pod %s/%s failing health checks (%d times): %s", e.InvolvedObject.Namespace, e.InvolvedObject.Name, e.Count, e.Message),
				Action:   "Check liveness/readiness probe configuration — path, port, and timeouts. The app may be slow to start or unresponsive.",
				Resource: "pod/" + e.InvolvedObject.Namespace + "/" + e.InvolvedObject.Name,
			})
		}

		if e.Reason == "BackOff" && strings.Contains(e.Message, "back-off pulling image") {
			advisories = append(advisories, advisory{
				Severity: "warning",
				Title:    "Image pull back-off",
				Detail:   fmt.Sprintf("Pod %s/%s is in image pull back-off: %s", e.InvolvedObject.Namespace, e.InvolvedObject.Name, e.Message),
				Action:   "Verify image name and tag, check imagePullSecrets, and ensure the cluster can reach the registry.",
				Resource: "pod/" + e.InvolvedObject.Namespace + "/" + e.InvolvedObject.Name,
			})
		}
	}

	// ── Certificate rules ───────────────────────────────────────
	if certOut, err := prodKubectlJSON("get", "certificates", "--all-namespaces", "-o", "json"); err == nil {
		var certList struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					NotAfter   string `json:"notAfter"`
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
						Reason string `json:"reason"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(certOut), &certList) == nil {
			for _, cert := range certList.Items {
				certRef := fmt.Sprintf("%s/%s", cert.Metadata.Namespace, cert.Metadata.Name)

				// Check for not-ready certificates
				for _, c := range cert.Status.Conditions {
					if c.Type == "Ready" && c.Status != "True" {
						advisories = append(advisories, advisory{
							Severity: "warning",
							Title:    "Certificate not ready",
							Detail:   fmt.Sprintf("Certificate %s is not ready: %s", certRef, c.Reason),
							Action:   "Check ClusterIssuer status, DNS configuration, and ACME solver logs.",
							Resource: "certificate/" + certRef,
						})
					}
				}

				// Check expiry
				if cert.Status.NotAfter != "" {
					if expiry, err := time.Parse(time.RFC3339, cert.Status.NotAfter); err == nil {
						until := time.Until(expiry)
						if until < 0 {
							advisories = append(advisories, advisory{
								Severity: "critical",
								Title:    "Certificate expired",
								Detail:   fmt.Sprintf("Certificate %s expired %s ago.", certRef, (-until).Round(time.Hour)),
								Action:   "Check cert-manager logs and ClusterIssuer. Renewal may have failed due to DNS or rate limits.",
								Resource: "certificate/" + certRef,
							})
						} else if until < 7*24*time.Hour {
							advisories = append(advisories, advisory{
								Severity: "warning",
								Title:    "Certificate expiring soon",
								Detail:   fmt.Sprintf("Certificate %s expires in %s.", certRef, until.Round(time.Hour)),
								Action:   "Verify cert-manager is renewing. Check ClusterIssuer and ACME challenge status.",
								Resource: "certificate/" + certRef,
							})
						}
					}
				}
			}
		}
	}

	// ── Node metrics rules (high resource usage) ────────────────
	if topOut, err := prodKubectlJSON("top", "nodes", "--no-headers"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(topOut), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				nodeName := fields[0]
				cpuPct := strings.TrimSuffix(fields[2], "%")
				memPct := strings.TrimSuffix(fields[4], "%")

				var cpuVal, memVal int
				fmt.Sscanf(cpuPct, "%d", &cpuVal)
				fmt.Sscanf(memPct, "%d", &memVal)

				if cpuVal >= 90 {
					advisories = append(advisories, advisory{
						Severity: "critical",
						Title:    "Node CPU critically high",
						Detail:   fmt.Sprintf("Node %s is at %d%% CPU utilisation.", nodeName, cpuVal),
						Action:   "Reduce workload, increase CPU limits, or add nodes. Check for runaway processes.",
						Resource: "node/" + nodeName,
					})
				} else if cpuVal >= 75 {
					advisories = append(advisories, advisory{
						Severity: "warning",
						Title:    "Node CPU elevated",
						Detail:   fmt.Sprintf("Node %s is at %d%% CPU utilisation.", nodeName, cpuVal),
						Action:   "Monitor for trends. Consider scaling horizontally before it becomes critical.",
						Resource: "node/" + nodeName,
					})
				}

				if memVal >= 90 {
					advisories = append(advisories, advisory{
						Severity: "critical",
						Title:    "Node memory critically high",
						Detail:   fmt.Sprintf("Node %s is at %d%% memory utilisation.", nodeName, memVal),
						Action:   "Pod eviction is likely. Reduce memory limits on workloads or add a node immediately.",
						Resource: "node/" + nodeName,
					})
				} else if memVal >= 80 {
					advisories = append(advisories, advisory{
						Severity: "warning",
						Title:    "Node memory elevated",
						Detail:   fmt.Sprintf("Node %s is at %d%% memory utilisation.", nodeName, memVal),
						Action:   "Review pod memory requests/limits. Consider scaling the node pool before evictions start.",
						Resource: "node/" + nodeName,
					})
				}
			}
		}
	}

	// ── All clear ───────────────────────────────────────────────
	if len(advisories) == 0 {
		advisories = []advisory{{
			Severity: "info",
			Title:    "Cluster looks healthy",
			Detail:   "No issues detected across nodes, pods, deployments, events, or certificates.",
			Action:   "",
		}}
	}

	jsonResponse(w, map[string]interface{}{
		"advisories": advisories,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	})
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
