package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/jeffvincent/kindling/cli/core"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Export a Helm chart or Kustomize overlay from the current cluster state",
	Long: `Reads all DevStagingEnvironments in the cluster and generates
staging-ready Kubernetes manifests as a Helm chart or Kustomize overlay.

With --registry, images are copied from the Kind cluster's in-cluster
registry to your container registry via crane (no Docker daemon needed).

With --deploy, the generated chart is deployed to a staging cluster
in one step. The --context flag is required to specify the target cluster
and --registry is required to make images accessible outside Kind. Unless
--name/--namespace are set explicitly, --deploy derives both from the
current git branch (or --branch), so concurrent branches deployed to the
same shared staging cluster never collide. Any DSE with Ingress enabled
but no host set gets a branch-derived host too, via --staging-domain
(<branch-slug>.<staging-domain>) — an explicit spec.ingress.host always
wins over the derived one.

Examples:
  kindling snapshot                          # Helm chart in ./kindling-snapshot/
  kindling snapshot --format kustomize       # Kustomize overlay
  kindling snapshot -o ./my-chart            # custom output directory
  kindling snapshot --name my-platform       # custom chart name
  kindling snapshot -r ghcr.io/myorg         # push images + ready-to-run chart
  kindling snapshot -r ghcr.io/myorg -t v1.0 # push with specific tag

  # Full graduation: snapshot + push images + deploy to staging
  kindling snapshot -r ghcr.io/myorg --deploy --context my-staging-cluster
  kindling snapshot -r ghcr.io/myorg --deploy --context staging --namespace staging
  kindling snapshot -f kustomize -r ghcr.io/myorg --deploy --context staging

  # PR branch → its own name-scoped staging environment, no collisions
  kindling snapshot -r ghcr.io/myorg --deploy --context staging

  # ...with a branch-derived, resolvable Ingress host too
  kindling snapshot -r ghcr.io/myorg --deploy --context staging --staging-domain staging.example.com`,
	RunE: runSnapshot,
}

var (
	snapshotFormat        string
	snapshotOutput        string
	snapshotName          string
	snapshotRegistry      string
	snapshotTag           string
	snapshotDeploy        bool
	snapshotContext       string
	snapshotNamespace     string
	snapshotBranch        string
	snapshotStagingDomain string
)

func init() {
	snapshotCmd.Flags().StringVarP(&snapshotFormat, "format", "f", "helm", "Export format: helm or kustomize")
	snapshotCmd.Flags().StringVarP(&snapshotOutput, "output", "o", "", "Output directory (default: ./kindling-snapshot)")
	snapshotCmd.Flags().StringVarP(&snapshotName, "name", "n", "", "Chart/project name (default: derived from cluster)")
	snapshotCmd.Flags().StringVarP(&snapshotRegistry, "registry", "r", "", "Container registry (e.g. ghcr.io/myorg, 123456.dkr.ecr.us-east-1.amazonaws.com/myapp)")
	snapshotCmd.Flags().StringVarP(&snapshotTag, "tag", "t", "", "Image tag (default: git SHA or 'latest')")
	snapshotCmd.Flags().BoolVar(&snapshotDeploy, "deploy", false, "Deploy to a staging cluster after generating the chart")
	snapshotCmd.Flags().StringVar(&snapshotContext, "context", "", "Kubeconfig context for the staging cluster (required with --deploy)")
	snapshotCmd.Flags().StringVar(&snapshotNamespace, "namespace", "default", "Kubernetes namespace to deploy into (used with --deploy)")
	snapshotCmd.Flags().StringVar(&snapshotBranch, "branch", "", "Git branch to derive the staging environment name from (default: current branch; used with --deploy)")
	snapshotCmd.Flags().StringVar(&snapshotStagingDomain, "staging-domain", "", "Base domain for branch-derived Ingress hosts, e.g. staging.example.com (required for --deploy if the DSE doesn't already set an Ingress host)")
	rootCmd.AddCommand(snapshotCmd)
}

