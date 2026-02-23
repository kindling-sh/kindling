package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// actionResult is the standard JSON envelope for mutation endpoints.
type actionResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

func actionOK(w http.ResponseWriter, output string) {
	jsonResponse(w, actionResult{OK: true, Output: output})
}

func actionErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(actionResult{OK: false, Error: msg})
}

// requireMethod returns true if the method matches; otherwise writes 405.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	actionErr(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// captureKubectl runs a kubectl command against the active cluster and returns output.
func captureKubectl(args ...string) (string, error) {
	full := append([]string{"--context", "kind-" + clusterName}, args...)
	return runSilent("kubectl", full...)
}

// ── POST /api/deploy ────────────────────────────────────────────
// Accepts { "yaml": "<manifest>" } and applies it via kubectl.

func handleDeployAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.YAML) == "" {
		actionErr(w, "request body must contain { \"yaml\": \"...\" }", http.StatusBadRequest)
		return
	}

	cmd := exec.Command("kubectl", "--context", "kind-"+clusterName, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(body.YAML)
	out, err := cmd.CombinedOutput()
	if err != nil {
		actionErr(w, string(out), http.StatusUnprocessableEntity)
		return
	}
	actionOK(w, string(out))
}

// ── DELETE /api/dses/{namespace}/{name} ─────────────────────────

