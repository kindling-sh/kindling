# CLI Internals

This document describes every CLI command: flags, implementation,
code paths, error handling, and design considerations.

Source: `cli/cmd/`, `cli/core/`

---

## Module structure

The CLI is a separate Go module: `github.com/jeffvincent/kindling/cli`

Dependencies:
- `cobra` — command framework
- `fsnotify` — file watching (sync command)
- `gorilla/mux` — dashboard HTTP router
- `pkg/ci` — shared CI provider package (via `replace` directive)

Build: `cd cli && go build -o kindling .` (or `make cli`)

---

## root.go — Root command and global hooks

**Global flags:**
- `--project-dir` — override project directory resolution
- `--kubeconfig` — override kubeconfig path (default `~/.kube/config`)

**PersistentPreRun: `autoIntel()`**

Every command (except `intel`, `version`, `help`, `completion`) triggers
automatic agent context management:

```
1. Read .kindling/context.md — if missing, skip
2. If intel explicitly disabled (marker file), skip
3. If stale (>1 hour since last interaction), restore
4. If not active, activate
5. Touch interaction timestamp
```

This ensures the `.github/copilot-instructions.md` context file stays
current without manual intervention.

**`resolveProjectDir()`**

Three-tier fallback:
1. `--project-dir` flag value
2. Current directory (if `kind-config.yaml` exists)
3. `~/.kindling` (auto-clones from GitHub if missing)

---

## init.go — Cluster bootstrap

**Command:** `kindling init`

**What it does:**
1. Check Docker Desktop is running
2. Check if `dev` Kind cluster already exists
3. Create Kind cluster from `kind-config.yaml`
4. Deploy in-cluster registry (`registry:5000`)
5. Configure containerd mirror (`localhost:5000` → `registry:5000`)
6. Deploy ingress-nginx controller
7. Wait for ingress to be ready
8. Deploy kindling operator (kustomize build + kubectl apply)
9. Wait for operator to be ready

**Key implementation details:**
- Uses `core.RunKubectl()` for all kubectl operations
- Progress is reported via colored emoji output (🔧, ✅, ❌)
- Each step checks for existing resources before creating
- Kind config enables ingress-ready labels and port mappings
- The registry is deployed as a simple Deployment + Service + ConfigMap
- Operator deployment uses kustomize from `config/default/`

**Flags:**
- None (uses global flags only)

**Error handling:**
- Docker not running → fatal error with install instructions
- Cluster already exists → prints notice, continues with remaining steps
- kubectl failures → wraps error with step context

---

## deploy.go — DSE deployment

**Command:** `kindling deploy -f <dse-yaml>`

**What it does:**
1. Read and validate the DSE YAML file
2. `kubectl apply -f <file>`
3. Wait for the DSE to become ready (polls status)

**Flags:**
- `-f, --file` — path to DSE YAML (required)

**Implementation:** Thin wrapper around `core.RunKubectl("apply", "-f", file)`.

---

## push.go — Git push with selective rebuild

**Command:** `kindling push -s <service>`

**What it does:**
1. **Secret pre-flight check** — reads the CI workflow file, extracts
   all `secretKeyRef` names, checks each K8s secret exists. If any are
   missing, prints a table and aborts.
2. Stage all changes (`git add -A`)
3. Commit with auto-generated message (`kindling push: <service> @ <timestamp>`)
4. `git push`
5. Print status message (CI will pick up the push)

**Flags:**
- `-s, --service` — service to rebuild (required)
- `-m, --message` — custom commit message

**Secret pre-flight logic:**

```go
func checkSecretsExist(workflowFile string) error {
    content := readFile(workflowFile)
    refs := findAllSecretKeyRefs(content)  // regex: secretKeyRef.name: (\S+)
    for _, ref := range unique(refs) {
        _, err := core.RunKubectl("get", "secret", ref)
        if err != nil {
            missing = append(missing, ref)
        }
    }
    if len(missing) > 0 {
        return fmt.Errorf("missing secrets: %v\nRun: kindling secrets set ...", missing)
    }
}
```

This catches the common mistake of pushing without setting required
secrets, which would cause the CI job to fail.

---

## sync.go — Live file sync (1892 lines)

**Command:** `kindling sync -d <deployment>`

This is the most complex command. It implements the inner development
loop: watch local files → sync to pod → restart process.

**Flags:**
- `-d, --deployment` — target Deployment name (required)
- `-c, --container` — target container (default: first)
- `--debounce` — batch window in milliseconds (default: 500)
- `--exclude` — glob patterns to exclude (default: `.git,node_modules,__pycache__`)
- `--build-cmd` — local build command to run before sync
- `--sync-path` — remote directory (default: auto-detected from workdir)
- `--no-restart` — skip process restart after sync
- `--watch` — directory to watch (default: `.`)

