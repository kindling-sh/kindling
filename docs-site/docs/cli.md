---
sidebar_position: 1
title: CLI Reference
description: Complete reference for all kindling CLI commands, organized by journey phase.
---

# CLI Reference

The `kindling` CLI guides you through the full developer journey:
**analyze → generate → dev loop → graduate**. Commands are organized
by the phase of the journey they belong to.

## Installation

```bash
brew install kindling-sh/tap/kindling
```

Or build from source:

```bash
cd kindling && make cli
sudo cp bin/kindling /usr/local/bin/
```

---

## Global flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--cluster` | `-c` | `dev` | Kind cluster name |
| `--project-dir` | `-p` | `.` (cwd) | Path to kindling project root |

---

## Setup

Commands that bootstrap your local environment.

### `kindling init`

Bootstrap a Kind cluster with the kindling operator.

```
kindling init [flags]
```

**Recommended resources:**

| Workload | CPUs | Memory | Disk |
|---|---|---|---|
| Small (1–3 lightweight services) | 4 | 8 GB | 30 GB |
| Medium (4–6 services, mixed languages) | 6 | 12 GB | 50 GB |
| Large (7+ services, heavy compilers like Rust/Java/C#) | 8+ | 16 GB | 80 GB |

**What it does (in order):**
1. Preflight checks (kind, kubectl, docker on PATH; also go, make if `--build`)
2. `kind create cluster --name dev --config kind-config.yaml`
3. Switch kubectl context to `kind-dev`
4. Run `setup-ingress.sh` (installs Traefik ingress controller + in-cluster registry)
5. Pull operator image from GHCR, or build from source with `--build`
6. Tag as `controller:latest` and load into Kind
7. Apply CRDs and deploy operator via kustomize
8. Wait for controller-manager rollout

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--skip-cluster` | `false` | Skip Kind cluster creation (use existing cluster) |
| `--build` | `false` | Build the operator image from source instead of pulling |
| `--operator-image` | `ghcr.io/kindling-sh/kindling-operator:latest` | Operator image to pull |
| `--image` | — | Node Docker image for Kind (e.g. `kindest/node:v1.29.0`) |
| `--kubeconfig` | — | Path to write kubeconfig |
| `--wait` | — | Wait for control plane (e.g. `60s`, `5m`) |
| `--retain` | `false` | Retain cluster nodes for debugging |
| `--expose` | `false` | Start a public HTTPS tunnel after bootstrap |

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

Create a CI runner pool. Supports **GitHub Actions** (default) and **GitLab CI**.

```
kindling runners [flags]
```

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--username` | `-u` | — | CI platform username |
| `--repo` | `-r` | — | Repository — `owner/repo` (GitHub) or `group/project` (GitLab) |
| `--token` | `-t` | — | PAT or runner registration token |
| `--ci-provider` | | `github` | CI provider: `github` or `gitlab` |

**Examples:**

```bash
kindling runners -u myuser -r myorg/myrepo -t ghp_xxxxx
kindling runners --ci-provider gitlab -u myuser -r mygroup/myproject -t glpat_xxxxx
```

---

### `kindling intel`

Toggle agent context files for AI coding assistants (GitHub Copilot, Claude Code, Cursor, Windsurf).

```
kindling intel on
kindling intel off
```

---

## Onboarding

Commands that prepare your project for kindling.

### `kindling analyze`

Check your project's readiness before generating a workflow.

```
kindling analyze [flags]
```

**What it checks:**
- **Dockerfiles** — found, valid, Kaniko-compatible
- **Dependencies** — Postgres, Redis, MongoDB, Kafka, and 11 more
- **Secrets** — external credentials (`*_API_KEY`, `*_SECRET`, `*_TOKEN`)
- **Agent architecture** — LangChain, CrewAI, LangGraph, OpenAI Agents SDK, MCP servers
- **Inter-service communication** — HTTP calls between services
- **Build context** — verifies COPY/ADD paths align with the Dockerfile's build context

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--repo-path` | `-r` | `.` | Path to the local repository to analyze |
| `--verbose` | `-v` | `false` | Show additional detail |

See the full [Analyze reference](analyze.md) for details.

---

### `kindling scaffold` *(coming soon)*

> Generate Dockerfiles and project structure for repos that don't have them.

---

### `kindling generate`

AI-generate a CI workflow for any repository.

```
kindling generate [flags]
```

**What it does:**
1. Scans the repository for Dockerfiles, dependency manifests, and source files
2. Detects services, languages, ports, health-check endpoints, and dependencies
3. Builds a detailed prompt and calls the AI provider
4. Writes a complete CI workflow using `kindling-build` and `kindling-deploy` actions

**Smart scanning:**
- docker-compose.yml analysis for build contexts, depends_on, and env vars
- Helm chart detection and rendering
- Kustomize overlay detection and rendering
- `.env` template scanning (`.env.sample`, `.env.example`, etc.)
- Ingress heuristics — only user-facing services get routes by default
- External credential detection with `kindling secrets set` suggestions
- OAuth/OIDC detection with `kindling expose` suggestions

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--api-key` | `-k` | — (required) | GenAI API key |
| `--repo-path` | `-r` | `.` | Path to the repository |
| `--ai-provider` | | `openai` | `openai` or `anthropic` |
| `--model` | | auto | Model name (default: `o3` / `claude-sonnet-4-20250514`) |
| `--output` | `-o` | auto | Output path for the workflow file |
| `--dry-run` | | `false` | Print to stdout instead of writing |
| `--ingress-all` | | `false` | Wire every service with an ingress route |
| `--no-helm` | | `false` | Skip Helm/Kustomize rendering |
| `--ci-provider` | | `github` | `github` or `gitlab` |

**Examples:**

```bash
kindling generate -k sk-... -r .
kindling generate -k sk-... -r . --dry-run
kindling generate -k sk-ant-... -r . --ai-provider anthropic
kindling generate -k sk-... -r . --ci-provider gitlab
kindling generate -k sk-... -r . --ingress-all
```

---

## Dev Loop

Commands for iterating on your app — the core development experience.

### `kindling push`

Rebuild and redeploy a single service via CI.

```
kindling push -s <service>
```

**Pre-flight checks:**
- Verifies workflow secrets exist in the cluster
- Validates the workflow file exists

---

### `kindling sync`

Live-sync local files into a running pod with language-aware hot reload.

```
kindling sync [flags]
```

**Restart strategies (auto-detected):**

| Strategy | Runtimes | How it works |
|---|---|---|
| **wrapper + kill** | Node.js, Python, Ruby, Perl, Lua, Julia, R, Elixir, Deno, Bun | Patches deployment, syncs files, kills child to respawn |
| **signal reload** | uvicorn, gunicorn, Puma, Unicorn, Nginx, Apache | Sends SIGHUP for zero-downtime reload |
| **auto-reload** | PHP, nodemon | Syncs files — runtime re-reads automatically |
| **local build + sync** | Go, Rust, Java, Kotlin, C#/.NET, C/C++, Zig | Cross-compiles locally, syncs binary, restarts |

**Automatic rollback:** When you stop sync (Ctrl+C), the deployment returns
to its pre-sync state.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | — (required) | Target deployment name |
| `--src` | — | `.` | Local source directory |
| `--dest` | — | `/app` | Destination inside container |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--restart` | — | `false` | Restart app after each sync |
| `--once` | — | `false` | Sync once and exit |
| `--container` | — | — | Container name (multi-container pods) |
| `--exclude` | — | — | Additional exclude patterns |
| `--debounce` | — | `500ms` | Debounce interval |
| `--language` | — | auto | Override runtime detection |
| `--build-cmd` | — | auto | Local build command for compiled languages |
| `--build-output` | — | auto | Path to built artifact |

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

Attach a VS Code debugger to a running deployment. Detects the runtime
automatically, injects the debug agent, port-forwards the debug port,
and writes a launch configuration.

```
kindling debug -d <deployment> [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | | Deployment name (required) |
| `--stop` | | `false` | Stop an active debug session |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--port` | | (auto) | Override local debug port |
| `--no-launch` | | `false` | Skip writing launch.json |

**Supported runtimes and app servers:**

| Runtime | App servers | Debug tool | Port |
|---|---|---|---|
| **Python** | uvicorn, gunicorn, flask, django, celery, fastapi, daphne, hypercorn, waitress, tornado, sanic, gRPC, plain python | debugpy | 5678 |
| **Node.js** | node, ts-node, tsx, npm, yarn, pnpm, Express, NestJS | V8 Inspector | 9229 |
| **Deno** | deno | V8 Inspector | 9229 |
| **Bun** | bun | Bun Inspector | 6499 |
| **Go** | any compiled binary (cross-compile + Delve inject) | Delve | 2345 |
| **Ruby** | ruby, rails, puma, unicorn, thin, falcon, bundle exec | rdbg | 12345 |

```bash
# Start debugging
kindling debug -d my-api
# Press F5 in VS Code to attach

# Stop debugging (restores original deployment)
kindling debug --stop -d my-api
# Or press Ctrl-C in the terminal
```

See [Debugging](/docs/debugging) for full language-specific documentation,
dependencies, and troubleshooting.

---

### `kindling dev`

Run a frontend dev server locally with full access to cluster APIs.
Designed for frontend deployments (nginx, caddy, httpd) where you want
hot reload from your local dev server instead of building and syncing
static assets.

```
kindling dev -d <deployment> [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | | Frontend deployment name (required) |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--stop` | | `false` | Stop the dev session |

**What it does:**
1. Detects the frontend deployment (nginx/caddy/httpd serving a SPA)
2. Resolves the local source directory (monorepo-aware)
3. Port-forwards all backend API services to localhost
4. Detects OAuth/OIDC and starts an HTTPS tunnel if needed
5. Auto-patches Vite/Next.js config for tunnel hostname
6. Launches your local dev server (`npm/pnpm/yarn run dev`)
7. Ctrl-C stops the dev server, tunnel, and port-forwards cleanly

```bash
# Start frontend dev mode
kindling dev -d my-frontend
# Dev server starts automatically — edit code, see changes instantly

# Stop dev mode
kindling dev --stop -d my-frontend
# Or press Ctrl-C
```

See [Dev Mode](/docs/dev-mode) for full documentation.

---

### `kindling load`

Build and load a container image directly into Kind (without CI).

```
kindling load -s <service> --context <path>
```

---

### `kindling dashboard`

Launch the kindling web dashboard.

```
kindling dashboard [--port 9090]
```

| Feature | Description |
|---|---|
| **Environments** | All DevStagingEnvironments with status |
| **Sync button** | One-click live sync with auto-detected runtime |
| **Load button** | Rebuild image locally + rolling update |
| **Runtime badges** | Per-service runtime detection |
| **Pod status** | Pods, restart counts, container readiness |
| **Log viewer** | Real-time container logs |
| **Secrets** | Create and manage kindling secrets |
| **Env Vars** | Set/unset environment variables |
| **Scale & Restart** | Scale deployments, rolling restart |
| **Expose** | Start/stop public HTTPS tunnels |

---

### `kindling expose`

Create a public HTTPS tunnel to the cluster's ingress controller.

```
kindling expose [flags]
```

| Provider | Account required |
|---|---|
| cloudflared | No (free quick tunnels) |
| ngrok | Yes (free tier available) |

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--tunnel` | auto-detect | `cloudflared` or `ngrok` |
| `--port` | `80` | Local port to expose |
| `--stop` | `false` | Stop tunnel and restore ingress |
| `--service` | — | Specific ingress to route to |

---

### `kindling env`

Manage environment variables on running deployments.

```
kindling env <subcommand> <deployment> [args]
```

| Subcommand | Description |
|---|---|
| `set <deploy> KEY=VALUE [...]` | Set environment variables |
| `list <deploy>` | List environment variables |
| `unset <deploy> KEY [...]` | Remove environment variables |

---

### `kindling secrets`

Manage external credentials as Kubernetes Secrets.

```
kindling secrets <subcommand>
```

| Subcommand | Description |
|---|---|
| `set <name> <value>` | Create/update a K8s Secret + local backup |
| `list` | List all kindling-managed secrets |
| `delete <name>` | Remove from cluster and backup |
| `restore` | Re-create secrets from backup after cluster rebuild |

---

## Operations

### `kindling status`

Show the status of the cluster, operator, runners, and environments.
Includes crash diagnostics for unhealthy pods.

### `kindling logs`

Tail the kindling controller logs.

| Flag | Short | Default | Description |
|---|---|---|---|
| `--all` | — | `false` | All containers in the pod |
| `--since` | — | `5m` | Duration (e.g. `5m`, `1h`) |
| `--follow` | `-f` | `true` | Follow output |

### `kindling deploy`

Apply a DevStagingEnvironment from a YAML file (manual deploy).

```
kindling deploy -f <file>
```

---

## Lifecycle

### `kindling reset`

Remove the runner pool to point at a new repo. Leaves the cluster intact.

```
kindling reset [-y]
```

### `kindling destroy`

Delete the Kind cluster and all resources.

```
kindling destroy [-y]
```

### `kindling snapshot`

Export a Helm chart or Kustomize overlay from the current cluster state,
optionally push images to a container registry, and deploy to a production cluster.
Reads all DevStagingEnvironments in the cluster and generates
production-ready Kubernetes manifests.

```
kindling snapshot [flags]
```

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--format` | `-f` | `helm` | Export format: `helm` or `kustomize` |
| `--output` | `-o` | `./kindling-snapshot` | Output directory |
| `--name` | `-n` | `kindling-snapshot` | Chart/project name |
| `--registry` | `-r` | | Container registry (e.g. `ghcr.io/myorg`, `123456.dkr.ecr.us-east-1.amazonaws.com/myapp`) |
| `--tag` | `-t` | git SHA | Image tag (default: git SHA or `latest`) |
| `--deploy` | | `false` | Deploy to a production cluster after generating the chart |
| `--context` | | | Kubeconfig context for the production cluster (required with `--deploy`) |
| `--namespace` | | `default` | Kubernetes namespace to deploy into (used with `--deploy`) |

**What it does:**

1. Reads all DSEs from the cluster
2. Strips the GitHub actor prefix from service names (e.g. `jeff-gateway` → `gateway`)
3. Generates a production-ready chart with:
   - **values.yaml** — clean defaults with empty env vars for production connection strings
   - **values-live.yaml** — populated with your dev cluster's actual values
   - Deployment + Service templates per service
   - Dependency deployments (Postgres, Redis, MongoDB, etc.)
4. Auto-injected env vars (e.g. `DATABASE_URL`, `MONGO_URL`, `REDIS_URL`) are
   real configurable values in `values.yaml`, not hardcoded dev values
5. With `--registry`, pushes images from the local Kind registry to the target
   registry using `crane copy`
6. With `--deploy`, installs the Helm chart on the production cluster using
   the specified `--context`

**Examples:**

```bash
# Generate only
kindling snapshot                          # Helm chart in ./kindling-snapshot/
kindling snapshot --format kustomize       # Kustomize overlay
kindling snapshot -o ./my-chart            # custom output directory
kindling snapshot --name my-platform       # custom chart name

# Push images + deploy to production
kindling snapshot -r ghcr.io/myorg --deploy --context do-prod
kindling snapshot -r ghcr.io/myorg -t v1.2.0 --deploy --context do-prod --namespace staging
```

**Using the output manually:**

```bash
# Dry-run with live dev values
helm template my-app ./kindling-snapshot -f values-live.yaml

# Install with production values
helm install my-app ./kindling-snapshot \
  --set gateway.env.DATABASE_URL=postgres://prod-host:5432/mydb \
  --set inventory.env.MONGO_URL=mongodb://prod-host:27017
```

### `kindling production tls`

Configure TLS with cert-manager for production Ingress resources. Installs
cert-manager (if not already present), creates a ClusterIssuer for Let's Encrypt,
and optionally patches a DSE YAML file to enable TLS.

```
kindling production tls [flags]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--context` | *(required)* | Kubeconfig context for the production cluster |
| `--domain` | *(required)* | Domain name for the TLS certificate |
| `--email` | *(required)* | Email for Let's Encrypt registration |
| `--issuer` | `letsencrypt-prod` | ClusterIssuer name |
| `--staging` | `false` | Use Let's Encrypt staging server (for testing) |
| `--file` / `-f` | | Optional: DSE YAML to patch with TLS config |
| `--ingress-class` | `traefik` | IngressClass for the ACME solver |

**What it does:**

1. Refuses Kind contexts (use `kindling expose` for local dev TLS)
2. Installs cert-manager v1.17.1 if not already present
3. Creates a Let's Encrypt ClusterIssuer
4. Optionally patches a DSE YAML file with `host`, `ingressClassName`,
   `cert-manager.io/cluster-issuer` annotation, and TLS secret config

**Examples:**

```bash
kindling production tls --context my-prod --domain app.example.com --email admin@example.com
kindling production tls --context my-prod --domain app.example.com --staging
kindling production tls --context my-prod --domain app.example.com -f dev-environment.yaml
```

### `kindling version`

Print the CLI version.

---

## Typical workflow

```bash
# ── SETUP ─────────────────────────────────────────────────
kindling init
kindling secrets restore             # if rebuilding cluster
kindling runners -u alice -r acme/myapp -t ghp_xxxxx

# ── ONBOARDING ────────────────────────────────────────────
kindling analyze                     # check project readiness
kindling generate -k sk-... -r .     # AI-generate workflow
kindling secrets set STRIPE_KEY sk_live_...

# ── DEV LOOP ──────────────────────────────────────────────
git push origin main                 # outer loop: build + deploy
kindling status                      # verify deployment
kindling sync -d alice-myapp --restart  # inner loop: live sync
kindling debug -d alice-myapp        # attach debugger (F5 in VS Code)
kindling dev -d alice-frontend       # frontend hot reload
kindling dashboard                   # visual control plane

# ── ITERATE ───────────────────────────────────────────────
kindling env set alice-myapp LOG_LEVEL=debug
kindling push -s alice-myapp         # one-service rebuild
kindling expose                      # public URL for OAuth

# ── GRADUATE TO PRODUCTION ────────────────────────────────
kindling snapshot -r ghcr.io/myorg --deploy --context my-prod
kindling production tls --context my-prod --domain app.example.com --email admin@example.com

# ── LIFECYCLE ─────────────────────────────────────────────
kindling reset                       # switch repos
kindling destroy -y                  # tear down
```
