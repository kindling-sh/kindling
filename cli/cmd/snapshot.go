package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Export a Helm chart or Kustomize overlay from the current cluster state",
	Long: `Reads all DevStagingEnvironments in the cluster and generates
production-ready Kubernetes manifests as a Helm chart or Kustomize overlay.

With --registry, images are copied from the Kind cluster's in-cluster
registry to your container registry via crane (no Docker daemon needed).

With --deploy, the generated chart is deployed to a production cluster
in one step. The --context flag is required to specify the target cluster
and --registry is required to make images accessible outside Kind.

Examples:
  kindling snapshot                          # Helm chart in ./kindling-snapshot/
  kindling snapshot --format kustomize       # Kustomize overlay
  kindling snapshot -o ./my-chart            # custom output directory
  kindling snapshot --name my-platform       # custom chart name
  kindling snapshot -r ghcr.io/myorg         # push images + ready-to-run chart
  kindling snapshot -r ghcr.io/myorg -t v1.0 # push with specific tag

  # Full graduation: snapshot + push images + deploy to production
  kindling snapshot -r ghcr.io/myorg --deploy --context my-prod-cluster
  kindling snapshot -r ghcr.io/myorg --deploy --context prod --namespace staging
  kindling snapshot -f kustomize -r ghcr.io/myorg --deploy --context prod`,
	RunE: runSnapshot,
}

var (
	snapshotFormat    string
	snapshotOutput    string
	snapshotName      string
	snapshotRegistry  string
	snapshotTag       string
	snapshotDeploy    bool
	snapshotContext   string
	snapshotNamespace string
)

func init() {
	snapshotCmd.Flags().StringVarP(&snapshotFormat, "format", "f", "helm", "Export format: helm or kustomize")
	snapshotCmd.Flags().StringVarP(&snapshotOutput, "output", "o", "", "Output directory (default: ./kindling-snapshot)")
	snapshotCmd.Flags().StringVarP(&snapshotName, "name", "n", "", "Chart/project name (default: derived from cluster)")
	snapshotCmd.Flags().StringVarP(&snapshotRegistry, "registry", "r", "", "Container registry (e.g. ghcr.io/myorg, 123456.dkr.ecr.us-east-1.amazonaws.com/myapp)")
	snapshotCmd.Flags().StringVarP(&snapshotTag, "tag", "t", "", "Image tag (default: git SHA or 'latest')")
	snapshotCmd.Flags().BoolVar(&snapshotDeploy, "deploy", false, "Deploy to a production cluster after generating the chart")
	snapshotCmd.Flags().StringVar(&snapshotContext, "context", "", "Kubeconfig context for the production cluster (required with --deploy)")
	snapshotCmd.Flags().StringVar(&snapshotNamespace, "namespace", "default", "Kubernetes namespace to deploy into (used with --deploy)")
	rootCmd.AddCommand(snapshotCmd)
}

// ── DSE reader ──────────────────────────────────────────────────

type snapshotDSE struct {
	Name     string
	Image    string
	Port     int
	Replicas int
	Env      []snapshotEnvVar
	Deps     []snapshotDep
	Ingress  *snapshotIngress
}

type snapshotEnvVar struct {
	Name  string
	Value string
}

type snapshotDep struct {
	Type    string
	Version string
	Port    int
}

type snapshotIngress struct {
	Enabled          bool
	Host             string
	Path             string
	PathType         string
	IngressClassName string
	TLSSecretName    string
}