**Architecture:**

```
main goroutine
  │
  ├─ detectRuntime()       ← reads /proc/1/cmdline from pod
  ├─ selectProfile()       ← maps runtime → restart strategy
  ├─ setupWatcher()        ← fsnotify on local directory
  │
  └─ event loop
       ├─ on file change → add to pending set
       ├─ on debounce timer fire →
       │    ├─ run build-cmd (if set)
       │    ├─ kubectl cp each changed file
       │    └─ restart(profile)
       └─ on signal → cleanup + exit
```

**Runtime detection (`detectRuntime()`)**

Two-tier detection strategy:

1. **crictl inspect** (preferred) — queries the container runtime on
   the Kind node for the original OCI process args. This returns the
   true `CMD`/`ENTRYPOINT` from the image, unaffected by process title
   rewrites (e.g. Puma rewriting to `puma 6.6.1 (tcp://...)`).

2. **`/proc/1/cmdline` fallback** — if crictl fails (e.g. no container
   name available), falls back to reading the process cmdline from
   inside the pod. May return rewritten titles for some runtimes.

```go
func detectRuntime(pod, namespace, container string) (runtimeProfile, string) {
    // 1. Try crictl inspect first (original OCI args)
    cID := exec("docker exec dev-control-plane crictl ps --name <container> -q")
    inspect := exec("docker exec dev-control-plane crictl inspect --output json <cID>")
    args := inspect.info.runtimeSpec.process.args  // e.g. ["ruby", "app.rb"]

    // 2. Fall back to /proc/1/cmdline
    cmdline := exec("kubectl exec <pod> -- cat /proc/1/cmdline")

    // Match against 49 runtime profiles
    proc := filepath.Base(args[0])  // e.g. "ruby", "node", "python3"
    return matchRuntime(proc, args)
}
```

**`readContainerCommand()`** — used by `debug.go` to get the original
command for debug wrapping. Uses the same crictl-first strategy to
avoid process title rewrites breaking the debug command.

**Runtime profiles (49)**

Each profile defines:
- `RestartMode` — how to restart the process
- `Signal` — which signal to send (if signal mode)
- `SyncPath` — where files live in the container
- `BuildRequired` — whether a local build step is needed
- `Description` — human-readable name

Full profile list:

| Runtime | Mode | Signal | Notes |
|---|---|---|---|
| node | wrapper | — | Wraps with bash loop |
| nodemon | none | — | Built-in file watching |
| ts-node | wrapper | — | TypeScript direct execution |
| python | wrapper | — | Wraps with bash loop |
| uvicorn | signal | SIGHUP | `--reload` flag |
| gunicorn | signal | SIGHUP | Master → workers |
| flask | wrapper | — | Dev server |
| django | wrapper | — | `manage.py runserver` |
| fastapi | signal | SIGHUP | Via uvicorn |
| celery | signal | SIGHUP | Worker pool |
| daphne | kill | — | Django Channels ASGI |
| hypercorn | signal | SIGHUP | ASGI server |
| waitress | kill | — | WSGI server |
| tornado | kill | — | Async framework |
| sanic | kill | — | Async framework |
| django | kill | — | `manage.py` detection |
| grpcio | kill | — | Python gRPC server |
| ruby/rails | wrapper | — | Puma dev mode |
| puma | signal | SIGUSR2 | Phased restart |
| unicorn | signal | SIGUSR2 | Graceful restart |
| thin | kill | — | Thin web server |
| falcon | kill | — | Async Ruby server |
| php/php-fpm | none | — | Per-request reload |
| nginx | signal | SIGHUP | Config reload |
| apache/httpd | signal | SIGUSR1 | Graceful restart |
| java/spring | wrapper | — | JVM restart required |
| go (binary) | compiled | — | Cross-compile + copy |
| rust (binary) | compiled | — | Cross-compile + copy |
| next | wrapper | — | Next.js dev server |
| vite | wrapper | — | HMR handles reloads |
| webpack | wrapper | — | Dev server |
| deno | wrapper | — | Deno runtime |
| bun | wrapper | — | Bun runtime |
| dotnet | wrapper | — | .NET runtime |
| elixir/mix | wrapper | — | Mix/Phoenix |
| perl | wrapper | — | Perl processes |
| lua/openresty | signal | SIGHUP | nginx-based |
| caddy | signal | SIGUSR1 | Config reload |
| traefik | signal | SIGHUP | Proxy reload |
| envoy | signal | SIGHUP | Proxy reload |
| supervisord | signal | SIGHUP | Process manager |
| pm2 | none | — | Built-in watch |
| passenger | signal | SIGUSR2 | Phased restart |
| reactor/netty | wrapper | — | JVM-based |
| quarkus | wrapper | — | Dev mode |
| micronaut | wrapper | — | JVM restart |
| ktor | wrapper | — | Kotlin server |

