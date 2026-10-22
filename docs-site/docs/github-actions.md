---
sidebar_position: 4
title: GitHub Actions Reference
description: Documentation for kindling-build and kindling-deploy reusable composite actions.
---

# GitHub Actions Reference

kindling ships three **reusable composite actions** that replace 15+ lines
of signal-file boilerplate with a single `uses:` step. These actions
live in the kindling repo under `.github/actions/` and are referenced as:

```
kindling-sh/kindling/.github/actions/kindling-build@main
kindling-sh/kindling/.github/actions/kindling-deploy@main
kindling-sh/kindling/.github/actions/kindling-snapshot-deploy@main
```

---

## kindling-build

Build and push a container image via the Kaniko build-agent sidecar.

:::warning Dockerfile required
This action runs the `Dockerfile` found in the build context directory as-is using Kaniko. It does **not** generate or modify Dockerfiles. Each service must have a working Dockerfile that builds successfully on its own (e.g. `docker build .`). If it doesn't build locally, it won't build in kindling.
:::

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `name` | ✅ | — | Unique build name (used for signal files: `/builds/<name>.*`) |
| `context` | ✅ | — | Path to the build context directory |
| `image` | ✅ | — | Full image reference (`registry/name:tag`) |
| `exclude` | ❌ | `""` | `tar --exclude` patterns (space-separated, e.g. `"./ui ./.git"`) |
| `dockerfile` | ❌ | `""` | Path to Dockerfile relative to build context (e.g. `api/Dockerfile`). If omitted, Kaniko uses the default `Dockerfile` in the context root. |
| `timeout` | ❌ | `300` | Max seconds to wait for the build to complete |

### What it does

1. Creates a tarball of the build context at `/builds/<name>.tar.gz`
   (with optional `--exclude` patterns)
2. Writes the target image to `/builds/<name>.dest`
3. If `dockerfile` is specified, writes the path to `/builds/<name>.dockerfile`
4. Touches `/builds/<name>.request` to trigger the Kaniko sidecar
5. Polls for `/builds/<name>.done` (up to `timeout` seconds)
6. Checks `/builds/<name>.exitcode` — exits non-zero on failure

### Usage

```yaml
- name: Build my-app
  uses: kindling-sh/kindling/.github/actions/kindling-build@main
  with:
    name: my-app
    context: ${{ github.workspace }}
    image: "registry:5000/my-app:${{ github.sha }}"
```

### With exclusions

```yaml
- name: Build API (exclude UI directory)
  uses: kindling-sh/kindling/.github/actions/kindling-build@main
  with:
    name: my-api
    context: ${{ github.workspace }}
    image: "registry:5000/my-api:${{ github.sha }}"
    exclude: "./ui ./.git ./node_modules"
```

### With custom Dockerfile path

When the Dockerfile is not at the context root (common in monorepos or
projects using docker-compose with `context: .` and `dockerfile: api/Dockerfile`):

```yaml
- name: Build API (Dockerfile in subdirectory)
  uses: kindling-sh/kindling/.github/actions/kindling-build@main
  with:
    name: my-api
    context: ${{ github.workspace }}
    image: "registry:5000/my-api:${{ github.sha }}"
    dockerfile: "api/Dockerfile"
```

---

## kindling-deploy

Deploy a DevStagingEnvironment CR via the build-agent sidecar.

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `name` | ✅ | — | DSE `metadata.name` (typically `<actor>-<service>`) |
| `image` | ✅ | — | Container image reference |
| `port` | ✅ | — | Container port (string) |
| `labels` | ❌ | `""` | Extra labels as YAML block |
| `env` | ❌ | `""` | Extra env vars as YAML block |
| `dependencies` | ❌ | `""` | Dependencies as YAML block |
| `ingress-host` | ❌ | `""` | Ingress hostname (omit to skip ingress) |
| `ingress-class` | ❌ | `traefik` | Ingress class name |
| `routes` | ❌ | `""` | Extra path → service routes merged onto this DSE's Ingress, as a YAML block (requires `ingress-host`) |
| `health-check-path` | ❌ | `/healthz` | HTTP health check path |
| `replicas` | ❌ | `1` | Number of replicas |
| `service-type` | ❌ | `ClusterIP` | Service type |
| `wait` | ❌ | `true` | Wait for deployment rollout |
| `wait-timeout` | ❌ | `180s` | Rollout wait timeout |

### What it does