func readClusterDSEs() ([]snapshotDSE, error) {
	out, err := core.Kubectl(clusterName, "get", "devstagingenvironments", "--all-namespaces", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("cannot read DSEs: %s", out)
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
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
					Type    string `json:"type"`
					Version string `json:"version,omitempty"`
					Port    *int   `json:"port,omitempty"`
				} `json:"dependencies"`
				Ingress *struct {
					Enabled          bool              `json:"enabled"`
					Host             string            `json:"host,omitempty"`
					Path             string            `json:"path,omitempty"`
					PathType         string            `json:"pathType,omitempty"`
					IngressClassName *string           `json:"ingressClassName,omitempty"`
					TLS              *struct {
						SecretName string `json:"secretName"`
					} `json:"tls,omitempty"`
					Annotations map[string]string `json:"annotations,omitempty"`
				} `json:"ingress,omitempty"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("cannot parse DSE list: %w", err)
	}

	var dses []snapshotDSE
	for _, item := range list.Items {
		replicas := 1
		if item.Spec.Deployment.Replicas != nil {
			replicas = *item.Spec.Deployment.Replicas
		}
		d := snapshotDSE{
			Name:     item.Metadata.Name,
			Image:    item.Spec.Deployment.Image,
			Port:     item.Spec.Deployment.Port,
			Replicas: replicas,
		}
		for _, e := range item.Spec.Deployment.Env {
			d.Env = append(d.Env, snapshotEnvVar{Name: e.Name, Value: e.Value})
		}
		for _, dep := range item.Spec.Dependencies {
			port := 0
			if dep.Port != nil {
				port = *dep.Port
			}
			d.Deps = append(d.Deps, snapshotDep{
				Type:    dep.Type,
				Version: dep.Version,
				Port:    port,
			})
		}
		dses = append(dses, d)
	}

	// Populate ingress from parsed spec
	for i, item := range list.Items {
		if item.Spec.Ingress != nil && item.Spec.Ingress.Enabled {
			ing := &snapshotIngress{
				Enabled:  true,
				Host:     item.Spec.Ingress.Host,
				Path:     item.Spec.Ingress.Path,
				PathType: item.Spec.Ingress.PathType,
			}
			if ing.Path == "" {
				ing.Path = "/"
			}
			if ing.PathType == "" {
				ing.PathType = "Prefix"
			}
			if item.Spec.Ingress.IngressClassName != nil {
				ing.IngressClassName = *item.Spec.Ingress.IngressClassName
			}
			if item.Spec.Ingress.TLS != nil {
				ing.TLSSecretName = item.Spec.Ingress.TLS.SecretName
			}
			dses[i].Ingress = ing
		}
	}

	return dses, nil
}

// detectUserPrefix finds the GitHub actor prefix (e.g. "jeff-vincent-")
// that is common to the majority of DSE names. The CI workflow names
// every DSE as "${{ github.actor }}-<service-name>", so this prefix
// must be stripped to produce clean chart names like "gateway" instead
// of "jeff-vincent-gateway".
// Returns the prefix including trailing dash, or "" if none detected.
func detectUserPrefix(dses []snapshotDSE) string {
	if len(dses) < 2 {
		return ""
	}

	// Count how many names share each possible dash-delimited prefix.
	counts := make(map[string]int)
	for _, d := range dses {
		parts := strings.Split(d.Name, "-")
		// Try each prefix length, leaving at least 1 segment as the service name.
		for pLen := 1; pLen < len(parts); pLen++ {
			prefix := strings.Join(parts[:pLen], "-") + "-"
			counts[prefix]++
		}
	}

	// Pick the longest prefix shared by the most names (minimum 2,
	// must cover more than half the DSEs to qualify as the user prefix).
	var best string
	bestCount := 0
	for prefix, count := range counts {
		if count < 2 {
			continue
		}
		if count > bestCount || (count == bestCount && len(prefix) > len(best)) {
			best = prefix
			bestCount = count
		}
	}

	if bestCount > len(dses)/2 {
		return best
	}
	return ""
}

// ── Dependency defaults (mirrors operator registry) ─────────────

type depDefaults struct {
	Image      string
	Port       int
	EnvVarName string
	Env        []snapshotEnvVar
}

var depRegistry = map[string]depDefaults{
	"postgres":      {Image: "postgres", Port: 5432, EnvVarName: "DATABASE_URL", Env: []snapshotEnvVar{{Name: "POSTGRES_USER", Value: "devuser"}, {Name: "POSTGRES_PASSWORD", Value: "devpass"}, {Name: "POSTGRES_DB", Value: "devdb"}}},
	"redis":         {Image: "redis", Port: 6379, EnvVarName: "REDIS_URL"},
	"mysql":         {Image: "mysql", Port: 3306, EnvVarName: "DATABASE_URL", Env: []snapshotEnvVar{{Name: "MYSQL_ROOT_PASSWORD", Value: "devpass"}, {Name: "MYSQL_DATABASE", Value: "devdb"}, {Name: "MYSQL_USER", Value: "devuser"}, {Name: "MYSQL_PASSWORD", Value: "devpass"}}},
	"mongodb":       {Image: "mongo", Port: 27017, EnvVarName: "MONGO_URL", Env: []snapshotEnvVar{{Name: "MONGO_INITDB_ROOT_USERNAME", Value: "devuser"}, {Name: "MONGO_INITDB_ROOT_PASSWORD", Value: "devpass"}}},
	"rabbitmq":      {Image: "rabbitmq", Port: 5672, EnvVarName: "AMQP_URL", Env: []snapshotEnvVar{{Name: "RABBITMQ_DEFAULT_USER", Value: "devuser"}, {Name: "RABBITMQ_DEFAULT_PASS", Value: "devpass"}}},
	"minio":         {Image: "minio/minio", Port: 9000, EnvVarName: "S3_ENDPOINT", Env: []snapshotEnvVar{{Name: "MINIO_ROOT_USER", Value: "minioadmin"}, {Name: "MINIO_ROOT_PASSWORD", Value: "minioadmin"}}},
	"elasticsearch": {Image: "docker.elastic.co/elasticsearch/elasticsearch", Port: 9200, EnvVarName: "ELASTICSEARCH_URL", Env: []snapshotEnvVar{{Name: "discovery.type", Value: "single-node"}, {Name: "xpack.security.enabled", Value: "false"}}},
	"kafka":         {Image: "apache/kafka", Port: 9092, EnvVarName: "KAFKA_BROKER_URL"},
	"nats":          {Image: "nats", Port: 4222, EnvVarName: "NATS_URL"},
	"memcached":     {Image: "memcached", Port: 11211, EnvVarName: "MEMCACHED_URL"},
}

// ── Main command ────────────────────────────────────────────────

func runSnapshot(cmd *cobra.Command, args []string) error {
	// ── Validate --deploy prerequisites ────────────────────────
	if snapshotDeploy {
		if snapshotContext == "" {
			return fmt.Errorf("--context is required when using --deploy")
		}
		if strings.HasPrefix(snapshotContext, "kind-") {
			return fmt.Errorf("context %q looks like a Kind cluster — use 'kindling deploy' for local dev", snapshotContext)
		}
		if snapshotRegistry == "" {
			return fmt.Errorf("--registry is required when using --deploy (images must be accessible from the production cluster)")
		}
	}

	header("Exporting cluster snapshot")

	step("📡", "Reading DevStagingEnvironments from cluster")
	dses, err := readClusterDSEs()
	if err != nil {
		return err
	}
	if len(dses) == 0 {
		warn("No DevStagingEnvironments found in cluster — nothing to export")
		return nil
	}
	success(fmt.Sprintf("Found %d service(s)", len(dses)))

	// Strip GitHub actor prefix (e.g. "jeff-vincent-gateway" → "gateway")
	var userPrefix string
	if prefix := detectUserPrefix(dses); prefix != "" {
		userPrefix = prefix
		step("✂️", fmt.Sprintf("Stripping user prefix %q from service names", strings.TrimSuffix(prefix, "-")))
		for i := range dses {
			if stripped := strings.TrimPrefix(dses[i].Name, prefix); stripped != "" {
				dses[i].Name = stripped
			}
			// Also strip prefix from ingress host
			if dses[i].Ingress != nil && dses[i].Ingress.Host != "" {
				dses[i].Ingress.Host = strings.TrimPrefix(dses[i].Ingress.Host, prefix)
			}
			// Strip prefix from env var values (e.g. service URLs like
			// "http://jeff-vincent-orders:5000" → "http://orders:5000")
			for j := range dses[i].Env {
				dses[i].Env[j].Value = strings.ReplaceAll(dses[i].Env[j].Value, prefix, "")
			}
		}
	}

	chartName := snapshotName
	if chartName == "" {
		chartName = "kindling-snapshot"
	}

	outDir := snapshotOutput
	if outDir == "" {
		outDir = "./" + chartName
	}
	outDir, _ = filepath.Abs(outDir)

	// ── Registry: re-tag and push images ────────────────────────
	if snapshotRegistry != "" {
		tag := snapshotTag
		if tag == "" {
			tag = detectNextTag(snapshotRegistry, dses[0].Name)
		}
		step("🏷", fmt.Sprintf("Re-tagging images → %s (tag: %s)", snapshotRegistry, tag))

		if err := craneCopyImages(dses, snapshotRegistry, tag, userPrefix); err != nil {
			warn(fmt.Sprintf("Could not push images: %v", err))
			warn("Falling back to image references only (no push)")
			// Still rewrite image refs so the chart targets the right registry
			for i := range dses {
				dses[i].Image = registryImage(dses[i].Name, snapshotRegistry, tag)
			}
		}
	}

	var exportErr error
	switch snapshotFormat {
	case "helm":
		exportErr = exportHelm(outDir, chartName, dses)
	case "kustomize":
		exportErr = exportKustomize(outDir, chartName, dses)
	default:
		return fmt.Errorf("unknown format %q — use 'helm' or 'kustomize'", snapshotFormat)
	}
	if exportErr != nil {
		return exportErr
	}

	// ── Deploy to production cluster ───────────────────────────
	if !snapshotDeploy {
		return nil
	}

	header("Deploying to production")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, snapshotContext, colorReset))

	// Verify cluster connectivity
	step("🔍", "Verifying cluster connectivity")
	if err := run("kubectl", "cluster-info", "--context", snapshotContext); err != nil {
		return fmt.Errorf("cannot reach cluster via context %q: %w", snapshotContext, err)
	}

	switch snapshotFormat {
	case "helm":
		if !commandExists("helm") {
			return fmt.Errorf("helm not found on PATH — install it or deploy manually")
		}
		step("🚀", fmt.Sprintf("Running helm upgrade --install %s", chartName))
		helmArgs := []string{
			"upgrade", "--install", chartName, outDir,
			"--kube-context", snapshotContext,
			"--namespace", snapshotNamespace,
			"--create-namespace",
			"-f", filepath.Join(outDir, "values-live.yaml"),
			"--wait",
			"--timeout", "5m",
		}
		if err := run("helm", helmArgs...); err != nil {
			return fmt.Errorf("helm deploy failed: %w", err)
		}
	case "kustomize":
		step("🚀", fmt.Sprintf("Running kubectl apply -k %s", outDir))
		if err := run("kubectl", "--context", snapshotContext, "apply",
			"-k", outDir,
			"-n", snapshotNamespace); err != nil {
			return fmt.Errorf("kustomize deploy failed: %w", err)
		}
	}

	success("Deployed to production")
	fmt.Println()
	fmt.Printf("  Check pods:     %skubectl --context %s -n %s get pods%s\n", colorCyan, snapshotContext, snapshotNamespace, colorReset)
	fmt.Printf("  Check services: %skubectl --context %s -n %s get svc%s\n", colorCyan, snapshotContext, snapshotNamespace, colorReset)
	fmt.Printf("  Check ingress:  %skubectl --context %s -n %s get ingress%s\n", colorCyan, snapshotContext, snapshotNamespace, colorReset)
	fmt.Println()
	return nil
}

// ════════════════════════════════════════════════════════════════
// Helm export
// ════════════════════════════════════════════════════════════════

func exportHelm(outDir, chartName string, dses []snapshotDSE) error {
	step("⎈", "Generating Helm chart")

	templatesDir := filepath.Join(outDir, "templates")
	os.MkdirAll(templatesDir, 0755)

	// Collect unique deps across all DSEs
	depsSeen := make(map[string]bool)
	for _, dse := range dses {
		for _, dep := range dse.Deps {
			depsSeen[dep.Type] = true
		}
	}

	// ── Chart.yaml ──────────────────────────────────────────────
	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s
description: Exported from kindling cluster snapshot
version: 0.1.0
appVersion: "1.0.0"
type: application
`, chartName)
	writeSnapshotFile(outDir, "Chart.yaml", chartYAML)

	// ── .helmignore ─────────────────────────────────────────────
	// Prevents Helm from packaging unrelated project files when the
	// chart is generated inside an existing source tree (e.g. -o .).
	helmIgnore := `# Generated by kindling snapshot
.git
node_modules
__pycache__
*.pyc
.env
.venv
vendor
dist
build
tmp
.DS_Store
*.swp
*.swo
`
	writeSnapshotFile(outDir, ".helmignore", helmIgnore)

	// ── values.yaml (clean chart with commented examples) ───────
	valuesYAML := buildValuesYAML(chartName, dses, depsSeen, false)
	writeSnapshotFile(outDir, "values.yaml", valuesYAML)

	// ── values-live.yaml (populated from current cluster) ───────
	liveYAML := buildValuesYAML(chartName, dses, depsSeen, true)
	writeSnapshotFile(outDir, "values-live.yaml", liveYAML)

	// ── Templates: service deployments ──────────────────────────
	for _, dse := range dses {
		safe := helmSafe(dse.Name)
		writeSnapshotFile(templatesDir, safe+"-deployment.yaml", helmDeploymentTemplate(dse, chartName, dses))
		writeSnapshotFile(templatesDir, safe+"-service.yaml", helmServiceTemplate(dse, chartName))
		if dse.Ingress != nil && dse.Ingress.Enabled {
			writeSnapshotFile(templatesDir, safe+"-ingress.yaml", helmIngressTemplate(dse, chartName))
		}
	}

	// ── Templates: dependency deployments ───────────────────────
	for depType := range depsSeen {
		def, ok := depRegistry[depType]
		if !ok {
			continue
		}
		safe := helmSafe(depType)
		writeSnapshotFile(templatesDir, safe+"-deployment.yaml", helmDepDeploymentTemplate(depType, def))
		writeSnapshotFile(templatesDir, safe+"-service.yaml", helmDepServiceTemplate(depType, def))
	}

	// ── _helpers.tpl ────────────────────────────────────────────
	helpers := fmt.Sprintf(`{{/*
Common labels
*/}}
{{- define "%s.labels" -}}
app.kubernetes.io/managed-by: helm
app.kubernetes.io/part-of: %s
{{- end }}
`, chartName, chartName)
	writeSnapshotFile(templatesDir, "_helpers.tpl", helpers)

	success(fmt.Sprintf("Helm chart written to %s", outDir))
	fmt.Println()
	fmt.Printf("  Install with:  %shelm install %s %s%s\n", colorCyan, chartName, outDir, colorReset)
	fmt.Printf("  Dry-run:       %shelm template %s %s%s\n", colorCyan, chartName, outDir, colorReset)
	fmt.Printf("  Live values:   %shelm install %s %s -f values-live.yaml%s\n", colorCyan, chartName, outDir, colorReset)
	fmt.Println()
	return nil
}

