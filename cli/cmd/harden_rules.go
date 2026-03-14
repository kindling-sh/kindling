package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ── Security checks ─────────────────────────────────────────────

func hardenCheckSecurity(repoPath string, ctx *repoContext, cfg *hardenConfig) []hardenFinding {
	var findings []hardenFinding

	// Collect all scannable content
	allContent := make(map[string]string)
	for k, v := range ctx.sourceSnippets {
		allContent[k] = v
	}
	for k, v := range ctx.dockerfiles {
		allContent[k] = v
	}
	for k, v := range ctx.depFiles {
		allContent[k] = v
	}

	findings = append(findings, hardenHardcodedSecrets(allContent)...)
	findings = append(findings, hardenEnvFilesInGit(repoPath)...)
	findings = append(findings, hardenGitignoreHygiene(repoPath)...)
	findings = append(findings, hardenDependencyPinning(ctx)...)
	findings = append(findings, hardenCodePatterns(allContent)...)

	return findings
}

// hardenHardcodedSecrets detects inline credentials in source files.
func hardenHardcodedSecrets(content map[string]string) []hardenFinding {
	var findings []hardenFinding

	secretPatterns := []struct {
		pattern string
		label   string
	}{
		{"sk-", "OpenAI API key"},
		{"sk_live_", "Stripe live key"},
		{"sk_test_", "Stripe test key"},
		{"ghp_", "GitHub personal access token"},
		{"gho_", "GitHub OAuth token"},
		{"glpat-", "GitLab personal access token"},
		{"xoxb-", "Slack bot token"},
		{"xoxp-", "Slack user token"},
		{"AKIA", "AWS access key ID"},
		{"mongodb+srv://", "MongoDB connection string"},
		{"postgres://", "PostgreSQL connection string"},
		{"redis://:", "Redis connection string with password"},
		{"-----BEGIN RSA PRIVATE KEY", "RSA private key"},
		{"-----BEGIN OPENSSH PRIVATE KEY", "SSH private key"},
		{"-----BEGIN EC PRIVATE KEY", "EC private key"},
	}

	seen := make(map[string]bool)
	for file, body := range content {
		if strings.HasPrefix(filepath.Base(file), ".env") {
			continue
		}
		for _, sp := range secretPatterns {
			if strings.Contains(body, sp.pattern) && !seen[sp.label] {
				seen[sp.label] = true
				findings = append(findings, hardenFinding{
					RuleID:   "no-hardcoded-secrets",
					Category: catSecurity,
					Severity: ruleSevError,
					File:     file,
					Message:  fmt.Sprintf("Possible %s inline — move to kindling secrets", sp.label),
					Fix:      fmt.Sprintf("kindling secrets set %s <value>", strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(sp.label, " ", "_"), "-", "_"))),
				})
			}
		}
	}

	return findings
}

// hardenEnvFilesInGit checks if .env files are tracked in git.
func hardenEnvFilesInGit(repoPath string) []hardenFinding {
	var findings []hardenFinding

	envFiles := []string{".env", ".env.local", ".env.production"}
	for _, ef := range envFiles {
		fullPath := filepath.Join(repoPath, ef)
		if _, err := os.Stat(fullPath); err != nil {
			continue
		}
		if !isGitignored(repoPath, ef) {
			findings = append(findings, hardenFinding{
				RuleID:   "no-env-files-in-git",
				Category: catSecurity,
				Severity: ruleSevError,
				File:     ef,
				Message:  "Exists and isn't gitignored — secrets could be pushed to remote",
				Fix:      fmt.Sprintf("echo '%s' >> .gitignore && git rm --cached %s", ef, ef),
			})
		}
	}

	return findings
}

