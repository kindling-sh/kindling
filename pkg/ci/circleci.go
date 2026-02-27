package ci

import (
	"fmt"
	"strings"
)

// CircleCIProvider implements Provider for CircleCI.
type CircleCIProvider struct{}

func init() {
	Register(&CircleCIProvider{})
}

// Compile-time interface checks.
var (
	_ Provider          = (*CircleCIProvider)(nil)
	_ RunnerAdapter     = (*CircleCIRunnerAdapter)(nil)
	_ WorkflowGenerator = (*CircleCIWorkflowGenerator)(nil)
)

func (c *CircleCIProvider) Name() string          { return "circleci" }
func (c *CircleCIProvider) DisplayName() string   { return "CircleCI" }
func (c *CircleCIProvider) Runner() RunnerAdapter { return &CircleCIRunnerAdapter{} }
func (c *CircleCIProvider) Workflow() WorkflowGenerator {
	return &CircleCIWorkflowGenerator{}
}
func (c *CircleCIProvider) CLILabels() CLILabels {
	return CLILabels{
		Username:        "CircleCI username",
		Repository:      "CircleCI project (org/project)",
		Token:           "CircleCI runner resource class token",
		SecretName:      "circleci-runner-token",
		CRDKind:         "GithubActionRunnerPool",
		CRDPlural:       "githubactionrunnerpools",
		CRDListHeader:   "CircleCI Runner Pools",
		RunnerComponent: "circleci-runner",
		ActionsURLFmt:   "https://app.circleci.com/pipelines/%s",
		CRDAPIVersion:   "apps.example.com/v1alpha1",
	}
}

// ────────────────────────────────────────────────────────────────────────────
// CircleCIRunnerAdapter
// ────────────────────────────────────────────────────────────────────────────

// CircleCIRunnerAdapter implements RunnerAdapter for CircleCI self-hosted runners.
// It embeds BaseRunnerAdapter for shared naming conventions.
type CircleCIRunnerAdapter struct{ BaseRunnerAdapter }

func (a *CircleCIRunnerAdapter) DefaultImage() string {
	return "circleci/runner-agent:machine-3"
}

func (a *CircleCIRunnerAdapter) DefaultWorkDir() string {
	return "/tmp/_work"
}

func (a *CircleCIRunnerAdapter) DefaultTokenKey() string {
	return "circleci-token"
}

// APIBaseURL returns the CircleCI runner API base URL.
// The self-hosted runner API lives at runner.circleci.com, separate
// from the main v2 API at circleci.com/api/v2.
func (a *CircleCIRunnerAdapter) APIBaseURL(platformURL string) string {
	return "https://runner.circleci.com/api/v3"
}

func (a *CircleCIRunnerAdapter) RunnerEnvVars(cfg RunnerEnvConfig) []ContainerEnvVar {
	// CircleCI machine runner 3 only needs:
	// - CIRCLECI_RUNNER_API_AUTH_TOKEN  (the resource class token, created via CircleCI web UI)
	// - CIRCLECI_RUNNER_NAME           (a unique name for this runner instance)
	// The resource class token is NOT a PAT — it is generated once when the
	// resource class is created and cannot be retrieved again.
	envVars := []ContainerEnvVar{
		{
			// The resource class token (from the referenced Secret) authenticates
			// this runner with CircleCI. Created via CircleCI web UI or CLI.
			Name: "CIRCLECI_RUNNER_API_AUTH_TOKEN",
			SecretRef: &SecretRef{
				Name: cfg.TokenSecretName,
				Key:  cfg.TokenSecretKey,
			},
		},
		{
			Name:  "CIRCLECI_RUNNER_NAME",
			Value: fmt.Sprintf("%s-%s", SanitizeDNS(cfg.Username), cfg.CRName),
		},
		{
			Name:  "CIRCLECI_RUNNER_WORK_DIR",
			Value: cfg.WorkDir,
		},
		{
			Name:  "CIRCLECI_USERNAME",
			Value: cfg.Username,
		},
	}

	return envVars
}