1. Generates a complete `DevStagingEnvironment` YAML manifest from the inputs
2. Writes it to `/builds/<name>-dse.yaml`
3. Touches `/builds/<name>-dse.apply` to trigger sidecar `kubectl apply`
4. Waits for the sidecar to confirm the apply succeeded
5. Optionally waits for the Deployment rollout to complete

### Basic usage

```yaml
- name: Deploy my-app
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-app"
    image: "registry:5000/my-app:${{ github.sha }}"
    port: "8080"
    ingress-host: "${{ github.actor }}-my-app.localhost"
```

### With dependencies

```yaml
- name: Deploy with Postgres + Redis
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-app"
    image: "registry:5000/my-app:${{ github.sha }}"
    port: "8080"
    ingress-host: "${{ github.actor }}-my-app.localhost"
    dependencies: |
      - type: postgres
        version: "16"
      - type: redis
```

### With env vars

```yaml
- name: Deploy with environment variables
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-my-ui"
    image: "registry:5000/my-ui:${{ github.sha }}"
    port: "80"
    health-check-path: "/"
    ingress-host: "${{ github.actor }}-my-ui.localhost"
    env: |
      - name: API_URL
        value: "http://${{ github.actor }}-my-api:8080"
      - name: NODE_ENV
        value: "development"
```

### With extra ingress routes

Route additional paths on the same Ingress to other services — useful for
a frontend that needs `/api` and `/auth` proxied to backend services
without standing up separate Ingresses:

```yaml
- name: Deploy frontend
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-frontend"
    image: "registry:5000/frontend:${{ github.sha }}"
    port: "80"
    ingress-host: "${{ github.actor }}-frontend.localhost"
    routes: |
      - path: /api
        service: "${{ github.actor }}-my-api"
        port: 8080
      - path: /auth
        service: "${{ github.actor }}-auth"
        port: 8081
        pathType: Exact
```

---

## kindling-snapshot-deploy

Run `kindling snapshot --deploy` (the default `mode`) — image copy to a
real registry plus a Helm install against a separate staging cluster — or
`kindling snapshot --destroy` (`mode: destroy`, see below) to tear that
same deployment back down, from a self-hosted runner, via the same
build-agent sidecar.

:::warning Requires an opted-in CIRunnerPool
This action only works on a `CIRunnerPool` with `spec.enableSnapshotDeploy: true`
and `spec.localClusterName` set — that swaps the sidecar's image for one
that also has `helm`, `crane`, and the `kindling` CLI installed, and adds
the `.snapshot-deploy` signal handler to its polling loop. Pools that
don't set this flag keep the plain `kubectl`-only sidecar and can't run
this action.
:::

:::info Registry auth and --creds-config secrets need spec.buildAgentEnv
The build-agent sidecar is a long-running container with its own
environment — it doesn't inherit the triggering workflow's `env:`/
`secrets.*` context. Any credential `kindling snapshot` resolves via an
environment variable (`KINDLING_REGISTRY_PASSWORD` for an authenticated
`--registry` push, or anything a `--creds-config` entry's `fromEnv`
references) has to already be set on the `CIRunnerPool`'s
`spec.buildAgentEnv`, always via a `secretKeyRef`:

```yaml
spec:
  enableSnapshotDeploy: true
  localClusterName: kindling
  buildAgentEnv:
    - name: KINDLING_REGISTRY_USERNAME
      valueFrom:
        secretKeyRef:
          name: registry-credentials
          key: username
    - name: KINDLING_REGISTRY_PASSWORD
      valueFrom:
        secretKeyRef:
          name: registry-credentials
          key: password
```
:::

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `name` | ✅ | — | Unique request name (used for signal files: `/builds/<name>.*`) |
| `mode` | ❌ | `deploy` | `deploy` runs `--deploy`; `destroy` runs `--destroy` instead, tearing down the staging release/resources this branch created |
| `registry` | ❌† | `""` | Target container registry (passed through as `--registry`) — †required when `mode` is `deploy` (the action fails fast if empty), ignored and not required when `mode` is `destroy` |
| `staging-context` | ✅ | — | Kubeconfig context name for the staging cluster (passed through as `--context`) |
| `staging-kubeconfig` | ✅ | — | Full kubeconfig YAML for the staging cluster — pass `${{ secrets.STAGING_KUBECONFIG }}`, never a literal value |
| `extra-args` | ❌ | `""` | Additional `kindling snapshot` flags. In `deploy` mode: e.g. `--creds-config`, `--namespace`, `--ingress-services`, `--render-prod-values`. In `destroy` mode: e.g. `--branch`, `--name`, `--namespace`, `--format` overrides to match whatever `--deploy` originally used. `--deploy`/`--registry`/`--context`/`--non-interactive` (deploy) or `--destroy`/`--context` (destroy) are always added automatically, and in deploy mode `--output` is always forced to a path under `/builds` (see below) — an `--output` in `extra-args` is silently overridden, since the chart-extraction step depends on knowing exactly where the chart landed. |
| `timeout` | ❌ | `600` | Max seconds to wait for the deploy to complete (ignored in destroy mode) |

