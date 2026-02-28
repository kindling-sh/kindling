---
sidebar_position: 3
title: Agent Intel
description: Automatically give your coding agent full kindling context — Copilot, Claude Code, Cursor, and Windsurf.
---

# Agent Intel

`kindling intel` keeps your coding agent in sync with how kindling works.
It writes a context file into the agent's system prompt location so the
agent knows about your cluster, CLI commands, dependency injection, build
protocol, and secrets flow — without you having to explain it every time.

---

## How it works

Kindling supports four coding agents out of the box:

| Agent | Config file |
|---|---|
| **GitHub Copilot** | `.github/copilot-instructions.md` |
| **Claude Code** | `CLAUDE.md` |
| **Cursor** | `.cursor/rules/kindling.mdc` |
| **Windsurf** | `.windsurfrules` |

When intel activates, kindling:

1. **Backs up** any existing agent config file (stored alongside the original)
2. **Writes** a context file that includes CLI commands, architectural principles, dependency auto-injection tables, Kaniko compatibility notes, and project-specific details (detected languages, Dockerfiles, CI status)
3. **Tracks state** in `.kindling/intel-state.json`

When intel deactivates, the originals are restored exactly as they were.

---

## Auto-lifecycle

Intel is designed to be hands-free. It activates and deactivates
automatically based on your kindling usage:

- **Any `kindling` command** activates intel if it isn't already active
  (and hasn't been manually disabled)
- **After 1 hour of inactivity** (no kindling commands), the next
  invocation restores originals before re-activating with a fresh context
- **`kindling intel off`** disables auto-activation entirely — intel
  stays off until you explicitly run `kindling intel on`

This means your agent always has up-to-date context while you're actively
developing, and your config files are clean when you're not.

---

## Manual control

### Activate

```bash
kindling intel on
```

Activates intel immediately. If it was previously disabled with
`kindling intel off`, this clears the disable flag and re-enables
auto-lifecycle.

### Deactivate

```bash
kindling intel off
```

Restores all original agent config files and sets a disable flag so
auto-lifecycle won't re-activate until you explicitly run
`kindling intel on`.

### Check status

```bash
kindling intel status
```

Shows whether intel is active, which agent files were written, and
when the last interaction occurred.

---

## What the context file contains

The generated context includes:

- **Architectural principles** — deploy with `kindling deploy`, builds
  use Kaniko, dependencies go in the DSE YAML, secrets go through
  `kindling secrets set`
- **Dependency auto-injection table** — which env vars are injected for
  each dependency type (e.g. `postgres` → `DATABASE_URL`)
- **CLI reference** — every command with a one-line description
- **Key files** — where workflows, environment specs, and context files live
- **Secrets flow** — `kindling secrets set` → K8s Secret → `secretKeyRef`
- **Build protocol** — source tarball → Kaniko → `localhost:5001` → deploy
- **Kaniko compatibility notes** — no BuildKit ARGs, no `.git` directory,
  Poetry needs `--no-root`, npm needs cache redirect
- **Project-specific details** — detected languages, Dockerfiles found,
  CI workflow status

---

## Dashboard integration

The [web dashboard](dashboard.md) includes an **Agent Intel** card on
the overview page. From the browser you can:

- See whether intel is active or inactive
- Toggle intel on/off with a single click
- View which agent config files are managed

The dashboard also has a **Generate Workflow** command in the command
menu (⌘K) that streams AI workflow generation output in real time.

---

## Files created

| File | Purpose |
|---|---|
| `.kindling/intel-state.json` | Tracks active state, backups, written files, last interaction |
| `.kindling/intel-disabled` | Present when auto-lifecycle is disabled |
| `*.kindling-backup` | Backup of the original agent config (alongside the original) |

:::tip
Add `.kindling/` to your `.gitignore` — it's local state that shouldn't
be committed. The agent config files themselves (like
`.github/copilot-instructions.md`) **are** committed, so your team
benefits from the context too.
:::

---

## Examples

```bash
# Activate intel for all detected agents
kindling intel on

# Check what's active
kindling intel status

# Disable and restore originals
kindling intel off

# Intel also activates automatically with any command:
kindling status    # ← intel activates in the background
kindling deploy    # ← context refreshes automatically
```
