package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ── production metrics ──────────────────────────────────────────

var (
	prodMetricsContext   string
	prodMetricsRetention string
	prodMetricsScrape    string
	prodMetricsUninstall bool
)

var productionMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Install lightweight metrics (VictoriaMetrics + kube-state-metrics)",
	Long: `Installs VictoriaMetrics single-node and kube-state-metrics with minimal
resource footprint, optimised for small production clusters.

VictoriaMetrics is a PromQL-compatible metrics backend that uses 2-5x less
memory than Prometheus. The kindling dashboard auto-detects it — all charts,
sparklines, and the PromQL console work out of the box.

What gets installed:
  • VictoriaMetrics single-node  (~60MB RAM, 2h default retention)
  • kube-state-metrics           (~30MB RAM, cluster object metrics)
  • ServiceAccount + RBAC for kube-state-metrics
  • Scrape configs for kubelet, kube-state-metrics, and pod annotations

Examples:
  kindling production metrics --context my-prod
  kindling production metrics --context my-prod --retention 24h --scrape 30s
  kindling production metrics --context my-prod --uninstall`,
	RunE: runProductionMetrics,
}

func init() {
	productionMetricsCmd.Flags().StringVar(&prodMetricsContext, "context", "", "Kubeconfig context for the production cluster (required)")
	productionMetricsCmd.Flags().StringVar(&prodMetricsRetention, "retention", "2h", "How long to retain metrics data (e.g. 2h, 24h, 7d)")
	productionMetricsCmd.Flags().StringVar(&prodMetricsScrape, "scrape", "30s", "Scrape interval (e.g. 15s, 30s, 60s)")
	productionMetricsCmd.Flags().BoolVar(&prodMetricsUninstall, "uninstall", false, "Remove metrics stack instead of installing")
	_ = productionMetricsCmd.MarkFlagRequired("context")
	productionCmd.AddCommand(productionMetricsCmd)
}

func runProductionMetrics(cmd *cobra.Command, args []string) error {
	ctx := prodMetricsContext

	if strings.HasPrefix(ctx, "kind-") {
		return fmt.Errorf("context %q looks like a Kind cluster — production metrics are for external clusters", ctx)
	}

	if prodMetricsUninstall {
		return uninstallMetrics(ctx)
	}

	header("Lightweight metrics stack")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, ctx, colorReset))

	// ── Create namespace ────────────────────────────────────────
	step("📦", "Creating monitoring namespace")
	nsYAML := `apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
  labels:
    app.kubernetes.io/managed-by: kindling
`
	if err := runStdin(nsYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("failed to create monitoring namespace: %w", err)
	}

	// ── Install kube-state-metrics ──────────────────────────────
	step("📊", "Installing kube-state-metrics")
	if err := installKubeStateMetrics(ctx); err != nil {
		return fmt.Errorf("kube-state-metrics installation failed: %w", err)
	}
	success("kube-state-metrics installed")

	// ── Install VictoriaMetrics ─────────────────────────────────
	step("📈", "Installing VictoriaMetrics single-node")
	if err := installVictoriaMetrics(ctx); err != nil {
		return fmt.Errorf("VictoriaMetrics installation failed: %w", err)
	}

	// Wait for rollout
	step("⏳", "Waiting for VictoriaMetrics to be ready")
	for i := 0; i < 30; i++ {
		_, err := runSilent("kubectl", "--context", ctx, "-n", "monitoring",
			"rollout", "status", "deployment/vmsingle", "--timeout=5s")
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	success("VictoriaMetrics installed")

	// ── Summary ─────────────────────────────────────────────────
	fmt.Println()
	fmt.Fprintf(os.Stderr, "  %s📊 Metrics stack ready!%s\n", colorGreen+colorBold, colorReset)
	fmt.Println()
	fmt.Fprintf(os.Stderr, "  VictoriaMetrics  %smonitoring/vmsingle:8428%s  (retention: %s, scrape: %s)\n",
		colorCyan, colorReset, prodMetricsRetention, prodMetricsScrape)
	fmt.Fprintf(os.Stderr, "  kube-state       %smonitoring/kube-state-metrics:8080%s\n",
		colorCyan, colorReset)
	fmt.Println()
	fmt.Fprintf(os.Stderr, "  The kindling dashboard auto-detects VictoriaMetrics.\n")
	fmt.Fprintf(os.Stderr, "  Run: %skindling dashboard --prod-context %s%s\n", colorCyan, ctx, colorReset)
	fmt.Println()

	return nil
}

// ── kube-state-metrics manifests ────────────────────────────────