### Outputs

| Output | Description |
|---|---|
| `chart-path` | Path, in the GitHub Actions workspace (not `/builds`), to the exported chart — includes `values-prod.yaml` and `MISSING_CREDENTIALS.md` when `--render-prod-values` was passed and/or credentials were missing. Empty if the deploy failed before the chart was exported. |

### What it does

1. Writes the staging kubeconfig secret to `/builds/<name>.staging-kubeconfig`
   (mode `0600`) — the only place this value is ever written to disk
2. Writes the (non-secret) `kindling snapshot` flags to `/builds/<name>.snapshot-deploy-args`,
   forcing `--output /builds/<name>-chart`
3. Touches `/builds/<name>.snapshot-deploy` to trigger the sidecar
4. Polls for `/builds/<name>.snapshot-deploy-done` (up to `timeout` seconds)
5. Checks `/builds/<name>.snapshot-deploy-exitcode` — exits non-zero on failure
6. Copies `/builds/<name>-chart` into the GitHub Actions workspace (and
   removes the `/builds` copy) so later steps —
   `actions/upload-artifact@v4`, `git add`, `gh pr comment`, etc. — can
   use it without any knowledge of the `/builds` signal-file protocol.
   Runs even if the deploy step failed, so a partial chart (if any) is
   still inspectable; `chart-path` is empty if nothing was ever written.

The sidecar removes both the raw staging kubeconfig and the merged
kubeconfig it builds from it immediately after the `kindling snapshot`
subprocess exits, success or failure — neither ever outlives the single
triggering request.

### Usage

```yaml
- name: Deploy checkout to staging
  id: deploy
  uses: kindling-sh/kindling/.github/actions/kindling-snapshot-deploy@main
  with:
    name: checkout-staging
    registry: ghcr.io/myorg
    staging-context: staging
    staging-kubeconfig: ${{ secrets.STAGING_KUBECONFIG }}
    extra-args: "--creds-config deploy/staging-credentials.yaml --render-prod-values"

- name: Upload the production-ready chart
  if: steps.deploy.outputs.chart-path != ''
  uses: actions/upload-artifact@v4
  with:
    name: checkout-staging-chart
    path: ${{ steps.deploy.outputs.chart-path }}
```

### Tearing down a staging preview on PR close

`mode: destroy` reverses exactly what a prior `--deploy` run created for a
branch — `helm uninstall` (or, for `--format kustomize`, a label-selector
`kubectl delete`), resolving the identical branch-derived name/namespace
`--deploy` uses by default. Pair with a `pull_request: closed` trigger —
no `merged` guard needed, unlike `kindling-gitops-handoff` below, since an
abandoned/rejected PR's preview environment needs tearing down too:

```yaml
name: Staging Teardown
on:
  pull_request:
    types: [closed]

jobs:
  staging-teardown:
    runs-on: [self-hosted, "${{ github.actor }}"]
    steps:
      - uses: kindling-sh/kindling/.github/actions/kindling-snapshot-deploy@main
        with:
          name: teardown-${{ github.event.pull_request.number }}
          mode: destroy
          staging-context: staging
          staging-kubeconfig: ${{ secrets.STAGING_KUBECONFIG }}
```

Idempotent — a PR closed twice, or closed after a failed first deploy,
never fails this job: a missing Helm release (or no label-matching
resources, for Kustomize) is treated as "already torn down," not an error.
If the original deploy used a non-default `--branch`/`--name`/`--namespace`/
`--format`, pass the same overrides here via `extra-args` so the resolved
name/namespace actually matches what was deployed.

---

## kindling-pr-preview

