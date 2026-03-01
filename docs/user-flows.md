# User Flows — End-to-End Walkthroughs

This document traces complete user journeys through the system,
showing how CLI commands, operator logic, and cluster resources
interact.

---

## Flow 1: First-time setup (greenfield project)

### Scenario
Developer has a Node.js app with a Dockerfile and wants to set up
a local dev environment with Postgres.

### Steps

```
1. kindling init
   │
   ├─ Creates Kind cluster "dev" from kind-config.yaml
   ├─ Deploys registry (registry:5000)
   ├─ Configures containerd mirror
   ├─ Deploys ingress-nginx
   ├─ Deploys kindling operator to kindling-system
   └─ Waits for all components to be ready
       └─ Output: ✅ Cluster ready

2. kindling runners -u myorg -r myapp -t ghp_TOKEN
   │
   ├─ Creates K8s Secret "runner-token" with PAT
   ├─ Applies CIRunnerPool CR
   │   └─ Operator reconciles:
   │       ├─ ServiceAccount "myorg-myapp-runner"
   │       ├─ ClusterRole + ClusterRoleBinding
   │       └─ Deployment (runner + build-agent sidecar)
   └─ Waits for runner pod to be running
       └─ Output: ✅ Runner registered with GitHub

3. kindling analyze
   │
   ├─ Finds Dockerfile ✅
   ├─ Detects Node.js (package.json) ✅
   ├─ Detects postgres import (pg package) ⚠️ "not in DSE spec"
   ├─ No hardcoded secrets ✅
   ├─ No CI workflow ❌ "run kindling generate"
   ├─ 1 service detected ✅
   └─ Port 3000 detected ✅

4. kindling generate -k sk-OPENAI_KEY -r .
   │
   ├─ Scans project: Node.js, Dockerfile, port 3000, postgres dep
   ├─ Builds AI prompt with project context
   ├─ Calls OpenAI → receives workflow + DSE YAML
   ├─ Writes .github/workflows/dev-deploy.yml
   └─ Writes .kindling/dev-environment.yaml
       └─ Contains: 1 service (api:3000) + 1 dep (postgres)

5. kindling secrets set OPENAI_API_KEY sk-abc123
   │
   ├─ Creates K8s Secret "openai-api-key"
   └─ Persists to .kindling/secrets.yaml

6. kindling push -s api
   │
   ├─ Pre-flight: checks all secretKeyRefs exist ✅
   ├─ git add -A
   ├─ git commit -m "kindling push: api @ 1234567890"
   └─ git push
       └─ GitHub Actions picks up the push:
           ├─ Runner pod receives job
           ├─ Checkout code
           ├─ Build: tar → .request → Kaniko → registry:5000/api:sha
           └─ Deploy: kubectl apply -f .kindling/dev-environment.yaml
               └─ Operator reconciles DSE:
                   ├─ postgres Secret + Deployment + Service
                   ├─ Init container: wait-for-postgres
                   ├─ App Deployment (image: registry:5000/api:sha)
                   │   └─ Env: DATABASE_URL=postgresql://...
                   ├─ App Service (port 3000)
                   └─ App Ingress (myapp.localhost)

7. kindling status
   │
   └─ Shows:
       Cluster: dev (running)
       DSE: myapp (Ready)
       Deployments: myapp-api 1/1, myapp-postgres 1/1
       Ingress: myapp.localhost → myapp-api:3000
```

---

## Flow 2: Inner-loop development (live sync)

### Scenario
Developer wants rapid iteration without waiting for CI builds.

### Steps

