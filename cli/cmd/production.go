package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ── Production parent command ───────────────────────────────────

var productionCmd = &cobra.Command{
	Use:   "production",
	Short: "Graduate your app from kindling to a production Kubernetes cluster",
	Long: `Commands for deploying your application to a real (non-Kind) Kubernetes
cluster. This converts your DevStagingEnvironment spec into a standard Helm
chart and deploys it — no kindling operator required in production.

The --context flag is required on all subcommands to prevent accidentally
targeting the local Kind cluster.

Subcommands:
  deploy   Generate a Helm chart from a DSE and deploy to production
  tls      Install cert-manager and configure TLS for Ingress resources`,
}

func init() {
	rootCmd.AddCommand(productionCmd)
}

// ── production deploy ───────────────────────────────────────────

var (
	prodDeployFile      string
	prodDeployContext    string
	prodDeployRegistry  string
	prodDeployTag       string
	prodDeployNamespace string
	prodDeployChartOnly bool
	prodDeployChartDir  string
)

var productionDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Generate a Helm chart from a DSE and deploy to production",
	Long: `Reads a DevStagingEnvironment YAML, generates a standard Helm chart
(Deployment, Service, Ingress, dependency resources), and deploys it to your
production cluster via Helm. No kindling operator is needed in production.

The generated chart is saved to .kindling/charts/<name>/ and can be
customised or committed to your repo for GitOps workflows.

Use --chart-only to generate the chart without deploying.

Examples:
  kindling production deploy -f dev-environment.yaml --context my-prod-cluster
  kindling production deploy -f dev-environment.yaml --context prod --registry ghcr.io/myorg
  kindling production deploy -f dev-environment.yaml --context prod --chart-only
  kindling production deploy -f dev-environment.yaml --context prod --namespace staging`,
	RunE: runProductionDeploy,
}

func init() {
	productionDeployCmd.Flags().StringVarP(&prodDeployFile, "file", "f", "", "Path to DevStagingEnvironment YAML file (required)")
	productionDeployCmd.Flags().StringVar(&prodDeployContext, "context", "", "Kubeconfig context for the production cluster (required)")
	productionDeployCmd.Flags().StringVar(&prodDeployRegistry, "registry", "", "Remote container registry (e.g. ghcr.io/myorg) — rewrites localhost:5001 refs")
	productionDeployCmd.Flags().StringVar(&prodDeployTag, "tag", "", "Image tag override (default: keep existing tag)")
	productionDeployCmd.Flags().StringVarP(&prodDeployNamespace, "namespace", "n", "default", "Kubernetes namespace to deploy into")
	productionDeployCmd.Flags().BoolVar(&prodDeployChartOnly, "chart-only", false, "Generate the Helm chart without deploying")
	productionDeployCmd.Flags().StringVar(&prodDeployChartDir, "chart-dir", "", "Output directory for the chart (default: .kindling/charts/<name>)")
	_ = productionDeployCmd.MarkFlagRequired("file")
	_ = productionDeployCmd.MarkFlagRequired("context")
	productionCmd.AddCommand(productionDeployCmd)
}

// ── DSE YAML parsing (lightweight — avoids importing the full CRD types) ──

type dseManifest struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   dseMetadata `yaml:"metadata"`
	Spec       dseSpec     `yaml:"spec"`
}

type dseMetadata struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels,omitempty"`
}

type dseSpec struct {
	Deployment   dseDeployment   `yaml:"deployment"`
	Service      dseService      `yaml:"service"`
	Ingress      *dseIngress     `yaml:"ingress,omitempty"`
	Dependencies []dseDependency `yaml:"dependencies,omitempty"`
}

type dseDeployment struct {
	Image       string          `yaml:"image"`
	Replicas    *int32          `yaml:"replicas,omitempty"`
	Port        int32           `yaml:"port"`
	Command     []string        `yaml:"command,omitempty"`
	Args        []string        `yaml:"args,omitempty"`
	Env         []dseEnvVar     `yaml:"env,omitempty"`
	Resources   *dseResources   `yaml:"resources,omitempty"`
	HealthCheck *dseHealthCheck `yaml:"healthCheck,omitempty"`
}

type dseEnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value,omitempty"`
}

type dseResources struct {
	CPURequest    string `yaml:"cpuRequest,omitempty"`
	CPULimit      string `yaml:"cpuLimit,omitempty"`
	MemoryRequest string `yaml:"memoryRequest,omitempty"`
	MemoryLimit   string `yaml:"memoryLimit,omitempty"`
}

type dseHealthCheck struct {
	Type                string `yaml:"type,omitempty"`
	Path                string `yaml:"path,omitempty"`
	Port                *int32 `yaml:"port,omitempty"`
	InitialDelaySeconds *int32 `yaml:"initialDelaySeconds,omitempty"`
	PeriodSeconds       *int32 `yaml:"periodSeconds,omitempty"`
}

