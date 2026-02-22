# sample-app

A small Go web server that demonstrates the full **kindling** developer
loop. It connects to Postgres and Redis (auto-provisioned by the operator),
serves a dashboard UI with live dependency status, a Redis-backed visit
counter, and a simple notes feature persisted in Postgres.

The goal is to show the shortest path from `git push` to a working app
with real dependencies, running on your local Kind cluster.

## Architecture

```
 ┌──────────────┐       ┌───────────────┐       ┌──────────┐
 │  sample-app  │──────▶│  PostgreSQL 16 │       │  Redis   │
 │  :8080       │──────▶│  (auto)        │       │  (auto)  │
 └──────┬───────┘       └───────────────┘       └──────────┘
        │
        ▼
  GET /            → Dashboard UI (status, visits, notes)
  GET /healthz     → Liveness probe (JSON)
  GET /api/status  → Postgres + Redis connectivity (JSON)
  GET /api/notes   → Notes list (JSON)
  POST /api/notes  → Add a note (form submit)
```

## Files

```
sample-app/
├── .github/workflows/
│   └── dev-deploy.yml       # GitHub Actions workflow (uses kindling actions)
├── main.go                  # The app — ~100 lines of Go
├── Dockerfile               # Two-stage build (golang:1.25 → alpine:3.19)
├── go.mod
├── dev-environment.yaml     # DevStagingEnvironment CR (for manual deploy)
└── README.md                # ← you are here
```

## Quick-start

### Prerequisites

- Local Kind cluster with **kindling** operator deployed ([Getting Started](../../README.md#getting-started))
- `GithubActionRunnerPool` CR applied with your GitHub username

### Option A — Push to GitHub (CI flow)

Copy this example into a repo that your runner pool targets:

```bash
mkdir my-app && cd my-app && git init
cp -r /path/to/kindling/examples/sample-app/* .
cp -r /path/to/kindling/examples/sample-app/.github .

git remote add origin git@github.com:you/my-app.git
git add -A && git commit -m "initial commit" && git push -u origin main
```

The included workflow uses the **reusable kindling actions** — no raw
signal-file scripting:

```yaml
# .github/workflows/dev-deploy.yml (simplified)
steps:
  - uses: actions/checkout@v4

  - name: Clean builds directory
    run: rm -f /builds/*

  - name: Build image
    uses: kindling-sh/kindling/.github/actions/kindling-build@main
    with:
      name: sample-app
      context: ${{ github.workspace }}
      image: "registry:5000/sample-app:${{ env.TAG }}"

  - name: Deploy
    uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
    with:
      name: "${{ github.actor }}-sample-app"
      image: "registry:5000/sample-app:${{ env.TAG }}"
      port: "8080"
      ingress-host: "${{ github.actor }}-sample-app.localhost"
      dependencies: |
        - type: postgres
          version: "16"
        - type: redis
```

### Option B — Deploy manually (no GitHub)

```bash
docker build -t sample-app:dev examples/sample-app/
kind load docker-image sample-app:dev --name dev
kubectl apply -f examples/sample-app/dev-environment.yaml
kubectl rollout status deployment/sample-app-dev --timeout=120s
```

### Try it out

Open in a browser for the full dashboard:

```
http://<username>-sample-app.localhost/
```

Or hit the JSON APIs:

```bash
curl http://<username>-sample-app.localhost/healthz
curl http://<username>-sample-app.localhost/api/status | jq .
```

The dashboard shows:
- **Postgres / Redis status** — green dots when connected
- **Visit counter** — incremented in Redis on every page load
- **Notes** — add notes via the form, persisted in Postgres

## What the operator creates

| Resource | Description |
|---|---|
| **Deployment** | Your app container with health checks |
| **Service** (ClusterIP) | Internal routing |
| **Ingress** | `<user>-sample-app.localhost` → your app |
| **Postgres 16** | Pod + Service, `DATABASE_URL` injected |
| **Redis** | Pod + Service, `REDIS_URL` injected |

You write zero infrastructure YAML for the backing services — just
declare `dependencies: [{type: postgres}, {type: redis}]` and the
operator handles the rest.

## Cleaning up

```bash
kubectl delete devstagingenvironment sample-app-dev
```

The operator garbage-collects all owned resources automatically.