// hardenGitignoreHygiene checks .gitignore covers sensitive paths.
func hardenGitignoreHygiene(repoPath string) []hardenFinding {
	var findings []hardenFinding

	gitignorePath := filepath.Join(repoPath, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		findings = append(findings, hardenFinding{
			RuleID:   "gitignore-hygiene",
			Category: catSecurity,
			Severity: ruleSevWarning,
			Message:  "No .gitignore found — .env files and credentials could end up in git",
			Fix:      "echo '.env\\n.env.*\\n*.pem\\n*.key' >> .gitignore",
		})
		return findings
	}

	gitignore := string(content)
	if !strings.Contains(gitignore, ".env") {
		findings = append(findings, hardenFinding{
			RuleID:   "gitignore-hygiene",
			Category: catSecurity,
			Severity: ruleSevWarning,
			Message:  ".gitignore doesn't mention .env files",
			Fix:      "echo '.env\\n.env.*' >> .gitignore",
		})
	}

	return findings
}

// hardenDependencyPinning checks for unpinned dependencies.
func hardenDependencyPinning(ctx *repoContext) []hardenFinding {
	var findings []hardenFinding

	for path, content := range ctx.depFiles {
		name := filepath.Base(path)

		switch name {
		case "requirements.txt":
			unpinned := 0
			for _, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
					continue
				}
				if !strings.Contains(line, "==") && !strings.Contains(line, ">=") {
					unpinned++
				}
			}
			if unpinned > 0 {
				findings = append(findings, hardenFinding{
					RuleID:   "dependency-pinning",
					Category: catSecurity,
					Severity: ruleSevWarning,
					File:     path,
					Message:  fmt.Sprintf("%d unpinned dependencies — builds could break on new releases", unpinned),
					Fix:      "pip freeze > requirements.txt",
				})
			}

		case "package.json":
			if strings.Contains(content, `"*"`) || strings.Contains(content, `": "latest"`) {
				findings = append(findings, hardenFinding{
					RuleID:   "dependency-pinning",
					Category: catSecurity,
					Severity: ruleSevWarning,
					File:     path,
					Message:  "Wildcard or 'latest' dependency versions — pinning or using ranges is safer",
				})
			}
		}
	}

	return findings
}

// hardenCodePatterns scans source for security anti-patterns.
func hardenCodePatterns(content map[string]string) []hardenFinding {
	var findings []hardenFinding
	seen := make(map[string]bool)

	for file, body := range content {
		lower := strings.ToLower(body)

		// eval()
		if !seen["eval"] && strings.Contains(body, "eval(") {
			for _, line := range strings.Split(body, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.Contains(trimmed, "eval(") &&
					!strings.HasPrefix(trimmed, "#") &&
					!strings.HasPrefix(trimmed, "//") &&
					!strings.HasPrefix(trimmed, "*") {
					seen["eval"] = true
					findings = append(findings, hardenFinding{
						RuleID:   "no-eval",
						Category: catSecurity,
						Severity: ruleSevWarning,
						File:     file,
						Message:  "eval() usage — code injection vector if it touches user input",
					})
					break
				}
			}
		}

		// Shell injection
		if !seen["shell"] && (strings.Contains(body, "shell=True") || strings.Contains(body, "os.system(")) {
			seen["shell"] = true
			findings = append(findings, hardenFinding{
				RuleID:   "no-shell-injection",
				Category: catSecurity,
				Severity: ruleSevWarning,
				File:     file,
				Message:  "Shell execution — ensure no user input flows into these commands",
			})
		}

		// SQL concatenation
		if !seen["sqli"] && (strings.Contains(body, `"SELECT `) || strings.Contains(body, `'SELECT `)) &&
			(strings.Contains(body, `" + `) || strings.Contains(body, `' + `) ||
				strings.Contains(body, "f\"SELECT") || strings.Contains(body, "f'SELECT")) {
			seen["sqli"] = true
			findings = append(findings, hardenFinding{
				RuleID:   "no-sql-concatenation",
				Category: catSecurity,
				Severity: ruleSevWarning,
				File:     file,
				Message:  "SQL string concatenation — use parameterized queries instead",
			})
		}

		// CORS wildcard
		if !seen["cors"] && (strings.Contains(body, `"*"`) || strings.Contains(body, "'*'")) &&
			(strings.Contains(lower, "cors") || strings.Contains(lower, "access-control-allow-origin")) {
			seen["cors"] = true
			findings = append(findings, hardenFinding{
				RuleID:   "cors-wildcard",
				Category: catSecurity,
				Severity: ruleSevInfo,
				File:     file,
				Message:  "CORS wildcard (*) — fine for dev, lock it down for production",
			})
		}

		// Debug mode
		if !seen["debug"] && (strings.Contains(lower, "debug=true") || strings.Contains(lower, "debug = true") ||
			strings.Contains(lower, "app.debug = true") || strings.Contains(lower, "debug=1")) {
			seen["debug"] = true
			findings = append(findings, hardenFinding{
				RuleID:   "debug-mode-off",
				Category: catSecurity,
				Severity: ruleSevInfo,
				File:     file,
				Message:  "Debug mode enabled — make sure it's off before production",
			})
		}
	}

	return findings
}

