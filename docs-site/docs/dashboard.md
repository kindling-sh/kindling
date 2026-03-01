---
sidebar_position: 11
title: Dashboard
description: Web-based dashboard for monitoring and managing your kindling cluster.
---

# Dashboard

`kindling dashboard` launches a local web UI that gives you a real-time
view of everything running in your cluster — environments, runners,
builds, logs, and health status.

---

## Quick start

```bash
kindling dashboard
```

Opens at [http://localhost:9090](http://localhost:9090) by default.

```bash
# Custom port
kindling dashboard --port 8080
```

---

## What you can see

The dashboard provides a single-page view of your entire kindling cluster:

- **Cluster health** — node status, resource usage
- **Operator status** — controller-manager readiness
- **Registry** — in-cluster registry deployment
- **Ingress controller** — ingress-nginx status
- **Runner pools** — GitHub Actions runners, connected repos, online status
- **Dev environments** — every DevStagingEnvironment CR with image, replicas, ready state
- **Pods** — all pods in the default namespace with status and age
- **Unhealthy pods** — CrashLoopBackOff / Error pods with recent log lines

---

## What you can do

The dashboard isn't just read-only. From the UI you can:

- **Toggle Agent Intel** — activate or deactivate coding agent context with one click
- **Generate workflows** — open the command menu (⌘K) and stream AI workflow generation in real time
- **Create and manage secrets** — set, list, and delete API keys and credentials
- **Manage environment variables** — set, list, and unset env vars on deployments
- **Start and stop tunnels** — create public HTTPS tunnels for OAuth callbacks
- **Bootstrap the cluster** — run `kindling init` from the browser
- **Connect runners** — register GitHub Actions runners
- **View logs** — stream controller and pod logs
- **Topology editor** — visual drag-and-drop canvas for designing your architecture

---

## Topology editor

The topology page (`/topology`) provides a visual canvas for designing and
deploying your entire service architecture:

### Stage → Connect → Scaffold → Deploy

1. **Stage** — drag a service from the palette onto the canvas. It appears
   with an amber "staged" badge but no files are generated yet.
2. **Connect** — draw edges between nodes. Service-to-service connections
   show as dashed cyan lines; service-to-dependency connections show as
   solid lines with arrows.
3. **Scaffold** — click a staged service to open its detail sidebar. The
   scaffold button detects connected dependencies and injects the right
   env vars (e.g. `MONGO_URL`, `DATABASE_URL`) into the generated code.
4. **Deploy** — the deploy bar appears whenever you have pending changes
   or staged services. One click deploys everything.

### Canvas persistence

Your canvas layout — nodes, edges, and positions — is saved to
`.kindling/canvas.json` and restored on reload. Staged services survive
page refreshes.

### Supported service templates

- Node.js (Express)
- Python (Flask)
- Go (net/http)

### Supported dependencies

PostgreSQL, Redis, MongoDB, MySQL, RabbitMQ, MinIO, Elasticsearch,
Kafka, NATS, Memcached

---

## Architecture

The dashboard is a single-binary embedded web app — no separate install,
no Node.js runtime, no external dependencies. The React frontend is
compiled to static assets and embedded into the Go binary via `go:embed`.
The backend serves a REST API that wraps the same `core/` package used
by the CLI, so every action available in the terminal is available in the
browser.

---

## Flags

| Flag | Default | Description |
|---|---|---|
| `--port` | `9090` | Port to serve the dashboard on |
