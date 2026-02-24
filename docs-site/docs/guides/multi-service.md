---
title: Multi-Service Apps
description: Deploy microservices architectures with multiple services and shared dependencies.
---

# Multi-Service Apps

Kindling handles multi-service architectures natively. Each service gets
its own DevStagingEnvironment — independent deployment, scaling, health
checks, and dependencies — while sharing the same cluster, registry,
and ingress controller.

---

## Example: microservices demo

The built-in microservices example has four services:

```
microservices/
├── gateway/     → API gateway, port 9090, ingress on gateway.localhost
├── orders/      → Order service, port 8080, depends on postgres + redis
├── inventory/   → Inventory service, port 8080, depends on mongodb
└── ui/          → React frontend, port 3000, ingress on ui.localhost
```

### Try it

```bash
# Copy the demo
cp -r ~/.kindling/examples/microservices ~/kindling-demo
cd ~/kindling-demo

# Create a GitHub repo
gh repo create kindling-demo --public --source . --push

# Connect runners
kindling runners -u <your-user> -r <your-user>/kindling-demo -t <pat>

# Push to trigger the workflow
git push
```

---

## How multi-service workflows work

Each service gets a **build** step and a **deploy** step in the workflow:

```yaml
- name: Build gateway
  uses: kindling-sh/kindling/.github/actions/kindling-build@main
  with:
    name: gateway
    context: ${{ github.workspace }}/gateway
    image: "registry:5000/gateway:${{ env.TAG }}"

- name: Deploy gateway
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-gateway"
    image: "registry:5000/gateway:${{ env.TAG }}"
    port: "9090"
    ingress-host: "${{ github.actor }}-gateway.localhost"
```

Services with dependencies declare them in the deploy step:

```yaml
- name: Deploy orders
  uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
  with:
    name: "${{ github.actor }}-orders"
    image: "registry:5000/orders:${{ env.TAG }}"
    port: "8080"
    dependencies: |
      - type: postgres
        version: "16"
      - type: redis
```

The operator auto-provisions each dependency, creates a Service for it,
and injects connection strings (`DATABASE_URL`, `REDIS_URL`) into the
app container.

---

## Resource considerations

Each service adds a pod + its dependencies. On a Mac laptop:

| Setup | Typical memory |
|---|---|
| 2–3 lightweight services, no deps | ~1.5 GB total |
| 4 services + postgres + redis + mongodb | ~2–3 GB total |
| 6+ services with heavy deps (Kafka, Elasticsearch) | 4–8 GB total |

See [Docker Desktop Resources](docker-resources.md) for allocation
recommendations.

---

## Live sync across services

You can sync individual services independently:

```bash
# Sync the gateway service
kindling sync -d gateway --src ./gateway --restart

# In another terminal, sync the orders service
kindling sync -d orders --src ./orders --restart
```

---

## Shared dependencies

If two services both need Postgres, each gets its own independent
instance. This mirrors production isolation and avoids schema conflicts.
The operator names them `<dse-name>-postgres` so they don't collide.

---

## Using `kindling generate` for multi-service repos

```bash
kindling generate -k sk-... -r /path/to/multi-service-repo
```

The scanner detects each directory with a Dockerfile, analyzes
docker-compose.yml for `depends_on` relationships, and generates a
workflow with the correct build order and dependency declarations.
