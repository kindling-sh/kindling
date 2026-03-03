---
sidebar_position: 11
title: Debugging
description: Attach VS Code debuggers to running services — Python, Node.js, Go, and Ruby with one command.
---

# Debugging

`kindling debug` attaches a real debugger to a running service inside your
Kind cluster. One command detects the runtime, injects the debug agent,
port-forwards the debug port, and writes a VS Code launch config. Press
F5 and set breakpoints — just like local development.

---

## Quickstart

```bash
# Start a debug session
kindling debug -d <deployment-name>

# Press F5 in VS Code — breakpoints work immediately
# When done:
kindling debug --stop -d <deployment-name>
# Or just press Ctrl-C in the terminal
```

That's it. Kindling handles runtime detection, debug agent injection,
probe management, port forwarding, and VS Code configuration automatically.

---

## How it works

```
kindling debug -d my-api
        │
        ├─ 1. Detect runtime (Python / Node / Go / Ruby)
        ├─ 2. Read original container command via crictl
        ├─ 3. Normalize for debug (strip wrappers, single worker, etc.)
        ├─ 4. Build debug-wrapped command
        ├─ 5. Disable health probes (so breakpoints don't kill the pod)
        ├─ 6. Patch deployment with debug command
        ├─ 7. Save debug state immediately (safe rollback on failure)
        ├─ 8. Wait for new pod to start
        ├─ 9. Inject debug tools (Go only: cross-compile + Delve)
        ├─ 10. Port-forward debug port to localhost
        ├─ 11. Label deployment with session metadata
        └─ 12. Write .vscode/launch.json + tasks.json

        F5 → VS Code attaches to the debugger
        Ctrl-C → restores original deployment (command + probes)
```

Health probes are automatically disabled during debug sessions. When you
hit a breakpoint, Kubernetes won't kill your pod for failing a liveness
check. Probes are restored when the session ends.

**Session labels** are applied to the Deployment during debug (visible via
`kindling status`):
- `kindling.dev/mode: debug`
- `kindling.dev/runtime: python|node|go|ruby`

---

## Supported runtimes

