---
sidebar_position: 1
title: Analyze
description: Check your project's readiness before generating a CI workflow.
---

# Analyze

`kindling analyze` is the first step in the kindling journey. Run it before
`kindling generate` to check your project's readiness.

```bash
kindling analyze
```

## What it checks

### Dockerfiles

Finds all Dockerfiles in your repo and checks Kaniko compatibility:

- No BuildKit-only features (`TARGETARCH`, `BUILDPLATFORM`, etc.)
- No `RUN --mount` syntax (safe to include, but caching is ignored)
- Go builds should use `-buildvcs=false` (no `.git` directory in Kaniko)
- Poetry builds should use `--no-root`
- npm builds should redirect cache: `ENV npm_config_cache=/tmp/.npm`

### Dependencies

Detects backing services from source code, config files, and Dockerfiles:

- **Databases:** Postgres, MySQL, MongoDB
- **Caches:** Redis, Memcached
- **Message queues:** RabbitMQ, Kafka, NATS
- **Search:** Elasticsearch
- **Storage:** MinIO (S3-compatible)

### Secrets

Scans for external credentials your app references:
- `*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_DSN`
- Suggests `kindling secrets set` for each one

### Agent architecture

Detects AI/ML frameworks and multi-agent patterns:
- **Frameworks:** LangChain, CrewAI, LangGraph, OpenAI Agents SDK
- **MCP servers:** Model Context Protocol server detection
- **Inter-service calls:** HTTP communication between services
- **Concurrent agents:** Multiple agent processes that need shared infrastructure

### Build context alignment

Verifies that COPY/ADD paths in each Dockerfile align with the expected
build context. Catches mismatches before they cause Kaniko build failures.

## Example output

```
📁 Repository: /path/to/your-app
──────────────────────────────

🐳 Dockerfiles
  ✅ ./Dockerfile (Kaniko-compatible)
  ✅ ./agent/Dockerfile (Kaniko-compatible)

📦 Dependencies
  • postgres 16
  • redis (latest)

🔑 Secrets
  ⚠️  OPENAI_API_KEY — run: kindling secrets set OPENAI_API_KEY <value>
  ⚠️  STRIPE_KEY — run: kindling secrets set STRIPE_KEY <value>

🤖 Agent Frameworks
  • LangChain detected in agent/

✅ Ready for 'kindling generate'
```

## Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--repo-path` | `-r` | `.` | Path to the local repository to analyze |
| `--verbose` | `-v` | `false` | Show additional detail |

## After analyze

Once your project passes analysis:

1. **Set any secrets** it detected:
   ```bash
   kindling secrets set OPENAI_API_KEY sk-...
   ```

2. **Generate a workflow:**
   ```bash
   kindling generate -k <api-key> -r .
   ```

3. **Push and deploy:**
   ```bash
   git push origin main
   ```

## Coming soon: scaffold

For new projects that don't have Dockerfiles yet, `kindling scaffold`
will generate them automatically. Stay tuned.
