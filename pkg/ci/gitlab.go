package ci

import (
	"fmt"
	"strings"
)

// GitLabProvider implements Provider for GitLab CI/CD.
type GitLabProvider struct{}

func init() {
	Register(&GitLabProvider{})
}

// Compile-time interface checks.
var (
	_ Provider          = (*GitLabProvider)(nil)
	_ RunnerAdapter     = (*GitLabRunnerAdapter)(nil)
	_ WorkflowGenerator = (*GitLabWorkflowGenerator)(nil)
)

func (g *GitLabProvider) Name() string        { return "gitlab" }
func (g *GitLabProvider) DisplayName() string  { return "GitLab CI" }
func (g *GitLabProvider) Runner() RunnerAdapter { return &GitLabRunnerAdapter{} }
func (g *GitLabProvider) Workflow() WorkflowGenerator {
	return &GitLabWorkflowGenerator{}
}
func (g *GitLabProvider) CLILabels() CLILabels {
	return CLILabels{
		Username:        "GitLab username",
		Repository:      "GitLab project (group/project)",
		Token:           "GitLab PAT (api scope)",
		SecretName:      "gitlab-runner-token",
		CRDKind:         "GithubActionRunnerPool",
		CRDPlural:       "githubactionrunnerpools",
		CRDListHeader:   "GitLab CI Runner Pools",
		RunnerComponent: "gitlab-ci-runner",
		ActionsURLFmt:   "https://gitlab.com/%s/-/pipelines",
		CRDAPIVersion:   "apps.example.com/v1alpha1",
	}
}

// ────────────────────────────────────────────────────────────────────────────
// GitLabRunnerAdapter
// ────────────────────────────────────────────────────────────────────────────

// GitLabRunnerAdapter implements RunnerAdapter for GitLab CI self-hosted runners.
// It embeds BaseRunnerAdapter for shared naming conventions.
type GitLabRunnerAdapter struct{ BaseRunnerAdapter }

func (a *GitLabRunnerAdapter) DefaultImage() string {
	return "gitlab/gitlab-runner:latest"
}

func (a *GitLabRunnerAdapter) DefaultTokenKey() string {
	return "gitlab-token"
}

// APIBaseURL returns the GitLab API base URL.
// For gitlab.com it returns "https://gitlab.com/api/v4".
// For self-managed instances it appends "/api/v4".
func (a *GitLabRunnerAdapter) APIBaseURL(platformURL string) string {
	platformURL = strings.TrimRight(platformURL, "/")
	if platformURL == "" {
		platformURL = "https://gitlab.com"
	}
	return platformURL + "/api/v4"
}

func (a *GitLabRunnerAdapter) RunnerEnvVars(cfg RunnerEnvConfig) []ContainerEnvVar {
	platformURL := cfg.PlatformURL
	if platformURL == "" {
		platformURL = "https://gitlab.com"
	}

	runnerTags := []string{"self-hosted", cfg.Username}
	runnerTags = append(runnerTags, cfg.Labels...)

	apiURL := a.APIBaseURL(platformURL)

	envVars := []ContainerEnvVar{
		{
			// The GitLab PAT (from the referenced Secret) is used at startup
			// to create a runner authentication token via the API.
			Name: "GITLAB_PAT",
			SecretRef: &SecretRef{
				Name: cfg.TokenSecretName,
				Key:  cfg.TokenSecretKey,
			},
		},
		{
			Name:  "CI_SERVER_URL",
			Value: platformURL,
		},
		{
			// API base URL for token exchange.
			Name:  "GITLAB_API_URL",
			Value: apiURL,
		},
		{
			// Project path for runner registration (e.g. "group/project").
			Name:  "RUNNER_PROJECT_PATH",
			Value: cfg.Repository,
		},
		{
			Name:  "RUNNER_NAME",
			Value: fmt.Sprintf("%s-%s", cfg.Username, cfg.CRName),
		},
		{
			Name:  "RUNNER_TAG_LIST",
			Value: strings.Join(runnerTags, ","),
		},
		{
			Name:  "RUNNER_EXECUTOR",
			Value: "shell",
		},
		{
			Name:  "GITLAB_USERNAME",
			Value: cfg.Username,
		},
	}

	if cfg.RunnerGroup != "" {
		envVars = append(envVars, ContainerEnvVar{
			Name:  "RUNNER_GROUP",
			Value: cfg.RunnerGroup,
		})
	}

	return envVars
}

