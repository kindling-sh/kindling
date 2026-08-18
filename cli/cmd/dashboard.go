package cmd

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

//go:embed dashboard-ui/dist
var dashboardFS embed.FS

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Launch the kindling web dashboard",
	Long: `Starts a local web server that provides a comprehensive dashboard
for your kindling cluster. Shows all Kubernetes resources, DSE environments,
runner pools, health checks, logs, and more.

The dashboard runs on http://localhost:19090 by default.`,
	RunE: runDashboard,
}

var dashboardPort int

func init() {
	dashboardCmd.Flags().IntVar(&dashboardPort, "port", 19090, "Port to serve the dashboard on")
	dashboardCmd.Flags().StringVar(&stagingContext, "staging-context", "", "Kubeconfig context for staging cluster (enables staging panel)")
	rootCmd.AddCommand(dashboardCmd)
}

func runDashboard(cmd *cobra.Command, args []string) error {
	mux := http.NewServeMux()

	// ── API routes (read-only) ──────────────────────────────────
	mux.HandleFunc("/api/contexts", handleContexts)
	mux.HandleFunc("/api/cluster", handleCluster)
	mux.HandleFunc("/api/nodes", handleNodes)
	mux.HandleFunc("/api/operator", handleOperator)
	mux.HandleFunc("/api/registry", handleRegistry)
	mux.HandleFunc("/api/ingress-controller", handleIngressController)
	mux.HandleFunc("/api/dses", handleDSEs)
	mux.HandleFunc("/api/runners", handleRunners)
	mux.HandleFunc("/api/deployments", handleDeployments)
	mux.HandleFunc("/api/replicasets", handleReplicaSets)
	mux.HandleFunc("/api/pods", handlePods)
	mux.HandleFunc("/api/services", handleServices)
	mux.HandleFunc("/api/ingresses", handleIngresses)
	mux.HandleFunc("/api/secrets", handleSecrets)
	mux.HandleFunc("/api/events", handleEvents)
	mux.HandleFunc("/api/namespaces", handleNamespaces)
	mux.HandleFunc("/api/serviceaccounts", handleServiceAccounts)
	mux.HandleFunc("/api/roles", handleRoles)
	mux.HandleFunc("/api/rolebindings", handleRoleBindings)
	mux.HandleFunc("/api/clusterroles", handleClusterRoles)
	mux.HandleFunc("/api/clusterrolebindings", handleClusterRoleBindings)
	mux.HandleFunc("/api/logs/", handleLogs)

	// ── API routes (actions) ────────────────────────────────────
	mux.HandleFunc("/api/deploy", handleDeployAction)
	mux.HandleFunc("/api/dses/", handleDeleteDSE) // DELETE /api/dses/{ns}/{name}
	mux.HandleFunc("/api/secrets/create", handleCreateSecret)
	mux.HandleFunc("/api/secrets/", handleDeleteSecret) // DELETE /api/secrets/{ns}/{name}
	mux.HandleFunc("/api/runners/create", handleCreateRunner)
	mux.HandleFunc("/api/reset-runners", handleResetRunners)
	mux.HandleFunc("/api/env/set", handleEnvSet)
	mux.HandleFunc("/api/env/unset", handleEnvUnset)
	mux.HandleFunc("/api/env/list/", handleEnvList)          // GET /api/env/list/{ns}/{dep}
	mux.HandleFunc("/api/expose", handleExposeAction)        // POST=start, DELETE=stop
	mux.HandleFunc("/api/expose/status", handleExposeStatus) // GET
	mux.HandleFunc("/api/cluster/destroy", handleDestroyCluster)
	mux.HandleFunc("/api/init", handleInitCluster)
	mux.HandleFunc("/api/restart/", handleRestartDeployment)
	mux.HandleFunc("/api/scale/", handleScaleDeployment)
	mux.HandleFunc("/api/pods/", handleDeletePod) // DELETE /api/pods/{ns}/{name}
	mux.HandleFunc("/api/apply", handleApplyYAML)
	mux.HandleFunc("/api/sync", handleSyncAction)                      // POST=start, DELETE=stop
	mux.HandleFunc("/api/sync/status", handleSyncStatus)               // GET
	mux.HandleFunc("/api/runtime/", handleRuntimeDetect)               // GET /api/runtime/{ns}/{dep}
	mux.HandleFunc("/api/load", handleLoadAction)                      // POST — build + load + rollout
	mux.HandleFunc("/api/load-context", handleLoadContext)             // GET — discover service dirs
	mux.HandleFunc("/api/analyze", handleAnalyze)                      // POST — repo readiness analysis
	mux.HandleFunc("/api/generate", handleGenerate)                    // POST — AI workflow generation (ndjson)
	mux.HandleFunc("/api/git/commit-and-push", handleGitCommitAndPush) // POST — commit + push (ndjson)

	// ── API routes (topology editor) ────────────────────────────
	mux.HandleFunc("/api/topology", handleGetTopology)                    // GET — current topology graph
	mux.HandleFunc("/api/topology/status", handleGetTopologyStatus)       // GET — live pod health overlay
	mux.HandleFunc("/api/topology/logs", handleTopologyLogs)              // GET — aggregated pod logs by node
	mux.HandleFunc("/api/topology/node/detail", handleTopologyNodeDetail) // GET — pods, events, env
	mux.HandleFunc("/api/topology/deploy", handleDeployTopology)          // POST — deploy topology
	mux.HandleFunc("/api/topology/scaffold", handleScaffoldService)       // POST — scaffold service dir
	mux.HandleFunc("/api/topology/cleanup", handleCleanupService)         // POST — cleanup deleted service
	mux.HandleFunc("/api/topology/edge/remove", handleRemoveEdge)         // POST — remove edge env/dep from DSE
	mux.HandleFunc("/api/topology/canvas", handleSaveCanvas)              // POST — persist canvas overlay
	mux.HandleFunc("/api/topology/workspace", handleWorkspaceInfo)        // GET — repo root + service dirs
	mux.HandleFunc("/api/topology/check-path", handleCheckPath)           // GET — check dir existence
	mux.HandleFunc("/api/fs/complete", handleFsComplete)                  // GET — dir autocomplete

	// ── API routes (proxy / API explorer) ───────────────────────
	mux.HandleFunc("/api/proxy", handleProxy) // POST — proxy request to in-cluster service
	mux.HandleFunc("/api/proxy/services/", func(w http.ResponseWriter, r *http.Request) {
		// Route to spec handler if path ends with /spec
		if strings.HasSuffix(r.URL.Path, "/spec") {
			handleApiSpec(w, r)
			return
		}
		handleProxyServiceDetail(w, r)
	})
	mux.HandleFunc("/api/proxy/services", handleProxyServices) // GET — list proxyable services

	// ── API routes (debug) ──────────────────────────────────────
	mux.HandleFunc("/api/debug", handleDebugAction)        // POST=start, DELETE=stop
	mux.HandleFunc("/api/debug/status", handleDebugStatus) // GET — active debug sessions

	// ── API routes (staging cluster) ────────────────────────────
	if stagingContext != "" {
		mux.HandleFunc("/api/staging/cluster", handleStagingCluster)
		mux.HandleFunc("/api/staging/contexts", handleStagingContexts)
		mux.HandleFunc("/api/staging/nodes", handleStagingNodes)
		mux.HandleFunc("/api/staging/namespaces", handleStagingNamespaces)
		mux.HandleFunc("/api/staging/deployments", handleStagingDeployments)
		mux.HandleFunc("/api/staging/pods", handleStagingPods)
		mux.HandleFunc("/api/staging/services", handleStagingServices)
		mux.HandleFunc("/api/staging/ingresses", handleStagingIngresses)
		mux.HandleFunc("/api/staging/ingress-controller", handleStagingIngressController)
		mux.HandleFunc("/api/staging/events", handleStagingEvents)
		mux.HandleFunc("/api/staging/secrets", handleStagingSecrets)
		mux.HandleFunc("/api/staging/statefulsets", handleStagingStatefulSets)
		mux.HandleFunc("/api/staging/daemonsets", handleStagingDaemonSets)
		mux.HandleFunc("/api/staging/replicasets", handleStagingReplicaSets)
		mux.HandleFunc("/api/staging/clusterroles", handleStagingClusterRoles)
		mux.HandleFunc("/api/staging/clusterrolebindings", handleStagingClusterRoleBindings)
		mux.HandleFunc("/api/staging/logs/", handleStagingLogs)
		mux.HandleFunc("/api/staging/restart/", handleStagingRestart)
		mux.HandleFunc("/api/staging/scale/", handleStagingScale)
		mux.HandleFunc("/api/staging/delete-pod/", handleStagingDeletePod)
		mux.HandleFunc("/api/staging/rollout-history/", handleStagingRolloutHistory)
		mux.HandleFunc("/api/staging/rollback/", handleStagingRollback)
		mux.HandleFunc("/api/staging/rollout-status/", handleStagingRolloutStatus)
		mux.HandleFunc("/api/staging/exec", handleStagingExec)
		mux.HandleFunc("/api/staging/describe/", handleStagingDescribe)
		mux.HandleFunc("/api/staging/certificates", handleStagingCertificates)
		mux.HandleFunc("/api/staging/clusterissuers", handleStagingClusterIssuers)
		mux.HandleFunc("/api/staging/node-metrics", handleStagingNodeMetrics)
		mux.HandleFunc("/api/staging/pod-metrics", handleStagingPodMetrics)
		mux.HandleFunc("/api/staging/apply", handleStagingApply)
		mux.HandleFunc("/api/staging/advisor", handleStagingAdvisor)

		// Snapshot / Deploy
		mux.HandleFunc("/api/staging/snapshot/status", handleStagingSnapshotStatus)
		mux.HandleFunc("/api/staging/snapshot/credentials", handleStagingSnapshotCredentials)
		mux.HandleFunc("/api/staging/snapshot/deploy", handleStagingSnapshotDeploy)
		mux.HandleFunc("/api/staging/snapshot/secrets/update", handleStagingSnapshotSecretsUpdate)

		// TLS management
		mux.HandleFunc("/api/staging/tls/status", handleStagingTLSStatus)
		mux.HandleFunc("/api/staging/tls/install", handleStagingTLSInstall)

		// VictoriaMetrics management
		mux.HandleFunc("/api/staging/metrics/status", handleStagingMetricsStatus)
		mux.HandleFunc("/api/staging/metrics/install", handleStagingMetricsInstall)
		mux.HandleFunc("/api/staging/metrics/uninstall", handleStagingMetricsUninstall)

		// Prometheus-compatible query API
		mux.HandleFunc("/api/staging/prometheus/status", handlePromStatus)
		mux.HandleFunc("/api/staging/prometheus/query", handlePromQuery)
		mux.HandleFunc("/api/staging/prometheus/query_range", handlePromQueryRange)
	} else {
		// Return a minimal handler so the frontend can detect no staging context
		mux.HandleFunc("/api/staging/cluster", func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, map[string]interface{}{
				"context":   "",
				"connected": false,
			})
		})
	}

	// ── Static frontend ─────────────────────────────────────────
	distFS, err := fs.Sub(dashboardFS, "dashboard-ui/dist")
	if err != nil {
		return fmt.Errorf("cannot load embedded dashboard: %w", err)
	}
	fileServer := http.FileServer(http.FS(distFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// For SPA routing: serve index.html for non-file paths
		path := r.URL.Path
		if path != "/" && !strings.Contains(path, ".") {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", dashboardPort)

	// Kill any stale dashboard process still bound to the port.
	if pid := findProcessOnPort(dashboardPort); pid > 0 {
		fmt.Fprintf(os.Stderr, "  ⚠️  Port %d in use by pid %d — stopping stale dashboard...\n", dashboardPort, pid)
		_ = syscall.Kill(pid, syscall.SIGTERM)
		// Give it a moment to release the port.
		for i := 0; i < 10; i++ {
			time.Sleep(300 * time.Millisecond)
			if findProcessOnPort(dashboardPort) == 0 {
				break
			}
		}
		if p := findProcessOnPort(dashboardPort); p > 0 {
			_ = syscall.Kill(p, syscall.SIGKILL)
			time.Sleep(300 * time.Millisecond)
		}
	}

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		fmt.Fprintln(os.Stderr, "\nShutting down dashboard...")
		cleanupPromForward()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	fmt.Fprintf(os.Stderr, "\n%s%s▸ Kindling Dashboard%s\n", colorBold, colorCyan, colorReset)
	fmt.Fprintf(os.Stderr, "  🌐  http://localhost:%d\n", dashboardPort)
	if stagingContext != "" {
		fmt.Fprintf(os.Stderr, "  🏭  Staging context: %s%s%s\n", colorBold, stagingContext, colorReset)
	}
	fmt.Fprintf(os.Stderr, "  %sPress Ctrl+C to stop%s\n\n", colorDim, colorReset)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// ── JSON helpers ────────────────────────────────────────────────

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// kubectl runs a kubectl command and returns stdout.
func kubectlJSON(args ...string) (string, error) {
	return core.Kubectl(clusterName, args...)
}

// findProcessOnPort uses lsof to find the PID listening on a TCP port.
// Returns 0 if nothing is bound.
func findProcessOnPort(port int) int {
	out, err := runCapture("lsof", "-ti", fmt.Sprintf("tcp:%d", port))
	if err != nil || strings.TrimSpace(out) == "" {
		return 0
	}
	// lsof may return multiple PIDs (one per line) — take the first.
	line := strings.TrimSpace(strings.Split(out, "\n")[0])
	pid, err := strconv.Atoi(line)
	if err != nil {
		return 0
	}
	return pid
}
