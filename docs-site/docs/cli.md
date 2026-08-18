---
sidebar_position: 1
title: CLI Reference
description: Complete reference for all kindling CLI commands. Structured for LLM and agent consumption.
---

<!-- 
  LLM/Agent parsing notes:
  - Each command follows: COMMAND → SYNOPSIS → DESCRIPTION → FLAGS → EXAMPLES
  - Commands are grouped by phase: Setup → Develop → Staging → Lifecycle
  - All flags use consistent table format: Flag | Short | Default | Description
  - Search for "kindling <command>" to jump to any command
-->

# CLI Reference

## Command Index

| Command | Phase | Purpose |
|---|---|---|
| `kindling init` | Setup | Bootstrap Kind cluster + operator |
| `kindling runners` | Setup | Register CI runner pool (GitHub/GitLab) |
| `kindling explain` | Setup | On-demand kindling concepts/workflow guidance |
| `kindling analyze` | Setup | Check project readiness |
| `kindling generate` | Setup | AI-generate CI workflow |
| `kindling deploy` | Develop | Apply DSE from YAML file |
| `kindling push` | Develop | Rebuild + redeploy one service via CI |
| `kindling sync` | Develop | Live-sync files into running pod |
| `kindling debug` | Develop | Attach VS Code debugger to deployment |
| `kindling dev` | Develop | Frontend dev server with cluster access |
| `kindling load` | Develop | Build + load image into Kind directly |
| `kindling dashboard` | Develop | Launch web dashboard |
| `kindling expose` | Develop | Public HTTPS tunnel to cluster |
| `kindling env` | Develop | Manage deployment env vars |
| `kindling secrets` | Develop | Manage external credentials |
| `kindling ingress` | Develop | Manage extra Ingress routes on a DSE |
| `kindling deps` | Develop | Manage shared dependency resources |
| `kindling status` | Develop | Cluster + environment status |
| `kindling logs` | Develop | Tail controller logs |
| `kindling snapshot` | Staging | Export Helm/Kustomize + deploy |
| `kindling staging tls` | Staging | Configure TLS with cert-manager |
| `kindling reset` | Lifecycle | Remove runner pool, keep cluster |
| `kindling destroy` | Lifecycle | Delete cluster and all resources |
| `kindling version` | — | Print CLI version |

## Installation

```bash
brew install kindling-sh/tap/kindling
```

Build from source:

```bash
cd kindling && make cli
sudo cp bin/kindling /usr/local/bin/
```

## Global Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--cluster` | `-c` | `dev` | Kind cluster name |
| `--project-dir` | `-p` | `.` | Path to kindling project root |

---

## Setup

### `kindling init`

**Synopsis:** `kindling init [flags]`

Bootstrap a Kind cluster with the kindling operator, ingress controller, and in-cluster registry.

**Steps:** preflight checks → `kind create cluster` → switch kubectl context → install Traefik ingress + registry → load operator image → apply CRDs → deploy operator → wait for rollout.

**Recommended resources:**

| Workload | CPUs | Memory | Disk |
|---|---|---|---|
| Small (1–3 services) | 4 | 8 GB | 30 GB |
| Medium (4–6 services) | 6 | 12 GB | 50 GB |
| Large (7+ services) | 8+ | 16 GB | 80 GB |

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--skip-cluster` | | `false` | Skip Kind cluster creation (use existing) |
| `--build` | | `false` | Build operator from source instead of pulling |
| `--operator-image` | | `ghcr.io/kindling-sh/kindling-operator:latest` | Operator image to pull |
| `--image` | | — | Kind node image (e.g. `kindest/node:v1.29.0`) |
| `--kubeconfig` | | — | Path to write kubeconfig |
| `--wait` | | — | Wait for control plane (e.g. `60s`, `5m`) |
| `--retain` | | `false` | Retain nodes for debugging |
| `--expose` | | `false` | Start HTTPS tunnel after bootstrap |

**Examples:**

```bash
kindling init
kindling init --build
kindling init --expose
kindling init --image kindest/node:v1.29.0
kindling init --skip-cluster
```

---

### `kindling runners`

**Synopsis:** `kindling runners [flags]`

Create a CI runner pool. Supports GitHub Actions (default) and GitLab CI.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--username` | `-u` | — | CI platform username |
| `--repo` | `-r` | — | Repository (`owner/repo` or `group/project`) |
| `--token` | `-t` | — | PAT or runner registration token |
| `--ci-provider` | | `github` | `github` or `gitlab` |

