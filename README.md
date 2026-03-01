<p align="center">
  <img src="https://img.shields.io/badge/Kubernetes-Operator-326CE5?logo=kubernetes&logoColor=white" alt="Kubernetes Operator" />
  <img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" alt="Go 1.25" />
  <img src="https://img.shields.io/badge/kubebuilder-v4-blueviolet" alt="Kubebuilder v4" />
  <img src="https://img.shields.io/badge/License-Apache%202.0-blue" alt="Apache 2.0" />
</p>

<div align="center">

# <img src="assets/logo.svg" width="42" height="42" alt="kindling" style="vertical-align: middle;" /> kindling

**Dev on your laptop. Deploy to production. One tool.**

[![Docs](https://img.shields.io/badge/📖_Documentation-kindling--sh.github.io-FF6B35?style=for-the-badge)](https://kindling-sh.github.io/kindling/)
[![GitHub Release](https://img.shields.io/github/v/release/kindling-sh/kindling?style=for-the-badge&logo=github&label=Latest)](https://github.com/kindling-sh/kindling/releases/latest)
[![Install](https://img.shields.io/badge/brew_install-kindling-FBB040?style=for-the-badge&logo=homebrew&logoColor=white)](https://github.com/kindling-sh/homebrew-tap)

### Supported CI Platforms

<a href="#cirunnepool"><img src="https://img.shields.io/badge/GitHub_Actions-2088FF?style=for-the-badge&logo=githubactions&logoColor=white" alt="GitHub Actions" /></a>&nbsp;&nbsp;<a href="#cirunnerpool"><img src="https://img.shields.io/badge/GitLab_CI-FC6D26?style=for-the-badge&logo=gitlab&logoColor=white" alt="GitLab CI" /></a>

</div>

---

## The Journey

kindling is a complete development lifecycle tool. It takes your project from first commit to production deployment — whether you're starting fresh or bringing an existing app.

```
  Have a project?                              Starting fresh?
       │                                            │
       ▼                                            ▼
  kindling analyze                          kindling scaffold (coming soon)
  (check readiness)                         (opinionated project structure)
       │                                            │
       ▼                                            ▼
  kindling generate ◄───────────────────────────────┘
  (AI-writes your CI workflow)
       │
       ▼
  ┌─────────────────── Dev Loop ───────────────────┐
  │                                                 │
  │  push → build → deploy (outer loop)            │
  │       ↕                                         │
  │  edit → sync → reload (inner loop)             │
  │       ↕                                         │
  │  expose → test OAuth / webhooks                │
  │       ↕                                         │
  │  add services → debug → iterate                │
  │                                                 │
  └─────────────────────────────────────────────────┘
       │
       ▼
  kindling promote (coming soon)
  (graduate to production with TLS)
```

Zero cloud CI minutes. Sub-second iteration. Full Kubernetes fidelity.

### Lightweight by Design

kindling adds almost nothing to your machine. The entire operator is a **14 MB static binary** that runs as a single pod requesting **15m CPU and 128 MB RAM**. Everything else in the cluster is *your* application. A single-service setup runs comfortably under 1 GB total.

| Component | Footprint |
|---|---|
| CLI binary | 14 MB, single static Go binary |
| Operator pod | 15m CPU / 128 Mi RAM (2 containers) |
| In-cluster registry | ~30 Mi RAM |
| Ingress controller | ~90 Mi RAM |
| **kindling total** | **< 250 Mi RAM** — everything else is your app |

---

## 1. Analyze — Check Your Project's Readiness

Before generating a workflow, `kindling analyze` scans your repo and tells you exactly what's ready and what needs attention:

```bash
kindling analyze
```

```
  kindling analyze — /path/to/your-app

  ✅ Git repository initialized
  ✅ Has commits
  ✅ Remote: https://github.com/you/your-app.git
  ✅ Found 2 Dockerfile(s)
  ✅ Found 3 dependency manifest(s)
  ℹ️  Primary language: Python
  ℹ️  Multi-agent architecture detected
  ℹ️  Agent frameworks: LangChain
  ✅ Secret OPENAI_API_KEY exists in cluster
  ✅ Kind cluster 'dev' is running
  ✅ Ready for 'kindling generate'
```

Analyze checks: git state, Dockerfiles, Kaniko compatibility, build context paths, dependencies, agent architecture (LangChain, CrewAI, AutoGen, etc.), secrets, project structure, and cluster health.

---

## 2. Generate — AI-Writes Your CI Workflow

Point `kindling generate` at your repo. It scans everything — Dockerfiles, docker-compose, Helm charts, source code — and uses AI to produce a complete CI workflow:

```bash
kindling generate -k <api-key> -r /path/to/your-app
```

It detects services, languages, dependencies, ports, health checks, external credentials, OAuth patterns, agent frameworks, MCP servers, worker processes, and inter-service calls. Supports OpenAI (`o3`) and Anthropic (`claude-sonnet`).

The output is `.github/workflows/dev-deploy.yml` (or `.gitlab-ci.yml`).

---

## 3. Dev Loop — Build, Sync, Iterate

The dev loop has two gears: the **outer loop** (CI-driven) and the **inner loop** (sub-second live sync).

### Outer Loop — Push and deploy

Every `git push` triggers a real CI pipeline on your laptop. A self-hosted runner builds containers via Kaniko, pushes to an in-cluster registry, and the kindling operator deploys a complete staging environment.

```bash
kindling push -s api,worker              # push + selective rebuild
kindling status                          # see what's running
```

### Inner Loop — Edit and see instantly

Once deployed, skip CI entirely. Edit a file, sync it into the running container, see the result in under a second:

```bash
kindling sync -d my-api --restart        # watch + auto-restart
```

The runtime is auto-detected from the container's process:

| Strategy | Runtimes | What happens |
|---|---|---|
| **Signal reload** | uvicorn, gunicorn, Puma, Nginx | SIGHUP for zero-downtime reload |
| **Wrapper + kill** | Node.js, Python, Ruby, Deno, Bun | Restart-loop wrapper respawns process |
| **Local build + sync** | Go, Rust, Java, C# | Cross-compiles locally, syncs binary |
| **Frontend build** | React, Vue, Angular, Svelte | Builds locally, syncs into nginx |
| **Auto-reload** | PHP, nodemon | Just syncs — runtime picks them up |

When you stop syncing, the deployment **automatically rolls back** to its original state.

### Dashboard — Visual Control Plane

```bash
kindling dashboard                       # open at localhost:9090
```

View environments, pods, logs, events. Click **Sync** or **Load** on any service. The topology map shows your full service graph.

### Expose — Public HTTPS for OAuth & Webhooks

```bash
kindling expose                          # HTTPS tunnel, one command
```

### Add Services, Debug, Iterate

```bash
kindling env set my-api LOG_LEVEL=debug  # change env vars live
kindling secrets set STRIPE_KEY sk_...   # manage credentials
kindling push -s new-service             # add and deploy a new service
```

---

## 4. Graduate to Production *(coming soon)*

Once your app works in the dev loop, promote it to a real cluster:

```bash
kindling promote                         # dev → production
```

Scans your dev cluster, pushes images to your production registry, deploys with TLS certificates, and gives you live URLs. No YAML surgery — kindling generates production-ready manifests from your running dev environment.

---

## Quick Start

```bash
# Install
brew install kindling-sh/tap/kindling

# Bootstrap local cluster
kindling init

# Register a CI runner (GitHub or GitLab)
kindling runners -u <user> -r <owner/repo> -t <pat>

# Check your project
kindling analyze

# AI-generate a CI workflow
kindling generate -k <api-key> -r /path/to/app

# Push → build → deploy
git push origin main

# Open the dashboard
kindling dashboard

# Start live sync on a service
kindling sync -d <user>-my-app --restart
```

→ [Full Getting Started Guide](docs/getting-started.md)

---

## Agent Intel — Context for AI Coding Agents

`kindling intel` auto-configures GitHub Copilot, Claude Code, Cursor, and Windsurf with full project context — CLI commands, dependency injection, build protocol, secrets flow, and Kaniko compatibility. Activates on any `kindling` command, restores originals after an hour of inactivity.

```bash
kindling intel on                        # activate now
kindling intel status                    # check what's active
kindling intel off                       # restore originals
```

---

## Dependencies — Auto-Provisioned

Declare dependencies in your workflow. The operator provisions them and injects connection URLs:

| Dependency | Injected env var |
|---|---|
| postgres | `DATABASE_URL` |
| redis | `REDIS_URL` |
| mysql | `DATABASE_URL` |
| mongodb | `MONGO_URL` |
| rabbitmq | `AMQP_URL` |
| kafka | `KAFKA_BROKER_URL` |
| elasticsearch | `ELASTICSEARCH_URL` |
| minio | `S3_ENDPOINT` |
| nats | `NATS_URL` |
| memcached | `MEMCACHED_URL` |
| cassandra | `CASSANDRA_URL` |
| consul | `CONSUL_HTTP_ADDR` |
| vault | `VAULT_ADDR` |
| influxdb | `INFLUXDB_URL` |
| jaeger | `JAEGER_ENDPOINT` |

→ [Dependency Reference](docs/dependencies.md)

---

## Secrets Management

```bash
kindling secrets set STRIPE_KEY sk_live_abc123     # store
kindling secrets list                               # list
kindling secrets restore                            # restore after cluster rebuild
```

Secrets are stored as Kubernetes Secrets with automatic local backup. They survive cluster rebuilds.

→ [docs/secrets.md](docs/secrets.md)

---

## Custom Resources

The operator manages two CRDs in the `apps.example.com/v1alpha1` group:

### `CIRunnerPool`

Declares a self-hosted CI runner pool. Supports GitHub Actions and GitLab CI.

```yaml
apiVersion: apps.example.com/v1alpha1
kind: CIRunnerPool
metadata:
  name: jeff-runner-pool
spec:
  ciProvider: github
  githubUsername: "jeff-vincent"
  repository: "jeff-vincent/demo-kindling"
  tokenSecretRef:
    name: github-runner-token
  replicas: 1
```

### `DevStagingEnvironment`

Declares a complete staging environment: Deployment, Service, Ingress, and dependencies.

```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: jeff-dev
spec:
  deployment:
    image: registry:5000/myapp:jeff-abc123
    replicas: 1
    port: 8080
    healthCheck:
      path: /healthz
  service:
    port: 8080
  ingress:
    enabled: true
    host: jeff-dev.localhost
    ingressClassName: nginx
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
```

→ [CRD Reference](docs/crd-reference.md)

---

## Reusable CI Actions

### `kindling-build`

Builds a container image via the Kaniko sidecar:

```yaml
- uses: kindling-sh/kindling/.github/actions/kindling-build@main
  with:
    name: my-app
    context: ${{ github.workspace }}
    image: "registry:5000/my-app:${{ env.TAG }}"
```

### `kindling-deploy`

Generates and applies a DevStagingEnvironment CR:

```yaml
- uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-app"
    image: "registry:5000/my-app:${{ env.TAG }}"
    port: "8080"
    ingress-host: "${{ github.actor }}-my-app.localhost"
    dependencies: |
      - type: postgres
        version: "16"
```

→ [GitHub Actions Reference](docs/github-actions.md)

---

## Example Apps

### 🟢 sample-app — Single service
A Go web server with Postgres + Redis.
→ [examples/sample-app/](examples/sample-app/)

### 🔵 microservices — Four services + queue
Orders, Inventory, Gateway, and a React UI with Postgres, MongoDB, and Redis.
→ [examples/microservices/](examples/microservices/)

### 🟣 platform-api — Five dependencies + dashboard
Go API + React dashboard with Postgres, Redis, Elasticsearch, Kafka, and Vault.
→ [examples/platform-api/](examples/platform-api/)

---

## Installation

### Homebrew (recommended)

```bash
brew install kindling-sh/tap/kindling
```

### Pre-built binaries

Download from [GitHub Releases](https://github.com/kindling-sh/kindling/releases).

> **macOS Gatekeeper note:** If you see *"Apple could not verify kindling is free of malware"*, run:
> ```bash
> sudo xattr -d com.apple.quarantine /usr/local/bin/kindling
> ```

### Build from source

```bash
git clone https://github.com/kindling-sh/kindling.git
cd kindling && make cli
sudo mv bin/kindling /usr/local/bin/
```

### Prerequisites

| Tool | Version |
|---|---|
| [Docker](https://docs.docker.com/get-docker/) | 24+ |
| [Kind](https://kind.sigs.k8s.io/) | 0.20+ |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.28+ |

### Recommended Docker Desktop resources

| Workload | CPUs | Memory | Disk | kindling overhead |
|---|---|---|---|---|
| Minimal (1 service, no deps) | 2 | 4 GB | 20 GB | < 250 Mi |
| Small (1–3 services) | 4 | 8 GB | 30 GB | < 250 Mi |
| Medium (4–6 services) | 6 | 12 GB | 50 GB | < 250 Mi |
| Large (7+ services) | 8+ | 16 GB | 80 GB | < 250 Mi |

> kindling's own footprint stays constant regardless of workload size.
> The memory growth comes from your services and dependencies.

---

## CLI Reference

| Command | Description |
|---|---|
| **Setup** | |
| `kindling init` | Bootstrap Kind cluster + operator + registry + ingress |
| `kindling runners` | Register a CI runner (GitHub Actions or GitLab CI) |
| `kindling intel` | Auto-configure coding agents with project context |
| **Onboarding** | |
| `kindling analyze` | Check project readiness — git, Dockerfiles, secrets, cluster |
| `kindling generate` | AI-generate a CI workflow |
| `kindling scaffold` | *(coming soon)* Generate opinionated project structure |
| **Dev Loop** | |
| `kindling push` | Git push with selective service rebuild |
| `kindling sync` | Live-sync files + hot reload |
| `kindling dashboard` | Web dashboard with topology map |
| `kindling deploy` | Apply a DevStagingEnvironment from YAML |
| `kindling load` | Build + load image without CI |
| `kindling expose` | Public HTTPS tunnel for OAuth/webhooks |
| **Operations** | |
| `kindling status` | Cluster and environment status |
| `kindling logs` | Tail operator logs |
| `kindling secrets` | Manage external credentials |
| `kindling env` | Set/list/unset env vars on deployments |
| `kindling snapshot` | Export Helm chart or Kustomize overlay from cluster state |
| **Lifecycle** | |
| `kindling promote` | *(coming soon)* Graduate to production with TLS |
| `kindling reset` | Remove runner pool (keep cluster) |
| `kindling destroy` | Tear down the cluster |

→ [Full CLI Reference](docs/cli.md)

---

## Roadmap

- [x] GitHub Actions + GitLab CI runners on localhost
- [x] 15 auto-provisioned dependency types
- [x] Kaniko container builds (no Docker daemon)
- [x] AI workflow generation with agent architecture awareness
- [x] `kindling analyze` — deterministic project readiness checking
- [x] `kindling sync` — live file sync with 30+ language-aware restart strategies
- [x] `kindling dashboard` — web UI with topology map, sync/load, runtime detection
- [x] `kindling snapshot` — export Helm charts or Kustomize overlays from running cluster
- [x] `kindling intel` — auto-configure coding agents with project context
- [x] Secrets management with local backup across cluster rebuilds
- [x] Public HTTPS tunnels for OAuth
- [ ] `kindling scaffold` — opinionated project structure with service templates
- [ ] `kindling promote` — graduate to production with TLS and DNS
- [ ] Topology map: drag-and-drop service/dependency editor
- [ ] Interactive service health resolution in dashboard

---

## Contributing

Contributions welcome! Please open an issue to discuss your idea before submitting a PR.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Run `make test`
4. Open a Pull Request

---

## License

Apache License 2.0 — see [LICENSE](LICENSE) for full text.

