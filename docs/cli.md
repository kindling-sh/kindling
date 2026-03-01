# CLI Reference

The `kindling` CLI guides you through the full developer journey:
**analyze → generate → dev loop → promote**. Commands are organized
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
4. Run `setup-ingress.sh` (installs ingress-nginx + in-cluster registry)
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

Activates or deactivates project context that helps your coding agent understand kindling's architecture, CLI, and deployment model.

---

## Onboarding

Commands that prepare your project for kindling.

### `kindling analyze`

Check your project's readiness before generating a workflow.

```
kindling analyze [flags]
```

**What it checks:**
- **Dockerfiles** — found, valid, Kaniko-compatible (no BuildKit-only features like `TARGETARCH`)
- **Dependencies** — Postgres, Redis, MongoDB, Kafka, and 11 more detected from source
- **Secrets** — external credentials (`*_API_KEY`, `*_SECRET`, `*_TOKEN`) your app references
- **Agent architecture** — LangChain, CrewAI, LangGraph, OpenAI Agents SDK, MCP servers
- **Inter-service communication** — HTTP calls between services in a multi-service repo
- **Build context** — verifies COPY/ADD paths align with the Dockerfile's build context
- **Project structure** — suggestions for Kaniko compatibility (`.dockerignore`, `Go -buildvcs=false`, etc.)

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--repo-path` | `-r` | `.` | Path to the local repository to analyze |
| `--verbose` | `-v` | `false` | Show additional detail |

**Examples:**

```bash
kindling analyze
kindling analyze -r /path/to/my-app
kindling analyze -v
```

---

### `kindling scaffold` *(coming soon)*

> Generate Dockerfiles and project structure for repos that don't have them.
> This command is on the roadmap.

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
kindling push -s <service> [flags]
```

**Pre-flight checks:**
- Verifies workflow secrets exist in the cluster (checks both bare and `kindling-secret-` prefixed names)
- Validates the workflow file exists

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--service` | `-s` | — (required) | Service name to rebuild |

**Examples:**

```bash
kindling push -s my-api
```

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
to its pre-sync state. No manual cleanup.

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

### `kindling load`

Build and load a container image directly into Kind (without CI).

```
kindling load -s <service> --context <path> [flags]
```

Useful for Dockerfile changes or dependency updates that need a full
rebuild without going through `git push`.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--service` | `-s` | — (required) | Service name |
| `--context` | — | `.` | Build context path |

---

### `kindling dashboard`

Launch the kindling web dashboard.

```
kindling dashboard [--port 9090]
```

A visual interface for the full dev loop:

| Feature | Description |
|---|---|
| **Environments** | All DevStagingEnvironments with status |
| **Sync button** | One-click live sync with auto-detected runtime |
| **Load button** | Rebuild image locally + rolling update |
| **Runtime badges** | Per-service runtime detection |
| **Pod status** | Pods, restart counts, container readiness |
| **Log viewer** | Real-time container logs |
| **Services & Ingresses** | Routing rules, ports, hostnames |
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

Auto-patches the active ingress with the tunnel hostname. Saves the
original config for restoration on `--stop`.

**Supported providers:**

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

**Examples:**

```bash
kindling expose
kindling expose --tunnel cloudflared
kindling expose --stop
```

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

Changes trigger an automatic rolling restart.

```bash
kindling env set myapp-dev DATABASE_PORT=5432
kindling env list myapp-dev
kindling env unset myapp-dev LOG_LEVEL
```

---

### `kindling secrets`

Manage external credentials as Kubernetes Secrets.

```
kindling secrets <subcommand> [flags]
```

| Subcommand | Description |
|---|---|
| `set <name> <value>` | Create/update a K8s Secret + local backup |
| `list` | List all kindling-managed secrets |
| `delete <name>` | Remove from cluster and backup |
| `restore` | Re-create secrets from backup after cluster rebuild |

Secrets are stored as `kindling-secret-<lowercase-name>` with a local
backup at `.kindling/secrets.yaml` (auto-gitignored).

```bash
kindling secrets set STRIPE_KEY sk_live_abc123
kindling secrets list
kindling secrets restore   # after kindling init
```

---

## Operations

Commands for monitoring and troubleshooting.

### `kindling status`

Show the status of the cluster, operator, runners, and environments.

```
kindling status
```

Shows: cluster health, operator readiness, registry, ingress, runner pools,
DevStagingEnvironments, pods, and **crash diagnostics** for unhealthy pods.

---

### `kindling logs`

Tail the kindling controller logs.

```
kindling logs [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--all` | — | `false` | All containers in the pod |
| `--since` | — | `5m` | Duration (e.g. `5m`, `1h`) |
| `--follow` | `-f` | `true` | Follow output |

---

### `kindling deploy`

Apply a DevStagingEnvironment from a YAML file (manual deploy).

```
kindling deploy -f <file>
```

```bash
kindling deploy -f examples/sample-app/dev-environment.yaml
```

---

## Lifecycle

Commands for resetting and tearing down environments.

### `kindling reset`

Remove the runner pool to point at a new repo. Leaves the cluster intact.

```
kindling reset [-y]
```

```bash
kindling reset -y
kindling runners -u myuser -r neworg/newrepo -t ghp_xxxxx
```

---

### `kindling destroy`

Delete the Kind cluster and all resources.

```
kindling destroy [-y]
```

---

### `kindling promote` *(coming soon)*

> Graduate your staging environment to a production cluster with TLS, DNS,
> and real infrastructure. This command is on the roadmap.

---

### `kindling version`

Print the CLI version.

```bash
kindling version
# kindling 0.8.1-dev (darwin/arm64)
```

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
kindling dashboard                   # visual control plane

# ── ITERATE ───────────────────────────────────────────────
kindling env set alice-myapp LOG_LEVEL=debug
kindling push -s alice-myapp         # one-service rebuild
kindling expose                      # public URL for OAuth

# ── LIFECYCLE ─────────────────────────────────────────────
kindling reset                       # switch repos
kindling destroy -y                  # tear down
```