// ── Container best practices ────────────────────────────────────

func hardenCheckContainers(ctx *repoContext, cfg *hardenConfig) []hardenFinding {
	var findings []hardenFinding

	for path, content := range ctx.dockerfiles {
		lines := strings.Split(content, "\n")
		hasUser := false
		usesLatest := false
		hasHealthcheck := false
		isMultiStage := false
		stageCount := 0
		hasSecretsCopy := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			upper := strings.ToUpper(trimmed)

			if strings.HasPrefix(upper, "USER ") {
				hasUser = true
			}

			if strings.HasPrefix(upper, "HEALTHCHECK ") {
				hasHealthcheck = true
			}

			if strings.HasPrefix(upper, "FROM ") {
				stageCount++
				if stageCount > 1 {
					isMultiStage = true
				}
				fromParts := strings.Fields(trimmed)
				if len(fromParts) >= 2 {
					image := fromParts[1]
					if strings.HasSuffix(image, ":latest") ||
						(!strings.Contains(image, ":") && !strings.Contains(image, "$") && !strings.HasPrefix(image, "--")) {
						usesLatest = true
					}
				}
			}

			// Check for copying sensitive files
			if strings.HasPrefix(upper, "COPY ") || strings.HasPrefix(upper, "ADD ") {
				lowerLine := strings.ToLower(trimmed)
				if strings.Contains(lowerLine, ".env") ||
					strings.Contains(lowerLine, "*.key") ||
					strings.Contains(lowerLine, "*.pem") ||
					strings.Contains(lowerLine, "id_rsa") {
					hasSecretsCopy = true
				}
			}
		}

		if !hasUser {
			findings = append(findings, hardenFinding{
				RuleID:   "no-root-container",
				Category: catContainers,
				Severity: ruleSevError,
				File:     path,
				Message:  "Runs as root — add a USER directive for a non-root user",
				Fix:      "Add to Dockerfile: RUN adduser -D appuser && USER appuser",
			})
		}

		if usesLatest {
			findings = append(findings, hardenFinding{
				RuleID:   "pin-base-images",
				Category: catContainers,
				Severity: ruleSevWarning,
				File:     path,
				Message:  "Unpinned base image — pin to a specific tag to avoid surprise breakage",
			})
		}

		if !isMultiStage && stageCount == 1 {
			// Check if this might benefit from multi-stage
			if strings.Contains(content, "gcc") || strings.Contains(content, "build-essential") ||
				strings.Contains(content, "npm install") || strings.Contains(content, "go build") ||
				strings.Contains(content, "cargo build") || strings.Contains(content, "maven") ||
				strings.Contains(content, "gradle") {
				findings = append(findings, hardenFinding{
					RuleID:   "multi-stage-build",
					Category: catContainers,
					Severity: ruleSevInfo,
					File:     path,
					Message:  "Single-stage build with build tools — multi-stage would reduce image size",
				})
			}
		}

		// Minimal base image suggestion
		if strings.Contains(content, "FROM ubuntu") || strings.Contains(content, "FROM debian") {
			findings = append(findings, hardenFinding{
				RuleID:   "minimal-base-image",
				Category: catContainers,
				Severity: ruleSevInfo,
				File:     path,
				Message:  "Using full OS base image — consider alpine or distroless for smaller attack surface",
			})
		}

		if hasSecretsCopy {
			findings = append(findings, hardenFinding{
				RuleID:   "no-secrets-in-layers",
				Category: catContainers,
				Severity: ruleSevError,
				File:     path,
				Message:  "Copying secret files (.env, .key, .pem) into image layers — these persist in layer history",
				Fix:      "Use kindling secrets set instead, or mount as K8s secrets at runtime",
			})
		}

		if !hasHealthcheck {
			findings = append(findings, hardenFinding{
				RuleID:   "healthcheck-present",
				Category: catContainers,
				Severity: ruleSevWarning,
				File:     path,
				Message:  "No HEALTHCHECK instruction — K8s probes handle this, but Docker standalone won't auto-restart",
			})
		}
	}

	// Check for securityContext recommendations (scan DSE files if present)
	findings = append(findings, hardenCheckSecurityContext(ctx)...)

	return findings
}

