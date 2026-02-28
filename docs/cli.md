# CLI Reference

The `kindling` CLI provides a **dev-in-CI** workflow with two nested loops.
The **outer loop** commands (`init`, `runners`, `generate`, `deploy`) set up
CI/CD on your laptop. The **inner loop** commands (`sync`, `dashboard`) give
you sub-second iteration on running containers. The CLI wraps `kind`, `kubectl`,
and Docker into a streamlined developer workflow.

## Installation

```bash
cd kindling
make cli
# Binary is at bin/kindling
sudo cp bin/kindling /usr/local/bin/  # optional: add to PATH
```

---

## Global flags

These flags are available on every command:

| Flag | Short | Default | Description |
|---|---|---|---|
| `--cluster` | `-c` | `dev` | Kind cluster name |
| `--project-dir` | `-p` | `.` (cwd) | Path to kindling project root |

---

## Commands

### `kindling init`

Bootstrap a Kind cluster with the kindling operator.

```
kindling init [flags]
```

**Recommended resources:**

Kindling runs a full Kubernetes cluster inside Docker via Kind. Allocate enough
Docker Desktop resources for your workload:

| Workload | CPUs | Memory | Disk |
|---|---|---|---|
| Small (1–3 lightweight services) | 4 | 8 GB | 30 GB |
| Medium (4–6 services, mixed languages) | 6 | 12 GB | 50 GB |
| Large (7+ services, heavy compilers like Rust/Java/C#) | 8+ | 16 GB | 80 GB |

These resources are configured in **Docker Desktop → Settings → Resources**. The
default Kind config uses a single control-plane node. For larger workloads, this
is sufficient since all pods schedule on the same node — adding worker nodes
doesn't help much in a local dev context and just splits the available memory.

> **Tip:** Kaniko layer caching is enabled (`registry:5000/cache`), so first
> builds are slow but subsequent rebuilds are fast. Make sure you have enough
> disk for the cache — heavy stacks (Rust, Java) can use 2–5 GB of cached
> layers per service.

**What it does (in order):**
1. Preflight checks (kind, kubectl, docker on PATH; also go, make if `--build`)
2. `kind create cluster --name dev --config kind-config.yaml`
3. Switch kubectl context to `kind-dev`
4. Run `setup-ingress.sh` (installs ingress-nginx + in-cluster registry)
5. Pull operator image from GHCR (`docker pull ghcr.io/kindling-sh/kindling-operator:latest`), or build from source with `--build`
6. Tag as `controller:latest` and `kind load docker-image controller:latest --name dev`
7. Apply CRDs (`kubectl apply -f config/crd/bases`)
8. Deploy operator via kustomize
9. Wait for controller-manager rollout

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--skip-cluster` | `false` | Skip Kind cluster creation (use existing cluster) |
| `--build` | `false` | Build the operator image from source instead of pulling the pre-built image (requires Go and Make) |
| `--operator-image` | `ghcr.io/kindling-sh/kindling-operator:latest` | Operator image to pull (ignored when `--build` is set) |
| `--image` | — | Node Docker image for Kind (e.g. `kindest/node:v1.29.0`) |
| `--kubeconfig` | — | Path to write kubeconfig instead of default location |
| `--wait` | — | Wait for control plane to be ready (e.g. `60s`, `5m`) |
| `--retain` | `false` | Retain cluster nodes for debugging on creation failure |
| `--expose` | `false` | Start a public HTTPS tunnel after bootstrap (runs `kindling expose`) |

**Examples:**

```bash
# Default: create "dev" cluster (pulls pre-built operator image)
kindling init

# Build operator from source (requires Go and Make)
kindling init --build

# Use a custom operator image
kindling init --operator-image myregistry.io/kindling-operator:v1.0.0

# Bootstrap and immediately start a public tunnel
kindling init --expose

# Use a specific Kubernetes version
kindling init --image kindest/node:v1.29.0

# Custom cluster name with debug retention
kindling init -c staging --retain --wait 5m

# Skip cluster creation, just deploy operator into existing cluster
kindling init --skip-cluster
```

---

### `kindling runners`

Create a CI runner pool in the cluster. Supports **GitHub Actions** (default) and **GitLab CI**.

```
kindling runners [flags]
```

**What it does:**
1. Prompts for any missing values (username, repo, token)
2. Creates the runner token Secret
3. Applies a runner pool CR
4. Waits for the runner pod to become ready
5. Verifies the runner registers with the CI platform

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--username` | `-u` | — | CI platform username |
| `--repo` | `-r` | — | Repository — `owner/repo` (GitHub) or `group/project` (GitLab) |
| `--token` | `-t` | — | Personal Access Token or runner registration token |
| `--provider` | | `github` | CI provider: `github` or `gitlab` |

**Examples:**

```bash
# Interactive mode (prompts for missing values)
kindling runners

# GitHub Actions (default)
kindling runners -u myuser -r myorg/myrepo -t ghp_xxxxx

# GitLab CI
kindling runners --provider gitlab -u myuser -r mygroup/myproject -t glpat_xxxxx
```

---

### `kindling generate`

AI-generate a CI workflow for any repository. Supports **GitHub Actions** (`.github/workflows/dev-deploy.yml`) and **GitLab CI** (`.gitlab-ci.yml`).

```
kindling generate [flags]
```

**What it does:**
1. Scans the repository for Dockerfiles, dependency manifests, and source files
2. Detects services, languages, ports, health-check endpoints, and backing dependencies
3. Builds a detailed prompt and calls the AI provider (OpenAI or Anthropic)
4. Writes a complete CI workflow using `kindling-build` and `kindling-deploy` actions

**Supported languages:** Go, TypeScript, Python, Java, Rust, Ruby, PHP, C#, Elixir

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--api-key` | `-k` | — (required) | GenAI API key |
| `--repo-path` | `-r` | `.` | Path to the local repository to analyze |
| `--provider` | | `openai` | AI provider: `openai` or `anthropic` |
| `--model` | | auto | Model name (default: `o3` for openai, `claude-sonnet-4-20250514` for anthropic). Supports OpenAI reasoning models (`o3`, `o3-mini`) which use the `developer` role and extended thinking. |
| `--output` | `-o` | `<repo>/.github/workflows/dev-deploy.yml` | Output path for the workflow file |
| `--dry-run` | | `false` | Print the generated workflow to stdout instead of writing a file |
| `--ingress-all` | | `false` | Wire every service with an ingress route, not just detected frontends |
| `--no-helm` | | `false` | Skip Helm/Kustomize rendering; use raw source inference only |

**Smart scanning features:**

- **docker-compose.yml analysis** — Uses docker-compose as the source of truth for build contexts (`context` + `dockerfile`), `depends_on` for dependency types, and `environment` sections for env var mappings across all services.
- **Helm charts** — Detects `Chart.yaml`, runs `helm template` to render manifests, passes them to the AI as authoritative context. Falls back gracefully if `helm` is not installed.
- **Kustomize overlays** — Detects `kustomization.yaml`, runs `kustomize build` for rendered context. Falls back gracefully if `kustomize` is not installed.
- **`.env` template files** — Scans `.env.sample`, `.env.example`, `.env.development`, and `.env.template` files for required configuration variables.
- **Ingress heuristics** — Only user-facing services (frontends, SSR, gateways) get ingress routes by default. Use `--ingress-all` to override.
- **External credential detection** — Scans for `*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_DSN`, etc. and suggests `kindling secrets set` for each.
- **OAuth/OIDC detection** — Flags Auth0, Okta, Firebase Auth, NextAuth, Passport.js patterns and suggests `kindling expose`.

**Examples:**

```bash
# Generate with OpenAI (default: o3 model)
kindling generate -k sk-... -r /path/to/my-app

# Use a specific model
kindling generate -k sk-... -r . --model gpt-4o

# Use o3-mini for faster, cheaper reasoning
kindling generate -k sk-... -r . --model o3-mini

# Use Anthropic
kindling generate -k sk-ant-... -r . --provider anthropic

# Preview without writing
kindling generate -k sk-... -r . --dry-run

# Custom output path
kindling generate -k sk-... -r . -o ./my-workflow.yml

# Wire every service with ingress (not just frontends)
kindling generate -k sk-... -r . --ingress-all

# Skip Helm/Kustomize rendering
kindling generate -k sk-... -r . --no-helm
```

---

### `kindling deploy`

Apply a DevStagingEnvironment from a YAML file.

```
kindling deploy -f <file> [flags]
```

**What it does:**
1. Runs `kubectl apply -f <file>`
2. Lists all current DevStagingEnvironments

**Flags:**

| Flag | Short | Required | Description |
|---|---|---|---|
| `--file` | `-f` | ✅ | Path to DevStagingEnvironment YAML file |

**Examples:**

```bash
kindling deploy -f examples/sample-app/dev-environment.yaml
kindling deploy -f examples/platform-api/dev-environment.yaml
```

---

### `kindling status`

Show the status of the cluster, operator, runners, and environments.

```
kindling status
```

**What it shows:**
- **Cluster** — Kind cluster existence and node status
- **Operator** — Controller-manager deployment readiness
- **Registry** — In-cluster registry deployment status
- **Ingress Controller** — ingress-nginx pod status
- **Runner Pools** — CI runner pool CRs (name, username, repo)
- **Dev Environments** — DevStagingEnvironment CRs (image, replicas,
  ready state)
- **Pods** — All pods in the default namespace with status and age
- **Unhealthy Pods** — Pods in CrashLoopBackOff, Error, or other non-Running
  states with their last 10 log lines for quick diagnosis

**Example output:**

```
▸ Cluster
  ✅ Kind cluster "dev" exists
    dev-control-plane   Ready   v1.31.0

▸ Operator
    ✓  kindling-controller-manager   1   1   2026-02-15T10:00:00Z

▸ Registry
    registry:5000  1   1

▸ Ingress Controller
    ingress-nginx-controller-xxxxx   Running   0

▸ Runner Pools
    myuser-runner-pool   myuser   myorg/myrepo

▸ Dev Staging Environments
    NAME         IMAGE                              REPLICAS   AVAILABLE   READY
    myuser-app   registry:5000/myapp:abc123         1          1           true
```

---

### `kindling logs`

Tail the kindling controller logs.

```
kindling logs [flags]
```

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--all` | — | `false` | Show logs from all containers in the pod |
| `--since` | — | `5m` | Show logs since duration (e.g. `5m`, `1h`) |
| `--follow` | `-f` | `true` | Follow log output (stream). Press Ctrl+C to stop |

**Examples:**

```bash
# Stream recent logs (default)
kindling logs

# Show last hour, don't follow
kindling logs --since 1h -f=false

# All containers including kube-rbac-proxy
kindling logs --all
```

---

### `kindling sync`

Live-sync local files into a running pod with language-aware hot reload.

```
kindling sync [flags]
```

**What it does:**
1. Finds the running pod for the specified deployment
2. Auto-detects the runtime from the container's PID 1 command line
3. Syncs local files into the container via `kubectl cp`
4. Restarts the process using the detected strategy
5. Optionally watches for file changes and re-syncs on each save

**Restart strategies (auto-detected):**

| Strategy | Runtimes | How it works |
|---|---|---|
| **wrapper + kill** | Node.js, Python, Ruby, Perl, Lua, Julia, R, Elixir, Deno, Bun | Patches the deployment with a restart-loop wrapper, syncs files, kills the child process so the loop respawns it with new code |
| **signal reload** | uvicorn, gunicorn, Puma, Unicorn, Nginx, Apache (httpd) | Sends SIGHUP or SIGUSR2 to PID 1 for zero-downtime graceful reload. Falls back to wrapper+kill if PID 1 is the wrapper shell |
| **auto-reload** | PHP (mod_php, php-fpm), nodemon | Just syncs files — the runtime re-reads on every request or watches for changes itself |
| **local build + binary sync** | Go, Rust, Java, Kotlin, C#/.NET, C/C++, Zig | Cross-compiles locally for the container's OS/arch, syncs just the binary, and restarts via the wrapper |

**Runtime detection** reads `/proc/1/cmdline` from the container and matches against 30+ known process signatures (`node`, `python`, `ruby`, `uvicorn`, `gunicorn`, `puma`, `nginx`, `httpd`, `go`, `cargo`, `dotnet`, `java`, `mix`, `iex`, `deno`, `bun`, `perl`, `php-fpm`, `lua`, `luajit`, `julia`, `Rscript`, etc.). For Python, it also scans command arguments for framework names (uvicorn, gunicorn, flask, django, etc.).

**Compiled language support:** For `modeRebuild` languages, `kindling sync` queries the Kind node's architecture (`kubectl get nodes -o jsonpath=...`) and auto-generates the correct cross-compilation command:
- **Go** — `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o <tmpfile> .`
- **Rust** — `cargo build --release --target aarch64-unknown-linux-gnu`
- **Java** — `mvn package -DskipTests` or `gradle build -x test`
- **.NET** — `dotnet publish -r linux-arm64 -c Release --self-contained`

Use `--build-cmd` and `--build-output` to override the auto-detected build.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | — (required) | Target deployment name |
| `--src` | — | `.` | Local source directory to watch |
| `--dest` | — | `/app` | Destination path inside the container |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--restart` | — | `false` | Restart the app process after each sync batch |
| `--once` | — | `false` | Sync once and exit (no file watching) |
| `--container` | — | — | Container name (for multi-container pods) |
| `--exclude` | — | — | Additional patterns to exclude (repeatable) |
| `--debounce` | — | `500ms` | Debounce interval for batching rapid file changes |
| `--language` | — | auto-detect | Override runtime detection (`node`, `python`, `ruby`, `php`, `go`, `rust`, `java`, `dotnet`, `elixir`, ...) |
| `--build-cmd` | — | auto-detect | Local build command for compiled languages |
| `--build-output` | — | auto-detect | Path to built artifact to sync into the container |

**Default excludes:** `.git`, `node_modules`, `__pycache__`, `.venv`, `vendor`, `target`, `.next`, `dist`, `build`, `*.pyc`, `*.o`, `*.exe`, `*.log`, `.DS_Store`.

**Examples:**

```bash
# Watch current directory + auto-restart a Node.js service
kindling sync -d my-api --restart

# One-shot sync + restart (no file watching)
kindling sync -d my-api --restart --once

# Sync into a Python/uvicorn service (detects SIGHUP strategy)
kindling sync -d orders --src ./services/orders --restart

# Sync static files into Nginx (SIGHUP zero-downtime reload)
kindling sync -d frontend --src ./dist --dest /usr/share/nginx/html --restart

# Go service — auto cross-compiles locally for container arch
kindling sync -d gateway --restart --language go

# Custom build command + output for compiled languages
kindling sync -d gateway --restart \
  --build-cmd 'CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/gw .' \
  --build-output ./bin/gw

# Sync a specific source dir with extra excludes
kindling sync -d my-api --src ./src --exclude '*.test.js' --exclude 'fixtures/'

# Target a specific container in a multi-container pod
kindling sync -d my-api --container app --restart
```

---

### `kindling dashboard`

Launch the kindling web dashboard — a visual interface for both
development loops.

```
kindling dashboard [flags]
```

**What it does:**
1. Starts an HTTP server serving the embedded React/TypeScript SPA
2. Exposes read-only API endpoints for all cluster resources
3. Exposes action API endpoints for sync, load, deploy, and management
4. Opens at `http://localhost:9090` (default)

**Dashboard features:**

| Feature | Description |
|---|---|
| **Environments** | View all DevStagingEnvironments with status, image, replicas |
| **Sync button** | One-click live sync with auto-detected runtime and real-time status |
| **Load button** | Rebuild container image locally + load into Kind + rolling update |
| **Runtime badges** | Per-service runtime detection (Node.js, Python, Go, nginx, etc.) |
| **Pod status** | View pod status, restart counts, container readiness |
| **Log viewer** | Tail container logs in real time |
| **Services** | View all services with ports and selectors |
| **Ingresses** | View routing rules and hostnames |
| **Events** | Kubernetes events stream for debugging |
| **Secrets** | Create and manage kindling secrets |
| **Runners** | View and create GitHub Actions runner pools |
| **Deploy** | Apply DevStagingEnvironment YAML directly |
| **Env Vars** | Set/unset environment variables on deployments |
| **Scale** | Scale deployments up or down |
| **Restart** | Rolling restart deployments |
| **Expose** | Start/stop public HTTPS tunnels |
| **Destroy** | Tear down the cluster |

**Sync via dashboard:**

The Sync button on each service:
1. Auto-detects the runtime from the container's PID 1 process
2. Shows the detected runtime as a badge (e.g., `Python/uvicorn`, `Go`, `Node.js`)
3. Prompts for the local source directory
4. Starts a sync session with the correct restart strategy
5. Shows sync count and status in real time
6. On Stop — automatically rolls back the deployment to its pre-sync state

**Load via dashboard:**

The Load button rebuilds a container image without going through GitHub Actions:
1. Discovers local service directories matching the deployment name
2. Runs `docker build` locally
3. Loads the image into Kind via `kind load docker-image`
4. Triggers a rolling update on the deployment

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--port` | `9090` | Port to serve the dashboard on |

**Examples:**

```bash
# Default port
kindling dashboard

# Custom port
kindling dashboard --port 8080
```

---

### `kindling secrets`

Manage external credentials (API keys, tokens, DSNs) as Kubernetes
Secrets in the Kind cluster.

```
kindling secrets <subcommand> [flags]
```

**Subcommands:**

| Subcommand | Description |
|---|---|
| `set <name> <value>` | Create or update a K8s Secret and back it up locally |
| `list` | List all kindling-managed secrets (names only) |
| `delete <name>` | Remove from cluster and local backup |
| `restore` | Re-create all secrets from `.kindling/secrets.yaml` after a cluster rebuild |

**How it works:**

- Each secret is stored as a K8s Secret named `kindling-secret-<lowercase-name>` with the label `app.kubernetes.io/managed-by=kindling`
- A local backup is maintained at `.kindling/secrets.yaml` (base64-encoded values, auto-gitignored)
- `kindling secrets restore` reads the backup and re-creates all secrets — run this after `kindling init` to restore credentials from a previous cluster

**Examples:**

```bash
# Store a Stripe API key
kindling secrets set STRIPE_KEY sk_live_abc123

# Store a database connection string
kindling secrets set DATABASE_URL postgres://user:pass@host/db

# List all managed secrets
kindling secrets list

# Remove a secret
kindling secrets delete STRIPE_KEY

# After cluster rebuild, restore all secrets
kindling init
kindling secrets restore
```

---

### `kindling expose`

Create a public HTTPS tunnel to the Kind cluster’s ingress controller
for OAuth/OIDC callbacks.

```
kindling expose [flags]
```

**What it does:**
1. Detects an available tunnel provider (cloudflared or ngrok)
2. Verifies the Kind cluster is running
3. Starts a tunnel from a public HTTPS URL to `localhost:<port>`
4. Auto-patches the active ingress with the tunnel hostname
5. Saves the original ingress host (and TLS config if present) as annotations for later restoration
6. Saves tunnel state to a ConfigMap (`kindling-tunnel`) in the cluster
7. Runs in the background — the CLI returns immediately

**TLS handling:** If the ingress has `spec.tls` (e.g. cert-manager), the tunnel
automatically saves and strips it (cloudflared handles TLS at the edge). On
`--stop`, the original TLS config is restored.

**Self-healing:** On each start, restores any ingresses left with stale tunnel
hostnames from a previous tunnel that died without cleanup.

**Supported providers:**

| Provider | Account required | Install |
|---|---|---|
| cloudflared | No (quick tunnels are free) | [Download](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/) |
| ngrok | Yes (free tier available) | [Download](https://ngrok.com/download) |

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--provider` | auto-detect | Tunnel provider: `cloudflared` or `ngrok` |
| `--port` | `80` | Local port to expose (default: ingress controller) |
| `--stop` | `false` | Stop a running tunnel and restore original ingress configuration |
| `--service` | — | Ingress name to route tunnel traffic to (default: first ingress found) |

**Examples:**

```bash
# Auto-detect provider, expose port 80
kindling expose

# Use cloudflared explicitly
kindling expose --provider cloudflared

# Route tunnel to a specific ingress
kindling expose --service ui-ingress

# Stop the tunnel and restore ingresses
kindling expose --stop

# Expose a different port
kindling expose --port 443
```

---

### `kindling env`

Manage environment variables on running deployments without redeploying.

```
kindling env <subcommand> <deployment> [args]
```

**Subcommands:**

| Subcommand | Description |
|---|---|
| `set <deployment> KEY=VALUE [...]` | Set one or more environment variables |
| `list <deployment>` | List all environment variables on a deployment |
| `unset <deployment> KEY [...]` | Remove one or more environment variables |

**How it works:**

- Uses `kubectl set env` under the hood
- Triggers a rolling restart automatically so new values take effect immediately
- No need to rebuild or push — changes are live in seconds

**Examples:**

```bash
# Set a single env var
kindling env set myapp-dev DATABASE_PORT=5432

# Set multiple at once
kindling env set myapp-dev DATABASE_PORT=5432 REDIS_HOST=redis-svc LOG_LEVEL=debug

# List all env vars on a deployment
kindling env list myapp-dev

# Remove env vars
kindling env unset myapp-dev LOG_LEVEL
```

---

### `kindling reset`

Remove the runner pool so you can point it at a new repo.

```
kindling reset [flags]
```

**What it does:**
1. Deletes all CI runner pool CRs in the cluster
2. Deletes the runner token Secret
3. Leaves the cluster, operator, and DevStagingEnvironments intact

Use this when you want to switch to a different GitHub repository without
tearing down the entire cluster.

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--force` | `-y` | `false` | Skip confirmation prompt |

**Examples:**

```bash
# Interactive confirmation
kindling reset

# Skip confirmation
kindling reset -y

# Then re-point at a new repo
kindling runners -u myuser -r neworg/newrepo -t ghp_xxxxx
```

---

### `kindling destroy`

Delete the Kind cluster and all resources.

```
kindling destroy [flags]
```

**What it does:**
1. Stops any running tunnel (restores ingresses)
2. Checks cluster exists
3. Prompts for confirmation (type cluster name)
4. `kind delete cluster --name <cluster>`

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--force` | `-y` | `false` | Skip confirmation prompt |

**Examples:**

```bash
# Interactive confirmation
kindling destroy

# Skip confirmation
kindling destroy -y

# Destroy a named cluster
kindling destroy -c staging -y
```

---

### `kindling version`

Print the CLI version.

```
kindling version
```

**Output:**

```
kindling dev (darwin/arm64)
```

The version string is set at build time via `-ldflags`:

```bash
go build -ldflags "-X github.com/jeffvincent/kindling/cli/cmd.Version=v1.0.0" -o bin/kindling ./cli/
```

---

## Typical workflow

```bash
# ── OUTER LOOP SETUP ─────────────────────────────────────

# 1. Bootstrap everything
kindling init

# 2. Restore secrets from a previous cluster (if any)
kindling secrets restore

# 3. Connect a GitHub repo
kindling runners -u alice -r acme/myapp -t ghp_xxxxx

# 4. Generate a workflow for your app
kindling generate -k sk-... -r /path/to/myapp

# 5. Set any external credentials that were detected
kindling secrets set STRIPE_KEY sk_live_...

# 6. Push code → outer loop builds + deploys automatically
git push origin main

# 7. Check status
kindling status

# ── INNER LOOP (fast iteration) ──────────────────────────

# 8. Open the dashboard for visual control
kindling dashboard

# 9. Start live sync from the CLI (or click Sync in the dashboard)
kindling sync -d alice-myapp --restart

# 10. Edit files → changes appear instantly
#     Stop sync (Ctrl+C) → deployment rolls back automatically

# 11. Set env vars without redeploying
kindling env set alice-myapp DATABASE_PORT=5432

# ── MANAGEMENT ───────────────────────────────────────────

# 12. Re-point runners at a different repo
kindling reset
kindling runners -u alice -r acme/other-app -t ghp_xxxxx

# 13. Manual deploy (without CI)
kindling deploy -f dev-environment.yaml

# 14. Tear down when done
kindling destroy -y
```
