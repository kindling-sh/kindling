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
  deployment:           # Required — configures the app Deployment
    image: ""           # Required — container image (e.g. "registry:5000/app:v1")
    port: 8080          # Required — container port (1–65535)
    replicas: 1         # Optional — pod replicas (default: 1, min: 1)
    command: []         # Optional — override container entrypoint
    args: []            # Optional — arguments to entrypoint
    env:                # Optional — environment variables
      - name: KEY
        value: "value"
    resources:          # Optional — CPU/memory requests and limits
      cpuRequest: "100m"
      cpuLimit: "500m"
      memoryRequest: "128Mi"
      memoryLimit: "512Mi"
    healthCheck:        # Optional — liveness and readiness probes
      path: "/healthz"            # HTTP path (default: "/healthz")
      port: 8080                  # Override probe port (default: container port)
      initialDelaySeconds: 5      # Delay before first probe (default: 5)
      periodSeconds: 10           # Probe interval (default: 10)

  service:              # Required — configures the Service
    port: 8080          # Required — service port (1–65535)
    targetPort: 8080    # Optional — backend port (default: deployment port)
    type: "ClusterIP"   # Optional — ClusterIP | NodePort | LoadBalancer

  ingress:              # Optional — configures external access
    enabled: true       # Required if block present — create Ingress resource
    host: "app.localhost"         # Hostname for the Ingress rule
    path: "/"                     # URL path prefix (default: "/")
    pathType: "Prefix"            # Prefix | Exact | ImplementationSpecific
    ingressClassName: "nginx"     # IngressClass name
    annotations:                  # Extra annotations on the Ingress
      nginx.ingress.kubernetes.io/rewrite-target: /
    tls:                          # Optional — TLS termination
      secretName: "tls-secret"
      hosts:
        - "app.localhost"

  dependencies:         # Optional — auto-provisioned backing services
    - type: postgres              # Required — dependency type (see below)
      version: "16"               # Optional — image tag
      image: ""                   # Optional — full image override
      port: 5432                  # Optional — override default port
      envVarName: "DATABASE_URL"  # Optional — override injected env var name
      storageSize: "1Gi"          # Optional — PVC size for stateful deps
      env:                        # Optional — override container env vars
        - name: POSTGRES_USER
          value: "custom"
      resources:                  # Optional — CPU/memory for dep container
        cpuRequest: "100m"
        memoryLimit: "512Mi"
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

#### `spec.deployment.resources`

| Field | Type | Example |
|---|---|---|
| `cpuRequest` | *Quantity | `"100m"` |
| `cpuLimit` | *Quantity | `"500m"` |
| `memoryRequest` | *Quantity | `"128Mi"` |
| `memoryLimit` | *Quantity | `"512Mi"` |

#### `spec.deployment.healthCheck`

| Field | Type | Default | Description |
|---|---|---|---|
| `path` | string | `"/healthz"` | HTTP GET path |
| `port` | *int32 | container port | Override probe port |
| `initialDelaySeconds` | *int32 | `5` | Delay before first probe |
| `periodSeconds` | *int32 | `10` | Probe interval |

When `healthCheck` is specified, the operator configures both
liveness and readiness probes with the same settings.

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
| `path` | string | ❌ | `"/"` | URL path prefix |
| `pathType` | string | ❌ | `"Prefix"` | `Prefix`, `Exact`, `ImplementationSpecific` |
| `ingressClassName` | *string | ❌ | — | IngressClass name (e.g. `"nginx"`) |
| `annotations` | map[string]string | ❌ | — | Extra Ingress annotations |
| `tls` | *IngressTLSSpec | ❌ | — | TLS configuration |

#### `spec.ingress.tls`

| Field | Type | Required | Description |
|---|---|---|---|
| `secretName` | string | ✅ | TLS Secret name |
| `hosts` | []string | ❌ | Hosts covered by the cert (defaults to ingress host) |

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

**Supported dependency types:**

`postgres` · `redis` · `mysql` · `mongodb` · `rabbitmq` · `minio` ·
`elasticsearch` · `kafka` · `nats` · `memcached` · `cassandra` ·
`consul` · `vault` · `influxdb` · `jaeger`

See [dependencies.md](dependencies.md) for complete details on each type.

### Status fields

| Field | Type | Description |
|---|---|---|
| `availableReplicas` | int32 | Number of ready pods |
| `deploymentReady` | bool | Deployment has reached desired state |
| `serviceReady` | bool | Service has been created |
| `ingressReady` | bool | Ingress has been created (if enabled) |
| `dependenciesReady` | bool | All declared dependencies are running |
| `url` | string | Externally reachable URL (if Ingress configured) |
| `conditions` | []Condition | Standard Kubernetes conditions |

**Conditions:**

| Type | Description |
|---|---|
| `Ready` | `True` when Deployment, Service, Ingress, and Dependencies are all ready |
| `DeploymentReady` | Deployment reconciliation status |
| `ServiceReady` | Service reconciliation status |
| `IngressReady` | Ingress reconciliation status |
| `DependenciesReady` | Dependency reconciliation status |

### Print columns (kubectl)

```
NAME    IMAGE                          REPLICAS   AVAILABLE   READY   AGE
myapp   registry:5000/myapp:abc123     1          1           true    5m
```

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