// StartupScript returns the bash script that starts the CircleCI machine
// runner 3 agent. The resource class token is provided directly as
// CIRCLECI_RUNNER_API_AUTH_TOKEN (created via CircleCI web UI or CLI).
// No PAT exchange is needed — the runner agent authenticates directly
// with the resource class token.
func (a *CircleCIRunnerAdapter) StartupScript() string {
	return `#!/bin/bash
set -uo pipefail

# ── Validate required environment variables ──────────────────────
if [ -z "${CIRCLECI_RUNNER_API_AUTH_TOKEN:-}" ]; then
  echo "❌ CIRCLECI_RUNNER_API_AUTH_TOKEN is not set."
  echo "   Create a resource class and token in CircleCI:"
  echo "   https://app.circleci.com → Self-Hosted Runners → Create Resource Class"
  exit 1
fi

if [ -z "${CIRCLECI_RUNNER_NAME:-}" ]; then
  echo "❌ CIRCLECI_RUNNER_NAME is not set."
  exit 1
fi

echo "✅ CircleCI runner configured"
echo "   Runner name: ${CIRCLECI_RUNNER_NAME}"
echo "   Working dir: ${CIRCLECI_RUNNER_WORK_DIR:-/tmp/_work}"

# Clean up on shutdown
cleanup() {
  echo "🛑 Stopping runner agent..."
}
trap cleanup SIGTERM SIGINT

# Start the machine runner 3 agent (exec so PID 1 gets signals)
exec /opt/circleci/bin/circleci-runner machine \
  --runner.name "${CIRCLECI_RUNNER_NAME}" \
  --runner.working-directory "${CIRCLECI_RUNNER_WORK_DIR:-/tmp/_work}"
`
}

func (a *CircleCIRunnerAdapter) RunnerLabels(username string, crName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":             crName,
		"app.kubernetes.io/component":        "circleci-runner",
		"app.kubernetes.io/managed-by":       "githubactionrunnerpool-operator",
		"app.kubernetes.io/instance":         crName,
		"apps.example.com/circleci-username": SanitizeDNS(username),
	}
}

// DeploymentName, ServiceAccountName, ClusterRoleName, and
// ClusterRoleBindingName are inherited from BaseRunnerAdapter.

// ────────────────────────────────────────────────────────────────────────────
// CircleCIWorkflowGenerator
// ────────────────────────────────────────────────────────────────────────────

// CircleCIWorkflowGenerator implements WorkflowGenerator for CircleCI.
type CircleCIWorkflowGenerator struct{}

func (g *CircleCIWorkflowGenerator) DefaultOutputPath() string {
	return ".circleci/config.yml"
}

// SystemPrompt returns the full system prompt for CircleCI config generation.
func (g *CircleCIWorkflowGenerator) SystemPrompt(hostArch string) string {
	prompt := `You are an expert at generating CircleCI configuration files for kindling, a Kubernetes operator that provides local dev/staging environments on Kind clusters.

You generate .circleci/config.yml files that use inline shell scripts to perform
builds and deploys via the kindling sidecar mechanism.

The pipeline runs on a self-hosted CircleCI runner inside the Kind cluster.
The runner pod has a Kaniko build-agent sidecar and a shared /builds emptyDir volume.

## Build mechanism (kindling-build equivalent)

To build a container image, write a run step that:
1. Creates a tarball of the build context: tar -czf /builds/<name>.tar.gz -C <context> .
2. Writes the destination image to: echo "<image>" > /builds/<name>.dest
3. Optionally writes a custom Dockerfile path: echo "<path>" > /builds/<name>.dockerfile
4. Triggers the sidecar: touch /builds/<name>.request
5. Waits for /builds/<name>.done (poll every 2s, timeout after the specified seconds)
6. Checks /builds/<name>.exitcode for success (0) or failure

Build inputs: name (required), context (required), image (required), exclude (optional), dockerfile (optional), timeout (default 300)

## Deploy mechanism (kindling-deploy equivalent)

To deploy a DevStagingEnvironment CR, write a run step that:
1. Generates a DSE YAML file at /builds/<name>-dse.yaml
2. Triggers the sidecar: touch /builds/<name>-dse.apply
3. Waits for /builds/<name>-dse.apply-done
4. Checks /builds/<name>-dse.apply-exitcode

` + PromptDeployInputs + `

` + PromptDockerfileExistence + `

Key conventions you MUST follow:
- Registry: registry:5000 (in-cluster)
- Image tag: ${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}
  IMPORTANT: CircleCI cannot evaluate bash substring syntax like ${CIRCLE_SHA1:0:8}
  inside the declarative "environment:" block. You MUST compute TAG in a run step
  and persist it via $BASH_ENV:
    - run:
        name: Set image tag
        command: |
          echo "export TAG=${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}" >> "$BASH_ENV"
  Do NOT put TAG in the environment: block.
- Heredocs: CircleCI v2.1 uses << for parameter syntax, so heredocs like <<EOF
  MUST be escaped as \<<EOF in the YAML. The shell still interprets \<< as <<.
- Runner resource class: use the self-hosted resource class with machine: true
- Ingress host pattern: ${CIRCLE_USERNAME}-<service>.localhost
- DSE name pattern: ${CIRCLE_USERNAME}-<service>
- Trigger on push to the default branch
- Always include a checkout step
- Tar context paths: ALWAYS use paths relative to the repo root (e.g. -C . or -C ui),
  NEVER use ~/project which does not exist on self-hosted runners.
  After checkout, the CWD is the repo root.
- Always include a "Clean builds directory" step that only removes files for THAT
  service: rm -f /builds/<service>.* — never rm -f /builds/* which races with
  parallel jobs
- For multi-service repos, use separate jobs with requires for ordering
` + PromptHealthChecks + `
- If a service (like an API gateway) depends on other services via env vars,
  deploy it LAST so its upstreams are already running

` + PromptDependencyDetection + `

` + PromptDependencyAutoInjection + `

` + PromptBuildTimeout + `

` + PromptKanakoPatching + `

Combine all Kaniko patches for a service into the build script BEFORE creating the tarball.

For multi-service repos, generate separate jobs for each service with proper
workflow ordering (requires) and inter-service env vars wired up.

CRITICAL — Inter-service environment variables:
When a service calls other services via gRPC or HTTP, it reads their addresses from
environment variables. You MUST examine the source code snippets for EVERY service
and find ALL env var references that look like service address variables. For each,
add an env entry mapping it to the target service's cluster-internal DNS name:
  ${CIRCLE_USERNAME}-<service-name>:<port>
Do NOT skip inter-service env vars — without them, services cannot find each other.

` + PromptDockerCompose + `

` + PromptDevStagingPhilosophy + `

` + PromptOAuth + `

` + PromptFinalValidation

	return strings.ReplaceAll(prompt, "HOSTARCH", hostArch)
}

