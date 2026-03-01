package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jeffvincent/kindling/cli/core"
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
	return core.Kubectl(clusterName, args...)
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

	out, err := core.KubectlApplyStdin(clusterName, body.YAML)
	if err != nil {
		actionErr(w, out, http.StatusUnprocessableEntity)
		return
	}
	actionOK(w, out)
}

// ── DELETE /api/dses/{namespace}/{name} ─────────────────────────

func handleDeleteDSE(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/dses/", 2)
	if err != nil {
		actionErr(w, "usage: DELETE /api/dses/{namespace}/{name}", http.StatusBadRequest)
		return
	}
	ns, name := parts[0], parts[1]
	out, err := core.DeleteDSE(clusterName, name, ns)
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

	out, err := core.CreateSecret(core.SecretConfig{
		ClusterName: clusterName,
		Name:        body.Name,
		Value:       body.Value,
		Namespace:   body.Namespace,
	})
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── DELETE /api/secrets/{namespace}/{name} ───────────────────────

func handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/secrets/", 2)
	if err != nil {
		actionErr(w, "usage: DELETE /api/secrets/{namespace}/{name}", http.StatusBadRequest)
		return
	}
	ns, name := parts[0], parts[1]
	out, err := core.DeleteSecretByK8sName(clusterName, name, ns)
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

	outputs, err := core.CreateRunnerPool(core.RunnerPoolConfig{
		ClusterName: clusterName,
		Username:    body.Username,
		Repo:        body.Repo,
		Token:       body.Token,
		Namespace:   "default",
	})
	if err != nil {
		actionErr(w, strings.Join(outputs, "\n")+"\n"+err.Error(), http.StatusInternalServerError)
		return
	}
	actionOK(w, strings.Join(outputs, "\n"))
}

// ── POST /api/reset-runners ─────────────────────────────────────

func handleResetRunners(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	outputs, err := core.ResetRunners(clusterName, "default", "")
	if err != nil {
		actionErr(w, strings.Join(outputs, "\n")+"\n"+err.Error(), http.StatusInternalServerError)
		return
	}
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

	var pairs []string
	for k, v := range body.Env {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	out, err := core.SetEnv(clusterName, body.Deployment, body.Namespace, pairs)
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

	out, err := core.UnsetEnv(clusterName, body.Deployment, body.Namespace, body.Keys)
	if err != nil {
		actionErr(w, out, http.StatusInternalServerError)
		return
	}
	actionOK(w, out)
}

// ── GET /api/env/list/{namespace}/{deployment} ──────────────────

func handleEnvList(w http.ResponseWriter, r *http.Request) {
	parts, err := parsePathParams(r.URL.Path, "/api/env/list/", 2)
	if err != nil {
		actionErr(w, "usage: /api/env/list/{namespace}/{deployment}", http.StatusBadRequest)
		return
	}
	ns, dep := parts[0], parts[1]

	envVars, err := core.ListEnv(clusterName, dep, ns)
	if err != nil {
		actionErr(w, err.Error(), http.StatusNotFound)
		return
	}

	if envVars == nil {
		envVars = []core.EnvVar{}
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

	if !core.CommandExists("cloudflared") {
		actionErr(w, "cloudflared is not installed. Install it with: brew install cloudflared", http.StatusUnprocessableEntity)
		return
	}

	// Check if already running — same check the CLI uses.
	if info, _ := core.ReadTunnelInfo(); info != nil && info.PID > 0 {
		if core.ProcessAlive(info.PID) {
			actionErr(w, "tunnel already running — stop it first", http.StatusConflict)
			return
		}
		// Stale PID — clean up before starting fresh.
		core.CleanupTunnel(clusterName)
		restoreIngresses()
	}

	// Parse optional service from body.
	var body struct {
		Service string `json:"service"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	exposeService = body.Service

	// Start cloudflared — use the same core function as the CLI (15s timeout for HTTP).
	result, err := core.StartCloudflaredTunnel(exposePort, 15, false)
	if err != nil {
		actionErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	core.SaveTunnelInfo(clusterName, result.PublicURL, "cloudflared", result.PID)
	patchIngressesForTunnel(result.PublicURL)

	actionOK(w, "Tunnel started: "+result.PublicURL)
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

	info, err := core.ReadTunnelInfo()
	if err != nil || info == nil || info.PID == 0 {
		jsonResponse(w, status{})
		return
	}

	if !core.ProcessAlive(info.PID) {
		// Stale — clean up.
		core.CleanupTunnel(clusterName)
		restoreIngresses()
		jsonResponse(w, status{})
		return
	}

	// Quick DNS check via public resolver so we don't tell the browser
	// to visit a URL that will NXDOMAIN (and get cached).
	dnsOK := core.CheckDNSOnce(info.URL)

	jsonResponse(w, status{Running: true, URL: info.URL, DNSReady: dnsOK})
}

// ── DELETE /api/cluster ─────────────────────────────────────────
// Destroys the Kind cluster.

func handleDestroyCluster(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	if !core.ClusterExists(clusterName) {
		actionErr(w, "cluster '"+clusterName+"' does not exist", http.StatusNotFound)
		return
	}

	out, err := core.DestroyCluster(clusterName)
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
		if !core.CommandExists(bin) {
			json.NewEncoder(w).Encode(actionResult{OK: false, Error: bin + " is not installed"})
			return
		}
	}

	if core.ClusterExists(clusterName) {
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
	applyOut, err := core.KubectlApplyStdin(clusterName, kOut)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "operator deploy failed: " + applyOut})
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
	parts, err := parsePathParams(r.URL.Path, "/api/restart/", 2)
	if err != nil {
		actionErr(w, "usage: POST /api/restart/{namespace}/{deployment}", http.StatusBadRequest)
		return
	}
	ns, dep := parts[0], parts[1]
	out, err := core.RestartDeployment(clusterName, dep, ns)
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
	parts, err := parsePathParams(r.URL.Path, "/api/scale/", 2)
	if err != nil {
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

	out, err := core.ScaleDeployment(clusterName, dep, ns, body.Replicas)
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
	parts, err := parsePathParams(r.URL.Path, "/api/pods/", 2)
	if err != nil {
		actionErr(w, "usage: DELETE /api/pods/{namespace}/{pod}", http.StatusBadRequest)
		return
	}
	ns, name := parts[0], parts[1]
	out, err := core.DeletePod(clusterName, name, ns)
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

	out, err := core.KubectlApplyStdin(clusterName, payload.YAML)
	if err != nil {
		actionErr(w, out, http.StatusUnprocessableEntity)
		return
	}
	actionOK(w, out)
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
			ctx := kindContext()
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

	outputs, err := core.BuildAndLoad(core.LoadConfig{
		ClusterName: clusterName,
		Service:     body.Service,
		Context:     body.Context,
		Dockerfile:  body.Dockerfile,
		Namespace:   body.Namespace,
		NoDeploy:    body.NoDeploy,
		Platform:    body.Platform,
	})
	if err != nil {
		actionErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	actionOK(w, strings.Join(outputs, "\n"))
}

// ── GET/POST/DELETE /api/intel ───────────────────────────────────
// GET  → return intel status (active, disabled, files, last interaction)
// POST → activate intel
// DELETE → deactivate intel

func handleIntel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleIntelStatus(w, r)
	case http.MethodPost:
		handleIntelActivate(w, r)
	case http.MethodDelete:
		handleIntelDeactivate(w, r)
	default:
		actionErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleIntelStatus(w http.ResponseWriter, r *http.Request) {
	type intelStatusResponse struct {
		Status          string   `json:"status"` // "active", "disabled", "inactive"
		Files           []string `json:"files,omitempty"`
		LastInteraction string   `json:"last_interaction,omitempty"`
		Timeout         string   `json:"timeout"`
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		jsonResponse(w, intelStatusResponse{Status: "inactive", Timeout: intelSessionTimeout.String()})
		return
	}

	// Check disabled flag
	if _, err := os.Stat(filepath.Join(repoRoot, intelDisabledFile)); err == nil {
		jsonResponse(w, intelStatusResponse{Status: "disabled", Timeout: intelSessionTimeout.String()})
		return
	}

	state, _ := loadIntelState(repoRoot)
	if state != nil && state.Active {
		resp := intelStatusResponse{
			Status:          "active",
			Files:           state.Written,
			LastInteraction: state.LastInteraction,
			Timeout:         intelSessionTimeout.String(),
		}
		jsonResponse(w, resp)
		return
	}

	jsonResponse(w, intelStatusResponse{Status: "inactive", Timeout: intelSessionTimeout.String()})
}

func handleIntelActivate(w http.ResponseWriter, r *http.Request) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		actionErr(w, "cannot find repo root: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Clear disabled flag
	os.Remove(filepath.Join(repoRoot, intelDisabledFile))

	// Check if already active — just touch timestamp
	state, _ := loadIntelState(repoRoot)
	if state != nil && state.Active {
		state.touchInteraction()
		saveIntelState(repoRoot, state)
		actionOK(w, "intel already active (timestamp refreshed)")
		return
	}

	if err := activateIntel(repoRoot, false); err != nil {
		actionErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actionOK(w, "kindling intel activated")
}

func handleIntelDeactivate(w http.ResponseWriter, r *http.Request) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		actionErr(w, "cannot find repo root: "+err.Error(), http.StatusInternalServerError)
		return
	}

	state, _ := loadIntelState(repoRoot)
	if state != nil && state.Active {
		restoreIntel(repoRoot, state)
	}
	scrubKindlingFiles(repoRoot)
	setIntelDisabled(repoRoot)
	actionOK(w, "kindling intel deactivated")
}

// ── POST /api/generate ──────────────────────────────────────────
// Streams ndjson progress, then returns the generated workflow.
// Body: { "apiKey", "repoPath?", "provider?", "model?", "ciProvider?", "branch?", "dryRun?" }

func handleGenerate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var body struct {
		APIKey     string `json:"apiKey"`
		RepoPath   string `json:"repoPath"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		CIProvider string `json:"ciProvider"`
		Branch     string `json:"branch"`
		DryRun     bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.APIKey == "" {
		actionErr(w, "apiKey is required", http.StatusBadRequest)
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

	// Defaults
	repoPath := body.RepoPath
	if repoPath == "" {
		root, err := findRepoRoot()
		if err != nil {
			json.NewEncoder(w).Encode(actionResult{OK: false, Error: "cannot find repo root: " + err.Error()})
			return
		}
		repoPath = root
	} else {
		abs, err := filepath.Abs(repoPath)
		if err != nil {
			json.NewEncoder(w).Encode(actionResult{OK: false, Error: "invalid repo path: " + err.Error()})
			return
		}
		repoPath = abs
	}

	provider := body.Provider
	if provider == "" {
		provider = "openai"
	}
	model := body.Model
	if model == "" {
		switch provider {
		case "anthropic":
			model = "claude-sonnet-4-20250514"
		default:
			model = "o3"
		}
	}

	ciProv, err := resolveProvider(body.CIProvider)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: err.Error()})
		return
	}

	branch := body.Branch
	if branch == "" {
		out, err := runCapture("git", "-C", repoPath, "symbolic-ref", "--short", "HEAD")
		if err == nil {
			branch = strings.TrimSpace(out)
		} else {
			branch = "main"
		}
	}

	// Scan
	send("Scanning repository…")
	repoCtx, err := scanRepo(repoPath)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "repo scan failed: " + err.Error()})
		return
	}
	repoCtx.branch = branch
	send(fmt.Sprintf("Found %d Dockerfile(s), %d dependency manifest(s), %d source file(s)",
		repoCtx.dockerfileCount, repoCtx.depFileCount, len(repoCtx.sourceSnippets)))

	// Call AI
	send(fmt.Sprintf("Calling %s (%s)…", provider, model))
	systemPrompt, userPrompt := buildGeneratePrompt(repoCtx, ciProv)
	workflow, err := callGenAI(provider, body.APIKey, model, systemPrompt, userPrompt)
	if err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "AI generation failed: " + err.Error()})
		return
	}
	workflow = cleanYAMLResponse(workflow)

	if body.DryRun {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":       true,
			"output":   "Workflow generated (dry-run)",
			"workflow": workflow,
		})
		return
	}

	// Write file
	wfGen := ciProv.Workflow()
	outputPath := filepath.Join(repoPath, wfGen.DefaultOutputPath())
	outDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "cannot create output directory: " + err.Error()})
		return
	}
	if err := os.WriteFile(outputPath, []byte(workflow+"\n"), 0644); err != nil {
		json.NewEncoder(w).Encode(actionResult{OK: false, Error: "cannot write workflow file: " + err.Error()})
		return
	}

	relPath, _ := filepath.Rel(repoPath, outputPath)
	if relPath == "" {
		relPath = outputPath
	}
	send("Workflow written to " + relPath)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"output":   "Workflow generated and written to " + relPath,
		"workflow": workflow,
		"path":     relPath,
	})
}

