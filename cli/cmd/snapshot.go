package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

With --registry, images are extracted from the Kind cluster, re-tagged
with clean names, and pushed to your container registry so the exported
chart is ready to deploy anywhere.

Examples:
  kindling snapshot                          # Helm chart in ./kindling-snapshot/
  kindling snapshot --format kustomize       # Kustomize overlay
  kindling snapshot -o ./my-chart            # custom output directory
  kindling snapshot --name my-platform       # custom chart name
  kindling snapshot -r ghcr.io/myorg         # push images + ready-to-run chart
  kindling snapshot -r ghcr.io/myorg -t v1.0 # push with specific tag`,
	RunE: runSnapshot,
}

var (
	snapshotFormat   string
	snapshotOutput   string
	snapshotName     string
	snapshotRegistry string
	snapshotTag      string
)

func init() {
	snapshotCmd.Flags().StringVarP(&snapshotFormat, "format", "f", "helm", "Export format: helm or kustomize")
	snapshotCmd.Flags().StringVarP(&snapshotOutput, "output", "o", "", "Output directory (default: ./kindling-snapshot)")
	snapshotCmd.Flags().StringVarP(&snapshotName, "name", "n", "", "Chart/project name (default: derived from cluster)")
	snapshotCmd.Flags().StringVarP(&snapshotRegistry, "registry", "r", "", "Container registry (e.g. ghcr.io/myorg, 123456.dkr.ecr.us-east-1.amazonaws.com/myapp)")
	snapshotCmd.Flags().StringVarP(&snapshotTag, "tag", "t", "", "Image tag (default: git SHA or 'latest')")
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
	if prefix := detectUserPrefix(dses); prefix != "" {
		step("✂️", fmt.Sprintf("Stripping user prefix %q from service names", strings.TrimSuffix(prefix, "-")))
		for i := range dses {
			if stripped := strings.TrimPrefix(dses[i].Name, prefix); stripped != "" {
				dses[i].Name = stripped
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
			tag = detectGitTag()
		}
		step("🏷", fmt.Sprintf("Re-tagging images → %s (tag: %s)", snapshotRegistry, tag))

		if err := extractKindImages(dses); err != nil {
			warn(fmt.Sprintf("Could not extract images from Kind nodes: %v", err))
			warn("Falling back to image references only (no push)")
		} else {
			for i := range dses {
				newRef, err := retagAndPush(dses[i].Image, dses[i].Name, snapshotRegistry, tag)
				if err != nil {
					warn(fmt.Sprintf("Failed to push %s: %v", dses[i].Name, err))
					// Still rewrite the image ref so the chart is correct
					newRef = registryImage(dses[i].Name, snapshotRegistry, tag)
				}
				dses[i].Image = newRef
			}
			success("Images pushed to registry")
		}
	}

	switch snapshotFormat {
	case "helm":
		return exportHelm(outDir, chartName, dses)
	case "kustomize":
		return exportKustomize(outDir, chartName, dses)
	default:
		return fmt.Errorf("unknown format %q — use 'helm' or 'kustomize'", snapshotFormat)
	}
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

	// ── values.yaml (clean chart with commented examples) ───────
	valuesYAML := buildValuesYAML(chartName, dses, depsSeen, false)
	writeSnapshotFile(outDir, "values.yaml", valuesYAML)

	// ── values-live.yaml (populated from current cluster) ───────
	liveYAML := buildValuesYAML(chartName, dses, depsSeen, true)
	writeSnapshotFile(outDir, "values-live.yaml", liveYAML)

	// ── Templates: service deployments ──────────────────────────
	for _, dse := range dses {
		safe := helmSafe(dse.Name)
		writeSnapshotFile(templatesDir, safe+"-deployment.yaml", helmDeploymentTemplate(dse))
		writeSnapshotFile(templatesDir, safe+"-service.yaml", helmServiceTemplate(dse))
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
		safe := helmSafe(dse.Name)
		prodImage := productionImageClean(dse.Image, dse.Name)
		liveImage := dse.Image

		buf.WriteString(fmt.Sprintf("%s:\n", safe))

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
		buf.WriteString("\n")
	}

	// ── Dependency values ───────────────────────────────────────
	for depType := range depsSeen {
		def, ok := depRegistry[depType]
		if !ok {
			continue
		}
		safe := helmSafe(depType)

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

		buf.WriteString(fmt.Sprintf("%s:\n", safe))
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

func helmDeploymentTemplate(dse snapshotDSE) string {
	safe := helmSafe(dse.Name)

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
`, safe, def.EnvVarName, def.EnvVarName, safe, def.EnvVarName))
		}
	}
	// User-defined env vars from values
	if len(dse.Env) > 0 {
		for _, e := range dse.Env {
			envLines.WriteString(fmt.Sprintf(`        {{- if .Values.%s.env.%s }}
        - name: %s
          value: {{ .Values.%s.env.%s | quote }}
        {{- end }}
`, safe, e.Name, e.Name, safe, e.Name))
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
        ports:
        - containerPort: {{ .Values.%s.port }}
%s`, safe, safe, "kindling-snapshot", safe, safe, safe, safe, safe, safe, envSection)
}

func helmServiceTemplate(dse snapshotDSE) string {
	safe := helmSafe(dse.Name)
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
`, safe, safe, "kindling-snapshot", safe, safe, safe)
}

func helmDepDeploymentTemplate(depType string, def depDefaults) string {
	safe := helmSafe(depType)

	var envLines strings.Builder
	if len(def.Env) > 0 {
		envLines.WriteString("        env:\n")
		for _, e := range def.Env {
			envLines.WriteString(fmt.Sprintf(`        - name: %s
          value: {{ .Values.%s.env.%s | quote }}
`, e.Name, safe, e.Name))
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
        ports:
        - containerPort: {{ .Values.%s.port }}
%s{{- end }}
`, safe, safe, safe, safe, safe, safe, safe, safe, envLines.String())
}

func helmDepServiceTemplate(depType string, def depDefaults) string {
	safe := helmSafe(depType)
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
`, safe, safe, safe, safe, safe, safe)
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
	var envLines strings.Builder
	// Dependency connection strings
	for _, dep := range dse.Deps {
		if def, ok := depRegistry[dep.Type]; ok {
			envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: \"\"  # TODO: set your production %s connection string\n",
				def.EnvVarName, dep.Type))
		}
	}
	// User env
	for _, e := range dse.Env {
		envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: \"%s\"\n", e.Name, e.Value))
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

// helmSafe makes a name safe for use as a Helm values key / K8s resource name.
func helmSafe(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
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

// ── Registry helpers ────────────────────────────────────────────

// registryImage builds a clean registry-qualified image reference.
//   registryImage("orders", "ghcr.io/myorg", "abc123") → "ghcr.io/myorg/orders:abc123"
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

// registryPullRef rewrites an in-cluster image reference so it can be
// pulled through a localhost port-forward.
//   registryPullRef("registry:5000/gateway:abc", 52431) → "localhost:52431/gateway:abc"
//   registryPullRef("ghcr.io/org/svc:v1", 52431)       → "ghcr.io/org/svc:v1"  (no-op)
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
// the Kind in-cluster registry and needs port-forward to pull.
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
// It picks a free port to avoid conflicts.
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

	// Wait for port-forward to be ready ("Forwarding from ..." on stderr)
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

// extractKindImages pulls deployed images from the in-cluster registry
// via kubectl port-forward into the local Docker daemon.
func extractKindImages(dses []snapshotDSE) error {
	// Check if any images actually need the in-cluster registry
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
	for _, dse := range dses {
		if dse.Image == "" || seen[dse.Image] {
			continue
		}
		seen[dse.Image] = true

		pullRef := registryPullRef(dse.Image, localPort)

		step("📥", fmt.Sprintf("Pulling %s", pullRef))

		if _, err := runSilent("docker", "pull", pullRef); err != nil {
			// Fallback: check if image is already local (e.g. built outside Kind)
			_, checkErr := runSilent("docker", "image", "inspect", dse.Image)
			if checkErr != nil {
				return fmt.Errorf("cannot pull %s: %w", pullRef, err)
			}
		} else if pullRef != dse.Image {
			// Tag with the original DSE name so retagAndPush can find it
			_, _ = runSilent("docker", "tag", pullRef, dse.Image)
		}
	}
	return nil
}

// retagAndPush re-tags a local Docker image and pushes it to the target registry.
func retagAndPush(sourceImage, name, registry, tag string) (string, error) {
	newRef := registryImage(name, registry, tag)

	step("🏷", fmt.Sprintf("%s → %s", sourceImage, newRef))

	// Tag
	if _, err := runSilent("docker", "tag", sourceImage, newRef); err != nil {
		return "", fmt.Errorf("docker tag failed: %w", err)
	}

	// Push
	step("📤", fmt.Sprintf("Pushing %s", newRef))
	if _, err := runSilent("docker", "push", newRef); err != nil {
		return "", fmt.Errorf("docker push failed: %w", err)
	}

	return newRef, nil
}
