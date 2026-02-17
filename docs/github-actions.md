# GitHub Actions Reference

kindling ships two **reusable composite actions** that replace 15+ lines
of signal-file boilerplate with a single `uses:` step. These actions
live in the kindling repo under `.github/actions/` and are referenced as:

```
jeff-vincent/kindling/.github/actions/kindling-build@main
jeff-vincent/kindling/.github/actions/kindling-deploy@main
```

---

## kindling-build

Build and push a container image via the Kaniko build-agent sidecar.

> **⚠️ Dockerfile required:** This action runs the `Dockerfile` found in the build context directory as-is using Kaniko. It does **not** generate or modify Dockerfiles. Each service must have a working Dockerfile that builds successfully on its own (e.g. `docker build .`). If it doesn't build locally, it won't build in kindling. Kaniko is stricter than local Docker in some cases — for example, `COPY`-ing a file that doesn't exist (like a missing lockfile) will fail immediately rather than being silently skipped.

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `name` | ✅ | — | Unique build name (used for signal files: `/builds/<name>.*`) |
| `context` | ✅ | — | Path to the build context directory |
| `image` | ✅ | — | Full image reference (`registry/name:tag`) |
| `exclude` | ❌ | `""` | `tar --exclude` patterns (space-separated, e.g. `"./ui ./.git"`) |
| `timeout` | ❌ | `300` | Max seconds to wait for the build to complete |

### What it does

1. Creates a tarball of the build context at `/builds/<name>.tar.gz`
   (with optional `--exclude` patterns)
2. Writes the target image to `/builds/<name>.dest`
3. Touches `/builds/<name>.request` to trigger the Kaniko sidecar
4. Polls for `/builds/<name>.done` (up to `timeout` seconds)
5. Checks `/builds/<name>.exitcode` — exits non-zero on failure

### Usage

```yaml
- name: Build my-app
  uses: jeff-vincent/kindling/.github/actions/kindling-build@main
  with:
    name: my-app
    context: ${{ github.workspace }}
    image: "registry:5000/my-app:${{ github.sha }}"
```

### With exclusions

```yaml
- name: Build API (exclude UI directory)
  uses: jeff-vincent/kindling/.github/actions/kindling-build@main
  with:
    name: my-api
    context: ${{ github.workspace }}
    image: "registry:5000/my-api:${{ github.sha }}"
    exclude: "./ui ./.git ./node_modules"
```

### With custom timeout

```yaml
- name: Build large image
  uses: jeff-vincent/kindling/.github/actions/kindling-build@main
  with:
    name: my-big-app
    context: ${{ github.workspace }}
    image: "registry:5000/my-big-app:${{ github.sha }}"
    timeout: "600"
```

---

## kindling-deploy

Deploy a DevStagingEnvironment CR via the build-agent sidecar.

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `name` | ✅ | — | DSE `metadata.name` (typically `<actor>-<service>`) |
| `image` | ✅ | — | Container image reference |
| `port` | ✅ | — | Container port (string) |
| `labels` | ❌ | `""` | Extra labels as YAML block |
| `env` | ❌ | `""` | Extra env vars as YAML block |
| `dependencies` | ❌ | `""` | Dependencies as YAML block |
| `ingress-host` | ❌ | `""` | Ingress hostname (omit to skip ingress) |
| `ingress-class` | ❌ | `nginx` | Ingress class name |
| `health-check-path` | ❌ | `/healthz` | HTTP health check path |
| `replicas` | ❌ | `1` | Number of replicas |
| `service-type` | ❌ | `ClusterIP` | Service type |
| `wait` | ❌ | `true` | Wait for deployment rollout |
| `wait-timeout` | ❌ | `180s` | Rollout wait timeout |

### What it does

1. Generates a complete `DevStagingEnvironment` YAML manifest from the
   inputs
2. Writes it to `/builds/<name>-dse.yaml`
3. Touches `/builds/<name>-dse.apply` to trigger sidecar `kubectl apply`
4. Waits for the sidecar to confirm the apply succeeded
5. Optionally waits for the Deployment rollout to complete

### Basic usage

```yaml
- name: Deploy my-app
  uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-app"
    image: "registry:5000/my-app:${{ github.sha }}"
    port: "8080"
    ingress-host: "${{ github.actor }}-my-app.localhost"
```