// ═══════════════════════════════════════════════════════════════
// Topology Editor API
// ═══════════════════════════════════════════════════════════════

// ── GET /api/topology/status ────────────────────────────────────
// Returns live pod health for every node in the topology graph.
// The frontend polls this every few seconds and overlays the result.

type topologyNodeStatus struct {
	Phase      string                  `json:"phase"`      // aggregate: Running, Pending, Error, CrashLoopBackOff, Unknown
	Ready      int                     `json:"ready"`      // pods with all containers ready
	Total      int                     `json:"total"`      // total pods matched
	Restarts   int                     `json:"restarts"`   // sum of all container restart counts
	LastDeploy string                  `json:"lastDeploy"` // deployment's last-updated timestamp
	Containers []topologyContainerInfo `json:"containers,omitempty"`
}

type topologyContainerInfo struct {
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Restarts int    `json:"restarts"`
	State    string `json:"state"` // running, waiting, terminated
	Reason   string `json:"reason,omitempty"`
}

func handleGetTopologyStatus(w http.ResponseWriter, r *http.Request) {
	result := make(map[string]*topologyNodeStatus)

	// 1. Get all kindling-managed pods (includes app + dependency pods)
	podOut, err := kubectlJSON("get", "pods", "--all-namespaces",
		"-l", "app.kubernetes.io/managed-by=devstagingenvironment-operator", "-o", "json")
	if err != nil {
		// No pods — return empty
		jsonResponse(w, result)
		return
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
					State        struct {
						Running *struct{} `json:"running"`
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
						Terminated *struct {
							Reason string `json:"reason"`
						} `json:"terminated"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(podOut), &podList); err != nil {
		jsonResponse(w, result)
		return
	}

	// 2. Get all kindling-managed deployments for lastDeploy timestamps
	depOut, _ := kubectlJSON("get", "deployments", "--all-namespaces",
		"-l", "app.kubernetes.io/managed-by=devstagingenvironment-operator", "-o", "json")
	deployTimestamps := make(map[string]string) // "partOf/component" → timestamp
	if depOut != "" {
		var depList struct {
			Items []struct {
				Metadata struct {
					Labels            map[string]string `json:"labels"`
					CreationTimestamp string            `json:"creationTimestamp"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type               string `json:"type"`
						LastTransitionTime string `json:"lastTransitionTime"`
						LastUpdateTime     string `json:"lastUpdateTime"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(depOut), &depList) == nil {
			for _, d := range depList.Items {
				partOf := d.Metadata.Labels["app.kubernetes.io/part-of"]
				component := d.Metadata.Labels["app.kubernetes.io/component"]
				appName := d.Metadata.Labels["app.kubernetes.io/name"]
				// Build key: prefer part-of, fall back to name
				owner := partOf
				if owner == "" {
					owner = appName
				}
				key := owner + "/" + component

				// Use the most recent Progressing condition update time
				ts := d.Metadata.CreationTimestamp
				for _, c := range d.Status.Conditions {
					if c.Type == "Progressing" && c.LastUpdateTime != "" {
						ts = c.LastUpdateTime
					}
				}
				deployTimestamps[key] = ts
			}
		}
	}

	// 3. Map pods to topology node IDs
	// Service nodes: id = "svc-<dseName>", labels: name=<dseName>, component="" or "app"
	// Dependency nodes: id = "dep-<depType>", labels: part-of=<dseName>, component=<depType>
	for _, pod := range podList.Items {
		partOf := pod.Metadata.Labels["app.kubernetes.io/part-of"]
		component := pod.Metadata.Labels["app.kubernetes.io/component"]
		appName := pod.Metadata.Labels["app.kubernetes.io/name"]

		// Determine topology node ID
		var nodeID string
		if component != "" && component != "app" && component != appName && component != partOf {
			// Dependency pod: has a component label like "redis", "postgres", etc.
			nodeID = "dep-" + component
		} else if partOf != "" {
			// Service pod with part-of label
			nodeID = "svc-" + partOf
		} else if appName != "" {
			// Service pod without part-of — use name label
			nodeID = "svc-" + appName
		} else {
			continue
		}

		status, exists := result[nodeID]
		if !exists {
			status = &topologyNodeStatus{Phase: "Unknown"}
			result[nodeID] = status
		}

		status.Total++

		// Check if all containers are ready
		allReady := true
		for _, cs := range pod.Status.ContainerStatuses {
			status.Restarts += cs.RestartCount

			state := "unknown"
			reason := ""
			if cs.State.Running != nil {
				state = "running"
			} else if cs.State.Waiting != nil {
				state = "waiting"
				reason = cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil {
				state = "terminated"
				reason = cs.State.Terminated.Reason
			}

			if !cs.Ready {
				allReady = false
			}

			status.Containers = append(status.Containers, topologyContainerInfo{
				Name:     cs.Name,
				Ready:    cs.Ready,
				Restarts: cs.RestartCount,
				State:    state,
				Reason:   reason,
			})
		}
		if allReady && len(pod.Status.ContainerStatuses) > 0 {
			status.Ready++
		}

		// Determine aggregate phase (worst wins)
		podPhase := pod.Status.Phase
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				podPhase = "CrashLoopBackOff"
				break
			}
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "ImagePullBackOff" {
				podPhase = "ImagePullBackOff"
				break
			}
		}

		// Phase priority: CrashLoopBackOff > ImagePullBackOff > Failed > Pending > Running > Unknown
		status.Phase = worstPhase(status.Phase, podPhase)

		// Attach deploy timestamp
		owner := partOf
		if owner == "" {
			owner = appName
		}
		tsKey := owner + "/" + component
		if ts, ok := deployTimestamps[tsKey]; ok && status.LastDeploy == "" {
			status.LastDeploy = ts
		}
		// Also try without component for services
		if status.LastDeploy == "" {
			if ts, ok := deployTimestamps[owner+"/"]; ok {
				status.LastDeploy = ts
			}
		}
	}

	jsonResponse(w, result)
}

// ── GET /api/topology/logs?node=<nodeId>&tail=200 ───────────────
// Returns aggregated logs from all pods matching a topology node.

func handleTopologyLogs(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")
	if nodeID == "" {
		jsonError(w, "missing ?node= parameter", 400)
		return
	}

	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "200"
	}

	// Derive label selector from node ID
	// Service nodes: svc-<name> → component in (app,<name>), part-of=<name>
	// Dependency nodes: dep-<type> → component=<type>
	var labelSelector string
	if strings.HasPrefix(nodeID, "svc-") {
		name := strings.TrimPrefix(nodeID, "svc-")
		labelSelector = "app.kubernetes.io/managed-by=devstagingenvironment-operator,app.kubernetes.io/name=" + name
	} else if strings.HasPrefix(nodeID, "dep-") {
		depType := strings.TrimPrefix(nodeID, "dep-")
		labelSelector = "app.kubernetes.io/managed-by=devstagingenvironment-operator,app.kubernetes.io/component=" + depType
	} else {
		jsonError(w, "invalid node ID format", 400)
		return
	}

	// Get matching pod names
	podOut, err := kubectlJSON("get", "pods", "--all-namespaces",
		"-l", labelSelector, "-o", "jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name}\n{end}")
	if err != nil || strings.TrimSpace(podOut) == "" {
		jsonResponse(w, map[string]interface{}{
			"lines": []string{},
			"pods":  []string{},
		})
		return
	}

	podRefs := strings.Split(strings.TrimSpace(podOut), "\n")

	type logEntry struct {
		Pod  string `json:"pod"`
		Line string `json:"line"`
	}

	var lines []logEntry
	var podNames []string
	for _, ref := range podRefs {
		parts := strings.SplitN(ref, "/", 2)
		if len(parts) != 2 {
			continue
		}
		ns, podName := parts[0], parts[1]
		podNames = append(podNames, podName)

		out, err := runCapture("kubectl", "logs", podName, "-n", ns, "--tail="+tail, "--timestamps=true")
		if err != nil {
			lines = append(lines, logEntry{Pod: podName, Line: "[error fetching logs: " + err.Error() + "]"})
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line != "" {
				lines = append(lines, logEntry{Pod: podName, Line: line})
			}
		}
	}

	jsonResponse(w, map[string]interface{}{
		"lines": lines,
		"pods":  podNames,
	})
}

// ── GET /api/topology/node/detail?node=<nodeId> ─────────────────
// Returns pods, recent events, and environment variables for a topology node.

func handleTopologyNodeDetail(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")
	if nodeID == "" {
		jsonError(w, "missing ?node= parameter", 400)
		return
	}

	// Derive label selector
	var labelSelector string
	if strings.HasPrefix(nodeID, "svc-") {
		name := strings.TrimPrefix(nodeID, "svc-")
		labelSelector = "app.kubernetes.io/managed-by=devstagingenvironment-operator,app.kubernetes.io/name=" + name
	} else if strings.HasPrefix(nodeID, "dep-") {
		depType := strings.TrimPrefix(nodeID, "dep-")
		labelSelector = "app.kubernetes.io/managed-by=devstagingenvironment-operator,app.kubernetes.io/component=" + depType
	} else {
		jsonError(w, "invalid node ID format", 400)
		return
	}

	type podInfo struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Phase     string `json:"phase"`
		Ready     string `json:"ready"`
		Restarts  int    `json:"restarts"`
		Age       string `json:"age"`
		Node      string `json:"node"`
	}

	type eventInfo struct {
		Type    string `json:"type"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Age     string `json:"age"`
		Count   int    `json:"count"`
	}

	type envVar struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	type deploymentInfo struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Replicas  int    `json:"replicas"`
		Available int    `json:"available"`
	}

	type nodeDetail struct {
		Pods       []podInfo       `json:"pods"`
		Events     []eventInfo     `json:"events"`
		Env        []envVar        `json:"env"`
		Deployment *deploymentInfo `json:"deployment,omitempty"`
	}

	detail := nodeDetail{}

	// 0. Get deployment info (for scale controls)
	depOut, err := kubectlJSON("get", "deployments", "--all-namespaces",
		"-l", labelSelector, "-o", "json")
	if err == nil && depOut != "" {
		var depList struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Replicas int `json:"replicas"`
				} `json:"spec"`
				Status struct {
					AvailableReplicas int `json:"availableReplicas"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(depOut), &depList) == nil && len(depList.Items) > 0 {
			d := depList.Items[0]
			detail.Deployment = &deploymentInfo{
				Name:      d.Metadata.Name,
				Namespace: d.Metadata.Namespace,
				Replicas:  d.Spec.Replicas,
				Available: d.Status.AvailableReplicas,
			}
		}
	}

	// 1. Get pods
	podOut, err := kubectlJSON("get", "pods", "--all-namespaces",
		"-l", labelSelector, "-o", "json")
	if err == nil && podOut != "" {
		var podList struct {
			Items []struct {
				Metadata struct {
					Name              string `json:"name"`
					Namespace         string `json:"namespace"`
					CreationTimestamp string `json:"creationTimestamp"`
				} `json:"metadata"`
				Spec struct {
					NodeName   string `json:"nodeName"`
					Containers []struct {
						Env []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Name         string `json:"name"`
						Ready        bool   `json:"ready"`
						RestartCount int    `json:"restartCount"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(podOut), &podList) == nil {
			for _, p := range podList.Items {
				readyCount := 0
				totalContainers := len(p.Status.ContainerStatuses)
				restarts := 0
				for _, cs := range p.Status.ContainerStatuses {
					if cs.Ready {
						readyCount++
					}
					restarts += cs.RestartCount
				}
				detail.Pods = append(detail.Pods, podInfo{
					Name:      p.Metadata.Name,
					Namespace: p.Metadata.Namespace,
					Phase:     p.Status.Phase,
					Ready:     fmt.Sprintf("%d/%d", readyCount, totalContainers),
					Restarts:  restarts,
					Age:       p.Metadata.CreationTimestamp,
					Node:      p.Spec.NodeName,
				})

				// Collect env vars from first pod only
				if len(detail.Env) == 0 {
					for _, c := range p.Spec.Containers {
						for _, e := range c.Env {
							// Skip Kubernetes-injected vars
							if strings.HasPrefix(e.Name, "KUBERNETES_") {
								continue
							}
							detail.Env = append(detail.Env, envVar{Name: e.Name, Value: e.Value})
						}
					}
				}
			}
		}
	}

	// 2. Get recent events for these pods
	if len(detail.Pods) > 0 {
		ns := detail.Pods[0].Namespace
		evtOut, err := kubectlJSON("get", "events", "-n", ns,
			"--field-selector=involvedObject.kind=Pod",
			"--sort-by=.lastTimestamp", "-o", "json")
		if err == nil && evtOut != "" {
			var evtList struct {
				Items []struct {
					Type           string `json:"type"`
					Reason         string `json:"reason"`
					Message        string `json:"message"`
					Count          int    `json:"count"`
					LastTimestamp  string `json:"lastTimestamp"`
					InvolvedObject struct {
						Name string `json:"name"`
					} `json:"involvedObject"`
				} `json:"items"`
			}
			if json.Unmarshal([]byte(evtOut), &evtList) == nil {
				podSet := map[string]bool{}
				for _, p := range detail.Pods {
					podSet[p.Name] = true
				}
				for _, e := range evtList.Items {
					if !podSet[e.InvolvedObject.Name] {
						continue
					}
					detail.Events = append(detail.Events, eventInfo{
						Type:    e.Type,
						Reason:  e.Reason,
						Message: e.Message,
						Age:     e.LastTimestamp,
						Count:   e.Count,
					})
				}
				// Keep only last 20 events
				if len(detail.Events) > 20 {
					detail.Events = detail.Events[len(detail.Events)-20:]
				}
			}
		}
	}

	jsonResponse(w, detail)
}