func installKubeStateMetrics(ctx string) error {
	sa := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-state-metrics
  namespace: monitoring
  labels:
    app.kubernetes.io/name: kube-state-metrics
    app.kubernetes.io/managed-by: kindling
`
	clusterRole := `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-state-metrics
  labels:
    app.kubernetes.io/name: kube-state-metrics
    app.kubernetes.io/managed-by: kindling
rules:
- apiGroups: [""]
  resources:
  - configmaps
  - secrets
  - nodes
  - pods
  - services
  - serviceaccounts
  - resourcequotas
  - replicationcontrollers
  - limitranges
  - persistentvolumeclaims
  - persistentvolumes
  - namespaces
  - endpoints
  - events
  verbs: ["list", "watch"]
- apiGroups: ["apps"]
  resources:
  - statefulsets
  - daemonsets
  - deployments
  - replicasets
  verbs: ["list", "watch"]
- apiGroups: ["batch"]
  resources:
  - cronjobs
  - jobs
  verbs: ["list", "watch"]
- apiGroups: ["autoscaling"]
  resources:
  - horizontalpodautoscalers
  verbs: ["list", "watch"]
- apiGroups: ["networking.k8s.io"]
  resources:
  - networkpolicies
  - ingresses
  verbs: ["list", "watch"]
- apiGroups: ["coordination.k8s.io"]
  resources:
  - leases
  verbs: ["list", "watch"]
- apiGroups: ["certificates.k8s.io"]
  resources:
  - certificatesigningrequests
  verbs: ["list", "watch"]
- apiGroups: ["storage.k8s.io"]
  resources:
  - storageclasses
  - volumeattachments
  verbs: ["list", "watch"]
- apiGroups: ["policy"]
  resources:
  - poddisruptionbudgets
  verbs: ["list", "watch"]
`
	crb := `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-state-metrics
  labels:
    app.kubernetes.io/name: kube-state-metrics
    app.kubernetes.io/managed-by: kindling
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-state-metrics
subjects:
- kind: ServiceAccount
  name: kube-state-metrics
  namespace: monitoring
`
	deploy := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-state-metrics
  namespace: monitoring
  labels:
    app.kubernetes.io/name: kube-state-metrics
    app.kubernetes.io/managed-by: kindling
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: kube-state-metrics
  template:
    metadata:
      labels:
        app.kubernetes.io/name: kube-state-metrics
    spec:
      serviceAccountName: kube-state-metrics
      containers:
      - name: kube-state-metrics
        image: registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.13.0
        ports:
        - containerPort: 8080
          name: http-metrics
        - containerPort: 8081
          name: telemetry
        resources:
          requests:
            cpu: 10m
            memory: 32Mi
          limits:
            memory: 64Mi
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          timeoutSeconds: 5
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          timeoutSeconds: 5
      nodeSelector:
        kubernetes.io/os: linux
`
	svc := `apiVersion: v1
kind: Service
metadata:
  name: kube-state-metrics
  namespace: monitoring
  labels:
    app.kubernetes.io/name: kube-state-metrics
    app.kubernetes.io/managed-by: kindling
spec:
  ports:
  - name: http-metrics
    port: 8080
    targetPort: http-metrics
  - name: telemetry
    port: 8081
    targetPort: telemetry
  selector:
    app.kubernetes.io/name: kube-state-metrics
`

	manifests := []string{sa, clusterRole, crb, deploy, svc}
	combined := strings.Join(manifests, "---\n")
	return runStdin(combined, "kubectl", "--context", ctx, "apply", "-f", "-")
}

// ── VictoriaMetrics manifests ───────────────────────────────────

func installVictoriaMetrics(ctx string) error {
	scrapeConfig := buildScrapeConfig()

	configMap := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: vmsingle-config
  namespace: monitoring
  labels:
    app.kubernetes.io/name: vmsingle
    app.kubernetes.io/managed-by: kindling
data:
  scrape.yml: |
%s
`, indent(scrapeConfig, 4))

	deploy := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: vmsingle
  namespace: monitoring
  labels:
    app.kubernetes.io/name: vmsingle
    app.kubernetes.io/component: server
    app.kubernetes.io/managed-by: kindling
    app: vmsingle
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vmsingle
  template:
    metadata:
      labels:
        app: vmsingle
        app.kubernetes.io/name: vmsingle
    spec:
      containers:
      - name: vmsingle
        image: victoriametrics/victoria-metrics:v1.106.1
        args:
        - -retentionPeriod=%s
        - -promscrape.config=/config/scrape.yml
        - -search.latencyOffset=5s
        - -search.maxUniqueTimeseries=50000
        ports:
        - containerPort: 8428
          name: http
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            memory: 256Mi
        readinessProbe:
          httpGet:
            path: /health
            port: 8428
          initialDelaySeconds: 5
        livenessProbe:
          httpGet:
            path: /health
            port: 8428
          initialDelaySeconds: 30
        volumeMounts:
        - name: config
          mountPath: /config
        - name: data
          mountPath: /victoria-metrics-data
      volumes:
      - name: config
        configMap:
          name: vmsingle-config
      - name: data
        emptyDir:
          sizeLimit: 1Gi
      nodeSelector:
        kubernetes.io/os: linux
`, prodMetricsRetention)

	svc := `apiVersion: v1
kind: Service
metadata:
  name: vmsingle
  namespace: monitoring
  labels:
    app.kubernetes.io/name: vmsingle
    app: vmsingle
    app.kubernetes.io/managed-by: kindling
spec:
  ports:
  - name: http
    port: 8428
    targetPort: http
  selector:
    app: vmsingle
`

	manifests := []string{configMap, deploy, svc}
	combined := strings.Join(manifests, "---\n")
	return runStdin(combined, "kubectl", "--context", ctx, "apply", "-f", "-")
}

