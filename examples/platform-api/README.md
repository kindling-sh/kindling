# platform-api

A Go web server + React dashboard that demonstrates **kindling** with
five backing services — all auto-provisioned by the operator. The API
connects to PostgreSQL, Redis, Elasticsearch, Kafka, and Vault; the
dashboard gives you a real-time view of every service's health.

```
                                ┌───────────────┐
                           ┌───▶│ PostgreSQL 16 │
                           │    └───────────────┘
                           │    ┌───────────────┐
                           ├───▶│  Redis        │
 ┌──────────────┐          │    └───────────────┘
 │ platform-ui  │  /api/*  │    ┌───────────────┐
 │ (React+nginx)├─────────▶├───▶│ Elasticsearch │
 │  :80         │          │    └───────────────┘
 └──────────────┘   ┌──────┴──┐ ┌───────────────┐
                    │platform- ├▶│  Kafka        │
                    │api :8080 │ └───────────────┘
                    └──────┬──┘ ┌───────────────┐
                           └───▶│  Vault (dev)  │
                                └───────────────┘
```

## What you get

| URL | Description |
|---|---|
| `http://platform-ui.localhost` | 🖥️ **Dashboard** — live status of all 5 services |
| `http://platform-api.localhost/` | API hello message |
| `http://platform-api.localhost/healthz` | Liveness probe |
| `http://platform-api.localhost/status` | Raw JSON — pings all 5 deps |

The dashboard auto-refreshes every 5 seconds and shows:
- Status cards for each service with connection details
- Per-service uptime sparklines
- Event log tracking connect/disconnect state changes
- Quick links to API endpoints

## Files

```
platform-api/
├── main.go                        # Go API (~210 lines)
├── Dockerfile                     # Two-stage build (golang → alpine)
├── go.mod / go.sum                # Go dependencies
├── dev-environment.yaml           # 2 DevStagingEnvironment CRs
├── .github/workflows/
│   └── dev-deploy.yml             # GitHub Actions workflow
├── ui/
│   ├── src/
│   │   ├── App.tsx                # React dashboard component
│   │   ├── App.css                # Dark theme styles
│   │   ├── main.tsx               # Entry point
│   │   └── vite-env.d.ts
│   ├── public/favicon.svg
│   ├── index.html
│   ├── Dockerfile                 # node:20 → nginx:1.25
│   ├── nginx.conf.template        # Proxies /api/* → platform-api
│   ├── entrypoint.sh              # envsubst for API_URL
│   ├── package.json
│   ├── tsconfig.json
│   └── vite.config.ts
└── README.md                      # ← you are here
```

## Quick-start

### Prerequisites

- Local Kind cluster created with `kind-config.yaml`
- **kindling** operator deployed ([Getting Started](../../README.md#getting-started))
- `setup-ingress.sh` run (deploys registry + ingress-nginx)

### Option A — Push to GitHub (CI flow)

If you have a `GithubActionRunnerPool` running, copy this example into
your target repo. The included `.github/workflows/dev-deploy.yml` will:

1. Build the API image via Kaniko
2. Build the UI image via Kaniko
3. Push both to `registry:5000`
4. Apply two `DevStagingEnvironment` CRs (API + UI)
5. Wait for rollout
6. Smoke-test both `/healthz` and the dashboard

Just `git push` and the runner handles everything.

### Option B — Deploy manually (no GitHub)

```bash
# 1. Build and load both images into Kind
docker build -t platform-api:dev examples/platform-api/
docker build -t platform-api-ui:dev examples/platform-api/ui/
kind load docker-image platform-api:dev --name dev
kind load docker-image platform-api-ui:dev --name dev

# 2. Apply both DevStagingEnvironment CRs
kubectl apply -f examples/platform-api/dev-environment.yaml

# 3. Wait for rollout (longer timeout — 5 backing services to provision)
kubectl rollout status deployment/platform-api-dev --timeout=180s
kubectl rollout status deployment/platform-api-ui-dev --timeout=120s
```

### Try it out

With ingress-nginx running:

```bash
# Open the dashboard
open http://platform-ui.localhost

# Or hit the API directly
curl http://platform-api.localhost/
curl http://platform-api.localhost/healthz
curl http://platform-api.localhost/status | jq .
```

<details>
<summary><strong>Without Ingress (port-forward fallback)</strong></summary>

```bash
# API
kubectl port-forward svc/platform-api-dev 8080:8080
curl localhost:8080/status | jq .

# UI
kubectl port-forward svc/platform-api-ui-dev 3000:80
open http://localhost:3000
```

</details>

Expected `/status` output:

```json
{
  "app": "platform-api",
  "time": "2026-02-15T12:00:00Z",
  "postgres": { "status": "connected" },
  "redis": { "status": "connected" },
  "elasticsearch": { "status": "connected", "version": "8.12.0" },
  "kafka": { "status": "connected", "brokers": "1" },
  "vault": { "status": "connected", "sealed": "false", "version": "1.15.4" }
}
```

## What the operator creates for you

When you apply the two `DevStagingEnvironment` CRs, the kindling operator
auto-provisions:

| Resource | Description |
|---|---|
| **API Deployment** | Go API container with health checks |
| **API Service** (ClusterIP) | Internal routing to the API |
| **API Ingress** | `platform-api.localhost` → API |
| **UI Deployment** | nginx serving the React SPA |
| **UI Service** (ClusterIP) | Internal routing to the dashboard |
| **UI Ingress** | `platform-ui.localhost` → dashboard |
| **Postgres 16** | Pod + Service, `DATABASE_URL` injected |
| **Redis** | Pod + Service, `REDIS_URL` injected |
| **Elasticsearch 8** | Pod + Service, `ELASTICSEARCH_URL` injected |
| **Kafka** (KRaft) | Pod + Service, `KAFKA_BROKER_URL` injected |
| **Vault** (dev) | Pod + Service, `VAULT_ADDR` + `VAULT_TOKEN` injected |

That's **11 Kubernetes resources** from two small YAML blocks. You write
zero infrastructure YAML for the backing services.

## Local UI development

To develop the dashboard locally with hot reload:

```bash
# Start the API (or port-forward to an existing one)
cd examples/platform-api && go run .

# In another terminal, start the Vite dev server
cd examples/platform-api/ui && npm install && npm run dev
```

Vite proxies `/api/*` to `localhost:8080` automatically — see
`vite.config.ts`.

## Cleaning up

```bash
kubectl delete devstagingenvironment platform-api-dev platform-api-ui-dev
```

The operator garbage-collects all owned resources automatically.