// worstPhase returns the more severe of two pod phases.
func worstPhase(current, incoming string) string {
	priority := map[string]int{
		"Unknown":          0,
		"Running":          1,
		"Succeeded":        2,
		"Pending":          3,
		"Failed":           4,
		"ImagePullBackOff": 5,
		"CrashLoopBackOff": 6,
	}
	cp := priority[current]
	ip := priority[incoming]
	if ip > cp {
		return incoming
	}
	return current
}

// ── Canvas persistence ──────────────────────────────────────────
// The full canvas overlay (user-added nodes and edges that aren't
// from the cluster) is saved to .kindling/canvas.json so it
// survives page navigation and dashboard restarts.

func kindlingDir() string {
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		return filepath.Join(strings.TrimSpace(string(out)), ".kindling")
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, ".kindling")
}

func canvasFilePath() string {
	return filepath.Join(kindlingDir(), "canvas.json")
}

// canvasOverlay stores the user-added nodes and edges that aren't
// derived from cluster DSEs.
type canvasOverlay struct {
	Nodes     []topologyNode              `json:"nodes"`
	Edges     []topologyEdge              `json:"edges"`
	Positions map[string]topologyPosition `json:"positions,omitempty"`
}

func loadCanvas() canvasOverlay {
	data, err := os.ReadFile(canvasFilePath())
	if err != nil {
		return canvasOverlay{}
	}
	var c canvasOverlay
	if err := json.Unmarshal(data, &c); err != nil {
		return canvasOverlay{}
	}
	return c
}

