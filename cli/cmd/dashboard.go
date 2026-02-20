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
	"strings"
	"syscall"
	"time"

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
	mux.HandleFunc("/api/pods", handlePods)
	mux.HandleFunc("/api/services", handleServices)
	mux.HandleFunc("/api/ingresses", handleIngresses)
	mux.HandleFunc("/api/secrets", handleSecrets)
	mux.HandleFunc("/api/events", handleEvents)
	mux.HandleFunc("/api/namespaces", handleNamespaces)
	mux.HandleFunc("/api/logs/", handleLogs)

	// ── API routes (actions) ────────────────────────────────────
	mux.HandleFunc("/api/deploy", handleDeployAction)
	mux.HandleFunc("/api/dses/", handleDeleteDSE)        // DELETE /api/dses/{ns}/{name}
	mux.HandleFunc("/api/secrets/create", handleCreateSecret)
	mux.HandleFunc("/api/secrets/", handleDeleteSecret)   // DELETE /api/secrets/{ns}/{name}
	mux.HandleFunc("/api/runners/create", handleCreateRunner)
	mux.HandleFunc("/api/reset-runners", handleResetRunners)
	mux.HandleFunc("/api/env/set", handleEnvSet)
	mux.HandleFunc("/api/env/unset", handleEnvUnset)
	mux.HandleFunc("/api/env/list/", handleEnvList)       // GET /api/env/list/{ns}/{dep}
	mux.HandleFunc("/api/expose", handleExposeAction)     // POST=start, DELETE=stop
	mux.HandleFunc("/api/expose/status", handleExposeStatus)
	mux.HandleFunc("/api/cluster/destroy", handleDestroyCluster)
	mux.HandleFunc("/api/init", handleInitCluster)
	mux.HandleFunc("/api/restart/", handleRestartDeployment)
	mux.HandleFunc("/api/scale/", handleScaleDeployment)
	mux.HandleFunc("/api/pods/", handleDeletePod)         // DELETE /api/pods/{ns}/{name}
	mux.HandleFunc("/api/apply", handleApplyYAML)

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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	fmt.Fprintf(os.Stderr, "\n%s%s▸ Kindling Dashboard%s\n", colorBold, colorCyan, colorReset)
	fmt.Fprintf(os.Stderr, "  🌐  http://localhost:%d\n", dashboardPort)
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
	fullArgs := append([]string{"--context", "kind-" + clusterName}, args...)
	return runCapture("kubectl", fullArgs...)
}
