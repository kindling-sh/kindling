---
sidebar_position: 7
title: Graduating to Production
description: Take your dev environment to a real Kubernetes cluster with TLS, image push, and Helm deploy.
---

# Graduating to Production

Once your app runs reliably in the local Kind cluster, you can **graduate**
it to any Kubernetes cluster — DigitalOcean, AWS EKS, GKE, bare-metal, or
anything with a kubeconfig context.

kindling's graduation flow uses two commands:

| Command | Purpose |
|---|---|
| `kindling snapshot --deploy` | Generate a Helm chart, push images, and deploy |
| `kindling production tls` | Set up TLS with cert-manager and Let's Encrypt |

---

## Prerequisites

Before graduating, make sure you have:

1. **A running Kind cluster** with your app deployed and healthy (`kindling status`)
2. **A production Kubernetes cluster** with a kubeconfig context configured (`kubectl config get-contexts`)
3. **A container registry** you can push to (GHCR, ECR, Docker Hub, etc.)
4. **Registry authentication** (`docker login`, `aws ecr get-login-password`, etc.)
5. **Helm v3** installed (`brew install helm`)
6. **crane** installed for image copy (`brew install crane`)

---

## Step 1: Snapshot and deploy

The `kindling snapshot` command reads all DevStagingEnvironments from your
local Kind cluster and generates a production-ready Helm chart. With
`--deploy`, it also pushes images and installs the chart on your production
cluster in one step.

```bash
kindling snapshot \
  --registry ghcr.io/myorg \
  --deploy \
  --context my-prod-cluster
```

### What happens

1. **Reads cluster state** — discovers all DSEs, services, dependencies
2. **Strips dev prefixes** — `jeff-vincent-gateway` becomes `gateway`
3. **Generates Helm chart** — templates, values.yaml, values-live.yaml
4. **Pushes images** — copies each image from `localhost:5001` to your registry using `crane copy`
5. **Installs chart** — runs `helm upgrade --install` on the production cluster

### Common flags

```bash
# Custom image tag (default: git SHA)
kindling snapshot -r ghcr.io/myorg -t v1.2.0 --deploy --context my-prod

# Deploy into a specific namespace
kindling snapshot -r ghcr.io/myorg --deploy --context my-prod --namespace staging

# Custom chart name and output directory
kindling snapshot -r ghcr.io/myorg -n my-platform -o ./charts/prod --deploy --context my-prod
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
  --kube-context my-prod \
  --set gateway.env.DATABASE_URL=postgres://prod-host:5432/mydb
```

---

## Step 2: Configure TLS

Once your app is deployed to production, set up automatic TLS certificates
with Let's Encrypt:

```bash
kindling production tls \
  --context my-prod-cluster \
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
kindling production tls \
  --context my-prod \
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

### Testing with staging certificates

Use `--staging` to get test certificates from Let's Encrypt's staging server
(no rate limits, but browsers will show a warning):

```bash
kindling production tls \
  --context my-prod \
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
kubectl get svc -n traefik --context my-prod

# Create a DNS A record:
#   app.example.com → <EXTERNAL-IP>
```

cert-manager will automatically provision a TLS certificate once DNS
propagates and the HTTP-01 challenge succeeds.

---

## Complete example

Here's the full flow from a working dev environment to production:

```bash
# ── Verify dev is healthy ─────────────────────────────────
kindling status

# ── Authenticate to your registry ─────────────────────────
echo $GHCR_TOKEN | docker login ghcr.io -u myuser --password-stdin

# ── Graduate to production ────────────────────────────────
kindling snapshot \
  -r ghcr.io/myorg \
  --deploy \
  --context do-prod-cluster

# ── Configure TLS ────────────────────────────────────────
kindling production tls \
  --context do-prod-cluster \
  --domain api.myapp.com \
  --email team@myapp.com

# ── Verify ────────────────────────────────────────────────
kubectl get pods --context do-prod-cluster
kubectl get ingress --context do-prod-cluster
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