// hardenCheckSecurityContext scans for DSE YAML files and checks security settings.
func hardenCheckSecurityContext(ctx *repoContext) []hardenFinding {
	var findings []hardenFinding

	// Look for .kindling/dev-environment.yaml or similar
	for path, content := range ctx.depFiles {
		if !strings.Contains(path, "environment") || !strings.HasSuffix(path, ".yaml") {
			continue
		}
		if !strings.Contains(content, "securityContext") {
			findings = append(findings, hardenFinding{
				RuleID:   "drop-capabilities",
				Category: catContainers,
				Severity: ruleSevInfo,
				File:     path,
				Message:  "No securityContext — consider adding capabilities.drop: [ALL] and readOnlyRootFilesystem",
			})
		}
	}

	return findings
}

// ── Scalability checks ──────────────────────────────────────────

func hardenCheckScalability(ctx *repoContext, cfg *hardenConfig) []hardenFinding {
	var findings []hardenFinding
	seen := make(map[string]bool)

	for file, body := range ctx.sourceSnippets {
		lower := strings.ToLower(body)

		// Graceful shutdown (check for SIGTERM handlers)
		if !seen["sigterm"] {
			hasSigHandler := strings.Contains(body, "signal.Notify") || // Go
				strings.Contains(body, "process.on('SIGTERM'") || strings.Contains(body, `process.on("SIGTERM"`) || // Node
				strings.Contains(body, "signal.signal(signal.SIGTERM") || // Python
				strings.Contains(body, "Signal.trap") // Ruby

			if !hasSigHandler {
				// Only flag if this looks like a server entry point
				if strings.Contains(lower, "listen(") || strings.Contains(body, "app.run(") ||
					strings.Contains(body, "http.ListenAndServe") || strings.Contains(body, "uvicorn.run") {
					seen["sigterm"] = true
					findings = append(findings, hardenFinding{
						RuleID:   "graceful-shutdown",
						Category: catScalability,
						Severity: ruleSevWarning,
						File:     file,
						Message:  "Server with no SIGTERM handler — pods get SIGKILL after 30s grace period",
					})
				}
			}
		}

		// Stateless services — local file writes in handlers
		if !seen["stateless"] {
			// Check for file writing patterns in what looks like handler code
			hasFileWrite := false
			if strings.Contains(body, "open(") && (strings.Contains(body, "'w'") || strings.Contains(body, `"w"`)) {
				hasFileWrite = true
			}
			if strings.Contains(body, "os.WriteFile") || strings.Contains(body, "ioutil.WriteFile") {
				hasFileWrite = true
			}
			if strings.Contains(body, "fs.writeFile") || strings.Contains(body, "fs.writeFileSync") {
				hasFileWrite = true
			}
			if hasFileWrite && (strings.Contains(lower, "handler") || strings.Contains(lower, "route") ||
				strings.Contains(lower, "endpoint") || strings.Contains(lower, "api")) {
				seen["stateless"] = true
				findings = append(findings, hardenFinding{
					RuleID:   "stateless-services",
					Category: catScalability,
					Severity: ruleSevInfo,
					File:     file,
					Message:  "Local file writes detected — data won't survive pod restart; consider object storage or a database",
				})
			}
		}
	}

	// Resource limits check — scan Dockerfiles for resource hints
	if len(ctx.dockerfiles) > 0 {
		findings = append(findings, hardenFinding{
			RuleID:   "resource-limits",
			Category: catScalability,
			Severity: ruleSevWarning,
			Message:  "Set CPU and memory limits in your DSE spec to prevent noisy-neighbor issues under load",
			Fix:      "Add to dev-environment.yaml: resources: { limits: { cpu: '500m', memory: '512Mi' } }",
		})
	}

	return findings
}