func buildScrapeConfig() string {
	return fmt.Sprintf(`global:
  scrape_interval: %s

scrape_configs:
  # Kubelet cAdvisor — container CPU, memory, network, disk
  - job_name: kubelet
    scheme: https
    tls_config:
      insecure_skip_verify: true
    bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
    kubernetes_sd_configs:
    - role: node
    relabel_configs:
    - target_label: __address__
      replacement: kubernetes.default.svc:443
    - source_labels: [__meta_kubernetes_node_name]
      target_label: __metrics_path__
      replacement: /api/v1/nodes/${1}/proxy/metrics/cadvisor
    metric_relabel_configs:
    - source_labels: [__name__]
      regex: container_(cpu_usage_seconds_total|memory_working_set_bytes|network_receive_bytes_total|network_transmit_bytes_total|fs_usage_bytes|fs_limit_bytes)
      action: keep

  # kube-state-metrics — deployments, pods, nodes, etc.
  - job_name: kube-state-metrics
    static_configs:
    - targets: ["kube-state-metrics.monitoring.svc:8080"]
    metric_relabel_configs:
    - source_labels: [__name__]
      regex: kube_(deployment_status_replicas|deployment_spec_replicas|pod_status_phase|pod_container_status_restarts_total|node_status_condition|node_info|pod_info|namespace_created|service_info|daemonset_status_desired_number_scheduled|daemonset_status_number_ready|statefulset_status_replicas|statefulset_replicas)
      action: keep

  # Pods with prometheus.io annotations
  - job_name: pod-annotations
    kubernetes_sd_configs:
    - role: pod
    relabel_configs:
    - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
      action: keep
      regex: "true"
    - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
      target_label: __metrics_path__
      regex: (.+)
    - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
      action: replace
      regex: ([^:]+)(?::(\d+))?;(\d+)
      replacement: ${1}:${3}
      target_label: __address__
    - source_labels: [__meta_kubernetes_namespace]
      target_label: namespace
    - source_labels: [__meta_kubernetes_pod_name]
      target_label: pod
`, prodMetricsScrape)
}

// indent prepends each line with n spaces.
func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

// ── Uninstall ───────────────────────────────────────────────────

func uninstallMetrics(ctx string) error {
	header("Removing metrics stack")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, ctx, colorReset))

	if !confirmPrompt("Remove VictoriaMetrics and kube-state-metrics from the cluster?") {
		fmt.Println("  Cancelled.")
		return nil
	}

	step("🗑", "Removing VictoriaMetrics")
	_ = run("kubectl", "--context", ctx, "-n", "monitoring", "delete", "deployment", "vmsingle", "--ignore-not-found")
	_ = run("kubectl", "--context", ctx, "-n", "monitoring", "delete", "service", "vmsingle", "--ignore-not-found")
	_ = run("kubectl", "--context", ctx, "-n", "monitoring", "delete", "configmap", "vmsingle-config", "--ignore-not-found")

	step("🗑", "Removing kube-state-metrics")
	_ = run("kubectl", "--context", ctx, "-n", "monitoring", "delete", "deployment", "kube-state-metrics", "--ignore-not-found")
	_ = run("kubectl", "--context", ctx, "-n", "monitoring", "delete", "service", "kube-state-metrics", "--ignore-not-found")
	_ = run("kubectl", "--context", ctx, "delete", "clusterrole", "kube-state-metrics", "--ignore-not-found")
	_ = run("kubectl", "--context", ctx, "delete", "clusterrolebinding", "kube-state-metrics", "--ignore-not-found")
	_ = run("kubectl", "--context", ctx, "-n", "monitoring", "delete", "serviceaccount", "kube-state-metrics", "--ignore-not-found")

	success("Metrics stack removed")
	fmt.Println()
	return nil
}
