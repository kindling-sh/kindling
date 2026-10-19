package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ════════════════════════════════════════════════════════════════
// Shared snapshot pipeline functions
//
// Used by both the CLI `kindling snapshot` command and the dashboard
// deploy API. All the real logic lives here; callers just feed in
// parameters and a progress callback.
// ════════════════════════════════════════════════════════════════

// ── 1. Strip user prefix ────────────────────────────────────────

// StripDSEPrefix detects a common GitHub actor prefix across DSE names
// (e.g. "jeff-vincent-") and removes it from names, ingress hosts, and
// env var values. Returns the prefix that was stripped (empty if none).
func stripDSEPrefix(dses []snapshotDSE) string {
	prefix := detectUserPrefix(dses)
	if prefix == "" {
		return ""
	}
	for i := range dses {
		if stripped := strings.TrimPrefix(dses[i].Name, prefix); stripped != "" {
			dses[i].Name = stripped
		}
		if dses[i].Ingress != nil && dses[i].Ingress.Host != "" {
			dses[i].Ingress.Host = strings.TrimPrefix(dses[i].Ingress.Host, prefix)
		}
		if dses[i].Ingress != nil {
			for j := range dses[i].Ingress.Routes {
				if stripped := strings.TrimPrefix(dses[i].Ingress.Routes[j].Service, prefix); stripped != "" {
					dses[i].Ingress.Routes[j].Service = stripped
				}
			}
		}
		for j := range dses[i].Env {
			dses[i].Env[j].Value = strings.ReplaceAll(dses[i].Env[j].Value, prefix, "")
		}
	}
	return prefix
}

// ── 2. Push images ──────────────────────────────────────────────

// pushSnapshotImages pushes images from the local Kind cluster to a
// remote registry via crane/docker, then rewrites dse[i].Image to
// point at the destination. If any image fails to push, it returns an
// error instead of silently leaving the chart pointed at whatever
// image already happens to exist at the destination tag.
//
// progress is an optional callback for streaming status messages.
// Returns the pushed image digests (dse.Name -> "sha256:..."), needed by
// callers that also want to render production values (a digest-pinned
// image reference is required there, not just a tag).
func pushSnapshotImages(dses []snapshotDSE, registry, tag, userPrefix, regUser, regPass string, progress func(string)) (map[string]string, error) {
	if progress == nil {
		progress = func(string) {}
	}

	progress(fmt.Sprintf("Pushing images to %s (tag: %s)", registry, tag))

	digests, err := craneCopyImages(dses, registry, tag, userPrefix, regUser, regPass)
	if err != nil {
		return nil, err
	}

	progress("Images pushed to registry")
	return digests, nil
}

// ── 3. Export chart ─────────────────────────────────────────────

// exportSnapshot generates a Helm chart or Kustomize overlay into outDir.
func exportSnapshot(format, outDir, chartName string, dses []snapshotDSE) error {
	switch format {
	case "helm":
		return exportHelm(outDir, chartName, dses)
	case "kustomize":
		return exportKustomize(outDir, chartName, dses)
	default:
		return fmt.Errorf("unknown format %q — use 'helm' or 'kustomize'", format)
	}
}

// ── 4. Ensure ingress controller ────────────────────────────────

// ensureIngressController checks whether the target staging cluster
// has an ingress controller running. If not, it installs Traefik via
// Helm. progress is an optional callback for streaming status messages.
func ensureIngressController(context string, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}

	// Check for existing ingress controller by looking for IngressClass resources
	out, err := runSilent("kubectl", "--context", context, "get", "ingressclass", "-o", "jsonpath={.items[*].metadata.name}")
	if err == nil && strings.TrimSpace(out) != "" {
		progress(fmt.Sprintf("Ingress controller found: %s", strings.TrimSpace(out)))
		return nil
	}

	progress("No ingress controller detected — installing Traefik")

	if !commandExists("helm") {
		return fmt.Errorf("helm is required to install an ingress controller")
	}

	// Add Traefik repo
	if _, err := runSilent("helm", "repo", "add", "traefik", "https://traefik.github.io/charts"); err != nil {
		// Ignore "already exists" errors
		progress("Traefik Helm repo already configured")
	}
	if _, err := runSilent("helm", "repo", "update", "traefik"); err != nil {
		progress("Warning: could not update Traefik repo")
	}

	// Install Traefik
	_, err = runSilent("helm",
		"--kube-context", context,
		"install", "traefik", "traefik/traefik",
		"--namespace", "traefik",
		"--create-namespace",
		"--set", "ingressClass.enabled=true",
		"--set", "ingressClass.isDefaultClass=true",
		"--wait",
		"--timeout", "2m",
	)
	if err != nil {
		return fmt.Errorf("failed to install Traefik: %w", err)
	}

	progress("Traefik ingress controller installed")
	return nil
}

// ── 5. Detect IngressClass ───────────────────────────────────────