**With Ingress:**
```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: web-app
spec:
  deployment:
    image: registry:5000/web:latest
    port: 8080
    healthCheck:
      path: /healthz
  service:
    port: 8080
  ingress:
    enabled: true
    host: web-app.localhost
    ingressClassName: nginx
```

**Full-featured:**
```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: platform-api
  labels:
    team: backend
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
      initialDelaySeconds: 10
      periodSeconds: 15
  service:
    port: 8080
    type: ClusterIP
  ingress:
    enabled: true
    host: platform.localhost
    ingressClassName: nginx
    annotations:
      nginx.ingress.kubernetes.io/proxy-read-timeout: "120"
  dependencies:
    - type: postgres
      version: "16"
      storageSize: "5Gi"
    - type: redis
    - type: elasticsearch
    - type: kafka
    - type: vault
```

---

## GithubActionRunnerPool

Declares a pool of self-hosted CI runners. The current implementation
registers GitHub Actions runners with a specific repository.

**API version:** `apps.example.com/v1alpha1`  
**Kind:** `GithubActionRunnerPool`  
**Scope:** Namespaced

### Full spec

```yaml
apiVersion: apps.example.com/v1alpha1
kind: GithubActionRunnerPool
metadata:
  name: myuser-runner-pool
spec:
  githubUsername: "myuser"         # Required — GitHub handle
  repository: "myorg/myrepo"      # Required — full repo slug
  tokenSecretRef:                  # Required — reference to PAT secret
    name: github-runner-token
    key: github-token              # Default: "github-token"
  githubURL: "https://github.com" # Optional — for GitHub Enterprise
  replicas: 1                     # Optional — runner count (default: 1)
  runnerImage: "ghcr.io/actions/actions-runner:latest"  # Optional
  labels:                         # Optional — extra runner labels
    - linux
    - gpu
  runnerGroup: "Default"          # Optional — GitHub runner group
  serviceAccountName: ""          # Optional — K8s service account
  workDir: "/home/runner/_work"   # Optional — working directory
  env:                            # Optional — extra env vars
    - name: RUNNER_DEBUG
      value: "1"
  resources:                      # Optional — CPU/memory limits
    cpuRequest: "500m"
    cpuLimit: "2"
    memoryRequest: "512Mi"
    memoryLimit: "4Gi"
  volumeMounts: []                # Optional — extra volume mounts
  volumes: []                     # Optional — extra volumes
```

### Spec fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `githubUsername` | string | ✅ | — | GitHub handle (added as runner label) |
| `repository` | string | ✅ | — | Full repo slug (`owner/repo`) |
| `tokenSecretRef` | SecretKeyRef | ✅ | — | Reference to PAT Secret |
| `githubURL` | string | ❌ | `https://github.com` | Base URL (for GHE) |
| `replicas` | *int32 | ❌ | `1` | Number of runner pods |
| `runnerImage` | string | ❌ | `ghcr.io/actions/actions-runner:latest` | Runner container image |
| `labels` | []string | ❌ | — | Extra runner labels |
| `runnerGroup` | string | ❌ | `"Default"` | GitHub runner group |
| `serviceAccountName` | string | ❌ | — | K8s ServiceAccount |
| `workDir` | string | ❌ | `/home/runner/_work` | Working directory path |
| `env` | []EnvVar | ❌ | — | Extra environment variables |
| `resources` | *RunnerResourceRequirements | ❌ | — | CPU/memory for runner pod |
| `volumeMounts` | []VolumeMount | ❌ | — | Additional volume mounts |
| `volumes` | []Volume | ❌ | — | Additional volumes |

#### `spec.tokenSecretRef`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | ✅ | — | Secret name |
| `key` | string | ❌ | `"github-token"` | Key within Secret data |

#### `spec.resources`

| Field | Type | Example |
|---|---|---|
| `cpuRequest` | *Quantity | `"500m"` |
| `cpuLimit` | *Quantity | `"2"` |
| `memoryRequest` | *Quantity | `"512Mi"` |
| `memoryLimit` | *Quantity | `"4Gi"` |

### Status fields

| Field | Type | Description |
|---|---|---|
| `replicas` | int32 | Desired replica count |
| `readyRunners` | int32 | Number of ready runner pods |
| `runnerRegistered` | bool | Runner registered with GitHub |
| `activeJob` | string | Currently executing job ID (or empty) |
| `lastJobCompleted` | *Time | Timestamp of last completed job |
| `devEnvironmentRef` | string | Name of last DSE CR created by a job |
| `conditions` | []Condition | Standard Kubernetes conditions |

### Print columns (kubectl)

```
NAME                 USER     REPOSITORY       REPLICAS   READY   AGE
myuser-runner-pool   myuser   myorg/myrepo     1          1       10m
```

### Runner pod structure

Each runner pod created by the operator contains:

| Container | Image | Purpose |
|---|---|---|
| `runner` | Runner image | Registers with GitHub, executes workflow jobs |
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

### Example

```yaml
apiVersion: apps.example.com/v1alpha1
kind: GithubActionRunnerPool
metadata:
  name: alice-runner-pool
spec:
  githubUsername: "alice"
  repository: "acme/platform"
  tokenSecretRef:
    name: github-runner-token
    key: github-token
  replicas: 1
  labels:
    - linux
```