**Wrapper mode implementation:**

```go
func installWrapper(deployment, container string, cmdline string) {
    wrapperScript := fmt.Sprintf(`#!/bin/sh
while true; do
  %s &
  PID=$!
  wait $PID
done`, cmdline)

    // Write wrapper to /tmp/kindling-wrapper.sh
    exec("kubectl exec ... -- sh -c 'cat > /tmp/kindling-wrapper.sh << EOF\n%s\nEOF'", wrapperScript)
    exec("kubectl exec ... -- chmod +x /tmp/kindling-wrapper.sh")

    // Kill original process, start wrapper
    exec("kubectl exec ... -- kill 1")  // PID 1 dies, wrapper takes over
}
```

**Frontend sync:**

When a frontend framework is detected (React, Vue, Angular, Next,
Vite), sync runs the local build command (`npm run build`) and syncs
the output directory to the pod's webroot (usually `/usr/share/nginx/html`
or `/app/build`).

---

## debug.go — Debugger attachment (1403 lines)

**Command:** `kindling debug -d <deployment>`

Attaches a VS Code debugger to a running deployment by injecting a
debug agent, port-forwarding the debug port, and writing launch configs.

**Flags:**
- `-d, --deployment` — target Deployment name (required)
- `-n, --namespace` — Kubernetes namespace (default: `default`)
- `--port` — override local debug port (default: per-runtime)
- `--stop` — stop an active debug session
- `--no-launch` — skip writing launch.json

**Architecture:**

```
startDebug()
  │
  ├─ findPodForDeployment()    ← find a running pod
  ├─ detectRuntime()           ← crictl inspect → /proc/1/cmdline fallback
  ├─ isFrontendDeployment()    ← if frontend, suggest `kindling dev` and abort
  ├─ matchDebugProfile()       ← map runtime name → debug config (with aliases)
  ├─ readContainerCommand()    ← get original CMD (crictl-first)
  ├─ stripDebugWrapper()       ← remove any existing debug wrapping (prevents double wrap)
  ├─ buildDebugCommand()       ← wrap with debug agent
  ├─ remove health probes      ← JSON patch (prevents breakpoint kills)
  ├─ patch deployment command  ← JSON patch
  ├─ saveDebugState()          ← persist IMMEDIATELY (safe rollback if process dies)
  ├─ wait for rollout          ← kubectl rollout status
  ├─ injectGoDebugTools()      ← Go only: cross-compile + kubectl cp + auto-rollback on failure
  ├─ port-forward debug port   ← kubectl port-forward (background)
  ├─ labelSession()            ← add kindling.dev/mode + kindling.dev/runtime labels
  ├─ writeLaunchConfig()       ← .vscode/launch.json + tasks.json
  └─ wait for Ctrl-C → stopDebug()

stopDebug()
  │
  ├─ loadDebugState()          ← read saved state
  ├─ kill port-forward         ← os.FindProcess + Kill
  ├─ unlabelSession()          ← remove session labels
  ├─ restore deployment        ← rollout undo (if probes removed)
  │                               or JSON patch (remove command)
  ├─ wait for rollout
  └─ clearDebugState()
```

**Supported runtimes:**

| Runtime | Debugger | Port | Install | VS Code type |
|---|---|---|---|---|
| Python | debugpy | 5678 | `pip install debugpy` | `debugpy` |
| Node.js | V8 Inspector | 9229 | Built-in | `node` |
| Deno | V8 Inspector | 9229 | Built-in | `node` |
| Bun | Bun Inspector | 6499 | Built-in | `bun` |
| Go | Delve | 2345 | Cross-compile + inject | `go` |
| Ruby | rdbg | 12345 | `gem install debug` | `rdbg` |

**matchDebugProfile():**

Runtime matching uses a priority list to avoid false substring matches
(`deno` before `node`, etc.). Also supports aliases for runtime names
that don't contain the debug key (e.g. "TypeScript (tsx)" → `node`).
Falls back to command-line inspection for Node.js tools (`ts-node`,
`tsx`, `npx`, `npm`, `yarn`, `pnpm`).