// currentBranch returns the current git branch name, so 'kindling snapshot
// --deploy' can derive a stable per-branch environment name with no extra
// flag needed in the common case (both for a human on their own checkout
// and for a CI job on a runner that already checked the branch out).
func currentBranch() (string, error) {
	out, err := core.RunCapture("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("could not determine current git branch: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// slugifyBranch converts a git branch name into a stable, RFC 1123-valid
// Kubernetes name component. Deterministic: same branch name always
// produces the same slug. main/master are not special-cased here on
// purpose — if a workflow needs different treatment for those branches,
// that's a decision at the call site (e.g. don't invoke --deploy from a
// main push), not inside this pure function.
func slugifyBranch(branch string) string {
	s := strings.ToLower(branch)
	// Replace anything that isn't a-z0-9 with a hyphen.
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	// Collapse repeated hyphens.
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	// Reserve room for a "-staging" or service-name suffix elsewhere in the
	// naming pipeline; 40 chars leaves headroom under the 63-char DNS label
	// limit once composed with other segments.
	const maxLen = 40
	if len(s) > maxLen {
		s = strings.Trim(s[:maxLen], "-")
	}
	if s == "" {
		return "branch" // pathological case: branch name was all symbols
	}
	return s
}

// applyBranchIngressHost fills in the Ingress host for any DSE that has
// Ingress enabled but no *meaningful* host set, using derivedHost
// (typically "<branch-slug>.<staging-domain>"). A host is considered
// meaningful — and left untouched — only if it's non-empty and isn't the
// local dev convention (<name>.localhost, carried straight over from the
// local Kind cluster's DSE). That local host is never resolvable outside
// Kind and is frequently identical across branches, so treating it as
// "explicit intent" would silently keep every branch colliding on the
// same host — the exact bug this function exists to fix. A genuinely
// custom, non-localhost host is still never overridden. Returns the
// names of the DSEs that got a host filled in, for caller-side logging.
// If a DSE needs a host and derivedHost is empty (no --staging-domain and
// no meaningful explicit host), returns an error naming that DSE rather
// than silently falling through to an unreachable/colliding environment.
func applyBranchIngressHost(dses []snapshotDSE, derivedHost string) ([]string, error) {
	var derived []string
	for i := range dses {
		if dses[i].Ingress == nil || !dses[i].Ingress.Enabled {
			continue
		}
		host := dses[i].Ingress.Host
		if host != "" && !strings.HasSuffix(host, ".localhost") {
			continue // genuinely explicit, non-local host — never overridden
		}
		if derivedHost == "" {
			return nil, fmt.Errorf("no Ingress host available for %q — set --staging-domain, or set spec.ingress.host explicitly in the DSE", dses[i].Name)
		}
		dses[i].Ingress.Host = derivedHost
		derived = append(derived, dses[i].Name)
	}
	return derived, nil
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
	Compute  string // e.g. "gpu", "high-memory", "arm64"
}

type snapshotEnvVar struct {
	Name     string
	Value    string
	IsSecret bool // true when sourced from a K8s secretKeyRef
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
	Routes           []snapshotRoute
}

// snapshotRoute is an additional path -> service route merged onto the
// same Ingress alongside the primary Host/Path route (spec.ingress.routes).
type snapshotRoute struct {
	Path     string
	PathType string
	Service  string
	Port     int
}

func readClusterDSEs() ([]snapshotDSE, error) {
	out, err := core.Kubectl(clusterName, "get", "devstagingenvironments", "--all-namespaces", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("cannot read DSEs: %s", out)
	}

	var list struct {
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
					Compute  string `json:"compute,omitempty"`
					Env      []struct {
						Name      string `json:"name"`
						Value     string `json:"value"`
						ValueFrom *struct {
							SecretKeyRef *struct {
								Name string `json:"name"`
								Key  string `json:"key"`
							} `json:"secretKeyRef"`
						} `json:"valueFrom,omitempty"`
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
					Enabled          bool    `json:"enabled"`
					Host             string  `json:"host,omitempty"`
					Path             string  `json:"path,omitempty"`
					PathType         string  `json:"pathType,omitempty"`
					IngressClassName *string `json:"ingressClassName,omitempty"`
					TLS              *struct {
						SecretName string `json:"secretName"`
					} `json:"tls,omitempty"`
					Annotations map[string]string `json:"annotations,omitempty"`
					Routes      []struct {
						Path     string `json:"path"`
						PathType string `json:"pathType,omitempty"`
						Service  string `json:"service"`
						Port     int    `json:"port"`
					} `json:"routes,omitempty"`
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
			Compute:  item.Spec.Deployment.Compute,
		}
		for _, e := range item.Spec.Deployment.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				// Resolve the secret value from the cluster
				val := resolveSecretValue(e.ValueFrom.SecretKeyRef.Name,
					e.ValueFrom.SecretKeyRef.Key, item.Metadata.Namespace)
				d.Env = append(d.Env, snapshotEnvVar{
					Name: e.Name, Value: val, IsSecret: true,
				})
			} else {
				d.Env = append(d.Env, snapshotEnvVar{Name: e.Name, Value: e.Value})
			}
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
			for _, route := range item.Spec.Ingress.Routes {
				rPathType := route.PathType
				if rPathType == "" {
					rPathType = "Prefix"
				}
				ing.Routes = append(ing.Routes, snapshotRoute{
					Path:     route.Path,
					PathType: rPathType,
					Service:  route.Service,
					Port:     route.Port,
				})
			}
			dses[i].Ingress = ing
		}
	}

	return dses, nil
}

