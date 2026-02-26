package ci

import (
	"fmt"
	"strings"
)

// GitHubProvider implements Provider for GitHub Actions.
type GitHubProvider struct{}

func init() {
	Register(&GitHubProvider{})
}

// Compile-time interface checks.
var (
	_ Provider          = (*GitHubProvider)(nil)
	_ RunnerAdapter     = (*GitHubRunnerAdapter)(nil)
	_ WorkflowGenerator = (*GitHubWorkflowGenerator)(nil)
)

func (g *GitHubProvider) Name() string          { return "github" }
func (g *GitHubProvider) DisplayName() string   { return "GitHub Actions" }
func (g *GitHubProvider) Runner() RunnerAdapter { return &GitHubRunnerAdapter{} }
func (g *GitHubProvider) Workflow() WorkflowGenerator {
	return &GitHubWorkflowGenerator{}
}
func (g *GitHubProvider) CLILabels() CLILabels {
	return CLILabels{
		Username:        "GitHub username",
		Repository:      "GitHub repository (owner/repo)",
		Token:           "GitHub PAT (repo scope)",
		SecretName:      "github-runner-token",
		CRDKind:         "GithubActionRunnerPool",
		CRDPlural:       "githubactionrunnerpools",
		CRDListHeader:   "GitHub Actions Runner Pools",
		RunnerComponent: "github-actions-runner",
		ActionsURLFmt:   "https://github.com/%s/actions",
		CRDAPIVersion:   "apps.example.com/v1alpha1",
	}
}

// ────────────────────────────────────────────────────────────────────────────
// GitHubRunnerAdapter
// ────────────────────────────────────────────────────────────────────────────

// GitHubRunnerAdapter implements RunnerAdapter for GitHub Actions self-hosted runners.
type GitHubRunnerAdapter struct{}

func (a *GitHubRunnerAdapter) DefaultImage() string {
	return "ghcr.io/actions/actions-runner:latest"
}

func (a *GitHubRunnerAdapter) DefaultTokenKey() string {
	return "github-token"
}

// APIBaseURL returns the REST API base URL for a given GitHub instance.
// For github.com it returns "https://api.github.com".
// For GitHub Enterprise Server (e.g. "https://git.corp.com") it returns
// "https://git.corp.com/api/v3".
func (a *GitHubRunnerAdapter) APIBaseURL(platformURL string) string {
	platformURL = strings.TrimRight(platformURL, "/")
	if platformURL == "https://github.com" || platformURL == "" {
		return "https://api.github.com"
	}
	return platformURL + "/api/v3"
}

func (a *GitHubRunnerAdapter) RunnerEnvVars(cfg RunnerEnvConfig) []ContainerEnvVar {
	platformURL := cfg.PlatformURL
	if platformURL == "" {
		platformURL = "https://github.com"
	}

	apiURL := a.APIBaseURL(platformURL)

	// Build runner labels: always include "self-hosted" and the username so
	// the workflow can do `runs-on: [self-hosted, <username>]`.
	runnerLabels := []string{"self-hosted", cfg.Username}
	runnerLabels = append(runnerLabels, cfg.Labels...)

	envVars := []ContainerEnvVar{
		{
			// The GitHub PAT (from the referenced Secret) is used at startup
			// to obtain a short-lived runner registration token via the API.
			Name: "GITHUB_PAT",
			SecretRef: &SecretRef{
				Name: cfg.TokenSecretName,
				Key:  cfg.TokenSecretKey,
			},
		},
		{
			// Runner name includes the username so it's identifiable in the GH UI.
			Name:  "RUNNER_NAME_PREFIX",
			Value: fmt.Sprintf("%s-%s", cfg.Username, cfg.CRName),
		},
		{
			Name:  "RUNNER_WORKDIR",
			Value: cfg.WorkDir,
		},
		{
			// Repository URL for runner registration.
			Name:  "RUNNER_REPOSITORY_URL",
			Value: fmt.Sprintf("%s/%s", platformURL, cfg.Repository),
		},
		{
			// API base URL for token exchange (handles GHE vs github.com).
			Name:  "GITHUB_API_URL",
			Value: apiURL,
		},
		{
			// Repo slug for API calls (e.g. "jeff-vincent/kindling").
			Name:  "RUNNER_REPO_SLUG",
			Value: cfg.Repository,
		},
		{
			// Expose the GitHub username to workflow steps so the job knows
			// whose local cluster it is running on.
			Name:  "GITHUB_USERNAME",
			Value: cfg.Username,
		},
		{
			Name:  "RUNNER_LABELS",
			Value: strings.Join(runnerLabels, ","),
		},
	}

	if cfg.RunnerGroup != "" {
		envVars = append(envVars, ContainerEnvVar{
			Name:  "RUNNER_GROUP",
			Value: cfg.RunnerGroup,
		})
	}

	// Non-ephemeral: runner stays alive between jobs so it keeps polling for
	// the developer's next push.
	envVars = append(envVars, ContainerEnvVar{
		Name:  "RUNNER_EPHEMERAL",
		Value: "false",
	})

	return envVars
}