type dseService struct {
	Port       int32  `yaml:"port"`
	TargetPort *int32 `yaml:"targetPort,omitempty"`
	Type       string `yaml:"type,omitempty"`
}

type dseIngress struct {
	Enabled          bool              `yaml:"enabled,omitempty"`
	Host             string            `yaml:"host,omitempty"`
	Path             string            `yaml:"path,omitempty"`
	PathType         string            `yaml:"pathType,omitempty"`
	IngressClassName *string           `yaml:"ingressClassName,omitempty"`
	TLS              *dseTLS           `yaml:"tls,omitempty"`
	Annotations      map[string]string `yaml:"annotations,omitempty"`
}

type dseTLS struct {
	SecretName string   `yaml:"secretName"`
	Hosts      []string `yaml:"hosts,omitempty"`
}

type dseDependency struct {
	Type       string      `yaml:"type"`
	Version    string      `yaml:"version,omitempty"`
	Image      string      `yaml:"image,omitempty"`
	Port       *int32      `yaml:"port,omitempty"`
	Env        []dseEnvVar `yaml:"env,omitempty"`
	EnvVarName string      `yaml:"envVarName,omitempty"`
}

// ── Dependency defaults ─────────────────────────────────────────

type prodDepDefaults struct {
	Image   string
	Port    int32
	EnvVar  string
	ConnStr func(name string, port int32) string
}