// resolveSecretValue reads a K8s secret value from the cluster.
// Falls back to empty string on error (the user will supply the
// staging value via the credential prompt).
func resolveSecretValue(secretName, key, namespace string) string {
	ns := namespace
	if ns == "" {
		ns = "default"
	}
	// Try the DSE's namespace first, then default
	for _, tryNS := range []string{ns, "default"} {
		out, err := exec.Command("kubectl", "get", "secret", secretName,
			"-n", tryNS,
			"-o", fmt.Sprintf("jsonpath={.data.%s}", key)).CombinedOutput()
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			continue
		}
		decoded, err := exec.Command("bash", "-c",
			fmt.Sprintf("echo -n '%s' | base64 -d", strings.TrimSpace(string(out)))).CombinedOutput()
		if err != nil {
			continue
		}
		return strings.TrimSpace(string(decoded))
	}
	return ""
}

// promptSnapshotSecrets prompts the user for staging values for all
// secret-backed env vars. The dev cluster values are shown as defaults.
// The user can press Enter to accept the default or type a new value.
// Updated values are written back into the DSE structs so they appear
// in the generated chart (values-live.yaml and the credential override).
func promptSnapshotSecrets(dses []snapshotDSE) error {
	// Collect all secret env vars across services
	type secretField struct {
		dseIdx int
		envIdx int
		name   string
		devVal string
		svc    string
	}
	var fields []secretField
	for i, dse := range dses {
		for j, e := range dse.Env {
			if e.IsSecret {
				fields = append(fields, secretField{
					dseIdx: i, envIdx: j,
					name: e.Name, devVal: e.Value,
					svc: dse.Name,
				})
			}
		}
	}
	if len(fields) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr)
	step("🔑", fmt.Sprintf("Found %d %s from K8s secrets",
		len(fields), pluralize(len(fields), "env var", "env vars")))
	fmt.Fprintln(os.Stderr, "       Accept the dev defaults (press Enter) or enter staging values.")
	fmt.Fprintln(os.Stderr)

	// Build form fields — one input per secret
	type fieldRef struct {
		idx   int
		value *string
	}
	var refs []fieldRef
	var huhFields []huh.Field

	for i, f := range fields {
		val := new(string)
		*val = f.devVal // pre-fill with dev value

		desc := fmt.Sprintf("Service: %s", f.svc)
		if f.devVal != "" {
			desc += fmt.Sprintf("\nDev value: %s", truncateStr(f.devVal, 60))
		}

		huhFields = append(huhFields, huh.NewInput().
			Title(f.name).
			Description(desc).
			Value(val))

		refs = append(refs, fieldRef{idx: i, value: val})
	}

	form := huh.NewForm(huh.NewGroup(huhFields...))
	if err := form.Run(); err != nil {
		return fmt.Errorf("secret configuration cancelled: %w", err)
	}

	// Write values back into DSE structs
	changed := 0
	for _, ref := range refs {
		f := fields[ref.idx]
		newVal := *ref.value
		if newVal != f.devVal {
			changed++
		}
		dses[f.dseIdx].Env[f.envIdx].Value = newVal
	}

	if changed > 0 {
		step("✓", fmt.Sprintf("Updated %d %s with staging values",
			changed, pluralize(changed, "secret", "secrets")))
	} else {
		step("✓", "Using dev values for all secrets")
	}
	return nil
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
	var branch, slug, derivedIngressHost string
	if snapshotDeploy {
		if snapshotContext == "" {
			return fmt.Errorf("--context is required when using --deploy")
		}
		if strings.HasPrefix(snapshotContext, "kind-") {
			return fmt.Errorf("context %q looks like a Kind cluster — use 'kindling deploy' for local dev", snapshotContext)
		}
		if snapshotRegistry == "" {
			return fmt.Errorf("--registry is required when using --deploy (images must be accessible from the staging cluster)")
		}

		// ── Branch-derived naming ────────────────────────────────
		// Two branches deployed to the same shared staging cluster must
		// not collide — in name, namespace, or Ingress host.
		// Only fills in defaults the user didn't already set explicitly.
		branch = snapshotBranch
		if branch == "" {
			var err error
			branch, err = currentBranch()
			if err != nil {
				return err
			}
		}
		slug = slugifyBranch(branch)
		if snapshotName == "" {
			snapshotName = slug
		}
		if snapshotNamespace == "default" && !cmd.Flags().Changed("namespace") {
			snapshotNamespace = slug
		}
		if snapshotStagingDomain != "" {
			derivedIngressHost = fmt.Sprintf("%s.%s", slug, snapshotStagingDomain)
		}
	}

	// Fail fast on missing tools before reading cluster state or prompting
	// for registry credentials — cheaper to catch here than deep inside
	// the push step after the user has already typed a password.
	if snapshotRegistry != "" && !commandExists("crane") {
		return fmt.Errorf("crane is required for --registry (brew install crane) — run 'kindling init' to check for this and other optional tools")
	}

	header("Exporting cluster snapshot")

	if snapshotDeploy {
		step("🌿", fmt.Sprintf("Branch %q → slug %q (used for any of --name/--namespace not set explicitly)", branch, slug))
	}

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
	userPrefix := stripDSEPrefix(dses)
	if userPrefix != "" {
		step("✂️", fmt.Sprintf("Stripping user prefix %q from service names", strings.TrimSuffix(userPrefix, "-")))
	}

	// ── Branch-derived Ingress host ──────────────────────────────
	// Any DSE that already has Ingress enabled but no host set gets the
	// same branch-derived treatment as --name/--namespace — an explicit
	// spec.ingress.host is never overridden. Without --staging-domain,
	// a genuinely missing host fails the build instead of silently
	// producing an unreachable environment (the previous "# TODO: set
	// your staging hostname" placeholder behavior).
	if snapshotDeploy {
		derived, err := applyBranchIngressHost(dses, derivedIngressHost)
		if err != nil {
			return err
		}
		for _, name := range derived {
			step("🌐", fmt.Sprintf("%s: derived Ingress host %s", name, derivedIngressHost))
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
	var regUser, regPass string
	if snapshotRegistry != "" {
		tag := snapshotTag
		if tag == "" {
			tag = detectNextTag(snapshotRegistry, dses[0].Name)
		}
		step("🏷", fmt.Sprintf("Re-tagging images → %s (tag: %s)", snapshotRegistry, tag))

		// Prompt for registry credentials
		regHost := registryHost(snapshotRegistry)
		step("🔑", fmt.Sprintf("Registry credentials for %s", regHost))
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Username").
					Value(&regUser),
				huh.NewInput().
					Title("Password / Token").
					EchoMode(huh.EchoModePassword).
					Value(&regPass),
			),
		)
		if err := form.Run(); err != nil {
			return fmt.Errorf("registry credentials cancelled: %w", err)
		}

		if err := craneCopyImages(dses, snapshotRegistry, tag, userPrefix, regUser, regPass); err != nil {
			return fmt.Errorf("image push failed: %w", err)
		}
	}

	// ── Prompt for secret values ────────────────────────────────
	// If any services have secretKeyRef env vars, prompt the user to
	// accept the dev defaults or enter staging-specific values.
	if err := promptSnapshotSecrets(dses); err != nil {
		return err
	}

	if err := exportSnapshot(snapshotFormat, outDir, chartName, dses); err != nil {
		return err
	}

	// ── Deploy to staging cluster ───────────────────────────
	if !snapshotDeploy {
		return nil
	}

	header("Deploying to staging")
	step("🔗", fmt.Sprintf("Target context: %s%s%s", colorBold, snapshotContext, colorReset))

	// Verify cluster connectivity
	step("🔍", "Verifying cluster connectivity")
	if err := run("kubectl", "cluster-info", "--context", snapshotContext); err != nil {
		return fmt.Errorf("cannot reach cluster via context %q: %w", snapshotContext, err)
	}

	// ── Ensure ingress controller ──────────────────────────────
	if err := ensureIngressController(snapshotContext, func(msg string) {
		step("🌐", msg)
	}); err != nil {
		warn(fmt.Sprintf("Could not ensure ingress controller: %v", err))
	}

	// ── Ingress selector ──────────────────────────────────────
	// Offer ALL services for ingress selection, pre-selecting ones
	// that already had ingress enabled in dev.
	var selectedIngress []string
	if len(dses) > 0 {
		options := make([]huh.Option[string], len(dses))
		var preSelected []string
		for i, dse := range dses {
			label := dse.Name
			if dse.Ingress != nil && dse.Ingress.Enabled {
				label += " (ingress in dev)"
				preSelected = append(preSelected, dse.Name)
			}
			options[i] = huh.NewOption(label, dse.Name)
		}
		selectedIngress = preSelected

		form := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Which services should be publicly accessible?").
					Description("Selected services will have Ingress enabled.\nUse space to toggle, enter to confirm.").
					Options(options...).
					Value(&selectedIngress),
			),
		)

		if err := form.Run(); err != nil {
			return fmt.Errorf("ingress selection cancelled: %w", err)
		}
	}

	// Build a set of selected ingress services for quick lookup
	selectedSet := make(map[string]bool)
	for _, svc := range selectedIngress {
		selectedSet[svc] = true
	}
	if len(selectedIngress) > 0 {
		step("🌐", fmt.Sprintf("Ingress enabled for: %s", strings.Join(selectedIngress, ", ")))
	} else {
		step("🌐", "No services selected for public ingress")
	}

	// Detect the IngressClass on the target cluster
	ingClass := detectIngressClass(snapshotContext)
	if ingClass != "" {
		step("🌐", fmt.Sprintf("Using IngressClass: %s", ingClass))
	}

	// ── Staging credentials ──────────────────────────────────
	// Detect dev-default connection strings and prompt for staging values.
	credOverrides, err := resolveStagingCredentials(chartName, snapshotContext, dses)
	if err != nil {
		return fmt.Errorf("credential configuration failed: %w", err)
	}
	var credsFile string
	if len(credOverrides) > 0 {
		path, err := writeCredsOverrideFile(credOverrides)
		if err != nil {
			return err
		}
		credsFile = path
		defer os.Remove(credsFile)
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
			"--timeout", "10m",
		}
		// Apply staging credential overrides (takes precedence over values-live.yaml)
		if credsFile != "" {
			helmArgs = append(helmArgs, "-f", credsFile)
		}
		// Enable/disable ingress for all services based on user selection.
		for _, dse := range dses {
			vk := helmValuesKey(dse.Name)
			if selectedSet[dse.Name] {
				helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.enabled=true", vk))
				// Prefer the host already resolved onto this DSE (explicit
				// spec.ingress.host, or the branch-derived fallback applied
				// above); only fall back to derivedIngressHost directly for
				// a service selected here that had no Ingress config at all
				// in dev. Never force it blank — that silently breaks
				// Host-based routing on the shared staging cluster.
				host := ""
				if dse.Ingress != nil {
					host = dse.Ingress.Host
				}
				if host == "" {
					host = derivedIngressHost
				}
				if host != "" {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.host=%s", vk, host))
				}
				if ingClass != "" {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.ingressClassName=%s", vk, ingClass))
				}
			} else {
				helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.enabled=false", vk))
			}
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

	success("Deployed to staging")
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
		// Generate secrets template if the service has secret-backed env vars
		if tpl := helmSecretsTemplate(dse, chartName); tpl != "" {
			writeSnapshotFile(templatesDir, safe+"-secrets.yaml", tpl)
		}
		// Generate ingress template for every service so users can
		// enable ingress at deploy time even if it wasn't in dev.
		if dse.Ingress == nil {
			dse.Ingress = &snapshotIngress{
				Enabled:  false,
				Path:     "/",
				PathType: "Prefix",
			}
		}
		writeSnapshotFile(templatesDir, safe+"-ingress.yaml", helmIngressTemplate(dse, chartName))
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
		buf.WriteString("# staging values, or use values-live.yaml as a starting point.\n\n")
	}

	// ── Service values ──────────────────────────────────────────
	for _, dse := range dses {
		vk := helmValuesKey(dse.Name)
		stagingImg := stagingImageClean(dse.Image, dse.Name)
		liveImage := dse.Image

		buf.WriteString(fmt.Sprintf("%s:\n", vk))

		if live {
			buf.WriteString(fmt.Sprintf("  image: \"%s\"\n", liveImage))
		} else {
			buf.WriteString(fmt.Sprintf("  image: \"%s\"", stagingImg))
			if liveImage != stagingImg {
				buf.WriteString(fmt.Sprintf("  # ← currently: %s", liveImage))
			}
			buf.WriteString("\n")
		}

		buf.WriteString(fmt.Sprintf("  port: %d\n", dse.Port))
		buf.WriteString(fmt.Sprintf("  replicas: %d\n", dse.Replicas))

		// Compute scheduling — nodeSelector + toleration for special hardware
		if dse.Compute != "" {
			buf.WriteString(fmt.Sprintf("  compute: \"%s\"\n", dse.Compute))
		} else {
			buf.WriteString("  compute: \"\"  # e.g. \"gpu\", \"high-memory\", \"arm64\"\n")
		}

		// Env vars — user-defined (non-secret) + dependency connection strings
		hasPlainEnv := false
		for _, e := range dse.Env {
			if !e.IsSecret {
				hasPlainEnv = true
				break
			}
		}
		if hasPlainEnv || len(dse.Deps) > 0 {
			buf.WriteString("  env:\n")
			// Non-secret user env vars
			for _, e := range dse.Env {
				if e.IsSecret {
					continue
				}
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
							buildConnectionURL(chartName, dep.Type, helmSafe(dep.Type), def)))
					} else {
						buf.WriteString(fmt.Sprintf("    %s: \"\"  # TODO: set your staging %s connection string\n",
							def.EnvVarName, dep.Type))
					}
				}
			}
		}

		// Secrets — env vars sourced from K8s secrets in dev
		hasSecrets := false
		for _, e := range dse.Env {
			if e.IsSecret {
				hasSecrets = true
				break
			}
		}
		if hasSecrets {
			buf.WriteString("  secrets:\n")
			for _, e := range dse.Env {
				if !e.IsSecret {
					continue
				}
				if live {
					buf.WriteString(fmt.Sprintf("    %s: \"%s\"\n", e.Name, yamlEscape(e.Value)))
				} else {
					buf.WriteString(fmt.Sprintf("    %s: \"\"  # TODO: set staging value\n", e.Name))
				}
			}
		}

		// Ingress config — always generate so users can enable at deploy time
		buf.WriteString("  ingress:\n")
		if dse.Ingress != nil && dse.Ingress.Enabled {
			buf.WriteString("    enabled: true\n")
			if live {
				buf.WriteString(fmt.Sprintf("    host: \"%s\"\n", dse.Ingress.Host))
			} else {
				buf.WriteString(fmt.Sprintf("    host: \"\"  # TODO: set your staging hostname (dev: %s)\n", dse.Ingress.Host))
			}
			path := dse.Ingress.Path
			pathType := dse.Ingress.PathType
			if path == "" {
				path = "/"
			}
			if pathType == "" {
				pathType = "Prefix"
			}
			buf.WriteString(fmt.Sprintf("    path: \"%s\"\n", path))
			buf.WriteString(fmt.Sprintf("    pathType: \"%s\"\n", pathType))
			if dse.Ingress.IngressClassName != "" {
				buf.WriteString(fmt.Sprintf("    ingressClassName: \"%s\"\n", dse.Ingress.IngressClassName))
			}
			if dse.Ingress.TLSSecretName != "" {
				buf.WriteString("    tls:\n")
				buf.WriteString(fmt.Sprintf("      secretName: \"%s\"\n", dse.Ingress.TLSSecretName))
			}
			if len(dse.Ingress.Routes) > 0 {
				buf.WriteString("    routes:\n")
				for _, route := range dse.Ingress.Routes {
					buf.WriteString(fmt.Sprintf("    - path: \"%s\"\n", route.Path))
					buf.WriteString(fmt.Sprintf("      pathType: \"%s\"\n", route.PathType))
					buf.WriteString(fmt.Sprintf("      service: \"%s\"\n", route.Service))
					buf.WriteString(fmt.Sprintf("      port: %d\n", route.Port))
				}
			} else {
				buf.WriteString("    routes: []\n")
			}
			buf.WriteString("    annotations: {}\n")
		} else {
			buf.WriteString("    enabled: false\n")
			buf.WriteString("    host: \"\"\n")
			buf.WriteString("    path: \"/\"\n")
			buf.WriteString("    pathType: \"Prefix\"\n")
			buf.WriteString("    routes: []\n")
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
				buildConnectionURL("", depType, safe, def)))
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

