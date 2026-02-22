---
title: Manual Deploy
description: Deploy an app without GitHub Actions — build locally, load into Kind, and apply a DSE manifest.
---

# Manual Deploy (without GitHub Actions)

If you want to deploy without CI — useful for quick tests or air-gapped environments.

## Build and load the image

```bash
docker build -t my-app:dev .
kind load docker-image my-app:dev --name dev
```

## Create a DevStagingEnvironment manifest

```yaml title="dev-environment.yaml"
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: my-app-dev
spec:
  deployment:
    image: my-app:dev
    port: 8080
    healthCheck:
      path: /healthz
  service:
    port: 8080
  ingress:
    enabled: true
    host: my-app.localhost
    ingressClassName: nginx
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
```

## Deploy

```bash
kindling deploy -f dev-environment.yaml
```

## Verify

```bash
kindling status
curl http://my-app.localhost/healthz
```

## Clean up

```bash
kubectl delete devstagingenvironment my-app-dev
```