// StartupScript returns the bash script that:
//  1. Exchanges the GitLab PAT for a runner authentication token via the API
//  2. Registers the runner with gitlab-runner register using the auth token
//  3. Sets up a SIGTERM trap so the runner de-registers on pod shutdown
//  4. Starts the runner process
func (a *GitLabRunnerAdapter) StartupScript() string {
	return `#!/bin/bash
set -uo pipefail

# ── Exchange PAT for a runner authentication token ──────────────
echo "🔑 Exchanging PAT for runner authentication token..."
echo "   API: ${GITLAB_API_URL}/user/runners"

# Encode project path for URL
PROJECT_ID_ENC=$(python3 -c "import urllib.parse; print(urllib.parse.quote('${RUNNER_PROJECT_PATH}', safe=''))" 2>/dev/null || echo "${RUNNER_PROJECT_PATH}")

# Create a project-scoped runner via the REST API (GitLab 16.0+)
# POST /user/runners creates a runner and returns an authentication token.
HTTP_CODE=$(curl -sS -o /tmp/runner_response.json -w '%{http_code}' -X POST \
  -H "PRIVATE-TOKEN: ${GITLAB_PAT}" \
  -H "Content-Type: application/json" \
  -d "{
    \"runner_type\": \"project_type\",
    \"project_id\": null,
    \"description\": \"${RUNNER_NAME}\",
    \"tag_list\": [$(echo \"${RUNNER_TAG_LIST}\" | sed 's/,/\",\"/g' | sed 's/^/\"/;s/$/\"/')],
    \"run_untagged\": false
  }" \
  "${GITLAB_API_URL}/user/runners") || true

echo "   HTTP status: ${HTTP_CODE}"

if [ "${HTTP_CODE}" != "201" ]; then
  echo "❌ GitLab API returned HTTP ${HTTP_CODE}:"
  cat /tmp/runner_response.json 2>/dev/null || echo "(no response body)"
  echo ""
  echo "Falling back to project runners endpoint..."

  # Fallback: look up project ID then use /projects/:id/runners
  PROJECT_HTTP=$(curl -sS -o /tmp/project_response.json -w '%{http_code}' \
    -H "PRIVATE-TOKEN: ${GITLAB_PAT}" \
    "${GITLAB_API_URL}/projects/${PROJECT_ID_ENC}") || true

  if [ "${PROJECT_HTTP}" = "200" ]; then
    PROJECT_ID=$(python3 -c "import json; print(json.load(open('/tmp/project_response.json'))['id'])")
    echo "   Project ID: ${PROJECT_ID}"

    HTTP_CODE=$(curl -sS -o /tmp/runner_response.json -w '%{http_code}' -X POST \
      -H "PRIVATE-TOKEN: ${GITLAB_PAT}" \
      -H "Content-Type: application/json" \
      -d "{
        \"runner_type\": \"project_type\",
        \"project_id\": ${PROJECT_ID},
        \"description\": \"${RUNNER_NAME}\",
        \"tag_list\": [$(echo \"${RUNNER_TAG_LIST}\" | sed 's/,/\",\"/g' | sed 's/^/\"/;s/$/\"/')],
        \"run_untagged\": false
      }" \
      "${GITLAB_API_URL}/user/runners") || true

    echo "   HTTP status: ${HTTP_CODE}"
  fi

  if [ "${HTTP_CODE}" != "201" ]; then
    echo "❌ Failed to create runner:"
    cat /tmp/runner_response.json 2>/dev/null || echo "(no response body)"
    echo ""
    echo "Make sure your PAT has the 'api' scope and you have Maintainer access."
    exit 1
  fi
fi

RUNNER_TOKEN=$(python3 -c "import json; print(json.load(open('/tmp/runner_response.json'))['token'])")
RUNNER_ID=$(python3 -c "import json; print(json.load(open('/tmp/runner_response.json'))['id'])")
echo "✅ Got runner token (runner ID: ${RUNNER_ID})"

# ── Register the runner with the obtained token ──────────────────
echo "🔧 Registering runner..."
gitlab-runner register \
  --non-interactive \
  --url "${CI_SERVER_URL}" \
  --token "${RUNNER_TOKEN}" \
  --executor "${RUNNER_EXECUTOR}" \
  --name "${RUNNER_NAME}"

# De-register on shutdown
cleanup() {
  echo "🛑 Unregistering runner..."
  gitlab-runner unregister --name "${RUNNER_NAME}" || true
  # Delete the runner via API so the token is invalidated
  curl -sS -X DELETE \
    -H "PRIVATE-TOKEN: ${GITLAB_PAT}" \
    "${GITLAB_API_URL}/runners/${RUNNER_ID}" 2>/dev/null || true
}
trap cleanup SIGTERM SIGINT

echo "✅ Runner registered and ready"

# Start the runner (exec so PID 1 gets signals)
exec gitlab-runner run
`
}

