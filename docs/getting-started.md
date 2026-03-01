# Getting Started

This guide walks you through the full kindling journey: **analyze** your
project, **generate** a CI workflow, enter the **dev loop**, and eventually
**graduate to production**.

```
analyze → generate → dev loop → promote
   ↓          ↓          ↓          ↓
 readiness  workflow   push/sync  production
 check      via AI     iterate    (coming soon)
```

---

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| [Docker](https://docs.docker.com/get-docker/) | 24+ | Container runtime |
| [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | 0.20+ | Local Kubernetes clusters |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.28+ | Kubernetes CLI |
| [Go](https://go.dev/dl/) | 1.20+ | Building the CLI from source |
| [Make](https://www.gnu.org/software/make/) | Any | Building the CLI from source |

> **Note:** Go and Make are only required if you build the CLI from source.
> If you install via Homebrew (`brew install kindling-sh/tap/kindling`),
> you don't need them.

Verify everything is installed:

```bash
kind --version && kubectl version --client && docker info -f '{{.ServerVersion}}'
```

---

## Phase 0 — Setup

### Install the CLI

```bash
brew install kindling-sh/tap/kindling
```

Or build from source:

```bash
git clone https://github.com/kindling-sh/kindling.git
cd kindling && make cli
sudo cp bin/kindling /usr/local/bin/
```

### Bootstrap the cluster

**Before you begin**, allocate enough Docker Desktop resources
(Settings → Resources):

| Workload | CPUs | Memory | Disk |
|---|---|---|---|
| Small (1–3 lightweight services) | 4 | 8 GB | 30 GB |
| Medium (4–6 services, mixed languages) | 6 | 12 GB | 50 GB |
| Large (7+ services, heavy compilers) | 8+ | 16 GB | 80 GB |

```bash
kindling init
```

This creates a Kind cluster named `dev` with:
- In-cluster container registry (`registry:5000`)
- ingress-nginx controller (routes `*.localhost` → your apps)
- kindling operator (watches for DevStagingEnvironment CRs)

### Register a CI runner

kindling supports **GitHub Actions** and **GitLab CI**. You need a personal
access token for your platform.

#### GitHub Actions

You need a [GitHub PAT](https://github.com/settings/tokens) with the **repo** scope.

```bash
kindling runners -u <your-github-username> -r <owner/repo> -t <your-pat>
```

#### GitLab CI

You need a [GitLab runner registration token](https://docs.gitlab.com/ee/ci/runners/) for your project.

```bash
kindling runners --ci-provider gitlab -u <your-gitlab-username> -r <group/project> -t <your-token>
```

---

## Phase 1 — Analyze

Before generating a workflow, let kindling check your project's readiness:

```bash
kindling analyze
```

**What it checks:**
- **Dockerfiles** — found, valid, Kaniko-compatible (no BuildKit-only features)
- **Dependencies** — Postgres, Redis, MongoDB, Kafka, and 11 more detected from source
- **Secrets** — external credentials (`*_API_KEY`, `*_SECRET`, `*_TOKEN`) your app references
- **Agent architecture** — LangChain, CrewAI, LangGraph, OpenAI Agents SDK, MCP servers
- **Inter-service communication** — HTTP calls between your services
- **Build context** — verifies COPY/ADD paths align with the Dockerfile's build context

**Example output:**

```
📁 Repository: /path/to/your-app
──────────────────────────────

🐳 Dockerfiles
  ✅ ./Dockerfile (Kaniko-compatible)
  ✅ ./agent/Dockerfile (Kaniko-compatible)

📦 Dependencies
  • postgres 16
  • redis (latest)

🔑 Secrets
  ⚠️  OPENAI_API_KEY — run: kindling secrets set OPENAI_API_KEY <value>
  ⚠️  STRIPE_KEY — run: kindling secrets set STRIPE_KEY <value>

🤖 Agent Frameworks
  • LangChain detected in agent/

✅ Ready for 'kindling generate'
```

Fix any issues it flags before moving on. For example, if it detects
missing secrets, set them now:

```bash
kindling secrets set OPENAI_API_KEY sk-...
kindling secrets set STRIPE_KEY sk_live_...
```

> **Coming soon:** `kindling scaffold` will generate Dockerfiles and project
> structure for new projects that don't have them yet.

---

## Phase 2 — Generate

> **⚠️ Dockerfile required:** Your app must have a working Dockerfile.
> Kaniko builds it as-is inside the cluster.

### Option A: AI-generate the workflow (recommended)

```bash
kindling generate -k <your-api-key> -r /path/to/your-app
```

This detects all services, languages, dependencies, ports, and
health-check endpoints, then writes `.github/workflows/dev-deploy.yml`.

```bash
# Use Anthropic instead of OpenAI
kindling generate -k sk-ant-... -r . --ai-provider anthropic

# Preview without writing a file
kindling generate -k sk-... -r . --dry-run
```

### Option B: Write the workflow manually

```yaml
name: Dev Deploy
on:
  push:
    branches: [main]

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]
    env:
      TAG: ${{ github.sha }}
    steps:
      - uses: actions/checkout@v4

      - name: Clean builds directory
        run: rm -f /builds/*

      - name: Build image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: my-app
          context: ${{ github.workspace }}
          image: "registry:5000/my-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-app"
          image: "registry:5000/my-app:${{ env.TAG }}"
          port: "8080"
          ingress-host: "${{ github.actor }}-my-app.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis
```

---

## Phase 3 — Dev Loop

The dev loop has two speeds:

### Outer loop: push → build → deploy

```bash
git add -A && git commit -m "add kindling workflow" && git push
```

The runner picks up the job, builds via Kaniko, and deploys. Watch progress:

```bash
kindling status
kindling logs
```

Access your app:

```bash
curl http://<your-username>-my-app.localhost/
```

Every `git push` triggers a full CI build + deploy.

### Inner loop: edit → sync → reload

For rapid iteration without waiting for CI:

```bash
# Watch for file changes and auto-restart (strategy auto-detected)
kindling sync -d myapp-dev --restart

# One-shot sync + restart
kindling sync -d myapp-dev --restart --once

# Go service — cross-compiles locally, syncs the binary
kindling sync -d my-gateway --restart --language go
```

`kindling sync` auto-detects the runtime and chooses the right strategy:

| Runtime | Strategy | What happens |
|---|---|---|
| Node.js, Python, Ruby | Wrapper + kill | Patches deployment with restart loop, syncs files, kills child process |
| uvicorn, Nginx, Puma | Signal reload | Sends SIGHUP for zero-downtime reload |
| Go, Rust, Java | Local build + sync | Cross-compiles locally, syncs binary, restarts |
| React/Vue (nginx) | Frontend build | Builds locally, syncs dist/ into nginx html root |
| PHP, nodemon | Auto-reload | Just syncs files — runtime picks them up |

### Automatic rollback

When you stop sync (Ctrl+C):
- If the deployment was patched (wrapper injected), kindling performs a
  `rollout undo` — removing the wrapper entirely
- If only files were synced, it performs a `rollout restart` for a fresh pod
- Either way, the container returns to exactly the state it was in before sync

### The dashboard

```bash
kindling dashboard
```

Open `http://localhost:9090` for a visual control plane:
- **Sync** and **Load** buttons on every service
- Pod status, logs, events, services, and ingresses
- Runtime detection badges (Node.js, Python, Go, etc.)
- Secrets, env vars, and scaling controls

### Public URLs for OAuth

```bash
kindling expose
```

Creates an HTTPS tunnel via cloudflared or ngrok. Auto-patches your ingress
with the tunnel hostname. Stop with `kindling expose --stop`.

### Other useful commands while iterating

```bash
# Set env vars without redeploying
kindling env set myapp-dev DATABASE_PORT=5432

# List env vars
kindling env list myapp-dev

# Rebuild an image locally (skip CI)
kindling load -s my-app --context .

# One-service rebuild via CI
kindling push -s my-app

# Switch repos without destroying the cluster
kindling reset
kindling runners -u myuser -r neworg/newrepo -t ghp_xxxxx
```

---

## Phase 4 — Graduate to Production *(coming soon)*

> `kindling promote` will graduate your staging environment to a production
> cluster with TLS, DNS, and real infrastructure. This feature is on the
> roadmap for an upcoming release.

---

## Manual deploy (without GitHub Actions)

If you want to deploy without CI:

```bash
# Build the image
docker build -t my-app:dev .
kind load docker-image my-app:dev --name dev

# Create a dev-environment.yaml
cat > dev-environment.yaml <<EOF
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: my-app-dev
spec:
  deployment:
    image: my-app:dev
    port: 8080
    healthCheck:
      path: /healthz
  service:
    port: 8080
  ingress:
    enabled: true
    host: my-app.localhost
    ingressClassName: nginx
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
EOF

# Deploy
kindling deploy -f dev-environment.yaml

# Then start the dev loop
kindling sync -d my-app-dev --restart
```

---

## Cleaning up

Remove a single environment:

```bash
kubectl delete devstagingenvironment my-app-dev
```

Tear down everything:

```bash
kindling destroy -y
```

---

## Next steps

- [CLI Reference](cli.md) — all commands and flags
- [Architecture](architecture.md) — how the operator works under the hood
- [Secrets Management](secrets.md) — managing credentials across cluster rebuilds
- [OAuth & Tunnels](oauth-tunnels.md) — public HTTPS for OAuth callbacks
- [Dependency Reference](dependencies.md) — all 15 dependency types
- [CRD Reference](crd-reference.md) — full DevStagingEnvironment and CIRunnerPool specs
- [GitHub Actions Reference](github-actions.md) — reusable action docs