// buildValuesYAML generates either a clean values.yaml with commented examples (live=false)
// or a fully-populated values-live.yaml with actual running values (live=true).
func buildValuesYAML(chartName string, dses []snapshotDSE, depsSeen map[string]bool, live bool) string {
	var buf strings.Builder

	if live {
		buf.WriteString("# Generated by kindling snapshot — LIVE VALUES\n")
		buf.WriteString("# These are the actual values from your running cluster.\n")
		buf.WriteString("# Install with: helm install <release> ./chart -f values-live.yaml\n\n")
	} else {
		buf.WriteString("# Generated by kindling snapshot\n")
		buf.WriteString("#\n")
		buf.WriteString("# Lines marked with ← are the values currently running in your\n")
		buf.WriteString("# kindling dev cluster. Replace the defaults below with your\n")
		buf.WriteString("# production values, or use values-live.yaml as a starting point.\n\n")
	}

	// ── Service values ──────────────────────────────────────────
	for _, dse := range dses {
		vk := helmValuesKey(dse.Name)
		prodImage := productionImageClean(dse.Image, dse.Name)
		liveImage := dse.Image

		buf.WriteString(fmt.Sprintf("%s:\n", vk))

		if live {
			buf.WriteString(fmt.Sprintf("  image: \"%s\"\n", liveImage))
		} else {
			buf.WriteString(fmt.Sprintf("  image: \"%s\"", prodImage))
			if liveImage != prodImage {
				buf.WriteString(fmt.Sprintf("  # ← currently: %s", liveImage))
			}
			buf.WriteString("\n")
		}

		buf.WriteString(fmt.Sprintf("  port: %d\n", dse.Port))
		buf.WriteString(fmt.Sprintf("  replicas: %d\n", dse.Replicas))

		// Env vars — user-defined + dependency connection strings
		hasEnv := len(dse.Env) > 0 || len(dse.Deps) > 0
		if hasEnv {
			buf.WriteString("  env:\n")
			// User-defined env vars
			for _, e := range dse.Env {
				if live {
					buf.WriteString(fmt.Sprintf("    %s: \"%s\"\n", e.Name, e.Value))
				} else {
					buf.WriteString(fmt.Sprintf("    %s: \"%s\"  # ← live value\n", e.Name, e.Value))
				}
			}
			// Dependency connection strings — real configurable values
			for _, dep := range dse.Deps {
				if def, ok := depRegistry[dep.Type]; ok {
					if live {
						buf.WriteString(fmt.Sprintf("    %s: \"%s\"\n", def.EnvVarName,
							buildExampleConnectionURL(dep.Type, helmSafe(dep.Type), def)))
					} else {
						buf.WriteString(fmt.Sprintf("    %s: \"\"  # TODO: set your production %s connection string\n",
							def.EnvVarName, dep.Type))
					}
				}
			}
		}

		// Ingress config
		if dse.Ingress != nil && dse.Ingress.Enabled {
			buf.WriteString("  ingress:\n")
			buf.WriteString("    enabled: true\n")
			if live {
				buf.WriteString(fmt.Sprintf("    host: \"%s\"\n", dse.Ingress.Host))
			} else {
				buf.WriteString(fmt.Sprintf("    host: \"\"  # TODO: set your production hostname (dev: %s)\n", dse.Ingress.Host))
			}
			buf.WriteString(fmt.Sprintf("    path: \"%s\"\n", dse.Ingress.Path))
			buf.WriteString(fmt.Sprintf("    pathType: \"%s\"\n", dse.Ingress.PathType))
			if dse.Ingress.IngressClassName != "" {
				buf.WriteString(fmt.Sprintf("    ingressClassName: \"%s\"\n", dse.Ingress.IngressClassName))
			}
			if dse.Ingress.TLSSecretName != "" {
				buf.WriteString("    tls:\n")
				buf.WriteString(fmt.Sprintf("      secretName: \"%s\"\n", dse.Ingress.TLSSecretName))
			}
			buf.WriteString("    annotations: {}\n")
		}

		buf.WriteString("\n")
	}

	// ── Dependency values ───────────────────────────────────────
	for depType := range depsSeen {
		def, ok := depRegistry[depType]
		if !ok {
			continue
		}
		safe := helmSafe(depType)
		vk := helmValuesKey(depType)

		// Find version from the first DSE that references this dep
		version := "latest"
		for _, dse := range dses {
			for _, d := range dse.Deps {
				if d.Type == depType && d.Version != "" {
					version = d.Version
					break
				}
			}
		}

		buf.WriteString(fmt.Sprintf("%s:\n", vk))
		buf.WriteString("  enabled: true\n")

		imageStr := fmt.Sprintf("%s:%s", def.Image, version)
		if live {
			buf.WriteString(fmt.Sprintf("  image: \"%s\"\n", imageStr))
		} else {
			buf.WriteString(fmt.Sprintf("  image: \"%s\"  # ← dev version\n", imageStr))
		}

		buf.WriteString(fmt.Sprintf("  port: %d\n", def.Port))

		if len(def.Env) > 0 {
			buf.WriteString("  env:\n")
			for _, e := range def.Env {
				if live {
					buf.WriteString(fmt.Sprintf("    %s: \"%s\"\n", e.Name, e.Value))
				} else {
					buf.WriteString(fmt.Sprintf("    %s: \"%s\"  # ← dev default\n", e.Name, e.Value))
				}
			}
		}

		// Connection string example
		if !live {
			buf.WriteString(fmt.Sprintf("  # Connection: %s\n",
				buildExampleConnectionURL(depType, safe, def)))
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

// buildExampleConnectionURL generates a sample connection string for a dependency type
// using the Helm release template format, mirroring the operator's buildConnectionURL.
func buildExampleConnectionURL(depType, safe string, def depDefaults) string {
	host := fmt.Sprintf("<release>-%s", safe)
	switch depType {
	case "postgres":
		user, pass, db := "devuser", "devpass", "devdb"
		for _, e := range def.Env {
			switch e.Name {
			case "POSTGRES_USER":
				user = e.Value
			case "POSTGRES_PASSWORD":
				pass = e.Value
			case "POSTGRES_DB":
				db = e.Value
			}
		}
		return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, pass, host, def.Port, db)
	case "redis":
		return fmt.Sprintf("redis://%s:%d/0", host, def.Port)
	case "mysql":
		user, pass, db := "devuser", "devpass", "devdb"
		for _, e := range def.Env {
			switch e.Name {
			case "MYSQL_USER":
				user = e.Value
			case "MYSQL_PASSWORD":
				pass = e.Value
			case "MYSQL_DATABASE":
				db = e.Value
			}
		}
		return fmt.Sprintf("mysql://%s:%s@%s:%d/%s", user, pass, host, def.Port, db)
	case "mongodb":
		user, pass := "devuser", "devpass"
		for _, e := range def.Env {
			switch e.Name {
			case "MONGO_INITDB_ROOT_USERNAME":
				user = e.Value
			case "MONGO_INITDB_ROOT_PASSWORD":
				pass = e.Value
			}
		}
		return fmt.Sprintf("mongodb://%s:%s@%s:%d", user, pass, host, def.Port)
	case "rabbitmq":
		user, pass := "devuser", "devpass"
		for _, e := range def.Env {
			switch e.Name {
			case "RABBITMQ_DEFAULT_USER":
				user = e.Value
			case "RABBITMQ_DEFAULT_PASS":
				pass = e.Value
			}
		}
		return fmt.Sprintf("amqp://%s:%s@%s:%d/", user, pass, host, def.Port)
	case "minio":
		return fmt.Sprintf("http://%s:%d", host, def.Port)
	case "elasticsearch":
		return fmt.Sprintf("http://%s:%d", host, def.Port)
	case "kafka":
		return fmt.Sprintf("%s:%d", host, def.Port)
	case "nats":
		return fmt.Sprintf("nats://%s:%d", host, def.Port)
	case "memcached":
		return fmt.Sprintf("%s:%d", host, def.Port)
	default:
		return fmt.Sprintf("%s:%d", host, def.Port)
	}
}

// productionImageClean is like productionImage but without the trailing comment.
func productionImageClean(image, name string) string {
	if strings.HasPrefix(image, "localhost:5001/") {
		return name + ":latest"
	}
	if !strings.Contains(image, "/") && !strings.Contains(image, ":latest") {
		return name + ":latest"
	}
	return image
}

func helmDeploymentTemplate(dse snapshotDSE, chartName string, allDSEs []snapshotDSE) string {
	safe := helmSafe(dse.Name)
	vk := helmValuesKey(dse.Name)

	// Build a set of known service names for rewriting env var values
	knownServices := make(map[string]bool)
	for _, d := range allDSEs {
		knownServices[helmSafe(d.Name)] = true
	}

	// Build env block — connection strings from deps + user env
	var envLines strings.Builder
	// Dep connection strings — now sourced from values.yaml so users can
	// set their production URLs without editing templates.
	for _, dep := range dse.Deps {
		if def, ok := depRegistry[dep.Type]; ok {
			envLines.WriteString(fmt.Sprintf(`        {{- if .Values.%s.env.%s }}
        - name: %s
          value: {{ .Values.%s.env.%s | quote }}
        {{- end }}
`, vk, def.EnvVarName, def.EnvVarName, vk, def.EnvVarName))
		}
	}
	// User-defined env vars — if the value references a sibling service,
	// generate a Helm template expression so the URL uses the release name.
	// Otherwise source from values.yaml.
	if len(dse.Env) > 0 {
		for _, e := range dse.Env {
			if helmVal := rewriteServiceURL(e.Value, knownServices); helmVal != "" {
				// Directly embed the Helm-templated value
				envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: %s\n", e.Name, helmVal))
			} else {
				envLines.WriteString(fmt.Sprintf(`        {{- if .Values.%s.env.%s }}
        - name: %s
          value: {{ .Values.%s.env.%s | quote }}
        {{- end }}
`, vk, e.Name, e.Name, vk, e.Name))
			}
		}
	}

	envSection := ""
	if envLines.Len() > 0 {
		envSection = fmt.Sprintf("        env:\n%s", envLines.String())
	}

	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-%s
  labels:
    app: %s
    {{- include "%s.labels" . | nindent 4 }}
spec:
  replicas: {{ .Values.%s.replicas }}
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: {{ .Values.%s.image }}
        imagePullPolicy: Always
        ports:
        - containerPort: {{ .Values.%s.port }}
%s`, safe, safe, chartName, vk, safe, safe, safe, vk, vk, envSection)
}

func helmServiceTemplate(dse snapshotDSE, chartName string) string {
	safe := helmSafe(dse.Name)
	vk := helmValuesKey(dse.Name)
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: {{ .Release.Name }}-%s
  labels:
    app: %s
    {{- include "%s.labels" . | nindent 4 }}
spec:
  selector:
    app: %s
  ports:
  - port: {{ .Values.%s.port }}
    targetPort: {{ .Values.%s.port }}
    protocol: TCP
`, safe, safe, chartName, safe, vk, vk)
}

func helmIngressTemplate(dse snapshotDSE, chartName string) string {
	safe := helmSafe(dse.Name)
	vk := helmValuesKey(dse.Name)

	// Build TLS block if configured
	var tlsBlock string
	if dse.Ingress.TLSSecretName != "" {
		tlsBlock = fmt.Sprintf(`
  {{- if .Values.%s.ingress.tls.secretName }}
  tls:
  - secretName: {{ .Values.%s.ingress.tls.secretName }}
    hosts:
    - {{ .Values.%s.ingress.host }}
  {{- end }}`, vk, vk, vk)
	}

	return fmt.Sprintf(`{{- if .Values.%s.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Release.Name }}-%s
  labels:
    app: %s
    {{- include "%s.labels" . | nindent 4 }}
  {{- with .Values.%s.ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- if .Values.%s.ingress.ingressClassName }}
  ingressClassName: {{ .Values.%s.ingress.ingressClassName }}
  {{- end }}%s
  rules:
  - host: {{ .Values.%s.ingress.host }}
    http:
      paths:
      - path: {{ .Values.%s.ingress.path }}
        pathType: {{ .Values.%s.ingress.pathType }}
        backend:
          service:
            name: {{ .Release.Name }}-%s
            port:
              number: {{ .Values.%s.port }}
{{- end }}
`, vk, safe, safe, chartName, vk, vk, vk, tlsBlock, vk, vk, vk, safe, vk)
}

func helmDepDeploymentTemplate(depType string, def depDefaults) string {
	safe := helmSafe(depType)
	vk := helmValuesKey(depType)

	var envLines strings.Builder
	if len(def.Env) > 0 {
		envLines.WriteString("        env:\n")
		for _, e := range def.Env {
			envLines.WriteString(fmt.Sprintf(`        - name: %s
          value: {{ .Values.%s.env.%s | quote }}
`, e.Name, vk, e.Name))
		}
	}

	return fmt.Sprintf(`{{- if .Values.%s.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-%s
  labels:
    app: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: {{ .Values.%s.image }}
        imagePullPolicy: Always
        ports:
        - containerPort: {{ .Values.%s.port }}
%s{{- end }}
`, vk, safe, safe, safe, safe, safe, vk, vk, envLines.String())
}

func helmDepServiceTemplate(depType string, def depDefaults) string {
	safe := helmSafe(depType)
	vk := helmValuesKey(depType)
	return fmt.Sprintf(`{{- if .Values.%s.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Release.Name }}-%s
  labels:
    app: %s
spec:
  selector:
    app: %s
  ports:
  - port: {{ .Values.%s.port }}
    targetPort: {{ .Values.%s.port }}
    protocol: TCP
{{- end }}
`, vk, safe, safe, safe, vk, vk)
}

// ════════════════════════════════════════════════════════════════
// Kustomize export
// ════════════════════════════════════════════════════════════════

func exportKustomize(outDir, name string, dses []snapshotDSE) error {
	step("📦", "Generating Kustomize overlay")

	baseDir := filepath.Join(outDir, "base")
	os.MkdirAll(baseDir, 0755)

	var resources []string
	depsSeen := make(map[string]bool)

	// ── Service manifests ───────────────────────────────────────
	for _, dse := range dses {
		safe := helmSafe(dse.Name)
		depYAML := kustomizeDeployment(dse)
		svcYAML := kustomizeService(dse)
		writeSnapshotFile(baseDir, safe+"-deployment.yaml", depYAML)
		writeSnapshotFile(baseDir, safe+"-service.yaml", svcYAML)
		resources = append(resources, safe+"-deployment.yaml", safe+"-service.yaml")

		if dse.Ingress != nil && dse.Ingress.Enabled {
			ingYAML := kustomizeIngress(dse)
			writeSnapshotFile(baseDir, safe+"-ingress.yaml", ingYAML)
			resources = append(resources, safe+"-ingress.yaml")
		}

		for _, dep := range dse.Deps {
			if !depsSeen[dep.Type] {
				depsSeen[dep.Type] = true
			}
		}
	}

	// ── Dependency manifests ────────────────────────────────────
	for depType := range depsSeen {
		def, ok := depRegistry[depType]
		if !ok {
			continue
		}
		safe := helmSafe(depType)
		// Find version from DSEs
		version := "latest"
		for _, dse := range dses {
			for _, d := range dse.Deps {
				if d.Type == depType && d.Version != "" {
					version = d.Version
					break
				}
			}
		}
		writeSnapshotFile(baseDir, safe+"-deployment.yaml", kustomizeDepDeployment(depType, def, version))
		writeSnapshotFile(baseDir, safe+"-service.yaml", kustomizeDepService(depType, def))
		resources = append(resources, safe+"-deployment.yaml", safe+"-service.yaml")
	}

	// ── kustomization.yaml ──────────────────────────────────────
	var kBuf strings.Builder
	kBuf.WriteString("# Generated by kindling snapshot\n")
	kBuf.WriteString("apiVersion: kustomize.config.k8s.io/v1beta1\n")
	kBuf.WriteString("kind: Kustomization\n\n")
	kBuf.WriteString(fmt.Sprintf("namePrefix: %s-\n\n", name))
	kBuf.WriteString("commonLabels:\n")
	kBuf.WriteString(fmt.Sprintf("  app.kubernetes.io/part-of: %s\n\n", name))
	kBuf.WriteString("resources:\n")
	// Deduplicate resources (deps may overwrite files)
	seen := make(map[string]bool)
	for _, r := range resources {
		if !seen[r] {
			kBuf.WriteString(fmt.Sprintf("  - %s\n", r))
			seen[r] = true
		}
	}
	writeSnapshotFile(baseDir, "kustomization.yaml", kBuf.String())

	// ── Top-level kustomization pointing to base ────────────────
	topKustomize := fmt.Sprintf(`# Generated by kindling snapshot
# Use this overlay to customize for different environments.
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - base
`)
	writeSnapshotFile(outDir, "kustomization.yaml", topKustomize)

	success(fmt.Sprintf("Kustomize overlay written to %s", outDir))
	fmt.Println()
	fmt.Printf("  Preview:  %skubectl kustomize %s%s\n", colorCyan, outDir, colorReset)
	fmt.Printf("  Apply:    %skubectl apply -k %s%s\n", colorCyan, outDir, colorReset)
	fmt.Println()
	return nil
}

func kustomizeDeployment(dse snapshotDSE) string {
	// Build set of known service names for detecting service URLs
	// (note: in kustomize path we don't have allDSEs, but the env values
	// have already been prefix-stripped, so we just add a TODO comment)
	var envLines strings.Builder
	// Dependency connection strings
	for _, dep := range dse.Deps {
		if def, ok := depRegistry[dep.Type]; ok {
			envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: \"\"  # TODO: set your production %s connection string\n",
				def.EnvVarName, dep.Type))
		}
	}
	// User env — note: namePrefix will rename services, so URLs referencing
	// sibling services need updating to include the namePrefix.
	for _, e := range dse.Env {
		envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: \"%s\"  # TODO: update hostname if namePrefix changes service names\n", e.Name, e.Value))
	}

	envSection := ""
	if envLines.Len() > 0 {
		envSection = fmt.Sprintf("        env:\n%s", envLines.String())
	}

	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: %s
        imagePullPolicy: Always
        ports:
        - containerPort: %d