Deploys a PR's branch to a shared staging cluster (via
`kindling-snapshot-deploy`, with `--render-prod-values` always added) and
posts a single PR comment with the resulting `values-prod.yaml` — editing
that same comment on every subsequent push, never posting a second one.
Also uploads the chart as a build artifact named `values-<pr-number>`, so
a later merge-triggered step can retrieve the *exact* artifact reviewers
already saw, rather than re-rendering.

:::info Requires pull-requests: write
The calling job needs `permissions: { pull-requests: write }` to post/edit
the comment. Everything else (the `enableSnapshotDeploy` `CIRunnerPool`
requirement, the `STAGING_KUBECONFIG` secret, etc.) is identical to
`kindling-snapshot-deploy` above, since this action calls it directly.
:::

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `name` | ✅ | — | Unique request name, passed through to `kindling-snapshot-deploy` |
| `pr-number` | ✅ | — | Pull request number to comment on — pass `${{ github.event.pull_request.number }}` |
| `registry` | ✅ | — | Target container registry |
| `staging-context` | ✅ | — | Kubeconfig context name for the staging cluster |
| `staging-kubeconfig` | ✅ | — | Full kubeconfig YAML — pass `${{ secrets.STAGING_KUBECONFIG }}` |
| `extra-args` | ❌ | `""` | Additional `kindling snapshot` flags — `--render-prod-values` is always added on top of whatever's here |
| `timeout` | ❌ | `600` | Max seconds to wait for the deploy to complete |

### Outputs

| Output | Description |
|---|---|
| `chart-path` | Same as `kindling-snapshot-deploy`'s output — pass-through |

### Usage

```yaml
name: Staging Preview
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  staging-preview:
    runs-on: [self-hosted, "${{ github.actor }}"]
    permissions:
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - uses: kindling-sh/kindling/.github/actions/kindling-pr-preview@main
        with:
          name: pr-${{ github.event.pull_request.number }}
          pr-number: ${{ github.event.pull_request.number }}
          registry: ghcr.io/myorg
          staging-context: staging
          staging-kubeconfig: ${{ secrets.STAGING_KUBECONFIG }}
```

Every subsequent push to the PR re-runs this same job (the `synchronize`
event type), redeploying under the same branch-derived name (stable
across pushes) and editing the same comment with the latest values.

---

## kindling-gitops-handoff

