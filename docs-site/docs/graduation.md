---
sidebar_position: 7
title: Graduating to a Staging Cluster
description: Take your dev environment to a persistent staging Kubernetes cluster with TLS, image push, and Helm deploy.
---

# Graduating to a Staging Cluster

Once your app runs reliably in the local Kind cluster, you can **graduate**
it to a persistent staging Kubernetes cluster — any cluster with a
kubeconfig context. This is the bridge from the disposable local Kind loop
to a shared, longer-lived environment; it is **not** a path to production.
Kindling never holds a production credential, never calls a production
cluster's API server, and never deploys to production on your behalf —
that handoff happens through a GitOps controller at the git boundary (see
the project's staging/production redesign notes for the full model).

kindling's graduation flow uses two commands:

| Command | Purpose |
|---|---|
| `kindling snapshot --deploy` | Generate a Helm chart, push images, and deploy |
| `kindling staging tls` | Set up TLS with cert-manager and Let's Encrypt |

:::tip Dashboard
You can also manage graduation from the dashboard: **Staging → Overview** and **Staging → Deploy**. See [Dashboard](dashboard.md) for details.
:::

:::caution Dependencies are not persistent
Dependencies provisioned by `kindling snapshot --deploy` (Postgres, Redis,
Mongo, etc.) are the same convenience, non-persistent Deployments used in
local dev — they run without durable storage and are not backed up. They're
fine for a staging cluster used to validate behavior before a real release,
but never point a staging deployment's dependencies at data you can't
afford to lose, and never treat this flow as a substitute for your
organization's actual production data infrastructure.
:::

---

## Prerequisites

Before graduating, make sure you have:

1. **A running Kind cluster** with your app deployed and healthy (`kindling status`)
2. **A staging Kubernetes cluster** with a kubeconfig context configured (`kubectl config get-contexts`)
3. **A container registry** you can push to (GHCR, ECR, Docker Hub, etc.)
4. **Registry authentication** (`docker login`, `aws ecr get-login-password`, etc.)
5. **Helm v3** installed (`brew install helm`)
6. **crane** installed for image copy (`brew install crane`)

---

## Step 1: Snapshot and deploy

The `kindling snapshot` command reads all DevStagingEnvironments from your
local Kind cluster and generates a staging-ready Helm chart. With
`--deploy`, it also pushes images and installs the chart on your staging
cluster in one step.

```bash
kindling snapshot \
  --registry ghcr.io/myorg \
  --deploy \
  --context my-staging-cluster
```

### What happens

1. **Reads cluster state** — discovers all DSEs, services, dependencies
2. **Strips dev prefixes** — `jeff-vincent-gateway` becomes `gateway`
3. **Derives a name/namespace/Ingress host from the current git branch** (unless `--name`/`--namespace`/an explicit `spec.ingress.host` are already set) — so concurrent branches never collide on the same shared staging cluster, and never end up with an unreachable environment
4. **Generates Helm chart** — templates, values.yaml, values-live.yaml
5. **Pushes images** — copies each image from `localhost:5001` to your registry using `crane copy`, tagged `<branch-slug>-N` by default (unless `--tag` is set) so concurrent branches don't share the same tag sequence on a shared registry
6. **Installs chart** — runs `helm upgrade --install` on the staging cluster

### Common flags

```bash
# Custom image tag (default: next sequential <branch-slug>-N)
kindling snapshot -r ghcr.io/myorg -t v1.2.0 --deploy --context my-staging

# Deploy into a specific namespace
kindling snapshot -r ghcr.io/myorg --deploy --context my-staging --namespace staging

# Custom chart name and output directory
kindling snapshot -r ghcr.io/myorg -n my-platform -o ./charts/staging --deploy --context my-staging

# Multiple PR branches on the same shared cluster, no collisions —
# each gets its own name/namespace derived from the branch (no --name/--namespace needed)
kindling snapshot -r ghcr.io/myorg --deploy --context my-staging
# ...or override which branch to derive from explicitly
kindling snapshot -r ghcr.io/myorg --deploy --context my-staging --branch feature/checkout-retry

# Give each branch a real, resolvable Ingress host too (requires a
# wildcard DNS/TLS setup on *.staging.example.com on the target cluster)
kindling snapshot -r ghcr.io/myorg --deploy --context my-staging --staging-domain example.com
# -> feature-checkout-retry.staging.example.com, unique per branch
```

### Generate without deploying

If you just want the chart (for CI pipelines, GitOps, etc.):

```bash
kindling snapshot -r ghcr.io/myorg
```

This produces a complete Helm chart in `./kindling-snapshot/` with images
tagged for your registry. Deploy it yourself with:

```bash
helm install my-app ./kindling-snapshot \
  --kube-context my-staging \
  --set gateway.env.DATABASE_URL=postgres://staging-host:5432/mydb
```

### Rendering values for production — without deploying anything

`--render-prod-values` writes a `values-prod.yaml` alongside the chart, for handing off to whatever process takes a chart the rest of the way into production (a GitOps controller, a platform team's own pipeline, `helm install` run by someone else with a production credential kindling never holds):

```bash
kindling snapshot -r ghcr.io/myorg --render-prod-values
```

This is the same clean-defaults `values.yaml` every service already gets — TODO placeholders for anything credential-shaped, dependency connection strings included — except each service's `image` is pinned to the exact digest that was just pushed (`registry/name@sha256:...`, never a mutable tag), and `KINDLING_ENV_PREFIX` (default `prod-`, override with `--prod-env-prefix`) is added to every service's env. Kindling never generates, resolves, or stores a real credential anywhere in this path — the chart's Deployment template already wires every secret-backed env var to a `secretKeyRef` against a chart-managed Secret, identically for staging and production since both deploy the same chart. Filling in the real value in that Secret (or wiring an external one) is entirely up to your own process, by design.

### Running this from CI, non-interactively

Everything above works unattended too — `--non-interactive` (also
auto-detected when stdin isn't a TTY) skips every prompt. Registry auth
comes from `--registry-username`/`KINDLING_REGISTRY_USERNAME` +
`KINDLING_REGISTRY_PASSWORD`; staging credentials resolve from
`--creds-config` (a committed YAML file mapping credential env vars to
where their staging value actually lives — almost always a reference to
an env var a CI job already populated from a secret, never a literal
value) with the dev-cluster value as an automatic fallback. Anything
genuinely unresolvable never fails the deploy — it's warned about and
written to `MISSING_CREDENTIALS.md` for a later workflow step to check:

```bash
kindling snapshot -r ghcr.io/myorg --deploy --context staging \
  --non-interactive --creds-config deploy/staging-credentials.yaml
```

A `CIRunnerPool` with `spec.enableSnapshotDeploy: true` can run this
exact command from its own self-hosted runner via the
[`kindling-snapshot-deploy`](github-actions.md#kindling-snapshot-deploy)
composite action — the whole graduation step then happens inside the
same GH Actions job that already builds and deploys your dev
environment, with the staging cluster's kubeconfig supplied as a GitHub
Actions secret. No laptop, no human at a TTY, required.

---

## Step 2: Configure TLS

Once your app is deployed to staging, set up automatic TLS certificates
with Let's Encrypt:

```bash
kindling staging tls \
  --context my-staging-cluster \
  --domain app.example.com \
  --email admin@example.com
```

### What happens

1. **Installs cert-manager** v1.17.1 (if not already present)
2. **Creates a ClusterIssuer** for Let's Encrypt (production ACME server)
3. **Optionally patches your DSE YAML** with TLS config

### Patching a DSE file

If you pass `--file`, the command patches your DSE YAML with the correct
ingress annotations and TLS block:

```bash
kindling staging tls \
  --context my-staging \
  --domain app.example.com \
  --email admin@example.com \
  -f .kindling/dev-environment.yaml
```

This adds to your DSE's ingress section:

```yaml
ingress:
  enabled: true
  host: app.example.com
  ingressClassName: traefik
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    secretName: app-example-com-tls
    hosts:
      - app.example.com
```

### Testing with staging ACME certificates

Use `--staging` to get test certificates from Let's Encrypt's own staging
server (no rate limits, but browsers will show a warning) — this is
separate from, and can be combined with, deploying to your kindling
staging cluster:

```bash
kindling staging tls \
  --context my-staging \
  --domain app.example.com \
  --email admin@example.com \
  --staging
```

---

## Step 3: Point your DNS

After deploying and configuring TLS, point your domain to the cluster's
load balancer:

```bash
# Get the external IP of your Traefik load balancer
kubectl get svc -n traefik --context my-staging

# Create a DNS A record:
#   app.example.com → <EXTERNAL-IP>
```

cert-manager will automatically provision a TLS certificate once DNS
propagates and the HTTP-01 challenge succeeds.

---

## Complete example

Here's the full flow from a working dev environment to a staging cluster:

```bash
# ── Verify dev is healthy ─────────────────────
kindling status

# ── Authenticate to your registry ───────────────────
echo $GHCR_TOKEN | docker login ghcr.io -u myuser --password-stdin

# ── Graduate to staging ────────────────────────
kindling snapshot \
  -r ghcr.io/myorg \
  --deploy \
  --context do-staging-cluster

# ── Configure TLS ─────────────────────
kindling staging tls \
  --context do-staging-cluster \
  --domain api.myapp.com \
  --email team@myapp.com

# ── Verify ──────────────────────────
kubectl get pods --context do-staging-cluster
kubectl get ingress --context do-staging-cluster
curl https://api.myapp.com/health
```

---

## Updating a production deployment

To push updates, just run `snapshot --deploy` again:

```bash
kindling snapshot -r ghcr.io/myorg --deploy --context do-prod-cluster
```

This will:
- Re-read the current cluster state
- Push updated images with a new tag (git SHA)
- Run `helm upgrade` to roll out changes

---

## Troubleshooting

### Images fail to push

Make sure you're authenticated to your registry:

```bash
docker login ghcr.io          # GHCR
aws ecr get-login-password ... # ECR
```

And that `crane` is installed:

```bash
brew install crane
```

### cert-manager challenges failing

Check the challenge status:

```bash
kubectl get challenges --context my-prod -A
kubectl describe challenge <name> --context my-prod
```

Common causes:
- DNS not pointing to the cluster yet
- Port 80 not open on the load balancer (needed for HTTP-01)
- IngressClass mismatch (use `--ingress-class` flag)

### Helm release conflicts

If a previous install left a broken release:

```bash
helm uninstall kindling-snapshot --kube-context my-prod
kindling snapshot -r ghcr.io/myorg --deploy --context my-prod
```