**Special detection patterns in `matchRuntime()`:**
- `python -m <module>` → looks up `<module>` (e.g. `uvicorn`, `gunicorn`)
- `python manage.py` → `django`
- `bundle exec <cmd>` → looks up `<cmd>` (e.g. `puma`, `rails`)
- `npx <cmd>` → looks up `<cmd>`

**buildDebugCommand():**

| Runtime | Transform |
|---|---|
| Node.js | `node server.js` → `node --inspect=0.0.0.0:9229 server.js` |
| Node.js (framework CLI) | `npm start` → `NODE_OPTIONS='--inspect=0.0.0.0:9229' npm start` |
| Node.js (ts-node/tsx) | `ts-node src/index.ts` → `ts-node --inspect=0.0.0.0:9229 src/index.ts` |
| Node.js (wrapper script) | `docker-entrypoint.sh node server.js` → `node --inspect=0.0.0.0:9229 server.js` |
| Python | `python app.py` → `pip install debugpy -q; python -m debugpy --listen 0.0.0.0:5678 app.py` |
| Python (uvicorn) | `uvicorn main:app -w 4` → `python -m debugpy --listen 0.0.0.0:5678 -m uvicorn main:app -w 1` |
| Python (gunicorn) | `gunicorn app:app` → `python -m debugpy --listen 0.0.0.0:5678 -m gunicorn app:app --timeout 0` |
| Python (daphne) | `daphne myapp.asgi:app` → `python -m debugpy --listen 0.0.0.0:5678 -m daphne myapp.asgi:app` |
| Python (hypercorn) | `hypercorn main:app -w 4` → `python -m debugpy --listen 0.0.0.0:5678 -m hypercorn main:app -w 1` |
| Python (waitress) | `waitress-serve --port=8080 myapp:app` → `python -m debugpy --listen 0.0.0.0:5678 -m waitress --port=8080 myapp:app` |
| Python (manage.py) | `python manage.py runserver` → `python -m debugpy --listen 0.0.0.0:5678 manage.py runserver` |
| Ruby | `ruby app.rb` → `gem install debug -q; rdbg -n -c --open --host 0.0.0.0 --port 12345 -- ruby app.rb` |
| Go | `./server` → wait-loop for `/tmp/dlv` → `dlv exec --headless /tmp/_debug_bin` |

**findRuntimeBinary():**

Locates the actual runtime binary in args, skipping entrypoint wrapper
scripts like `docker-entrypoint.sh`. Falls back to index 0 if not found.
Handles versioned binary names (python3, python3.12, node18, etc.).

**normalizePythonForDebug():**

Adjusts Python server flags for single-process debugging:
- `--workers N` / `-w N` → `--workers 1` (debugpy attaches to one process)
- Gunicorn: adds `--timeout 0` (prevents master from killing paused worker)
- Prints warnings when values are changed

**stripDebugWrapper():**

Removes any existing debug wrapper from a command before re-wrapping.
Prevents double-wrapping when restarting a debug session (e.g. after
a crash) — strips `pip install debugpy ...;`, `gem install debug ...;`,
`NODE_OPTIONS=...`, and the Delve wait-loop.

**Go sync-inspired approach:**

Instead of requiring Go toolchain in the container:
1. `detectNodeArch()` — queries Kind node for OS/arch
2. `findGoSourceDir()` — finds `go.mod` in CWD/subdirectories
3. Cross-compile locally: `CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -gcflags='all=-N -l' -buildvcs=false`
4. `ensureDelve()` — downloads/caches Delve binary for target arch at `~/.kindling/cache/dlv-<arch>`
5. `injectGoDebugTools()` — kubectl cp both files into `/tmp/` in the container
6. **Auto-rollback**: if inject fails, calls `stopDebug()` to restore the original deployment

**Ruby-specific flags:**
- `-n` (nonstop) — starts the app immediately without waiting for a
  debugger connection. Without this, health probes fail because the
  app never starts until a debugger attaches.
- `-c` (command mode) — treats `ruby app.rb` as a command to execute.
  Without this, rdbg interprets `ruby` as a script filename and runs
  `ruby ruby app.rb` → LoadError.

**Health probe handling:**

Breakpoints pause program execution, which causes Kubernetes liveness
and readiness probes to fail — killing the debug pod. Kindling removes
probes during debug sessions:

1. **On start**: checks for existing liveness/readiness probes, saves
   `hadProbes` in debug state, removes probes via JSON patch
2. **On stop**: if probes were removed, uses `kubectl rollout undo` to
   revert to the pre-debug revision (restores probes + command atomically)

**Debug state persistence:**

State is saved to `~/.kindling/debug-<deployment>.json`:

```json
{
  "deployment": "jeff-vincent-orders",
  "namespace": "default",
  "runtime": "python",
  "localPort": 5678,
  "remotePort": 5678,
  "origCmd": "python -m uvicorn main:app --host 0.0.0.0 --port 5000",
  "hadCommand": false,
  "hadProbes": true,
  "pfPid": 12345,
  "devPid": 0,
  "srcDir": ""
}
```

**Launch config generation (`writeLaunchConfig()`):**

Writes two VS Code configurations:
1. **"kindling: debug \<name\>"** — includes `preLaunchTask` that starts
   `kindling debug` as a background VS Code task. Press F5 for one-click.
2. **"kindling: attach \<name\>"** — attach-only, no task. For re-attaching
   to an already-running session.

Path mappings are auto-configured using the detected remote working
directory and optional source subdirectory (monorepo support).

---

## dev.go — Frontend dev mode (597 lines)

**Command:** `kindling dev -d <deployment>`

Runs a local frontend dev server with full access to cluster APIs.
Designed for frontend deployments (nginx/caddy/httpd serving SPAs)
where you want hot reload instead of building and syncing static assets.

**Flags:**
- `-d, --deployment` — target frontend Deployment name (required)
- `-n, --namespace` — Kubernetes namespace (default: `default`)
- `--stop` — stop an active dev session

**Architecture:**

```
runDev()
  │
  ├─ findPodForDeployment()           ← find a running pod
  ├─ detectRuntime() + readContainerCommand()
  ├─ isFrontendDeployment()           ← verify nginx/caddy/httpd
  ├─ localSourceDirForDeployment()    ← resolve local source (absolute path)
  ├─ detect package manager           ← pnpm-lock.yaml / yarn.lock / package-lock.json
  ├─ discoverAPIServices()            ← find backend services in namespace
  │    └─ port-forward each service   ← kubectl port-forward (background processes)
  ├─ detectFrontendOAuth()            ← scan source for OAuth/OIDC patterns
  │    ├─ core.StartCloudflaredTunnel()  ← HTTPS tunnel
  │    ├─ patchDevServerAllowedHost()    ← Vite/Next.js config patch
  │    └─ export KINDLING_TUNNEL_URL
  ├─ exec.Command(pm, "run", "dev")   ← launch dev server as child process
  │    └─ SysProcAttr{Setpgid: true}  ← process group for clean kill
  ├─ saveDebugState()                 ← persist DevPid + SrcDir
  ├─ labelSession()                   ← kindling.dev/mode=dev, runtime=frontend
  └─ select on Ctrl-C or dev server exit
       ├─ syscall.Kill(-pgid, SIGTERM) ← kill process group (npm + vite)
       ├─ restoreDevServerConfig()     ← revert Vite/Next.js patches
       ├─ unlabelSession()
       └─ clearDebugState()
```

**isFrontendDeployment():**

Checks the container command for known static-file web servers:
`nginx`, `caddy`, `httpd`, `apache`, `serve`. Also validates that a
`package.json` exists in the resolved source directory to avoid
treating plain nginx reverse proxies as frontends.

**localSourceDirForDeployment():**

Resolves the local source directory using a scoring system:

1. **Deployment-suffix fast path** — strips the actor prefix
   (e.g. `jeff-frontend` → `frontend`) and checks for an exact
   directory match. Gets a +20 score boost.
2. **Recursive scan** — walks directories looking for `package.json`,
   scores based on directory name similarity to deployment name.
3. Returns absolute paths via `filepath.Abs()`.

**discoverAPIServices():**

Lists services in the namespace, filters out the frontend deployment's
own service, and port-forwards each to a random local port. Returns
a `[]serviceURL` with name, local port, and remote port.

**detectFrontendOAuth():**

Scans source files for OAuth/OIDC patterns:
- `NEXTAUTH`, `NEXT_AUTH`
- `OIDC`, `OPENID`
- `oauth`, `OAuth`
- `AUTH0`, `CLERK`
- `callback`, `redirect_uri`

If found, starts a cloudflared tunnel for HTTPS callbacks.

**patchDevServerAllowedHost():**

Patches Vite or Next.js config to allow the tunnel hostname:
- **Vite**: Adds `allowedHosts: ['hostname']` to `server:` block
  in `vite.config.ts` / `vite.config.js`
- **Next.js**: Sets `allowedDevHosts` in `next.config.js`
- Creates `.kindling-dev-backup` files for restoration

**restoreDevServerConfig():**

Restores the original config from `.kindling-dev-backup` files
when the dev session ends.

**detectDevServerPort():**

Reads `package.json` and config files to determine the dev server
port (default 5173 for Vite, 3000 for Next.js, etc.). Used for
display and proxy configuration.

