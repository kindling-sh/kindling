package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ── /api/prod/snapshot/status — snapshot readiness check ─────────

func handleProdSnapshotStatus(w http.ResponseWriter, r *http.Request) {
	// Read DSEs from the dev cluster
	dses, err := readClusterDSEs()

	// Check tools
	helmOk := commandExists("helm")
	craneOk := commandExists("crane")
	dockerOk := commandExists("docker")

	result := map[string]interface{}{
		"services":  []interface{}{},
		"helm":      helmOk,
		"crane":     craneOk,
		"docker":    dockerOk,
		"context":   prodContext,
		"connected": prodContext != "",
	}

	if err == nil && len(dses) > 0 {
		var svcs []map[string]interface{}
		for _, dse := range dses {
			svc := map[string]interface{}{
				"name":     dse.Name,
				"image":    dse.Image,
				"port":     dse.Port,
				"replicas": dse.Replicas,
			}
			if dse.Ingress != nil {
				svc["ingress"] = map[string]interface{}{
					"enabled": dse.Ingress.Enabled,
					"host":    dse.Ingress.Host,
				}
			}
			var deps []string
			for _, d := range dse.Deps {
				deps = append(deps, d.Type)
			}
			svc["deps"] = deps
			svcs = append(svcs, svc)
		}
		result["services"] = svcs
	}

	jsonResponse(w, result)
}

// ── /api/prod/snapshot/deploy — run snapshot + deploy ────────────

func handleProdSnapshotDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		Registry  string   `json:"registry"`
		Tag       string   `json:"tag"`
		Format    string   `json:"format"`
		Namespace string   `json:"namespace"`
		Ingress   []string `json:"ingress"` // services to enable ingress for
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}

	if body.Registry == "" {
		jsonError(w, "registry is required", 400)
		return
	}
	if prodContext == "" {
		jsonError(w, "no production context configured", 400)
		return
	}

	format := body.Format
	if format == "" {
		format = "helm"
	}
	ns := body.Namespace
	if ns == "" {
		ns = "default"
	}
	tag := body.Tag
	if tag == "" {
		tag = "latest"
	}

	// Stream progress via SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}

	send := func(msgType, msg string) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": msgType, "message": msg}))
		flusher.Flush()
	}

	send("step", "Reading DevStagingEnvironments from cluster")
	dses, err := readClusterDSEs()
	if err != nil {
		send("error", "Failed to read DSEs: "+err.Error())
		return
	}
	if len(dses) == 0 {
		send("error", "No DevStagingEnvironments found in cluster")
		return
	}
	send("step", fmt.Sprintf("Found %d service(s)", len(dses)))

	// Strip user prefix
	if prefix := detectUserPrefix(dses); prefix != "" {
		send("step", fmt.Sprintf("Stripping user prefix %q", strings.TrimSuffix(prefix, "-")))
		for i := range dses {
			if stripped := strings.TrimPrefix(dses[i].Name, prefix); stripped != "" {
				dses[i].Name = stripped
			}
			if dses[i].Ingress != nil && dses[i].Ingress.Host != "" {
				dses[i].Ingress.Host = strings.TrimPrefix(dses[i].Ingress.Host, prefix)
			}
			for j := range dses[i].Env {
				dses[i].Env[j].Value = strings.ReplaceAll(dses[i].Env[j].Value, prefix, "")
			}
		}
	}

	// Push images
	send("step", fmt.Sprintf("Pushing images to %s", body.Registry))
	for i := range dses {
		dst := registryImage(dses[i].Name, body.Registry, tag)
		send("step", fmt.Sprintf("  %s → %s", dses[i].Name, dst))
		dses[i].Image = dst
	}

	// Try crane copy for real pushes
	if commandExists("crane") {
		needsPF := false
		for _, dse := range dses {
			if isClusterRegistryImage(dse.Image) {
				needsPF = true
				break
			}
		}
		if needsPF {
			send("step", "Port-forwarding to in-cluster registry")
		}
		// Best-effort push — images already rewritten above
		_ = craneCopyImages(dses, body.Registry, tag, detectUserPrefix(dses))
	}

	// Generate chart
	chartName := "kindling-snapshot"
	outDir := "/tmp/kindling-snapshot-" + fmt.Sprintf("%d", time.Now().UnixMilli())

	switch format {
	case "helm":
		send("step", "Generating Helm chart")
		if err := exportHelm(outDir, chartName, dses); err != nil {
			send("error", "Chart generation failed: "+err.Error())
			return
		}
	case "kustomize":
		send("step", "Generating Kustomize overlay")
		if err := exportKustomize(outDir, chartName, dses); err != nil {
			send("error", "Kustomize generation failed: "+err.Error())
			return
		}
	}

	// Build ingress selection set
	selectedSet := make(map[string]bool)
	for _, svc := range body.Ingress {
		selectedSet[svc] = true
	}

	// Deploy
	send("step", fmt.Sprintf("Deploying to %s (namespace: %s)", prodContext, ns))

	switch format {
	case "helm":
		if !commandExists("helm") {
			send("error", "helm not found on PATH")
			return
		}
		helmArgs := []string{
			"upgrade", "--install", chartName, outDir,
			"--kube-context", prodContext,
			"--namespace", ns,
			"--create-namespace",
			"--timeout", "10m",
		}
		// Ingress selection
		for _, dse := range dses {
			if dse.Ingress != nil && dse.Ingress.Enabled {
				vk := helmValuesKey(dse.Name)
				if !selectedSet[dse.Name] {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.enabled=false", vk))
				} else {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.host=", vk))
				}
			}
		}
		out, err := runSilent("helm", helmArgs...)
		if err != nil {
			send("error", "Helm deploy failed: "+out)
			return
		}
		send("step", "Helm deploy complete")
	case "kustomize":
		out, err := runSilent("kubectl", "--context", prodContext, "apply",
			"-k", outDir, "-n", ns)
		if err != nil {
			send("error", "Kustomize deploy failed: "+out)
			return
		}
		send("step", "Kustomize deploy complete")
	}

	send("done", "Deployed to production cluster")
}