func handleDeleteDSE(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/dses/"), "/")
	if len(parts) < 2 {
		actionErr(w, "usage: DELETE /api/dses/{namespace}/{name}", http.StatusBadRequest)
		return
	}
	ns, name := parts[0], parts[1]
	out, err := captureKubectl("delete", "devstagingenvironment", name, "-n", ns)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── POST /api/secrets/create ────────────────────────────────────
// Body: { "name": "KEY", "value": "val", "namespace": "default" }

func handleCreateSecret(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Name      string `json:"name"`
		Value     string `json:"value"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Value == "" {
		actionErr(w, "name and value are required", http.StatusBadRequest)
		return
	}
	if body.Namespace == "" {
		body.Namespace = "default"
	}

	// Delete existing if present (ignore error)
	captureKubectl("delete", "secret", body.Name, "-n", body.Namespace, "--ignore-not-found")

	// Create
	out, err := captureKubectl("create", "secret", "generic", body.Name,
		"--from-literal="+body.Name+"="+body.Value,
		"-n", body.Namespace)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}

	// Label it as kindling-managed
	captureKubectl("label", "secret", body.Name,
		"-n", body.Namespace,
		"app.kubernetes.io/managed-by=kindling", "--overwrite")

	actionOK(w, out)
}

// ── DELETE /api/secrets/{namespace}/{name} ───────────────────────

func handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/secrets/"), "/")
	if len(parts) < 2 {
		actionErr(w, "usage: DELETE /api/secrets/{namespace}/{name}", http.StatusBadRequest)
		return
	}
	ns, name := parts[0], parts[1]
	out, err := captureKubectl("delete", "secret", name, "-n", ns)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── POST /api/runners/create ────────────────────────────────────
// Body: { "username": "...", "repo": "...", "token": "..." }

func handleCreateRunner(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Repo     string `json:"repo"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Username == "" || body.Repo == "" || body.Token == "" {
		actionErr(w, "username, repo, and token are all required", http.StatusBadRequest)
		return
	}

	var outputs []string

	// 1. Create/update github-runner-token secret
	captureKubectl("delete", "secret", "github-runner-token", "-n", "default", "--ignore-not-found")
	out, err := captureKubectl("create", "secret", "generic", "github-runner-token",
		"--from-literal=github-token="+body.Token,
		"-n", "default")
	if err != nil {
		actionErr(w, "failed to create token secret: "+out, http.StatusInternalServerError)
		return
	}
	outputs = append(outputs, out)

	// 2. Apply runner pool CR
	yaml := fmt.Sprintf(`apiVersion: apps.example.com/v1alpha1
kind: GithubActionRunnerPool
metadata:
  name: %s-runner-pool
  namespace: default
spec:
  githubUsername: %s
  repository: %s
  tokenSecretRef:
    name: github-runner-token
    key: github-token
  replicas: 1
  labels:
    - kindling`, body.Username, body.Username, body.Repo)

	cmd := exec.Command("kubectl", "--context", "kind-"+clusterName, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	applyOut, err := cmd.CombinedOutput()
	if err != nil {
		actionErr(w, "failed to apply runner pool: "+string(applyOut), http.StatusInternalServerError)
		return
	}
	outputs = append(outputs, string(applyOut))
	actionOK(w, strings.Join(outputs, "\n"))
}

// ── POST /api/reset-runners ─────────────────────────────────────

func handleResetRunners(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var outputs []string

	out, err := captureKubectl("delete", "githubactionrunnerpools", "--all", "-n", "default")
	if err == nil {
		outputs = append(outputs, out)
	}
	out2, _ := captureKubectl("delete", "secret", "github-runner-token", "-n", "default", "--ignore-not-found")
	outputs = append(outputs, out2)
	actionOK(w, strings.Join(outputs, "\n"))
}

// ── POST /api/env/set ───────────────────────────────────────────
// Body: { "deployment": "name", "namespace": "default", "env": { "KEY": "val", ... } }

func handleEnvSet(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Deployment string            `json:"deployment"`
		Namespace  string            `json:"namespace"`
		Env        map[string]string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Deployment == "" || len(body.Env) == 0 {
		actionErr(w, "deployment and env are required", http.StatusBadRequest)
		return
	}
	if body.Namespace == "" {
		body.Namespace = "default"
	}

	args := []string{"set", "env", "deployment/" + body.Deployment, "-n", body.Namespace}
	for k, v := range body.Env {
		args = append(args, fmt.Sprintf("%s=%s", k, v))
	}
	out, err := captureKubectl(args...)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── POST /api/env/unset ─────────────────────────────────────────
// Body: { "deployment": "name", "namespace": "default", "keys": ["KEY1", "KEY2"] }

func handleEnvUnset(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Deployment string   `json:"deployment"`
		Namespace  string   `json:"namespace"`
		Keys       []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Deployment == "" || len(body.Keys) == 0 {
		actionErr(w, "deployment and keys are required", http.StatusBadRequest)
		return
	}
	if body.Namespace == "" {
		body.Namespace = "default"
	}

	args := []string{"set", "env", "deployment/" + body.Deployment, "-n", body.Namespace}
	for _, k := range body.Keys {
		args = append(args, k+"-")
	}
	out, err := captureKubectl(args...)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── GET /api/env/list/{namespace}/{deployment} ──────────────────

func handleEnvList(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/env/list/"), "/")
	if len(parts) < 2 {
		actionErr(w, "usage: /api/env/list/{namespace}/{deployment}", http.StatusBadRequest)
		return
	}
	ns, dep := parts[0], parts[1]

	out, err := kubectlJSON("get", "deployment", dep, "-n", ns, "-o", "json")
	if err != nil {
		actionErr(w, "deployment not found", http.StatusNotFound)
		return
	}

	// Parse and extract env vars from the first container
	var deployment map[string]interface{}
	if err := json.Unmarshal([]byte(out), &deployment); err != nil {
		actionErr(w, "parse error", http.StatusInternalServerError)
		return
	}

	type envVar struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var envVars []envVar

	if spec, ok := deployment["spec"].(map[string]interface{}); ok {
		if tmpl, ok := spec["template"].(map[string]interface{}); ok {
			if tspec, ok := tmpl["spec"].(map[string]interface{}); ok {
				if containers, ok := tspec["containers"].([]interface{}); ok && len(containers) > 0 {
					if c, ok := containers[0].(map[string]interface{}); ok {
						if env, ok := c["env"].([]interface{}); ok {
							for _, e := range env {
								if ev, ok := e.(map[string]interface{}); ok {
									name, _ := ev["name"].(string)
									value, _ := ev["value"].(string)
									envVars = append(envVars, envVar{Name: name, Value: value})
								}
							}
						}
					}
				}
			}
		}
	}

	if envVars == nil {
		envVars = []envVar{}
	}
	jsonResponse(w, envVars)
}

// ── POST /api/expose ────────────────────────────────────────────
// Starts a cloudflared tunnel using the same flow as the CLI.
// Body: { "service": "my-ingress" } (optional)

func handleExposeAction(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		handleUnexpose(w, r)
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	if !commandExists("cloudflared") {
		actionErr(w, "cloudflared is not installed. Install it with: brew install cloudflared", http.StatusUnprocessableEntity)
		return
	}

	// Check if already running — same check the CLI uses.
	if info, _ := readTunnelInfo(); info != nil && info.PID > 0 {
		if processAlive(info.PID) {
			actionErr(w, "tunnel already running — stop it first", http.StatusConflict)
			return
		}
		// Stale PID — clean up before starting fresh.
		cleanupTunnel()
	}

	// Parse optional service from body.
	var body struct {
		Service string `json:"service"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	exposeService = body.Service

	// Start cloudflared — same as CLI's runCloudflaredTunnel().
	tunnelCmd := exec.Command("cloudflared", "tunnel",
		"--url", fmt.Sprintf("http://localhost:%d", exposePort),
	)

	var stderrBuf bytes.Buffer
	var mu sync.Mutex
	pr, pw := io.Pipe()
	tunnelCmd.Stdout = nil
	tunnelCmd.Stderr = pw
	tunnelCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				mu.Lock()
				stderrBuf.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	if err := tunnelCmd.Start(); err != nil {
		pw.Close()
		actionErr(w, "failed to start cloudflared: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Poll stderr for the tunnel URL — cap at 15s so the HTTP response
	// doesn't time out in the browser.
	var publicURL string
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		mu.Lock()
		data := stderrBuf.String()
		mu.Unlock()
		for _, line := range strings.Split(data, "\n") {
			if strings.Contains(line, ".trycloudflare.com") {
				for _, word := range strings.Fields(line) {
					if strings.HasPrefix(word, "https://") && strings.Contains(word, ".trycloudflare.com") {
						publicURL = strings.TrimRight(word, "|, ")
						break
					}
				}
			}
		}
		if publicURL != "" {
			break
		}
	}

	if publicURL == "" {
		if tunnelCmd.Process != nil {
			_ = tunnelCmd.Process.Kill()
		}
		pw.Close()
		actionErr(w, "Tunnel started but could not detect public URL — try again or use the CLI", http.StatusInternalServerError)
		return
	}

	// Persist immediately and respond — the frontend polls /api/expose/status
	// for liveness, so we don't need to block on DNS propagation here.
	saveTunnelInfo(publicURL, "cloudflared", tunnelCmd.Process.Pid)
	patchIngressesForTunnel(publicURL)

	// Release the child — runs in background.
	go func() {
		_ = tunnelCmd.Wait()
		pw.Close()
	}()

	actionOK(w, "Tunnel started: "+publicURL)
}

// ── DELETE /api/expose ──────────────────────────────────────────
// Stops a running tunnel using the same flow as `kindling expose --stop`.

func handleUnexpose(w http.ResponseWriter, r *http.Request) {
	if err := stopTunnel(); err != nil {
		actionErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actionOK(w, "Tunnel stopped")
}

// ── GET /api/expose/status ──────────────────────────────────────
// Reads tunnel state from .kindling/tunnel.yaml — same source as the CLI.

func handleExposeStatus(w http.ResponseWriter, r *http.Request) {
	type status struct {
		Running  bool   `json:"running"`
		URL      string `json:"url,omitempty"`
		DNSReady bool   `json:"dns_ready"`
	}

	info, err := readTunnelInfo()
	if err != nil || info == nil || info.PID == 0 {
		jsonResponse(w, status{})
		return
	}

	if !processAlive(info.PID) {
		// Stale — clean up.
		cleanupTunnel()
		jsonResponse(w, status{})
		return
	}

	// Quick DNS check via public resolver so we don't tell the browser
	// to visit a URL that will NXDOMAIN (and get cached).
	dnsOK := checkDNSOnce(info.URL)

	jsonResponse(w, status{Running: true, URL: info.URL, DNSReady: dnsOK})
}

// ── DELETE /api/cluster ─────────────────────────────────────────
// Destroys the Kind cluster.

func handleDestroyCluster(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	if !clusterExists(clusterName) {
		actionErr(w, "cluster '"+clusterName+"' does not exist", http.StatusNotFound)
		return
	}

	out, err := runSilent("kind", "delete", "cluster", "--name", clusterName)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── POST /api/init ──────────────────────────────────────────────
// Streams init progress as newline-delimited JSON (SSE-like).

func handleInitCluster(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	flusher, canFlush := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")

	send := func(msg string) {
		json.NewEncoder(w).Encode(map[string]string{"status": msg})
		if canFlush {
			flusher.Flush()
		}
	}

	// Preflight
	for _, bin := range []string{"kind", "kubectl", "docker"} {
		if !commandExists(bin) {
			json.NewEncoder(w).Encode(actionResult{OK: false, Error: bin + " is not installed"})
			return
		}
	}

	if clusterExists(clusterName) {
		send("Cluster '" + clusterName + "' already exists — skipping creation")
	} else {
		send("Creating Kind cluster '" + clusterName + "'...")
		projDir, err := resolveProjectDir()
		if err != nil {
			json.NewEncoder(w).Encode(actionResult{OK: false, Error: err.Error()})
			return
		}
		configPath := projDir + "/kind-config.yaml"
		out, err := runSilent("kind", "create", "cluster", "--name", clusterName, "--config", configPath)
		if err != nil {
			json.NewEncoder(w).Encode(actionResult{OK: false, Error: "kind create failed: " + out})
			return
		}
		send("Cluster created")
	}

	// Setup ingress
	send("Setting up ingress-nginx...")
	projDir, _ := resolveProjectDir()
	ingressScript := projDir + "/setup-ingress.sh"
	if _, err := os.Stat(ingressScript); err == nil {
		out, err := runSilent("bash", ingressScript)
		if err != nil {
			send("Warning: ingress setup issue: " + out)
		} else {
			send("Ingress-nginx configured")
		}
	}

	// Build operator image
	send("Building operator image...")
	out, err := runSilent("docker", "build", "-t", "kindling-operator:latest", projDir)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "docker build failed: " + out})
		return
	}
	send("Operator image built")

	// Load into Kind
	send("Loading image into Kind cluster...")
	out, err = runSilent("kind", "load", "docker-image", "kindling-operator:latest", "--name", clusterName)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "kind load failed: " + out})
		return
	}
	send("Image loaded")

	// Apply CRDs
	send("Applying CRDs...")
	crdDir := projDir + "/config/crd/bases"
	out, err = captureKubectl("apply", "-f", crdDir)
	if err != nil {
		send("Warning: CRD apply issue: " + out)
	} else {
		send("CRDs applied")
	}

	// Deploy operator via kustomize
	send("Deploying operator...")
	kustomize, err := ensureKustomize(projDir)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: err.Error()})
		return
	}
	kOut, err := runCapture(kustomize, "build", projDir+"/config/default")
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "kustomize build failed"})
		return
	}
	cmd := exec.Command("kubectl", "--context", "kind-"+clusterName, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(kOut)
	applyOut, err := cmd.CombinedOutput()
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "operator deploy failed: " + string(applyOut)})
		return
	}
	send("Operator deployed")

	// Wait for rollout
	send("Waiting for operator rollout...")
	out, err = captureKubectl("rollout", "status", "deployment/kindling-controller-manager",
		"-n", "kindling-system", "--timeout=120s")
	if err != nil {
		send("Warning: rollout timeout: " + out)
	} else {
		send("Operator is ready")
	}

	// Deploy registry
	send("Deploying in-cluster registry...")
	registryManifest := projDir + "/config/registry/registry.yaml"
	if _, statErr := os.Stat(registryManifest); statErr == nil {
		out, err = captureKubectl("apply", "-f", registryManifest)
		if err != nil {
			send("Warning: registry deploy issue: " + out)
		} else {
			send("Registry deployed")
		}
	}

	json.NewEncoder(w).Encode(actionResult{OK: true, Output: "Cluster initialization complete"})
}

// ── POST /api/restart/{namespace}/{deployment} ──────────────────

func handleRestartDeployment(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/restart/"), "/")
	if len(parts) < 2 {
		actionErr(w, "usage: POST /api/restart/{namespace}/{deployment}", http.StatusBadRequest)
		return
	}
	ns, dep := parts[0], parts[1]
	out, err := captureKubectl("rollout", "restart", "deployment/"+dep, "-n", ns)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── POST /api/scale/{namespace}/{deployment} ────────────────────
// Body: { "replicas": 3 }

func handleScaleDeployment(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/scale/"), "/")
	if len(parts) < 2 {
		actionErr(w, "usage: POST /api/scale/{namespace}/{deployment}", http.StatusBadRequest)
		return
	}
	ns, dep := parts[0], parts[1]

	var body struct {
		Replicas int `json:"replicas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid body", http.StatusBadRequest)
		return
	}

	out, err := captureKubectl("scale", "deployment/"+dep, "-n", ns,
		fmt.Sprintf("--replicas=%d", body.Replicas))
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── DELETE /api/pods/{namespace}/{name} ──────────────────────────

func handleDeletePod(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/pods/"), "/")
	if len(parts) < 2 {
		actionErr(w, "usage: DELETE /api/pods/{namespace}/{pod}", http.StatusBadRequest)
		return
	}
	ns, name := parts[0], parts[1]
	out, err := captureKubectl("delete", "pod", name, "-n", ns)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── POST /api/apply ─────────────────────────────────────────────
// Generic kubectl apply — Body: { "yaml": "..." }

func handleApplyYAML(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		actionErr(w, "failed to read body", http.StatusBadRequest)
		return
	}
	var payload struct {
		YAML string `json:"yaml"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.YAML) == "" {
		actionErr(w, "body must contain {\"yaml\": \"...\"}", http.StatusBadRequest)
		return
	}

	cmd := exec.Command("kubectl", "--context", "kind-"+clusterName, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(payload.YAML)
	out, err := cmd.CombinedOutput()
	if err != nil {
		actionErr(w, string(out), http.StatusUnprocessableEntity)
		return
	}
	actionOK(w, string(out))
}

// ── Sync state ──────────────────────────────────────────────────
// Tracks a running file-sync session (one per dashboard instance).

var (
	activeSyncMu   sync.Mutex
	activeSyncStop chan struct{} // closed to signal stop
	activeSyncInfo *syncInfo
)

type syncInfo struct {
	Deployment    string    `json:"deployment"`
	Namespace     string    `json:"namespace"`
	Src           string    `json:"src"`
	Dest          string    `json:"dest"`
	Container     string    `json:"container,omitempty"`
	Restart       bool      `json:"restart"`
	Pod           string    `json:"pod"`
	SyncCount     int       `json:"sync_count"`
	LastSync      time.Time `json:"last_sync,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	IsFrontend    bool      `json:"is_frontend"`
	SavedRevision string    `json:"-"` // deployment revision before sync, for rollback
	WasPatched    bool      `json:"-"` // true if deployment command was patched (wrapper)
}

// ── POST /api/sync ──────────────────────────────────────────────
// Starts a file-sync session for a deployment.

func handleSyncAction(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		handleSyncStop(w, r)
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var body struct {
		Deployment string `json:"deployment"`
		Namespace  string `json:"namespace"`
		Src        string `json:"src"`
		Dest       string `json:"dest"`
		Container  string `json:"container"`
		Restart    bool   `json:"restart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Deployment == "" {
		actionErr(w, "deployment is required", http.StatusBadRequest)
		return
	}
	if body.Namespace == "" {
		body.Namespace = "default"
	}
	if body.Src == "" {
		body.Src = "."
	}
	if body.Dest == "" {
		body.Dest = "/app"
	}

	srcAbs, err := filepath.Abs(body.Src)
	if err != nil {
		actionErr(w, "invalid src path: "+err.Error(), http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(srcAbs); err != nil || !info.IsDir() {
		actionErr(w, "src directory does not exist: "+srcAbs, http.StatusBadRequest)
		return
	}

	activeSyncMu.Lock()
	if activeSyncStop != nil {
		activeSyncMu.Unlock()
		actionErr(w, "sync already running — stop it first", http.StatusConflict)
		return
	}

	// Find the target pod
	pod, err := findPodForDeployment(body.Deployment, body.Namespace)
	if err != nil {
		activeSyncMu.Unlock()
		actionErr(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	// Detect the sync mode for the watch loop (must happen before initial
	// sync so we can back up the html root for frontend projects).
	frontend := isFrontendProject(srcAbs)
	profile, _ := detectRuntime(pod, body.Namespace, body.Container)
	if strings.HasPrefix(profile.Name, "unknown") && srcAbs != "" {
		if detected := detectLanguageFromSource(srcAbs); detected != "" {
			if p, ok := runtimeTable[detected]; ok {
				profile = p
			}
		}
	}
	compiled := profile.Mode == modeRebuild

	if frontend {
		body.Dest = detectNginxHtmlRoot(pod, body.Namespace, body.Container)
	}

	// Save the current deployment revision so we can rollback on stop.
	savedRevision := getDeploymentRevision(body.Deployment, body.Namespace)

	// Use the unified syncAndRestart dispatcher for the initial sync.
	// This handles ALL modes: frontend build, Go cross-compile, signal reload,
	// wrapper+kill, etc. — the same logic as `kindling sync` CLI.
	newPod, syncErr := syncAndRestart(pod, body.Namespace, body.Container, srcAbs, body.Dest, nil)
	if syncErr != nil {
		activeSyncMu.Unlock()
		actionErr(w, "initial sync failed: "+syncErr.Error(), http.StatusInternalServerError)
		return
	}
	pod = newPod

	// Check if the deployment was actually patched (revision changed).
	// This handles fallback cases (e.g. modeSignal failing → wrapper restart).
	postRevision := getDeploymentRevision(body.Deployment, body.Namespace)
	wasPatched := postRevision != savedRevision

	stopCh := make(chan struct{})
	activeSyncStop = stopCh
	now := time.Now()
	activeSyncInfo = &syncInfo{
		Deployment:    body.Deployment,
		Namespace:     body.Namespace,
		Src:           srcAbs,
		Dest:          body.Dest,
		Container:     body.Container,
		Restart:       body.Restart,
		Pod:           pod,
		SyncCount:     1,
		LastSync:      now,
		StartedAt:     now,
		IsFrontend:    frontend,
		SavedRevision: savedRevision,
		WasPatched:    wasPatched,
	}
	activeSyncMu.Unlock()

	// Start the watcher in a goroutine
	go runDashboardSync(body.Deployment, body.Namespace, srcAbs, body.Dest, body.Container, body.Restart, frontend, compiled, stopCh)

	modeDesc := profile.Name
	if frontend {
		modeDesc = "frontend build"
	} else if compiled {
		modeDesc = fmt.Sprintf("%s (cross-compile)", profile.Name)
	}
	actionOK(w, fmt.Sprintf("Sync started (%s): %s → %s:%s", modeDesc, srcAbs, pod, body.Dest))
}

// runDashboardSync is the background goroutine for dashboard-initiated sync.
func runDashboardSync(deployment, namespace, srcDir, dest, container string, restart, frontend, compiled bool, stopCh chan struct{}) {
	excludes := append([]string{}, defaultExcludes...)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		activeSyncMu.Lock()
		activeSyncStop = nil
		activeSyncInfo = nil
		activeSyncMu.Unlock()
		return
	}
	defer func() {
		watcher.Close()

		// Restore the deployment to its pre-sync state.
		// If the deployment command was patched (wrapper for Go/Python/Node),
		// rollout undo reverts both the command AND creates a fresh pod.
		// Otherwise, rollout restart just creates a fresh pod from the image.
		activeSyncMu.Lock()
		info := activeSyncInfo
		activeSyncMu.Unlock()

		if info != nil {
			ctx := "kind-" + clusterName
			if info.WasPatched && info.SavedRevision != "" {
				step("♻️", fmt.Sprintf("Rolling back deployment/%s to revision %s", deployment, info.SavedRevision))
				_ = run("kubectl", "rollout", "undo", fmt.Sprintf("deployment/%s", deployment),
					"-n", namespace, "--context", ctx,
					fmt.Sprintf("--to-revision=%s", info.SavedRevision))
			} else {
				step("♻️", fmt.Sprintf("Restarting deployment/%s to restore original state", deployment))
				_ = run("kubectl", "rollout", "restart", fmt.Sprintf("deployment/%s", deployment),
					"-n", namespace, "--context", ctx)
			}
			_ = run("kubectl", "rollout", "status", fmt.Sprintf("deployment/%s", deployment),
				"-n", namespace, "--context", ctx, "--timeout=90s")
			success(fmt.Sprintf("Deployment %s restored to original state", deployment))
		}

		activeSyncMu.Lock()
		activeSyncStop = nil
		activeSyncInfo = nil
		activeSyncMu.Unlock()
	}()

	if err := addWatchDirRecursive(watcher, srcDir, excludes); err != nil {
		return
	}

	var debounceTimer *time.Timer
	pendingFiles := make(map[string]bool)

	flushSync := func() {
		if len(pendingFiles) == 0 {
			return
		}

		activeSyncMu.Lock()
		info := activeSyncInfo
		activeSyncMu.Unlock()
		if info == nil {
			return
		}

		// Re-discover pod
		pod, err := findPodForDeployment(deployment, namespace)
		if err != nil {
			pendingFiles = make(map[string]bool)
			return
		}

		pendingFiles = make(map[string]bool)

		if frontend {
			// Frontend: rebuild and sync dist/ — don't sync individual source files
			profile := runtimeProfile{Name: "Nginx", Mode: modeSignal, Signal: "HUP"}
			if _, err := restartViaFrontendBuild(pod, namespace, container, srcDir, profile); err != nil {
				// Build failed — don't update sync count
				return
			}

			activeSyncMu.Lock()
			if activeSyncInfo != nil {
				activeSyncInfo.Pod = pod
				activeSyncInfo.SyncCount++
				activeSyncInfo.LastSync = time.Now()
			}
			activeSyncMu.Unlock()
			return
		}

		if compiled {
			// Compiled languages (Go, Rust, etc.): use syncAndRestart which
			// handles cross-compile + binary sync — don't sync individual files
			newPod, err := syncAndRestart(pod, namespace, container, srcDir, dest, nil)
			if err != nil {
				return
			}

			activeSyncMu.Lock()
			if activeSyncInfo != nil {
				activeSyncInfo.Pod = newPod
				activeSyncInfo.SyncCount++
				activeSyncInfo.LastSync = time.Now()
			}
			activeSyncMu.Unlock()
			return
		}

		// Standard file sync
		fileList := make([]string, 0, len(pendingFiles))
		for f := range pendingFiles {
			fileList = append(fileList, f)
		}

		var synced int
		for _, localPath := range fileList {
			relPath, _ := filepath.Rel(srcDir, localPath)
			destPath := filepath.Join(dest, relPath)
			destPath = strings.ReplaceAll(destPath, "\\", "/")
			if syncFile(pod, namespace, localPath, destPath, container) == nil {
				synced++
			}
		}

		if restart && synced > 0 {
			_, _ = syncAndRestart(pod, namespace, container, srcDir, dest, nil)
		}

		activeSyncMu.Lock()
		if activeSyncInfo != nil {
			activeSyncInfo.Pod = pod
			activeSyncInfo.SyncCount += synced
			activeSyncInfo.LastSync = time.Now()
		}
		activeSyncMu.Unlock()
	}

	for {
		select {
		case <-stopCh:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			flushSync()
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}
			relPath, _ := filepath.Rel(srcDir, event.Name)
			if shouldExclude(relPath, excludes) {
				continue
			}
			if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
				if event.Has(fsnotify.Create) {
					_ = addWatchDirRecursive(watcher, event.Name, excludes)
				}
				continue
			}
			pendingFiles[event.Name] = true
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(500*time.Millisecond, flushSync)

		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// ── DELETE /api/sync ────────────────────────────────────────────
func handleSyncStop(w http.ResponseWriter, r *http.Request) {
	activeSyncMu.Lock()
	defer activeSyncMu.Unlock()

	if activeSyncStop == nil {
		actionErr(w, "no sync session running", http.StatusNotFound)
		return
	}
	close(activeSyncStop)
	actionOK(w, "Sync stopped")
}

// ── GET /api/sync/status ────────────────────────────────────────
func handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	activeSyncMu.Lock()
	defer activeSyncMu.Unlock()

	type status struct {
		Running    bool      `json:"running"`
		Deployment string    `json:"deployment,omitempty"`
		Namespace  string    `json:"namespace,omitempty"`
		Src        string    `json:"src,omitempty"`
		Dest       string    `json:"dest,omitempty"`
		Pod        string    `json:"pod,omitempty"`
		SyncCount  int       `json:"sync_count"`
		LastSync   time.Time `json:"last_sync,omitempty"`
		StartedAt  time.Time `json:"started_at,omitempty"`
	}

	if activeSyncInfo == nil {
		jsonResponse(w, status{})
		return
	}

	jsonResponse(w, status{
		Running:    true,
		Deployment: activeSyncInfo.Deployment,
		Namespace:  activeSyncInfo.Namespace,
		Src:        activeSyncInfo.Src,
		Dest:       activeSyncInfo.Dest,
		Pod:        activeSyncInfo.Pod,
		SyncCount:  activeSyncInfo.SyncCount,
		LastSync:   activeSyncInfo.LastSync,
		StartedAt:  activeSyncInfo.StartedAt,
	})
}

// ── POST /api/load ──────────────────────────────────────────────
// Builds a Docker image, loads it into Kind, and patches the DSE.
// Body: { "service": "...", "context": ".", "dockerfile": "Dockerfile",
//         "namespace": "default", "no_deploy": false, "platform": "" }

func handleLoadAction(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var body struct {
		Service    string `json:"service"`
		Context    string `json:"context"`
		Dockerfile string `json:"dockerfile"`
		Namespace  string `json:"namespace"`
		NoDeploy   bool   `json:"no_deploy"`
		Platform   string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.Service == "" {
		actionErr(w, "service is required", http.StatusBadRequest)
		return
	}
	if body.Context == "" {
		body.Context = "."
	}
	if body.Namespace == "" {
		body.Namespace = "default"
	}

	ctxAbs, err := filepath.Abs(body.Context)
	if err != nil {
		actionErr(w, "cannot resolve context path: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(ctxAbs); os.IsNotExist(err) {
		actionErr(w, "context directory does not exist: "+ctxAbs, http.StatusBadRequest)
		return
	}

	// Resolve Dockerfile
	dockerfile := body.Dockerfile
	if dockerfile == "" {
		dockerfile = filepath.Join(ctxAbs, "Dockerfile")
	} else if !filepath.IsAbs(dockerfile) {
		dockerfile = filepath.Join(ctxAbs, dockerfile)
	}
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		actionErr(w, "Dockerfile not found: "+dockerfile, http.StatusBadRequest)
		return
	}

	// Check cluster
	if !clusterExists(clusterName) {
		actionErr(w, "Kind cluster not found — run kindling init first", http.StatusUnprocessableEntity)
		return
	}

	imageTag := loadImageTag(body.Service)
	var outputs []string

	// 1. Docker build
	dockerArgs := []string{"build", "-t", imageTag, "-f", dockerfile}
	if body.Platform != "" {
		dockerArgs = append(dockerArgs, "--platform", body.Platform)
	}
	dockerArgs = append(dockerArgs, ctxAbs)

	buildOut, err := runCapture("docker", dockerArgs...)
	if err != nil {
		actionErr(w, "docker build failed: "+buildOut, http.StatusInternalServerError)
		return
	}
	outputs = append(outputs, "✓ Image built: "+imageTag)

	// 2. Load into Kind
	loadOut, err := runCapture("kind", "load", "docker-image", imageTag, "--name", clusterName)
	if err != nil {
		actionErr(w, "kind load failed: "+loadOut, http.StatusInternalServerError)
		return
	}
	outputs = append(outputs, "✓ Image loaded into cluster")

	// 3. Patch DSE (unless no_deploy)
	if !body.NoDeploy {
		// Check DSE exists
		if _, err := runCapture("kubectl", "--context", "kind-"+clusterName,
			"get", "dse", body.Service, "-n", body.Namespace); err != nil {
			// Try patching deployment directly if no DSE
			patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"%s","image":"%s"}]}}}}`,
				body.Service, imageTag)
			patchOut, err := runCapture("kubectl", "--context", "kind-"+clusterName,
				"patch", "deployment", body.Service,
				"-n", body.Namespace,
				"--type=strategic",
				"-p", patch)
			if err != nil {
				actionErr(w, "no DSE or deployment found for "+body.Service+": "+patchOut, http.StatusUnprocessableEntity)
				return
			}
			outputs = append(outputs, "✓ Deployment patched: "+body.Service+" → "+imageTag)
		} else {
			// Patch the DSE image
			patch := fmt.Sprintf(`{"spec":{"deployment":{"image":"%s"}}}`, imageTag)
			patchOut, err := runCapture("kubectl", "--context", "kind-"+clusterName,
				"patch", "dse", body.Service,
				"-n", body.Namespace,
				"--type=merge",
				"-p", patch)
			if err != nil {
				actionErr(w, "failed to patch DSE: "+patchOut, http.StatusInternalServerError)
				return
			}
			outputs = append(outputs, "✓ DSE patched: "+body.Service+" → "+imageTag)
		}

		// Wait for rollout (with timeout)
		rollOut, _ := runCapture("kubectl", "--context", "kind-"+clusterName,
			"rollout", "status", "deployment/"+body.Service,
			"-n", body.Namespace, "--timeout=60s")
		if strings.Contains(rollOut, "successfully rolled out") {
			outputs = append(outputs, "✓ Rollout complete")
		} else {
			outputs = append(outputs, "⚠ Rollout may still be in progress")
		}
	} else {
		outputs = append(outputs, "⏭ Skipped deploy (no_deploy=true)")
	}

	actionOK(w, strings.Join(outputs, "\n"))
}