```
1. kindling sync -d myapp-api
   │
   ├─ Detect runtime: reads /proc/1/cmdline → "node server.js"
   ├─ Select profile: node → ModeWrapper
   ├─ Install wrapper:
   │   ├─ Write /tmp/kindling-wrapper.sh to pod
   │   └─ Start wrapper loop (node server.js in a while-true)
   ├─ Start fsnotify watcher on ./ (exclude: .git, node_modules)
   └─ Enter event loop:
       │
       ├─ Developer edits src/routes/users.js
       │   ├─ fsnotify fires
       │   ├─ 500ms debounce timer starts
       │   ├─ Developer edits src/routes/posts.js
       │   ├─ Added to pending set (timer resets)
       │   ├─ Timer fires (no more changes)
       │   ├─ kubectl cp src/routes/users.js pod:/app/src/routes/users.js
       │   ├─ kubectl cp src/routes/posts.js pod:/app/src/routes/posts.js
       │   ├─ Kill node process (PID from wrapper)
       │   └─ Wrapper automatically restarts node server.js
       │       └─ Output: 🔄 Synced 2 files, restarted (node)
       │
       └─ Ctrl+C
            ├─ Stop watcher
            ├─ (wrapper keeps running in pod)
            └─ Exit
```

### Runtime-specific variations

**Python (uvicorn):**
```
Detect: "uvicorn main:app"
Profile: uvicorn → ModeSignal (SIGHUP)
Restart: kill -SIGHUP 1  (uvicorn gracefully reloads)
```

**Go (compiled binary):**
```
Detect: "/app/server" (ELF binary)
Profile: go → ModeCompiled
Restart:
  1. GOOS=linux GOARCH=amd64 go build -o server .
  2. kubectl cp server pod:/app/server
  3. kill 1 → wrapper restarts
```

**React (frontend):**
```
Detect: "nginx" serving static files
Profile: react → ModeNone (no process restart)
Sync:
  1. npm run build (locally)
  2. kubectl cp build/ pod:/usr/share/nginx/html/
  3. No restart needed (nginx serves new files immediately)
```

---

## Flow 3: Exposing for OAuth/webhooks

### Scenario
Developer needs a public URL for GitHub OAuth callback testing.

### Steps

```
1. kindling expose
   │
   ├─ Detect tunnel provider: cloudflared found ✅
   ├─ Start: cloudflared tunnel --url localhost:80
   ├─ Parse tunnel URL: https://abc-xyz.trycloudflare.com
   ├─ Patch Ingress:
   │   └─ myapp.localhost → abc-xyz.trycloudflare.com
   └─ Output:
       🌐 Public URL: https://abc-xyz.trycloudflare.com
       📋 Ingress patched: myapp.localhost → abc-xyz.trycloudflare.com

2. Developer configures OAuth app callback:
   https://abc-xyz.trycloudflare.com/auth/callback

3. OAuth flow works end-to-end through tunnel

4. Ctrl+C (or kindling unexpose)
   │
   ├─ Kill cloudflared process
   ├─ Restore Ingress:
   │   └─ abc-xyz.trycloudflare.com → myapp.localhost
   └─ Output: ✅ Tunnel closed, Ingress restored
```

---

## Flow 4: Multi-service architecture

### Scenario
Project has api/ and web/ directories, each with their own Dockerfile.

### DSE spec

```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: platform
spec:
  services:
    - name: api
      image: registry:5000/api:latest
      port: 3000
      ingress:
        enabled: true
        host: api.localhost
    - name: web
      image: registry:5000/web:latest
      port: 80
      ingress:
        enabled: true
        host: web.localhost
  dependencies:
    - name: db
      type: postgres
    - name: cache
      type: redis
```

### What the operator creates

```
Deployments:
  platform-api      (1 pod: init-wait-db, init-wait-cache, api container)
  platform-web      (1 pod: init-wait-db, init-wait-cache, web container)
  platform-db       (1 pod: postgres:15)
  platform-cache    (1 pod: redis:7)

Services:
  platform-api      ClusterIP :3000
  platform-web      ClusterIP :80
  platform-db       ClusterIP :5432
  platform-cache    ClusterIP :6379

Ingresses:
  platform-api      api.localhost → platform-api:3000
  platform-web      web.localhost → platform-web:80

Secrets:
  platform-db-credentials
  platform-cache-credentials

Env vars injected into api + web:
  DATABASE_URL=postgresql://user:password@platform-db:5432/devdb?sslmode=disable
  REDIS_URL=redis://platform-cache:6379/0
```

### Selective rebuild

