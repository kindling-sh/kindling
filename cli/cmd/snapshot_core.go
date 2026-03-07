package cmd

import (
	"fmt"
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
		for j := range dses[i].Env {
			dses[i].Env[j].Value = strings.ReplaceAll(dses[i].Env[j].Value, prefix, "")
		}
	}
	return prefix
}

// ── 2. Push images ──────────────────────────────────────────────

// pushSnapshotImages pushes images from the local Kind cluster to a
// remote registry via crane/docker, then rewrites dse[i].Image to
// point at the destination. On total failure the image refs are still
// rewritten so the generated chart references the correct registry.
//
// progress is an optional callback for streaming status messages.
func pushSnapshotImages(dses []snapshotDSE, registry, tag, userPrefix string, progress func(string)) {
	if progress == nil {
		progress = func(string) {}
	}

	progress(fmt.Sprintf("Pushing images to %s (tag: %s)", registry, tag))

	err := craneCopyImages(dses, registry, tag, userPrefix)
	if err != nil {
		progress(fmt.Sprintf("Image push encountered errors: %v — falling back to ref-only rewrite", err))
		// Fallback: rewrite image refs so the chart targets the right
		// registry even though the push didn't succeed for everything.
		for i := range dses {
			dses[i].Image = registryImage(dses[i].Name, registry, tag)
		}
	} else {
		progress("Images pushed to registry")
	}
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

// ensureIngressController checks whether the target production cluster
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
// a production cluster.
type DeployOpts struct {
	Context         string
	Namespace       string
	Format          string          // "helm" or "kustomize"
	OutDir          string          // path to the generated chart
	ChartName       string          // Helm release name
	DSEs            []snapshotDSE   // for ingress flag generation
	SelectedIngress map[string]bool // services to enable ingress for
	IngressClass    string          // IngressClass name for the target cluster
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
		// Ingress selection: enable/disable for all services based on user selection
		for _, dse := range opts.DSEs {
			vk := helmValuesKey(dse.Name)
			if opts.SelectedIngress[dse.Name] {
				helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.enabled=true", vk))
				helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.host=", vk))
				if opts.IngressClass != "" {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.ingressClassName=%s", vk, opts.IngressClass))
				}
			} else {
				helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.enabled=false", vk))
			}
		}
		return runSilent("helm", helmArgs...)

	case "kustomize":
		return runSilent("kubectl", "--context", opts.Context, "apply",
			"-k", opts.OutDir, "-n", opts.Namespace)

	default:
		return "", fmt.Errorf("unknown format %q", opts.Format)
	}
}