Commits the exact, already-reviewed `values-prod.yaml` + chart — the
`values-<pr-number>` artifact `kindling-pr-preview` uploaded — into a
GitOps repo on PR merge. Never re-renders at merge time: a fresh render
could subtly differ from what was actually reviewed (a dev-cluster value
that changed between the PR's last push and the merge, different map
iteration order, etc.), which would silently invalidate the review. Never
touches a live cluster, never resolves a credential, and never performs
the production deploy itself — stops at a git push (or a PR, with
`open-pr: true`) against the GitOps repo; whatever GitOps controller
already watches it (Argo CD, Flux, a platform team's own pipeline) takes
it from there.

:::warning The calling workflow owns the merged == true guard
A `pull_request: closed` event fires for PRs closed **without** merging
too. This action has no way to distinguish that on its own — the calling
job must gate on `if: github.event.pull_request.merged == true`, exactly
like the example below. Also requires `permissions: { actions: read }` (to
look up the artifact) on the current repo, and the token passed as
`gitops-token` needs push access (and `pull-requests: write`, if
`open-pr: true`) to whatever `gitops-repo` is — which may be an entirely
different, more tightly-scoped repo than the one the workflow runs in, so
this token is deliberately a separate input from the job's own
`GITHUB_TOKEN`.
:::

### Inputs

| Input | Required | Default | Description |
|---|---|---|---|
| `pr-number` | ✅ | — | The merged PR's number — pass `${{ github.event.pull_request.number }}` |
| `gitops-repo` | ❌ | *(current repo)* | `owner/repo` to commit into |
| `gitops-base-branch` | ❌ | `main` | Branch in `gitops-repo` to commit/PR against |
| `gitops-path` | ❌ | `environments/production` | Destination path within `gitops-repo` |
| `gitops-token` | ✅ | — | Token with push access to `gitops-repo` — pass `${{ secrets.GITOPS_PAT }}`, never a literal value |
| `open-pr` | ❌ | `"false"` | Open a PR against `gitops-base-branch` instead of committing directly to it |

### Outputs

| Output | Description |
|---|---|
| `promoted` | `"true"` if a commit was made (or PR opened), `"false"` if `gitops-repo` already had identical content (idempotent — safe to re-run) |

### Usage

```yaml
name: Promote to GitOps
on:
  pull_request:
    types: [closed]

jobs:
  promote:
    if: github.event.pull_request.merged == true
    runs-on: [self-hosted, "${{ github.actor }}"]
    permissions:
      actions: read
    steps:
      - uses: kindling-sh/kindling/.github/actions/kindling-gitops-handoff@main
        with:
          pr-number: ${{ github.event.pull_request.number }}
          gitops-repo: myorg/gitops-production
          gitops-token: ${{ secrets.GITOPS_PAT }}
```

Closing a PR without merging never triggers this job at all, since the
`if:` guard is evaluated before any step runs.

---

## promote-pr.yml (manual promote, for the dashboard's "Promote now")

The kindling dashboard's PR-preview page (see its own docs) can trigger a
promote **without** waiting for a real merge event — useful for testing
the GitOps wiring itself, re-promoting after fixing an unrelated problem,
or promoting a branch that won't go through a normal PR-merge flow. It
does this by dispatching a `workflow_dispatch`-triggered workflow that
calls the *exact same* `kindling-gitops-handoff` action the merge-
triggered job above already uses — the dashboard never talks to the
GitOps repo directly, and never re-implements the artifact-fetch/commit
logic itself, so a manual promote and a real merge's automatic one are
provably identical (same action, same inputs, same artifact).

Add this workflow to any repo you want the dashboard's "Promote now"
button to work against — the dashboard looks for a workflow file named
exactly `promote-pr.yml` by default (configurable from the dashboard's
own GitHub settings if you need a different filename):

```yaml
# .github/workflows/promote-pr.yml
name: Promote PR (manual)

on:
  workflow_dispatch:
    inputs:
      pr_number:
        description: "PR number to promote (must already have a kindling-pr-preview artifact)"
        required: true

permissions:
  actions: read

jobs:
  promote:
    runs-on: ubuntu-latest
    steps:
      - uses: kindling-sh/kindling/.github/actions/kindling-gitops-handoff@main
        with:
          pr-number: ${{ inputs.pr_number }}
          gitops-repo: myorg/gitops-production
          gitops-token: ${{ secrets.GITOPS_PAT }}
```

The dashboard dispatches this workflow via the GitHub API
(`POST /repos/{owner}/{repo}/actions/workflows/promote-pr.yml/dispatches`),
then polls the resulting run until it completes, surfacing the same
`promoted` outcome the merge-triggered job above would have produced for
an identical PR number — because it's the same action, retrieving the
same `values-<pr-number>` artifact, either way.

---

## Complete workflow example

A full workflow using both actions:

```yaml
name: Dev Deploy
on:
  push:
    branches: [main]

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]
    env:
      TAG: ${{ github.sha }}
    steps:
      - uses: actions/checkout@v4

      - name: Clean builds directory
        run: rm -f /builds/*

      # ── Build ──────────────────────────────────────────────
      - name: Build API image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: my-api
          context: ${{ github.workspace }}
          image: "registry:5000/my-api:${{ env.TAG }}"
          exclude: "./ui"

      - name: Build UI image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: my-ui
          context: "${{ github.workspace }}/ui"
          image: "registry:5000/my-ui:${{ env.TAG }}"

      # ── Deploy ─────────────────────────────────────────────
      - name: Deploy API
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-api"
          image: "registry:5000/my-api:${{ env.TAG }}"
          port: "8080"
          ingress-host: "${{ github.actor }}-my-api.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis

      - name: Deploy UI
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-ui"
          image: "registry:5000/my-ui:${{ env.TAG }}"
          port: "80"
          health-check-path: "/"
          ingress-host: "${{ github.actor }}-my-ui.localhost"
          env: |
            - name: API_URL
              value: "http://${{ github.actor }}-my-api:8080"
```

---

## Troubleshooting

### Build times out

Increase the timeout:

```yaml
with:
  timeout: "600"  # 10 minutes
```

### Deploy apply fails

Check the output for:
- Invalid YAML indentation
- Missing required fields
- Image not found in registry

### Rollout doesn't complete

Check pod events:

```bash
kubectl describe pod -l app.kubernetes.io/name=<name>
kubectl logs -l app.kubernetes.io/name=<name>
```

Common causes:
- Image pull errors (image not pushed to registry:5000)
- Health check failing (wrong path or port)
- Dependency not ready (database still starting)