func saveCanvas(c canvasOverlay) {
	p := canvasFilePath()
	os.MkdirAll(filepath.Dir(p), 0755)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		log.Printf("[canvas] marshal error: %v", err)
		return
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		log.Printf("[canvas] write error: %v", err)
	}
}

// addCanvasNode adds or replaces a node in the canvas overlay.
func addCanvasNode(node topologyNode) {
	c := loadCanvas()
	found := false
	for i, n := range c.Nodes {
		if n.Data.Label == node.Data.Label {
			c.Nodes[i] = node
			found = true
			break
		}
	}
	if !found {
		c.Nodes = append(c.Nodes, node)
	}
	saveCanvas(c)
}

// removeCanvasNode removes a node (and its edges) by label.
func removeCanvasNode(label string) {
	c := loadCanvas()
	var nodeID string
	var filteredNodes []topologyNode
	for _, n := range c.Nodes {
		if n.Data.Label != label {
			filteredNodes = append(filteredNodes, n)
		} else {
			nodeID = n.ID
		}
	}
	// Also remove edges connected to this node
	var filteredEdges []topologyEdge
	for _, e := range c.Edges {
		if e.Source != nodeID && e.Target != nodeID {
			filteredEdges = append(filteredEdges, e)
		}
	}
	saveCanvas(canvasOverlay{Nodes: filteredNodes, Edges: filteredEdges})
}

// ── Backward compat aliases ─────────────────────────────────────

func addStagedNode(node topologyNode) { addCanvasNode(node) }
func removeStagedNode(label string)   { removeCanvasNode(label) }

// ── POST /api/topology/canvas ───────────────────────────────────
// Saves the current canvas overlay (user-added nodes + edges).

func handleSaveCanvas(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var payload canvasOverlay
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		actionErr(w, "invalid canvas payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	saveCanvas(payload)
	jsonResponse(w, map[string]bool{"ok": true})
}

// topologyNode is the JSON shape for a node in the topology graph.
type topologyNode struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Position topologyPosition `json:"position"`
	Data     topologyData     `json:"data"`
}

type topologyPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type topologyData struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	DepType     string `json:"depType,omitempty"`
	Version     string `json:"version,omitempty"`
	Port        int    `json:"port,omitempty"`
	EnvVarName  string `json:"envVarName,omitempty"`
	Image       string `json:"image,omitempty"`
	Path        string `json:"path,omitempty"`
	ServicePort int    `json:"servicePort,omitempty"`
	Replicas    int    `json:"replicas,omitempty"`
	DSEName     string `json:"dseName,omitempty"`
	IsNew       bool   `json:"isNew,omitempty"`
	IsDirty     bool   `json:"isDirty,omitempty"`
	Staged      bool   `json:"staged,omitempty"`
	Scaffolded  bool   `json:"scaffolded,omitempty"`
	Language    string `json:"language,omitempty"`
	FromCluster bool   `json:"fromCluster,omitempty"`
}

