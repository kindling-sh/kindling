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
				"compute":  dse.Compute,
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

// ── /api/prod/snapshot/credentials — detect dev credentials ─────

func handleProdSnapshotCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", 405)
		return
	}

	dses, err := readClusterDSEs()
	if err != nil {
		jsonError(w, "failed to read DSEs: "+err.Error(), 500)
		return
	}

	chartName := "kindling-snapshot"
	// Strip user prefix to match what the chart will use
	stripDSEPrefix(dses)

	entries := detectDevCredentials(chartName, dses)
	secretEntries := detectUserSecrets(dses)
	entries = append(entries, secretEntries...)

	// Check for cached values
	var cachedCreds map[string]string
	var cachedAt string
	if prodContext != "" {
		if cached := loadCredCache(prodContext); cached != nil && len(cached.Creds) > 0 {
			cachedCreds = cached.Creds
			cachedAt = cached.UpdatedAt.Format(time.RFC3339)
		}
	}

	type credInfo struct {
		EnvVar   string   `json:"env_var"`
		DepType  string   `json:"dep_type"`
		DevValue string   `json:"dev_value"`
		Services []string `json:"services"`
		Cached   string   `json:"cached,omitempty"` // cached production value (if any)
	}

	var result []credInfo
	for _, e := range entries {
		ci := credInfo{
			EnvVar:   e.EnvVarName,
			DepType:  e.DepType,
			DevValue: e.DevValue,
			Services: e.Services,
		}
		if cachedCreds != nil {
			if v, ok := cachedCreds[e.EnvVarName]; ok {
				ci.Cached = v
			}
		}
		result = append(result, ci)
	}

	jsonResponse(w, map[string]interface{}{
		"credentials": result,
		"cached_at":   cachedAt,
	})
}

func handleProdSnapshotDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}

	var body struct {
		Registry     string            `json:"registry"`
		RegistryUser string            `json:"registry_user"`
		RegistryPass string            `json:"registry_pass"`
		Tag          string            `json:"tag"`
		Format       string            `json:"format"`
		Namespace    string            `json:"namespace"`
		Ingress      []string          `json:"ingress"`     // services to enable ingress for
		Credentials  map[string]string `json:"credentials"` // envVarName → production connection string
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

	// ── Strip user prefix (shared pipeline) ────────────────────
	userPrefix := stripDSEPrefix(dses)
	if userPrefix != "" {
		send("step", fmt.Sprintf("Stripped user prefix %q", strings.TrimSuffix(userPrefix, "-")))
	}

	// ── Push images (shared pipeline) ──────────────────────────
	pushSnapshotImages(dses, body.Registry, tag, userPrefix, body.RegistryUser, body.RegistryPass, func(msg string) {
		send("step", msg)
	})

	// ── Generate chart (shared pipeline) ────────────────────────
	chartName := "kindling-snapshot"
	outDir := "/tmp/kindling-snapshot-" + fmt.Sprintf("%d", time.Now().UnixMilli())

	send("step", fmt.Sprintf("Generating %s chart", format))
	if err := exportSnapshot(format, outDir, chartName, dses); err != nil {
		send("error", "Chart generation failed: "+err.Error())
		return
	}

	// ── Ensure ingress controller (shared pipeline) ─────────────
	if err := ensureIngressController(prodContext, func(msg string) {
		send("step", msg)
	}); err != nil {
		send("error", "Ingress controller setup failed: "+err.Error())
		return
	}

	// ── Deploy (shared pipeline) ────────────────────────────────
	// The frontend sends ingress names with the original user prefix
	// (from /api/prod/snapshot/status), but DSE names have been
	// stripped above. Strip the same prefix from ingress selections
	// so the lookup matches the chart's values keys.
	selectedSet := make(map[string]bool)
	for _, svc := range body.Ingress {
		stripped := svc
		if userPrefix != "" {
			stripped = strings.TrimPrefix(svc, userPrefix)
		}
		selectedSet[stripped] = true
	}

	// Auto-detect the IngressClass on the target cluster
	ingClass := detectIngressClass(prodContext)
	if ingClass != "" {
		send("step", fmt.Sprintf("Using IngressClass: %s", ingClass))
	}

	send("step", fmt.Sprintf("Deploying to %s (namespace: %s)", prodContext, ns))

	// Build credential overrides from user-supplied production values
	var credOverrides map[string]map[string]credOverride
	if len(body.Credentials) > 0 {
		entries := detectDevCredentials(chartName, dses)
		secretEntries := detectUserSecrets(dses)
		entries = append(entries, secretEntries...)
		credOverrides = buildOverrideMap(entries, dses, body.Credentials)
		send("step", fmt.Sprintf("Applying %d production credential override(s)", len(body.Credentials)))
		// Cache for future deploys
		_ = saveCredCache(prodContext, chartName, body.Credentials)
	} else {
		// Auto-apply cached credentials if available
		cached := loadCredCache(prodContext)
		if cached != nil && len(cached.Creds) > 0 {
			entries := detectDevCredentials(chartName, dses)
			secretEntries := detectUserSecrets(dses)
			entries = append(entries, secretEntries...)
			credOverrides = buildOverrideMap(entries, dses, cached.Creds)
			send("step", fmt.Sprintf("Using %d cached production credential(s)", len(cached.Creds)))
		}
	}

	out, err := deploySnapshot(DeployOpts{
		Context:         prodContext,
		Namespace:       ns,
		Format:          format,
		OutDir:          outDir,
		ChartName:       chartName,
		DSEs:            dses,
		SelectedIngress: selectedSet,
		IngressClass:    ingClass,
		CredOverrides:   credOverrides,
	})
	if err != nil {
		send("error", fmt.Sprintf("Deploy failed: %s", out))
		return
	}
	send("step", "Deploy complete")

	send("done", "Deployed to production cluster")
}

