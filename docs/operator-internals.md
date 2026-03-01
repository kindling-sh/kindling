# Operator Internals

This document describes the operator's reconciliation logic, CRD type
definitions, dependency provisioning, status management, and RBAC model.

Source: `internal/controller/`, `api/v1alpha1/`

---

## CRD types

### API Group and versions

```
Group:   apps.example.com
Version: v1alpha1
```

Registered in `api/v1alpha1/groupversion_info.go` via the
`kubebuilder:object:root` and `kubebuilder:subresource:status` markers.

### DevStagingEnvironment

**ShortName:** `dse`

```go
type DevStagingEnvironmentSpec struct {
    Services     []DeploymentSpec  // application containers
    Dependencies []DependencySpec  // auto-provisioned infra
    Env          []EnvVar          // extra env vars
}
```

#### DeploymentSpec

| Field | Type | Description |
|---|---|---|
| `name` | string | Deployment name, used as label selector |
| `image` | string | Container image (usually `registry:5000/<svc>:<tag>`) |
| `replicas` | *int32 | Replica count, defaults to 1 |
| `port` | int32 | Container port (exposed via Service) |
| `command` | []string | Override entrypoint |
| `args` | []string | Override args |
| `env` | []EnvVar | Per-service env vars |
| `resources` | corev1.ResourceRequirements | CPU/memory limits |
| `volumeMounts` | []VolumeMount | Volume mount definitions |
| `volumes` | []Volume | Volume definitions |
| `ingress` | *IngressSpec | Ingress configuration |
| `service` | *ServiceSpec | Service configuration override |
| `healthCheck` | *HealthCheckSpec | Liveness/readiness probes |
| `initContainers` | []Container | Extra init containers |

#### ServiceSpec

| Field | Type | Description |
|---|---|---|
| `type` | string | `ClusterIP` (default), `NodePort`, `LoadBalancer` |
| `port` | int32 | Service port (defaults to container port) |
| `targetPort` | int32 | Target port on pod |

#### IngressSpec

| Field | Type | Description |
|---|---|---|
| `enabled` | bool | Whether to create an Ingress |
| `host` | string | Hostname (e.g., `myapp.localhost`) |
| `path` | string | Path prefix (default `/`) |
| `pathType` | string | `Prefix` (default), `Exact`, `ImplementationSpecific` |
| `annotations` | map[string]string | Ingress annotations |
| `tls` | []IngressTLS | TLS configuration |

#### DependencySpec

| Field | Type | Description |
|---|---|---|
| `name` | string | Dependency name (becomes `<dse>-<name>`) |
| `type` | string | One of 15 supported types (see Dependencies doc) |
| `image` | string | Override default image |
| `port` | int32 | Override default port |
| `env` | []EnvVar | Extra env vars for the dependency pod |
| `envVarName` | string | Override auto-injected env var name |
| `storage` | string | PVC size (e.g., `1Gi`) |
| `version` | string | Image tag override |
| `config` | map[string]string | Type-specific configuration |

#### Status fields

```go
type DevStagingEnvironmentStatus struct {
    DeploymentReady   bool
    ServiceReady      bool
    IngressReady      bool
    DependenciesReady bool
    Conditions        []metav1.Condition
    URL               string   // constructed from Ingress host
    Services          []string // names of reconciled services
}
```

The `Ready` condition aggregates all four readiness flags. It's set to
`True` only when all are true.

### CIRunnerPool

```go
type CIRunnerPoolSpec struct {
    Provider      string // "github" or "gitlab"
    Owner         string // GitHub org or GitLab group
    Repository    string // repo name
    TokenSecret   string // K8s secret name containing PAT
    Labels        []string
    RunnerGroup   string
    Replicas      *int32
    Resources     corev1.ResourceRequirements
    GitLab        *GitLabConfig // GitLab-specific fields
}
```

#### Status fields

```go
type CIRunnerPoolStatus struct {
    Ready      bool
    Replicas   int32
    ActiveJobs int32
    Conditions []metav1.Condition
}
```

---

## DevStagingEnvironment Reconciler

Source: `internal/controller/devstagingenvironment_controller.go` (~1438 lines)

### Reconciliation flow