// ── Performance checks ──────────────────────────────────────────

func hardenCheckPerformance(ctx *repoContext, cfg *hardenConfig) []hardenFinding {
	var findings []hardenFinding
	seen := make(map[string]bool)

	for file, body := range ctx.sourceSnippets {
		// Sync I/O in async context
		if !seen["sync-io"] {
			isAsync := strings.Contains(body, "async def") || strings.Contains(body, "asyncio") ||
				strings.Contains(body, "async function") || strings.Contains(body, "await ")

			if isAsync {
				hasSyncIO := false
				// Python: time.sleep, open() without aiofiles, requests. in async
				if strings.Contains(body, "time.sleep(") {
					hasSyncIO = true
				}
				if strings.Contains(body, "import requests") && !strings.Contains(body, "import aiohttp") {
					hasSyncIO = true
				}

				if hasSyncIO {
					seen["sync-io"] = true
					findings = append(findings, hardenFinding{
						RuleID:   "no-sync-io-in-async",
						Category: catPerformance,
						Severity: ruleSevWarning,
						File:     file,
						Message:  "Blocking I/O in async code — use async alternatives (aiohttp, asyncio.sleep, aiofiles)",
					})
				}
			}
		}

		// N+1 query patterns (loop with DB calls)
		if !seen["n-plus-one"] {
			hasLoop := strings.Contains(body, "for ") || strings.Contains(body, "forEach")
			hasDBCall := strings.Contains(body, ".find_one(") || strings.Contains(body, ".findOne(") ||
				strings.Contains(body, ".execute(") || strings.Contains(body, ".query(") ||
				strings.Contains(body, "SELECT ") || strings.Contains(body, ".get(")

			if hasLoop && hasDBCall {
				// Rough heuristic: DB call inside a loop body
				lines := strings.Split(body, "\n")
				inLoop := false
				for _, line := range lines {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "for ") || strings.HasPrefix(trimmed, "for(") ||
						strings.Contains(trimmed, ".forEach(") || strings.Contains(trimmed, ".map(") {
						inLoop = true
					}
					if inLoop && (strings.Contains(trimmed, ".find_one(") || strings.Contains(trimmed, ".findOne(") ||
						strings.Contains(trimmed, ".execute(") || strings.Contains(trimmed, "SELECT ")) {
						seen["n-plus-one"] = true
						findings = append(findings, hardenFinding{
							RuleID:   "n-plus-one-queries",
							Category: catPerformance,
							Severity: ruleSevInfo,
							File:     file,
							Message:  "Possible N+1 query pattern — database call inside a loop; consider batch operations",
						})
						break
					}
				}
			}
		}
	}

	return findings
}

