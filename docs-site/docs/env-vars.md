---
sidebar_position: 12
title: Environment Variables
description: Set, list, and unset environment variables on running deployments without redeploying.
---

# Environment Variables

`kindling env` manages environment variables on running deployments
directly — no redeploy, no image rebuild. Changes take effect
immediately via a rolling restart of the affected pods.

---

## Quick start

```bash
# Set a variable
kindling env set myapp-dev LOG_LEVEL=debug

# Set multiple at once
kindling env set myapp-dev DATABASE_PORT=5432 REDIS_HOST=redis-svc LOG_LEVEL=debug

# List all env vars on a deployment
kindling env list myapp-dev

# Remove a variable
kindling env unset myapp-dev LOG_LEVEL
```

---

## How it works

Under the hood, `kindling env set` patches the deployment's container
spec with the new environment variables and Kubernetes performs a
rolling update. This means:

- No image rebuild required
- Existing pods are replaced gracefully
- The change is visible in `kubectl describe deployment`

---

## env vs. secrets

| | `kindling env` | `kindling secrets` |
|---|---|---|
| **Purpose** | Runtime config (ports, log levels, feature flags) | Sensitive credentials (API keys, tokens, DSNs) |
| **Storage** | Inline in deployment spec | Kubernetes Secret + local backup file |
| **Survives cluster rebuild** | ❌ No | ✅ Yes (via `kindling secrets restore`) |
| **Values visible** | Yes (`kindling env list`) | Never printed (`kindling secrets list` shows names only) |

Use `kindling secrets` for anything sensitive. Use `kindling env` for
everything else.

---

## Commands

### `kindling env set`

```bash
kindling env set <deployment> KEY=VALUE [KEY=VALUE ...]
```

Sets one or more environment variables on the specified deployment.

### `kindling env list`

```bash
kindling env list <deployment>
```

Lists all environment variables currently set on the deployment's
primary container.

### `kindling env unset`

```bash
kindling env unset <deployment> KEY [KEY ...]
```

Removes one or more environment variables from the deployment.