```bash
kindling push -s api    # only rebuilds api image
kindling push -s web    # only rebuilds web image
```

### Independent sync

```bash
# Terminal 1
kindling sync -d platform-api

# Terminal 2
kindling sync -d platform-web
```

Each sync session independently watches, syncs, and restarts its
target Deployment.

---

## Flow 5: Build + load (no CI)

### Scenario
Developer wants to test a quick change without going through CI.

### Steps

```
1. kindling load -s api --context .
   │
   ├─ docker build -t localhost:5001/api:1234567890 .
   ├─ kind load docker-image localhost:5001/api:1234567890 --name dev
   ├─ Patch DSE (or Deployment):
   │   └─ image: registry:5000/api:1234567890
   └─ Wait for rollout
       └─ Output: ✅ Built, loaded, and deployed api

2. Visit http://myapp.localhost → sees new changes
```

**When to use `load` vs `push`:**
- `load` — quick local iteration, no git commit, uses `docker build`
- `push` — CI build, creates git commit, uses Kaniko

---

## Flow 6: Topology editor (visual)

### Scenario
Developer wants to visually design a multi-service architecture.

### Steps

```
1. kindling dashboard
   │
   └─ Opens browser to http://localhost:9090

2. Navigate to /topology

3. Drag "Node.js" from palette → canvas
   └─ Creates ServiceNode: name=service-1, port=3000

4. Drag "PostgreSQL" from palette → canvas
   └─ Creates DependencyNode: type=postgres, name=postgres

5. Draw edge from service-1 → postgres
   └─ Creates connection (will become dependency reference)

6. Click service-1 node → config panel opens
   └─ Edit: name=api, image=registry:5000/api:latest, port=3000

7. Click "Deploy" button
   │
   ├─ Frontend POST /api/topology/deploy with nodes+edges
   ├─ Backend converts to DSE YAML
   ├─ kubectl apply -f <generated-yaml>
   └─ Operator reconciles
       └─ Dashboard shows: ✅ Deployed

8. Click "Scaffold" button
   │
   ├─ Opens dialog: choose output directory
   ├─ POST /api/topology/scaffold
   └─ Backend generates:
       ├─ api/Dockerfile
       ├─ api/package.json
       ├─ api/server.js
       ├─ .kindling/dev-environment.yaml
       └─ .github/workflows/dev-deploy.yml
```

---

## Flow 7: Disaster recovery

### Scenario
Developer runs `kindling destroy` and needs to rebuild.

### Steps

```
1. kindling destroy --force
   │
   ├─ kind delete cluster --name dev
   └─ Cluster, all resources, all secrets: gone

2. kindling init
   │
   └─ Fresh cluster, operator, registry, ingress

3. kindling runners -u myorg -r myapp -t ghp_TOKEN
   │
   └─ New runner pool registered

4. kindling secrets import
   │
   ├─ Reads .kindling/secrets.yaml (survived destroy)
   └─ Re-creates all K8s Secrets

5. kindling push -s api
   │
   ├─ Pre-flight passes (secrets restored)
   └─ CI builds and deploys from latest commit
```

**Total recovery time:** ~2 minutes (cluster init ~60s, deploy ~60s)

---

## Flow 8: Agent context lifecycle

### Scenario
Developer uses GitHub Copilot and wants persistent project context.

### Steps

```
1. kindling intel on
   │
   ├─ Scan project: languages, Dockerfiles, deps, CI status
   ├─ Generate .kindling/context.md (canonical)
   ├─ Generate .github/copilot-instructions.md (Copilot)
   └─ Output: 🔥 kindling intel active

2. Developer uses any kindling command
   │
   └─ PersistentPreRun → autoIntel():
       ├─ Check staleness (>1 hour?)
       ├─ If stale, regenerate context
       └─ Touch interaction timestamp

3. Developer adds new service or dependency
   │
   └─ Next kindling command:
       ├─ autoIntel() detects project change
       └─ Regenerates context files

4. kindling intel off
   │
   ├─ Remove .github/copilot-instructions.md
   ├─ Create .kindling/intel-disabled marker
   └─ Output: Intel disabled
```
