---
sidebar_position: 1
title: Quickstart
description: Go from zero to a deployed app on the public internet in 5 minutes.
---

# Quickstart

Zero to a running app — publicly accessible on the internet — in 5 minutes.

## Install

```bash
brew install kindling-sh/tap/kindling
```

:::tip
This installs `kindling`, `kind`, and `kubectl` automatically. That's it — one command, all dependencies handled.
:::

## Bootstrap

```bash
kindling init
```

Creates a local Kubernetes cluster with an in-cluster container registry, ingress controller, and the kindling operator — all in one shot.

## Connect a GitHub repo

You need a [GitHub Personal Access Token](https://github.com/settings/tokens) with the **repo** scope.

```bash
kindling runners -u <github-user> -r <owner/repo> -t <pat>
```

This registers a self-hosted GitHub Actions runner in your cluster, bound to your repo.

## Generate a workflow

```bash
kindling generate -k <openai-api-key> -r /path/to/your-app
```

Scans your repo — Dockerfiles, docker-compose, Helm charts, source code — and writes a complete `.github/workflows/dev-deploy.yml` using AI.

:::note
Works with OpenAI (default) or Anthropic (`--provider anthropic`). Your app needs a working Dockerfile.
:::

## Push and deploy

```bash
cd /path/to/your-app
git add -A && git commit -m "add kindling workflow" && git push
```

Your laptop builds the image, deploys it with auto-provisioned dependencies (Postgres, Redis, etc.), and wires up ingress — all locally.

## Access your app

```bash
curl http://<your-user>-<your-app>.localhost
```

### Want a public URL?

```bash
kindling expose
```

Instantly creates an HTTPS tunnel. Share the URL with anyone.

---

## What just happened?

```
brew install → kindling init → kindling runners → kindling generate → git push → app running
     ↓              ↓                ↓                   ↓               ↓           ↓
  CLI + deps    K8s cluster     GH Actions runner    AI workflow    Local build   localhost + tunnel
```

Every subsequent `git push` rebuilds and redeploys automatically. No cloud CI minutes. No Docker Hub. No YAML to write.

---

## Next steps

| Want to... | Guide |
|---|---|
| Manage API keys and secrets | [Secrets Management](secrets.md) |
| Set up OAuth callbacks | [OAuth & Tunnels](oauth-tunnels.md) |
| Deploy without GitHub Actions | [Manual Deploy](guides/manual-deploy.md) |
| See all 15 dependency types | [Dependency Reference](dependencies.md) |
| Understand the internals | [Architecture](architecture.md) |
