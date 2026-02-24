---
sidebar_position: 10
title: File Sync & Hot Reload
description: Live-sync local files into running pods with language-aware restart strategies.
---

# File Sync & Hot Reload

`kindling sync` watches your local source files and syncs changes into a
running pod in real time — no image rebuild, no redeploy. It detects
your runtime automatically and picks the right restart strategy.

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

## Restart strategies

The restart strategy is auto-detected from the container's PID 1 process:

### Wrapper + kill (interpreted languages)

Patches the deployment with a restart-loop shell wrapper. On each sync,
kills the child process so the loop respawns it with new code.

**Runtimes:** Node.js, Python, Ruby, Perl, Lua, Julia, R, Elixir, Deno, Bun

### Signal reload (servers with graceful reload)

Sends SIGHUP or SIGUSR2 to PID 1 for zero-downtime reload. No wrapper needed.

**Runtimes:** uvicorn, gunicorn, Puma, Unicorn, Nginx, Apache (httpd)

### Auto-reload (request-per-file runtimes)

Just syncs files — the runtime re-reads source on every request or
watches for changes itself.

**Runtimes:** PHP (mod_php, php-fpm), nodemon

### Local build + binary sync (compiled languages)

Cross-compiles locally for the container's OS/arch, syncs just the
binary, and restarts via the wrapper.

**Runtimes:** Go, Rust, Java/Kotlin, C#/.NET, C/C++, Zig

The cross-compile command is auto-generated based on the Kind node's
architecture. For example, Go gets:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o <tmpfile> .
```

Use `--build-cmd` and `--build-output` to override.

---

## Examples

```bash
# Node.js — wrapper+kill strategy
kindling sync -d my-api --restart

# Python/uvicorn — signal reload (SIGHUP)
kindling sync -d orders --src ./services/orders --restart

# Nginx — zero-downtime SIGHUP reload
kindling sync -d frontend --src ./dist --dest /usr/share/nginx/html --restart

# Go — cross-compiles locally, syncs binary
kindling sync -d gateway --restart --language go

# Custom build command
kindling sync -d gateway --restart \
  --build-cmd 'CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/gw .' \
  --build-output ./bin/gw

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
| Quick iteration on interpreted code | `kindling sync --restart` |
| Changing a Go/Rust service frequently | `kindling sync --restart --language go` |
| Full image rebuild needed (Dockerfile changed, deps changed) | `git push` (triggers CI rebuild) |
