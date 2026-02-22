---
sidebar_position: 7
title: Secrets Management
description: Managing API keys, tokens, and credentials across cluster rebuilds.
---

# Secrets Management

`kindling secrets` manages external credentials — API keys, tokens, DSNs,
passwords — as Kubernetes Secrets in the Kind cluster. A local backup
file ensures credentials survive cluster rebuilds.

---

## Quick start

```bash
# Store a credential
kindling secrets set STRIPE_KEY sk_live_abc123

# List managed secrets (names only — values never printed)
kindling secrets list

# Remove a secret
kindling secrets delete STRIPE_KEY

# After cluster rebuild, restore all secrets
kindling secrets restore
```

---

## How it works

### Storage

Each secret is stored as a Kubernetes Secret:

| Property | Value |
|---|---|
| **Name** | `kindling-secret-<lowercase-name>` (e.g. `kindling-secret-stripe-key`) |
| **Namespace** | `default` |
| **Label** | `app.kubernetes.io/managed-by=kindling` |
| **Data key** | `value` |

Example: `kindling secrets set STRIPE_KEY sk_live_abc123` creates:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: kindling-secret-stripe-key
  labels:
    app.kubernetes.io/managed-by: kindling
type: Opaque
data:
  value: c2tfbGl2ZV9hYmMxMjM=   # base64 of sk_live_abc123
```

### Local backup

Every `set` and `delete` operation also updates `.kindling/secrets.yaml`
in the current directory. This file stores base64-encoded values so
secrets survive `kindling destroy` + `kindling init` cycles.

```yaml
# .kindling/secrets.yaml — managed by kindling, do not edit
STRIPE_KEY: c2tfbGl2ZV9hYmMxMjM=
OPENAI_API_KEY: c2stLi4u
```

The `.kindling/` directory is automatically added to `.gitignore` to
prevent accidental commits.

### Restore flow

After rebuilding the cluster:

```bash
kindling init
kindling secrets restore    # reads .kindling/secrets.yaml → creates K8s Secrets
```

---

## Integration with `kindling generate`

### Automatic credential detection

During `kindling generate`, the repo scanner looks for environment
variable references matching external credential patterns:

**Suffix matches:**
`*_API_KEY`, `*_APIKEY`, `*_SECRET`, `*_SECRET_KEY`, `*_TOKEN`,
`*_ACCESS_TOKEN`, `*_AUTH_TOKEN`, `*_DSN`, `*_CONNECTION_STRING`,
`*_PASSWORD`, `*_CLIENT_ID`, `*_CLIENT_SECRET`, `*_PRIVATE_KEY`,
`*_SIGNING_KEY`, `*_WEBHOOK_SECRET`

**Exact matches:**
`DATABASE_URL`, `REDIS_URL`, `MONGO_URL`, `STRIPE_KEY`,
`SENDGRID_API_KEY`, `AWS_SECRET_ACCESS_KEY`, `GITHUB_TOKEN`,
`SENTRY_DSN`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and more.

The scanner checks:
- Source files (Go, TypeScript, Python, etc.)
- Dockerfiles (`ENV` directives)
- `docker-compose.yml` (`environment:` sections)
- `.env`, `.env.example`, `.env.sample`, `.env.development`, `.env.local`

### CLI output

When credentials are detected, you'll see:

```
  🔑 Detected 3 external credential(s): OPENAI_API_KEY, SENTRY_DSN, STRIPE_KEY
  💡 Run: kindling secrets set <NAME> <VALUE> for each
```

### Generated workflow

The AI wires detected credentials using `secretKeyRef`:

```yaml
env: |
  # Requires: kindling secrets set STRIPE_KEY <value>
  STRIPE_KEY:
    secretKeyRef:
      name: kindling-secret-stripe-key
      key: value
```

---

## Naming convention

The env var name is converted to a K8s Secret name:

| Env var | K8s Secret name |
|---|---|
| `STRIPE_KEY` | `kindling-secret-stripe-key` |
| `OPENAI_API_KEY` | `kindling-secret-openai-api-key` |
| `DATABASE_URL` | `kindling-secret-database-url` |
| `AWS_SECRET_ACCESS_KEY` | `kindling-secret-aws-secret-access-key` |

Rule: lowercase the name, replace `_` with `-`, prefix with `kindling-secret-`.

---

## Command reference

### `kindling secrets set <name> <value>`

Creates or updates a K8s Secret and writes to the local backup.

```bash
kindling secrets set STRIPE_KEY sk_live_abc123
```

### `kindling secrets list`

Lists all secrets labeled `app.kubernetes.io/managed-by=kindling`:

```bash
kindling secrets list
```

Output:

```
  🔐 Managed secrets:
     • STRIPE_KEY (kindling-secret-stripe-key)
     • OPENAI_API_KEY (kindling-secret-openai-api-key)
```

### `kindling secrets delete <name>`

Removes the K8s Secret and the entry from the local backup:

```bash
kindling secrets delete STRIPE_KEY
```

### `kindling secrets restore`

Reads `.kindling/secrets.yaml` and creates K8s Secrets for every entry:

```bash
kindling secrets restore
```

Use this after `kindling destroy` + `kindling init` to restore all
credentials without re-entering them.