// ── Observability checks ────────────────────────────────────────

func hardenCheckObservability(ctx *repoContext, cfg *hardenConfig) []hardenFinding {
	var findings []hardenFinding
	seen := make(map[string]bool)

	hasStructuredLogging := false
	hasPrintLogging := false

	for file, body := range ctx.sourceSnippets {
		// Structured logging
		if strings.Contains(body, "logging.") || strings.Contains(body, "log.") ||
			strings.Contains(body, "logger.") || strings.Contains(body, "winston") ||
			strings.Contains(body, "pino") || strings.Contains(body, "structlog") ||
			strings.Contains(body, "zerolog") || strings.Contains(body, "zap.") ||
			strings.Contains(body, "slog.") {
			hasStructuredLogging = true
		}

		if strings.Contains(body, "print(") || strings.Contains(body, "console.log(") ||
			strings.Contains(body, "fmt.Println(") || strings.Contains(body, "fmt.Printf(") {
			if !seen["print-logging"] {
				seen["print-logging"] = true
				hasPrintLogging = true
				_ = file // used implicitly
			}
		}
	}

	if hasPrintLogging && !hasStructuredLogging {
		findings = append(findings, hardenFinding{
			RuleID:   "structured-logging",
			Category: catObservability,
			Severity: ruleSevInfo,
			Message:  "Using print/console.log for logging — structured logging makes production debugging much easier",
		})
	}

	// Check for error tracking
	hasErrorTracking := false
	for _, body := range ctx.sourceSnippets {
		if strings.Contains(body, "sentry") || strings.Contains(body, "rollbar") ||
			strings.Contains(body, "bugsnag") || strings.Contains(body, "airbrake") {
			hasErrorTracking = true
			break
		}
	}
	for _, body := range ctx.depFiles {
		if strings.Contains(body, "sentry") || strings.Contains(body, "rollbar") ||
			strings.Contains(body, "bugsnag") {
			hasErrorTracking = true
			break
		}
	}

	if !hasErrorTracking && len(ctx.sourceSnippets) > 0 {
		findings = append(findings, hardenFinding{
			RuleID:   "error-reporting",
			Category: catObservability,
			Severity: ruleSevInfo,
			Message:  "No error tracking integration detected — Sentry or similar catches production errors before users report them",
		})
	}

	return findings
}

// ── CI hygiene checks ───────────────────────────────────────────

var dseEnvVarRegex = regexp.MustCompile(`(?m)^\s+-\s+name:\s+(\w+)`)