type topologyEdge struct {
	ID     string                 `json:"id"`
	Type   string                 `json:"type,omitempty"`
	Source string                 `json:"source"`
	Target string                 `json:"target"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

type topologyGraph struct {
	Nodes        []topologyNode `json:"nodes"`
	Edges        []topologyEdge `json:"edges"`
	HasPositions bool           `json:"hasPositions,omitempty"`
}

// svcEnvVar represents a service-to-service env var to inject into a container.
type svcEnvVar struct {
	Name  string
	Value string
}

// ── GET /api/topology ───────────────────────────────────────────
// Reads all DSEs from the cluster and builds a topology graph.

func handleGetTopology(w http.ResponseWriter, r *http.Request) {
	out, err := kubectlJSON("get", "devstagingenvironments", "--all-namespaces", "-o", "json")
	if err != nil {
		// No DSEs yet — return empty graph
		jsonResponse(w, topologyGraph{Nodes: []topologyNode{}, Edges: []topologyEdge{}})
		return
	}

	var dseList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Deployment struct {
					Image    string `json:"image"`
					Port     int    `json:"port"`
					Replicas *int   `json:"replicas"`
					Env      []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"env"`
				} `json:"deployment"`
				Service struct {
					Port int `json:"port"`
				} `json:"service"`
				Dependencies []struct {
					Type       string `json:"type"`
					Version    string `json:"version,omitempty"`
					Port       *int   `json:"port,omitempty"`
					EnvVarName string `json:"envVarName,omitempty"`
				} `json:"dependencies"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(out), &dseList); err != nil {
		jsonResponse(w, topologyGraph{Nodes: []topologyNode{}, Edges: []topologyEdge{}})
		return
	}

	graph := topologyGraph{
		Nodes: []topologyNode{},
		Edges: []topologyEdge{},
	}

	// Track dependency nodes so we can reuse them across DSEs
	depNodes := make(map[string]string) // depType -> nodeID
	svcY := 80.0
	depY := 80.0

	for _, dse := range dseList.Items {
		// Create service node
		replicas := 1
		if dse.Spec.Deployment.Replicas != nil {
			replicas = *dse.Spec.Deployment.Replicas
		}
		svcID := fmt.Sprintf("svc-%s", dse.Metadata.Name)
		graph.Nodes = append(graph.Nodes, topologyNode{
			ID:       svcID,
			Type:     "service",
			Position: topologyPosition{X: 400, Y: svcY},
			Data: topologyData{
				Kind:        "service",
				Label:       dse.Metadata.Name,
				Image:       dse.Spec.Deployment.Image,
				ServicePort: dse.Spec.Service.Port,
				Replicas:    replicas,
				DSEName:     dse.Metadata.Name,
				FromCluster: true,
			},
		})
		svcY += 220

		// Create dependency nodes and edges
		for _, dep := range dse.Spec.Dependencies {
			depID, exists := depNodes[dep.Type]
			if !exists {
				depID = fmt.Sprintf("dep-%s", dep.Type)
				port := 0
				if dep.Port != nil {
					port = *dep.Port
				}
				graph.Nodes = append(graph.Nodes, topologyNode{
					ID:       depID,
					Type:     "dependency",
					Position: topologyPosition{X: 800, Y: depY},
					Data: topologyData{
						Kind:        "dependency",
						Label:       dep.Type,
						DepType:     dep.Type,
						Version:     dep.Version,
						Port:        port,
						EnvVarName:  dep.EnvVarName,
						FromCluster: true,
					},
				})
				depNodes[dep.Type] = depID
				depY += 160
			}

			// Determine the env var label for this dependency edge
			depLabel := dep.EnvVarName
			if depLabel == "" {
				// Fall back to well-known auto-injected env var names
				depAutoEnv := map[string]string{
					"postgres": "DATABASE_URL", "redis": "REDIS_URL",
					"mysql": "DATABASE_URL", "mongodb": "MONGO_URL",
					"rabbitmq": "AMQP_URL", "minio": "S3_ENDPOINT",
					"elasticsearch": "ELASTICSEARCH_URL", "kafka": "KAFKA_BROKER_URL",
					"nats": "NATS_URL", "memcached": "MEMCACHED_URL",
					"cassandra": "CASSANDRA_URL", "consul": "CONSUL_URL",
					"vault": "VAULT_ADDR", "influxdb": "INFLUXDB_URL",
					"jaeger": "JAEGER_URL",
				}
				depLabel = depAutoEnv[dep.Type]
			}
			graph.Edges = append(graph.Edges, topologyEdge{
				ID:     fmt.Sprintf("e-%s-%s", svcID, depID),
				Type:   "connection",
				Source: svcID,
				Target: depID,
				Data: map[string]interface{}{
					"_label": depLabel,
				},
			})
		}
	}

	// Reconstruct service-to-service edges from deployed env vars.
	// Pattern: if DSE "A" has env var *_URL=http://<other-svc-name>:<port>,
	// and "svc-<other-svc-name>" exists, create a service-edge.
	svcNodeIDs := make(map[string]bool)    // set of service node IDs
	svcNameToID := make(map[string]string) // svc name → node ID
	for _, n := range graph.Nodes {
		if n.Data.Kind == "service" {
			svcNodeIDs[n.ID] = true
			svcNameToID[n.Data.DSEName] = n.ID
		}
	}
	for _, dse := range dseList.Items {
		srcID := "svc-" + dse.Metadata.Name
		for _, ev := range dse.Spec.Deployment.Env {
			if !strings.HasSuffix(ev.Name, "_URL") {
				continue
			}
			// Try to match the value against another service: http://<name>:<port>
			for targetName, targetID := range svcNameToID {
				if targetID == srcID {
					continue
				}
				if strings.Contains(ev.Value, "://"+targetName+":") {
					edgeID := fmt.Sprintf("e-%s-%s", srcID, targetID)
					graph.Edges = append(graph.Edges, topologyEdge{
						ID:     edgeID,
						Type:   "service-edge",
						Source: srcID,
						Target: targetID,
						Data: map[string]interface{}{
							"_label":    ev.Name,
							"_envVar":   ev.Name,
							"_envValue": ev.Value,
						},
					})
					break
				}
			}
		}
	}

	// Merge in any persisted canvas nodes/edges that aren't already in the cluster
	canvas := loadCanvas()
	clusterNodeIDs := make(map[string]bool)
	clusterDepTypes := make(map[string]bool) // track dep types already from cluster
	for _, n := range graph.Nodes {
		clusterNodeIDs[n.ID] = true
		if n.Data.Kind == "dependency" && n.Data.DepType != "" {
			clusterDepTypes[n.Data.DepType] = true
		}
	}
	// Track which canvas node IDs we actually merge in
	// Map canvas dep IDs to cluster dep IDs for edge remapping
	canvasToCluster := make(map[string]string) // old canvas ID → cluster ID
	mergedNodeIDs := make(map[string]bool)
	for _, cn := range canvas.Nodes {
		if clusterNodeIDs[cn.ID] {
			continue // exact ID already in cluster
		}
		// Skip canvas dependency nodes whose depType already exists from cluster
		if cn.Data.Kind == "dependency" && cn.Data.DepType != "" && clusterDepTypes[cn.Data.DepType] {
			canvasToCluster[cn.ID] = "dep-" + cn.Data.DepType
			continue
		}
		graph.Nodes = append(graph.Nodes, cn)
		mergedNodeIDs[cn.ID] = true
	}
	// Merge canvas edges whose source and target both exist (after remapping)
	allNodeIDs := make(map[string]bool)
	for id := range clusterNodeIDs {
		allNodeIDs[id] = true
	}
	for id := range mergedNodeIDs {
		allNodeIDs[id] = true
	}
	for _, ce := range canvas.Edges {
		// Remap canvas dep IDs to cluster IDs
		src := ce.Source
		tgt := ce.Target
		if mapped, ok := canvasToCluster[src]; ok {
			src = mapped
		}
		if mapped, ok := canvasToCluster[tgt]; ok {
			tgt = mapped
		}
		ce.Source = src
		ce.Target = tgt
		if allNodeIDs[ce.Source] && allNodeIDs[ce.Target] {
			// Skip if this edge duplicates one already in the graph
			dup := false
			for _, existing := range graph.Edges {
				if existing.Source == ce.Source && existing.Target == ce.Target {
					dup = true
					break
				}
			}
			if !dup {
				graph.Edges = append(graph.Edges, ce)
			}
		}
	}

	// Apply saved positions from canvas overlay to all nodes
	if len(canvas.Positions) > 0 {
		for i, n := range graph.Nodes {
			if pos, ok := canvas.Positions[n.ID]; ok {
				graph.Nodes[i].Position = pos
			}
		}
		graph.HasPositions = true
	}

	jsonResponse(w, graph)
}

// ── POST /api/topology/deploy ───────────────────────────────────
// Accepts a topology graph and converts it to DSE YAML(s), then applies.

func handleDeployTopology(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var graph topologyGraph
	if err := json.NewDecoder(r.Body).Decode(&graph); err != nil {
		actionErr(w, "invalid topology graph: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Build a map of node IDs to nodes
	nodeMap := make(map[string]topologyNode)
	for _, n := range graph.Nodes {
		nodeMap[n.ID] = n
	}

	// Find service nodes and their connected dependencies
	type serviceBundle struct {
		service      topologyNode
		dependencies []topologyNode
		svcEnvVars   []svcEnvVar // env vars from service-to-service edges
	}
	bundles := []serviceBundle{}

	for _, n := range graph.Nodes {
		if n.Data.Kind != "service" {
			continue
		}
		bundle := serviceBundle{service: n}
		// Find all edges where this service is the source
		for _, e := range graph.Edges {
			var depID string
			if e.Source == n.ID {
				depID = e.Target
			} else if e.Target == n.ID {
				depID = e.Source
			} else {
				continue
			}
			if dep, ok := nodeMap[depID]; ok && dep.Data.Kind == "dependency" {
				bundle.dependencies = append(bundle.dependencies, dep)
			}
			// Service-to-service: if this service is the SOURCE and target is another service
			if e.Source == n.ID {
				if target, ok := nodeMap[e.Target]; ok && target.Data.Kind == "service" {
					envVar := ""
					envValue := ""
					if data := e.Data; data != nil {
						if v, ok := data["_envVar"].(string); ok {
							envVar = v
						}
						if v, ok := data["_envValue"].(string); ok {
							envValue = v
						}
					}
					if envVar == "" {
						// Derive from target name
						tName := strings.ToLower(strings.ReplaceAll(target.Data.Label, " ", "-"))
						envVar = strings.ToUpper(strings.ReplaceAll(tName, "-", "_")) + "_URL"
						port := target.Data.ServicePort
						if port == 0 {
							port = 3000
						}
						envValue = fmt.Sprintf("http://%s:%d", tName, port)
					}
					bundle.svcEnvVars = append(bundle.svcEnvVars, svcEnvVar{Name: envVar, Value: envValue})
				}
			}
		}
		bundles = append(bundles, bundle)
	}

	if len(bundles) == 0 {
		actionErr(w, "no service nodes found in topology", http.StatusBadRequest)
		return
	}

	// Log what we received for debugging
	for _, n := range graph.Nodes {
		log.Printf("[deploy] node id=%s kind=%s label=%s isNew=%v isDirty=%v", n.ID, n.Data.Kind, n.Data.Label, n.Data.IsNew, n.Data.IsDirty)
	}
	for _, e := range graph.Edges {
		log.Printf("[deploy] edge %s -> %s", e.Source, e.Target)
	}

	// Detect orphan dependency nodes (not connected to any service)
	connectedDeps := make(map[string]bool)
	for _, b := range bundles {
		for _, d := range b.dependencies {
			connectedDeps[d.ID] = true
		}
	}
	var orphanNames []string
	for _, n := range graph.Nodes {
		if n.Data.Kind == "dependency" && !connectedDeps[n.ID] {
			orphanNames = append(orphanNames, n.Data.Label)
		}
	}

	// If ONLY orphan deps and no other changes, tell the user to connect them
	if len(orphanNames) > 0 {
		// Check if there are any actual deployable changes
		hasDeployable := false
		for _, b := range bundles {
			if b.service.Data.IsNew || b.service.Data.IsDirty {
				hasDeployable = true
				break
			}
			if len(b.svcEnvVars) > 0 && svcEnvVarsChanged(b.service, b.svcEnvVars) {
				hasDeployable = true
				break
			}
			if depsChanged(b.service, b.dependencies) {
				hasDeployable = true
				break
			}
			for _, d := range b.dependencies {
				if d.Data.IsNew || d.Data.IsDirty {
					hasDeployable = true
					break
				}
			}
			if hasDeployable {
				break
			}
		}
		if !hasDeployable {
			actionErr(w, fmt.Sprintf("%s not connected to any service — draw an edge from a service to the dependency, then deploy again", strings.Join(orphanNames, ", ")), http.StatusBadRequest)
			return
		}
	}

	// Only deploy bundles that contain at least one new or dirty node,
	// or that have new service-to-service env vars or new dependencies
	// not yet in the cluster.
	var deployBundles []serviceBundle
	for _, b := range bundles {
		if b.service.Data.IsNew || b.service.Data.IsDirty {
			deployBundles = append(deployBundles, b)
			continue
		}
		depChanged := false
		for _, d := range b.dependencies {
			if d.Data.IsNew || d.Data.IsDirty {
				depChanged = true
				break
			}
		}
		if depChanged {
			deployBundles = append(deployBundles, b)
			continue
		}
		// Check if service-to-service env vars differ from what's deployed
		if len(b.svcEnvVars) > 0 && svcEnvVarsChanged(b.service, b.svcEnvVars) {
			deployBundles = append(deployBundles, b)
			continue
		}
		// Check if dependency connections differ from what's deployed
		if depsChanged(b.service, b.dependencies) {
			deployBundles = append(deployBundles, b)
			continue
		}
	}

	if len(deployBundles) == 0 {
		actionOK(w, "Nothing to deploy — no changes detected")
		return
	}

	// Check for staged services that aren't ready
	var stagedNames []string
	var readyBundles []serviceBundle
	for _, b := range deployBundles {
		if b.service.Data.Staged {
			// Staged service — check if it has a path with source files
			if b.service.Data.Path == "" {
				stagedNames = append(stagedNames, b.service.Data.Label)
				continue
			}
			// Path exists — check if the directory is actually populated
			if _, err := os.Stat(b.service.Data.Path); os.IsNotExist(err) {
				stagedNames = append(stagedNames, b.service.Data.Label)
				continue
			}
		}
		readyBundles = append(readyBundles, b)
	}

	if len(readyBundles) == 0 && len(stagedNames) > 0 {
		actionErr(w, fmt.Sprintf("%s still staged — write your code in the scaffolded directory, then deploy when ready", strings.Join(stagedNames, ", ")), http.StatusBadRequest)
		return
	}

	// Build images for new services that have a source path with a Dockerfile.
	// Track the actual loaded image tags so the DSE uses the right reference.
	builtImages := make(map[string]string) // service label -> loaded image tag
	var outputs []string
	for _, b := range readyBundles {
		if b.service.Data.IsNew && b.service.Data.Path != "" {
			dfPath := filepath.Join(b.service.Data.Path, "Dockerfile")
			if _, err := os.Stat(dfPath); err == nil {
				safeName := strings.ToLower(strings.ReplaceAll(b.service.Data.Label, " ", "-"))
				log.Printf("[deploy] building image for %s from %s", safeName, b.service.Data.Path)
				loadMsgs, err := core.BuildAndLoad(core.LoadConfig{
					ClusterName: clusterName,
					Service:     safeName,
					Context:     b.service.Data.Path,
					NoDeploy:    true, // we'll apply the DSE ourselves below
				})
				if err != nil {
					actionErr(w, fmt.Sprintf("image build failed for %s: %v", safeName, err), http.StatusInternalServerError)
					return
				}
				// Extract the image tag from the "✓ Image built: <tag>" message
				for _, m := range loadMsgs {
					log.Printf("[deploy] %s: %s", safeName, m)
					if strings.HasPrefix(m, "✓ Image built: ") {
						builtImages[b.service.Data.Label] = strings.TrimPrefix(m, "✓ Image built: ")
					}
				}
				outputs = append(outputs, fmt.Sprintf("✓ %s image built & loaded", safeName))
			}
		}
	}

	// Generate and apply DSE YAML for each changed service bundle
	for i, b := range readyBundles {
		// Override image with the loaded tag if we just built it
		if tag, ok := builtImages[b.service.Data.Label]; ok {
			readyBundles[i].service.Data.Image = tag
			b = readyBundles[i]
		}
		yaml := buildDSEYAML(b.service, b.dependencies, b.svcEnvVars)
		log.Printf("[deploy] applying DSE for %s:\n%s", b.service.Data.Label, yaml)
		out, err := core.KubectlApplyStdin(clusterName, yaml)
		if err != nil {
			actionErr(w, "deploy failed for "+b.service.Data.Label+": "+out, http.StatusInternalServerError)
			return
		}
		// Remove from staged storage once successfully deployed
		removeStagedNode(b.service.Data.Label)
		outputs = append(outputs, fmt.Sprintf("✓ %s deployed", b.service.Data.Label))
	}

	// Append staged warnings
	for _, name := range stagedNames {
		outputs = append(outputs, fmt.Sprintf("⏳ %s staged — develop your code, then deploy again", name))
	}

	// Append orphan warnings if any
	for _, name := range orphanNames {
		outputs = append(outputs, fmt.Sprintf("⚠ %s not connected — draw an edge to include it", name))
	}

	actionOK(w, strings.Join(outputs, "\n"))
}

// existingDSEIngressYAML fetches the ingress spec from an existing DSE in the cluster.
// Returns the YAML lines to append (empty string if no ingress or DSE not found).
func existingDSEIngressYAML(dseName string) string {
	if dseName == "" {
		return ""
	}
	out, err := kubectlJSON("get", "devstagingenvironment", dseName, "-n", "default", "-o", "json")
	if err != nil {
		return ""
	}
	var dse struct {
		Spec struct {
			Ingress *struct {
				Enabled          bool              `json:"enabled"`
				Host             string            `json:"host,omitempty"`
				Path             string            `json:"path,omitempty"`
				PathType         string            `json:"pathType,omitempty"`
				IngressClassName string            `json:"ingressClassName,omitempty"`
				Annotations      map[string]string `json:"annotations,omitempty"`
			} `json:"ingress,omitempty"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &dse); err != nil || dse.Spec.Ingress == nil {
		return ""
	}
	ing := dse.Spec.Ingress
	var sb strings.Builder
	sb.WriteString("  ingress:\n")
	sb.WriteString(fmt.Sprintf("    enabled: %v\n", ing.Enabled))
	if ing.Host != "" {
		sb.WriteString(fmt.Sprintf("    host: %s\n", ing.Host))
	}
	if ing.Path != "" {
		sb.WriteString(fmt.Sprintf("    path: %s\n", ing.Path))
	}
	if ing.PathType != "" {
		sb.WriteString(fmt.Sprintf("    pathType: %s\n", ing.PathType))
	}
	if ing.IngressClassName != "" {
		sb.WriteString(fmt.Sprintf("    ingressClassName: %s\n", ing.IngressClassName))
	}
	if len(ing.Annotations) > 0 {
		sb.WriteString("    annotations:\n")
		for k, v := range ing.Annotations {
			sb.WriteString(fmt.Sprintf("      %s: \"%s\"\n", k, v))
		}
	}
	return sb.String()
}

