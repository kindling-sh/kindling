---
sidebar_position: 6
title: CRD Reference
description: Full spec reference for DevStagingEnvironment and CIRunnerPool custom resources.
---

# CRD Reference

kindling defines two Custom Resource Definitions (CRDs) under the
`apps.example.com/v1alpha1` API group.

---

## DevStagingEnvironment

Declares a complete application environment: a Deployment, Service,
optional Ingress, and zero or more auto-provisioned backing services.

**API version:** `apps.example.com/v1alpha1`  
**Kind:** `DevStagingEnvironment`  
**Scope:** Namespaced  
**Short name:** `dse`

### Full spec

```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: my-app
spec:
  deployment:
    image: ""
    port: 8080
    replicas: 1
    command: []
    args: []
    env:
      - name: KEY
        value: "value"
    resources:
      cpuRequest: "100m"
      cpuLimit: "500m"
      memoryRequest: "128Mi"
      memoryLimit: "512Mi"
    healthCheck:
      path: "/healthz"
      port: 8080
      initialDelaySeconds: 5
      periodSeconds: 10

  service:
    port: 8080
    targetPort: 8080
    type: "ClusterIP"

  ingress:
    enabled: true
    host: "app.localhost"
    path: "/"
    pathType: "Prefix"
    ingressClassName: "traefik"
    annotations:
      traefik.ingress.kubernetes.io/router.entrypoints: web
    tls:
      secretName: "tls-secret"
      hosts:
        - "app.localhost"
    routes:
      - path: "/orders"
        service: "my-orders-svc"
        port: 5000
      - path: "/inventory"
        pathType: "Exact"
        service: "my-inventory-svc"
        port: 3000

  dependencies:
    - type: postgres
      version: "16"
      image: ""
      port: 5432
      envVarName: "DATABASE_URL"
      storageSize: "1Gi"
      env:
        - name: POSTGRES_USER
          value: "custom"
      resources:
        cpuRequest: "100m"
        memoryLimit: "512Mi"
    - type: redis
      shared: true
      sharedKey: "billing"
```

### Spec fields

#### `spec.deployment`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `image` | string | ✅ | — | Container image reference |
| `port` | int32 | ✅ | — | Container port (1–65535) |
| `replicas` | *int32 | ❌ | `1` | Number of pod replicas |
| `command` | []string | ❌ | — | Override container entrypoint |
| `args` | []string | ❌ | — | Arguments passed to entrypoint |
| `env` | []EnvVar | ❌ | — | Environment variables |
| `resources` | *ResourceRequirements | ❌ | — | CPU/memory requests and limits |
| `healthCheck` | *HealthCheckSpec | ❌ | — | Liveness and readiness probe config |

#### `spec.service`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `port` | int32 | ✅ | — | Service port (1–65535) |
| `targetPort` | *int32 | ❌ | deployment port | Backend target port |
| `type` | string | ❌ | `"ClusterIP"` | `ClusterIP`, `NodePort`, or `LoadBalancer` |

#### `spec.ingress`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | bool | ✅ | `false` | Whether to create an Ingress |
| `host` | string | ❌ | — | Hostname for the Ingress rule |
| `path` | string | ❌ | `"/"` | URL path prefix for the primary route |
| `pathType` | string | ❌ | `"Prefix"` | `Prefix`, `Exact`, `ImplementationSpecific` |
| `ingressClassName` | *string | ❌ | — | IngressClass name (e.g. `"traefik"`) |
| `annotations` | map[string]string | ❌ | — | Extra Ingress annotations |
| `tls` | *IngressTLSSpec | ❌ | — | TLS configuration |
| `routes` | []IngressRouteSpec | ❌ | — | Extra path -> service routes merged onto this same Ingress (see below) |

##### `spec.ingress.routes[]`

Additional path -> service routes merged onto the **same** Ingress
object/host as the primary route above — useful for a frontend (e.g. an
SPA) that calls several backend services via same-origin path prefixes
(`/orgs`, `/auth`, `/billing`, ...) under one shared host, without a
second, unmanaged Ingress object.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | ✅ | — | URL path to match, e.g. `"/orders"` |
| `pathType` | string | ❌ | `"Prefix"` | `Prefix`, `Exact`, `ImplementationSpecific` |
| `service` | string | ✅ | — | Kubernetes Service name to route to |
| `port` | int32 | ✅ | — | Service port to route to |

Manage routes without editing YAML directly via `kindling ingress`:

```bash
# Patch the live DSE's Ingress with a new route
kindling ingress add-route my-app --path /orders --service my-orders-svc --port 5000

# See the primary route + all extra routes
kindling ingress list my-app

# Remove a route
kindling ingress remove-route my-app --path /orders

# Once confirmed working, persist it into the DSE YAML so it's applied
# on every future deploy
kindling ingress save my-app -f dev-environment.yaml
```

