package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── Knowledge pack system prompt ────────────────────────────────

const hardenAgentSystemPrompt = `You are kindling's production-readiness advisor. The developer has a working
application and just ran "kindling harden" which flagged issues. Your job is to
help them fix each issue WITHOUT breaking their app.

## Core principles

1. **The app works today.** Every suggestion must preserve that. If a fix has
   a risk of breaking something, call it out explicitly with a rollback step.
2. **Explain the "why" first.** Developers accept changes they understand.
   Skip "you should" — say "this matters because…"
3. **Give the exact code.** Not "consider adding a USER directive" — give the
   actual Dockerfile lines, in context, with a before/after.
4. **One thing at a time.** Each fix should be independently deployable. Never
   bundle unrelated changes.
5. **Test command included.** Every fix ends with how to verify it worked.

## kindling context

- Builds use Kaniko (not Docker BuildKit). No TARGETARCH, no --mount=type=cache,
  no .git directory → Go needs -buildvcs=false.
- The in-cluster registry is localhost:5001.
- Dependencies (postgres, redis, etc.) are declared in spec.dependencies[] and
  auto-inject connection env vars (DATABASE_URL, REDIS_URL, etc.).
- Secrets go through "kindling secrets set KEY VALUE" → K8s secretKeyRef.
- "kindling load -s <svc>" rebuilds a single service image.
- "kindling deploy -f <dse.yaml>" redeploys.
- "kindling diagnose" checks runtime health after changes.

## Fix patterns that commonly break things

### Adding USER (non-root container)
- BREAKS if the app writes to directories owned by root (logs, uploads, tmp).
  Fix: chown those dirs in the Dockerfile BEFORE the USER directive.
- BREAKS if the app binds to port <1024. Fix: use port 8080+ or
  setcap 'cap_net_bind_service=+ep' on the binary.
- BREAKS npm install if node_modules is volume-mounted as root.
  Fix: RUN mkdir -p /home/appuser/.npm && chown -R appuser /home/appuser

### Pinning base images
- BREAKS nothing, but picking the wrong tag means no security patches.
  Use the specific minor version tag (e.g., python:3.11-slim, not python:3.11.4).
- For multi-platform, prefer images without platform suffix — Kaniko handles it.

### Adding HEALTHCHECK
- BREAKS if the check path returns non-200 before the app finishes starting.
  Fix: add --start-period=30s and use a lightweight /health or /healthz endpoint.
- BREAKS if curl isn't in the image. Fix: use wget, or for distroless use
  HEALTHCHECK CMD ["/app", "--health"] if supported.

### Multi-stage builds
- BREAKS if runtime dependencies (shared libs) aren't copied from build stage.
  Fix: use ldd to find shared libraries, or just use -slim instead of
  distroless if you're not sure.
- BREAKS if ENV vars set in build stage aren't re-set in runtime stage.

### Removing eval/shell=True
- BREAKS if the code legitimately needs dynamic evaluation (template engines,
  plugin systems). Flag those as false positives in harden.yaml:
  overrides: { no-eval: "off" }

### Graceful shutdown
- BREAKS if the signal handler doesn't drain in-flight requests.
  Fix: signal handler should 1) stop accepting new connections, 2) wait for
  current requests, 3) close DB connections, 4) exit.
- Different frameworks have different patterns — give the framework-specific one.

### Resource limits
- BREAKS if set too low (OOMKilled, CPU throttling).
  Fix: start generous (512Mi/500m), watch metrics, tighten later.
  kindling diagnose will catch OOMKilled pods.

### Structured logging
- BREAKS nothing, but migrating mid-project is tedious. Suggest the minimal
  drop-in replacement: Python→structlog, Node→pino, Go→slog.

## Output format

For each finding, output:

### [rule-id] short title
**Why this matters:** 1-2 sentences.
**Risk of fixing:** what could break, and what to check.
**Fix:**
` + "```" + `diff
- old code
+ new code
` + "```" + `
**Verify:** command to confirm the fix works.

---

If a finding is a false positive for this specific codebase, say so and show
the harden.yaml override to silence it.
End with a summary: "X findings addressed. Run kindling harden again to verify."
`

// ── Agent runner ────────────────────────────────────────────────