func hardenCheckCIHygiene(repoPath string, ctx *repoContext, cfg *hardenConfig) []hardenFinding {
	var findings []hardenFinding

	// Look for DSE files
	dseFiles := findDSEFiles(repoPath)

	for _, dsePath := range dseFiles {
		content, err := os.ReadFile(dsePath)
		if err != nil {
			continue
		}
		body := string(content)
		relPath, _ := filepath.Rel(repoPath, dsePath)

		// Check for duplicate auto-injected env vars
		autoInjected := map[string]string{
			"DATABASE_URL":       "postgres or mysql",
			"REDIS_URL":          "redis",
			"MONGO_URL":          "mongodb",
			"AMQP_URL":           "rabbitmq",
			"S3_ENDPOINT":        "minio",
			"ELASTICSEARCH_URL":  "elasticsearch",
			"KAFKA_BROKER_URL":   "kafka",
			"NATS_URL":           "nats",
			"MEMCACHED_URL":      "memcached",
		}

		for envVar, dep := range autoInjected {
			if strings.Contains(body, envVar) && strings.Contains(body, dep) {
				// Check if the env var is explicitly set AND the dependency is declared
				if dseEnvVarRegex.MatchString(body) {
					matches := dseEnvVarRegex.FindAllStringSubmatch(body, -1)
					for _, m := range matches {
						if m[1] == envVar {
							findings = append(findings, hardenFinding{
								RuleID:   "duplicate-auto-injected-env",
								Category: catCIHygiene,
								Severity: ruleSevError,
								File:     relPath,
								Message:  fmt.Sprintf("%s is set manually but %s dependency auto-injects it — remove the manual entry", envVar, dep),
							})
						}
					}
				}
			}
		}

		// Check for health check path on HTTP services
		if strings.Contains(body, "port:") && !strings.Contains(body, "health-check") {
			findings = append(findings, hardenFinding{
				RuleID:   "missing-health-check-path",
				Category: catCIHygiene,
				Severity: ruleSevWarning,
				File:     relPath,
				Message:  "No health-check-path — pods won't auto-restart on application failure",
				Fix:      "Add health-check-path: /health (or your app's health endpoint)",
			})
		}

		// Check for hardcoded secrets in DSE
		secretPatterns := []string{"sk-", "sk_live_", "sk_test_", "ghp_", "AKIA", "xoxb-"}
		for _, pat := range secretPatterns {
			if strings.Contains(body, pat) {
				findings = append(findings, hardenFinding{
					RuleID:   "secrets-via-kindling",
					Category: catCIHygiene,
					Severity: ruleSevError,
					File:     relPath,
					Message:  "API key appears to be hardcoded in DSE YAML — use kindling secrets set instead",
					Fix:      "kindling secrets set <NAME> <value>",
				})
				break
			}
		}
	}

	// Check generated workflow files
	findings = append(findings, hardenCheckWorkflow(repoPath)...)

	return findings
}

// findDSEFiles looks for DevStagingEnvironment YAML files.
func findDSEFiles(repoPath string) []string {
	var files []string

	// Check well-known locations
	patterns := []string{
		filepath.Join(repoPath, ".kindling", "*.yaml"),
		filepath.Join(repoPath, ".kindling", "*.yml"),
		filepath.Join(repoPath, "*environment*.yaml"),
		filepath.Join(repoPath, "*environment*.yml"),
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err == nil {
			files = append(files, matches...)
		}
	}

	return files
}

// hardenCheckWorkflow validates generated CI workflow files.
func hardenCheckWorkflow(repoPath string) []hardenFinding {
	var findings []hardenFinding

	workflowPaths := []string{
		filepath.Join(repoPath, ".github", "workflows", "dev-deploy.yml"),
		filepath.Join(repoPath, ".gitlab-ci.yml"),
	}

	for _, wfPath := range workflowPaths {
		if _, err := os.Stat(wfPath); err != nil {
			continue
		}

		content, err := os.ReadFile(wfPath)
		if err != nil {
			continue
		}

		// Validate YAML parses
		var parsed interface{}
		if err := yaml.Unmarshal(content, &parsed); err != nil {
			relPath, _ := filepath.Rel(repoPath, wfPath)
			findings = append(findings, hardenFinding{
				RuleID:   "workflow-valid",
				Category: catCIHygiene,
				Severity: ruleSevError,
				File:     relPath,
				Message:  fmt.Sprintf("Workflow YAML is invalid: %s", err),
				Fix:      "kindling generate -k <api-key> -r .",
			})
		}

		// Check for hardcoded secrets in workflow
		body := string(content)
		if strings.Contains(body, "sk-") || strings.Contains(body, "AKIA") {
			relPath, _ := filepath.Rel(repoPath, wfPath)
			findings = append(findings, hardenFinding{
				RuleID:   "secrets-via-kindling",
				Category: catCIHygiene,
				Severity: ruleSevError,
				File:     relPath,
				Message:  "Credential pattern found in CI workflow — use secretKeyRef",
			})
		}
	}

	return findings
}

// ── Utility (reuse isGitignored from analyze.go) ────────────────

// clusterExists checks if a Kind cluster with the given name is running.
// (already defined in diagnose.go, used here for reference)
func clusterExistsForHarden(name string) bool {
	output, err := exec.Command("kind", "get", "clusters").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}