func (a *GitLabRunnerAdapter) RunnerLabels(username string, crName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":          crName,
		"app.kubernetes.io/component":     "gitlab-ci-runner",
		"app.kubernetes.io/managed-by":    "githubactionrunnerpool-operator",
		"app.kubernetes.io/instance":      crName,
		"apps.example.com/gitlab-username": username,
	}
}

// DeploymentName, ServiceAccountName, ClusterRoleName, and
// ClusterRoleBindingName are inherited from BaseRunnerAdapter.

// ────────────────────────────────────────────────────────────────────────────
// GitLabWorkflowGenerator
// ────────────────────────────────────────────────────────────────────────────

// GitLabWorkflowGenerator implements WorkflowGenerator for GitLab CI/CD.
type GitLabWorkflowGenerator struct{}

func (g *GitLabWorkflowGenerator) DefaultOutputPath() string {
	return ".gitlab-ci.yml"
}

// SystemPrompt returns the full system prompt for GitLab CI pipeline generation.
func (g *GitLabWorkflowGenerator) SystemPrompt(hostArch string) string {
	prompt := `You are an expert at generating GitLab CI pipeline files for kindling, a Kubernetes operator that provides local dev/staging environments on Kind clusters.

You generate .gitlab-ci.yml pipeline files that use inline shell scripts to perform
builds and deploys via the kindling sidecar mechanism.

The pipeline runs on a self-hosted GitLab runner pod inside the Kind cluster.
The runner pod has a Kaniko build-agent sidecar and a shared /builds emptyDir volume.

## Build mechanism (kindling-build equivalent)

To build a container image, write a shell script step that:
1. Creates a tarball of the build context: tar -czf /builds/<name>.tar.gz -C <context> .
2. Writes the destination image to: echo "<image>" > /builds/<name>.dest
3. Optionally writes a custom Dockerfile path: echo "<path>" > /builds/<name>.dockerfile
4. Triggers the sidecar: touch /builds/<name>.request
5. Waits for /builds/<name>.done (poll every 2s, timeout after the specified seconds)
6. Checks /builds/<name>.exitcode for success (0) or failure

Build inputs: name (required), context (required), image (required), exclude (optional), dockerfile (optional), timeout (default 300)

## Deploy mechanism (kindling-deploy equivalent)

To deploy a DevStagingEnvironment CR, write a shell script step that:
1. Generates a DSE YAML file at /builds/<name>-dse.yaml
2. Triggers the sidecar: touch /builds/<name>-dse.apply
3. Waits for /builds/<name>-dse.apply-done
4. Checks /builds/<name>-dse.apply-exitcode

` + PromptDeployInputs + `

` + PromptDockerfileExistence + `

Key conventions you MUST follow:
- Registry: registry:5000 (in-cluster)
- Image tag: ${GITLAB_USER_LOGIN}-${CI_COMMIT_SHORT_SHA}
- Runner tags: [self-hosted, "$GITLAB_USER_LOGIN"]
- Ingress host pattern: ${GITLAB_USER_LOGIN}-<service>.localhost
- DSE name pattern: ${GITLAB_USER_LOGIN}-<service>
- Trigger on push to the default branch
- Always include a checkout step (GitLab does this automatically)
- Always include a "Clean builds directory" step at the start
- For multi-service repos, use stages: [build, deploy] with build before deploy
` + PromptHealthChecks + `
- If a service (like an API gateway) depends on other services via env vars,
  deploy it LAST so its upstreams are already running

` + PromptDependencyDetection + `

` + PromptDependencyAutoInjection + `

` + PromptBuildTimeout + `

` + PromptKanakoPatching + `

Combine all Kaniko patches for a service into the build script BEFORE creating the tarball.

For multi-service repos, generate separate jobs for each service with proper
stage ordering and inter-service env vars wired up.

CRITICAL — Inter-service environment variables:
When a service calls other services via gRPC or HTTP, it reads their addresses from
environment variables. You MUST examine the source code snippets for EVERY service
and find ALL env var references that look like service address variables. For each,
add an env entry mapping it to the target service's cluster-internal DNS name:
  ${GITLAB_USER_LOGIN}-<service-name>:<port>
Do NOT skip inter-service env vars — without them, services cannot find each other.

` + PromptDockerCompose + `

` + PromptDevStagingPhilosophy + `

` + PromptOAuth + `

` + PromptFinalValidation

	return strings.ReplaceAll(prompt, "HOSTARCH", hostArch)
}