// ── /api/prod/snapshot/secrets/update — patch secrets post-deploy ─

func handleProdSnapshotSecretsUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	if prodContext == "" {
		jsonError(w, "no production context configured", 400)
		return
	}

	var body struct {
		Namespace   string            `json:"namespace"`
		Credentials map[string]string `json:"credentials"` // envVarName → new value
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}
	if len(body.Credentials) == 0 {
		jsonError(w, "no credentials provided", 400)
		return
	}

	ns := body.Namespace
	if ns == "" {
		ns = "default"
	}

	// Discover which K8s Secrets contain these keys by listing secrets
	// in the namespace that match our naming convention (<release>-*-secrets).
	out, err := prodKubectlJSON("get", "secrets", "-n", ns, "-o", "json")
	if err != nil {
		jsonError(w, "failed to list secrets: "+err.Error(), 500)
		return
	}

	var secretList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &secretList); err != nil {
		jsonError(w, "failed to parse secrets: "+err.Error(), 500)
		return
	}

	// Find secrets that end in "-secrets" and contain the requested keys
	updated := 0
	restartSet := make(map[string]bool) // deployment names to restart

	for envVar, newValue := range body.Credentials {
		for _, secret := range secretList.Items {
			if !strings.HasSuffix(secret.Metadata.Name, "-secrets") {
				continue
			}
			if _, hasKey := secret.Data[envVar]; !hasKey {
				continue
			}
			// Patch this secret with the new value using strategic merge
			patchJSON, _ := json.Marshal(map[string]interface{}{
				"stringData": map[string]string{envVar: newValue},
			})
			_, patchErr := prodKubectlJSON("patch", "secret", secret.Metadata.Name,
				"-n", ns, "--type", "strategic", "-p", string(patchJSON))
			if patchErr != nil {
				jsonError(w, fmt.Sprintf("failed to patch secret %s: %s", secret.Metadata.Name, patchErr.Error()), 500)
				return
			}
			updated++

			// Derive the deployment name from the secret name
			// Convention: <release>-<svc>-secrets → restart <release>-<svc>
			depName := strings.TrimSuffix(secret.Metadata.Name, "-secrets")
			restartSet[depName] = true
			break // each env var should only be in one secret
		}
	}

	// Restart deployments that reference the updated secrets
	var restarted []string
	for depName := range restartSet {
		_, err := prodKubectlJSON("rollout", "restart", "deployment/"+depName, "-n", ns)
		if err == nil {
			restarted = append(restarted, depName)
		}
	}

	// Cache the updated credentials
	chartName := "kindling-snapshot"
	_ = saveCredCache(prodContext, chartName, body.Credentials)

	jsonResponse(w, map[string]interface{}{
		"ok":        true,
		"updated":   updated,
		"restarted": restarted,
	})
}

// ── /api/prod/tls/status — cert-manager + TLS status ────────────

