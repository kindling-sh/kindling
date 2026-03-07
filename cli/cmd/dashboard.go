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

The dashboard runs on http://localhost:9090 by default.`,
	RunE: runDashboard,
}

var dashboardPort int

func init() {
	dashboardCmd.Flags().IntVar(&dashboardPort, "port", 9090, "Port to serve the dashboard on")
	dashboardCmd.Flags().StringVar(&prodContext, "prod-context", "", "Kubeconfig context for production cluster (enables production panel)")
	rootCmd.AddCommand(dashboardCmd)
}

func runDashboard(cmd *cobra.Command, args []string) error {
	mux := http.NewServeMux()

	// ── API routes (read-only) ──────────────────────────────────
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
	mux.HandleFunc("/api/env/list/", handleEnvList)   // GET /api/env/list/{ns}/{dep}
	mux.HandleFunc("/api/expose", handleExposeAction) // POST=start, DELETE=stop
	mux.HandleFunc("/api/expose/status", handleExposeStatus)
	mux.HandleFunc("/api/cluster/destroy", handleDestroyCluster)
	mux.HandleFunc("/api/init", handleInitCluster)
	mux.HandleFunc("/api/restart/", handleRestartDeployment)
	mux.HandleFunc("/api/scale/", handleScaleDeployment)
	mux.HandleFunc("/api/pods/", handleDeletePod) // DELETE /api/pods/{ns}/{name}
	mux.HandleFunc("/api/apply", handleApplyYAML)
	mux.HandleFunc("/api/sync", handleSyncAction)          // POST=start, DELETE=stop
	mux.HandleFunc("/api/sync/status", handleSyncStatus)   // GET
	mux.HandleFunc("/api/runtime/", handleRuntimeDetect)   // GET /api/runtime/{ns}/{dep}
	mux.HandleFunc("/api/load", handleLoadAction)          // POST — build + load + rollout
	mux.HandleFunc("/api/load-context", handleLoadContext) // GET — discover service dirs
	mux.HandleFunc("/api/intel", handleIntel)              // GET=status, POST=activate, DELETE=deactivate
	mux.HandleFunc("/api/generate", handleGenerate)        // POST — AI workflow generation (ndjson)

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

	// ── API routes (production cluster) ─────────────────────────
	if prodContext != "" {
		mux.HandleFunc("/api/prod/cluster", handleProdCluster)
		mux.HandleFunc("/api/prod/contexts", handleProdContexts)
		mux.HandleFunc("/api/prod/nodes", handleProdNodes)
		mux.HandleFunc("/api/prod/namespaces", handleProdNamespaces)
		mux.HandleFunc("/api/prod/deployments", handleProdDeployments)
		mux.HandleFunc("/api/prod/pods", handleProdPods)
		mux.HandleFunc("/api/prod/services", handleProdServices)
		mux.HandleFunc("/api/prod/ingresses", handleProdIngresses)
		mux.HandleFunc("/api/prod/events", handleProdEvents)
		mux.HandleFunc("/api/prod/secrets", handleProdSecrets)
		mux.HandleFunc("/api/prod/statefulsets", handleProdStatefulSets)
		mux.HandleFunc("/api/prod/daemonsets", handleProdDaemonSets)
		mux.HandleFunc("/api/prod/replicasets", handleProdReplicaSets)
		mux.HandleFunc("/api/prod/clusterroles", handleProdClusterRoles)
		mux.HandleFunc("/api/prod/clusterrolebindings", handleProdClusterRoleBindings)
		mux.HandleFunc("/api/prod/logs/", handleProdLogs)
		mux.HandleFunc("/api/prod/restart/", handleProdRestart)
		mux.HandleFunc("/api/prod/scale/", handleProdScale)
		mux.HandleFunc("/api/prod/delete-pod/", handleProdDeletePod)
		mux.HandleFunc("/api/prod/rollout-history/", handleProdRolloutHistory)
		mux.HandleFunc("/api/prod/rollback/", handleProdRollback)
		mux.HandleFunc("/api/prod/rollout-status/", handleProdRolloutStatus)
		mux.HandleFunc("/api/prod/exec", handleProdExec)
		mux.HandleFunc("/api/prod/describe/", handleProdDescribe)
		mux.HandleFunc("/api/prod/certificates", handleProdCertificates)
		mux.HandleFunc("/api/prod/clusterissuers", handleProdClusterIssuers)
		mux.HandleFunc("/api/prod/node-metrics", handleProdNodeMetrics)
		mux.HandleFunc("/api/prod/pod-metrics", handleProdPodMetrics)
		mux.HandleFunc("/api/prod/apply", handleProdApply)
		mux.HandleFunc("/api/prod/advisor", handleProdAdvisor)

		// Snapshot / Deploy
		mux.HandleFunc("/api/prod/snapshot/status", handleProdSnapshotStatus)
		mux.HandleFunc("/api/prod/snapshot/deploy", handleProdSnapshotDeploy)

		// TLS management
		mux.HandleFunc("/api/prod/tls/status", handleProdTLSStatus)
		mux.HandleFunc("/api/prod/tls/install", handleProdTLSInstall)

		// VictoriaMetrics management
		mux.HandleFunc("/api/prod/metrics/status", handleProdMetricsStatus)
		mux.HandleFunc("/api/prod/metrics/install", handleProdMetricsInstall)
		mux.HandleFunc("/api/prod/metrics/uninstall", handleProdMetricsUninstall)

		// Prometheus-compatible query API
		mux.HandleFunc("/api/prod/prometheus/status", handlePromStatus)
		mux.HandleFunc("/api/prod/prometheus/query", handlePromQuery)
		mux.HandleFunc("/api/prod/prometheus/query_range", handlePromQueryRange)
	} else {
		// Return a minimal handler so the frontend can detect no prod context
		mux.HandleFunc("/api/prod/cluster", func(w http.ResponseWriter, r *http.Request) {
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
	if prodContext != "" {
		fmt.Fprintf(os.Stderr, "  🏭  Production context: %s%s%s\n", colorBold, prodContext, colorReset)
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