func (g *GitLabWorkflowGenerator) PromptContext() PromptContext {
	return PromptContext{
		PlatformName:    "GitLab CI",
		WorkflowNoun:    "pipeline",
		BuildActionRef:  "(inline shell — see system prompt)",
		DeployActionRef: "(inline shell — see system prompt)",
		CheckoutAction:  "(automatic in GitLab CI)",
		ActorExpr:       "${GITLAB_USER_LOGIN}",
		SHAExpr:         "${CI_COMMIT_SHORT_SHA}",
		WorkspaceExpr:   "${CI_PROJECT_DIR}",
		RunnerSpec:      `[self-hosted, "$GITLAB_USER_LOGIN"]`,
		EnvTagExpr:      "${GITLAB_USER_LOGIN}-${CI_COMMIT_SHORT_SHA}",
		TriggerBlock: func(branch string) string {
			return fmt.Sprintf("workflow:\n  rules:\n    - if: $CI_COMMIT_BRANCH == \"%s\"\n    - when: manual", branch)
		},
		WorkflowFileDescription: "GitLab CI pipeline",
	}
}

func (g *GitLabWorkflowGenerator) ExampleWorkflows() (singleService, multiService string) {
	return gitlabSingleServiceExample, gitlabMultiServiceExample
}

// StripTemplateExpressions removes GitLab CI variable expressions from content.
func (g *GitLabWorkflowGenerator) StripTemplateExpressions(content string) string {
	replacements := []struct{ old, new string }{
		{"${GITLAB_USER_LOGIN}", "ACTOR"},
		{"$GITLAB_USER_LOGIN", "ACTOR"},
		{"${CI_COMMIT_SHORT_SHA}", "SHA"},
		{"$CI_COMMIT_SHORT_SHA", "SHA"},
		{"${CI_COMMIT_SHA}", "SHA"},
		{"$CI_COMMIT_SHA", "SHA"},
		{"${CI_PROJECT_DIR}", "WORKSPACE"},
		{"$CI_PROJECT_DIR", "WORKSPACE"},
		{"${REGISTRY}", "REGISTRY"},
		{"$REGISTRY", "REGISTRY"},
		{"${TAG}", "TAG"},
		{"$TAG", "TAG"},
	}
	for _, r := range replacements {
		content = strings.ReplaceAll(content, r.old, r.new)
	}
	return content
}

// ────────────────────────────────────────────────────────────────────────────
// Reference example pipelines for the AI prompt
// ────────────────────────────────────────────────────────────────────────────

