# Dashboard Internals

This document describes the dashboard server architecture, all API
endpoints, frontend pages, and the topology editor.

Source: `cli/cmd/dashboard.go`, `cli/cmd/dashboard_api.go`,
`cli/cmd/dashboard_actions.go`, `cli/cmd/dashboard-ui/`

---

## Server architecture

**Command:** `kindling dashboard`

The dashboard is a single-binary web application:
- Go HTTP server (`gorilla/mux`) serves the API and static files
- React + Vite frontend is embedded via `embed.FS`
- Default port: 9090

### Route registration (`dashboard.go`)

```go
func startDashboard(port string) {
    r := mux.NewRouter()

    // API routes
    api := r.PathPrefix("/api").Subrouter()
    registerAPIRoutes(api)      // read-only
    registerActionRoutes(api)   // mutations

    // Static files (embedded React build)
    r.PathPrefix("/").Handler(spaHandler(embeddedFS))

    http.ListenAndServe(":"+port, r)
}
```

The SPA handler serves `index.html` for all non-API, non-static paths,
enabling client-side routing.

---

## API endpoints — Read-only (`dashboard_api.go`)

All read-only endpoints use GET and return JSON.

### GET /api/cluster-info

Returns Kind cluster status, node info, and resource summary.

```json
{
  "name": "dev",
  "status": "running",
  "nodes": [{"name": "dev-control-plane", "status": "Ready"}],
  "resources": {
    "deployments": 5,
    "services": 5,
    "ingresses": 2,
    "dses": 1,
    "runnerPools": 1
  }
}
```

### GET /api/environments

Lists all DevStagingEnvironment CRs with status.

```json
[{
  "name": "my-app",
  "ready": true,
  "services": ["api", "web"],
  "dependencies": ["postgres", "redis"],
  "url": "http://my-app.localhost"
}]
```

### GET /api/deployments

Lists all Deployments with replica status and images.

### GET /api/services

Lists all Services with type, ports, and ClusterIP.

### GET /api/ingresses

Lists all Ingresses with hosts, paths, and backend services.

### GET /api/runner-pools

Lists all CIRunnerPool CRs with status and job counts.

### GET /api/pods

Lists all pods with status, restarts, age, and container info.

### GET /api/secrets

Lists kindling-managed secrets (names only, no values).

### GET /api/events

Lists recent K8s Events filtered to the default namespace.

### GET /api/logs/:deployment

Streams logs from the specified Deployment's pods. Supports
`?tail=N` and `?follow=true` query parameters.

---

## API endpoints — Mutations (`dashboard_actions.go`)

All mutation endpoints use POST and accept JSON bodies.

### POST /api/deploy

Deploys a DSE from a YAML body.

```json
{"yaml": "apiVersion: apps.example.com/v1alpha1\nkind: DevStagingEnvironment\n..."}
```

### POST /api/delete-environment

Deletes a DSE CR by name.

```json
{"name": "my-app"}
```

### POST /api/scale

Scales a Deployment.

```json
{"deployment": "my-app-api", "replicas": 3}
```

### POST /api/restart

Restarts a Deployment (rollout restart).

```json
{"deployment": "my-app-api"}
```

### POST /api/set-env

Sets an environment variable on a Deployment.

```json
{"deployment": "my-app-api", "key": "DEBUG", "value": "true"}
```

### POST /api/unset-env

Removes an environment variable.

```json
{"deployment": "my-app-api", "key": "DEBUG"}
```

### POST /api/create-secret

Creates a K8s Secret.

```json
{"name": "api-key", "key": "API_KEY", "value": "sk-..."}
```

### POST /api/delete-secret

Deletes a K8s Secret.

```json
{"name": "api-key"}
```

### POST /api/create-runner-pool

Creates a CIRunnerPool CR.

```json
{
  "provider": "github",
  "owner": "myorg",
  "repo": "myapp",
  "token": "ghp_...",
  "replicas": 1
}
```

### POST /api/delete-runner-pool

Deletes a CIRunnerPool CR.

```json
{"name": "myorg-myapp-runner"}
```

### POST /api/expose

Starts a public tunnel.

```json
{"provider": "cloudflared", "port": "80"}
```

Response: `{"url": "https://abc123.trycloudflare.com"}`

### POST /api/unexpose

Stops the active tunnel and restores Ingress hosts.

---

## Topology API endpoints

### GET /api/topology

Returns the current cluster topology as nodes and edges for
the ReactFlow canvas.