func runHardenAgent(
	repoPath string,
	findings []hardenFinding,
	repoCtx *repoContext,
	apiKey, provider, model string,
) error {
	if len(findings) == 0 {
		fmt.Fprintf(os.Stderr, "\n  %s✅ Nothing to fix — kindling harden found no issues%s\n\n",
			colorGreen, colorReset)
		return nil
	}

	header("Preparing context for AI advisor")

	userPrompt := buildHardenAgentPrompt(repoPath, findings, repoCtx)

	step("🤖", fmt.Sprintf("Provider: %s, Model: %s", provider, model))
	step("📋", fmt.Sprintf("%d finding(s) to analyze", len(findings)))
	step("⏳", "Calling API — generating remediation plan...")

	response, err := callGenAI(provider, apiKey, model, hardenAgentSystemPrompt, userPrompt)
	if err != nil {
		return fmt.Errorf("AI call failed: %w", err)
	}

	// Write remediation plan
	fmt.Fprintf(os.Stderr, "\n")
	header("Remediation plan")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Println(response)

	// Also save to file
	planPath := filepath.Join(repoPath, ".kindling", "harden-plan.md")
	kindlingDir := filepath.Join(repoPath, ".kindling")
	if err := os.MkdirAll(kindlingDir, 0755); err == nil {
		if err := os.WriteFile(planPath, []byte(response+"\n"), 0644); err == nil {
			fmt.Fprintf(os.Stderr, "\n  %s📄 Plan saved to %s.kindling/harden-plan.md%s\n\n",
				colorDim, colorCyan, colorReset)
		}
	}

	return nil
}

// buildHardenAgentPrompt constructs the user prompt with findings and
// relevant code snippets so the AI has full context.
func buildHardenAgentPrompt(
	repoPath string,
	findings []hardenFinding,
	repoCtx *repoContext,
) string {
	var b strings.Builder

	b.WriteString("# kindling harden findings\n\n")
	b.WriteString("The developer ran `kindling harden` on their project and got the following findings.\n")
	b.WriteString("Provide a remediation plan for each one.\n\n")

	// Group by category
	categoryOrder := []hardenCategory{catSecurity, catContainers, catScalability, catPerformance, catObservability, catCIHygiene}
	categoryNames := map[hardenCategory]string{
		catSecurity:      "Security",
		catContainers:    "Container Best Practices",
		catScalability:   "Scalability",
		catPerformance:   "Performance",
		catObservability: "Observability",
		catCIHygiene:     "CI Hygiene",
	}

	for _, cat := range categoryOrder {
		var catFindings []hardenFinding
		for _, f := range findings {
			if f.Category == cat {
				catFindings = append(catFindings, f)
			}
		}
		if len(catFindings) == 0 {
			continue
		}

		b.WriteString(fmt.Sprintf("## %s\n\n", categoryNames[cat]))
		for i, f := range catFindings {
			b.WriteString(fmt.Sprintf("%d. **[%s]** %s", i+1, f.RuleID, f.Message))
			if f.File != "" {
				b.WriteString(fmt.Sprintf(" (file: `%s`)", f.File))
			}
			b.WriteString("\n")
			if f.Fix != "" {
				b.WriteString(fmt.Sprintf("   Suggested fix: `%s`\n", f.Fix))
			}
		}
		b.WriteString("\n")
	}

	// Include relevant file contents
	b.WriteString("---\n\n# Relevant source files\n\n")

	// Collect files referenced by findings
	referencedFiles := make(map[string]bool)
	for _, f := range findings {
		if f.File != "" {
			referencedFiles[f.File] = true
		}
	}

	// Dockerfiles
	for path, content := range repoCtx.dockerfiles {
		b.WriteString(fmt.Sprintf("## %s\n```dockerfile\n%s\n```\n\n", path, content))
	}

	// Source files referenced in findings
	for path := range referencedFiles {
		// Check source snippets
		if content, ok := repoCtx.sourceSnippets[path]; ok {
			b.WriteString(fmt.Sprintf("## %s\n```\n%s\n```\n\n", path, content))
			continue
		}
		// Try to read the file directly (short excerpt)
		fullPath := filepath.Join(repoPath, path)
		if data, err := os.ReadFile(fullPath); err == nil {
			content := string(data)
			// Truncate very large files
			if len(content) > 4000 {
				content = content[:4000] + "\n\n... (truncated)"
			}
			b.WriteString(fmt.Sprintf("## %s\n```\n%s\n```\n\n", path, content))
		}
	}

	// Dependency files (for context)
	for path, content := range repoCtx.depFiles {
		if len(content) > 2000 {
			content = content[:2000] + "\n... (truncated)"
		}
		b.WriteString(fmt.Sprintf("## %s\n```\n%s\n```\n\n", path, content))
	}

	// DSE files
	dseFiles := findDSEFiles(repoPath)
	for _, dsePath := range dseFiles {
		if data, err := os.ReadFile(dsePath); err == nil {
			relPath, _ := filepath.Rel(repoPath, dsePath)
			b.WriteString(fmt.Sprintf("## %s\n```yaml\n%s\n```\n\n", relPath, string(data)))
		}
	}

	return b.String()
}