// StartupScript returns the bash script that:
//  1. Exchanges the GitHub PAT for a short-lived registration token
//  2. Calls config.sh to register the runner with GitHub
//  3. Sets up a SIGTERM trap so the runner de-registers on pod shutdown
//  4. Execs run.sh to start polling for jobs
func (a *GitHubRunnerAdapter) StartupScript() string {
	return `#!/bin/bash
set -uo pipefail

# ── Exchange PAT for a short-lived runner registration token ──────
echo "🔑 Exchanging PAT for runner registration token..."
echo "   API: ${GITHUB_API_URL}/repos/${RUNNER_REPO_SLUG}/actions/runners/registration-token"

HTTP_CODE=$(curl -sS -o /tmp/reg_response.json -w '%{http_code}' -X POST \
  -H "Authorization: Bearer ${GITHUB_PAT}" \
  -H "Accept: application/vnd.github+json" \
  "${GITHUB_API_URL}/repos/${RUNNER_REPO_SLUG}/actions/runners/registration-token") || true

echo "   HTTP status: ${HTTP_CODE}"

if [ "${HTTP_CODE}" != "201" ]; then
  echo "❌ GitHub API returned HTTP ${HTTP_CODE}:"
  cat /tmp/reg_response.json 2>/dev/null || echo "(no response body)"
  echo ""
  echo "Make sure your PAT has the 'repo' scope (classic) or"
  echo "'administration:write' permission (fine-grained)."
  exit 1
fi

RUNNER_TOKEN=$(grep -o '"token": *"[^"]*"' /tmp/reg_response.json | head -1 | cut -d'"' -f4)
rm -f /tmp/reg_response.json

if [ -z "${RUNNER_TOKEN}" ]; then
  echo "❌ Could not parse registration token from response"
  exit 1
fi
echo "✅ Registration token obtained (expires in ~1 hour)"

# De-register the runner on shutdown so it doesn't leave a ghost entry.
# Obtain a fresh removal token since the registration token may have expired.
cleanup() {
  echo "🛑 Removing runner..."
  REMOVE_TOKEN=$(curl -sS -X POST \
    -H "Authorization: Bearer ${GITHUB_PAT}" \
    -H "Accept: application/vnd.github+json" \
    "${GITHUB_API_URL}/repos/${RUNNER_REPO_SLUG}/actions/runners/remove-token" 2>/dev/null \
    | grep -o '"token": *"[^"]*"' | head -1 | cut -d'"' -f4) || true
  ./config.sh remove --token "${REMOVE_TOKEN:-${RUNNER_TOKEN}}" || true
}
trap cleanup SIGTERM SIGINT

# Build a runner name that fits GitHub's 64-char limit
RUNNER_NAME="${RUNNER_NAME_PREFIX}-$(hostname | rev | cut -d- -f1,2 | rev)"
RUNNER_NAME="${RUNNER_NAME:0:64}"

# Configure the runner (non-interactive)
./config.sh \
  --url "${RUNNER_REPOSITORY_URL}" \
  --token "${RUNNER_TOKEN}" \
  --name "${RUNNER_NAME}" \
  --labels "${RUNNER_LABELS}" \
  --work "${RUNNER_WORKDIR}" \
  --unattended \
  --replace

# Start the runner (exec so PID 1 gets signals)
exec ./run.sh
`
}

func (a *GitHubRunnerAdapter) RunnerLabels(username string, crName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":           crName,
		"app.kubernetes.io/component":      "github-actions-runner",
		"app.kubernetes.io/managed-by":     "githubactionrunnerpool-operator",
		"app.kubernetes.io/instance":       crName,
		"apps.example.com/github-username": username,
	}
}

func (a *GitHubRunnerAdapter) DeploymentName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

func (a *GitHubRunnerAdapter) ServiceAccountName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

func (a *GitHubRunnerAdapter) ClusterRoleName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

func (a *GitHubRunnerAdapter) ClusterRoleBindingName(username string) string {
	return fmt.Sprintf("%s-runner", username)
}

// ────────────────────────────────────────────────────────────────────────────
// GitHubWorkflowGenerator
// ────────────────────────────────────────────────────────────────────────────

// GitHubWorkflowGenerator implements WorkflowGenerator for GitHub Actions.
type GitHubWorkflowGenerator struct{}

func (g *GitHubWorkflowGenerator) DefaultOutputPath() string {
	return ".github/workflows/dev-deploy.yml"
}