%s`, dse.Name, dse.Replicas, dse.Name, dse.Name, dse.Name,
		productionImage(dse.Image, dse.Name), dse.Port, envSection)
}

func kustomizeService(dse snapshotDSE) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
  - port: %d
    targetPort: %d
    protocol: TCP
`, dse.Name, dse.Name, dse.Port, dse.Port)
}

func kustomizeIngress(dse snapshotDSE) string {
	ing := dse.Ingress

	var classLine string
	if ing.IngressClassName != "" {
		classLine = fmt.Sprintf("  ingressClassName: %s\n", ing.IngressClassName)
	}

	var tlsBlock string
	if ing.TLSSecretName != "" {
		tlsBlock = fmt.Sprintf(`  tls:
  - secretName: %s
    hosts:
    - %s
`, ing.TLSSecretName, ing.Host)
	}

	host := ing.Host
	if host == "" {
		host = dse.Name + ".example.com  # TODO: set your production hostname"
	}

	return fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
spec:
%s%s  rules:
  - host: %s
    http:
      paths:
      - path: %s
        pathType: %s
        backend:
          service:
            name: %s
            port:
              number: %d
`, dse.Name, classLine, tlsBlock, host, ing.Path, ing.PathType, dse.Name, dse.Port)
}

func kustomizeDepDeployment(depType string, def depDefaults, version string) string {
	safe := helmSafe(depType)
	var envLines strings.Builder
	if len(def.Env) > 0 {
		envLines.WriteString("        env:\n")
		for _, e := range def.Env {
			envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: \"%s\"\n", e.Name, e.Value))
		}
	}

	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: %s
        image: %s:%s
        ports:
        - containerPort: %d
%s`, safe, safe, safe, safe, def.Image, version, def.Port, envLines.String())
}

