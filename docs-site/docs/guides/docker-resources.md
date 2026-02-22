---
title: Docker Desktop Resources
description: Recommended Docker Desktop resource allocation for different workload sizes.
---

# Docker Desktop Resources

Kindling runs a full Kubernetes cluster inside Docker via Kind. Allocate enough resources in **Docker Desktop → Settings → Resources**.

## Recommended settings

| Workload | CPUs | Memory | Disk |
|---|---|---|---|
| Small (1–3 lightweight services) | 4 | 8 GB | 30 GB |
| Medium (4–6 services, mixed languages) | 6 | 12 GB | 50 GB |
| Large (7+ services, heavy compilers like Rust/Java/C#) | 8+ | 16 GB | 80 GB |

## Why disk matters

Kaniko layer caching is enabled (`registry:5000/cache`). First builds are slow, but subsequent rebuilds are fast. Heavy stacks (Rust, Java) can use 2–5 GB of cached layers per service.

## Troubleshooting

If builds are slow or pods are being evicted:

```bash
# Check node resource usage
kubectl top nodes

# Check for evicted pods
kubectl get pods --field-selector=status.phase=Failed
```

Increase Docker Desktop memory/disk allocation and restart.
