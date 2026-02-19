package cmd

import (
	"fmt"
	"os"
	"os/exec"
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