func kustomizeDepService(depType string, def depDefaults) string {
	safe := helmSafe(depType)
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
  - port: %d
    targetPort: %d
    protocol: TCP
`, safe, safe, def.Port, def.Port)
}

// ── Helpers ─────────────────────────────────────────────────────

func writeSnapshotFile(dir, name, content string) {
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		fmt.Printf("  ⚠  Failed to write %s: %v\n", p, err)
	}
}

// helmSafe makes a name safe for use as a K8s resource name or label value.
func helmSafe(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

// helmValuesKey makes a name safe for use as a Helm values.yaml key.
// Helm's Go template parser treats hyphens as subtraction operators,
// so we convert to underscores.
func helmValuesKey(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

// productionImage converts local registry images to placeholder production images.
// e.g. "localhost:5001/my-svc:123" → "my-svc:latest" (user replaces with their registry)
func productionImage(image, name string) string {
	if strings.HasPrefix(image, "localhost:5001/") {
		return name + ":latest  # TODO: replace with your production registry"
	}
	// If it's a kind-loaded tag like "my-svc:1234567", normalize
	if !strings.Contains(image, "/") && !strings.Contains(image, ":latest") {
		return name + ":latest  # TODO: replace with your production registry"
	}
	return image
}

// connectionProtocol returns the URL scheme for a dependency type.
func connectionProtocol(depType string) string {
	switch depType {
	case "postgres":
		return "postgresql"
	case "mysql":
		return "mysql"
	case "mongodb":
		return "mongodb"
	case "redis":
		return "redis"
	case "rabbitmq":
		return "amqp"
	case "nats":
		return "nats"
	case "elasticsearch":
		return "http"
	case "kafka":
		return "kafka"
	case "memcached":
		return "memcached"
	case "minio":
		return "http"
	default:
		return "tcp"
	}
}

// rewriteServiceURL checks if an env var value contains a URL referencing
// a known sibling service by hostname. If so, it returns a Helm template
// expression that uses {{ .Release.Name }}-<service> so the URL works
// regardless of the Helm release name.
//
// Example:
//
//	"http://orders:5000" → `"http://{{ .Release.Name }}-orders:5000"`
//	"some-plain-value"   → "" (no rewrite needed)
func rewriteServiceURL(value string, knownServices map[string]bool) string {
	// Match patterns like http://service-name:port or http://service-name/path
	for svc := range knownServices {
		// Check for hostname-style references: ://<svc>: or ://<svc>/
		for _, pattern := range []string{
			"://" + svc + ":",
			"://" + svc + "/",
			"://" + svc + "\"",
		} {
			if strings.Contains(value, pattern) {
				rewritten := strings.ReplaceAll(value,
					"://"+svc,
					"://{{ .Release.Name }}-"+svc)
				return fmt.Sprintf(`"%s"`, rewritten)
			}
		}
	}
	return ""
}

// ── Registry helpers ────────────────────────────────────────────

// registryImage builds a clean registry-qualified image reference.
//
//	registryImage("orders", "ghcr.io/myorg", "abc123") → "ghcr.io/myorg/orders:abc123"
func registryImage(name, registry, tag string) string {
	registry = strings.TrimRight(registry, "/")
	return fmt.Sprintf("%s/%s:%s", registry, helmSafe(name), tag)
}

// detectGitTag tries to get a short git SHA for tagging. Falls back to "latest".
func detectGitTag() string {
	out, err := runSilent("git", "rev-parse", "--short", "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "latest"
	}
	return strings.TrimSpace(out)
}

// detectNextTag queries the target registry for the first service's existing
// tags and returns the next sequential "snapshot-N" tag. Falls back to
// "snapshot-1" if the registry is unreachable or has no prior snapshots.
func detectNextTag(registry, sampleService string) string {
	repo := strings.TrimRight(registry, "/") + "/" + helmSafe(sampleService)

	out, err := runSilent("crane", "ls", repo)
	if err != nil {
		return "snapshot-1"
	}

	max := 0
	for _, line := range strings.Split(out, "\n") {
		tag := strings.TrimSpace(line)
		if strings.HasPrefix(tag, "snapshot-") {
			numStr := strings.TrimPrefix(tag, "snapshot-")
			if n, err := strconv.Atoi(numStr); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("snapshot-%d", max+1)
}

// registryPullRef rewrites an in-cluster image reference so it can be
// accessed through a localhost port-forward.
//
//	registryPullRef("registry:5000/gateway:abc", 52431) → "localhost:52431/gateway:abc"
//	registryPullRef("ghcr.io/org/svc:v1", 52431)       → "ghcr.io/org/svc:v1"  (no-op)
func registryPullRef(image string, localPort int) string {
	prefixes := []string{"registry:5000/", "localhost:5000/", "localhost:5001/"}
	for _, p := range prefixes {
		if strings.HasPrefix(image, p) {
			return fmt.Sprintf("localhost:%d/%s", localPort, strings.TrimPrefix(image, p))
		}
	}
	return image
}

// isClusterRegistryImage returns true if the image reference points to
// the Kind in-cluster registry and needs port-forward to access.
func isClusterRegistryImage(image string) bool {
	for _, p := range []string{"registry:5000/", "localhost:5000/", "localhost:5001/"} {
		if strings.HasPrefix(image, p) {
			return true
		}
	}
	return false
}

// startRegistryPortForward opens a kubectl port-forward to the in-cluster
// registry service and returns the local port and a cleanup function.
func startRegistryPortForward() (int, func(), error) {
	// Find a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, fmt.Errorf("cannot find free port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctx := core.ClusterContext(clusterName)
	cmd := exec.Command("kubectl", "--context", ctx,
		"port-forward", "svc/registry", fmt.Sprintf("%d:5000", port))

	// kubectl writes "Forwarding from ..." to stdout
	pipeR, _ := cmd.StdoutPipe()
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("cannot start port-forward: %w", err)
	}

	cleanup := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	// Wait for port-forward to be ready
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(pipeR)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "Forwarding from") {
				ready <- true
				return
			}
		}
		ready <- false
	}()

	select {
	case ok := <-ready:
		if !ok {
			cleanup()
			return 0, nil, fmt.Errorf("port-forward exited before becoming ready")
		}
	case <-time.After(10 * time.Second):
		cleanup()
		return 0, nil, fmt.Errorf("port-forward timed out after 10s")
	}

	return port, cleanup, nil
}

// craneCopyImages pushes images to the target registry. It first tries
// `crane copy` from the in-cluster registry; if that fails (e.g. the image
// was loaded via `kindling load` and only exists in the Docker daemon),
// it falls back to `docker tag` + `docker push`.
//
// userPrefix is the GitHub actor prefix (e.g. "jeff-vincent-") that was
// stripped from DSE names. It's needed to find images in the Docker daemon
// because `kindling load` tags them with the original prefixed name
// (e.g. "jeff-vincent-gateway:12345").
func craneCopyImages(dses []snapshotDSE, registry, tag, userPrefix string) error {
	// Check crane is installed
	if _, err := exec.LookPath("crane"); err != nil {
		return fmt.Errorf("crane is required for --registry (brew install crane)")
	}

	// Check if any images need the in-cluster registry
	needsPF := false
	for _, dse := range dses {
		if isClusterRegistryImage(dse.Image) {
			needsPF = true
			break
		}
	}

	var localPort int
	var cleanup func()

	if needsPF {
		step("🔌", "Port-forwarding to in-cluster registry")
		var err error
		localPort, cleanup, err = startRegistryPortForward()
		if err != nil {
			return fmt.Errorf("cannot reach registry: %w", err)
		}
		defer cleanup()
	}

	seen := make(map[string]bool)
	var failed []string
	for i := range dses {
		if dses[i].Image == "" || seen[dses[i].Image] {
			continue
		}
		seen[dses[i].Image] = true

		dst := registryImage(dses[i].Name, registry, tag)
		step("📤", fmt.Sprintf("%s → %s", dses[i].Name, dst))

		pushed := false

		// Prefer Docker daemon images — they're built by `kindling load`
		// with --platform linux/amd64, so they're the correct arch for
		// production. The in-cluster registry may have stale CI-built
		// images that match the host arch (arm64 on Apple Silicon).
		if localImg := findDockerImage(dses[i].Name, userPrefix); localImg != "" {
			step("🐳", fmt.Sprintf("Found %s in Docker daemon — pushing directly", localImg))
			if err := dockerTagAndPush(localImg, dst); err != nil {
				warn(fmt.Sprintf("Docker push failed for %s: %v — trying registry", dses[i].Name, err))
			} else {
				pushed = true
			}
		}

		// Fallback: crane copy from in-cluster registry
		if !pushed && isClusterRegistryImage(dses[i].Image) {
			src := registryPullRef(dses[i].Image, localPort)
			args := []string{"copy", "--insecure", src, dst}
			if _, err := runSilent("crane", args...); err == nil {
				pushed = true
			}
		}

		if !pushed {
			warn(fmt.Sprintf("Could not push %s — not in Docker daemon or registry", dses[i].Name))
			failed = append(failed, dses[i].Name)
		}

		// Always rewrite image ref to target the production registry
		dses[i].Image = dst
	}

	if len(failed) == len(seen) {
		return fmt.Errorf("all image pushes failed — check registry credentials and source images")
	}
	if len(failed) > 0 {
		warn(fmt.Sprintf("%d/%d images could not be pushed: %s",
			len(failed), len(seen), strings.Join(failed, ", ")))
	}

	success("Images pushed to registry")
	return nil
}

// findDockerImage searches the local Docker daemon for the most recent
// image matching a service name. It tries both the stripped name and the
// prefixed name (since `kindling load` tags images as "<prefix><service>").
func findDockerImage(serviceName, userPrefix string) string {
	candidates := []string{serviceName}
	if userPrefix != "" {
		// prefix includes trailing dash, e.g. "jeff-vincent-"
		candidates = append(candidates, strings.TrimSuffix(userPrefix, "-")+"-"+serviceName)
	}

	for _, name := range candidates {
		// docker images --format with filter by reference
		out, err := runSilent("docker", "images", "--format", "{{.Repository}}:{{.Tag}}", "--filter", "reference="+name)
		if err != nil || out == "" {
			continue
		}
		// Return the first (most recent) match
		lines := strings.Split(out, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasSuffix(line, ":<none>") {
				return line
			}
		}
	}
	return ""
}

// dockerTagAndPush tags a local Docker image as dst and pushes it.
func dockerTagAndPush(localImage, dst string) error {
	if _, err := runSilent("docker", "tag", localImage, dst); err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}
	if _, err := runSilent("docker", "push", dst); err != nil {
		return fmt.Errorf("docker push failed: %w", err)
	}
	return nil
}