**Process group management:**

The dev server is started with `SysProcAttr{Setpgid: true}` to create
a new process group. On cleanup, `syscall.Kill(-pgid, SIGTERM)` kills
the entire group — both the package manager process (npm) and the
spawned dev server (vite). This prevents orphan vite processes.

---

## load.go — Build + load + deploy

**Command:** `kindling load -s <service> [--context .]`

**What it does:**
1. `docker build` with the local Dockerfile
2. Tag as `localhost:5001/<service>:<unix-timestamp>`
3. `kind load docker-image <tag> --name dev`
4. Patch the DSE CR (or Deployment) with the new image tag
5. Wait for rollout

**Flags:**
- `-s, --service` — service name (required)
- `--context` — build context directory (default: `.`)
- `--dockerfile` — Dockerfile path (default: `Dockerfile`)
- `--build-arg` — build arguments (repeatable)

**Implementation:** Delegates to `core.BuildLoadDeploy()` which
orchestrates the docker build → kind load → kubectl patch pipeline.

---

## expose.go — Public HTTPS tunnel

**Command:** `kindling expose`

**What it does:**
1. Detect available tunnel provider (cloudflared → ngrok → localtunnel)
2. Start tunnel process pointing to `localhost:80`
3. Parse tunnel URL from process output
4. Patch all Ingress resources to use tunnel hostname
5. Print public URL

**Flags:**
- `--provider` — force tunnel provider (`cloudflared`, `ngrok`, `localtunnel`)
- `--port` — local port (default: 80)
- `--host` — hostname to expose (patches Ingress)
- `--auth-domain` — OAuth auth domain for callbacks

**Tunnel provider detection:**

```go
func detectTunnelProvider() string {
    if _, err := exec.LookPath("cloudflared"); err == nil {
        return "cloudflared"
    }
    if _, err := exec.LookPath("ngrok"); err == nil {
        return "ngrok"
    }
    return "localtunnel"  // npm-based fallback
}
```

**Ingress patching:**

After the tunnel URL is obtained, all Ingress resources in the cluster
are patched to replace `*.localhost` hosts with the tunnel hostname.
This enables OAuth callbacks and webhook testing with real URLs.

**Process management:**

The tunnel process is started in the background. The CLI sets up a
signal handler for SIGINT/SIGTERM that:
1. Kills the tunnel process
2. Restores original Ingress hosts
3. Exits cleanly

---

## status.go — Cluster overview

**Command:** `kindling status`

**What it does:**
1. Check Kind cluster exists
2. List all DSE CRs with status
3. List all Deployments with ready/total replicas
4. List all Services with ClusterIP
5. List all Ingresses with hosts
6. List all CIRunnerPools with status
7. Print formatted table

**Output format:**

```
🏗️  Cluster: dev (running)

📦 Environments:
  NAME        READY   SERVICES   DEPENDENCIES   URL
  my-app      True    2          3              http://my-app.localhost

🚀 Deployments:
  NAME                 READY   STATUS
  my-app-api           1/1     Running
  my-app-postgres      1/1     Running

🌐 Services:
  NAME                 TYPE        PORT
  my-app-api           ClusterIP   3000
  my-app-postgres      ClusterIP   5432

🔗 Ingresses:
  HOST                 SERVICE          PORT
  my-app.localhost     my-app-api       3000
```

---

## env.go — Environment variable management

**Command:** `kindling env set KEY=VALUE` / `kindling env list` / `kindling env unset KEY`

**What it does:**
- `set` — patches the Deployment to add/update an env var
- `list` — reads the Deployment spec and prints env vars
- `unset` — patches the Deployment to remove an env var

**Flags:**
- `-d, --deployment` — target Deployment (required for set/unset)

**Implementation:** Delegates to `core.SetEnvVar()`, `core.ListEnvVars()`,
`core.UnsetEnvVar()` which use `kubectl patch deployment` with
strategic merge patches.

---

## secrets.go — Secret CRUD + local persistence

**Command:** `kindling secrets set KEY VALUE` / `kindling secrets list` /
`kindling secrets delete KEY` / `kindling secrets export` / `kindling secrets import`

**What it does:**
- `set` — creates a K8s Secret with the given key-value pair + persists
  to `.kindling/secrets.yaml` for recovery
- `list` — lists all kindling-managed secrets
- `delete` — deletes the K8s Secret + removes from local store
- `export` — dumps all secrets to a YAML file
- `import` — restores secrets from a YAML file

**Naming convention:**

Secrets are stored with the key as a sanitized K8s secret name:

```go
func secretName(key string) string {
    // "DATABASE_URL" → "database-url"
    return strings.ToLower(strings.ReplaceAll(key, "_", "-"))
}
```

**Dual-name checking:**

When looking up secrets, the CLI checks both the original name and the
sanitized name to handle legacy secrets:

```go
func getSecret(key string) (*Secret, error) {
    // Try exact name first
    if s, err := kubectl("get", "secret", key); err == nil {
        return s, nil
    }
    // Try sanitized name
    return kubectl("get", "secret", secretName(key))
}
```

**Local persistence:**

All secrets are also saved to `.kindling/secrets.yaml` (gitignored).
This allows recovery after `kindling destroy` + `kindling init`:

```yaml
secrets:
  DATABASE_URL: postgresql://...
  OPENAI_API_KEY: sk-...
```

---

## runners.go — CI runner pool

**Command:** `kindling runners -u <owner> -r <repo> -t <token>`

**What it does:**
1. Create K8s Secret with the PAT
2. Apply a CIRunnerPool CR
3. Wait for runner pod to be ready

**Flags:**
- `-u, --user` — GitHub org or GitLab group
- `-r, --repo` — repository name
- `-t, --token` — Personal Access Token
- `--provider` — `github` (default) or `gitlab`
- `--labels` — extra runner labels
- `--replicas` — runner count (default: 1)

**Implementation:** Delegates to `core.CreateRunnerPool()` which
constructs the CIRunnerPool CR YAML and applies it.

---

## intel.go — Agent context management (729 lines)

**Command:** `kindling intel [on|off]`

**What it does:**
- `on` — scans the project, generates context files, enables auto-lifecycle
- `off` — removes context files, disables auto-lifecycle
- (no subcommand) — shows current status

