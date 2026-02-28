---
sidebar_position: 1
title: Quickstart
description: Go from zero to a deployed app on the public internet in 5 minutes.
---

# Quickstart

Set up CI for a new project in 5 minutes. Then keep building with live sync, secrets, tunnels, and a visual dashboard.

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

## Try the demo app

`kindling init` auto-clones the project to `~/.kindling`. Copy the included microservices demo — Go, Python, Node.js, and React — into a fresh directory:

```bash
cp -r ~/.kindling/examples/microservices ~/kindling-demo
```

This gives you a working 4-service app (API gateway, orders service, inventory service, React UI) with Postgres, Redis, and MongoDB — plus a pre-built GitHub Actions workflow (or run `kindling generate --provider gitlab` for GitLab CI). No AI key needed.

## Create a repo and push

```bash
cd ~/kindling-demo
git init && git add -A && git commit -m "initial commit"
gh repo create kindling-demo --private --source . --push
```

## Connect the runner

kindling supports **GitHub Actions** and **GitLab CI**.

### GitHub

You need a [GitHub Personal Access Token](https://github.com/settings/tokens) with the **repo** scope.

```bash
kindling runners -u <github-user> -r <github-user>/kindling-demo -t <pat>
```

### GitLab

You need a [GitLab runner registration token](https://docs.gitlab.com/ee/ci/runners/) for your project.

```bash
kindling runners --provider gitlab -u <gitlab-user> -r <group>/kindling-demo -t <token>
```

This registers a self-hosted CI runner in your cluster, bound to your repo. Push a change to trigger a build:

```bash
git commit --allow-empty -m "trigger build" && git push
```

## Watch it deploy

```bash
kindling status
```

The runner picks up the workflow, Kaniko builds all four images, the operator provisions Postgres, Redis, and MongoDB, and ingress routes go live:

```bash
curl http://<your-user>-ui.localhost
```

### Want a public URL?

```bash
kindling expose
```

Instantly creates an HTTPS tunnel. Share the URL with anyone.

---

## Use your own app

Once you've seen the demo, point kindling at your own repo:

```bash
kindling generate -k <openai-api-key> -r /path/to/your-app
```

Scans your repo — Dockerfiles, docker-compose, Helm charts, source code — and writes a complete CI workflow using AI (`.github/workflows/dev-deploy.yml` for GitHub, `.gitlab-ci.yml` for GitLab).

:::note
Works with OpenAI (default) or Anthropic (`--provider anthropic`). Use `--provider gitlab` for GitLab CI workflows. Your app needs a working Dockerfile.
:::

---

## What just happened?

```
brew install → kindling init → cp demo → create repo → git push → app running
     ↓              ↓              ↓            ↓               ↓           ↓
  CLI + deps    K8s cluster    Demo app    Git repo      Local build   localhost
```

Every subsequent `git push` rebuilds and redeploys automatically. No cloud CI minutes. No Docker Hub. No YAML by hand. Works with GitHub and GitLab.

---

## Next steps

| Want to... | Guide |
|---|---|
| Manage API keys and secrets | [Secrets Management](secrets.md) |
| Set up OAuth callbacks | [OAuth & Tunnels](oauth-tunnels.md) |
| Deploy without GitHub Actions | [Manual Deploy](guides/manual-deploy.md) |
| See all 15 dependency types | [Dependency Reference](dependencies.md) |
| Understand the internals | [Architecture](architecture.md) |