func handleProdTLSStatus(w http.ResponseWriter, r *http.Request) {
	if prodContext == "" {
		jsonError(w, "no production context configured", 400)
		return
	}

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

// ── /api/prod/tls/install — install cert-manager + ClusterIssuer + patch ingress ─

func handleProdTLSInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	if prodContext == "" {
		jsonError(w, "no production context configured", 400)
		return
	}

	var body struct {
		Email            string `json:"email"`
		Domain           string `json:"domain"`
		Issuer           string `json:"issuer"`
		IngressClass     string `json:"ingress_class"`
		Staging          bool   `json:"staging"`
		IngressName      string `json:"ingress_name"`
		IngressNamespace string `json:"ingress_namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", 400)
		return
	}
	if body.Email == "" || body.Domain == "" {
		jsonError(w, "email and domain are required", 400)
		return
	}
	// Validate inputs to prevent YAML injection
	if strings.ContainsAny(body.Email, "\n\r\t") || strings.ContainsAny(body.Domain, "\n\r\t /") {
		jsonError(w, "invalid characters in email or domain", 400)
		return
	}
	if body.Issuer == "" {
		body.Issuer = "letsencrypt-prod"
	}
	if strings.ContainsAny(body.Issuer, "\n\r\t /") {
		jsonError(w, "invalid characters in issuer name", 400)
		return
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
		webhookReady := false
		for i := 0; i < 30; i++ {
			_, err := runSilent("kubectl", "--context", ctx, "-n", "cert-manager",
				"rollout", "status", "deployment/cert-manager-webhook", "--timeout=5s")
			if err == nil {
				webhookReady = true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !webhookReady {
			send("error", "cert-manager webhook did not become ready — check pod status in cert-manager namespace")
			return
		}
		// Extra wait for CA bundle injection to complete
		time.Sleep(5 * time.Second)
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

	// Wait for ClusterIssuer to become ready
	send("step", "Waiting for ClusterIssuer to register with ACME")
	issuerReady := false
	for i := 0; i < 20; i++ {
		out, err := runSilent("kubectl", "--context", ctx, "get", "clusterissuer", body.Issuer, "-o", "json")
		if err == nil {
			var issuer struct {
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			}
			if json.Unmarshal([]byte(out), &issuer) == nil {
				for _, c := range issuer.Status.Conditions {
					if c.Type == "Ready" && c.Status == "True" {
						issuerReady = true
						break
					}
				}
			}
		}
		if issuerReady {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !issuerReady {
		send("error", "ClusterIssuer did not become ready — check cert-manager logs and ACME registration")
		return
	}
	send("step", "ClusterIssuer is ready")

	// Patch ingress with TLS if an ingress was specified
	if body.IngressName != "" {
		ns := body.IngressNamespace
		if ns == "" {
			ns = "default"
		}

		send("step", fmt.Sprintf("Reading ingress %s/%s", ns, body.IngressName))

		// Read current ingress to check existing TLS and host
		ingOut, err := runSilent("kubectl", "--context", ctx, "-n", ns, "get", "ingress", body.IngressName, "-o", "json")
		if err != nil {
			send("error", fmt.Sprintf("Ingress %s/%s not found: %s", ns, body.IngressName, err.Error()))
			return
		}

		var existing struct {
			Spec struct {
				Rules []struct {
					Host string `json:"host"`
				} `json:"rules"`
				TLS []struct {
					Hosts []string `json:"hosts"`
				} `json:"tls"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(ingOut), &existing); err != nil {
			send("error", "Failed to parse ingress: "+err.Error())
			return
		}

		// Check if TLS is already configured for this domain
		for _, t := range existing.Spec.TLS {
			for _, h := range t.Hosts {
				if h == body.Domain {
					send("step", fmt.Sprintf("Ingress already has TLS for %s — updating annotation", body.Domain))
				}
			}
		}

		// Build merge patch: annotation + TLS block
		secretName := strings.ReplaceAll(body.Domain, ".", "-") + "-tls"
		patch := map[string]interface{}{
			"metadata": map[string]interface{}{
				"annotations": map[string]string{
					"cert-manager.io/cluster-issuer": body.Issuer,
				},
			},
			"spec": map[string]interface{}{
				"tls": []map[string]interface{}{
					{
						"hosts":      []string{body.Domain},
						"secretName": secretName,
					},
				},
			},
		}
		patchJSON, _ := json.Marshal(patch)

		send("step", fmt.Sprintf("Patching ingress with TLS for %s (secret: %s)", body.Domain, secretName))
		if _, err := runSilent("kubectl", "--context", ctx, "-n", ns, "patch", "ingress", body.IngressName,
			"--type=merge", "-p", string(patchJSON)); err != nil {
			send("error", "Failed to patch ingress: "+err.Error())
			return
		}

		// If first rule has no host, set it to the domain
		if len(existing.Spec.Rules) > 0 && existing.Spec.Rules[0].Host == "" {
			send("step", fmt.Sprintf("Setting ingress host to %s", body.Domain))
			hostPatch := fmt.Sprintf(`[{"op":"replace","path":"/spec/rules/0/host","value":"%s"}]`, body.Domain)
			if _, err := runSilent("kubectl", "--context", ctx, "-n", ns, "patch", "ingress", body.IngressName,
				"--type=json", "-p", hostPatch); err != nil {
				send("step", "Warning: could not set host on ingress rule — set it manually")
			}
		}

		send("step", "Ingress patched — cert-manager will issue the certificate via HTTP-01 challenge")
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

	// Validate retention before starting the install
	if err := validateRetention(prodMetricsRetention); err != nil {
		send("error", err.Error())
		return
	}

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
