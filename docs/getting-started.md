# Getting Started

This guide walks you through setting up kindling from scratch and
deploying your first application with auto-provisioned dependencies.

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| [Docker](https://docs.docker.com/get-docker/) | 20.10+ | Container runtime |
| [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | 0.20+ | Local Kubernetes clusters |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.27+ | Kubernetes CLI |
| [Go](https://go.dev/dl/) | 1.20+ | Building the operator and CLI |
| [Make](https://www.gnu.org/software/make/) | Any | Build automation |

Verify everything is installed:

```bash
kind --version && kubectl version --client && docker info -f '{{.ServerVersion}}' && go version && make --version | head -1
```

---

## Step 1 — Clone the repo

```bash
git clone https://github.com/jeff-vincent/kindling.git
cd kindling
```

---

## Step 2 — Build the CLI

```bash
make cli
```

This produces `bin/kindling`. Optionally add it to your PATH:

```bash
sudo cp bin/kindling /usr/local/bin/
```

---

## Step 3 — Bootstrap the cluster

**Before you begin**, make sure Docker Desktop has enough resources allocated
(Settings → Resources):

| Workload | CPUs | Memory | Disk |
|---|---|---|---|
| Small (1–3 lightweight services) | 4 | 8 GB | 30 GB |
| Medium (4–6 services, mixed languages) | 6 | 12 GB | 50 GB |
| Large (7+ services, heavy compilers like Rust/Java/C#) | 8+ | 16 GB | 80 GB |

> **Tip:** Kaniko layer caching means first builds are slow but rebuilds are
> fast. Allocate enough disk for cached layers (2–5 GB per heavy-compiler service).

```bash
kindling init
```

This creates a Kind cluster named `dev` with:
- In-cluster container registry (`registry:5000`)
- ingress-nginx controller (routes `*.localhost` → your apps)
- kindling operator (watches for DevStagingEnvironment CRs)

Expected output:

```
▸ Preflight checks
  ✓  kind found
  ✓  kubectl found
  ✓  docker found
  ✓  make found
  ✓  go found

▸ Creating Kind cluster
  🔧  kind create cluster --name dev --config kind-config.yaml
  ✅ Kind cluster created

▸ Installing ingress-nginx + in-cluster registry
  ✅ Ingress and registry ready

▸ Building kindling operator image
  🏗️  make docker-build IMG=controller:latest
  ✅ Operator image built

▸ Installing CRDs + deploying operator
  ✅ Controller is running

  🎉 kindling is ready!
```

Verify:

```bash
kindling status
```

---

## Step 4 — Register a GitHub Actions runner

You need a GitHub Personal Access Token (PAT) with the `repo` scope.

```bash
kindling runners -u <your-github-username> -r <owner/repo> -t <your-pat>
```

This creates:
1. A Kubernetes Secret with your PAT
2. A `GithubActionRunnerPool` CR
3. A runner pod that registers with GitHub

Verify the runner is registered:

```bash
kubectl get githubactionrunnerpools
kubectl get pods -l app.kubernetes.io/managed-by=githubactionrunnerpool-operator
```

You should also see it listed under your repo's **Settings → Actions → Runners**.

---

## Step 5 — Create your app workflow

> **⚠️ Dockerfile required:** Your app must have a working Dockerfile that builds successfully on its own (e.g. `docker build .`). The `kindling-build` action runs this Dockerfile as-is via Kaniko inside the cluster — it does **not** generate or modify Dockerfiles. If it doesn't build locally, it won't build in kindling.

### Option A: AI-generate the workflow (recommended)

Use `kindling generate` to scan your repo and produce a complete workflow automatically:

```bash
kindling generate -k <your-api-key> -r /path/to/your-app
```

This detects all services, languages, dependencies, ports, and health-check
endpoints, then writes `.github/workflows/dev-deploy.yml` with correct build
steps, deploy steps, timeouts, and inter-service wiring. Supports OpenAI
(default) and Anthropic providers.

```bash
# Use Anthropic instead
kindling generate -k sk-ant-... -r . --provider anthropic

# Preview without writing a file
kindling generate -k sk-... -r . --dry-run
```

### Option B: Write the workflow manually

In your app repository, create `.github/workflows/dev-deploy.yml`:

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
        uses: jeff-vincent/kindling/.github/actions/kindling-build@main
        with:
          name: my-app
          context: ${{ github.workspace }}
          image: "registry:5000/my-app:${{ env.TAG }}"

      - name: Deploy
        uses: jeff-vincent/kindling/.github/actions/kindling-deploy@main
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

## Step 6 — Push and watch

```bash
git add -A && git commit -m "add kindling workflow" && git push
```

The runner picks up the job, builds your image via Kaniko, pushes to
`registry:5000`, and applies the DevStagingEnvironment CR. The operator
provisions Postgres and Redis automatically.

Watch progress:

```bash
kindling status
kindling logs
```

---

## Step 7 — Access your app

```bash
curl http://<your-username>-my-app.localhost/
curl http://<your-username>-my-app.localhost/healthz
```

---

## Step 8 — Iterate

Every `git push` triggers a new build + deploy. The operator updates the
Deployment in-place, and Kubernetes rolls out the new image with zero
downtime.

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

- [Dependency Reference](dependencies.md) — all 15 dependency types with
  code examples
- [CRD Reference](crd-reference.md) — full spec for DevStagingEnvironment
  and GithubActionRunnerPool
- [CLI Reference](cli.md) — all commands and flags
- [GitHub Actions Reference](github-actions.md) — reusable action docs
- [Architecture](architecture.md) — how it all works under the hood