#### `spec.dependencies[]`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | DependencyType | ✅ | — | See supported types below |
| `version` | string | ❌ | latest | Image tag |
| `image` | string | ❌ | — | Full image override |
| `port` | *int32 | ❌ | type default | Override service port |
| `envVarName` | string | ❌ | type default | Override injected env var name |
| `storageSize` | *Quantity | ❌ | `"1Gi"` | PVC size for stateful deps |
| `env` | []EnvVar | ❌ | — | Override dependency container env vars |
| `resources` | *ResourceRequirements | ❌ | — | CPU/memory for dependency container |
| `shared` | bool | ❌ | `false` | Converge with other DSEs on one instance per type/sharedKey instead of a dedicated instance per DSE (see [Dependencies](dependencies.md#shared-dependencies)) |
| `sharedKey` | string | ❌ | dependency Type | Groups shared instances into separate instances within the namespace |

**Supported dependency types:**

`postgres` · `redis` · `mysql` · `mongodb` · `rabbitmq` · `minio` ·
`elasticsearch` · `kafka` · `nats` · `memcached` · `cassandra` ·
`consul` · `vault` · `influxdb` · `jaeger`

### Status fields

| Field | Type | Description |
|---|---|---|
| `availableReplicas` | int32 | Number of ready pods |
| `deploymentReady` | bool | Deployment has reached desired state |
| `serviceReady` | bool | Service has been created |
| `ingressReady` | bool | Ingress has been created (if enabled) |
| `dependenciesReady` | bool | All declared dependencies are running |
| `url` | string | Externally reachable URL |
| `conditions` | []Condition | Standard Kubernetes conditions |

### Examples

**Minimal:**
```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: simple-app
spec:
  deployment:
    image: nginx:1.25
    port: 80
  service:
    port: 80
```

**Full-featured:**
```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: platform-api
spec:
  deployment:
    image: registry:5000/platform:v2
    replicas: 2
    port: 8080
    env:
      - name: LOG_LEVEL
        value: debug
    resources:
      cpuRequest: "250m"
      cpuLimit: "1"
      memoryRequest: "256Mi"
      memoryLimit: "1Gi"
    healthCheck:
      path: /healthz
  service:
    port: 8080
  ingress:
    enabled: true
    host: platform.localhost
    ingressClassName: traefik
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
    - type: elasticsearch
    - type: kafka
    - type: vault
```

---

## CIRunnerPool

Declares a pool of self-hosted CI runners. Supports **GitHub Actions**
and **GitLab CI** via the `spec.ciProvider` field (defaults to `github`).

**API version:** `apps.example.com/v1alpha1`  
**Kind:** `CIRunnerPool`  
**Scope:** Namespaced

### Full spec

```yaml
apiVersion: apps.example.com/v1alpha1
kind: CIRunnerPool
metadata:
  name: myuser-runner-pool
spec:
  ciProvider: github              # "github" or "gitlab"
  githubUsername: "myuser"
  repository: "myorg/myrepo"
  tokenSecretRef:
    name: github-runner-token
    key: github-token
  githubURL: "https://github.com"
  replicas: 1
  runnerImage: "ghcr.io/actions/actions-runner:latest"
  labels:
    - linux
```

### Spec fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `ciProvider` | string | ✅ | `""` | CI platform: `github` or `gitlab` |
| `githubUsername` | string | ✅ | — | Developer handle (added as runner label) |
| `repository` | string | ✅ | — | Repo slug — `owner/repo` (GitHub) or `group/project` (GitLab) |
| `tokenSecretRef` | SecretKeyRef | ✅ | — | Reference to CI token Secret |
| `githubURL` | string | ❌ | `https://github.com` | Base URL (for GHE or custom GitLab instance) |
| `replicas` | *int32 | ❌ | `1` | Number of runner pods |
| `runnerImage` | string | ❌ | `ghcr.io/actions/actions-runner:latest` | Runner container image |
| `labels` | []string | ❌ | — | Extra runner labels |

### Runner pod structure

Each runner pod created by the operator contains:

| Container | Image | Purpose |
|---|---|---|
| `runner` | Platform-specific runner image | Registers with CI platform (GitHub or GitLab), executes workflow jobs |
| `build-agent` | Kaniko executor | Builds container images from signal files |

Shared volume: `emptyDir` at `/builds/` for signal-file communication.

Runner labels automatically include:
- `self-hosted`
- `<githubUsername>` (from spec)
- Any extra labels from `spec.labels`

Workflows target a specific developer's runner with:
```yaml
runs-on: [self-hosted, "<username>"]
```