func (g *GitHubWorkflowGenerator) PromptContext() PromptContext {
	return PromptContext{
		PlatformName:    "GitHub Actions",
		WorkflowNoun:    "workflow",
		BuildActionRef:  "kindling-sh/kindling/.github/actions/kindling-build@main",
		DeployActionRef: "kindling-sh/kindling/.github/actions/kindling-deploy@main",
		CheckoutAction:  "actions/checkout@v4",
		ActorExpr:       "${{ github.actor }}",
		SHAExpr:         "${{ github.sha }}",
		WorkspaceExpr:   "${{ github.workspace }}",
		RunnerSpec:      `[self-hosted, "${{ github.actor }}"]`,
		EnvTagExpr:      "${{ github.actor }}-${{ github.sha }}",
		TriggerBlock: func(branch string) string {
			return fmt.Sprintf("on:\n  push:\n    branches: [%s]\n  workflow_dispatch:", branch)
		},
		WorkflowFileDescription: "GitHub Actions workflow",
	}
}

func (g *GitHubWorkflowGenerator) ExampleWorkflows() (singleService, multiService string) {
	return githubSingleServiceExample, githubMultiServiceExample
}

// StripTemplateExpressions removes GitHub Actions template expressions
// (${{ ... }}) from content, replacing them with placeholder text.
func (g *GitHubWorkflowGenerator) StripTemplateExpressions(content string) string {
	// Replace common GitHub Actions expressions with stable placeholders
	replacements := []struct{ old, new string }{
		{`${{ github.actor }}`, "ACTOR"},
		{`${{ github.sha }}`, "SHA"},
		{`${{ github.workspace }}`, "WORKSPACE"},
		{`${{ env.REGISTRY }}`, "REGISTRY"},
		{`${{ env.TAG }}`, "TAG"},
	}
	for _, r := range replacements {
		content = strings.ReplaceAll(content, r.old, r.new)
	}
	return content
}

// ────────────────────────────────────────────────────────────────────────────
// Reference example workflows for the AI prompt
// ────────────────────────────────────────────────────────────────────────────

const githubSingleServiceExample = `name: Dev Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

env:
  REGISTRY: "registry:5000"
  TAG: "${{ github.actor }}-${{ github.sha }}"

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Clean builds directory
        shell: bash
        run: |
          rm -f /builds/*.done /builds/*.request /builds/*.processing \
                /builds/*.apply /builds/*.apply-done /builds/*.apply-log \
                /builds/*.apply-exitcode /builds/*.exitcode \
                /builds/*.log /builds/*.dest /builds/*.tar.gz \
                /builds/*.yaml /builds/*.sh

      - name: Build image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: sample-app
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/sample-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-sample-app"
          image: "${{ env.REGISTRY }}/sample-app:${{ env.TAG }}"
          port: "8080"
          labels: |
            app.kubernetes.io/part-of: sample-app
            apps.example.com/github-username: ${{ github.actor }}
          ingress-host: "${{ github.actor }}-sample-app.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis

      - name: Summary
        run: |
          echo "🎉 Deploy complete!"
          echo "🌐 http://${{ github.actor }}-sample-app.localhost"`

const githubMultiServiceExample = `name: Dev Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

env:
  REGISTRY: "registry:5000"
  TAG: "${{ github.actor }}-${{ github.sha }}"

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]

    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Clean builds directory
        shell: bash
        run: |
          rm -f /builds/*.done /builds/*.request /builds/*.processing \
                /builds/*.apply /builds/*.apply-done /builds/*.apply-log \
                /builds/*.apply-exitcode /builds/*.exitcode \
                /builds/*.log /builds/*.dest /builds/*.tar.gz \
                /builds/*.yaml /builds/*.sh

      - name: Build API image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: api
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/api:${{ env.TAG }}"
          exclude: "./ui"

      - name: Build UI image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: ui
          context: "${{ github.workspace }}/ui"
          image: "${{ env.REGISTRY }}/ui:${{ env.TAG }}"

      - name: Deploy API
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-api"
          image: "${{ env.REGISTRY }}/api:${{ env.TAG }}"
          port: "8080"
          labels: |
            app.kubernetes.io/part-of: my-app
            app.kubernetes.io/component: api
            apps.example.com/github-username: ${{ github.actor }}
          ingress-host: "${{ github.actor }}-api.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis

      - name: Deploy UI
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-ui"
          image: "${{ env.REGISTRY }}/ui:${{ env.TAG }}"
          port: "80"
          health-check-path: "/"
          labels: |
            app.kubernetes.io/part-of: my-app
            app.kubernetes.io/component: ui
            apps.example.com/github-username: ${{ github.actor }}
          env: |
            - name: API_URL
              value: "http://${{ github.actor }}-api:8080"
          ingress-host: "${{ github.actor }}-ui.localhost"

      - name: Summary
        run: |
          echo "🎉 Deploy complete!"
          echo "🌐 UI:  http://${{ github.actor }}-ui.localhost"
          echo "🌐 API: http://${{ github.actor }}-api.localhost"`