var prodDepDefaultsMap = map[string]prodDepDefaults{
	"postgres": {Image: "postgres", Port: 5432, EnvVar: "DATABASE_URL",
		ConnStr: func(n string, p int32) string {
			return fmt.Sprintf("postgres://postgres:postgres@%s:%d/app?sslmode=disable", n, p)
		}},
	"redis": {Image: "redis", Port: 6379, EnvVar: "REDIS_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("redis://%s:%d/0", n, p) }},
	"mysql": {Image: "mysql", Port: 3306, EnvVar: "DATABASE_URL",
		ConnStr: func(n string, p int32) string {
			return fmt.Sprintf("mysql://root:mysql@tcp(%s:%d)/app", n, p)
		}},
	"mongodb": {Image: "mongo", Port: 27017, EnvVar: "MONGO_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("mongodb://%s:%d/app", n, p) }},
	"rabbitmq": {Image: "rabbitmq", Port: 5672, EnvVar: "AMQP_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("amqp://guest:guest@%s:%d/", n, p) }},
	"minio": {Image: "minio/minio", Port: 9000, EnvVar: "S3_ENDPOINT",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("http://%s:%d", n, p) }},
	"elasticsearch": {Image: "docker.elastic.co/elasticsearch/elasticsearch", Port: 9200, EnvVar: "ELASTICSEARCH_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("http://%s:%d", n, p) }},
	"kafka": {Image: "confluentinc/cp-kafka", Port: 9092, EnvVar: "KAFKA_BROKER_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("%s:%d", n, p) }},
	"nats": {Image: "nats", Port: 4222, EnvVar: "NATS_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("nats://%s:%d", n, p) }},
	"memcached": {Image: "memcached", Port: 11211, EnvVar: "MEMCACHED_URL",
		ConnStr: func(n string, p int32) string { return fmt.Sprintf("%s:%d", n, p) }},
}

// ── Main deploy function ────────────────────────────────────────

func runProductionDeploy(cmd *cobra.Command, args []string) error {
	if _, err := os.Stat(prodDeployFile); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", prodDeployFile)
	}

	ctx := prodDeployContext

	// Safety: refuse Kind contexts
	if strings.HasPrefix(ctx, "kind-") {
		return fmt.Errorf("context %q looks like a Kind cluster — use 'kindling deploy' for local dev", ctx)
	}

	header("Production deploy")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, ctx, colorReset))

	// ── Parse all DSE documents from the YAML ───────────────────
	data, err := os.ReadFile(prodDeployFile)
	if err != nil {
		return err
	}

	docs := strings.Split(string(data), "\n---")
	var dses []dseManifest
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var dse dseManifest
		if err := yaml.Unmarshal([]byte(doc), &dse); err != nil {
			warn(fmt.Sprintf("Skipping unparseable document: %v", err))
			continue
		}
		if dse.Kind != "DevStagingEnvironment" || dse.Metadata.Name == "" {
			continue
		}
		dses = append(dses, dse)
	}

	if len(dses) == 0 {
		return fmt.Errorf("no DevStagingEnvironment resources found in %s", prodDeployFile)
	}

	step("📄", fmt.Sprintf("Found %d service(s) in %s", len(dses), prodDeployFile))

	// ── Generate and deploy each DSE as a Helm chart ────────────
	for _, dse := range dses {
		name := dse.Metadata.Name
		// Strip -dev suffix for production naming
		prodName := strings.TrimSuffix(name, "-dev")

		chartDir := prodDeployChartDir
		if chartDir == "" {
			chartDir = filepath.Join(".kindling", "charts", prodName)
		} else if len(dses) > 1 {
			chartDir = filepath.Join(prodDeployChartDir, prodName)
		}

		step("📦", fmt.Sprintf("Generating Helm chart for %s → %s/", prodName, chartDir))
		if err := generateHelmChart(dse, prodName, chartDir, prodDeployRegistry, prodDeployTag); err != nil {
			return fmt.Errorf("chart generation failed for %s: %w", name, err)
		}
		success(fmt.Sprintf("Chart written to %s/", chartDir))

		if prodDeployChartOnly {
			continue
		}

		// ── Push images if --registry is set ────────────────────
		if prodDeployRegistry != "" {
			if err := pushLocalImage(dse.Spec.Deployment.Image, prodDeployRegistry, prodDeployTag); err != nil {
				warn(fmt.Sprintf("Image push for %s: %v", dse.Spec.Deployment.Image, err))
			}
		}

		// ── Verify cluster connectivity ─────────────────────────
		step("🔍", "Verifying cluster connectivity")
		if err := run("kubectl", "cluster-info", "--context", ctx); err != nil {
			return fmt.Errorf("cannot reach cluster via context %q: %w", ctx, err)
		}

		// ── Check for Helm ──────────────────────────────────────
		if !commandExists("helm") {
			return fmt.Errorf("helm not found on PATH — install it or use --chart-only to generate charts")
		}

		// ── Helm upgrade --install ──────────────────────────────
		step("🚀", fmt.Sprintf("Deploying %s via Helm", prodName))
		helmArgs := []string{
			"upgrade", "--install", prodName, chartDir,
			"--kube-context", ctx,
			"--namespace", prodDeployNamespace,
			"--create-namespace",
			"--wait",
			"--timeout", "5m",
		}
		if err := run("helm", helmArgs...); err != nil {
			return fmt.Errorf("helm deploy failed for %s: %w", prodName, err)
		}
		success(fmt.Sprintf("%s deployed", prodName))
	}

	if prodDeployChartOnly {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "  %s📦 Charts generated — deploy manually with:%s\n", colorGreen+colorBold, colorReset)
		for _, dse := range dses {
			prodName := strings.TrimSuffix(dse.Metadata.Name, "-dev")
			dir := prodDeployChartDir
			if dir == "" {
				dir = filepath.Join(".kindling", "charts", prodName)
			} else if len(dses) > 1 {
				dir = filepath.Join(prodDeployChartDir, prodName)
			}
			fmt.Fprintf(os.Stderr, "    helm upgrade --install %s %s --kube-context %s\n", prodName, dir, ctx)
		}
		fmt.Println()
		return nil
	}

	// ── Show status ─────────────────────────────────────────────
	fmt.Println()
	fmt.Printf("  Check pods:     %skubectl --context %s -n %s get pods%s\n", colorCyan, ctx, prodDeployNamespace, colorReset)
	fmt.Printf("  Check ingress:  %skubectl --context %s -n %s get ingress%s\n", colorCyan, ctx, prodDeployNamespace, colorReset)
	fmt.Printf("  Check services: %skubectl --context %s -n %s get svc%s\n", colorCyan, ctx, prodDeployNamespace, colorReset)
	fmt.Println()

	return nil
}

// ── Helm chart generation ───────────────────────────────────────