**Examples:**

```bash
kindling runners -u myuser -r myorg/myrepo -t ghp_xxxxx
kindling runners --ci-provider gitlab -u myuser -r mygroup/myproject -t glpat_xxxxx
```

---

### `kindling explain`

**Synopsis:** `kindling explain [topic]`

Prints short, current guidance on kindling concepts and workflows (e.g. debugging/hot-reload, dependency auto-injection, Kaniko build constraints, secrets flow, staging graduation) — pulled on demand instead of injected into every coding-agent session. Run with no arguments to list topics.

```bash
kindling explain
kindling explain debugging
```

---

### `kindling analyze`

**Synopsis:** `kindling analyze [flags]`

Check project readiness before generating a workflow. Detects Dockerfiles, dependencies (15 types), secrets, agent frameworks (LangChain, CrewAI, LangGraph, OpenAI Agents SDK, MCP), inter-service communication, and build context alignment.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--repo-path` | `-r` | `.` | Path to the repository |
| `--verbose` | `-v` | `false` | Show additional detail |

See [Analyze](analyze.md) for full reference.

---

### `kindling generate`

**Synopsis:** `kindling generate [flags]`

AI-generate a CI workflow for any repository. Scans for Dockerfiles, dependencies, ports, health checks, Helm charts, Kustomize overlays, docker-compose files, `.env` templates, and agent frameworks. Produces a complete GitHub Actions or GitLab CI workflow.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--api-key` | `-k` | — (required) | GenAI API key |
| `--repo-path` | `-r` | `.` | Path to the repository |
| `--ai-provider` | | `openai` | `openai` or `anthropic` |
| `--model` | | auto | Model name (default: `o3` / `claude-sonnet-4-20250514`) |
| `--output` | `-o` | auto | Output path for workflow file |
| `--dry-run` | | `false` | Print to stdout instead of writing |
| `--ingress-all` | | `false` | Wire every service with an ingress |
| `--no-helm` | | `false` | Skip Helm/Kustomize rendering |
| `--ci-provider` | | `github` | `github` or `gitlab` |
| `--shared-deps` | | — | Comma-separated dependency types (e.g. `redis,postgres`) to mark `shared: true` across all detected services |

**Examples:**

```bash
kindling generate -k sk-... -r .
kindling generate -k sk-... -r . --dry-run
kindling generate -k sk-ant-... -r . --ai-provider anthropic
kindling generate -k sk-... -r . --ci-provider gitlab
kindling generate -k sk-... -r . --ingress-all
kindling generate -k sk-... -r . --shared-deps redis,postgres
```

---

## Develop

### `kindling deploy`

**Synopsis:** `kindling deploy -f <file>`

Apply a DevStagingEnvironment from a YAML file (manual deploy).

---

### `kindling push`

**Synopsis:** `kindling push -s <service>`

Rebuild and redeploy a single service via CI. Verifies workflow secrets and workflow file exist before triggering.

---

### `kindling sync`

**Synopsis:** `kindling sync [flags]`

Live-sync local files into a running pod with language-aware hot reload. Automatic rollback on Ctrl+C.

**Restart strategies (auto-detected):**