// svcEnvVarsChanged checks whether the given service-to-service env vars
// differ from what is already deployed in the cluster DSE.
func svcEnvVarsChanged(svc topologyNode, envVars []svcEnvVar) bool {
	dseName := svc.Data.DSEName
	if dseName == "" {
		dseName = strings.ToLower(strings.ReplaceAll(svc.Data.Label, " ", "-"))
	}
	out, err := kubectlJSON("get", "devstagingenvironment", dseName, "-n", "default", "-o", "json")
	if err != nil {
		// DSE doesn't exist — env vars are new
		return true
	}
	var dse struct {
		Spec struct {
			Deployment struct {
				Env []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"deployment"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &dse); err != nil {
		return true
	}
	// Build a map of existing env vars
	existing := make(map[string]string)
	for _, e := range dse.Spec.Deployment.Env {
		existing[e.Name] = e.Value
	}
	// Check if any desired env var is missing or different
	for _, ev := range envVars {
		if val, ok := existing[ev.Name]; !ok || val != ev.Value {
			return true
		}
	}
	return false
}

// depsChanged checks whether the given bundle's dependency list differs from
// what is already deployed in the cluster DSE. Returns true when a new
// dependency type has been connected that isn't in the deployed spec.
func depsChanged(svc topologyNode, deps []topologyNode) bool {
	if len(deps) == 0 {
		return false
	}
	dseName := svc.Data.DSEName
	if dseName == "" {
		dseName = strings.ToLower(strings.ReplaceAll(svc.Data.Label, " ", "-"))
	}
	out, err := kubectlJSON("get", "devstagingenvironment", dseName, "-n", "default", "-o", "json")
	if err != nil {
		// DSE doesn't exist yet — deps are new
		return true
	}
	var dse struct {
		Spec struct {
			Dependencies []struct {
				Type string `json:"type"`
			} `json:"dependencies"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &dse); err != nil {
		return true
	}
	existing := make(map[string]bool)
	for _, d := range dse.Spec.Dependencies {
		existing[d.Type] = true
	}
	for _, d := range deps {
		if d.Data.DepType != "" && !existing[d.Data.DepType] {
			return true
		}
	}
	return false
}

// mergeEnvVars merges service-to-service env vars with any existing env vars
// from the deployed DSE, so user-set env vars are preserved.
func mergeEnvVars(dseName string, svcVars []svcEnvVar) []svcEnvVar {
	// Fetch existing env vars from the cluster
	out, err := kubectlJSON("get", "devstagingenvironment", dseName, "-n", "default", "-o", "json")
	if err != nil {
		return svcVars // new DSE, just use svc vars
	}
	var dse struct {
		Spec struct {
			Deployment struct {
				Env []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"deployment"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &dse); err != nil {
		return svcVars
	}

	// Start with existing vars, then override/add svc-to-svc vars
	newVarMap := make(map[string]string)
	for _, ev := range svcVars {
		newVarMap[ev.Name] = ev.Value
	}

	var result []svcEnvVar
	seen := make(map[string]bool)

	// Keep existing vars, updating any that are overridden by svc-to-svc
	for _, e := range dse.Spec.Deployment.Env {
		if val, ok := newVarMap[e.Name]; ok {
			result = append(result, svcEnvVar{Name: e.Name, Value: val})
		} else {
			result = append(result, svcEnvVar{Name: e.Name, Value: e.Value})
		}
		seen[e.Name] = true
	}

	// Add any new svc-to-svc vars not already present
	for _, ev := range svcVars {
		if !seen[ev.Name] {
			result = append(result, ev)
		}
	}

	return result
}

// buildDSEYAML generates a DevStagingEnvironment YAML manifest from a
// service node and its connected dependency nodes.
func buildDSEYAML(svc topologyNode, deps []topologyNode, envVars []svcEnvVar) string {
	name := svc.Data.DSEName
	if name == "" {
		name = strings.ToLower(strings.ReplaceAll(svc.Data.Label, " ", "-"))
	}
	image := svc.Data.Image
	if image == "" {
		image = fmt.Sprintf("localhost:5001/%s:latest", name)
	}
	port := svc.Data.ServicePort
	if port == 0 {
		port = 3000
	}
	replicas := svc.Data.Replicas
	if replicas == 0 {
		replicas = 1
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: "%s"
  namespace: default
spec:
  deployment:
    image: "%s"
    port: %d
    replicas: %d
`, name, image, port, replicas))

	// Service-to-service env vars (injected into this service's container)
	// Merge with any existing env vars from the deployed DSE.
	allEnvVars := mergeEnvVars(name, envVars)
	if len(allEnvVars) > 0 {
		sb.WriteString("    env:\n")
		for _, ev := range allEnvVars {
			sb.WriteString(fmt.Sprintf("    - name: %s\n      value: \"%s\"\n", ev.Name, ev.Value))
		}
	}

	sb.WriteString(fmt.Sprintf(`  service:
    port: %d
`, port))

	if len(deps) > 0 {
		sb.WriteString("  dependencies:\n")
		for _, dep := range deps {
			sb.WriteString(fmt.Sprintf("    - type: %s\n", dep.Data.DepType))
			if dep.Data.Version != "" {
				sb.WriteString(fmt.Sprintf("      version: \"%s\"\n", dep.Data.Version))
			}
			if dep.Data.Port > 0 {
				sb.WriteString(fmt.Sprintf("      port: %d\n", dep.Data.Port))
			}
			if dep.Data.EnvVarName != "" {
				sb.WriteString(fmt.Sprintf("      envVarName: %s\n", dep.Data.EnvVarName))
			}
		}
	}

	// Preserve existing ingress config from the live DSE (if any)
	if ingressYAML := existingDSEIngressYAML(svc.Data.DSEName); ingressYAML != "" {
		sb.WriteString(ingressYAML)
	}

	return sb.String()
}

// ── GET /api/topology/workspace ─────────────────────────────────
// Returns the detected workspace root (git root or cwd) and existing
// service directories so the frontend can auto-suggest scaffold paths.

func handleWorkspaceInfo(w http.ResponseWriter, r *http.Request) {
	// Try git root first
	root := ""
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(out))
	}
	if root == "" {
		root, _ = os.Getwd()
	}

	// Discover existing service-like directories (have Dockerfile or go.mod/package.json)
	var services []string
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" || e.Name() == "vendor" {
			continue
		}
		sub := filepath.Join(root, e.Name())
		if hasServiceMarker(sub) {
			services = append(services, e.Name())
		}
	}
	// Also check services/ subdirectory (monorepo pattern)
	servicesDir := filepath.Join(root, "services")
	if info, err := os.Stat(servicesDir); err == nil && info.IsDir() {
		entries, _ = os.ReadDir(servicesDir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(servicesDir, e.Name())
			if hasServiceMarker(sub) {
				services = append(services, "services/"+e.Name())
			}
		}
	}

	jsonResponse(w, map[string]interface{}{
		"root":     root,
		"services": services,
	})
}

