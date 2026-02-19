# Kindling — Session Context

## Project Overview

Kindling is a local Kubernetes CI/CD operator that bootstraps a Kind cluster on a
developer's laptop with self-hosted GitHub Actions runners, an in-cluster container
registry, and a Kubernetes operator that reconciles custom resources to deploy
applications with auto-provisioned dependencies.

**Repo**: `github.com/jeff-vincent/kindling`
**Language**: Go 1.25.7
**Framework**: controller-runtime (kubebuilder) for the operator, cobra for the CLI

## Build Commands

```bash
# CLI build (must set Go path — homebrew install)
cd /Users/jeffvincent/kindling/cli
export PATH="/opt/homebrew/Cellar/go@1.25/1.25.7_1/bin:$PATH"
export GOROOT="/opt/homebrew/Cellar/go@1.25/1.25.7_1/libexec"
go build -o /usr/local/bin/kindling .

# Operator image build + deploy (from repo root)
make docker-build docker-push IMG=localhost:5001/kindling-controller:latest
make deploy IMG=localhost:5001/kindling-controller:latest
```

## Architecture

### CRDs (api/v1alpha1/)
- **DevStagingEnvironment** — declares an app deployment with image, port, ingress,
  service, and dependencies (postgres, redis, mongodb, elasticsearch, kafka, vault, etc.)
- **GithubActionRunnerPool** — declares a self-hosted runner pool with GitHub username,
  repo, PAT secret ref, labels, and resource requirements

### Operator (internal/controller/)
- **devstagingenvironment_controller.go** — reconciles DSE CRs into Deployment, Service,
  Ingress, and auto-provisioned dependency pods (15 types supported)
- **githubactionrunnerpool_controller.go** — reconciles runner pool CRs into Deployments
  with a build-agent sidecar. Runner pods self-register with GitHub. The sidecar watches
  `/builds/` for build requests and kubectl apply requests, executing Kaniko builds and
  DSE applies on behalf of the workflow.

### CLI (cli/cmd/)
- **init.go** — creates Kind cluster, deploys ingress-nginx, in-cluster registry, builds
  and deploys the operator. Has `--expose` flag to also start a tunnel.
- **runners.go** (formerly quickstart.go) — creates github-runner-token secret and
  GithubActionRunnerPool CR. Prompts interactively for missing values.
- **reset.go** — deletes all runner pools and the token secret without destroying the
  cluster, so you can re-run `runners` for a different repo.
- **deploy.go** — applies a DevStagingEnvironment from a YAML file.
- **generate.go** — AI-powered workflow generation (OpenAI + Anthropic). Scans repo
  structure, detects languages/frameworks/Dockerfiles, generates dev-deploy.yml.
- **expose.go** — public HTTPS tunnel via cloudflared or ngrok. Backgrounds the tunnel,
  saves PID to `.kindling/tunnel.yaml`, creates `kindling-tunnel` ConfigMap in cluster.
  Auto-patches ingress hosts to tunnel hostname, saves/strips TLS blocks, self-heals
  orphaned ingresses on next run. `--service` flag targets a specific ingress.
  `--stop` kills the tunnel and restores original ingress hosts.
- **env.go** — `kindling env set/list/unset` for live env var management on running
  deployments without redeploying.
- **status.go** — dashboard view of cluster, operator, runners, environments, deployments,
  ingress routes. Detects unhealthy pods and shows crash logs inline.
- **logs.go** — tails the operator controller logs.
- **destroy.go** — deletes the Kind cluster. Stops any running tunnel first.
- **helpers.go** — ANSI colors, pretty-print helpers, command execution wrappers
  (run, runSilent, runCapture, runDir), path resolution.

### GitHub Actions (.github/actions/)
- **kindling-build/action.yml** — composite action that tarballs the build context and
  signals the sidecar to run a Kaniko build. Supports `--cache=true` with
  `registry:5000/cache` for layer caching.
- **kindling-deploy/action.yml** — composite action that generates a DSE YAML from inputs
  and signals the sidecar to `kubectl apply` it. Waits for rollout. On failure, fetches
  pod status and last 30 log lines for diagnostics. Has `tunnel: "true"` input that
  reads the `kindling-tunnel` ConfigMap to override the ingress host.

### Sidecar Protocol
The build-agent sidecar in runner pods watches the `/builds/` shared volume:
- `<name>.tar.gz` + `<name>.dest` + `<name>.request` → Kaniko build
- `<name>.yaml` + `<name>.apply` → kubectl apply
- `<name>.sh` + `<name>.kubectl` → arbitrary kubectl command
- Completion signaled via `<name>.done`, `<name>.apply-done`, `<name>.kubectl-done`
- Exit codes in `<name>.exitcode`, logs in `<name>.log`

## Git State

**Main branch**: stable, all features merged except the current feature branch
**Feature branch**: `feature/tls-public-exposure` — contains:
- Background tunnel with PID management (Setpgid, tunnel.yaml)
- `--stop` flag (SIGTERM → SIGKILL, cleanup)
- `--expose` flag on `kindling init`
- Auto-stop tunnel on `kindling destroy`
- ConfigMap `kindling-tunnel` in cluster
- Deploy action `tunnel: "true"` input
- Auto-patch ingresses with tunnel hostname
- Save/strip TLS blocks on patched ingresses
- Self-heal orphaned ingresses on startup
- `--service` flag to target specific ingress
- Restore ingresses on `--stop`
- `kindling reset` command
- `quickstart` renamed to `runners`
- Crash log diagnostics in deploy action and status command
- `kindling env set/list/unset` for live env var management

## Key Constraints

- **Nginx ingress admission webhook**: prevents two ingresses from sharing the same
  host+path. Only one ingress can be patched to the tunnel hostname at a time.
- **GitHub Actions `@main` ref**: composite actions referenced as `@main` won't see
  changes on feature branches. The `tunnel: "true"` input in the deploy action needs
  the feature branch merged to main to work via `@main`.
- **cloudflared tunnel hostname**: changes every time a new tunnel is started (random
  subdomain on `trycloudflare.com`). The CLI patches ingresses and creates a ConfigMap
  so the hostname is discoverable.
- **Annotation `kindling.dev/original-host`**: stored on patched ingresses to enable
  restore. Uses go-template for lookup (jsonpath can't handle dots/slashes in keys).
- **Annotation `kindling.dev/original-tls`**: stores the full TLS JSON block as a string
  so cert-manager / manual TLS config can be restored.

## Examples

- **examples/sample-app/** — single-service Go app with Postgres + Redis dependencies
- **examples/microservices/** — 4-service demo (orders, inventory, gateway, UI) with
  Postgres, Redis, MongoDB. Full dev-deploy.yml with 4 builds + 4 deploys.
- **examples/platform-api/** — 5-dependency demo (Postgres, Redis, Elasticsearch, Kafka,
  Vault) with a React UI

## Roadmap (TODO.md)

- Helm-native deploy detection in `kindling generate`
- Smarter ingress heuristics (detect frontends, API gateways)
- `--ingress-all` flag
- `kindling secrets` subcommand with encrypted local store
- External credential detection during generate
- `.kindling/secrets.yaml` config
- Multi-platform CI support (GitLab, Bitbucket, Gitea, CircleCI, Jenkins, Drone)
- OSS infrastructure (CONTRIBUTING.md, Homebrew tap, docs site)