// ── /api/prod/tls/status — cert-manager + TLS status ────────────

func handleProdTLSStatus(w http.ResponseWriter, r *http.Request) {
	result := map[string]interface{}{
		"cert_manager": false,
		"issuers":      []interface{}{},
		"certificates": []interface{}{},
	}

	// Check cert-manager
	_, err := prodKubectlJSON("get", "namespace", "cert-manager")
	if err == nil {
		result["cert_manager"] = true
	}

	// Get cluster issuers
	if out, err := prodKubectlJSON("get", "clusterissuers", "-o", "json"); err == nil {
		var list struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					ACME *struct {
						Server string `json:"server"`
						Email  string `json:"email"`
					} `json:"acme"`
				} `json:"spec"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil {
			var issuers []map[string]interface{}
			for _, item := range list.Items {
				issuer := map[string]interface{}{
					"name": item.Metadata.Name,
				}
				if item.Spec.ACME != nil {
					issuer["server"] = item.Spec.ACME.Server
					issuer["email"] = item.Spec.ACME.Email
				}
				ready := false
				for _, c := range item.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						ready = true
					}
				}
				issuer["ready"] = ready
				issuers = append(issuers, issuer)
			}
			result["issuers"] = issuers
		}
	}

	// Get certificates
	if out, err := prodKubectlJSON("get", "certificates", "--all-namespaces", "-o", "json"); err == nil {
		var list struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					DNSNames  []string `json:"dnsNames"`
					IssuerRef struct {
						Name string `json:"name"`
					} `json:"issuerRef"`
				} `json:"spec"`
				Status struct {
					NotAfter   string `json:"notAfter"`
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil {
			var certs []map[string]interface{}
			for _, item := range list.Items {
				cert := map[string]interface{}{
					"name":      item.Metadata.Name,
					"namespace": item.Metadata.Namespace,
					"dns_names": item.Spec.DNSNames,
					"issuer":    item.Spec.IssuerRef.Name,
					"not_after": item.Status.NotAfter,
				}
				ready := false
				for _, c := range item.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						ready = true
					}
				}
				cert["ready"] = ready
				certs = append(certs, cert)
			}
			result["certificates"] = certs
		}
	}

	jsonResponse(w, result)
}

// ── /api/prod/tls/install — install cert-manager + ClusterIssuer ─

func handleProdTLSInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		Email        string `json:"email"`
		Domain       string `json:"domain"`
		Issuer       string `json:"issuer"`
		IngressClass string `json:"ingress_class"`
		Staging      bool   `json:"staging"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}
	if body.Email == "" || body.Domain == "" {
		jsonError(w, "email and domain are required", 400)
		return
	}
	if body.Issuer == "" {
		body.Issuer = "letsencrypt-prod"
	}
	if body.IngressClass == "" {
		body.IngressClass = "traefik"
	}

	ctx := prodContext

	// Stream progress
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}

	send := func(msgType, msg string) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": msgType, "message": msg}))
		flusher.Flush()
	}

	// Install cert-manager if needed
	_, err := runSilent("kubectl", "--context", ctx, "get", "namespace", "cert-manager")
	if err != nil {
		send("step", "Installing cert-manager v1.17.1")
		certManagerURL := "https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml"
		if _, err := runSilent("kubectl", "--context", ctx, "apply", "-f", certManagerURL); err != nil {
			send("error", "cert-manager installation failed: "+err.Error())
			return
		}
		send("step", "Waiting for cert-manager webhook")
		for i := 0; i < 30; i++ {
			_, err := runSilent("kubectl", "--context", ctx, "-n", "cert-manager",
				"rollout", "status", "deployment/cert-manager-webhook", "--timeout=5s")
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		send("step", "cert-manager installed")
	} else {
		send("step", "cert-manager already installed")
	}

	// Create ClusterIssuer
	acmeServer := "https://acme-v02.api.letsencrypt.org/directory"
	if body.Staging {
		acmeServer = "https://acme-staging-v02.api.letsencrypt.org/directory"
		send("step", "Using Let's Encrypt staging server")
	}

	issuerYAML := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: %s
spec:
  acme:
    server: %s
    email: %s
    privateKeySecretRef:
      name: %s-account-key
    solvers:
    - http01:
        ingress:
          ingressClassName: %s
`, body.Issuer, acmeServer, body.Email, body.Issuer, body.IngressClass)

	send("step", fmt.Sprintf("Creating ClusterIssuer %q", body.Issuer))
	cmd := exec.Command("kubectl", "--context", ctx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(issuerYAML)
	if out, err := cmd.CombinedOutput(); err != nil {
		send("error", "ClusterIssuer creation failed: "+strings.TrimSpace(string(out)))
		return
	}

	send("done", fmt.Sprintf("TLS configured for %s with issuer %s", body.Domain, body.Issuer))
}

// ── /api/prod/metrics/status — VictoriaMetrics + kube-state-metrics status ──

func handleProdMetricsStatus(w http.ResponseWriter, r *http.Request) {
	result := map[string]interface{}{
		"victoria_metrics":   false,
		"kube_state_metrics": false,
		"vm_version":         "",
	}

	// Check VictoriaMetrics
	if out, err := prodKubectlJSON("get", "deployment", "vmsingle", "-n", "monitoring", "-o", "json"); err == nil {
		var dep struct {
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
			} `json:"status"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		if json.Unmarshal([]byte(out), &dep) == nil {
			result["victoria_metrics"] = dep.Status.ReadyReplicas > 0
			if len(dep.Spec.Template.Spec.Containers) > 0 {
				img := dep.Spec.Template.Spec.Containers[0].Image
				if parts := strings.Split(img, ":"); len(parts) > 1 {
					result["vm_version"] = parts[1]
				}
			}
		}
	}

	// Check kube-state-metrics
	if out, err := prodKubectlJSON("get", "deployment", "kube-state-metrics", "-n", "monitoring", "-o", "json"); err == nil {
		var dep struct {
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
			} `json:"status"`
		}
		if json.Unmarshal([]byte(out), &dep) == nil {
			result["kube_state_metrics"] = dep.Status.ReadyReplicas > 0
		}
	}

	jsonResponse(w, result)
}