// hasServiceMarker returns true if a directory looks like a service (has build/dep markers).
func hasServiceMarker(dir string) bool {
	markers := []string{"Dockerfile", "package.json", "go.mod", "requirements.txt", "Cargo.toml", "pom.xml"}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

// ── POST /api/topology/scaffold ─────────────────────────────────
// Creates a new service directory with a language-appropriate scaffold.
// Body: { "name": "my-api", "path": "/abs/path", "port": 3000, "language": "node" }
// Supported languages: node (default), go, python

func handleScaffoldService(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var body struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Port     int    `json:"port"`
		Language string `json:"language"`
		Deps     []struct {
			EnvVar string `json:"envVar"`
			Value  string `json:"value"`
		} `json:"deps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Path == "" {
		actionErr(w, "name and path are required", http.StatusBadRequest)
		return
	}
	if body.Port == 0 {
		body.Port = 3000
	}
	if body.Language == "" {
		body.Language = "node"
	}

	absPath, err := filepath.Abs(body.Path)
	if err != nil {
		actionErr(w, "invalid path: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create directory
	if err := os.MkdirAll(absPath, 0755); err != nil {
		actionErr(w, "cannot create directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Convert deps to simple string pairs
	envDeps := make([]scaffoldDep, len(body.Deps))
	for i, d := range body.Deps {
		envDeps[i] = scaffoldDep{EnvVar: d.EnvVar, Value: d.Value}
	}

	var scaffoldErr error
	switch body.Language {
	case "go":
		scaffoldErr = scaffoldGo(absPath, body.Name, body.Port, envDeps)
	case "python":
		scaffoldErr = scaffoldPython(absPath, body.Name, body.Port, envDeps)
	default:
		scaffoldErr = scaffoldNode(absPath, body.Name, body.Port, envDeps)
	}

	if scaffoldErr != nil {
		actionErr(w, "scaffold failed: "+scaffoldErr.Error(), http.StatusInternalServerError)
		return
	}

	// Persist as a staged node so it survives page navigation
	addStagedNode(topologyNode{
		ID:   fmt.Sprintf("svc-staged-%s", body.Name),
		Type: "service",
		Data: topologyData{
			Kind:        "service",
			Label:       body.Name,
			Image:       fmt.Sprintf("localhost:5001/%s:latest", body.Name),
			Path:        absPath,
			ServicePort: body.Port,
			Replicas:    1,
			IsNew:       true,
			Staged:      true,
			Language:    body.Language,
		},
	})

	jsonResponse(w, map[string]interface{}{
		"ok":       true,
		"message":  fmt.Sprintf("Service '%s' scaffolded at %s", body.Name, absPath),
		"path":     absPath,
		"language": body.Language,
	})
}

// ── Scaffold templates ──────────────────────────────────────────

type scaffoldDep struct {
	EnvVar string
	Value  string
}

func scaffoldNode(dir, name string, port int, deps []scaffoldDep) error {
	// Dockerfile (Kaniko-compatible)
	dockerfile := fmt.Sprintf(`FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
ENV npm_config_cache=/tmp/.npm
RUN npm ci --only=production 2>/dev/null || true
COPY . .
EXPOSE %d
CMD ["node", "index.js"]
`, port)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return err
	}

	pkgJSON := fmt.Sprintf(`{
  "name": "%s",
  "version": "0.1.0",
  "private": true,
  "main": "index.js",
  "scripts": {
    "start": "node index.js",
    "dev": "node --watch index.js"
  }
}
`, name)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		return err
	}

	// Build env var block for index.js
	var envBlock string
	for _, d := range deps {
		envBlock += fmt.Sprintf("const %s = process.env.%s || '%s';\n", strings.ReplaceAll(d.EnvVar, "-", "_"), d.EnvVar, d.Value)
	}
	if envBlock != "" {
		envBlock = "\n// Connection URLs (from topology edges)\n" + envBlock + "\n"
	}

	indexJS := fmt.Sprintf(`const http = require('http');
%s
const PORT = process.env.PORT || %d;

const server = http.createServer((req, res) => {
  if (req.url === '/healthz') {
    res.writeHead(200);
    res.end('ok');
    return;
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ service: '%s', status: 'running' }));
});

server.listen(PORT, () => {
  console.log('%s listening on port ' + PORT);
});
`, envBlock, port, name, name)
	return os.WriteFile(filepath.Join(dir, "index.js"), []byte(indexJS), 0644)
}

func scaffoldGo(dir, name string, port int, deps []scaffoldDep) error {
	// Dockerfile (Kaniko-compatible, no BuildKit)
	dockerfile := fmt.Sprintf(`FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -o /app .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app /app
EXPOSE %d
CMD ["/app"]
`, port)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return err
	}

	goMod := fmt.Sprintf(`module %s

go 1.23
`, name)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		return err
	}

	// Build env var block
	var envBlock string
	for _, d := range deps {
		varName := strings.ReplaceAll(strings.ToLower(d.EnvVar), "-", "_")
		envBlock += fmt.Sprintf("\t%s := os.Getenv(\"%s\") // %s\n", varName, d.EnvVar, d.Value)
	}
	if envBlock != "" {
		envBlock = "\n\t// Connection URLs (from topology edges)\n" + envBlock + "\t_ = 0 // silence unused warnings during dev\n\n"
	}

	mainGo := fmt.Sprintf(`package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "%d"
	}