func generateHelmChart(dse dseManifest, name, chartDir, registry, tagOverride string) error {
	// Create directory structure
	templatesDir := filepath.Join(chartDir, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		return err
	}

	// Resolve image
	image := dse.Spec.Deployment.Image
	if registry != "" && strings.HasPrefix(image, "localhost:5001/") {
		nameTag := strings.TrimPrefix(image, "localhost:5001/")
		parts := strings.SplitN(nameTag, ":", 2)
		imgName := parts[0]
		tag := "latest"
		if len(parts) == 2 {
			tag = parts[1]
		}
		if tagOverride != "" {
			tag = tagOverride
		}
		image = fmt.Sprintf("%s/%s:%s", strings.TrimSuffix(registry, "/"), imgName, tag)
	} else if tagOverride != "" {
		parts := strings.SplitN(image, ":", 2)
		image = parts[0] + ":" + tagOverride
	}

	replicas := int32(1)
	if dse.Spec.Deployment.Replicas != nil {
		replicas = *dse.Spec.Deployment.Replicas
	}

	svcType := dse.Spec.Service.Type
	if svcType == "" {
		svcType = "ClusterIP"
	}

	// ── Chart.yaml ──────────────────────────────────────────────
	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s
description: Production Helm chart generated by kindling
version: 0.1.0
appVersion: "1.0.0"
type: application
`, name)

	// ── values.yaml ─────────────────────────────────────────────
	valuesLines := []string{
		fmt.Sprintf("replicaCount: %d", replicas),
		"",
		"image:",
		fmt.Sprintf("  repository: %s", imageRepo(image)),
		fmt.Sprintf("  tag: %q", imageTag(image)),
		"  pullPolicy: IfNotPresent",
		"",
		fmt.Sprintf("containerPort: %d", dse.Spec.Deployment.Port),
		"",
		"service:",
		fmt.Sprintf("  type: %s", svcType),
		fmt.Sprintf("  port: %d", dse.Spec.Service.Port),
	}

	if dse.Spec.Ingress != nil && dse.Spec.Ingress.Enabled {
		valuesLines = append(valuesLines, "", "ingress:", "  enabled: true")
		if dse.Spec.Ingress.Host != "" {
			host := dse.Spec.Ingress.Host
			// Strip .localhost for production
			host = strings.TrimSuffix(host, ".localhost")
			valuesLines = append(valuesLines, fmt.Sprintf("  host: %s  # Update with your production domain", host))
		}
		cls := "nginx"
		if dse.Spec.Ingress.IngressClassName != nil {
			cls = *dse.Spec.Ingress.IngressClassName
		}
		valuesLines = append(valuesLines, fmt.Sprintf("  ingressClassName: %s", cls))
		path := dse.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}
		valuesLines = append(valuesLines, fmt.Sprintf("  path: %s", path))
		pathType := dse.Spec.Ingress.PathType
		if pathType == "" {
			pathType = "Prefix"
		}
		valuesLines = append(valuesLines, fmt.Sprintf("  pathType: %s", pathType))

		if dse.Spec.Ingress.TLS != nil {
			valuesLines = append(valuesLines, "  tls:", fmt.Sprintf("    secretName: %s", dse.Spec.Ingress.TLS.SecretName))
		}
		if len(dse.Spec.Ingress.Annotations) > 0 {
			valuesLines = append(valuesLines, "  annotations:")
			for k, v := range dse.Spec.Ingress.Annotations {
				valuesLines = append(valuesLines, fmt.Sprintf("    %s: %q", k, v))
			}
		}
	} else {
		valuesLines = append(valuesLines, "", "ingress:", "  enabled: false")
	}

	// Health check
	if dse.Spec.Deployment.HealthCheck != nil {
		hc := dse.Spec.Deployment.HealthCheck
		hcType := hc.Type
		if hcType == "" {
			hcType = "http"
		}
		path := hc.Path
		if path == "" {
			path = "/healthz"
		}
		valuesLines = append(valuesLines, "", "healthCheck:",
			fmt.Sprintf("  type: %s", hcType),
			fmt.Sprintf("  path: %s", path))
		if hc.InitialDelaySeconds != nil {
			valuesLines = append(valuesLines, fmt.Sprintf("  initialDelaySeconds: %d", *hc.InitialDelaySeconds))
		}
		if hc.PeriodSeconds != nil {
			valuesLines = append(valuesLines, fmt.Sprintf("  periodSeconds: %d", *hc.PeriodSeconds))
		}
	}

	// Resources
	if dse.Spec.Deployment.Resources != nil {
		r := dse.Spec.Deployment.Resources
		valuesLines = append(valuesLines, "", "resources:")
		if r.CPURequest != "" || r.MemoryRequest != "" {
			valuesLines = append(valuesLines, "  requests:")
			if r.CPURequest != "" {
				valuesLines = append(valuesLines, fmt.Sprintf("    cpu: %s", r.CPURequest))
			}
			if r.MemoryRequest != "" {
				valuesLines = append(valuesLines, fmt.Sprintf("    memory: %s", r.MemoryRequest))
			}
		}
		if r.CPULimit != "" || r.MemoryLimit != "" {
			valuesLines = append(valuesLines, "  limits:")
			if r.CPULimit != "" {
				valuesLines = append(valuesLines, fmt.Sprintf("    cpu: %s", r.CPULimit))
			}
			if r.MemoryLimit != "" {
				valuesLines = append(valuesLines, fmt.Sprintf("    memory: %s", r.MemoryLimit))
			}
		}
	}

	// Dependencies as values
	if len(dse.Spec.Dependencies) > 0 {
		valuesLines = append(valuesLines, "", "dependencies:")
		for _, dep := range dse.Spec.Dependencies {
			defaults := prodDepDefaultsMap[dep.Type]
			img := dep.Image
			if img == "" && defaults.Image != "" {
				version := dep.Version
				if version == "" {
					version = "latest"
				}
				img = defaults.Image + ":" + version
			}
			port := defaults.Port
			if dep.Port != nil {
				port = *dep.Port
			}
			valuesLines = append(valuesLines,
				fmt.Sprintf("  - type: %s", dep.Type),
				fmt.Sprintf("    image: %s", img),
				fmt.Sprintf("    port: %d", port))
		}
	}

	valuesLines = append(valuesLines, "")
	valuesYAML := strings.Join(valuesLines, "\n")

	// ── templates/deployment.yaml ────────────────────────────────
	deploymentTmpl := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
  labels:
    app.kubernetes.io/name: {{ .Release.Name }}
    app.kubernetes.io/managed-by: {{ .Release.Service }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .Release.Name }}
  template:
    metadata:
      labels:
        app.kubernetes.io/name: {{ .Release.Name }}
    spec:
      containers:
        - name: {{ .Release.Name }}
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - containerPort: {{ .Values.containerPort }}
              protocol: TCP
          {{- if .Values.healthCheck }}
          {{- if ne .Values.healthCheck.type "none" }}
          {{- if eq .Values.healthCheck.type "grpc" }}
          livenessProbe:
            grpc:
              port: {{ .Values.containerPort }}
            initialDelaySeconds: {{ .Values.healthCheck.initialDelaySeconds | default 5 }}
            periodSeconds: {{ .Values.healthCheck.periodSeconds | default 10 }}
          readinessProbe:
            grpc:
              port: {{ .Values.containerPort }}
            initialDelaySeconds: {{ .Values.healthCheck.initialDelaySeconds | default 5 }}
            periodSeconds: {{ .Values.healthCheck.periodSeconds | default 10 }}
          {{- else }}
          livenessProbe:
            httpGet:
              path: {{ .Values.healthCheck.path | default "/healthz" }}
              port: {{ .Values.containerPort }}
            initialDelaySeconds: {{ .Values.healthCheck.initialDelaySeconds | default 5 }}
            periodSeconds: {{ .Values.healthCheck.periodSeconds | default 10 }}
          readinessProbe:
            httpGet:
              path: {{ .Values.healthCheck.path | default "/healthz" }}
              port: {{ .Values.containerPort }}
            initialDelaySeconds: {{ .Values.healthCheck.initialDelaySeconds | default 5 }}
            periodSeconds: {{ .Values.healthCheck.periodSeconds | default 10 }}
          {{- end }}
          {{- end }}
          {{- end }}
          env:
`
	// Add dependency connection string env vars
	for _, dep := range dse.Spec.Dependencies {
		defaults := prodDepDefaultsMap[dep.Type]
		port := defaults.Port
		if dep.Port != nil {
			port = *dep.Port
		}
		envVar := dep.EnvVarName
		if envVar == "" {
			envVar = defaults.EnvVar
		}
		svcName := fmt.Sprintf("%s-%s", name, dep.Type)
		connStr := defaults.ConnStr(svcName, port)
		deploymentTmpl += fmt.Sprintf("            - name: %s\n              value: %q\n", envVar, connStr)
	}
	// Add user env vars
	for _, env := range dse.Spec.Deployment.Env {
		deploymentTmpl += fmt.Sprintf("            - name: %s\n              value: %q\n", env.Name, env.Value)
	}

	// Resources block
	deploymentTmpl += `          {{- if .Values.resources }}
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
          {{- end }}
`

	// ── templates/service.yaml ───────────────────────────────────
	serviceTmpl := `apiVersion: v1
kind: Service
metadata:
  name: {{ .Release.Name }}
  labels:
    app.kubernetes.io/name: {{ .Release.Name }}
spec:
  type: {{ .Values.service.type }}
  ports:
    - port: {{ .Values.service.port }}
      targetPort: {{ .Values.containerPort }}
      protocol: TCP
  selector:
    app.kubernetes.io/name: {{ .Release.Name }}
`

	// ── templates/ingress.yaml ──────────────────────────────────
	ingressTmpl := `{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Release.Name }}
  labels:
    app.kubernetes.io/name: {{ .Release.Name }}
  {{- if .Values.ingress.annotations }}
  annotations:
    {{- toYaml .Values.ingress.annotations | nindent 4 }}
  {{- end }}
spec:
  {{- if .Values.ingress.ingressClassName }}
  ingressClassName: {{ .Values.ingress.ingressClassName }}
  {{- end }}
  {{- if .Values.ingress.tls }}
  tls:
    - secretName: {{ .Values.ingress.tls.secretName }}
      hosts:
        - {{ .Values.ingress.host }}
  {{- end }}
  rules:
    - host: {{ .Values.ingress.host }}
      http:
        paths:
          - path: {{ .Values.ingress.path | default "/" }}
            pathType: {{ .Values.ingress.pathType | default "Prefix" }}
            backend:
              service:
                name: {{ .Release.Name }}
                port:
                  number: {{ .Values.service.port }}
{{- end }}
`

	// ── templates/dependencies.yaml ─────────────────────────────
	depTemplates := ""
	for _, dep := range dse.Spec.Dependencies {
		defaults := prodDepDefaultsMap[dep.Type]
		img := dep.Image
		if img == "" && defaults.Image != "" {
			version := dep.Version
			if version == "" {
				version = "latest"
			}
			img = defaults.Image + ":" + version
		}
		port := defaults.Port
		if dep.Port != nil {
			port = *dep.Port
		}
		depName := fmt.Sprintf("%s-%s", name, dep.Type)

		// Deployment for the dependency
		depTemplates += fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/component: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
    spec:
      containers:
        - name: %s
          image: %s
          ports:
            - containerPort: %d
`, depName, depName, dep.Type, depName, depName, dep.Type, img, port)

		// Add default env vars for common deps
		switch dep.Type {
		case "postgres":
			depTemplates += "          env:\n            - name: POSTGRES_DB\n              value: app\n            - name: POSTGRES_PASSWORD\n              value: postgres\n"
		case "mysql":
			depTemplates += "          env:\n            - name: MYSQL_DATABASE\n              value: app\n            - name: MYSQL_ROOT_PASSWORD\n              value: mysql\n"
		case "mongodb":
			// no env needed for default mongo
		case "elasticsearch":
			depTemplates += "          env:\n            - name: discovery.type\n              value: single-node\n            - name: xpack.security.enabled\n              value: \"false\"\n"
		case "minio":
			depTemplates += "          args:\n            - server\n            - /data\n"
		}
		// Custom env vars for the dependency
		for _, env := range dep.Env {
			depTemplates += fmt.Sprintf("            - name: %s\n              value: %q\n", env.Name, env.Value)
		}

		// Service for the dependency
		depTemplates += fmt.Sprintf(`---
apiVersion: v1
kind: Service
metadata:
  name: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/component: %s
spec:
  type: ClusterIP
  ports:
    - port: %d
      targetPort: %d
  selector:
    app.kubernetes.io/name: %s
`, depName, depName, dep.Type, port, port, depName)
	}

	// ── Write files ─────────────────────────────────────────────
	files := map[string]string{
		filepath.Join(chartDir, "Chart.yaml"):                   chartYAML,
		filepath.Join(chartDir, "values.yaml"):                  valuesYAML,
		filepath.Join(templatesDir, "deployment.yaml"):          deploymentTmpl,
		filepath.Join(templatesDir, "service.yaml"):             serviceTmpl,
		filepath.Join(templatesDir, "ingress.yaml"):             ingressTmpl,
	}
	if depTemplates != "" {
		files[filepath.Join(templatesDir, "dependencies.yaml")] = depTemplates
	}

	// Write a NOTES.txt
	notesTxt := fmt.Sprintf(`Deployed %s to production.

Check status:
  kubectl get pods -l app.kubernetes.io/name=%s
  kubectl get svc %s
  kubectl get ingress %s
`, name, name, name, name)
	files[filepath.Join(templatesDir, "NOTES.txt")] = notesTxt

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	return nil
}

// imageRepo extracts the repository from an image string like "ghcr.io/org/app:v1"
func imageRepo(image string) string {
	parts := strings.SplitN(image, ":", 2)
	return parts[0]
}

// imageTag extracts the tag from an image string, defaulting to "latest"
func imageTag(image string) string {
	parts := strings.SplitN(image, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return "latest"
}

// pushLocalImage re-tags a localhost:5001 image for a remote registry and pushes it.
func pushLocalImage(image, registry, tagOverride string) error {
	if !strings.HasPrefix(image, "localhost:5001/") {
		step("⏭️ ", fmt.Sprintf("Skipping non-local image: %s", image))
		return nil
	}

	nameTag := strings.TrimPrefix(image, "localhost:5001/")
	parts := strings.SplitN(nameTag, ":", 2)
	imgName := parts[0]
	tag := "latest"
	if len(parts) == 2 {
		tag = parts[1]
	}
	if tagOverride != "" {
		tag = tagOverride
	}

	remoteImg := fmt.Sprintf("%s/%s:%s", strings.TrimSuffix(registry, "/"), imgName, tag)

	step("🏷️ ", fmt.Sprintf("Tagging %s → %s", image, remoteImg))
	if err := run("docker", "tag", image, remoteImg); err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}

	step("⬆️ ", fmt.Sprintf("Pushing %s", remoteImg))
	if err := run("docker", "push", remoteImg); err != nil {
		return fmt.Errorf("docker push failed: %w", err)
	}

	return nil
}

// ── production tls ──────────────────────────────────────────────

var (
	prodTLSDomain       string
	prodTLSContext      string
	prodTLSEmail        string
	prodTLSIssuer       string
	prodTLSStaging      bool
	prodTLSDSEFile      string
	prodTLSIngressClass string
)

var productionTLSCmd = &cobra.Command{
	Use:   "tls",
	Short: "Configure TLS with cert-manager for production Ingress",
	Long: `Installs cert-manager (if not already present), creates a ClusterIssuer
for Let's Encrypt, and optionally patches a DSE YAML file to enable TLS on
its Ingress.

Examples:
  kindling production tls --context my-prod --domain app.example.com --email admin@example.com
  kindling production tls --context my-prod --domain app.example.com --staging
  kindling production tls --context my-prod --domain app.example.com -f dev-environment.yaml`,
	RunE: runProductionTLS,
}

func init() {
	productionTLSCmd.Flags().StringVar(&prodTLSContext, "context", "", "Kubeconfig context for the production cluster (required)")
	productionTLSCmd.Flags().StringVar(&prodTLSDomain, "domain", "", "Domain name for the TLS certificate (required)")
	productionTLSCmd.Flags().StringVar(&prodTLSEmail, "email", "", "Email for Let's Encrypt registration (required)")
	productionTLSCmd.Flags().StringVar(&prodTLSIssuer, "issuer", "letsencrypt-prod", "ClusterIssuer name")
	productionTLSCmd.Flags().BoolVar(&prodTLSStaging, "staging", false, "Use Let's Encrypt staging server (for testing)")
	productionTLSCmd.Flags().StringVarP(&prodTLSDSEFile, "file", "f", "", "Optional: DSE YAML to patch with TLS config")
	productionTLSCmd.Flags().StringVar(&prodTLSIngressClass, "ingress-class", "nginx", "IngressClass for the ACME solver")
	_ = productionTLSCmd.MarkFlagRequired("context")
	_ = productionTLSCmd.MarkFlagRequired("domain")
	_ = productionTLSCmd.MarkFlagRequired("email")
	productionCmd.AddCommand(productionTLSCmd)
}

func runProductionTLS(cmd *cobra.Command, args []string) error {
	ctx := prodTLSContext

	// Safety: refuse Kind contexts
	if strings.HasPrefix(ctx, "kind-") {
		return fmt.Errorf("context %q looks like a Kind cluster — use 'kindling expose' for local dev TLS", ctx)
	}

	header("TLS setup with cert-manager")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, ctx, colorReset))

	// ── Install cert-manager ────────────────────────────────────
	step("🔍", "Checking for cert-manager")
	_, err := runSilent("kubectl", "--context", ctx, "get", "namespace", "cert-manager")
	if err != nil {
		step("📦", "Installing cert-manager v1.17.1")
		certManagerURL := "https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml"
		if err := run("kubectl", "--context", ctx, "apply", "-f", certManagerURL); err != nil {
			return fmt.Errorf("cert-manager installation failed: %w", err)
		}

		step("⏳", "Waiting for cert-manager webhook to be ready")
		for i := 0; i < 30; i++ {
			_, err := runSilent("kubectl", "--context", ctx, "-n", "cert-manager",
				"rollout", "status", "deployment/cert-manager-webhook", "--timeout=5s")
			if err == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		success("cert-manager installed")
	} else {
		success("cert-manager already installed")
	}

	// ── Create ClusterIssuer ────────────────────────────────────
	acmeServer := "https://acme-v02.api.letsencrypt.org/directory"
	if prodTLSStaging {
		acmeServer = "https://acme-staging-v02.api.letsencrypt.org/directory"
		step("🧪", "Using Let's Encrypt staging server")
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
`, prodTLSIssuer, acmeServer, prodTLSEmail, prodTLSIssuer, prodTLSIngressClass)

	step("🔐", fmt.Sprintf("Creating ClusterIssuer %q", prodTLSIssuer))
	if err := runStdin(issuerYAML, "kubectl", "--context", ctx, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("ClusterIssuer creation failed: %w", err)
	}
	success("ClusterIssuer created")

	// ── Optionally patch a DSE file ─────────────────────────────
	if prodTLSDSEFile != "" {
		step("📝", fmt.Sprintf("Patching %s with TLS config", prodTLSDSEFile))
		if err := patchDSEWithTLS(prodTLSDSEFile, prodTLSDomain, prodTLSIssuer, prodTLSIngressClass); err != nil {
			return fmt.Errorf("failed to patch DSE: %w", err)
		}
		success(fmt.Sprintf("Updated %s with TLS config", prodTLSDSEFile))
		fmt.Println()
		fmt.Fprintf(os.Stderr, "  Deploy with: %skindling production deploy -f %s --context %s%s\n", colorCyan, prodTLSDSEFile, ctx, colorReset)
	}

	// ── Done ────────────────────────────────────────────────────
	fmt.Println()
	fmt.Fprintf(os.Stderr, "  %s🔒 TLS is configured!%s\n", colorGreen+colorBold, colorReset)
	fmt.Println()
	fmt.Println("  Your Ingress resources will get automatic TLS certificates from Let's Encrypt.")
	fmt.Println()
	fmt.Println("  To enable TLS on a DSE, add this to the ingress spec:")
	fmt.Println()
	fmt.Fprintf(os.Stderr, "    ingress:\n")
	fmt.Fprintf(os.Stderr, "      enabled: true\n")
	fmt.Fprintf(os.Stderr, "      host: %s\n", prodTLSDomain)
	fmt.Fprintf(os.Stderr, "      ingressClassName: %s\n", prodTLSIngressClass)
	fmt.Fprintf(os.Stderr, "      annotations:\n")
	fmt.Fprintf(os.Stderr, "        cert-manager.io/cluster-issuer: %s\n", prodTLSIssuer)
	fmt.Fprintf(os.Stderr, "      tls:\n")
	fmt.Fprintf(os.Stderr, "        secretName: %s-tls\n", strings.ReplaceAll(prodTLSDomain, ".", "-"))
	fmt.Fprintf(os.Stderr, "        hosts:\n")
	fmt.Fprintf(os.Stderr, "          - %s\n", prodTLSDomain)
	fmt.Println()

	return nil
}