const gitlabSingleServiceExample = `variables:
  REGISTRY: "registry:5000"
  TAG: "${GITLAB_USER_LOGIN}-${CI_COMMIT_SHORT_SHA}"

stages:
  - build
  - deploy

build-sample-app:
  stage: build
  tags: [self-hosted, "$GITLAB_USER_LOGIN"]
  script:
    # Clean builds directory
    - |
      rm -f /builds/*.done /builds/*.request /builds/*.processing \
            /builds/*.apply /builds/*.apply-done /builds/*.apply-log \
            /builds/*.apply-exitcode /builds/*.exitcode \
            /builds/*.log /builds/*.dest /builds/*.tar.gz \
            /builds/*.yaml /builds/*.sh

    # Build image via Kaniko sidecar
    - tar -czf /builds/sample-app.tar.gz -C ${CI_PROJECT_DIR} .
    - echo "${REGISTRY}/sample-app:${TAG}" > /builds/sample-app.dest
    - touch /builds/sample-app.request
    - |
      WAITED=0
      while [ ! -f /builds/sample-app.done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 300 ]; then
          echo "❌ Build timed out"; cat /builds/sample-app.log 2>/dev/null; exit 1
        fi
      done
    - |
      EXIT_CODE=$(cat /builds/sample-app.exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Build failed"; cat /builds/sample-app.log 2>/dev/null; exit 1
      fi
      echo "✅ Built sample-app"

deploy-sample-app:
  stage: deploy
  tags: [self-hosted, "$GITLAB_USER_LOGIN"]
  script:
    - |
      cat > /builds/${GITLAB_USER_LOGIN}-sample-app-dse.yaml <<EOF
      apiVersion: apps.example.com/v1alpha1
      kind: DevStagingEnvironment
      metadata:
        name: ${GITLAB_USER_LOGIN}-sample-app
        labels:
          app.kubernetes.io/name: ${GITLAB_USER_LOGIN}-sample-app
          app.kubernetes.io/managed-by: kindling
      spec:
        deployment:
          image: ${REGISTRY}/sample-app:${TAG}
          replicas: 1
          port: 8080
          healthCheck:
            type: http
            path: /healthz
        service:
          port: 8080
          type: ClusterIP
        ingress:
          enabled: true
          host: ${GITLAB_USER_LOGIN}-sample-app.localhost
          ingressClassName: nginx
        dependencies:
          - type: postgres
            version: "16"
          - type: redis
      EOF
    - touch /builds/${GITLAB_USER_LOGIN}-sample-app-dse.apply
    - |
      WAITED=0
      while [ ! -f /builds/${GITLAB_USER_LOGIN}-sample-app-dse.apply-done ]; do
        sleep 1; WAITED=$((WAITED+1))
        if [ ${WAITED} -ge 60 ]; then echo "❌ Deploy timed out"; exit 1; fi
      done
    - |
      EXIT_CODE=$(cat /builds/${GITLAB_USER_LOGIN}-sample-app-dse.apply-exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Deploy failed"; cat /builds/${GITLAB_USER_LOGIN}-sample-app-dse.apply-log 2>/dev/null; exit 1
      fi
    - echo "🎉 Deploy complete!"
    - echo "🌐 http://${GITLAB_USER_LOGIN}-sample-app.localhost"`