| Strategy | Runtimes | Mechanism |
|---|---|---|
| wrapper + kill | Node.js, Python, Ruby, Perl, Lua, Julia, R, Elixir, Deno, Bun | Patch deployment, sync files, kill child to respawn |
| signal reload | uvicorn, gunicorn, Puma, Unicorn, Nginx, Apache | SIGHUP for zero-downtime reload |
| auto-reload | PHP, nodemon | Sync files, runtime re-reads automatically |
| local build + sync | Go, Rust, Java, Kotlin, C#/.NET, C/C++, Zig | Cross-compile locally, sync binary, restart |

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | — (required) | Target deployment name |
| `--src` | | `.` | Local source directory |
| `--dest` | | `/app` | Destination inside container |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--restart` | | `false` | Restart app after each sync |
| `--once` | | `false` | Sync once and exit |
| `--container` | | — | Container name (multi-container pods) |
| `--exclude` | | — | Additional exclude patterns |
| `--debounce` | | `500ms` | Debounce interval |
| `--language` | | auto | Override runtime detection |
| `--build-cmd` | | auto | Local build command for compiled languages |
| `--build-output` | | auto | Path to built artifact |

**Examples:**

```bash
kindling sync -d my-api --restart
kindling sync -d my-api --restart --once
kindling sync -d orders --src ./services/orders --restart
kindling sync -d gateway --restart --language go
kindling sync -d frontend --src ./dist --dest /usr/share/nginx/html --restart
```

---

### `kindling debug`

**Synopsis:** `kindling debug -d <deployment> [flags]`

Attach a VS Code debugger to a running deployment. Auto-detects runtime, injects debug agent, port-forwards, and writes `launch.json`.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | — (required) | Deployment name |
| `--stop` | | `false` | Stop an active debug session |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--port` | | auto | Override local debug port |
| `--no-launch` | | `false` | Skip writing launch.json |

**Supported runtimes:**

| Runtime | Debug tool | Default port |
|---|---|---|
| Python | debugpy | 5678 |
| Node.js | V8 Inspector | 9229 |
| Deno | V8 Inspector | 9229 |
| Bun | Bun Inspector | 6499 |
| Go | Delve | 2345 |
| Ruby | rdbg | 12345 |

**Examples:**

```bash
kindling debug -d my-api          # start debugging, then F5 in VS Code
kindling debug --stop -d my-api   # stop and restore original deployment
```

See [Debugging](/docs/debugging) for language-specific details.

---

### `kindling dev`

**Synopsis:** `kindling dev -d <deployment> [flags]`

Run a frontend dev server locally with full access to cluster APIs. For frontend deployments (nginx, caddy, httpd) where you want local hot reload instead of build+sync.

**Steps:** detect frontend → resolve source dir → port-forward backend APIs → detect OAuth/start tunnel → patch Vite/Next.js config → launch dev server.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | — (required) | Frontend deployment name |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--stop` | | `false` | Stop the dev session |

**Examples:**

```bash
kindling dev -d my-frontend       # start dev mode
kindling dev --stop -d my-frontend  # stop
```

See [Dev Mode](/docs/dev-mode) for full documentation.

---

### `kindling load`

**Synopsis:** `kindling load -s <service> --context <path>`

Build and load a container image directly into Kind (without CI).

---

### `kindling dashboard`

**Synopsis:** `kindling dashboard [--port 9090]`

Launch the kindling web dashboard. Provides visual management for environments, sync, load, pods, logs, secrets, env vars, scaling, and tunnels.

**Dashboard sections:**

| Section | Capabilities |
|---|---|
| Setup | App Designer, Analyze & Generate, Runners |
| Develop | Environments, API Explorer, Cluster Resources |
| Staging | Overview, Deploy, Workloads, Network, TLS, Metrics |

---

### `kindling expose`

**Synopsis:** `kindling expose [flags]`

Create a public HTTPS tunnel to the cluster's ingress controller.

| Provider | Account required |
|---|---|
| cloudflared | No (free quick tunnels) |
| ngrok | Yes (free tier available) |

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--tunnel` | | auto-detect | `cloudflared` or `ngrok` |
| `--port` | | `80` | Local port to expose |
| `--stop` | | `false` | Stop tunnel and restore ingress |
| `--service` | | — | Specific ingress to route to |

---

### `kindling env`

**Synopsis:** `kindling env <subcommand> <deployment> [args]`

Manage environment variables on running deployments.

| Subcommand | Synopsis | Description |
|---|---|---|
| `set` | `kindling env set <deploy> KEY=VALUE [...]` | Set environment variables |
| `list` | `kindling env list <deploy>` | List environment variables |
| `unset` | `kindling env unset <deploy> KEY [...]` | Remove environment variables |

