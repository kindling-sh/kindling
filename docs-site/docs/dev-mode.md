---
sidebar_position: 12
title: Dev Mode
description: Run your frontend dev server locally with hot reload, while hitting APIs in the cluster.
---

# Dev Mode

`kindling dev` runs your frontend dev server locally with full access
to backend services in the cluster. Edit code, see changes instantly —
API calls go to the same services you deployed with `kindling deploy`.

---

## Quickstart

```bash
# Start frontend dev mode
kindling dev -d my-frontend

# Your dev server starts automatically with hot reload
# API services are port-forwarded to localhost
# Ctrl-C stops everything cleanly
```

---

## When to use it

Use `kindling dev` when your deployment serves **static assets** via
nginx, caddy, or httpd (a typical SPA pattern). These deployments don't
have a debuggable backend process — instead, you want to run your local
Vite/Next.js/Angular dev server with hot reload.

If you run `kindling debug` on a frontend deployment, it will tell you
to use `kindling dev` instead.

| Deployment type | Command |
|---|---|
| Backend API (Python, Node, Go, Ruby) | `kindling debug` |
| Frontend SPA (nginx, caddy, httpd) | `kindling dev` |

---

## How it works

```
kindling dev -d my-frontend
        │
        ├─ 1. Verify deployment is a frontend (nginx/caddy/httpd)
        ├─ 2. Resolve local source directory (monorepo-aware)
        ├─ 3. Detect package manager (npm/pnpm/yarn)
        ├─ 4. Discover backend API services in the cluster
        ├─ 5. Port-forward each API service to a local port
        ├─ 6. Detect OAuth/OIDC in source code
        │     ├─ If found: start HTTPS tunnel via cloudflared
        │     ├─ Auto-patch Vite allowedHosts or Next.js config
        │     └─ Export KINDLING_TUNNEL_URL env var
        ├─ 7. Launch local dev server (npm/pnpm/yarn run dev)
        ├─ 8. Label deployment with session metadata
        └─ 9. Stream dev server output inline

        Ctrl-C → stops dev server, tunnel, port-forwards
                 removes session labels
                 restores patched config files
```

---

## Frontend detection

A deployment is considered a frontend if its container runs one of
these web servers to serve static files:

| Server | Detection |
|---|---|
| **nginx** | Binary name starts with `nginx` |
| **caddy** | Binary name is `caddy` |
| **httpd** / **Apache** | Binary name is `httpd` or `apache` |
| **serve** | Binary name is `serve` (npm package) |

The detection is based on the container's actual running command
(via crictl), not the image name. A container running `nginx` but
named `my-app` will still be detected as a frontend.

:::caution Not just any nginx
An nginx container is only treated as a frontend if a `package.json`
exists in the resolved source directory. Plain nginx reverse proxies
without frontend source code won't trigger dev mode.
:::

---

## Source directory resolution

Kindling resolves the local source directory using a scoring system
that considers:

1. **Deployment-suffix fast path** — if your deployment is `jeff-frontend`
   and there's a directory called `frontend/`, it matches immediately
2. **Dockerfile COPY paths** — traces where source code is copied from
3. **Directory name similarity** — scores against the deployment name
4. **`package.json` presence** — boosts score for directories with frontend code

For monorepos, `cd` into the repository root and kindling will find
the right subdirectory automatically.

---

## API service discovery

Kindling automatically discovers other services in the same namespace
and port-forwards them to localhost:

```
🔌 Port-forwarding API services:
   orders-api    → localhost:54321 (port 3000)
   users-api     → localhost:54322 (port 8080)
   payments-api  → localhost:54323 (port 5000)
```