const gitlabMultiServiceExample = `variables:
  REGISTRY: "registry:5000"
  TAG: "${GITLAB_USER_LOGIN}-${CI_COMMIT_SHORT_SHA}"

stages:
  - build
  - deploy

# -- Build all images --

build-api:
  stage: build
  tags: [self-hosted, "$GITLAB_USER_LOGIN"]
  script:
    - |
      rm -f /builds/*.done /builds/*.request /builds/*.processing \
            /builds/*.apply /builds/*.apply-done /builds/*.apply-log \
            /builds/*.apply-exitcode /builds/*.exitcode \
            /builds/*.log /builds/*.dest /builds/*.tar.gz \
            /builds/*.yaml /builds/*.sh
    - tar -czf /builds/api.tar.gz -C ${CI_PROJECT_DIR} --exclude='./ui' .
    - echo "${REGISTRY}/api:${TAG}" > /builds/api.dest
    - touch /builds/api.request
    - |
      WAITED=0
      while [ ! -f /builds/api.done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 300 ]; then
          echo "❌ Build timed out"; cat /builds/api.log 2>/dev/null; exit 1
        fi
      done
    - |
      EXIT_CODE=$(cat /builds/api.exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Build failed"; cat /builds/api.log 2>/dev/null; exit 1
      fi
      echo "✅ Built api"

build-ui:
  stage: build
  tags: [self-hosted, "$GITLAB_USER_LOGIN"]
  script:
    - tar -czf /builds/ui.tar.gz -C ${CI_PROJECT_DIR}/ui .
    - echo "${REGISTRY}/ui:${TAG}" > /builds/ui.dest
    - touch /builds/ui.request
    - |
      WAITED=0
      while [ ! -f /builds/ui.done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 300 ]; then
          echo "❌ Build timed out"; cat /builds/ui.log 2>/dev/null; exit 1
        fi
      done
    - |
      EXIT_CODE=$(cat /builds/ui.exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Build failed"; cat /builds/ui.log 2>/dev/null; exit 1
      fi
      echo "✅ Built ui"

# -- Deploy in dependency order --

deploy-api:
  stage: deploy
  tags: [self-hosted, "$GITLAB_USER_LOGIN"]
  script:
    - |
      cat > /builds/${GITLAB_USER_LOGIN}-api-dse.yaml <<EOF
      apiVersion: apps.example.com/v1alpha1
      kind: DevStagingEnvironment
      metadata:
        name: ${GITLAB_USER_LOGIN}-api
        labels:
          app.kubernetes.io/name: ${GITLAB_USER_LOGIN}-api
          app.kubernetes.io/managed-by: kindling
          app.kubernetes.io/part-of: my-app
          app.kubernetes.io/component: api
          apps.example.com/gitlab-username: ${GITLAB_USER_LOGIN}
      spec:
        deployment:
          image: ${REGISTRY}/api:${TAG}
          replicas: 1
          port: 8080
          healthCheck:
            type: http
            path: /healthz
        service:
          port: 8080
          type: ClusterIP
        ingress:
          enabled: true
          host: ${GITLAB_USER_LOGIN}-api.localhost
          ingressClassName: nginx
        dependencies:
          - type: postgres
            version: "16"
          - type: redis
      EOF
    - touch /builds/${GITLAB_USER_LOGIN}-api-dse.apply
    - |
      WAITED=0
      while [ ! -f /builds/${GITLAB_USER_LOGIN}-api-dse.apply-done ]; do
        sleep 1; WAITED=$((WAITED+1))
        if [ ${WAITED} -ge 60 ]; then echo "❌ Deploy timed out"; exit 1; fi
      done
    - |
      EXIT_CODE=$(cat /builds/${GITLAB_USER_LOGIN}-api-dse.apply-exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Deploy failed"; cat /builds/${GITLAB_USER_LOGIN}-api-dse.apply-log 2>/dev/null; exit 1
      fi
      echo "✅ Deployed api"

deploy-ui:
  stage: deploy
  tags: [self-hosted, "$GITLAB_USER_LOGIN"]
  needs: [deploy-api]
  script:
    - |
      cat > /builds/${GITLAB_USER_LOGIN}-ui-dse.yaml <<EOF
      apiVersion: apps.example.com/v1alpha1
      kind: DevStagingEnvironment
      metadata:
        name: ${GITLAB_USER_LOGIN}-ui
        labels:
          app.kubernetes.io/name: ${GITLAB_USER_LOGIN}-ui
          app.kubernetes.io/managed-by: kindling
          app.kubernetes.io/part-of: my-app
          app.kubernetes.io/component: ui
          apps.example.com/gitlab-username: ${GITLAB_USER_LOGIN}
      spec:
        deployment:
          image: ${REGISTRY}/ui:${TAG}
          replicas: 1
          port: 80
          healthCheck:
            type: http
            path: /
          env:
            - name: API_URL
              value: "http://${GITLAB_USER_LOGIN}-api:8080"
        service:
          port: 80
          type: ClusterIP
        ingress:
          enabled: true
          host: ${GITLAB_USER_LOGIN}-ui.localhost
          ingressClassName: nginx
      EOF
    - touch /builds/${GITLAB_USER_LOGIN}-ui-dse.apply
    - |
      WAITED=0
      while [ ! -f /builds/${GITLAB_USER_LOGIN}-ui-dse.apply-done ]; do
        sleep 1; WAITED=$((WAITED+1))
        if [ ${WAITED} -ge 60 ]; then echo "❌ Deploy timed out"; exit 1; fi
      done
    - |
      EXIT_CODE=$(cat /builds/${GITLAB_USER_LOGIN}-ui-dse.apply-exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Deploy failed"; cat /builds/${GITLAB_USER_LOGIN}-ui-dse.apply-log 2>/dev/null; exit 1
      fi
    - echo "🎉 Deploy complete!"
    - echo "🌐 UI:  http://${GITLAB_USER_LOGIN}-ui.localhost"
    - echo "🌐 API: http://${GITLAB_USER_LOGIN}-api.localhost"`
