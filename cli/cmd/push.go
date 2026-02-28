package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var pushCmd = &cobra.Command{
	Use:   "push [git push args]",
	Short: "Git push with selective service rebuild",
	Long: `Wraps git push, optionally tagging the HEAD commit message with a
[kindling:service1,service2] marker so the CI workflow only rebuilds
and redeploys the named services.

Without --service the full pipeline runs (all services).

Examples:
  kindling push                              # push + rebuild everything
  kindling push --service orders             # push + rebuild orders only
  kindling push -s orders -s gateway         # push + rebuild orders & gateway
  kindling push -s ui -- origin my-branch    # extra git push args after --`,
	RunE:               runPush,
	DisableFlagParsing: false,
}

var pushServices []string

func init() {
	pushCmd.Flags().StringArrayVarP(&pushServices, "service", "s", nil,
		`Service(s) to rebuild (repeatable, or comma-separated).
Omit to rebuild all services.`)
	rootCmd.AddCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	// ── Pre-flight: check for missing secrets ───────────────────
	missing := checkWorkflowSecrets()
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "\n  %s%s⚠️  Missing secrets detected%s\n\n", colorBold, colorYellow, colorReset)
		for _, name := range missing {
			fmt.Fprintf(os.Stderr, "  %s❌ %s%s — pod will crash without this secret\n",
				colorRed, name, colorReset)
			fmt.Fprintf(os.Stderr, "     %s→ kindling secrets set %s <value>%s\n",
				colorDim, name, colorReset)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "  Push anyway? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintf(os.Stderr, "  %sAborted.%s Set the missing secrets and try again.\n\n", colorYellow, colorReset)
			return nil
		}
		fmt.Fprintln(os.Stderr)
	}

	// ── Normalise service list (allow -s "orders,gateway") ──────
	services := normaliseServices(pushServices)

	// ── If services specified, amend HEAD commit message ────────
	if len(services) > 0 {
		tag := "[kindling:" + strings.Join(services, ",") + "]"

		// Read current HEAD message
		msg, err := runCapture("git", "log", "-1", "--format=%B")
		if err != nil {
			return fmt.Errorf("cannot read HEAD commit message: %w", err)
		}

		// Strip any existing [kindling:...] tag so we don't stack them
		cleaned := stripKindlingTag(msg)

		newMsg := strings.TrimRight(cleaned, "\n") + "\n\n" + tag
		header("Selective push")
		step("🏷️ ", fmt.Sprintf("Tagging commit for: %s", strings.Join(services, ", ")))

		if err := runGit("commit", "--amend", "-m", newMsg); err != nil {
			return fmt.Errorf("failed to amend commit: %w", err)
		}
	} else {
		header("Pushing (full rebuild)")
	}

	// ── git push (pass through any extra args) ──────────────────
	pushArgs := append([]string{"push"}, args...)
	step("🚀", fmt.Sprintf("git %s", strings.Join(pushArgs, " ")))
	if err := runGit(pushArgs...); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}

	if len(services) > 0 {
		success(fmt.Sprintf("Pushed — only %s will rebuild", strings.Join(services, ", ")))
	} else {
		success("Pushed — full pipeline will run")
	}
	return nil
}

// normaliseServices splits comma-separated values and deduplicates.
func normaliseServices(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range raw {
		for _, part := range strings.Split(s, ",") {
			part = strings.TrimSpace(part)
			if part != "" && !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	return out
}

// stripKindlingTag removes any existing [kindling:...] marker.
func stripKindlingTag(msg string) string {
	lines := strings.Split(msg, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[kindling:") && strings.HasSuffix(trimmed, "]") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// runGit runs a git command, inheriting stdio.
func runGit(args ...string) error {
	c := exec.Command("git", args...)
	c.Dir, _ = os.Getwd()
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

// checkWorkflowSecrets scans the generated CI workflow for secretKeyRef
// entries and verifies each referenced K8s secret exists in the cluster.
// Returns the names of any missing secrets. Silently returns nil if no
// workflow is found or the cluster is unreachable.
func checkWorkflowSecrets() []string {
	// Find the workflow file
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	workflowPaths := []string{
		filepath.Join(cwd, ".github", "workflows", "dev-deploy.yml"),
		filepath.Join(cwd, ".github", "workflows", "dev-deploy.yaml"),
		filepath.Join(cwd, ".gitlab-ci.yml"),
	}

	var workflowContent string
	for _, p := range workflowPaths {
		data, err := os.ReadFile(p)
		if err == nil {
			workflowContent = string(data)
			break
		}
	}
	if workflowContent == "" {
		return nil
	}

	// Extract secretKeyRef names from the workflow
	requiredSecrets := extractSecretKeyRefNames(workflowContent)
	if len(requiredSecrets) == 0 {
		return nil
	}

	// Check which exist in the cluster — look for both the bare name
	// (e.g. openai-api-key) and the kindling-prefixed form
	// (kindling-secret-openai-api-key), since users create secrets via
	// `kindling secrets set` which adds the prefix.
	clusterSecrets := listClusterSecrets()
	var missing []string
	for _, name := range requiredSecrets {
		if clusterSecrets[name] || clusterSecrets["kindling-secret-"+name] {
			continue
		}
		missing = append(missing, name)
	}
	return missing
}

// extractSecretKeyRefNames parses YAML content for secretKeyRef blocks
// and returns the K8s secret names referenced.
func extractSecretKeyRefNames(content string) []string {
	var names []string
	seen := make(map[string]bool)
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "secretKeyRef:" && i+1 < len(lines) {
			// Next line should be "name: <secret-name>"
			nextLine := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(nextLine, "name:") {
				name := strings.TrimSpace(strings.TrimPrefix(nextLine, "name:"))
				if name != "" && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names
}