---

### `kindling secrets`

**Synopsis:** `kindling secrets <subcommand>`

Manage external credentials as Kubernetes Secrets with local backup.

| Subcommand | Synopsis | Description |
|---|---|---|
| `set` | `kindling secrets set <name> <value>` | Create/update Secret + local backup |
| `list` | `kindling secrets list` | List all kindling-managed secrets |
| `delete` | `kindling secrets delete <name>` | Remove from cluster and backup |
| `restore` | `kindling secrets restore` | Re-create secrets from backup after cluster rebuild |

---

### `kindling ingress`

**Synopsis:** `kindling ingress <subcommand>`

Manage extra path -> service routes on a DevStagingEnvironment's managed
Ingress (`spec.ingress.routes`) — additional paths merged onto the same
host/Ingress alongside the DSE's primary route. Useful for a frontend that
calls several backend services via same-origin path prefixes (`/orgs`,
`/auth`, `/billing`, ...) under one shared host.

| Subcommand | Synopsis | Description |
|---|---|---|
| `list` | `kindling ingress list <dse>` | Show the primary route + all extra routes |
| `add-route` | `kindling ingress add-route <dse> --path P --service S --port N` | Patch a route onto the live Ingress |
| `remove-route` | `kindling ingress remove-route <dse> --path P` | Remove a route from the live Ingress |
| `save` | `kindling ingress save <dse> -f <file>` | Write the live extra routes into a DSE YAML file |

**Workflow:** patch a route with `add-route`, confirm it works, then run
`save` to persist the same routes into the DSE's YAML file so they're
applied on every future deploy — not just this session's live patch.

```bash
kindling ingress add-route jeff-vincent-gateway --path /orders --service jeff-vincent-orders --port 5000
kindling ingress list jeff-vincent-gateway
kindling ingress save jeff-vincent-gateway -f dev-environment.yaml
```

---

### `kindling deps`

**Synopsis:** `kindling deps <subcommand>`