%s
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"service": "%s",
			"status":  "running",
		})
	})

	log.Printf("%s listening on port %%%%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
`, port, envBlock, name, name)
	return os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0644)
}

func scaffoldPython(dir, name string, port int, deps []scaffoldDep) error {
	// Dockerfile (Kaniko-compatible)
	dockerfile := fmt.Sprintf(`FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt 2>/dev/null || true
COPY . .
EXPOSE %d
CMD ["python", "app.py"]
`, port)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return err
	}

	requirements := "flask>=3.0\ngunicorn>=21.2\n"
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte(requirements), 0644); err != nil {
		return err
	}

	// Build env var block
	var envBlock string
	for _, d := range deps {
		varName := strings.ReplaceAll(strings.ToUpper(d.EnvVar), "-", "_")
		envBlock += fmt.Sprintf("%s = os.environ.get(\"%s\", \"%s\")\n", varName, d.EnvVar, d.Value)
	}
	if envBlock != "" {
		envBlock = "\n# Connection URLs (from topology edges)\n" + envBlock + "\n"
	}

	appPy := fmt.Sprintf(`import os
from flask import Flask, jsonify

app = Flask(__name__)
PORT = int(os.environ.get("PORT", %d))
%s

@app.route("/healthz")
def healthz():
    return "ok"


@app.route("/")
def index():
    return jsonify(service="%s", status="running")


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=PORT)
`, port, envBlock, name)
	return os.WriteFile(filepath.Join(dir, "app.py"), []byte(appPy), 0644)
}

// ── GET /api/topology/check-path ────────────────────────────────
// Checks if a directory exists and whether it has a Dockerfile.
// Query: ?path=/abs/path

func handleCheckPath(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		actionErr(w, "path query parameter is required", http.StatusBadRequest)
		return
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		jsonResponse(w, map[string]interface{}{
			"exists":         false,
			"has_dockerfile": false,
			"language":       "",
		})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		jsonResponse(w, map[string]interface{}{
			"exists":         false,
			"has_dockerfile": false,
			"language":       "",
		})
		return
	}

	hasDockerfile := false
	if _, err := os.Stat(filepath.Join(absPath, "Dockerfile")); err == nil {
		hasDockerfile = true
	}

	lang := detectLanguageFromSource(absPath)

	jsonResponse(w, map[string]interface{}{
		"exists":         true,
		"has_dockerfile": hasDockerfile,
		"language":       lang,
	})
}

// ── POST /api/topology/edge/remove ──────────────────────────────
// Removes an env var or dependency from a deployed DSE when an edge
// is deleted in the topology editor. This keeps the cluster in sync
// with the canvas state without requiring a full redeploy.
//
// Body: { "dseName": "my-api", "envVar": "RUNNER_URL" }         — svc-to-svc
//   or: { "dseName": "my-api", "depType": "mongodb" }           — svc-to-dep

func handleRemoveEdge(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var body struct {
		DSEName string `json:"dseName"`
		EnvVar  string `json:"envVar,omitempty"`  // for svc-to-svc edges
		DepType string `json:"depType,omitempty"` // for svc-to-dep edges
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.DSEName == "" {
		actionErr(w, "dseName is required", http.StatusBadRequest)
		return
	}

	// Fetch the current DSE
	out, err := kubectlJSON("get", "devstagingenvironment", body.DSEName, "-n", "default", "-o", "json")
	if err != nil {
		// DSE doesn't exist — nothing to clean up
		actionOK(w, "no deployed DSE to update")
		return
	}

	var dse struct {
		Spec struct {
			Deployment struct {
				Env []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"deployment"`
			Dependencies []struct {
				Type string `json:"type"`
			} `json:"dependencies"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &dse); err != nil {
		actionErr(w, "failed to parse DSE", http.StatusInternalServerError)
		return
	}

	var patches []string

	// Remove env var (svc-to-svc)
	if body.EnvVar != "" {
		for i, e := range dse.Spec.Deployment.Env {
			if e.Name == body.EnvVar {
				patches = append(patches, fmt.Sprintf(`{"op":"remove","path":"/spec/deployment/env/%d"}`, i))
				log.Printf("[edge-remove] removing env var %s from DSE %s", body.EnvVar, body.DSEName)
				break
			}
		}
	}

	// Remove dependency (svc-to-dep)
	if body.DepType != "" {
		for i, d := range dse.Spec.Dependencies {
			if d.Type == body.DepType {
				patches = append(patches, fmt.Sprintf(`{"op":"remove","path":"/spec/dependencies/%d"}`, i))
				log.Printf("[edge-remove] removing dependency %s from DSE %s", body.DepType, body.DSEName)
				break
			}
		}
	}

	if len(patches) == 0 {
		actionOK(w, "no changes needed")
		return
	}

	patchJSON := "[" + strings.Join(patches, ",") + "]"
	_, err = core.Kubectl(clusterName, "patch", "devstagingenvironment", body.DSEName,
		"-n", "default", "--type=json", "-p", patchJSON)
	if err != nil {
		log.Printf("[edge-remove] patch failed for DSE %s: %v", body.DSEName, err)
		actionErr(w, fmt.Sprintf("failed to patch DSE: %v", err), http.StatusInternalServerError)
		return
	}

	var desc string
	if body.EnvVar != "" {
		desc = fmt.Sprintf("Removed %s from %s", body.EnvVar, body.DSEName)
	} else {
		desc = fmt.Sprintf("Removed %s dependency from %s", body.DepType, body.DSEName)
	}
	actionOK(w, desc)
}

// ── POST /api/topology/cleanup ──────────────────────────────────
// Cleans up resources when a service is deleted from the topology:
// 1. Removes the scaffolded directory on disk
// 2. Strips build/deploy steps from the CI workflow
// 3. Removes env vars referencing the deleted service from other workflow steps
//
// Body: { "name": "my-api", "path": "/abs/path/services/my-api",
//         "referencedBy": ["INVENTORY_URL"] }

func handleCleanupService(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}

	var body struct {
		Name         string   `json:"name"`
		Path         string   `json:"path"`
		DSEName      string   `json:"dseName"`
		ReferencedBy []string `json:"referencedBy"` // env var names to strip from workflow
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		actionErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		actionErr(w, "name is required", http.StatusBadRequest)
		return
	}

	var cleaned []string

	// 1. Remove scaffolded directory
	if body.Path != "" {
		absPath, err := filepath.Abs(body.Path)
		if err == nil {
			if info, err := os.Stat(absPath); err == nil && info.IsDir() {
				if err := os.RemoveAll(absPath); err != nil {
					log.Printf("[cleanup] failed to remove directory %s: %v", absPath, err)
				} else {
					cleaned = append(cleaned, fmt.Sprintf("Removed directory %s", absPath))
					log.Printf("[cleanup] removed directory %s", absPath)
				}
			}
		}
	}

	// 2. Delete the DSE from the cluster if it exists
	if body.DSEName != "" {
		out, err := core.Kubectl(clusterName, "delete", "devstagingenvironment", body.DSEName, "-n", "default", "--ignore-not-found")
		if err == nil && !strings.Contains(out, "not found") {
			cleaned = append(cleaned, fmt.Sprintf("Deleted DSE %s from cluster", body.DSEName))
			log.Printf("[cleanup] deleted DSE %s", body.DSEName)
		}
	}

	// 3. Strip build/deploy steps from CI workflow
	workflowCleaned := cleanupWorkflow(body.Name, body.ReferencedBy)
	if workflowCleaned != "" {
		cleaned = append(cleaned, workflowCleaned)
	}

	// 4. Remove from staged storage
	removeStagedNode(body.Name)

	if len(cleaned) == 0 {
		actionOK(w, fmt.Sprintf("Service '%s' removed from topology (no on-disk cleanup needed)", body.Name))
		return
	}

	actionOK(w, strings.Join(cleaned, "\n"))
}

// cleanupWorkflow finds the dev-deploy.yml workflow and strips steps referencing
// the given service name, plus any env vars from referencedBy.
func cleanupWorkflow(serviceName string, referencedEnvVars []string) string {
	// Find the workflow file
	root := ""
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(out))
	}
	if root == "" {
		root, _ = os.Getwd()
	}

	wfPath := filepath.Join(root, ".github", "workflows", "dev-deploy.yml")
	data, err := os.ReadFile(wfPath)
	if err != nil {
		log.Printf("[cleanup] no workflow found at %s", wfPath)
		return ""
	}

	original := string(data)
	result := original
	changes := 0

	// Remove build and deploy steps that reference this service.
	// Steps look like:
	//   - name: Build <service> image
	//     ...uses kindling-build...
	//     with:
	//       name: <svc-name>
	//       context: "...<service>..."
	//
	// We match step blocks (lines starting with "      - name:" to the next such line)
	// and remove those whose name/context/with.name references the service.
	lines := strings.Split(result, "\n")
	var filtered []string
	safeName := strings.ToLower(strings.ReplaceAll(serviceName, " ", "-"))
	skipBlock := false
	i := 0

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Detect start of a workflow step (leading whitespace + "- name:")
		if strings.HasPrefix(trimmed, "- name:") && strings.Contains(line, "      - name:") {
			// Gather the full step block (until next "      - name:" or section heading)
			block := []string{line}
			j := i + 1
			for j < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[j])
				// Next step or section starts
				if strings.HasPrefix(nextTrimmed, "- name:") && strings.Contains(lines[j], "      - name:") {
					break
				}
				// Top-level key (no leading space, like "jobs:" or end of steps)
				if len(lines[j]) > 0 && lines[j][0] != ' ' && lines[j][0] != '#' {
					break
				}
				block = append(block, lines[j])
				j++
			}

			// Check if this block references our service
			blockText := strings.Join(block, "\n")
			blockLower := strings.ToLower(blockText)
			matches := strings.Contains(blockLower, safeName)

			if matches {
				// Skip this block
				i = j
				changes++
				continue
			}

			// Check if block contains env vars referencing our service and strip them
			if len(referencedEnvVars) > 0 {
				blockModified := false
				var cleanedBlock []string
				for _, bline := range block {
					skipLine := false
					for _, envVar := range referencedEnvVars {
						if strings.Contains(bline, envVar) {
							skipLine = true
							// Also skip the following "value:" line if this is a "- name:" env line
							break
						}
					}
					if !skipLine {
						cleanedBlock = append(cleanedBlock, bline)
					} else {
						blockModified = true
					}
				}
				if blockModified {
					// Also clean up orphaned "value:" lines that followed removed "- name:" lines
					var finalBlock []string
					for k, bline := range cleanedBlock {
						btrimmed := strings.TrimSpace(bline)
						// Skip value: lines that are now orphaned (previous line was removed)
						if strings.HasPrefix(btrimmed, "value:") && (k == 0 || strings.TrimSpace(cleanedBlock[k-1]) == "") {
							continue
						}
						finalBlock = append(finalBlock, bline)
					}
					filtered = append(filtered, finalBlock...)
					i = j
					changes++
					continue
				}
			}
		}

		filtered = append(filtered, line)
		i++
		_ = skipBlock
	}

	if changes == 0 {
		return ""
	}

	result = strings.Join(filtered, "\n")
	if err := os.WriteFile(wfPath, []byte(result), 0644); err != nil {
		log.Printf("[cleanup] failed to write workflow: %v", err)
		return ""
	}

	log.Printf("[cleanup] removed %d step(s) from workflow for service %s", changes, serviceName)
	return fmt.Sprintf("Cleaned %d step(s) from CI workflow", changes)
}