### With dependencies

```yaml
- name: Deploy with Postgres + Redis
  uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-app"
    image: "registry:5000/my-app:${{ github.sha }}"
    port: "8080"
    ingress-host: "${{ github.actor }}-my-app.localhost"
    dependencies: |
      - type: postgres
        version: "16"
      - type: redis
```

### With env vars

```yaml
- name: Deploy with environment variables
  uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-ui"
    image: "registry:5000/my-ui:${{ github.sha }}"
    port: "80"
    health-check-path: "/"
    ingress-host: "${{ github.actor }}-my-ui.localhost"
    env: |
      - name: API_URL
        value: "http://${{ github.actor }}-my-api:8080"
      - name: NODE_ENV
        value: "development"
```

### With all options

```yaml
- name: Deploy full-featured service
  uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-platform"
    image: "registry:5000/platform:${{ github.sha }}"
    port: "8080"
    replicas: "2"
    service-type: "ClusterIP"
    health-check-path: "/healthz"
    ingress-host: "${{ github.actor }}-platform.localhost"
    ingress-class: "nginx"
    labels: |
      app.kubernetes.io/part-of: my-platform
      tier: backend
    env: |
      - name: LOG_LEVEL
        value: "debug"
    dependencies: |
      - type: postgres
        version: "16"
      - type: redis
      - type: elasticsearch
      - type: kafka
      - type: vault
    wait: "true"
    wait-timeout: "300s"
```

---

## Complete workflow example

A full workflow using both actions:

```yaml
name: Dev Deploy
on:
  push:
    branches: [main]

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]
    env:
      TAG: ${{ github.sha }}
    steps:
      - uses: actions/checkout@v4

      - name: Clean builds directory
        run: rm -f /builds/*

      # ── Build ──────────────────────────────────────────────
      - name: Build API image
        uses: jeff-vincent/kindling/.github/actions/kindling-build@main
        with:
          name: my-api
          context: ${{ github.workspace }}
          image: "registry:5000/my-api:${{ env.TAG }}"
          exclude: "./ui"

      - name: Build UI image
        uses: jeff-vincent/kindling/.github/actions/kindling-build@main
        with:
          name: my-ui
          context: "${{ github.workspace }}/ui"
          image: "registry:5000/my-ui:${{ env.TAG }}"

      # ── Deploy ─────────────────────────────────────────────
      - name: Deploy API
        uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-api"
          image: "registry:5000/my-api:${{ env.TAG }}"
          port: "8080"
          ingress-host: "${{ github.actor }}-my-api.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis

      - name: Deploy UI
        uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-ui"
          image: "registry:5000/my-ui:${{ env.TAG }}"
          port: "80"
          health-check-path: "/"
          ingress-host: "${{ github.actor }}-my-ui.localhost"
          env: |
            - name: API_URL
              value: "http://${{ github.actor }}-my-api:8080"
```

---

## Generated YAML

The `kindling-deploy` action generates a `DevStagingEnvironment` CR
like this:

```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: alice-my-api
  labels:
    app.kubernetes.io/name: alice-my-api
    app.kubernetes.io/managed-by: kindling
spec:
  deployment:
    image: registry:5000/my-api:abc123
    replicas: 1
    port: 8080
    healthCheck:
      path: /healthz
  service:
    port: 8080
    type: ClusterIP
  ingress:
    enabled: true
    host: alice-my-api.localhost
    ingressClassName: nginx
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
```

The YAML is written to `/builds/<name>-dse.yaml`, applied via the
sidecar, then the action optionally waits for the Deployment rollout.

---

## Troubleshooting

### Build times out

Increase the timeout:

```yaml
with:
  timeout: "600"  # 10 minutes
```

Check the build log:

```bash
cat /builds/<name>.log
```

### Deploy apply fails

The action prints the generated YAML. Check the output for:
- Invalid YAML indentation
- Missing required fields
- Image not found in registry

### Rollout doesn't complete

Check pod events:

```bash
kubectl describe pod -l app.kubernetes.io/name=<name>
kubectl logs -l app.kubernetes.io/name=<name>
```

Common causes:
- Image pull errors (image not pushed to registry:5000)
- Health check failing (wrong path or port)
- Dependency not ready (database still starting)