// patchDSEWithTLS reads a DSE YAML file and adds/updates the ingress TLS section.
func patchDSEWithTLS(yamlFile, domain, issuer, ingressClass string) error {
	data, err := os.ReadFile(yamlFile)
	if err != nil {
		return err
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	var result []string

	secretName := strings.ReplaceAll(domain, ".", "-") + "-tls"
	ingressFound := false
	inTLS := false
	tlsInserted := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "ingress:" {
			ingressFound = true
		}

		if ingressFound && strings.HasPrefix(trimmed, "enabled:") {
			result = append(result, line)
			hasHost := false
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "host:") {
					hasHost = true
					break
				}
				if strings.TrimSpace(lines[j]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[j]), " ") {
					break
				}
			}
			if !hasHost {
				indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
				result = append(result, indent+"host: "+domain)
			}
			continue
		}

		if ingressFound && strings.HasPrefix(trimmed, "host:") {
			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
			result = append(result, indent+"host: "+domain)
			continue
		}

		if ingressFound && strings.HasPrefix(trimmed, "ingressClassName:") {
			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
			result = append(result, indent+"ingressClassName: "+ingressClass)
			continue
		}

		if ingressFound && trimmed == "tls:" {
			inTLS = true
		}

		if inTLS {
			if trimmed == "tls:" || strings.HasPrefix(trimmed, "secretName:") ||
				strings.HasPrefix(trimmed, "hosts:") || strings.HasPrefix(trimmed, "- ") {
				continue
			}
			inTLS = false
		}

		result = append(result, line)

		if ingressFound && !tlsInserted && (strings.HasPrefix(trimmed, "pathType:") ||
			strings.HasPrefix(trimmed, "path:") || strings.HasPrefix(trimmed, "host:")) {
			nextNonEmpty := ""
			for j := i + 1; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if t != "" {
					nextNonEmpty = t
					break
				}
			}
			if nextNonEmpty == "annotations:" || nextNonEmpty == "tls:" ||
				strings.HasPrefix(nextNonEmpty, "ingressClassName:") {
				continue
			}

			indent := strings.Repeat(" ", len(line)-len(strings.TrimLeft(line, " ")))
			result = append(result, indent+"ingressClassName: "+ingressClass)
			result = append(result, indent+"annotations:")
			result = append(result, indent+"  cert-manager.io/cluster-issuer: "+issuer)
			result = append(result, indent+"tls:")
			result = append(result, indent+"  secretName: "+secretName)
			result = append(result, indent+"  hosts:")
			result = append(result, indent+"    - "+domain)
			tlsInserted = true
		}
	}

	return os.WriteFile(yamlFile, []byte(strings.Join(result, "\n")), 0644)
}

// confirmPrompt asks the user for Y/n confirmation.
func confirmPrompt(question string) bool {
	fmt.Fprintf(os.Stderr, "  %s (Y/n): ", question)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "" || answer == "y" || answer == "yes"
	}
	return false
}