```
Reconcile() called
  │
  ├─ 1. Fetch DSE CR (return if NotFound)
  │
  ├─ 2. reconcileDependencies()
  │     └─ for each spec.dependencies[]:
  │          ├─ build credential Secret
  │          ├─ build Deployment (image, port, env from registry)
  │          ├─ build Service
  │          └─ create-or-update with spec-hash annotation
  │
  ├─ 3. pruneOrphanDependencies()
  │     └─ list Deployments with part-of=<cr>, delete if component ∉ spec
  │
  ├─ 4. reconcileDeployment()
  │     └─ for each spec.services[]:
  │          ├─ build Deployment (containers, init containers, volumes)
  │          ├─ inject dependency env vars (auto-injected URLs)
  │          ├─ add init containers for dependency readiness
  │          └─ create-or-update with spec-hash annotation
  │
  ├─ 5. reconcileService()
  │     └─ for each spec.services[]:
  │          ├─ build Service (type, port, selector)
  │          └─ create-or-update
  │
  ├─ 6. reconcileIngress()
  │     └─ for each spec.services[] where ingress.enabled:
  │          ├─ build Ingress (host, path, pathType, annotations, TLS)
  │          └─ create-or-update
  │
  └─ 7. updateStatus()
        ├─ check Deployment available replicas
        ├─ check Service exists
        ├─ check Ingress exists
        ├─ check all dependency Deployments have available replicas
        ├─ build Ready condition
        └─ status.Update()
```

### Spec-hash change detection

Every mutable child resource is annotated:

```go
annotations["apps.example.com/spec-hash"] = sha256(relevantSpecJSON)
```

During reconcile:
```go
existing := get(resource)
if existing.Annotations["spec-hash"] == desired.Annotations["spec-hash"] {
    return // skip update — nothing changed
}
// proceed with update
```

This prevents unnecessary rolling restarts when the DSE CR is
re-applied with no changes.

### Dependency registry

The `dependencyDefaults` map provides default configurations for each
supported dependency type:

```go
var dependencyDefaults = map[string]DependencyDefault{
    "postgres":      {Image: "postgres:15", Port: 5432, EnvVar: "DATABASE_URL", ...},
    "redis":         {Image: "redis:7", Port: 6379, EnvVar: "REDIS_URL", ...},
    "mysql":         {Image: "mysql:8", Port: 3306, EnvVar: "DATABASE_URL", ...},
    "mongodb":       {Image: "mongo:7", Port: 27017, EnvVar: "MONGO_URL", ...},
    "rabbitmq":      {Image: "rabbitmq:3-management", Port: 5672, EnvVar: "AMQP_URL", ...},
    "minio":         {Image: "minio/minio:latest", Port: 9000, EnvVar: "S3_ENDPOINT", ...},
    "elasticsearch": {Image: "elasticsearch:8.12.0", Port: 9200, EnvVar: "ELASTICSEARCH_URL", ...},
    "kafka":         {Image: "bitnami/kafka:latest", Port: 9092, EnvVar: "KAFKA_BROKER_URL", ...},
    "nats":          {Image: "nats:latest", Port: 4222, EnvVar: "NATS_URL", ...},
    "memcached":     {Image: "memcached:latest", Port: 11211, EnvVar: "MEMCACHED_URL", ...},
    "dynamodb":      {Image: "amazon/dynamodb-local:latest", Port: 8000, ...},
    "cassandra":     {Image: "cassandra:latest", Port: 9042, ...},
    "etcd":          {Image: "bitnami/etcd:latest", Port: 2379, ...},
    "consul":        {Image: "consul:latest", Port: 8500, ...},
    "vault":         {Image: "vault:latest", Port: 8200, ...},
}
```

Each entry defines:
- Default container image and port
- Auto-injected environment variable name
- Connection URL format string
- Default credentials (user/password)
- Extra environment variables for the dependency container

### Connection URL construction

`buildConnectionURL(depName, depType, svcName string)` constructs the
full connection string using the dependency type's URL template:

| Type | URL format |
|---|---|
| postgres | `postgresql://user:password@<svc>:5432/devdb?sslmode=disable` |
| redis | `redis://<svc>:6379/0` |
| mysql | `mysql://user:password@tcp(<svc>:3306)/devdb` |
| mongodb | `mongodb://user:password@<svc>:27017/devdb` |
| rabbitmq | `amqp://user:password@<svc>:5672/` |
| minio | `http://<svc>:9000` |
| elasticsearch | `http://<svc>:9200` |
| kafka | `<svc>:9092` |
| nats | `nats://<svc>:4222` |
| memcached | `<svc>:11211` |

Where `<svc>` is `<dse-name>-<dep-name>`.

### Init containers (dependency readiness)

For each dependency, the operator adds a busybox init container to the
app Deployment:

```yaml
- name: wait-for-<dep-name>
  image: busybox:1.36
  command:
    - sh
    - -c
    - |
      until nc -z <svc-name> <port>; do
        echo "Waiting for <dep-name>..."
        sleep 2
      done
```

This blocks the app container from starting until all dependencies
accept TCP connections, preventing connection-refused errors during
startup.

### Orphan pruning

When a dependency is removed from the CR spec:

```go
func (r *Reconciler) pruneOrphanDependencies(ctx, dse) {
    // List all Deployments with our ownership labels
    list := listDeployments(labels: {
        "app.kubernetes.io/part-of": dse.Name,
        "app.kubernetes.io/managed-by": "kindling",
    })

    // Build set of current dependency names
    currentDeps := set(dse.Spec.Dependencies[].Name)

    // Delete any that aren't in the current spec
    for _, dep := range list.Items {
        component := dep.Labels["app.kubernetes.io/component"]
        if !currentDeps.Contains(component) {
            delete(dep)
            // Also delete the associated Service
        }
    }
}
```

### Labels

All child resources get a standard label set:

```yaml
app.kubernetes.io/name: <service-or-dep-name>
app.kubernetes.io/part-of: <dse-name>
app.kubernetes.io/managed-by: kindling
app.kubernetes.io/component: <dep-type or "app">
```

### Event recording

The operator records K8s Events on the CR for significant actions:
- `Normal` / `Reconciled` — successful reconciliation
- `Normal` / `DependencyProvisioned` — dependency created/updated
- `Warning` / `ReconcileError` — reconciliation failure
- `Normal` / `OrphanPruned` — orphan dependency deleted

### RBAC markers

The controller uses `kubebuilder:rbac` markers for code-generated
ClusterRole:

```go
//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps.example.com,resources=devstagingenvironments/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
```

---

## CIRunnerPool Reconciler

Source: `internal/controller/cirunnerpool_controller.go`

### Reconciliation flow

```
Reconcile() called
  │
  ├─ 1. Fetch CIRunnerPool CR
  │
  ├─ 2. Resolve provider adapter (GitHub or GitLab)
  │     └─ pkg/ci.GetProvider(spec.Provider)
  │
  ├─ 3. Reconcile ServiceAccount
  │     └─ <pool-name>-runner in default namespace
  │
  ├─ 4. Reconcile ClusterRole
  │     └─ pods, deployments, dses, ingresses, secrets, events
  │
  ├─ 5. Reconcile ClusterRoleBinding
  │     └─ binds SA → ClusterRole
  │
  └─ 6. Reconcile Deployment
        ├─ Runner container (image from adapter)
        │   ├─ RUNNER_TOKEN from secretKeyRef
        │   ├─ RUNNER_REPOSITORY, RUNNER_LABELS, etc.
        │   └─ Registration script from adapter
        │
        └─ build-agent sidecar (bitnami/kubectl)
            ├─ Shared /builds volume
            └─ Signal-file watcher loop
```

### Provider abstraction (`pkg/ci`)

```go
type RunnerAdapter interface {
    RunnerImage() string
    WorkDir() string
    TokenKey() string
    EnvVars(spec) []corev1.EnvVar
    RegistrationScript(spec) string
    Labels(spec) string
}
```

Implementations:
- `GitHubRunnerAdapter` — `ghcr.io/actions/actions-runner`, token key
  `RUNNER_TOKEN`, label format `kindling,<extra>`
- `GitLabRunnerAdapter` — `gitlab/gitlab-runner`, token key
  `REGISTRATION_TOKEN`, label format `kindling,<extra>`

The `BaseRunnerAdapter` provides DNS-safe name sanitization shared
across providers.

---

## Watches and ownership

### DSE controller watches

```go
// Primary watch — our CRD
For(&appsv1alpha1.DevStagingEnvironment{})

// Secondary watches — child resources we create
Owns(&appsv1.Deployment{})
Owns(&corev1.Service{})
Owns(&networkingv1.Ingress{})
Owns(&corev1.Secret{})
```

When a child resource changes (e.g., a Deployment's status updates
after pod scheduling), the framework maps it back to the owning DSE
CR and triggers reconciliation.

### CIRunnerPool controller watches

```go
For(&appsv1alpha1.CIRunnerPool{})
Owns(&appsv1.Deployment{})
Owns(&corev1.ServiceAccount{})
Owns(&rbacv1.ClusterRole{})
Owns(&rbacv1.ClusterRoleBinding{})
```