Your frontend's API calls to `localhost:<port>` or relative paths will
reach the cluster services. If your dev server has a proxy configuration
(e.g. Vite's `server.proxy`), the forwarded ports will match.

---

## OAuth / OIDC tunnel

If kindling detects OAuth or OIDC patterns in your source code
(e.g. `NEXTAUTH`, `OIDC`, `oauth`, `AUTH0`, `CLERK`), it automatically:

1. Starts an HTTPS tunnel via cloudflared (free, no account needed)
2. Exports `KINDLING_TUNNEL_URL` as an environment variable
3. Patches your dev server config to allow the tunnel hostname

### Vite

Kindling adds the tunnel hostname to `vite.config.ts` → `server.allowedHosts`:

```ts
// Before:
export default defineConfig({
  server: { ... }
})

// After (auto-patched):
export default defineConfig({
  server: {
    allowedHosts: ['your-tunnel-id.trycloudflare.com'],
    ...
  }
})
```

The patch is **automatically reverted** when the dev session ends.

### Next.js

For Next.js, kindling sets `NEXTAUTH_URL` or equivalent environment
variables to the tunnel URL.

---

## Dev server auto-launch

Kindling detects your package manager and launches the dev server
automatically:

| Lock file | Package manager | Command |
|---|---|---|
| `pnpm-lock.yaml` | pnpm | `pnpm run dev` |
| `yarn.lock` | yarn | `yarn run dev` |
| `package-lock.json` | npm | `npm run dev` |
| *(fallback)* | npm | `npm run dev` |

The dev server runs as a child process with its output streamed
directly to your terminal. Kindling uses process group management
to ensure both the package manager and the spawned dev server
(e.g. Vite) are cleaned up on exit.

---

## Session labels

While a dev session is active, kindling labels the Deployment with:

```yaml
metadata:
  labels:
    kindling.dev/mode: dev
    kindling.dev/runtime: frontend
```

These labels are visible via `kindling status` in the **Active Dev
Sessions** section:

```
🔧 Active Dev Sessions:
  DEPLOYMENT         MODE    RUNTIME
  jeff-frontend      🖥️ dev   frontend
  jeff-api           🔧 debug python
```

Labels are removed when the session ends.

---

## CLI reference

### Start dev mode

```
kindling dev -d <deployment> [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | | Frontend deployment name (required) |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--stop` | | `false` | Stop the dev session |

### Stop dev mode

```
kindling dev --stop -d <deployment>
```

Or press **Ctrl-C** in the terminal where `kindling dev` is running.

Stopping a session:
1. Sends SIGTERM to the dev server process group
2. Stops the HTTPS tunnel (if running)
3. Stops all API port-forwards
4. Restores any patched config files (Vite, Next.js)
5. Removes session labels from the Deployment

---

## Troubleshooting

### "deployment X does not appear to be a frontend"

The container's command isn't nginx, caddy, or httpd. Check what
the deployment is actually running:

```bash
kindling status
kubectl get deploy <name> -o jsonpath='{.spec.template.spec.containers[0].command}' --context kind-dev
```

If it's a backend service, use `kindling debug` instead.

### "cannot find source directory"

Kindling couldn't find a local directory with a `package.json` that
matches the deployment. Try:

```bash
# cd to the repo root
cd /path/to/your/repo
kindling dev -d my-frontend
```

For monorepos, ensure the directory name contains part of the
deployment name (e.g. `frontend/` for a deployment named `jeff-frontend`).

### Dev server port conflict

If port 3000/5173 is already in use, the dev server will fail to start.
Stop other dev servers or change the port in your Vite/Next.js config.

### OAuth tunnel not starting

Ensure `cloudflared` is installed:

```bash
brew install cloudflare/cloudflare/cloudflared
```

---

## Interaction with other commands

### `kindling debug`

`kindling dev` is for frontends, `kindling debug` is for backends.
You can run both simultaneously — debug your API while developing
the frontend:

```bash
# Terminal 1: debug the backend API
kindling debug -d my-api

# Terminal 2: run the frontend dev server
kindling dev -d my-frontend
```

### `kindling sync`

`kindling dev` replaces `kindling sync` for frontend deployments.
With sync, you'd build locally and copy static assets into the nginx
container. With dev mode, you skip that entirely and run the dev
server locally with hot reload.

### `kindling status`

Shows active dev sessions alongside debug sessions in the
**Active Dev Sessions** section.
