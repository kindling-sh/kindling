---
sidebar_position: 5
title: GitLab CI Reference
description: Documentation for kindling GitLab CI pipeline generation and build/deploy mechanism.
---

# GitLab CI Reference

kindling generates `.gitlab-ci.yml` pipelines that use the same Kaniko
sidecar mechanism as GitHub Actions — but with inline shell scripts
instead of reusable composite actions.

```bash
# Generate a GitLab CI pipeline
kindling generate -k <api-key> -r . --ci-provider gitlab
```

---

## How it works

GitLab CI pipelines run on a self-hosted runner pod inside the Kind
cluster. The runner pod has:

- **A GitLab Runner** — picks up jobs from your GitLab project
- **A Kaniko build-agent sidecar** — builds container images without Docker
- **A shared `/builds` volume** — communication between runner and sidecar via signal files

The pipeline uses inline shell scripts that write signal files to
`/builds/`, triggering the Kaniko sidecar to build images and apply
DevStagingEnvironment manifests.

---

## Runner setup

```bash
# Register a GitLab CI runner (needs a PAT with create_runner scope)
kindling runners --ci-provider gitlab \
  -u <gitlab-user> \
  -r <group/project> \
  -t <gitlab-pat>
```

The runner auto-registers with your GitLab project and starts picking up
pipeline jobs. On pod shutdown, it de-registers itself automatically.

---

## Build mechanism

To build a container image, the pipeline script:

1. Creates a tarball of the build context: `tar -czf /builds/<name>.tar.gz -C <context> .`
2. Writes the destination image: `echo "<image>" > /builds/<name>.dest`
3. Optionally writes a custom Dockerfile path: `echo "<path>" > /builds/<name>.dockerfile`
4. Triggers the sidecar: `touch /builds/<name>.request`
5. Polls for `/builds/<name>.done` (default timeout: 300s)
6. Checks `/builds/<name>.exitcode` for success (0) or failure

### Build script example

```yaml
build-my-app:
  stage: build
  tags: [self-hosted, kindling]
  script:
    - rm -f /builds/*.done /builds/*.request /builds/*.tar.gz
    - tar -czf /builds/my-app.tar.gz -C ${CI_PROJECT_DIR} .
    - echo "${REGISTRY}/my-app:${TAG}" > /builds/my-app.dest
    - touch /builds/my-app.request
    - |
      WAITED=0
      while [ ! -f /builds/my-app.done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 300 ]; then
          echo "❌ Build timed out"; exit 1
        fi
      done
    - |
      EXIT_CODE=$(cat /builds/my-app.exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Build failed"; cat /builds/my-app.log 2>/dev/null; exit 1
      fi
      echo "✅ Built my-app"
```

---

## Deploy mechanism

To deploy a DevStagingEnvironment CR, the pipeline script:

1. Generates a DSE YAML file at `/builds/<name>-dse.yaml`
2. Triggers the sidecar: `touch /builds/<name>-dse.apply`
3. Waits for `/builds/<name>-dse.apply-done`
4. Checks `/builds/<name>-dse.apply-exitcode`

### Deploy script example

```yaml
deploy-my-app:
  stage: deploy
  tags: [self-hosted, kindling]
  script:
    - |
      cat > /builds/${KINDLING_USER}-my-app-dse.yaml <<EOF
      apiVersion: apps.example.com/v1alpha1
      kind: DevStagingEnvironment
      metadata:
        name: ${KINDLING_USER}-my-app
        labels:
          app.kubernetes.io/name: ${KINDLING_USER}-my-app
          app.kubernetes.io/managed-by: kindling
      spec:
        deployment:
          image: ${REGISTRY}/my-app:${TAG}
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
          host: ${KINDLING_USER}-my-app.localhost
          ingressClassName: traefik
      EOF
    - touch /builds/${KINDLING_USER}-my-app-dse.apply
    - |
      WAITED=0
      while [ ! -f /builds/${KINDLING_USER}-my-app-dse.apply-done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 120 ]; then
          echo "❌ Deploy timed out"; exit 1
        fi
      done
    - |
      EXIT_CODE=$(cat /builds/${KINDLING_USER}-my-app-dse.apply-exitcode 2>/dev/null || echo "1")
      if [ "${EXIT_CODE}" != "0" ]; then
        echo "❌ Deploy failed"; exit 1
      fi
      echo "✅ Deployed my-app"
```

---

## Key conventions

| Convention | Value |
|---|---|
| Registry | `registry:5000` (in-cluster) |
| Image tag | `${KINDLING_USER}-${CI_COMMIT_SHORT_SHA}` |
| Runner tags | `[self-hosted, kindling]` |
| Ingress host | `${KINDLING_USER}-<service>.localhost` |
| DSE name | `${KINDLING_USER}-<service>` |

:::warning KINDLING_USER vs GITLAB_USER_LOGIN
Do **not** use `$GITLAB_USER_LOGIN` — it often resolves to a project bot
username that breaks DNS naming rules. Instead, set `KINDLING_USER` explicitly
in the `variables:` block as a valid DNS-1035 label (lowercase, alphanumeric,
hyphens only).
:::

---

## Complete pipeline example

A multi-service pipeline with an API and UI:

```yaml
variables:
  REGISTRY: "registry:5000"
  KINDLING_USER: "myusername"
  TAG: "${KINDLING_USER}-${CI_COMMIT_SHORT_SHA}"

stages:
  - build
  - deploy

# ── Build ──────────────────────────────────────────────────
build-api:
  stage: build
  tags: [self-hosted, kindling]
  script:
    - rm -f /builds/*.done /builds/*.request /builds/*.tar.gz
    - tar -czf /builds/api.tar.gz -C ${CI_PROJECT_DIR} . --exclude ./ui
    - echo "${REGISTRY}/api:${TAG}" > /builds/api.dest
    - touch /builds/api.request
    - |
      WAITED=0
      while [ ! -f /builds/api.done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 300 ]; then echo "❌ Timed out"; exit 1; fi
      done
    - |
      EXIT=$(cat /builds/api.exitcode 2>/dev/null || echo "1")
      if [ "${EXIT}" != "0" ]; then echo "❌ Failed"; exit 1; fi

build-ui:
  stage: build
  tags: [self-hosted, kindling]
  script:
    - tar -czf /builds/ui.tar.gz -C ${CI_PROJECT_DIR}/ui .
    - echo "${REGISTRY}/ui:${TAG}" > /builds/ui.dest
    - touch /builds/ui.request
    - |
      WAITED=0
      while [ ! -f /builds/ui.done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 300 ]; then echo "❌ Timed out"; exit 1; fi
      done
    - |
      EXIT=$(cat /builds/ui.exitcode 2>/dev/null || echo "1")
      if [ "${EXIT}" != "0" ]; then echo "❌ Failed"; exit 1; fi

# ── Deploy ─────────────────────────────────────────────────
deploy-api:
  stage: deploy
  tags: [self-hosted, kindling]
  script:
    - |
      cat > /builds/${KINDLING_USER}-api-dse.yaml <<EOF
      apiVersion: apps.example.com/v1alpha1
      kind: DevStagingEnvironment
      metadata:
        name: ${KINDLING_USER}-api
      spec:
        deployment:
          image: ${REGISTRY}/api:${TAG}
          port: 8080
          healthCheck:
            path: /healthz
        service:
          port: 8080
        ingress:
          enabled: true
          host: ${KINDLING_USER}-api.localhost
          ingressClassName: traefik
        dependencies:
          - type: postgres
            version: "16"
          - type: redis
      EOF
    - touch /builds/${KINDLING_USER}-api-dse.apply
    - |
      WAITED=0
      while [ ! -f /builds/${KINDLING_USER}-api-dse.apply-done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 120 ]; then echo "❌ Timed out"; exit 1; fi
      done

deploy-ui:
  stage: deploy
  tags: [self-hosted, kindling]
  script:
    - |
      cat > /builds/${KINDLING_USER}-ui-dse.yaml <<EOF
      apiVersion: apps.example.com/v1alpha1
      kind: DevStagingEnvironment
      metadata:
        name: ${KINDLING_USER}-ui
      spec:
        deployment:
          image: ${REGISTRY}/ui:${TAG}
          port: 80
          healthCheck:
            path: /
          env:
            - name: API_URL
              value: "http://${KINDLING_USER}-api:8080"
        service:
          port: 80
        ingress:
          enabled: true
          host: ${KINDLING_USER}-ui.localhost
          ingressClassName: traefik
      EOF
    - touch /builds/${KINDLING_USER}-ui-dse.apply
    - |
      WAITED=0
      while [ ! -f /builds/${KINDLING_USER}-ui-dse.apply-done ]; do
        sleep 2; WAITED=$((WAITED+2))
        if [ ${WAITED} -ge 120 ]; then echo "❌ Timed out"; exit 1; fi
      done
```

---

## Heredoc escaping

:::warning
When a DSE env var uses Kubernetes dependent-variable syntax `$(VAR_NAME)`
inside a bash heredoc (`<<EOF ... EOF`), you **must** escape the dollar sign:

```yaml
# ❌ WRONG — bash interprets $(REDIS_URL) as command substitution
- name: CACHE_URL
  value: "$(REDIS_URL)"

# ✅ CORRECT — escaped for bash heredoc
- name: CACHE_URL
  value: "\$(REDIS_URL)"
```
:::

---

## Differences from GitHub Actions

| Aspect | GitHub Actions | GitLab CI |
|---|---|---|
| Build/deploy | Reusable composite actions (`uses:`) | Inline shell scripts |
| Actor variable | `${{ github.actor }}` | `${KINDLING_USER}` (set manually) |
| SHA variable | `${{ github.sha }}` | `${CI_COMMIT_SHORT_SHA}` |
| Workspace | `${{ github.workspace }}` | `${CI_PROJECT_DIR}` |
| Runner selector | `runs-on: [self-hosted, "${{ github.actor }}"]` | `tags: [self-hosted, kindling]` |
| Workflow file | `.github/workflows/dev-deploy.yml` | `.gitlab-ci.yml` |

---

## Troubleshooting

### Runner not picking up jobs

- Verify runner is registered: check your project's **Settings → CI/CD → Runners**
- Check runner pod is running: `kubectl get pods -l app.kubernetes.io/component=gitlab-ci-runner`
- Check runner logs: `kubectl logs -l app.kubernetes.io/component=gitlab-ci-runner`

### Build times out

Increase the timeout in the poll loop (default is 300 seconds):

```bash
if [ ${WAITED} -ge 600 ]; then  # 10 minutes
```

### Deploy fails with "command not found"

You're likely hitting the heredoc escaping issue. Escape `$(VAR_NAME)` as
`\$(VAR_NAME)` inside `<<EOF` blocks.