| Runtime | Debug tool | Port | VS Code extension |
|---|---|---|---|
| **Python** | debugpy | 5678 | [Python Debugger](https://marketplace.visualstudio.com/items?itemName=ms-python.debugpy) |
| **Node.js** | V8 Inspector | 9229 | Built-in (ships with VS Code) |
| **Deno** | V8 Inspector | 9229 | Built-in |
| **Bun** | Bun Inspector | 6499 | Built-in |
| **Go** | Delve | 2345 | [Go](https://marketplace.visualstudio.com/items?itemName=golang.Go) |
| **Ruby** | rdbg (debug gem) | 12345 | [VSCode rdbg](https://marketplace.visualstudio.com/items?itemName=KoichiSasada.vscode-rdbg) |

:::tip Frontend deployments
If `kindling debug` detects a frontend deployment (nginx, caddy, httpd
serving a SPA), it will suggest using `kindling dev` instead — which
runs your local dev server with hot reload and port-forwards the
cluster's API services. See [Dev Mode](/docs/dev-mode) for details.
:::

---

## Python

### Dependencies

**In-container** — installed automatically by `kindling debug`:
- `debugpy` — installed via `pip install debugpy` at debug startup

**Local (VS Code):**
- [Python](https://marketplace.visualstudio.com/items?itemName=ms-python.python) extension
- [Python Debugger](https://marketplace.visualstudio.com/items?itemName=ms-python.debugpy) extension

Both extensions are typically installed together and are the standard
Python development extensions for VS Code.

### How it works

Kindling wraps the original command with debugpy:

```
# Original:
python app.py

# Debug-wrapped:
pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:5678 app.py
```

Debugpy is installed in the container at startup (skipped if already
present). The `--listen` flag starts a DAP server that VS Code connects
to through the port-forward.

### Usage

```bash
kindling debug -d my-python-api
```

VS Code attaches with this launch configuration (auto-generated):

```json
{
  "type": "debugpy",
  "request": "attach",
  "connect": { "host": "localhost", "port": 5678 },
  "pathMappings": [
    { "localRoot": "${workspaceFolder}", "remoteRoot": "/app" }
  ],
  "justMyCode": false
}
```

### Frameworks

Works with any Python process — the runtime is detected from the
running container's command. Entrypoint wrapper scripts
(e.g. `docker-entrypoint.sh python app.py`) are automatically skipped.

**Multi-worker normalization:** Servers that fork multiple workers
(gunicorn, uvicorn, hypercorn, sanic with `--workers N`) are automatically
patched to `--workers 1` because debugpy attaches to a single process.
Gunicorn also gets `--timeout 0` to prevent the master from killing a
worker paused at a breakpoint.

| App server | Original command | Debug command |
|---|---|---|
| Plain Python | `python app.py` | `python -m debugpy --listen 0.0.0.0:5678 app.py` |
| Flask | `python -m flask run` | `python -m debugpy --listen 0.0.0.0:5678 -m flask run` |
| FastAPI/Uvicorn | `uvicorn main:app` | `python -m debugpy --listen 0.0.0.0:5678 -m uvicorn main:app --workers 1` |
| Gunicorn | `gunicorn -w 4 app:app` | `python -m debugpy --listen 0.0.0.0:5678 -m gunicorn -w 1 app:app --timeout 0` |
| Django | `python manage.py runserver` | `python -m debugpy --listen 0.0.0.0:5678 manage.py runserver` |
| Celery | `celery -A proj worker` | `python -m debugpy --listen 0.0.0.0:5678 -m celery -A proj worker` |
| Daphne (ASGI) | `daphne myapp.asgi:app` | `python -m debugpy --listen 0.0.0.0:5678 -m daphne myapp.asgi:app` |
| Hypercorn (ASGI) | `hypercorn main:app` | `python -m debugpy --listen 0.0.0.0:5678 -m hypercorn main:app --workers 1` |
| Waitress (WSGI) | `waitress-serve myapp:app` | `python -m debugpy --listen 0.0.0.0:5678 -m waitress-serve myapp:app` |
| Tornado | `python -m tornado.web` | `python -m debugpy --listen 0.0.0.0:5678 -m tornado.web` |
| Sanic | `sanic main:app` | `python -m debugpy --listen 0.0.0.0:5678 -m sanic main:app --workers 1` |
| gRPC | `python -m grpc_tools` | `python -m debugpy --listen 0.0.0.0:5678 -m grpc_tools` |
| `python -m uvicorn` | `python -m uvicorn main:app` | `python -m debugpy --listen 0.0.0.0:5678 -m uvicorn main:app` |

:::tip Double-wrap protection
If you restart a debug session without stopping the previous one (e.g.
after a crash), kindling automatically strips any existing debugpy wrapper
from the current command before re-wrapping. You won't end up with
`debugpy ... debugpy ... app.py`.
:::

---

## Node.js

### Dependencies

**In-container** — nothing to install. Node.js has a built-in V8
Inspector protocol.

**Local (VS Code):**
- No additional extensions needed — Node.js debugging is built into VS Code.

### How it works

Kindling adds the `--inspect` flag to the Node process:

```
# Original:
node server.js

# Debug-wrapped:
node --inspect=0.0.0.0:9229 server.js
```

No packages are installed — the V8 Inspector is built into Node.js.
VS Code connects via the Chrome DevTools Protocol through the
port-forward.

Entrypoint wrapper scripts (e.g. `docker-entrypoint.sh node server.js`)
are automatically detected and skipped — only the runtime binary
receives the `--inspect` flag.

### Usage

```bash
kindling debug -d my-node-api
```

VS Code attaches with this launch configuration (auto-generated):

```json
{
  "type": "node",
  "request": "attach",
  "address": "localhost",
  "port": 9229,
  "restart": true,
  "localRoot": "${workspaceFolder}",
  "remoteRoot": "/app"
}
```

### Frameworks

| App server | Original command | Debug command |
|---|---|---|
| Plain Node | `node server.js` | `node --inspect=0.0.0.0:9229 server.js` |
| Express | `node index.js` | `node --inspect=0.0.0.0:9229 index.js` |
| NestJS | `node dist/main.js` | `node --inspect=0.0.0.0:9229 dist/main.js` |
| ts-node | `npx ts-node src/index.ts` | `ts-node --inspect=0.0.0.0:9229 src/index.ts` |
| tsx | `npx tsx src/index.ts` | `tsx --inspect=0.0.0.0:9229 src/index.ts` |
| npm start | `npm start` | `NODE_OPTIONS='--inspect=0.0.0.0:9229' npm start` |
| yarn dev | `yarn dev` | `NODE_OPTIONS='--inspect=0.0.0.0:9229' yarn dev` |
| pnpm start | `pnpm start` | `NODE_OPTIONS='--inspect=0.0.0.0:9229' pnpm start` |
| Wrapper script | `docker-entrypoint.sh node server.js` | `node --inspect=0.0.0.0:9229 server.js` |

**TypeScript runners** (`ts-node`, `tsx`) accept `--inspect` directly —
no `NODE_OPTIONS` workaround needed.

**Framework CLIs** (`npm start`, `yarn dev`, `pnpm start`, `npx next dev`)
spawn their own Node process, so kindling injects `--inspect` via
`NODE_OPTIONS` which propagates to the child process.

### Deno and Bun

Kindling also supports Deno and Bun, which use the same V8 Inspector protocol:

| Runtime | Debug port | Debug command |
|---|---|---|
| Deno | 9229 | `deno --inspect=0.0.0.0:9229 run server.ts` |
| Bun | 6499 | `bun --inspect=0.0.0.0:6499 server.ts` |

---

## Go

### Dependencies

**Local (your machine):**
- Go toolchain (for cross-compilation)
- [Go](https://marketplace.visualstudio.com/items?itemName=golang.Go) VS Code extension

**In-container** — nothing to install. Kindling cross-compiles the
binary locally and injects both the debug binary and Delve into
the running container via `kubectl cp`.

### How it works

Kindling uses a **sync-inspired approach** — no Go toolchain is needed
inside the container:

```
1. Detect target OS/arch from the Kind node (linux/arm64 or linux/amd64)
2. Cross-compile your Go source locally with debug symbols:
   CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -gcflags='all=-N -l' -buildvcs=false -o _debug_bin .
3. Download/cache a Delve binary for the target architecture
4. kubectl cp both files into the running container (/tmp/dlv, /tmp/_debug_bin)
5. The patched command waits for /tmp/dlv to appear, then starts Delve
```

This means:
- **Scratch/distroless images work** — no Go toolchain needed in the container
- **Multi-stage builds work** — no Delve installation step in your Dockerfile
- **The debug binary has full symbols** — variables and stepping work correctly

```
# Patched container command:
echo 'Waiting for debug tools...';
while [ ! -f /tmp/dlv ]; do sleep 0.5; done;
echo 'Starting Delve debugger';
/tmp/dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --continue /tmp/_debug_bin
```

**Auto-rollback:** If the cross-compile or inject step fails, kindling
automatically restores the original deployment — you won't be left
with a broken container waiting for debug tools that never arrive.

### Usage

```bash
kindling debug -d my-go-api
```

VS Code attaches with this launch configuration (auto-generated):

```json
{
  "type": "go",
  "request": "attach",
  "mode": "remote",
  "host": "localhost",
  "port": 2345,
  "substitutePath": [
    { "from": "${workspaceFolder}", "to": "/app" }
  ]
}
```

### Build flags

Kindling cross-compiles your binary with `-gcflags='all=-N -l'`
automatically — you don't need to modify your Dockerfile. The debug
binary is placed at `/tmp/_debug_bin` and the original image binary
is untouched.

:::tip Source detection
Kindling looks for a `go.mod` file in the current directory and
subdirectories that match the deployment name. For monorepos, `cd` into
the service directory before running `kindling debug`.
:::

---

## Ruby

### Dependencies

**In-container** — installed automatically by `kindling debug`:
- `debug` gem (provides `rdbg`) — installed via `gem install debug`

**Local (your machine):**
- `rdbg` binary — the VS Code extension requires it locally:

```bash
# macOS with Homebrew Ruby
gem install debug

# Or with rbenv/rvm
gem install debug

# Verify installation
rdbg --version
```

**Local (VS Code):**
- [VSCode rdbg](https://marketplace.visualstudio.com/items?itemName=KoichiSasada.vscode-rdbg) extension

```bash
# Install from command line
code --install-extension KoichiSasada.vscode-rdbg
```

:::caution Ruby version requirement
The `debug` gem requires **Ruby 3.1+**. The system Ruby on macOS is
typically 2.6, which is too old. Install a modern Ruby via Homebrew
(`brew install ruby`) or a version manager (rbenv, rvm, asdf) before
installing the debug gem locally.
:::

### How it works

Kindling wraps the original command with rdbg in command mode:

```
# Original:
ruby app.rb

# Debug-wrapped:
gem install debug --no-document -q 2>/dev/null; \
rdbg -n -c --open --host 0.0.0.0 --port 12345 -- ruby app.rb
```

Key flags:
- `-n` (nonstop) — starts the app immediately without waiting for a
  debugger connection, so health probes don't fail
- `-c` (command mode) — treats `ruby app.rb` as a command to execute,
  not as a script filename
- `--open` — opens a TCP debug port for remote attachment

### Usage

```bash
kindling debug -d my-ruby-api
```

VS Code attaches with this launch configuration (auto-generated):

```json
{
  "type": "rdbg",
  "request": "attach",
  "debugPort": "12345",
  "localfsMap": "/app:${workspaceFolder}"
}
```

### Frameworks

| App server | Original command | Debug command |
|---|---|---|
| Plain Ruby | `ruby app.rb` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- ruby app.rb` |
| Sinatra/Puma | `ruby app.rb` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- ruby app.rb` |
| Rails | `rails server` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- rails server` |
| Puma (direct) | `bundle exec puma` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- bundle exec puma` |
| Unicorn | `bundle exec unicorn` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- bundle exec unicorn` |
| Thin | `thin start` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- thin start` |
| Falcon | `falcon serve` | `rdbg -n -c --open --host 0.0.0.0 --port 12345 -- falcon serve` |

:::tip Puma process title
Puma rewrites its process title at runtime (e.g., `puma 6.6.1 (tcp://0.0.0.0:4567) [app]`).
Kindling handles this by reading the original command from the container
runtime (crictl) rather than `/proc/1/cmdline`, so runtime detection and
debug wrapping work correctly regardless of process title changes.
:::

---

## CLI reference

### Start a debug session

```
kindling debug -d <deployment> [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | | Deployment name (required) |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--port` | | (auto) | Override local debug port |
| `--no-launch` | | `false` | Skip writing launch.json |

### Stop a debug session

```
kindling debug --stop -d <deployment>
```

Or press **Ctrl-C** in the terminal where `kindling debug` is running.

Stopping a session:
1. Kills the port-forward process
2. Restores the original container command
3. Re-enables health probes
4. Waits for the clean pod to roll out

---

## Troubleshooting

### "unsupported runtime" error

```
Error: unsupported runtime "unknown" for debugging
```

The pod may be in CrashLoopBackOff or not running. Check with:

```bash
kindling status
kubectl get pods --context kind-dev | grep <deployment>
```

### "connection closed" / "rdbg not found" (Ruby)

```
Error: /bin/zsh -lic 'rdbg --version': exit code is 127
```

The `rdbg` binary must be installed **locally** on your machine (not
just in the container). Install it with:

```bash
gem install debug
rdbg --version  # verify
```

If using macOS system Ruby (2.6), install a modern Ruby first:
```bash
brew install ruby
echo 'export PATH="/opt/homebrew/opt/ruby/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
gem install debug
```

### Port already in use

Kindling automatically detects port conflicts and picks a free port.
If you see issues, stop any existing debug sessions first:

```bash
kindling debug --stop -d <deployment>
```

### "lost connection to pod" during port-forward

The debug pod may have crashed. Check its logs:

```bash
kubectl logs -l app=<deployment> --context kind-dev --tail=50
```

Common causes:
- Health probes killing the pod (should be auto-disabled — update kindling)
- Debug tool installation failing (no network, no package manager)
- Incompatible runtime version

### Breakpoints not hitting

- **Python**: Ensure `justMyCode` is `false` in launch.json (auto-configured)
- **Go**: Compile with `-gcflags="all=-N -l"` to disable optimizations
- **Node.js**: Source maps must be enabled if using TypeScript
- **Ruby**: Ensure the `debug` gem version matches between container and local

### Variables showing as "optimized away" (Go)

Rebuild with optimization disabled:
```dockerfile
RUN go build -gcflags="all=-N -l" -o /app/server .
```

---

## Interaction with other commands

### `kindling sync` + `kindling debug`

File sync and debugging work together. Start debug first, then sync:

```bash
# Terminal 1: start debugger
kindling debug -d my-api

# Terminal 2: live-sync code changes
kindling sync -d my-api --restart
```

Changes synced by `kindling sync` will be picked up on the next
request — set a breakpoint and hit the endpoint to debug the new code.

### `kindling dev`

For **frontend** deployments (nginx/caddy/httpd serving SPAs),
use `kindling dev` instead of `kindling debug`. It runs your local dev
server with hot reload, port-forwards the cluster's API services,
and optionally starts an HTTPS tunnel for OAuth callbacks.

See [Dev Mode](/docs/dev-mode) for full documentation.

### `kindling dashboard`

The dashboard shows debug status for each deployment. Services with
an active debug session display a 🐛 indicator.

### `kindling status`

`kindling status` shows an **Active Dev Sessions** section listing all
deployments with active debug or dev sessions, along with the mode
(🔧 debug / 🖥️ dev) and detected runtime.