Manage shared dependency instances (`spec.dependencies[].shared`) — see
[Shared dependencies](dependencies.md#shared-dependencies). Shared
instances aren't owned by any single DSE and aren't deleted automatically
once unused, so use these to find and clean them up.

| Subcommand | Synopsis | Description |
|---|---|---|
| `list-shared` | `kindling deps list-shared` | List shared dependency instances and which DSEs reference them |
| `prune-shared` | `kindling deps prune-shared` | Delete shared instances no DSE references anymore |

```bash
kindling deps list-shared
kindling deps prune-shared
```

---

### `kindling status`

**Synopsis:** `kindling status`

Show cluster, operator, runner, and environment status. Includes crash diagnostics for unhealthy pods.

---

### `kindling logs`

**Synopsis:** `kindling logs [flags]`

Tail the kindling controller logs.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--all` | | `false` | All containers in the pod |
| `--since` | | `5m` | Duration (e.g. `5m`, `1h`) |
| `--follow` | `-f` | `true` | Follow output |

---

## Staging

### `kindling snapshot`

**Synopsis:** `kindling snapshot [flags]`

Export a Helm chart or Kustomize overlay from the current cluster state, optionally push images to a container registry, and deploy to a staging cluster.

**Steps:** read all DSEs → strip actor prefix from names → generate chart with `values.yaml` (clean defaults) + `values-live.yaml` (dev values) → optionally push images via `crane copy` → optionally `helm install` on staging cluster.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--format` | `-f` | `helm` | `helm` or `kustomize` |
| `--output` | `-o` | `./kindling-snapshot` | Output directory |
| `--name` | `-n` | `kindling-snapshot` | Chart/project name |
| `--registry` | `-r` | — | Container registry (e.g. `ghcr.io/myorg`) |
| `--tag` | `-t` | git SHA | Image tag |
| `--deploy` | | `false` | Deploy after generating chart |
| `--context` | | — | Kubeconfig context for staging cluster |
| `--namespace` | | `default` | Namespace to deploy into |

**Examples:**

```bash
# Generate only
kindling snapshot                          # Helm chart in ./kindling-snapshot/
kindling snapshot --format kustomize       # Kustomize overlay
kindling snapshot -o ./my-chart            # custom output directory

# Push images + deploy to staging
kindling snapshot -r ghcr.io/myorg --deploy --context do-staging
kindling snapshot -r ghcr.io/myorg -t v1.2.0 --deploy --context do-staging --namespace staging
```

**Manual usage:**

```bash
helm template my-app ./kindling-snapshot -f values-live.yaml
helm install my-app ./kindling-snapshot \
  --set gateway.env.DATABASE_URL=postgres://staging-host:5432/mydb
```

---

### `kindling staging tls`

**Synopsis:** `kindling staging tls [flags]`

Configure TLS with cert-manager for staging Ingress resources. Installs cert-manager, creates a Let's Encrypt ClusterIssuer, and optionally patches a DSE YAML to enable TLS.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--context` | | — (required) | Kubeconfig context for staging cluster |
| `--domain` | | — (required) | Domain for TLS certificate |
| `--email` | | — (required) | Email for Let's Encrypt registration |
| `--issuer` | | `letsencrypt-prod` | ClusterIssuer name |
| `--staging` | | `false` | Use Let's Encrypt staging server |
| `--file` | `-f` | — | DSE YAML to patch with TLS config |
| `--ingress-class` | | `traefik` | IngressClass for ACME solver |

**Examples:**

```bash
kindling staging tls --context my-staging --domain app.example.com --email admin@example.com
kindling staging tls --context my-staging --domain app.example.com --staging
kindling staging tls --context my-staging --domain app.example.com -f dev-environment.yaml
```

---

## Lifecycle

### `kindling reset`

**Synopsis:** `kindling reset [-y]`

Remove the runner pool to point at a new repo. Leaves the cluster intact.

---

### `kindling destroy`

**Synopsis:** `kindling destroy [-y]`

Delete the Kind cluster and all resources.

---

### `kindling version`

**Synopsis:** `kindling version`

Print the CLI version.

---

## Dependency Auto-Injection

When a dependency is declared in `spec.dependencies[]`, the operator auto-injects the connection URL:

| Dependency | Injected env var |
|---|---|
| postgres | `DATABASE_URL` |
| redis | `REDIS_URL` |
| mysql | `DATABASE_URL` |
| mongodb | `MONGO_URL` |
| rabbitmq | `AMQP_URL` |
| minio | `S3_ENDPOINT` |
| elasticsearch | `ELASTICSEARCH_URL` |
| kafka | `KAFKA_BROKER_URL` |
| nats | `NATS_URL` |
| memcached | `MEMCACHED_URL` |
| cassandra | `CASSANDRA_URL` |
| consul | `CONSUL_URL` |
| vault | `VAULT_ADDR` |
| influxdb | `INFLUXDB_URL` |
| jaeger | `JAEGER_URL` |

Do not duplicate these in `spec.env[]` — they are injected automatically.

---

## Typical Workflow

```bash
# ── SETUP ────────────────────────────────────────
kindling init
kindling runners -u alice -r acme/myapp -t ghp_xxxxx
kindling analyze
kindling generate -k sk-... -r .
kindling secrets set STRIPE_KEY sk_live_...

# ── DEVELOP ──────────────────────────────────────
git push origin main                    # outer loop: build + deploy
kindling status                         # verify deployment
kindling sync -d alice-myapp --restart  # inner loop: live sync
kindling debug -d alice-myapp           # attach debugger
kindling dev -d alice-frontend          # frontend hot reload
kindling dashboard                      # visual control plane
kindling env set alice-myapp LOG_LEVEL=debug
kindling push -s alice-myapp            # rebuild one service
kindling expose                         # public URL for OAuth

# ── PRODUCTION ───────────────────────────────────
kindling snapshot -r ghcr.io/myorg --deploy --context my-staging
kindling staging tls --context my-staging --domain app.example.com --email admin@example.com

# ── LIFECYCLE ────────────────────────────────────
kindling reset                          # switch repos
kindling destroy -y                     # tear down
```
