---
sidebar_position: 13
title: AI Workflow Generation
description: Auto-generate GitHub Actions workflows tailored to your repo using AI.
---

# AI Workflow Generation

`kindling generate` scans your repository and uses an LLM to produce a
complete GitHub Actions workflow — build steps, deploy steps,
dependencies, secrets, ingress routes — tailored to what's actually in
your code.

---

## Quick start

```bash
kindling generate -k <openai-api-key> -r /path/to/your-app
```

This writes `.github/workflows/dev-deploy.yml` into your repo. Push it
and your app builds and deploys automatically.

```bash
# Preview without writing
kindling generate -k sk-... -r . --dry-run

# Use Anthropic instead of OpenAI
kindling generate -k sk-ant-... -r . --provider anthropic
```

---

## What it detects

The scanner reads your repo structure and extracts:

- **Services** — each directory with a Dockerfile becomes a build + deploy step
- **Languages** — Go, TypeScript, Python, Java, Rust, Ruby, PHP, C#, Elixir
- **Ports** — from Dockerfile EXPOSE, framework defaults, and config files
- **Health check endpoints** — `/healthz`, `/health`, `/ready`, framework conventions
- **Dependencies** — Postgres, Redis, MongoDB, RabbitMQ, etc. from docker-compose, env vars, and import analysis
- **External credentials** — `*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_DSN` patterns → suggests `kindling secrets set` for each
- **OAuth/OIDC** — Auth0, Okta, Firebase Auth, NextAuth, Passport.js patterns → suggests `kindling expose`

---

## Smart scanning

### docker-compose.yml

If present, docker-compose is used as the primary source of truth:
build contexts, `depends_on` for dependency types, and `environment`
sections for env var mappings across services.

### Helm charts

Detects `Chart.yaml`, runs `helm template` to render manifests, and
passes them to the AI as authoritative context. Falls back gracefully
if `helm` is not installed.

### Kustomize overlays

Detects `kustomization.yaml`, runs `kustomize build` for rendered
context. Falls back gracefully if `kustomize` is not installed.

### .env template files

Scans `.env.sample`, `.env.example`, `.env.development`, and
`.env.template` for required configuration variables.

### Ingress heuristics

Only user-facing services (frontends, SSR apps, API gateways) get
ingress routes by default. Use `--ingress-all` to override.

---

## Models

| Provider | Default model | Notes |
|---|---|---|
| OpenAI | `o3` | Reasoning model — uses `developer` role and extended thinking |
| OpenAI | `o3-mini` | Faster and cheaper reasoning |
| OpenAI | `gpt-4o` | Standard chat model |
| Anthropic | `claude-sonnet-4-20250514` | Default for `--provider anthropic` |

```bash
# Use a specific model
kindling generate -k sk-... -r . --model o3-mini
```

---

## Examples

```bash
# Default (OpenAI o3)
kindling generate -k sk-... -r /path/to/my-app

# Anthropic
kindling generate -k sk-ant-... -r . --provider anthropic

# Custom output path
kindling generate -k sk-... -r . -o ./my-workflow.yml

# Wire every service with ingress
kindling generate -k sk-... -r . --ingress-all

# Skip Helm/Kustomize rendering
kindling generate -k sk-... -r . --no-helm
```

---

## Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--api-key` | `-k` | — (required) | GenAI API key |
| `--repo-path` | `-r` | `.` | Path to the repository to analyze |
| `--provider` | | `openai` | AI provider: `openai` or `anthropic` |
| `--model` | | auto | Model name |
| `--output` | `-o` | `<repo>/.github/workflows/dev-deploy.yml` | Output path |
| `--dry-run` | | `false` | Print to stdout instead of writing |
| `--ingress-all` | | `false` | Give every service an ingress route |
| `--no-helm` | | `false` | Skip Helm/Kustomize rendering |