func (g *CircleCIWorkflowGenerator) PromptContext() PromptContext {
	return PromptContext{
		PlatformName:    "CircleCI",
		WorkflowNoun:    "config",
		BuildActionRef:  "(inline shell — see system prompt)",
		DeployActionRef: "(inline shell — see system prompt)",
		CheckoutAction:  "checkout",
		ActorExpr:       "${CIRCLE_USERNAME}",
		SHAExpr:         "${CIRCLE_SHA1:0:8}",
		WorkspaceExpr:   ".",
		RunnerSpec:      "self-hosted resource class",
		EnvTagExpr:      "${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}",
		TriggerBlock: func(branch string) string {
			return fmt.Sprintf("workflows:\n  dev-deploy:\n    jobs:\n      - build-and-deploy:\n          filters:\n            branches:\n              only: %s", branch)
		},
		WorkflowFileDescription: "CircleCI config",
	}
}

func (g *CircleCIWorkflowGenerator) ExampleWorkflows() (singleService, multiService string) {
	return circleCISingleServiceExample, circleCIMultiServiceExample
}

// StripTemplateExpressions removes CircleCI variable expressions from content.
func (g *CircleCIWorkflowGenerator) StripTemplateExpressions(content string) string {
	replacements := []struct{ old, new string }{
		{"${CIRCLE_USERNAME}", "ACTOR"},
		{"$CIRCLE_USERNAME", "ACTOR"},
		{"${CIRCLE_SHA1:0:8}", "SHA"},
		{"${CIRCLE_SHA1}", "SHA"},
		{"$CIRCLE_SHA1", "SHA"},
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
// Reference example configs for the AI prompt
// ────────────────────────────────────────────────────────────────────────────

const circleCISingleServiceExample = `version: 2.1

jobs:
  build-and-deploy:
    machine: true
    resource_class: kindling/self-hosted
    environment:
      REGISTRY: "registry:5000"
    steps:
      - checkout

      - run:
          name: Set image tag
          command: |
            echo "export TAG=${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}" >> "$BASH_ENV"

      - run:
          name: Clean builds directory
          command: rm -f /builds/sample-app.*

      - run:
          name: Build sample-app image
          command: |
            tar -czf /builds/sample-app.tar.gz .
            echo "${REGISTRY}/sample-app:${TAG}" > /builds/sample-app.dest
            touch /builds/sample-app.request

            WAITED=0
            while [ ! -f /builds/sample-app.done ]; do
              sleep 2; WAITED=$((WAITED+2))
              if [ ${WAITED} -ge 300 ]; then
                echo "❌ Build timed out"; cat /builds/sample-app.log 2>/dev/null; exit 1
              fi
            done

            EXIT_CODE=$(cat /builds/sample-app.exitcode 2>/dev/null || echo "1")
            if [ "${EXIT_CODE}" != "0" ]; then
              echo "❌ Build failed"; cat /builds/sample-app.log 2>/dev/null; exit 1
            fi
            echo "✅ Built sample-app"

      - run:
          name: Deploy sample-app
          command: |
            cat > /builds/${CIRCLE_USERNAME}-sample-app-dse.yaml \<<EOF
            apiVersion: apps.example.com/v1alpha1
            kind: DevStagingEnvironment
            metadata:
              name: ${CIRCLE_USERNAME}-sample-app
              labels:
                app.kubernetes.io/name: ${CIRCLE_USERNAME}-sample-app
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
                host: ${CIRCLE_USERNAME}-sample-app.localhost
                ingressClassName: nginx
              dependencies:
                - type: postgres
                  version: "16"
                - type: redis
            EOF

            touch /builds/${CIRCLE_USERNAME}-sample-app-dse.apply

            WAITED=0
            while [ ! -f /builds/${CIRCLE_USERNAME}-sample-app-dse.apply-done ]; do
              sleep 1; WAITED=$((WAITED+1))
              if [ ${WAITED} -ge 60 ]; then echo "❌ Deploy timed out"; exit 1; fi
            done

            EXIT_CODE=$(cat /builds/${CIRCLE_USERNAME}-sample-app-dse.apply-exitcode 2>/dev/null || echo "1")
            if [ "${EXIT_CODE}" != "0" ]; then
              echo "❌ Deploy failed"
              cat /builds/${CIRCLE_USERNAME}-sample-app-dse.apply-log 2>/dev/null
              exit 1
            fi

            echo "🎉 Deploy complete!"
            echo "🌐 http://${CIRCLE_USERNAME}-sample-app.localhost"

workflows:
  dev-deploy:
    jobs:
      - build-and-deploy:
          filters:
            branches:
              only: main`

const circleCIMultiServiceExample = `version: 2.1

jobs:
  # -- Build all images --

  build-api:
    machine: true
    resource_class: kindling/self-hosted
    environment:
      REGISTRY: "registry:5000"
    steps:
      - checkout

      - run:
          name: Set image tag
          command: |
            echo "export TAG=${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}" >> "$BASH_ENV"

      - run:
          name: Clean builds directory
          command: rm -f /builds/api.*

      - run:
          name: Build API image
          command: |
            tar -czf /builds/api.tar.gz --exclude='./ui' .
            echo "${REGISTRY}/api:${TAG}" > /builds/api.dest
            touch /builds/api.request

            WAITED=0
            while [ ! -f /builds/api.done ]; do
              sleep 2; WAITED=$((WAITED+2))
              if [ ${WAITED} -ge 300 ]; then
                echo "❌ Build timed out"; cat /builds/api.log 2>/dev/null; exit 1
              fi
            done

            EXIT_CODE=$(cat /builds/api.exitcode 2>/dev/null || echo "1")
            if [ "${EXIT_CODE}" != "0" ]; then
              echo "❌ Build failed"; cat /builds/api.log 2>/dev/null; exit 1
            fi
            echo "✅ Built api"

  build-ui:
    machine: true
    resource_class: kindling/self-hosted
    environment:
      REGISTRY: "registry:5000"
    steps:
      - checkout

      - run:
          name: Set image tag
          command: |
            echo "export TAG=${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}" >> "$BASH_ENV"

      - run:
          name: Clean builds directory
          command: rm -f /builds/ui.*

      - run:
          name: Build UI image
          command: |
            tar -czf /builds/ui.tar.gz -C ui .
            echo "${REGISTRY}/ui:${TAG}" > /builds/ui.dest
            touch /builds/ui.request

            WAITED=0
            while [ ! -f /builds/ui.done ]; do
              sleep 2; WAITED=$((WAITED+2))
              if [ ${WAITED} -ge 300 ]; then
                echo "❌ Build timed out"; cat /builds/ui.log 2>/dev/null; exit 1
              fi
            done

            EXIT_CODE=$(cat /builds/ui.exitcode 2>/dev/null || echo "1")
            if [ "${EXIT_CODE}" != "0" ]; then
              echo "❌ Build failed"; cat /builds/ui.log 2>/dev/null; exit 1
            fi
            echo "✅ Built ui"

  # -- Deploy in dependency order --

  deploy-api:
    machine: true
    resource_class: kindling/self-hosted
    environment:
      REGISTRY: "registry:5000"
    steps:
      - run:
          name: Set image tag
          command: |
            echo "export TAG=${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}" >> "$BASH_ENV"

      - run:
          name: Deploy API
          command: |
            cat > /builds/${CIRCLE_USERNAME}-api-dse.yaml \<<EOF
            apiVersion: apps.example.com/v1alpha1
            kind: DevStagingEnvironment
            metadata:
              name: ${CIRCLE_USERNAME}-api
              labels:
                app.kubernetes.io/name: ${CIRCLE_USERNAME}-api
                app.kubernetes.io/managed-by: kindling
                app.kubernetes.io/part-of: my-app
                app.kubernetes.io/component: api
                apps.example.com/circleci-username: ${CIRCLE_USERNAME}
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
                host: ${CIRCLE_USERNAME}-api.localhost
                ingressClassName: nginx
              dependencies:
                - type: postgres
                  version: "16"
                - type: redis
            EOF

            touch /builds/${CIRCLE_USERNAME}-api-dse.apply

            WAITED=0
            while [ ! -f /builds/${CIRCLE_USERNAME}-api-dse.apply-done ]; do
              sleep 1; WAITED=$((WAITED+1))
              if [ ${WAITED} -ge 60 ]; then echo "❌ Deploy timed out"; exit 1; fi
            done

            EXIT_CODE=$(cat /builds/${CIRCLE_USERNAME}-api-dse.apply-exitcode 2>/dev/null || echo "1")
            if [ "${EXIT_CODE}" != "0" ]; then
              echo "❌ Deploy failed"
              cat /builds/${CIRCLE_USERNAME}-api-dse.apply-log 2>/dev/null
              exit 1
            fi
            echo "✅ Deployed api"

  deploy-ui:
    machine: true
    resource_class: kindling/self-hosted
    environment:
      REGISTRY: "registry:5000"
    steps:
      - run:
          name: Set image tag
          command: |
            echo "export TAG=${CIRCLE_USERNAME}-${CIRCLE_SHA1:0:8}" >> "$BASH_ENV"

      - run:
          name: Deploy UI
          command: |
            cat > /builds/${CIRCLE_USERNAME}-ui-dse.yaml \<<EOF
            apiVersion: apps.example.com/v1alpha1
            kind: DevStagingEnvironment
            metadata:
              name: ${CIRCLE_USERNAME}-ui
              labels:
                app.kubernetes.io/name: ${CIRCLE_USERNAME}-ui
                app.kubernetes.io/managed-by: kindling
                app.kubernetes.io/part-of: my-app
                app.kubernetes.io/component: ui
                apps.example.com/circleci-username: ${CIRCLE_USERNAME}
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
                    value: "http://${CIRCLE_USERNAME}-api:8080"
              service:
                port: 80
                type: ClusterIP
              ingress:
                enabled: true
                host: ${CIRCLE_USERNAME}-ui.localhost
                ingressClassName: nginx
            EOF

            touch /builds/${CIRCLE_USERNAME}-ui-dse.apply

            WAITED=0
            while [ ! -f /builds/${CIRCLE_USERNAME}-ui-dse.apply-done ]; do
              sleep 1; WAITED=$((WAITED+1))
              if [ ${WAITED} -ge 60 ]; then echo "❌ Deploy timed out"; exit 1; fi
            done

            EXIT_CODE=$(cat /builds/${CIRCLE_USERNAME}-ui-dse.apply-exitcode 2>/dev/null || echo "1")
            if [ "${EXIT_CODE}" != "0" ]; then
              echo "❌ Deploy failed"
              cat /builds/${CIRCLE_USERNAME}-ui-dse.apply-log 2>/dev/null
              exit 1
            fi

            echo "🎉 Deploy complete!"
            echo "🌐 UI:  http://${CIRCLE_USERNAME}-ui.localhost"
            echo "🌐 API: http://${CIRCLE_USERNAME}-api.localhost"

workflows:
  dev-deploy:
    jobs:
      - build-api:
          filters:
            branches:
              only: main
      - build-ui:
          filters:
            branches:
              only: main
      - deploy-api:
          requires: [build-api]
      - deploy-ui:
          requires: [build-ui, deploy-api]`