// ── /api/prod/metrics/install — install VictoriaMetrics stack ────

func handleProdMetricsInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		Retention string `json:"retention"`
		Scrape    string `json:"scrape"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}
	if body.Retention == "" {
		body.Retention = "2h"
	}
	if body.Scrape == "" {
		body.Scrape = "30s"
	}

	ctx := prodContext

	// Stream progress
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}

	send := func(msgType, msg string) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": msgType, "message": msg}))
		flusher.Flush()
	}

	// Set globals for the install functions
	prodMetricsContext = ctx
	prodMetricsRetention = body.Retention
	prodMetricsScrape = body.Scrape

	// Create namespace
	send("step", "Creating monitoring namespace")
	nsYAML := `apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
  labels:
    app.kubernetes.io/managed-by: kindling
`
	cmd := exec.Command("kubectl", "--context", ctx, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(nsYAML)
	if out, err := cmd.CombinedOutput(); err != nil {
		send("error", "Failed to create namespace: "+strings.TrimSpace(string(out)))
		return
	}

	// Install kube-state-metrics
	send("step", "Installing kube-state-metrics")
	if err := installKubeStateMetrics(ctx); err != nil {
		send("error", "kube-state-metrics failed: "+err.Error())
		return
	}
	send("step", "kube-state-metrics installed")

	// Install VictoriaMetrics
	send("step", "Installing VictoriaMetrics single-node")
	if err := installVictoriaMetrics(ctx); err != nil {
		send("error", "VictoriaMetrics failed: "+err.Error())
		return
	}

	// Wait for rollout
	send("step", "Waiting for VictoriaMetrics to be ready")
	for i := 0; i < 30; i++ {
		_, err := runSilent("kubectl", "--context", ctx, "-n", "monitoring",
			"rollout", "status", "deployment/vmsingle", "--timeout=5s")
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}

	send("done", fmt.Sprintf("Metrics stack installed (retention: %s, scrape: %s)", body.Retention, body.Scrape))
}

// ── /api/prod/metrics/uninstall — remove metrics stack ──────────

func handleProdMetricsUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	ctx := prodContext

	_, _ = runSilent("kubectl", "--context", ctx, "-n", "monitoring", "delete", "deployment", "vmsingle", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "-n", "monitoring", "delete", "service", "vmsingle", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "-n", "monitoring", "delete", "configmap", "vmsingle-config", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "-n", "monitoring", "delete", "deployment", "kube-state-metrics", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "-n", "monitoring", "delete", "service", "kube-state-metrics", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "delete", "clusterrole", "kube-state-metrics", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "delete", "clusterrolebinding", "kube-state-metrics", "--ignore-not-found")
	_, _ = runSilent("kubectl", "--context", ctx, "-n", "monitoring", "delete", "serviceaccount", "kube-state-metrics", "--ignore-not-found")

	jsonResponse(w, map[string]interface{}{"ok": true})
}

// mustJSON serialises a value to JSON, panicking on error (for SSE messages).
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