// detectIngressClass returns the name of the IngressClass to use on
// the target cluster. It prefers the default class; if there is exactly
// one class it returns that; if there are multiple non-default classes
// it returns the first one found. Returns "" only if no classes exist.
func detectIngressClass(kubeCtx string) string {
	// Try to get the default IngressClass first
	out, err := runSilent("kubectl", "--context", kubeCtx,
		"get", "ingressclass",
		"-o", `jsonpath={range .items[?(@.metadata.annotations.ingressclass\.kubernetes\.io/is-default-class=="true")]}{.metadata.name}{end}`)
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}

	// No default — list all classes
	out, err = runSilent("kubectl", "--context", kubeCtx,
		"get", "ingressclass",
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}

	classes := strings.Fields(strings.TrimSpace(out))
	if len(classes) == 1 {
		return classes[0]
	}
	// Multiple non-default classes — return the first
	return classes[0]
}

// ── 6. Deploy to cluster ────────────────────────────────────────

// DeployOpts carries the parameters for deploying a snapshot chart to
// a staging cluster.
type DeployOpts struct {
	Context         string
	Namespace       string
	Format          string                             // "helm" or "kustomize"
	OutDir          string                             // path to the generated chart
	ChartName       string                             // Helm release name
	DSEs            []snapshotDSE                      // for ingress flag generation
	SelectedIngress map[string]bool                    // services to enable ingress for
	IngressClass    string                             // IngressClass name for the target cluster
	IngressHost     string                             // branch-derived fallback host (deriveIngressHost) for a selected service with no Ingress host of its own already set
	CredOverrides   map[string]map[string]credOverride // valuesKey → envVar → stagingValue
}

// deploySnapshot runs helm upgrade --install or kubectl apply -k
// against the target cluster.
func deploySnapshot(opts DeployOpts) (string, error) {
	switch opts.Format {
	case "helm":
		if !commandExists("helm") {
			return "", fmt.Errorf("helm not found on PATH")
		}
		helmArgs := []string{
			"upgrade", "--install", opts.ChartName, opts.OutDir,
			"--kube-context", opts.Context,
			"--namespace", opts.Namespace,
			"--create-namespace",
			"--timeout", "10m",
		}
		// Include values-live.yaml if it exists (Helm export generates it)
		valuesLive := filepath.Join(opts.OutDir, "values-live.yaml")
		if fileExists(valuesLive) {
			helmArgs = append(helmArgs, "-f", valuesLive)
		}
		// Apply staging credential overrides (takes precedence over values-live.yaml)
		if len(opts.CredOverrides) > 0 {
			credsFile, err := writeCredsOverrideFile(opts.CredOverrides)
			if err != nil {
				return "", fmt.Errorf("cannot write credential overrides: %w", err)
			}
			defer func() { os.Remove(credsFile) }()
			helmArgs = append(helmArgs, "-f", credsFile)
		}
		// Ingress selection: enable/disable for all services based on user selection.
		for _, dse := range opts.DSEs {
			helmArgs = append(helmArgs, ingressHelmArgs(dse, opts.SelectedIngress[dse.Name], opts.IngressClass, opts.IngressHost)...)
		}
		return runSilent("helm", helmArgs...)

	case "kustomize":
		return runSilent("kubectl", "--context", opts.Context, "apply",
			"-k", opts.OutDir, "-n", opts.Namespace)

	default:
		return "", fmt.Errorf("unknown format %q", opts.Format)
	}
}

// ingressHelmArgs builds the `--set <vk>.ingress.*` helm args for a
// single service, pulled out of deploySnapshot as a pure function so the
// host-precedence rule below is unit-testable without exec'ing helm.
//
// Host precedence: when ingressHost is empty (no staging domain was
// configured for this deploy -- the long-standing default), every
// selected service gets a blank host, identical to this function's
// behavior before IngressHost existed on DeployOpts -- a caller that
// hasn't opted into branch-derived naming sees zero change. When
// ingressHost is set, it's used for every selected service *except* one
// that already has a genuinely custom (non-".localhost") host of its
// own -- that explicit intent is still never overridden, matching
// applyBranchIngressHost's own "never override explicit intent" rule,
// without needing this path to share its fail-fast behavior (a
// dashboard deploy with no staging domain set must never start failing
// deploys that worked before this field existed, even though every dev
// DSE typically already has a ".localhost" ingress host).
func ingressHelmArgs(dse snapshotDSE, selected bool, ingressClass, ingressHost string) []string {
	vk := helmValuesKey(dse.Name)
	if !selected {
		return []string{"--set", fmt.Sprintf("%s.ingress.enabled=false", vk)}
	}

	args := []string{"--set", fmt.Sprintf("%s.ingress.enabled=true", vk)}
	host := ingressHost
	if host != "" && dse.Ingress != nil && dse.Ingress.Host != "" && !strings.HasSuffix(dse.Ingress.Host, ".localhost") {
		host = dse.Ingress.Host
	}
	args = append(args, "--set", fmt.Sprintf("%s.ingress.host=%s", vk, host))
	if ingressClass != "" {
		args = append(args, "--set", fmt.Sprintf("%s.ingress.ingressClassName=%s", vk, ingressClass))
	}
	return args
}
