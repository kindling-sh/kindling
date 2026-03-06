# Architecture — Internal Reference

This document describes kindling's system design from the inside. It's
written for maintainers who need to understand *why* things work the way
they do, not just *what* they do.

---

## System overview

kindling implements a **dev-in-CI** workflow — two nested development
loops running on a local [Kind](https://kind.sigs.k8s.io) cluster. The
**outer loop** provides full CI/CD (push → build → deploy), and the
**inner loop** provides live sync with sub-second feedback.

```
┌─────────────────────────────────────────────────────────────┐
│  Developer Laptop                                           │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  Kind Cluster ("dev")                                 │  │
│  │                                                       │  │
│  │  ┌──────────────────┐  ┌────────────────────────────┐ │  │
│  │  │ kindling-system  │  │ default namespace          │ │  │
│  │  │                  │  │                            │ │  │
│  │  │  Operator        │  │  Runner Pod                │ │  │
│  │  │  (controller-    │  │  ├─ runner container       │ │  │
│  │  │   manager)       │  │  └─ build-agent sidecar    │ │  │
│  │  └────────┬─────────┘  │      (Kaniko + kubectl)    │ │  │
│  │           │            │                            │ │  │
│  │           │ reconciles │  Registry (registry:5000)  │ │  │
│  │           ▼            │                            │ │  │
│  │  ┌──────────────────┐  │  DSE "my-app"              │ │  │
│  │  │ DSE CR applied   │──│  ├─ Deployment             │ │  │
│  │  │ by runner or CLI │  │  ├─ Service                │ │  │
│  │  └──────────────────┘  │  ├─ Ingress                │ │  │
│  │                        │  ├─ postgres Deployment    │ │  │
│  │                        │  └─ redis Deployment       │ │  │
│  │                        └────────────────────────────┘ │  │
│  │                                                       │  │
│  │  Traefik ingress controller                           │  │
│  │  (localhost:80/443 → *.localhost)                     │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  CLI: kindling push / sync / load / status / expose         │
│  Dashboard: localhost:9090                                  │
└─────────────────────────────────────────────────────────────┘
```

---

## Resource footprint

kindling is designed to add minimal overhead to the Kind cluster. The
operator and infrastructure pods have a fixed, constant footprint
regardless of how many services you deploy.

### kindling infrastructure (constant)

| Pod | CPU request | Memory request | Purpose |
|---|---|---|---|
| controller-manager | 15m | 128 Mi | Operator + kube-rbac-proxy |
| registry | ~5m | ~30 Mi | In-cluster image registry |
| Traefik | ~10m | ~90 Mi | Localhost HTTP routing |
| **Total** | **~30m** | **~250 Mi** | |

### Per-service overhead

Each `DevStagingEnvironment` creates:
- 1 Deployment + 1 Service + 1 Ingress for the application
- 1 Deployment + 1 Service per dependency
- No sidecars, no DaemonSets, no per-service operators

The operator manages all services through a single reconciliation loop.
There is no per-service control plane cost.

### CLI binary

The `kindling` binary is a 14 MB statically-linked Go executable. It
has zero runtime dependencies — no Python, no Node.js, no JVM. It
communicates with the cluster via `kubectl` and with CI via the
respective platform's API.

---

## The two loops

### Outer loop — CI on your laptop

```
git push → CI platform dispatches job → self-hosted runner pod picks it up
→ Kaniko builds image → pushes to registry:5000 → applies DSE CR
→ operator reconciles Deployment + Service + Ingress + dependencies
```

The outer loop uses **GitHub Actions** or **GitLab CI** as the trigger
mechanism, but all compute runs locally. Key design decisions:

1. **Kaniko over Docker** — no Docker daemon required inside the
   cluster. Kaniko runs as a sidecar to the runner pod. This avoids
   DinD security issues and Docker socket mounting.

2. **In-cluster registry** — images are pushed to `registry:5000`
   (accessible from the host as `localhost:5001`). No DockerHub or
   ECR credentials needed.

3. **Signal-file protocol** — the runner communicates with the Kaniko
   sidecar via files in a shared `/builds` volume:
   - Runner writes `<name>.tar.gz` (build context) + `<name>.dest`
     (target image) + `<name>.request` (trigger)
   - Sidecar detects `.request`, runs Kaniko, writes `.exitcode` + `.done`
   - Runner polls for `.done`, reads `.exitcode`

4. **DSE CR as deployment unit** — the CI workflow applies a
   `DevStagingEnvironment` CR. The operator handles all the K8s
   resource creation. This keeps CI workflows simple and declarative.

### Inner loop — live sync + hot reload

```
edit file locally → fsnotify detects change → kubectl cp into pod
→ runtime-specific restart (signal, wrapper loop, or none)
```

The inner loop (`kindling sync`) bypasses CI entirely for rapid
iteration. Key design decisions:

1. **Runtime auto-detection** — reads `/proc/1/cmdline` from the
   container to identify the runtime (node, python, uvicorn, etc.)
   and selects the appropriate restart strategy.

2. **Four restart modes:**
   - `ModeWrapper` — wraps the process in a `while true; do CMD & PID=$!; wait; done` loop, kills child on sync
   - `ModeSignal` — sends SIGHUP/SIGUSR2 to PID 1 (uvicorn, gunicorn, nginx, puma)
   - `ModeNone` — no restart needed (PHP, nodemon with built-in watch)
   - `ModeCompiled` — cross-compiles locally, syncs binary, restarts

3. **Debounced batching** — file changes within a configurable window
   (default 500ms) are batched into a single sync operation.

4. **Frontend detection** — for React/Vue/Angular projects, sync
   automatically runs `npm run build` locally and syncs the output
   directory to the nginx container's webroot.

---

## Cluster topology

### What `kindling init` creates

1. **Kind cluster** — single control-plane node with ingress-ready
   label and port mappings (80/443). Uses `kind-config.yaml` at the
   repo root.

2. **In-cluster registry** — `registry:5000` Deployment + Service in
   the default namespace. containerd is configured via `hosts.toml`
   to mirror `registry:5000` to `localhost:5000`.

3. **Traefik** — Traefik v3.6 ingress controller, deployed as a
   DaemonSet with hostNetwork in the `traefik` namespace. Routes
   `*.localhost` → Services via Ingress rules.

4. **kindling operator** — `kindling-controller-manager` Deployment in
   `kindling-system` namespace. Watches `DevStagingEnvironment` and
   `CIRunnerPool` CRDs.

### Namespaces

| Namespace | Contents |
|---|---|
| `kindling-system` | Operator Deployment, kube-rbac-proxy sidecar |
| `default` | Registry, runner pods, DSE workloads, dependencies |
| `traefik` | Traefik ingress controller pods |

### Networking

- **Host → Cluster:** `localhost:80` and `localhost:443` map to the
  Kind node's ports, which route through Traefik.
- **Cluster internal:** Services use ClusterIP. Dependencies are
  addressed by DNS name (e.g., `myapp-postgres:5432`).
- **Registry:** In-cluster `registry:5000`, host-accessible as
  `localhost:5001`. containerd mirror handles the translation.
- **Tunnels:** `kindling expose` creates a cloudflared/ngrok tunnel
  from the public internet to `localhost:80`. The command patches
  Ingress hosts to match the tunnel hostname.

---

## Operator design

The operator follows the standard Kubernetes operator pattern using
`controller-runtime`. Two reconcilers:

### DevStagingEnvironmentReconciler

Watches `DevStagingEnvironment` CRs and reconciles into:
- 1 Deployment (app container + init containers for dep readiness)
- 1 Service (ClusterIP by default)
- 1 Ingress (optional, only if `spec.ingress.enabled`)
- N dependency bundles (Secret + Deployment + Service per dependency)

**Change detection:** Every child resource is annotated with a SHA-256
hash of the relevant spec section (`apps.example.com/spec-hash`). On
reconcile, the operator compares the desired hash against the existing
annotation. If they match, the update is skipped entirely. This prevents
unnecessary rolling restarts and reconcile loops.

**Dependency ordering:** Dependencies are reconciled *before* the app
Deployment. Each dependency gets a busybox init container in the app
pod that blocks until the dependency Service accepts TCP connections.

**Status updates:** After reconciling all child resources, the operator
fetches their current state and updates the CR status:
- `deploymentReady` — Deployment has desired replicas available
- `serviceReady` — Service exists
- `ingressReady` — Ingress exists (if enabled)
- `dependenciesReady` — all dependency Deployments have ≥1 available replica
- `conditions` — standard K8s conditions with `Ready` aggregate

**Orphan pruning:** When a dependency is removed from the CR spec,
the operator lists all child Deployments labeled `part-of: <cr-name>`
and deletes any whose `component` label doesn't match a current dep.

**OwnerReferences:** All child resources have `controllerReference` set
to the CR, so K8s garbage collection handles cleanup when the CR is
deleted.

### CIRunnerPoolReconciler

Watches `CIRunnerPool` CRs and reconciles into:
- 1 ServiceAccount
- 1 ClusterRole (pods, deployments, DSEs, ingresses, secrets, events)
- 1 ClusterRoleBinding
- 1 Deployment (runner container + build-agent sidecar)

**Provider abstraction:** The reconciler delegates to a `RunnerAdapter`
interface (in `pkg/ci`) that handles GitHub vs GitLab differences:
- Runner image, work directory, token key
- Environment variables and registration script
- Label format

**Build-agent sidecar:** A `bitnami/kubectl` container that watches
the `/builds` shared volume for signal files. It:
- Creates Kaniko pods for `.request` files
- Runs `kubectl apply` for `.apply` files
- Executes arbitrary scripts for `.kubectl` files

---

## CLI architecture

### Module structure

The CLI is a separate Go module (`github.com/jeffvincent/kindling/cli`)
with a `replace` directive pointing to `../pkg/ci` for the shared CI
provider package.

```
cli/
├── main.go          ← entry point
├── cmd/             ← all cobra commands
│   ├── root.go      ← root command, global flags, intel PersistentPreRun
│   ├── init.go      ← cluster bootstrap
│   ├── runners.go   ← CI runner pool creation
│   ├── analyze.go   ← project readiness checks
│   ├── generate.go  ← AI workflow generation
│   ├── deploy.go    ← DSE YAML apply
│   ├── push.go      ← git push with selective rebuild
│   ├── sync.go      ← live file sync (1796 lines — the biggest file)
│   ├── load.go      ← build + load + deploy inner loop
│   ├── expose.go    ← public HTTPS tunnel
│   ├── env.go       ← env var management
│   ├── secrets.go   ← secret CRUD + local persistence
│   ├── status.go    ← cluster overview
│   ├── logs.go      ← controller log streaming
│   ├── intel.go     ← agent context management
│   ├── destroy.go   ← cluster teardown
│   ├── reset.go     ← runner pool cleanup
│   ├── version.go   ← version display
│   ├── helpers.go   ← shared utilities, colors, exec helpers
│   ├── dashboard.go ← dashboard server + mux
│   ├── dashboard_api.go    ← read-only API handlers
│   ├── dashboard_actions.go ← mutation API handlers + topology
│   └── dashboard-ui/       ← React + Vite frontend
└── core/            ← business logic (no user-facing output)
    ├── kubectl.go   ← low-level kubectl wrappers
    ├── secrets.go   ← K8s secret CRUD
    ├── runners.go   ← runner pool CRUD
    ├── tunnel.go    ← tunnel process management
    ├── env.go       ← deployment env var management
    └── load.go      ← build + load + deploy pipeline
```

### cmd/core separation

The `cmd` package handles user-facing concerns: colored output, emoji,
interactive prompts, progress messages, error formatting.

The `core` package handles business logic: kubectl invocations,
resource creation, process management. Functions return structured
results and errors — they never print to stdout/stderr.

This separation means core functions are testable without stdout
capture, and the dashboard (which also uses core functions) doesn't
inherit CLI formatting.

### Global hooks

`rootCmd.PersistentPreRun` calls `autoIntel()` — the automatic agent
context lifecycle manager. On every CLI command (except `intel`,
`version`, `help`, `completion`), it:
1. Skips if intel was explicitly disabled
2. Restores if stale (>1 hour since last interaction)
3. Activates if not active
4. Touches the interaction timestamp

### Project directory resolution

`resolveProjectDir()` uses a 3-tier fallback:
1. `--project-dir` flag if set
2. Current working directory if it contains `kind-config.yaml`
3. Auto-clone to `~/.kindling` from the GitHub repo

This allows the CLI to work outside the kindling checkout.

---

## Build protocol

### Kaniko builds (CI outer loop)

```
Source tarball → /builds/<name>.tar.gz
Target image  → /builds/<name>.dest
Trigger       → /builds/<name>.request (touch)
Wait           → poll for /builds/<name>.done
Result        → /builds/<name>.exitcode (0 = success)
```

The build-agent sidecar creates a Kaniko pod with:
- `--context=tar:///workspace/<name>.tar.gz`
- `--destination=<image>` (from .dest file)
- `--cache=true --cache-repo=registry:5000/cache`
- `--insecure` (in-cluster registry has no TLS)
- `--push-retry=3`

**Kaniko compatibility constraints:**
- No BuildKit platform ARGs (`TARGETARCH`, `BUILDPLATFORM`)
- No `.git` directory — Go builds need `-buildvcs=false`
- Poetry needs `--no-root` flag
- npm needs `ENV npm_config_cache=/tmp/.npm`
- `RUN --mount=type=cache` is silently ignored (no caching benefit)

### Docker builds (CLI inner loop)

`kindling load` uses `docker build` locally (not Kaniko), then
`kind load docker-image` to transfer into the cluster. The image tag
uses a Unix timestamp for cache-busting:

```
localhost:5001/<service>:<unix-timestamp>
```

After loading, it patches the DSE CR (or falls back to patching the
Deployment directly) with the new image tag.

---

## Design decisions and rationale

### Why Kind over Minikube/k3s/Docker Compose?

- **Full Kubernetes API** — CRDs, RBAC, Ingress, PVCs work identically
  to production clusters
- **Multi-node capable** — can simulate realistic topologies (though
  we default to single-node for speed)
- **Traefik** — `*.localhost` routing works out of the box
- **Registry support** — containerd mirror configuration is clean
- **Docker Desktop integration** — resource limits via Docker Desktop
  settings, no separate VM management

### Why a Kubernetes operator over plain kubectl scripts?

- **Declarative reconciliation** — apply a DSE CR and the operator
  handles all resource creation, updates, and cleanup
- **Status tracking** — CR status reflects actual cluster state
- **Dependency management** — operator provisions databases, caches,
  queues with correct connection strings
- **Garbage collection** — OwnerReferences ensure cleanup on CR deletion
- **Idempotency** — spec-hash annotations prevent unnecessary updates

### Why Kaniko over Docker-in-Docker?

- **No privileged containers** — Kaniko runs as a regular pod
- **No Docker socket** — no security risks from DinD
- **Layer caching** — Kaniko's `--cache` flag stores layers in the
  in-cluster registry
- **Deterministic builds** — no Docker daemon state to worry about

### Why separate Go modules for CLI and operator?

- **Dependency isolation** — the CLI doesn't need controller-runtime,
  the operator doesn't need cobra/fsnotify
- **Build speed** — `make cli` is fast; `make build` (operator) pulls
  heavier K8s dependencies
- **Shared package** — `pkg/ci` is the only shared dependency, used
  by both the operator's runner controller and the CLI's generate/runners
  commands

### Why auto-inject connection strings?

- **Zero config for common patterns** — declaring `type: postgres`
  gives you `DATABASE_URL` with zero additional configuration
- **Convention over configuration** — every dependency type has a
  well-known env var name, port, and credential set
- **Overridable** — `envVarName`, `port`, `env`, `image` fields let
  you customize everything
- **Init container readiness** — busybox TCP probes prevent app
  crashes during dependency startup
