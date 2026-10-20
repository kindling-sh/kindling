---
sidebar_position: 8
title: TLS & Certificates
description: Set up automatic HTTPS with cert-manager and Let's Encrypt on your staging cluster.
---

# TLS & Certificates

`kindling staging tls` installs cert-manager and configures automatic TLS
certificates from Let's Encrypt on your staging cluster.

---

## Quick start

```bash
kindling staging tls \
  --context my-staging-cluster \
  --domain app.example.com \
  --email admin@example.com
```

This installs cert-manager (if needed), creates a ClusterIssuer for
Let's Encrypt, and optionally patches your DSE YAML with TLS config.

---

## How it works

1. **Installs cert-manager** v1.17.1 via its official manifest (skipped if already present)
2. **Creates a ClusterIssuer** named `letsencrypt-prod` using the ACME HTTP-01 solver
3. **Patches your DSE YAML** (if `--file` is passed) with ingress annotations and TLS block

cert-manager then watches for Ingress resources with the
`cert-manager.io/cluster-issuer` annotation and automatically provisions
certificates.

---

## Examples

### Basic — domain + email

```bash
kindling staging tls \
  --context do-staging \
  --domain api.myapp.com \
  --email team@myapp.com
```

### With DSE file patching

Pass `--file` to automatically update your DSE YAML with the correct
ingress annotations and TLS block:

```bash
kindling staging tls \
  --context do-staging \
  --domain api.myapp.com \
  --email team@myapp.com \
  -f .kindling/dev-environment.yaml
```

This adds the following to your DSE's ingress section:

```yaml
ingress:
  enabled: true
  host: api.myapp.com
  ingressClassName: traefik
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    secretName: api-myapp-com-tls
    hosts:
      - api.myapp.com
```

### Staging certificates (testing)

Use Let's Encrypt's staging server to avoid rate limits while testing.
Browsers will show a certificate warning, but the full ACME flow runs:

```bash
kindling staging tls \
  --context do-staging \
  --domain api.myapp.com \
  --email team@myapp.com \
  --staging
```

### Custom ingress class

If you're not using Traefik, specify the ingress class:

```bash
kindling staging tls \
  --context do-staging \
  --domain api.myapp.com \
  --email team@myapp.com \
  --ingress-class nginx
```

---

## DNS setup

After configuring TLS, point your domain to the cluster's load balancer:

```bash
# Get the external IP
kubectl get svc -n traefik --context do-staging

# Create a DNS A record:
#   api.myapp.com → <EXTERNAL-IP>
```

cert-manager provisions the certificate once DNS propagates and the HTTP-01
challenge succeeds. This can take a few minutes.

> **Note:** You may see `ERR_CERT_AUTHORITY_INVALID` in your browser while
> the certificate is being issued. Give it a few minutes.

---

## Dashboard

The TLS page is available under **Staging → TLS** in the dashboard. It
provides the same functionality with form inputs for domain, email, and
ingress selection, plus a **Wildcard TLS (shared staging)** button for
the guided wizard described below.

---

## Wildcard TLS (shared staging clusters)

The setup above issues one certificate per hostname (HTTP-01) — the right
choice for a single, known staging domain. A **shared** staging cluster,
where every feature branch gets its own `<branch-slug>.staging.<domain>`
host, needs a different approach: HTTP-01 cannot validate a wildcard name
at all. `--wildcard` requests a single DNS-01 certificate covering
`*.staging.<domain>` instead, so every branch gets HTTPS automatically
with zero per-branch setup.

Scoped to Cloudflare only today (`cert-manager`'s `cloudflare` DNS-01
solver).

### CLI

```bash
kubectl --context do-staging create secret generic cloudflare-api-token \
  -n cert-manager --from-literal=api-token=<cloudflare-token>

kindling staging tls \
  --context do-staging \
  --domain subnode1.xyz \
  --email team@myapp.com \
  --wildcard \
  --dns-provider cloudflare \
  --dns-token-secret cloudflare-api-token
```

`--domain` is the bare apex domain (`subnode1.xyz`), not
`staging.subnode1.xyz` — the command always inserts the `staging.`
segment itself, so it matches `kindling snapshot --deploy
--staging-domain`'s branch-derived hosts exactly.

### Dashboard wizard

The same flow, guided, from **Staging → TLS → Wildcard TLS (shared
staging)**:

1. **Domain & DNS provider check** — enter the apex domain; the wizard
   looks up its nameservers and confirms they're Cloudflare's before
   asking for anything else (not all registrars support wildcard
   records, so this is checked first to avoid wasted setup).
2. **API token** — paste a Cloudflare token scoped to this zone ("Edit
   zone DNS" template); validated immediately against Cloudflare's API,
   so a bad token is caught in seconds, not as a cryptic ACME failure
   several minutes later.
3. **Confirm the DNS record** — the wizard shows the exact
   `*.staging.<domain>` record to add and your ingress controller's
   current address, with a "Check now" button that does a live DNS
   lookup once you've added it.
4. **Issue the certificate** — applies the DNS-01 `ClusterIssuer`,
   requests the certificate, and sets it as Traefik's cluster-wide
   default once issued — streamed step by step. DNS-01 validation
   typically takes several minutes; this is expected.

The wizard never creates the DNS record on your behalf — only confirms it
 once you've added it yourself. Your Cloudflare token is sent once, used
 to create a Kubernetes Secret in the `cert-manager` namespace, and never
 stored, logged, or echoed back by the dashboard.

---

## Troubleshooting

### Certificate stuck in "Issuing"

Check the cert-manager logs and certificate status:

```bash
kubectl get certificates --context do-staging
kubectl describe certificate <name> --context do-staging
kubectl logs -n cert-manager deploy/cert-manager --context do-staging
```

Common causes: DNS not pointing to the right IP, port 80 not reachable
(HTTP-01 requires it), or Let's Encrypt rate limits (use `--staging` first).

### cert-manager already installed

If cert-manager is already running, `kindling staging tls` skips the
install step and just creates the ClusterIssuer. No conflicts.
