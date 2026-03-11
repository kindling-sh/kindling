---
sidebar_position: 14
title: CI Runners
description: Register self-hosted CI runners that build and deploy inside your Kind cluster.
---

# CI Runners

kindling provisions self-hosted GitHub Actions runners directly in your Kind
cluster so CI jobs execute locally — no cloud minutes, no queuing.

:::tip Dashboard
You can also register runners from the dashboard: **Setup → Runners**, or press **⌘K** and type "runner". See [Dashboard](dashboard.md) for details.
:::

## Register a runner

```bash
kindling runners -u <github-user> -r <repo> -t <pat>
```

| Flag | Description |
|------|-------------|
| `-u, --username` | GitHub username or org |
| `-r, --repo` | Repository name |
| `-t, --token` | Personal Access Token with `repo` scope |
| `--provider` | CI provider — `github` (default) or `gitlab` |

All flags are optional on the command line; the CLI prompts for any
missing values interactively.

### What happens

1. A Kubernetes secret `github-runner-token` is created with your PAT.
2. A `GithubActionRunnerPool` CR is applied — the operator starts a
   runner pod that registers with your repository.
3. Pushes to the repo (or `kindling push`) trigger builds that run
   on this local runner.

## Check runner status

```bash
kindling status
```

The **Runners** section shows registered pools and their ready state.
You can also see runner pods on the [dashboard](dashboard.md) **Runners** page
(`kindling dashboard` → Setup → Runners).

## Remove runners

```bash
kindling reset
```

This deletes the runner pool CR and the token secret while keeping
the Kind cluster intact. To tear down everything including the
cluster, use `kindling destroy`.

## How builds work

Source is tarballed and sent to a **Kaniko** sidecar inside the runner
pod. The built image is pushed to the in-cluster registry at
`localhost:5001` and the deployment is patched with the new image tag.

No Docker daemon is required — everything runs inside the cluster.

## GitLab CI

Pass `--provider gitlab` to register a GitLab runner instead.
The flow is identical but uses a GitLab runner token.

---

## Examples

### Simple — single repo

```bash
kindling runners -u jeff-vincent -r myorg/myapp -t ghp_abc123
```

One runner registers with `myorg/myapp`. Push to the repo and CI runs locally.

### Multi-service — shared runner across repos

```bash
kindling runners -u jeff-vincent -r myorg/frontend -t ghp_abc123
kindling runners -u jeff-vincent -r myorg/api -t ghp_abc123
```

Each repo gets its own runner pool. Both build inside the same Kind cluster.

### GitLab project

```bash
kindling runners -u jeff-vincent -r mygroup/myproject -t glrt-abc123 --provider gitlab
```

Same flow, different platform. The runner registers with GitLab instead of GitHub.
