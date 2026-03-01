---
sidebar_position: 10
title: File Sync & Hot Reload
description: Purpose-built hot reload for AI-generated code — Python, Node.js, Go, and Ruby with language-aware restart strategies.
---

# File Sync & Hot Reload

`kindling sync` is the inner loop for agentic development. When a coding
agent generates or modifies a service — a FastAPI endpoint, an Express
route, a Go handler, a Rails controller — the changes land in the
running container in seconds. No image rebuild. No rollout. No waiting.

This is purpose-built for the languages AI agents actually write:
**Python**, **Node.js/TypeScript**, **Go**, and **Ruby**. These four
cover the vast majority of code that Copilot, Claude Code, Cursor, and
similar agents generate in practice.

---

## The agentic dev loop

```
Agent writes code ──► file saved ──► kindling sync ──► app restarts
       │                                                     │
       └──── agent tests endpoint ◄── result in <2 seconds ─┘
```

With `kindling intel` feeding your agent the project context and
`kindling sync` hot-reloading every change, the agent can iterate
autonomously: write code → see it running → fix issues → repeat.
No human in the loop for the build-deploy cycle.

---

## Core language support

These are the languages kindling is optimized for — the languages
AI agents generate real projects in:

### Python <span class="badge badge--success">Hot reload</span>

The #1 language for AI-generated backends. FastAPI, Django, Flask,
Celery workers — all detected automatically.

| Runtime | Strategy | How it works |
|---|---|---|
| `python` / `python3` | Wrapper + kill | Restart loop, kill child on sync |
| `uvicorn` | Signal (SIGHUP) | Zero-downtime graceful reload |
| `gunicorn` | Signal (SIGHUP) | Zero-downtime graceful reload |
| `flask` | Wrapper + kill | Restart loop |
| `celery` | Wrapper + kill | Restart loop |

```bash
# FastAPI with uvicorn — zero-downtime reload
kindling sync -d my-api --restart

# Django
kindling sync -d backend --src ./backend --restart
```

### Node.js / TypeScript <span class="badge badge--success">Hot reload</span>

The #2 language for agent-generated code. Express, Next.js API routes,
Deno, Bun — all covered.

| Runtime | Strategy | How it works |
|---|---|---|
| `node` | Wrapper + kill | Restart loop |
| `deno` | Wrapper + kill | Restart loop |
| `bun` | Wrapper + kill | Restart loop |
| `ts-node` / `tsx` | Wrapper + kill | Restart loop |
| `nodemon` | Auto-reload | Files only — nodemon watches itself |

```bash
# Express / Next.js API
kindling sync -d my-api --restart

# Deno service
kindling sync -d deno-svc --restart
```

### Go <span class="badge badge--info">Build + sync</span>

Agents write Go frequently. Compiled, but kindling handles it:
cross-compiles locally for the container's OS/arch, syncs just the
binary, restarts the process. The pod stays running — no image rebuild,
no rollout.

```bash
# Auto-detected cross-compile + binary swap
kindling sync -d gateway --restart

# Custom build command
kindling sync -d gateway --restart \
  --build-cmd 'CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/svc .' \
  --build-output ./bin/svc
```

The cross-compile command is auto-generated based on the Kind node's
architecture. For example:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o <tmpfile> .
```

### Ruby <span class="badge badge--success">Hot reload</span>

Rails scaffolding is still a common agent pattern. Puma and Unicorn get
graceful signal-based reload; plain Ruby and Bundler use the restart loop.

| Runtime | Strategy | How it works |
|---|---|---|
| `ruby` | Wrapper + kill | Restart loop |
| `rails` | Wrapper + kill | Restart loop |
| `puma` | Signal (USR2) | Graceful phased restart |
| `unicorn` | Signal (USR2) | Graceful rolling restart |
| `bundle` | Wrapper + kill | Restart loop (delegates to inner cmd) |

```bash
# Rails with Puma — zero-downtime reload
kindling sync -d web --restart

# bundle exec rails server
kindling sync -d web --src ./app --restart
```

---

## Also supported

These runtimes work but are outside the core agentic AI use case:

| Language | Runtimes | Strategy |
|---|---|---|
| PHP | php, php-fpm, apache2 | Auto-reload / signal |
| Elixir | mix, elixir, iex | Wrapper + kill |
| Perl | perl | Wrapper + kill |
| Lua | lua, luajit | Wrapper + kill |
| R | R, Rscript | Wrapper + kill |
| Julia | julia | Wrapper + kill |
| Rust | cargo, rustc | Local build + sync |
| Java/Kotlin | java, kotlin | Rebuild |
| C#/.NET | dotnet | Local build + sync |
| C/C++ | gcc | Rebuild |
| Zig | zig | Local build + sync |
| Nginx / Caddy | nginx, caddy | Signal (HUP) |

---

## Quick start

```bash
# Watch current directory, restart the app on each change
kindling sync -d my-api --restart

# One-shot sync (no file watching)
kindling sync -d my-api --restart --once
```

Every time you save a file, it lands in the container and the process
restarts (or reloads, or does nothing — depending on the runtime).

---

## How it works

1. Finds the running pod for the target deployment
2. Reads `/proc/1/cmdline` to detect the runtime
3. Syncs local files into the container via `kubectl cp`
4. Restarts the process using the detected strategy
5. If `--once` is not set, watches for changes and repeats

---

## More examples

```bash
# Python/uvicorn — signal reload (SIGHUP)
kindling sync -d orders --src ./services/orders --restart

# Nginx — zero-downtime SIGHUP reload
kindling sync -d frontend --src ./dist --dest /usr/share/nginx/html --restart

# Extra excludes
kindling sync -d my-api --src ./src --exclude '*.test.js' --exclude 'fixtures/'

# Multi-container pod — target a specific container
kindling sync -d my-api --container app --restart
```

---

## Default excludes

These patterns are excluded automatically: `.git`, `node_modules`,
`__pycache__`, `.venv`, `vendor`, `target`, `.next`, `dist`, `build`,
`*.pyc`, `*.o`, `*.exe`, `*.log`, `.DS_Store`.

Add more with `--exclude`.

---

## Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--deployment` | `-d` | — (required) | Target deployment name |
| `--src` | — | `.` | Local source directory to watch |
| `--dest` | — | `/app` | Destination path inside the container |
| `--namespace` | `-n` | `default` | Kubernetes namespace |
| `--restart` | — | `false` | Restart the app process after each sync |
| `--once` | — | `false` | Sync once and exit (no file watching) |
| `--container` | — | — | Container name (for multi-container pods) |
| `--exclude` | — | — | Additional exclude patterns (repeatable) |
| `--debounce` | — | `500ms` | Debounce interval for batching rapid changes |
| `--language` | — | auto-detect | Override runtime detection |
| `--build-cmd` | — | auto-detect | Local build command for compiled languages |
| `--build-output` | — | auto-detect | Path to built artifact to sync |

---

## When to use sync vs. push

| Scenario | Use |
|---|---|
| Agent iterating on Python/Node/Ruby code | `kindling sync --restart` |
| Agent modifying a Go service | `kindling sync --restart` (auto cross-compiles) |
| Dockerfile or dependency manifest changed | `git push` (triggers full CI rebuild) |