```json
{
  "nodes": [
    {
      "id": "svc-api",
      "type": "service",
      "data": {
        "name": "api",
        "image": "registry:5000/api:latest",
        "port": 3000,
        "replicas": 1,
        "status": "running"
      },
      "position": {"x": 100, "y": 200}
    },
    {
      "id": "dep-postgres",
      "type": "dependency",
      "data": {
        "name": "postgres",
        "depType": "postgres",
        "image": "postgres:15",
        "port": 5432,
        "status": "running"
      },
      "position": {"x": 400, "y": 200}
    }
  ],
  "edges": [
    {"id": "e-api-postgres", "source": "svc-api", "target": "dep-postgres"}
  ]
}
```

The backend reads the current DSE CR and translates its spec into
ReactFlow node/edge format. Positions are either stored in an annotation
or auto-calculated.

### POST /api/topology/deploy

Deploys the topology as a DSE CR. Accepts the node/edge graph and
converts it back to a DSE spec.

```json
{
  "name": "my-app",
  "nodes": [...],
  "edges": [...],
  "env": [{"key": "NODE_ENV", "value": "development"}]
}
```

### POST /api/topology/scaffold

Generates project scaffolding from the topology definition.

```json
{
  "name": "my-app",
  "nodes": [...],
  "outputDir": "/Users/jeff/projects/my-app"
}
```

Creates:
- Dockerfile per service node
- DSE YAML
- CI workflow
- Basic application boilerplate

### GET /api/topology/check-path

Validates that a local path exists (used by the scaffold dialog).

```
GET /api/topology/check-path?path=/Users/jeff/projects/my-app
→ {"exists": true, "isDir": true}
```

---

## Frontend architecture

### Tech stack

- React 18 + TypeScript
- @xyflow/react v12 (ReactFlow) — canvas rendering
- Vite — build tool
- Tailwind CSS (utility classes in JSX)

### Pages

| Route | Component | Description |
|---|---|---|
| `/` | Dashboard | Overview: cluster status, DSEs, deployments |
| `/topology` | TopologyPage | Drag-and-drop visual editor |
| `/deployments` | Deployments | Deployment list with actions |
| `/services` | Services | Service list |
| `/secrets` | Secrets | Secret management |
| `/runners` | Runners | CI runner pool management |
| `/logs/:name` | LogViewer | Live log streaming |

### TopologyPage.tsx (738 lines)

The topology editor is the most complex frontend component:

**Layout:**
```
┌─────────────────────────────────────────────────────┐
│ Palette Sidebar │ ReactFlow Canvas │ Config Panel   │
│                 │                  │                 │
│ 📦 Services    │  ┌─────┐         │ Name: api       │
│   Node.js      │  │ api ├──┐      │ Image: node:20  │
│   Python       │  └─────┘  │      │ Port: 3000      │
│   Go           │           │      │ Replicas: 1     │
│   Ruby         │  ┌────────┴──┐   │                 │
│                │  │ postgres  │   │                 │
│ 🗄️ Dependencies│  └───────────┘   │                 │
│   PostgreSQL   │                  │                 │
│   Redis        │                  │                 │
│   MongoDB      │                  │                 │
│   ...          │                  │                 │
│                │                  │                 │
│ ──────────────── Deploy Bar ─────────────────────── │
│ [Deploy] [Scaffold] [Export YAML]                   │
└─────────────────────────────────────────────────────┘
```

**Custom node types:**

- `ServiceNode` — application container with name, image, port, status
  badge, and connection handles
- `DependencyNode` — infrastructure dependency with type icon, name,
  and connection handles

**Interaction model:**

1. Drag from palette → creates node on canvas
2. Drag between handles → creates edge (dependency connection)
3. Click node → opens config panel with editable fields
4. Click Deploy → POST /api/topology/deploy
5. Click Scaffold → opens dialog, POST /api/topology/scaffold
6. Click Export YAML → generates DSE YAML in browser

**State management:**

ReactFlow manages node/edge state internally. The component uses
`useNodesState()` and `useEdgesState()` hooks. On mount, it fetches
the current topology from GET /api/topology and initializes the canvas.

**types.ts (432 lines):**

Defines TypeScript interfaces for:
- `ServiceNodeData`, `DependencyNodeData`
- `TopologyNode`, `TopologyEdge`
- `PaletteItem` (15 dependency types + service templates)
- `DeployPayload`, `ScaffoldPayload`
- API response types

---

## Build and development

### Frontend dev

```bash
cd cli/cmd/dashboard-ui
npm install
npm run dev     # Vite dev server on :5173
```

The Vite dev server proxies `/api/*` to `localhost:9090` (the Go server).

### Frontend build

```bash
cd cli/cmd/dashboard-ui
npm run build   # output to dist/
```

The built files are embedded into the Go binary via `//go:embed`.

### Dashboard in the CLI

```bash
kindling dashboard          # starts on :9090
kindling dashboard --port 8080  # custom port
```

The dashboard command starts the Go HTTP server and opens the browser.
