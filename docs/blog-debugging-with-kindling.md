# 🐛 Debugging Remote Services Like They're Local: A Deep Dive into `kindling debug`

*One command. Six runtimes. Zero Docker Desktop. Full VS Code integration.*

---

You've got a microservices app running in your local Kind cluster via `kindling deploy`. Everything looks great in the dashboard — pods are green, endpoints are responding. But then a request returns a 500, and the logs say... nothing useful.

You need a debugger. Not `print()` statements. Not log levels. A real, honest-to-god breakpoint.

Here's the thing: your code is running inside a container, on a pod, in a Kubernetes cluster. Traditional debugging doesn't work here. You can't just press F5.

**Unless you're using `kindling debug`.**

---

## Table of Contents

1. [The 30-Second Version](#the-30-second-version)
2. [How It Actually Works](#how-it-actually-works)
3. [Walkthrough: Debugging a Python API](#walkthrough-debugging-a-python-api)
4. [Walkthrough: Debugging a Node.js Service](#walkthrough-debugging-a-nodejs-service)
5. [Walkthrough: Debugging a Go Service](#walkthrough-debugging-a-go-service)
6. [Walkthrough: Debugging a Ruby Service](#walkthrough-debugging-a-ruby-service)
7. [Using Debug with Live Sync](#using-debug-with-live-sync)
8. [The Dashboard Debug Button](#the-dashboard-debug-button)
9. [Supported Runtimes Reference](#supported-runtimes-reference)
10. [Tips and Troubleshooting](#tips-and-troubleshooting)

---

## The 30-Second Version

```bash
# From your service's project directory
cd ~/projects/my-api

# Start debugging
kindling debug -d my-api

# Press F5 in VS Code
# Set breakpoints
# Hit the endpoint
# Step through code
# ...profit

# When you're done
kindling debug --stop -d my-api
```

That's it. One command sets up the debugger, the port-forward, and writes your VS Code launch config. F5 attaches. Ctrl-C cleans everything up.

---

## How It Actually Works

When you run `kindling debug -d my-api`, here's what happens under the hood:

```
┌──────────────────────────────────────────────────────────────────┐
│  1. Detect runtime from the running pod                         │
│     crictl inspect → runtimeSpec.process.args                   │
│     Fallback: kubectl exec <pod> -- cat /proc/1/cmdline         │
│     → "python", "node", "go", "ruby", "deno", "bun"            │
│                                                                  │
│  2. Detect container working directory                           │
│     kubectl exec <pod> -- pwd                                   │
│     → "/app"                                                     │
│                                                                  │
│  3. Patch the deployment with debug wrapper                     │
│     Original:  uvicorn main:app --host 0.0.0.0 --port 5000     │
│     Patched:   pip install debugpy -q; python -m debugpy        │
│                --listen 0.0.0.0:5678 uvicorn main:app ...       │
│                                                                  │
│  4. Disable health probes (liveness + readiness)                │
│     Prevents breakpoints from triggering pod kills              │
│                                                                  │
│  5. Wait for the new pod to roll out                            │
│     kubectl rollout status deployment/my-api --timeout=120s     │
│                                                                  │
│  6. Port-forward the debug port to localhost                    │
│     kubectl port-forward deployment/my-api 5678:5678            │
│                                                                  │
│  7. Write .vscode/launch.json + tasks.json                      │
│     With pathMappings, correct port, preLaunchTask              │
│                                                                  │
│  8. Wait for Ctrl-C, then restore original deployment           │
│     Rollout undo restores command + probes atomically           │
└──────────────────────────────────────────────────────────────────┘
```

The beauty is that **the debugger runs inside the container**, with access to the real environment variables (`DATABASE_URL`, `REDIS_URL`, etc.), the real network (talking to other services), and the real dependencies. You're not debugging a mock — you're debugging production-like code with a real Postgres behind it.

---

## Walkthrough: Debugging a Python API

Let's say you have an `orders` service — a FastAPI app running on uvicorn:

```python
# orders/main.py
from fastapi import FastAPI
from db import get_db

app = FastAPI()

@app.get("/health")
async def health():
    return {"status": "ok"}

@app.get("/orders/{order_id}")
async def get_order(order_id: int):
    db = get_db()
    order = db.execute(
        "SELECT * FROM orders WHERE id = %s", (order_id,)
    ).fetchone()
    if not order:
        raise HTTPException(status_code=404, detail="Order not found")
    return dict(order)

@app.post("/orders")
async def create_order(payload: dict):
    db = get_db()
    # BUG: Why is this returning 500?
    result = db.execute(
        "INSERT INTO orders (customer, total) VALUES (%s, %s) RETURNING id",
        (payload["customer"], payload["total"])
    )
    db.commit()
    return {"id": result.fetchone()[0]}
```

The `POST /orders` endpoint is throwing a 500 and the logs just say "Internal Server Error". Time to debug.

### Step 1: Start the debug session

```bash
cd ~/projects/my-orders-service
kindling debug -d jeff-vincent-orders
```

Output:
```
  🔍  Detecting runtime for jeff-vincent-orders
  🎯  Detected runtime: Python (debugpy)
  📂  Remote working directory: /app
  📝  Original command: /usr/local/bin/python3.12 /usr/local/bin/uvicorn main:app --host 0.0.0.0 --port 5000
  ℹ️  No command override in deployment spec — will remove on stop
  🔧  Debug command: pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:5678 /usr/local/bin/uvicorn main:app --host 0.0.0.0 --port 5000
  🔧  Patching deployment with debug command
  ⏳  Waiting for debugger pod to start...
  ✅  Debug pod ready: jeff-vincent-orders-6c7dbc9fd5-xm74n
  🔗  Port-forwarding localhost:5678 → jeff-vincent-orders:5678
  📎  Wrote .vscode/tasks.json — background debug task configured
  📎  Wrote .vscode/launch.json — press F5 to start debugging

  🔧 Debugger ready on localhost:5678
  📎 Press F5 in VS Code to attach
  🛑 Press Ctrl-C or run 'kindling debug --stop -d jeff-vincent-orders' to stop
```

### Step 2: Set a breakpoint

In VS Code, open `main.py` and click in the gutter next to line 28 — the `db.execute` call inside `create_order`:

```python
@app.post("/orders")
async def create_order(payload: dict):
    db = get_db()
    result = db.execute(          # ← 🔴 Set breakpoint here
        "INSERT INTO orders (customer, total) VALUES (%s, %s) RETURNING id",
        (payload["customer"], payload["total"])
    )
```

### Step 3: Attach the debugger

Press **F5** in VS Code. Select **"kindling: attach jeff-vincent-orders"** if prompted.

The debug toolbar appears at the top of VS Code — you're connected.

### Step 4: Trigger the bug

```bash
curl -X POST http://localhost:8080/api/orders \
  -H "Content-Type: application/json" \
  -d '{"customer": "Acme Corp", "total": 99.99}'
```

VS Code pauses at your breakpoint. Now you can:

- **Hover** over `payload` to see `{"customer": "Acme Corp", "total": 99.99}`
- **Inspect** the `db` connection in the Variables panel
- **Step Into** the `db.execute()` call
- Use the **Debug Console** to evaluate expressions:

```python
>>> payload.keys()
dict_keys(['customer', 'total'])
>>> type(payload["total"])
<class 'float'>  # Ah-ha! The DB column expects Decimal, not float
```

Found it. The issue is a type mismatch. Fix, redeploy, move on.

### Step 5: Stop debugging

Either press Ctrl-C in the terminal, or:

```bash
kindling debug --stop -d jeff-vincent-orders
```

The deployment is restored to its original state — no debug wrapper, no debugpy.

### What launch.json looks like

Kindling generated this automatically:

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "kindling: debug jeff-vincent-orders",
      "type": "debugpy",
      "request": "attach",
      "connect": {
        "host": "localhost",
        "port": 5678
      },
      "pathMappings": [
        {
          "localRoot": "${workspaceFolder}",
          "remoteRoot": "/app"
        }
      ],
      "justMyCode": false,
      "preLaunchTask": "kindling: start debug jeff-vincent-orders"
    },
    {
      "name": "kindling: attach jeff-vincent-orders",
      "type": "debugpy",
      "request": "attach",
      "connect": {
        "host": "localhost",
        "port": 5678
      },
      "pathMappings": [
        {
          "localRoot": "${workspaceFolder}",
          "remoteRoot": "/app"
        }
      ],
      "justMyCode": false
    }
  ]
}
```

The `pathMappings` are critical — they tell debugpy that your local `~/projects/my-orders-service/main.py` corresponds to `/app/main.py` inside the container. Without this, breakpoints are silently ignored.

> **Note the two configs**: The first one ("debug") includes a `preLaunchTask` that automatically starts `kindling debug` when you press F5. The second one ("attach") is for re-attaching to an already-running debug session — useful if VS Code disconnects.

---

## Walkthrough: Debugging a Node.js Service

Node.js debugging is simpler because the V8 inspector is built in — no package to install.

```javascript
// gateway/index.js
const express = require('express');
const axios = require('axios');
const app = express();

app.use(express.json());

app.get('/api/orders/:id', async (req, res) => {
  try {
    const { data } = await axios.get(
      `http://jeff-vincent-orders:5000/orders/${req.params.id}`
    );
    res.json(data);
  } catch (err) {
    // BUG: Why are we getting ECONNREFUSED sometimes?
    console.error('Failed to fetch order:', err.message);
    res.status(502).json({ error: 'upstream error' });
  }
});

app.listen(3000, () => console.log('Gateway on :3000'));
```

### Start debugging

```bash
cd ~/projects/my-gateway
kindling debug -d jeff-vincent-gateway
```

```
  🎯  Detected runtime: Node.js
  📂  Remote working directory: /app
  🔧  Debug command: node --inspect=0.0.0.0:9229 index.js
  🔗  Port-forwarding localhost:9229 → jeff-vincent-gateway:9229
  🔧 Debugger ready on localhost:9229
```

No install step needed — Node's `--inspect` flag is all it takes.

### Set a breakpoint and inspect

Set a breakpoint on the `axios.get` call (line 10). Press F5. Hit the endpoint:

```bash
curl http://localhost:8080/api/orders/42
```

VS Code pauses. In the debug console:

```javascript
> req.params
{ id: '42' }
> process.env.DATABASE_URL
'postgresql://orders:orders@jeff-vincent-orders-postgres:5432/orders'
```

You can see the real environment variables — the ones injected by the DSE dependency system. This is the actual runtime context, not a local approximation.

### Generated launch.json for Node

```json
{
  "name": "kindling: debug jeff-vincent-gateway",
  "type": "node",
  "request": "attach",
  "address": "localhost",
  "port": 9229,
  "restart": true,
  "localRoot": "${workspaceFolder}",
  "remoteRoot": "/app",
  "preLaunchTask": "kindling: start debug jeff-vincent-gateway"
}
```

The `"restart": true` is key — if the process restarts (from a file sync or crash), VS Code automatically re-attaches.

---

## Walkthrough: Debugging a Go Service

Go debugging uses [Delve](https://github.com/go-delve/delve), the standard Go debugger. This one is a bit different because Go is compiled — you need the binary compiled with debug symbols.

```go
// inventory/main.go
package main

import (
    "database/sql"
    "encoding/json"
    "log"
    "net/http"
    "os"
)

func getInventory(w http.ResponseWriter, r *http.Request) {
    db, _ := sql.Open("postgres", os.Getenv("DATABASE_URL"))
    defer db.Close()

    rows, err := db.Query("SELECT id, sku, quantity FROM inventory")
    if err != nil {
        http.Error(w, err.Error(), 500) // ← 🔴 Breakpoint here
        return
    }
    // ...
}

func main() {
    http.HandleFunc("/inventory", getInventory)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### Start debugging

```bash
cd ~/projects/my-inventory
kindling debug -d jeff-vincent-inventory
```

```
  🎯  Detected runtime: Go (Delve)
  📂  Remote working directory: /app
  🔧  Debug command: dlv exec --headless --listen=:2345 --api-version=2
      --accept-multiclient --continue ./inventory
  🔗  Port-forwarding localhost:2345 → jeff-vincent-inventory:2345
  🔧 Debugger ready on localhost:2345
```

> ⚠️ **Important for Go**: Your Dockerfile must build with `go build -gcflags='all=-N -l'` to preserve debug symbols. Optimized binaries strip the info Delve needs. Also use `-buildvcs=false` since Kaniko doesn't have a `.git` directory.

### Generated launch.json for Go

```json
{
  "name": "kindling: debug jeff-vincent-inventory",
  "type": "go",
  "request": "attach",
  "mode": "remote",
  "host": "localhost",
  "port": 2345,
  "substitutePath": [
    {
      "from": "${workspaceFolder}",
      "to": "/app"
    }
  ],
  "preLaunchTask": "kindling: start debug jeff-vincent-inventory"
}
```

Go uses `substitutePath` instead of `pathMappings` — same concept, different VS Code extension convention.

---

## Walkthrough: Debugging a Ruby Service

Ruby debugging uses [rdbg](https://github.com/ruby/debug), the official Ruby debugger. It works with Sinatra, Rails, Puma, and any Ruby process.

```ruby
# notifications/app.rb
require 'sinatra'
require 'json'

set :bind, '0.0.0.0'
set :port, 4567

notifications = []

get '/health' do
  content_type :json
  { status: 'ok' }.to_json
end

post '/notifications' do
  content_type :json
  payload = JSON.parse(request.body.read)
  notification = {
    id: notifications.length + 1,
    message: payload['message'],
    created_at: Time.now.iso8601
  }
  notifications << notification
  # BUG: Why is the response missing the timestamp?
  { id: notification[:id], message: notification[:message] }.to_json
end
```

### Prerequisites

Ruby debugging requires the `rdbg` binary installed **locally** on your machine (not just in the container). The VS Code extension uses it to manage the DAP protocol:

```bash
# macOS — system Ruby is too old (2.6), install a modern one
brew install ruby
echo 'export PATH="/opt/homebrew/opt/ruby/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc

# Install the debug gem (provides rdbg)
gem install debug
rdbg --version  # verify

# Install the VS Code extension
code --install-extension KoichiSasada.vscode-rdbg
```

### Start debugging

```bash
cd ~/projects/my-notifications
kindling debug -d jeff-vincent-notifications
```

```
  🔍  Detecting runtime for jeff-vincent-notifications
  🎯  Detected runtime: Ruby (rdbg)
  📂  Remote working directory: /app
  📝  Original command: ruby app.rb
  🔧  Debug command: gem install debug --no-document -q 2>/dev/null;
       rdbg -n -c --open --host 0.0.0.0 --port 12345 -- ruby app.rb
  🔧  Patching deployment with debug command
  🔧  Disabling health probes for debug session
  ✅  Debug pod ready: jeff-vincent-notifications-5f8bd594c7-lgjg4
  🔗  Port-forwarding localhost:12345 → jeff-vincent-notifications:12345
  🔧 Debugger ready on localhost:12345
```

Notice two Ruby-specific details:
- **`-n` (nonstop)** — starts the app immediately. Without this, rdbg waits for a debugger connection before launching the app, causing health probes to fail.
- **`-c` (command mode)** — treats `ruby app.rb` as a command. Without this, rdbg interprets `ruby` as a script filename.

### Set a breakpoint and debug

Press F5, set a breakpoint on the `notifications << notification` line, and hit the endpoint:

```bash
curl -X POST http://localhost:8080/api/notifications \
  -H "Content-Type: application/json" \
  -d '{"message": "test notification"}'
```

In the debug console:

```ruby
> notification
{:id=>1, :message=>"test notification", :created_at=>"2026-03-02T00:30:00+00:00"}
> notification.keys
[:id, :message, :created_at]
```

The `created_at` key exists in the hash but the response hash doesn't include it. Bug found.

### Generated launch.json for Ruby

```json
{
  "name": "kindling: debug jeff-vincent-notifications",
  "type": "rdbg",
  "request": "attach",
  "debugPort": "12345",
  "localfsMap": "/app:${workspaceFolder}/notifications",
  "preLaunchTask": "kindling: start debug jeff-vincent-notifications"
}
```

The `localfsMap` maps container paths to local paths — Kindling auto-detects the source subdirectory in monorepo setups.

> **Note on Puma**: If your Ruby app uses Puma (common with Sinatra and Rails), Puma rewrites its process title at runtime to something like `puma 6.6.1 (tcp://0.0.0.0:4567) [app]`. Kindling handles this by reading the original command from the container runtime (crictl) rather than `/proc/1/cmdline`, so detection and debug wrapping work correctly.

---

## Using Debug with Live Sync

Here's where it gets really powerful. `kindling debug` gives you breakpoints on whatever code is **currently in the container**. But what if you want to debug your latest local changes?

**Combine `kindling sync` with `kindling debug`:**

```bash
# Terminal 1 — sync local code changes into the pod
kindling sync -d jeff-vincent-orders

# Terminal 2 — attach the debugger
kindling debug -d jeff-vincent-orders
```

Now you have a live development loop:

```
┌─────────────────────────────────────────────────────┐
│                                                     │
│  1. Edit main.py locally                            │
│  2. kindling sync pushes the file to the pod        │
│  3. uvicorn auto-reloads (or process restarts)      │
│  4. Debugger re-attaches (restart: true)            │
│  5. Hit endpoint → breakpoint pauses on new code    │
│                                                     │
│  Repeat.                                            │
│                                                     │
└─────────────────────────────────────────────────────┘
```

Your breakpoints hit on the code you just wrote, running against the real database, talking to the real services in the cluster. The feedback loop is **seconds**, not minutes.

### Order matters (sort of)

You can start sync or debug in either order, but be aware:

- **Starting debug restarts the pod** (it patches the deployment). If sync was running, it will reconnect to the new pod.
- **Starting sync after debug** just works — sync patches the files in the already-debugging pod.

The recommended flow: start sync first, then debug. But both orders work.

---

## The Dashboard Debug Button

If you prefer a GUI, the dashboard's Topology view has a built-in Debug button.

### How to use it

1. Open the dashboard: `kindling dashboard`
2. Navigate to the **Topology** page
3. Click on a service node (not a dependency — databases don't need debuggers)
4. In the sidebar, find the **🔧 Debug** button

When you click it:
- The button shows a loading spinner while the pod rolls out
- Once ready, it turns into a **🛑 Stop Debugger** button
- A status badge appears showing the runtime and port:
  > `🐛 Python (debugpy) on localhost:5678`
- A **📋 Copy launch config** button lets you copy the VS Code config to your clipboard

### One key difference from the CLI

The dashboard doesn't write `.vscode/launch.json` to disk — it doesn't know your local project directory. Instead, it returns the launch config JSON, and you can:

1. Click **📋 Copy launch config** to grab it
2. Paste it into your `.vscode/launch.json`
3. Press F5 to attach

For the full one-click F5 experience, use the CLI from your project directory.

---

## Supported Runtimes Reference

| Runtime | Debug Port | Debugger | Install | VS Code Extension |
|---------|-----------|----------|---------|-------------------|
| **Python** | 5678 | debugpy | Auto-installed (`pip install debugpy`) | [ms-python.debugpy](https://marketplace.visualstudio.com/items?itemName=ms-python.debugpy) |
| **Node.js** | 9229 | V8 Inspector | Built-in | Built-in |
| **Deno** | 9229 | V8 Inspector | Built-in | Built-in |
| **Bun** | 6499 | Bun Inspector | Built-in | [oven.bun-vscode](https://marketplace.visualstudio.com/items?itemName=oven.bun-vscode) |
| **Go** | 2345 | Delve (dlv) | Auto-installed (`go install ...dlv@latest`) | [golang.go](https://marketplace.visualstudio.com/items?itemName=golang.go) |
| **Ruby** | 12345 | rdbg | Auto-installed (`gem install debug`) | [KoichiSasada.vscode-rdbg](https://marketplace.visualstudio.com/items?itemName=KoichiSasada.vscode-rdbg) |

> **Ruby note**: Unlike other runtimes, Ruby requires the `rdbg` binary installed **locally** on your machine. The VS Code extension runs `rdbg --version` via your login shell to verify. Install with `gem install debug`. macOS system Ruby (2.6) is too old — install Ruby 3.1+ via `brew install ruby` or a version manager first.

### Runtime auto-detection

Kindling reads the original container command via `crictl inspect` on
the Kind node (falling back to `/proc/1/cmdline` if needed). The
detection priority is:

```
python → ruby → deno → bun → node → go
```

This ordering prevents false matches — for example, "deno" won't accidentally match as "node" despite both being JavaScript runtimes.

### What gets injected per runtime

| Runtime | What kindling wraps your command with |
|---------|--------------------------------------|
| Python | `pip install debugpy -q 2>/dev/null; python -m debugpy --listen 0.0.0.0:5678 <your-cmd>` |
| Node.js | `node --inspect=0.0.0.0:9229 <your-script>` |
| Deno | `deno run --inspect=0.0.0.0:9229 <your-script>` |
| Bun | `bun --inspect=0.0.0.0:6499 <your-script>` |
| Go | `dlv exec --headless --listen=:2345 --api-version=2 --accept-multiclient --continue <your-binary>` |
| Ruby | `gem install debug -q 2>/dev/null; rdbg -n -c --open --host 0.0.0.0 --port 12345 -- <your-cmd>` |

---

## Tips and Troubleshooting

### Breakpoints not hitting?

**Most common cause**: Missing path mappings. Your local file is at `~/projects/orders/main.py` but the container has it at `/app/main.py`. Kindling auto-detects the remote working directory and sets up `pathMappings`, but if your Dockerfile uses an unusual `WORKDIR`, double-check the generated `launch.json`.

```json
"pathMappings": [
  {
    "localRoot": "${workspaceFolder}",
    "remoteRoot": "/app"    ← This must match the container's pwd
  }
]
```

### Port already in use?

Kindling auto-detects port conflicts. If port 5678 is busy, it picks a free one:

```
  ℹ️  Port 5678 in use, using 54351 instead
```

The launch.json is updated to match. You can also force a specific port:

```bash
kindling debug -d my-api --port 9999
```

### Debugger disconnects when files sync?

For Node.js, the launch config includes `"restart": true`, which auto-reconnects after a process restart. For Python with uvicorn, the debugger re-listens automatically on reload.

If auto-reconnect doesn't work, use the **"kindling: attach \<deployment\>"** config (the second one in launch.json) to manually re-attach without restarting the whole debug session.

### Pod crashes after stopping?

If you Ctrl-C'd at a bad time (during rollout), the deployment might still have the debug command. Clean it up:

```bash
kindling debug --stop -d my-api
```

If that doesn't work (stale state), manually remove the command override:

```bash
kubectl patch deployment/my-api --context kind-dev --type=json \
  -p '[{"op":"remove","path":"/spec/template/spec/containers/0/command"}]'
```

### Writing launch.json to the wrong directory?

By default, kindling writes to `$PWD/.vscode/`. If you're in the wrong directory, use `--project-dir`:

```bash
kindling debug -d my-api -p ~/projects/my-api
```

### Skipping the launch.json write?

If you manage your own VS Code configs:

```bash
kindling debug -d my-api --no-launch
```

### Go: "could not launch process: not an executable"

Your binary needs debug symbols. Update your Dockerfile:

```dockerfile
# ❌ Wrong — strips debug info
RUN go build -o /app/server .

# ✅ Correct — preserves debug symbols
RUN go build -gcflags='all=-N -l' -buildvcs=false -o /app/server .
```

### Ruby: "rdbg --version: command not found"

The VS Code rdbg extension requires `rdbg` installed **locally**:

```bash
# macOS system Ruby is too old (2.6) — install a modern one
brew install ruby
echo 'export PATH="/opt/homebrew/opt/ruby/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc

# Install the debug gem
gem install debug
rdbg --version  # should print version

# Install the VS Code extension
code --install-extension KoichiSasada.vscode-rdbg
```

### Ruby: Pod in CrashLoopBackOff during debug

If the debug pod keeps crashing, check whether health probes are being
disabled. Older versions of kindling didn't remove probes, causing the
pod to be killed before the app starts. Update kindling and try again.

If probes are still present:

```bash
kubectl patch deployment/<name> --context kind-dev --type=json \
  -p '[{"op":"remove","path":"/spec/template/spec/containers/0/livenessProbe"},
       {"op":"remove","path":"/spec/template/spec/containers/0/readinessProbe"}]'
```

### The F5 one-click flow

Kindling generates a `tasks.json` alongside `launch.json`. The task runs `kindling debug` as a background process with a problem matcher that waits for `"Debugger ready"` before attaching. This means the first "kindling: debug" config in your launch.json will:

1. Start `kindling debug` in a VS Code terminal
2. Wait for the deployment to roll out and port-forward to be ready
3. Auto-attach the debugger

All from a single F5 press. The generated `tasks.json`:

```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "kindling: start debug my-api",
      "type": "shell",
      "command": "kindling debug -d my-api --port 5678",
      "isBackground": true,
      "problemMatcher": [
        {
          "pattern": [{ "regexp": "^__never_match__$" }],
          "background": {
            "activeOnStart": true,
            "beginsPattern": "Detecting runtime",
            "endsPattern": "Debugger ready"
          }
        }
      ],
      "presentation": {
        "reveal": "silent",
        "panel": "dedicated"
      }
    },
    {
      "label": "kindling: stop debug my-api",
      "type": "shell",
      "command": "kindling debug --stop -d my-api"
    }
  ]
}
```

---

## Wrapping Up

`kindling debug` brings the simplicity of local debugging to remote Kubernetes services. No Docker Desktop, no telepresence, no manual port-forwards, no hand-crafted launch configs.

**One command. F5. Breakpoints hit.**

The debugger sees the real environment — real database connections, real service mesh, real secrets. Combined with `kindling sync` for live code updates, you get a development loop that's as fast as local development but as realistic as staging.

```bash
# The complete workflow
kindling deploy -f dev-environment.yaml    # Deploy your services
kindling sync -d my-api                     # Live-sync code changes
kindling debug -d my-api                    # Attach debugger
# F5 → set breakpoints → debug → fix → repeat
kindling debug --stop -d my-api             # Clean up
```

Happy debugging. 🔥
