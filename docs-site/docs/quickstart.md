---
sidebar_position: 1
title: Quickstart
description: Go from zero to a deployed app on localhost in 5 minutes.
---

# Quickstart

Go from nothing to a running app in three phases: **analyze → generate → dev loop**.

---

## 1. Install & bootstrap

```bash
brew install kindling-sh/tap/kindling
kindling init
```

:::tip
`brew install` installs `kindling`, `kind`, and `kubectl` automatically. `kindling init` creates a local Kubernetes cluster with a container registry, ingress controller, and the kindling operator — all in one shot.
:::

## 2. Connect a CI runner

kindling supports **GitHub Actions** and **GitLab CI**.

```bash
# GitHub (needs a PAT with repo scope)
kindling runners -u <user> -r <owner/repo> -t <pat>

# GitLab
kindling runners --ci-provider gitlab -u <user> -r <group/project> -t <token>
```

---

## 3. Analyze your project

Before generating anything, check your project's readiness:

```bash
kindling analyze
```

This scans your repo and reports:
- Dockerfiles found and Kaniko compatibility
- Dependencies detected (Postgres, Redis, etc.)
- Secrets and credentials your app needs
- Agent frameworks (LangChain, CrewAI, etc.) and MCP servers
- Build context alignment between Dockerfiles and workflow

Fix any issues it flags, then move to generate.

## 4. Generate a workflow

```bash
kindling generate -k <api-key> -r .
```

AI-generates a complete CI workflow (`.github/workflows/dev-deploy.yml` or `.gitlab-ci.yml`). Detects services, languages, ports, dependencies, health checks, and secrets.

:::note
Works with OpenAI (default) or Anthropic (`--ai-provider anthropic`). Preview first with `--dry-run`.
:::

## 5. Push and deploy

```bash
git add -A && git commit -m "add kindling workflow" && git push
```

The runner picks up the job, Kaniko builds images in-cluster, the operator provisions dependencies, and ingress routes go live:

```bash
kindling status
curl http://<your-user>-my-app.localhost
```

## 6. Start the dev loop

Now iterate without pushing to git:

```bash
# Sub-second live sync — edit, save, see changes instantly
kindling sync -d <your-user>-my-app --restart

# Or open the visual dashboard
kindling dashboard
```

When you stop sync (Ctrl+C), the deployment automatically rolls back to its pre-sync state.

### Need a public URL?

```bash
kindling expose
```

Creates an HTTPS tunnel instantly — useful for OAuth callbacks, webhooks, or sharing with teammates.

---

## Try the demo app (optional)

Don't have a project handy? Use the included microservices demo:

```bash
cp -r ~/.kindling/examples/microservices ~/kindling-demo
cd ~/kindling-demo
git init && git add -A && git commit -m "initial commit"
gh repo create kindling-demo --private --source . --push
```

This gives you a 4-service app (Go, Python, Node.js, React) with Postgres, Redis, and MongoDB — plus a pre-built workflow. No AI key needed.

---

## The journey

```
analyze → generate → dev loop → promote
   ↓          ↓          ↓          ↓
 readiness  workflow   push/sync  production
 check      via AI     iterate    (coming soon)
```

Every `git push` rebuilds and redeploys. `kindling sync` gives you sub-second iteration. No cloud CI minutes. No Docker Hub. No YAML by hand.

---

## Next steps

| Want to... | Guide |
|---|---|
| Give your coding agent kindling context | [Agent Intel](intel.md) |
| Manage API keys and secrets | [Secrets Management](secrets.md) |
| Set up OAuth callbacks | [OAuth & Tunnels](oauth-tunnels.md) |
| Deploy without GitHub Actions | [Manual Deploy](guides/manual-deploy.md) |
| See all 15 dependency types | [Dependency Reference](dependencies.md) |
| Understand the internals | [Architecture](architecture.md) |
