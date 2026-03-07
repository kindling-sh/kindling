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

// ── 4. Deploy to cluster ────────────────────────────────────────

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
		// Ingress selection: disable unselected, clear hosts for selected
		for _, dse := range opts.DSEs {
			if dse.Ingress != nil && dse.Ingress.Enabled {
				vk := helmValuesKey(dse.Name)
				if !opts.SelectedIngress[dse.Name] {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.enabled=false", vk))
				} else {
					helmArgs = append(helmArgs, "--set", fmt.Sprintf("%s.ingress.host=", vk))
				}
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
