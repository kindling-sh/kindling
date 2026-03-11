---
sidebar_position: 9
title: Build & Load Images
description: Build container images and load them directly into your Kind cluster — no CI needed.
---

# Build & Load Images

`kindling load` builds a container image from source and loads it directly
into the Kind cluster. This is the fastest way to test a new build without
waiting for CI.

---

## Quick start

```bash
kindling load -s my-service --context .
```

This runs `docker build --platform linux/amd64` in the current directory, tags
the image for the in-cluster registry (`localhost:5001`), and loads it into Kind.

---

## How it works

1. **Builds** a Docker image using your Dockerfile (defaults to `./Dockerfile`)
2. **Tags** it as `localhost:5001/<service>:<timestamp>` for the Kind registry
3. **Loads** the image into the Kind cluster via `kind load docker-image`
4. **Patches** the matching deployment to use the new image tag

The image is always built for `linux/amd64`, regardless of your host
architecture. This ensures consistency between local dev and production.

---

## Examples

### Basic — single service

```bash
kindling load -s gateway --context ./gateway
```

### Custom Dockerfile

```bash
kindling load -s api --context . --dockerfile Dockerfile.dev
```

### Monorepo — service in a subdirectory

```bash
kindling load -s orders --context ./services/orders
```

### After editing a Dockerfile

When you change your Dockerfile or dependencies (not just source files),
`kindling load` is the right tool. For source-only changes, use
[`kindling sync`](sync.md) instead — it's faster because it skips the build.

---

## When to use load vs sync vs push

| Scenario | Command | Speed |
|---|---|---|
| Source file changed | `kindling sync` | Seconds |
| Dockerfile or deps changed | `kindling load` | 30s–2min |
| Need CI to run (tests, lint) | `kindling push` | Minutes |

---

## Dashboard

The Load modal is available from the **Environments** page — click the build
icon next to any service. It provides the same functionality with a file
picker for the build context.

---

## Troubleshooting

### Build fails with architecture errors

`kindling load` builds for `linux/amd64` by default. If your Dockerfile uses
`TARGETARCH` or `BUILDPLATFORM` ARGs, note that these are BuildKit-specific
and won't be set during Kaniko builds in CI. Avoid relying on them.

### Image loads but pod doesn't restart

The deployment is patched automatically. If the pod still shows the old image,
check `kubectl get events` for scheduling issues or resource limits.
