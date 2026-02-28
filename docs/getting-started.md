# Getting Started

This guide walks you through both development loops: the **outer loop**
(CI — push code, build containers, deploy environments) and the **inner
loop** (live sync — edit files, see changes instantly).

---

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| [Docker](https://docs.docker.com/get-docker/) | 20.10+ | Container runtime |
| [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | 0.20+ | Local Kubernetes clusters |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.27+ | Kubernetes CLI |
| [Go](https://go.dev/dl/) | 1.20+ | Building the CLI from source |
| [Make](https://www.gnu.org/software/make/) | Any | Building the CLI from source |

> **Note:** Go and Make are only required if you build the CLI from source
> (Step 2). If you install via Homebrew (`brew install kindling-sh/tap/kindling`),
> you don't need them.

Verify everything is installed:

```bash
kind --version && kubectl version --client && docker info -f '{{.ServerVersion}}'
```

---

## Step 1 — Clone the repo

```bash
git clone https://github.com/kindling-sh/kindling.git
cd kindling
```

---

## Step 2 — Build the CLI

```bash
make cli
```

This produces `bin/kindling`. Add it to your PATH:

```bash
sudo cp bin/kindling /usr/local/bin/
```

---

## Step 3 — Bootstrap the cluster

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

---

## Step 4 — Register a CI runner

kindling supports **GitHub Actions** and **GitLab CI**. You need a personal access token for your platform.

### GitHub Actions

You need a [GitHub PAT](https://github.com/settings/tokens) with the **repo** scope.

```bash
kindling runners -u <your-github-username> -r <owner/repo> -t <your-pat>
```

### GitLab CI

You need a [GitLab runner registration token](https://docs.gitlab.com/ee/ci/runners/) for your project.

```bash
kindling runners --ci-provider gitlab -u <your-gitlab-username> -r <group/project> -t <your-token>
```

Verify the runner is registered:

```bash
kubectl get cirunnerpools
kubectl get pods -l app.kubernetes.io/managed-by=cirunnerpool-operator
```

You should also see it listed under your repo's **Settings → Actions → Runners**.

---

## Step 5 — Create your app workflow

> **⚠️ Dockerfile required:** Your app must have a working Dockerfile that
> builds successfully on its own (`docker build .`). Kaniko runs it as-is.

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

## Step 5b — Set external credentials (if detected)

If `kindling generate` detected external credentials:

```bash
kindling secrets set STRIPE_KEY sk_live_abc123
kindling secrets set OPENAI_API_KEY sk-...
```

---

## Step 6 — Push and deploy (outer loop)

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

**This is the outer loop** — every `git push` triggers a full CI build + deploy.

---

## Step 7 — Launch the dashboard

```bash
kindling dashboard
```

Open `http://localhost:9090` in your browser. You'll see:

- All deployed environments with status, images, and replicas
- Runtime detection badges for each service (Node.js, Python, Go, etc.)
- **Sync** and **Load** buttons on every service
- Pod status, logs, events, services, and ingresses

The dashboard is your visual control plane for the inner loop.

---

## Step 8 — Start the inner loop (live sync)

Now for the fast part. Instead of pushing code and waiting for CI, sync
your changes directly into the running container:

### From the CLI

```bash
# Watch for file changes and auto-restart (strategy auto-detected)
kindling sync -d myapp-dev --restart

# One-shot sync + restart
kindling sync -d myapp-dev --restart --once

# Go service — cross-compiles locally, syncs the binary
kindling sync -d my-gateway --restart --language go
```

### From the dashboard

Click the **Sync** button on any service. The dashboard will:

1. Auto-detect the runtime from the container's process
2. Prompt you for the local source directory
3. Start the sync session with real-time status updates
4. Show the sync count as files are synced

Click **Stop** to end the session — the deployment automatically rolls
back to its pre-sync state.

### How it works

`kindling sync` auto-detects the runtime and chooses the right strategy:

| Runtime | Strategy | What happens |
|---|---|---|
| Node.js, Python, Ruby | Wrapper + kill | Patches deployment with restart loop, syncs files, kills child process |
| uvicorn, Nginx, Puma | Signal reload | Sends SIGHUP for zero-downtime reload |
| Go, Rust, Java | Local build + sync | Cross-compiles locally, syncs binary, restarts |
| React/Vue (nginx) | Frontend build | Builds locally, syncs dist/ into nginx html root |
| PHP, nodemon | Auto-reload | Just syncs files — runtime picks them up |

### Automatic rollback

When you stop a sync session (Ctrl+C or via the dashboard):

- If the deployment was patched (wrapper injected), kindling performs a
  `rollout undo` to the saved revision — removing the wrapper entirely
- If only files were synced (signal-reload servers), it performs a
  `rollout restart` for a fresh pod with the original image
- Either way, the container returns to exactly the state it was in
  before sync started

No manual cleanup. No stale processes. Clean rollback every time.

---

## Step 9 — Load without CI

For larger changes that need a full image rebuild but don't warrant a
`git push`, use **Load** from the dashboard:

1. Click **Load** on a service
2. Kindling runs `docker build` locally, loads the image into Kind, and
   triggers a rolling update
3. No GitHub Actions involved — it's a local build + deploy

This is useful when you've changed a Dockerfile, added dependencies, or
want to test a full rebuild without going through CI.

---

## Iterate

The two loops work together. Use the **inner loop** for rapid iteration
on code changes, and the **outer loop** when you're ready to commit:

```
 edit → sync → verify → edit → sync → verify → ... → commit → push → CI
  ▲                                                                     │
  └─────────────────── next feature ◄───────────────────────────────────┘
```

### Other useful commands while iterating

```bash
# Check status — includes crash diagnostics for unhealthy pods
kindling status

# Set env vars on a running deployment without redeploying
kindling env set myapp-dev DATABASE_PORT=5432

# List env vars
kindling env list myapp-dev

# Start a public HTTPS tunnel for OAuth callbacks
kindling expose

# Stop the tunnel
kindling expose --stop

# Switch to a different GitHub repo without destroying the cluster
kindling reset
kindling runners -u myuser -r neworg/newrepo -t ghp_xxxxx
```

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

# Then start the inner loop
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
- [Architecture](architecture.md) — how both loops work under the hood
- [Secrets Management](secrets.md) — managing credentials across cluster rebuilds
- [OAuth & Tunnels](oauth-tunnels.md) — public HTTPS for OAuth callbacks
- [Dependency Reference](dependencies.md) — all 15 dependency types
- [CRD Reference](crd-reference.md) — full DevStagingEnvironment and CIRunnerPool specs
- [GitHub Actions Reference](github-actions.md) — reusable action docs