**Context files generated:**
- `.kindling/context.md` — canonical context (this repo's details)
- `.github/copilot-instructions.md` — GitHub Copilot context (symlink or copy)

**Project scanning:**

```go
func scanProject() ProjectInfo {
    // Detect languages (Go, Python, Node, Ruby, Java, etc.)
    languages := detectLanguages()

    // Find Dockerfiles
    dockerfiles := findDockerfiles()

    // Check for existing CI workflow
    ciWorkflow := detectCIWorkflow()

    // Check for dependencies in DSE YAML
    dependencies := parseDSEDependencies()

    return ProjectInfo{Languages, Dockerfiles, CIWorkflow, Dependencies}
}
```

**Auto-lifecycle (PersistentPreRun):**

The auto-lifecycle hook ensures context files stay current:
- Regenerates if project structure changes
- Cleans up if intel was disabled
- Tracks last interaction timestamp for staleness detection
- Skips non-interactive commands (version, help, completion)

---

## analyze.go — Project readiness checks

**Command:** `kindling analyze`

**What it does:**
Runs 7 categories of checks and prints a readiness report:

1. **Dockerfile check** — finds all Dockerfiles, validates syntax
2. **Dependency check** — identifies databases/caches from code imports
3. **Secret check** — scans for hardcoded secrets, API key patterns
4. **CI workflow check** — validates existing workflow files
5. **Agent check** — detects multi-agent architectures (multiple services)
6. **Port check** — finds exposed ports in Dockerfiles and code
7. **Health check** — looks for health/readiness endpoints

**Output:**

```
🔍 Analyzing project...

✅ Dockerfiles: 2 found (api/Dockerfile, web/Dockerfile)
⚠️  Dependencies: postgres detected in code, not in DSE spec
✅ Secrets: no hardcoded secrets found
❌ CI Workflow: not found — run `kindling generate`
✅ Agents: 2 services detected (api, web)
✅ Ports: 3000 (api), 80 (web)
⚠️  Health checks: no /health endpoint found
```

---

## generate.go — AI workflow generation

**Command:** `kindling generate -k <api-key> -r .`

**What it does:**
1. Scan project for languages, Dockerfiles, dependencies
2. Build AI prompt from `pkg/ci/prompt.go` constants + project details
3. Call OpenAI API with the prompt
4. Parse response into workflow file
5. Write `.github/workflows/dev-deploy.yml` or `.gitlab-ci.yml`
6. Write `.kindling/dev-environment.yaml` (DSE spec)

**Flags:**
- `-k, --api-key` — OpenAI API key (required)
- `-r, --root` — project root (default: `.`)
- `--provider` — CI provider (`github` or `gitlab`, default: `github`)
- `--model` — AI model (default: `gpt-4`)

**Detection functions:**

```go
detectDockerfiles(root)    → []string
detectLanguages(root)      → []string
detectPackageManagers(root) → []string
detectDependencies(root)   → []DependencyHint
detectPorts(root)          → []int
detectServices(root)       → []ServiceHint
```

These feed into the AI prompt to generate accurate CI configurations.

---

## logs.go — Controller log streaming

**Command:** `kindling logs`

**What it does:**
Streams logs from the kindling controller manager pod:

```go
exec("kubectl", "logs", "-n", "kindling-system",
    "-l", "control-plane=controller-manager",
    "--follow", "--tail=100")
```

**Flags:**
- `--tail` — number of lines to show (default: 100)
- `--follow` / `-f` — follow log output (default: true)

---

## destroy.go — Cluster teardown

**Command:** `kindling destroy`

**What it does:**
1. Confirm with user (unless `--force`)
2. `kind delete cluster --name dev`
3. Clean up local kubeconfig context

**Flags:**
- `--force` — skip confirmation prompt

---

## reset.go — Runner pool cleanup

**Command:** `kindling reset`

**What it does:**
1. Delete all CIRunnerPool CRs
2. Delete runner Deployments
3. Delete runner ServiceAccounts, ClusterRoles, ClusterRoleBindings
4. Keep the Kind cluster and operator running

**Flags:**
- None (uses global flags)

---

## version.go — Version display

**Command:** `kindling version`

**Output:** `kindling <version> (<os>/<arch>)`

Reads from build-time `ldflags`:
```go
var (
    version = "dev"
    // set via: go build -ldflags "-X main.version=0.8.1"
)
```

---

## helpers.go — Shared utilities

Key functions:

- **`runCommand(name, args...)`** — exec with stdout/stderr capture
- **`runCommandSilent(name, args...)`** — exec with no output
- **`printStep(emoji, message)`** — colored progress output
- **`printSuccess(message)`** — green checkmark output
- **`printError(message)`** — red X output
- **`printWarning(message)`** — yellow warning output
- **`waitForCondition(fn, timeout, interval)`** — generic poller
- **`fileExists(path)`** — os.Stat wrapper
- **`readFileContent(path)`** — os.ReadFile wrapper
- **`writeFileContent(path, content)`** — os.WriteFile wrapper
- **`labelSession(deploy, ns, mode, runtime)`** — add `kindling.dev/mode` and `kindling.dev/runtime` labels to Deployment metadata (not pod template, no rollout)
- **`unlabelSession(deploy, ns)`** — remove session labels from Deployment

**Color constants:**
```go
colorReset  = "\033[0m"
colorRed    = "\033[31m"
colorGreen  = "\033[32m"
colorYellow = "\033[33m"
colorBlue   = "\033[34m"
colorCyan   = "\033[36m"
```

---

## core/ — Business logic package

### kubectl.go

```go
func RunKubectl(args ...string) (string, error)
func RunKubectlSilent(args ...string) (string, error)
func GetPodName(deployment string) (string, error)
func WaitForDeployment(name string, timeout time.Duration) error
```

All kubectl operations go through `RunKubectl`, which handles:
- Kubeconfig resolution
- Error wrapping with command context
- Output capture and trimming

### secrets.go

```go
func CreateSecret(name, key, value string) error
func GetSecret(name string) (map[string]string, error)
func DeleteSecret(name string) error
func ListSecrets() ([]SecretInfo, error)
func SecretExists(name string) bool
```

Secrets are created as `Opaque` type with a single key-value pair.
The naming convention sanitizes the key name for K8s compatibility.

### tunnel.go

```go
func StartTunnel(provider, port string) (*TunnelInfo, error)
func StopTunnel(info *TunnelInfo) error
func PatchIngressHost(oldHost, newHost string) error
func RestoreIngressHost(newHost, oldHost string) error
```

Tunnel management handles three providers with different URL extraction:
- cloudflared — parses URL from stderr
- ngrok — queries local API (`localhost:4040/api/tunnels`)
- localtunnel — parses URL from stdout

### env.go

```go
func SetEnvVar(deployment, key, value string) error
func UnsetEnvVar(deployment, key string) error
func ListEnvVars(deployment string) ([]EnvVar, error)
```

Uses `kubectl patch` with strategic merge patches to modify
Deployment container env arrays.

### runners.go

```go
func CreateRunnerPool(spec RunnerPoolInput) error
func DeleteRunnerPool(name string) error
func ListRunnerPools() ([]RunnerPoolInfo, error)
```

Constructs CIRunnerPool CR YAML from input and applies via kubectl.

### load.go

```go
func BuildLoadDeploy(opts BuildOpts) error
```

Orchestrates: docker build → kind load → kubectl patch. Uses Unix
timestamp tags for cache-busting.
