# CLI Reference

The `kindling` CLI bootstraps and manages a local Kind cluster running
the kindling operator. It wraps `kind`, `kubectl`, and `make` into a
streamlined developer workflow.

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
1. Preflight checks (kind, kubectl, docker, make, go on PATH)
2. `kind create cluster --name dev --config kind-config.yaml`
3. Switch kubectl context to `kind-dev`
4. Run `setup-ingress.sh` (installs ingress-nginx + in-cluster registry)
5. `make docker-build IMG=controller:latest`
6. `kind load docker-image controller:latest --name dev`
7. `make install` (install CRDs)
8. `make deploy IMG=controller:latest`
9. Wait for controller-manager rollout

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--skip-cluster` | `false` | Skip Kind cluster creation (use existing cluster) |
| `--image` | — | Node Docker image for Kind (e.g. `kindest/node:v1.29.0`) |
| `--kubeconfig` | — | Path to write kubeconfig instead of default location |
| `--wait` | — | Wait for control plane to be ready (e.g. `60s`, `5m`) |
| `--retain` | `false` | Retain cluster nodes for debugging on creation failure |

**Examples:**

```bash
# Default: create "dev" cluster
kindling init

# Use a specific Kubernetes version
kindling init --image kindest/node:v1.29.0

# Custom cluster name with debug retention
kindling init -c staging --retain --wait 5m

# Skip cluster creation, just deploy operator into existing cluster
kindling init --skip-cluster
```

---

### `kindling runners`

Create a GitHub Actions runner pool in the cluster.

```
kindling runners [flags]
```

**What it does:**
1. Prompts for any missing values (username, repo, PAT)
2. Creates `github-runner-token` Secret from the PAT
3. Applies a `GithubActionRunnerPool` CR
4. Waits for the runner pod to become ready
5. Verifies runner registers with GitHub

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--username` | `-u` | — | GitHub username |
| `--repo` | `-r` | — | GitHub repository (`owner/repo`) |
| `--token` | `-t` | — | GitHub Personal Access Token |

**Examples:**

```bash
# Interactive mode (prompts for missing values)
kindling runners

# Non-interactive
kindling runners -u myuser -r myorg/myrepo -t ghp_xxxxx
```

---

### `kindling generate`

AI-generate a GitHub Actions workflow for any repository.

```
kindling generate [flags]
```

**What it does:**
1. Scans the repository for Dockerfiles, dependency manifests, and source files
2. Detects services, languages, ports, health-check endpoints, and backing dependencies
3. Builds a detailed prompt and calls the AI provider (OpenAI or Anthropic)
4. Writes a complete `dev-deploy.yml` workflow using `kindling-build` and `kindling-deploy` actions

**Supported languages:** Go, TypeScript, Python, Java, Rust, Ruby, PHP, C#, Elixir

**Flags:**

| Flag | Short | Default | Description |
|---|---|---|---|
| `--api-key` | `-k` | — (required) | GenAI API key |
| `--repo-path` | `-r` | `.` | Path to the local repository to analyze |
| `--provider` | | `openai` | AI provider: `openai` or `anthropic` |
| `--model` | | auto | Model name (default: `gpt-4o` for openai, `claude-sonnet-4-20250514` for anthropic) |
| `--output` | `-o` | `<repo>/.github/workflows/dev-deploy.yml` | Output path for the workflow file |
| `--dry-run` | | `false` | Print the generated workflow to stdout instead of writing a file |

**Examples:**

```bash
# Generate with OpenAI (default)
kindling generate -k sk-... -r /path/to/my-app

# Use Anthropic
kindling generate -k sk-ant-... -r . --provider anthropic

# Preview without writing
kindling generate -k sk-... -r . --dry-run

# Custom output path
kindling generate -k sk-... -r . -o ./my-workflow.yml
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
- **Runner Pools** — GithubActionRunnerPool CRs (name, username, repo)
- **Dev Environments** — DevStagingEnvironment CRs (image, replicas,
  ready state)
- **Pods** — All pods in the default namespace with status and age

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

▸ GitHub Actions Runner Pools
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

### `kindling destroy`

Delete the Kind cluster and all resources.

```
kindling destroy [flags]
```

**What it does:**
1. Checks cluster exists
2. Prompts for confirmation (type cluster name)
3. `kind delete cluster --name <cluster>`

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
# 1. Bootstrap everything
kindling init

# 2. Connect a GitHub repo
kindling runners -u alice -r acme/myapp -t ghp_xxxxx

# 3. Generate a workflow for your app
kindling generate -k sk-... -r /path/to/myapp

# 4. Push code → CI builds + deploys automatically
git push origin main

# 5. Check status
kindling status

# 6. View controller logs
kindling logs

# 7. Manual deploy (without CI)
kindling deploy -f dev-environment.yaml

# 8. Tear down when done
kindling destroy -y
```