// buildConnectionURL generates a connection string for a dependency type using the
// given release name as the hostname prefix. When releasePrefix is empty, it falls
// back to "<release>" for documentation/example output.
func buildConnectionURL(releasePrefix, depType, safe string, def depDefaults) string {
	prefix := releasePrefix
	if prefix == "" {
		prefix = "<release>"
	}
	host := fmt.Sprintf("%s-%s", prefix, safe)
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

// stagingImageClean is like stagingImage but without the trailing comment.
func stagingImageClean(image, name string) string {
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
	// set their staging URLs without editing templates.
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
	// Secret-backed env vars use secretKeyRef pointing to the chart-managed secret.
	// Otherwise source from values.yaml.
	if len(dse.Env) > 0 {
		for _, e := range dse.Env {
			if e.IsSecret {
				// Reference the chart-managed K8s Secret via secretKeyRef
				envLines.WriteString(fmt.Sprintf(`        - name: %s
          valueFrom:
            secretKeyRef:
              name: {{ .Release.Name }}-%s-secrets
              key: %s
`, e.Name, safe, e.Name))
			} else if helmVal := rewriteServiceURL(e.Value, knownServices); helmVal != "" {
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

	computeSection := fmt.Sprintf(`      {{- if .Values.%s.compute }}
      nodeSelector:
        kindling.dev/compute: {{ .Values.%s.compute | quote }}
      tolerations:
      - key: kindling.dev/compute
        operator: Equal
        value: {{ .Values.%s.compute | quote }}
        effect: NoSchedule
      {{- end }}`, vk, vk, vk)

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
%s
      containers:
      - name: %s
        image: {{ .Values.%s.image }}
        imagePullPolicy: Always
        ports:
        - containerPort: {{ .Values.%s.port }}
%s`, safe, safe, chartName, vk, safe, safe, computeSection, safe, vk, vk, envSection)
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

// helmSecretsTemplate generates a K8s Secret resource for a service's
// secret-backed env vars. Returns "" if the service has no secrets.
func helmSecretsTemplate(dse snapshotDSE, chartName string) string {
	var secretKeys []snapshotEnvVar
	for _, e := range dse.Env {
		if e.IsSecret {
			secretKeys = append(secretKeys, e)
		}
	}
	if len(secretKeys) == 0 {
		return ""
	}

	safe := helmSafe(dse.Name)
	vk := helmValuesKey(dse.Name)

	var dataLines strings.Builder
	for _, e := range secretKeys {
		dataLines.WriteString(fmt.Sprintf("  %s: {{ .Values.%s.secrets.%s | quote }}\n", e.Name, vk, e.Name))
	}

	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: {{ .Release.Name }}-%s-secrets
  labels:
    app: %s
    {{- include "%s.labels" . | nindent 4 }}
type: Opaque
stringData:
%s`, safe, safe, chartName, dataLines.String())
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
      {{- range .Values.%s.ingress.routes }}
      - path: {{ .path }}
        pathType: {{ .pathType }}
        backend:
          service:
            name: {{ .service }}
            port:
              number: {{ .port }}
      {{- end }}
{{- end }}
`, vk, safe, safe, chartName, vk, vk, vk, tlsBlock, vk, vk, vk, safe, vk, vk)
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
			envLines.WriteString(fmt.Sprintf("        - name: %s\n          value: \"\"  # TODO: set your staging %s connection string\n",
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
		stagingImage(dse.Image, dse.Name), dse.Port, envSection)
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
		host = dse.Name + ".example.com  # TODO: set your staging hostname"
	}

	var extraPaths strings.Builder
	for _, route := range ing.Routes {
		extraPaths.WriteString(fmt.Sprintf(`      - path: %s
        pathType: %s
        backend:
          service:
            name: %s
            port:
              number: %d
`, route.Path, route.PathType, route.Service, route.Port))
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
%s`, dse.Name, classLine, tlsBlock, host, ing.Path, ing.PathType, dse.Name, dse.Port, extraPaths.String())
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

// yamlEscape escapes a string for safe inclusion in a YAML double-quoted value.
func yamlEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
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

// stagingImage converts local registry images to placeholder staging images.
// e.g. "localhost:5001/my-svc:123" → "my-svc:latest" (user replaces with their registry)
func stagingImage(image, name string) string {
	if strings.HasPrefix(image, "localhost:5001/") {
		return name + ":latest  # TODO: replace with your staging registry"
	}
	// If it's a kind-loaded tag like "my-svc:1234567", normalize
	if !strings.Contains(image, "/") && !strings.Contains(image, ":latest") {
		return name + ":latest  # TODO: replace with your staging registry"
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

// registryHost extracts the hostname (with port) from a registry string.
// e.g. "ghcr.io/myorg" → "ghcr.io", "jeffvincent" → "index.docker.io"
func registryHost(registry string) string {
	parts := strings.SplitN(registry, "/", 2)
	host := parts[0]
	if !strings.Contains(host, ".") {
		// Bare name like "jeffvincent" → Docker Hub
		return "index.docker.io"
	}
	return host
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
func craneCopyImages(dses []snapshotDSE, registry, tag, userPrefix, regUser, regPass string) error {
	// Check crane is installed
	if _, err := exec.LookPath("crane"); err != nil {
		return fmt.Errorf("crane is required for --registry (brew install crane)")
	}

	// Build a temporary Docker config dir so crane bypasses
	// Docker Desktop's "credsStore":"desktop" credential helper,
	// which crane can't resolve properly.
	var craneEnv []string
	if regUser != "" && regPass != "" {
		tmpDir, err := os.MkdirTemp("", "kindling-crane-*")
		if err != nil {
			return fmt.Errorf("cannot create temp docker config: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		// Write a minimal config with no credsStore
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(`{"auths":{}}`), 0600); err != nil {
			return fmt.Errorf("cannot write temp docker config: %w", err)
		}

		craneEnv = []string{"DOCKER_CONFIG=" + tmpDir}

		regHost := registryHost(registry)
		loginCmd := exec.Command("crane", "auth", "login", regHost, "-u", regUser, "-p", regPass)
		loginCmd.Env = append(os.Environ(), craneEnv...)
		if out, err := loginCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("crane auth login failed for %s: %w (output: %s)", regHost, err, string(out))
		}
		step("🔑", fmt.Sprintf("Authenticated to %s", regHost))
	}

	// runCrane executes crane with the isolated Docker config.
	runCrane := func(args ...string) (string, error) {
		cmd := exec.Command("crane", args...)
		if len(craneEnv) > 0 {
			cmd.Env = append(os.Environ(), craneEnv...)
		}
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
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

		// Prefer the in-cluster registry — images built by Kaniko in
		// the CI runner are linux/amd64 regardless of host arch. Docker
		// daemon images may be the host arch (arm64 on Apple Silicon)
		// which would cause "exec format error" on amd64 prod nodes.
		if !pushed && isClusterRegistryImage(dses[i].Image) {
			src := registryPullRef(dses[i].Image, localPort)
			if out, err := runCrane("copy", "--insecure", src, dst); err == nil {
				pushed = true
			} else {
				warn(fmt.Sprintf("crane copy failed for %s: %v (src=%s, output=%s)", dses[i].Name, err, src, out))
			}
		}

		// Fallback: Docker daemon images (e.g. for images loaded via
		// `kindling load` that aren't in the in-cluster registry).
		if !pushed {
			if localImg := findDockerImage(dses[i].Name, userPrefix); localImg != "" {
				step("🐳", fmt.Sprintf("Found %s in Docker daemon — pushing directly", localImg))
				if err := dockerTagAndPush(localImg, dst); err != nil {
					warn(fmt.Sprintf("Docker push failed for %s: %v", dses[i].Name, err))
				} else {
					pushed = true
				}
			}
		}

		if !pushed {
			warn(fmt.Sprintf("Could not push %s — not in registry or Docker daemon", dses[i].Name))
			failed = append(failed, dses[i].Name)
			// Do NOT rewrite the image ref on failure — leaving it pointed
			// at the staging destination tag would make the deploy
			// silently reuse whatever image already happens to exist there
			// (stale or wrong-arch), instead of surfacing the failure.
			continue
		}

		// Only rewrite the image ref once the push has actually succeeded.
		dses[i].Image = dst
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d/%d image(s) could not be pushed: %s — refusing to deploy, since the destination tag(s) may already reference a stale or wrong-architecture image",
			len(failed), len(seen), strings.Join(failed, ", "))
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
