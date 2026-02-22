---
title: Writing Workflows by Hand
description: Write a GitHub Actions workflow manually instead of using AI generation.
---

# Writing Workflows by Hand

If you prefer to write the workflow yourself instead of using `kindling generate`.

## Workflow template

Create `.github/workflows/dev-deploy.yml` in your app repository:

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

      - name: Build image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: my-app
          context: ${{ github.workspace }}
          image: "registry:5000/my-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-app"
          image: "registry:5000/my-app:${{ env.TAG }}"
          port: "8080"
          ingress-host: "${{ github.actor }}-my-app.localhost"
          dependencies: |
            - type: postgres
              version: "16"
            - type: redis
```

## Multi-service workflows

For multiple services, add a build + deploy step per service:

```yaml
      - name: Build gateway
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: gateway
          context: ${{ github.workspace }}/gateway
          image: "registry:5000/gateway:${{ env.TAG }}"

      - name: Build orders
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: orders
          context: ${{ github.workspace }}/orders
          image: "registry:5000/orders:${{ env.TAG }}"

      - name: Deploy gateway
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-gateway"
          image: "registry:5000/gateway:${{ env.TAG }}"
          port: "9090"
          ingress-host: "${{ github.actor }}-gateway.localhost"

      - name: Deploy orders
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-orders"
          image: "registry:5000/orders:${{ env.TAG }}"
          port: "8080"
          dependencies: |
            - type: postgres
            - type: redis
```

## External credentials in workflows

If your app needs API keys, set them with `kindling secrets set` first, then reference them in the deploy step:

```yaml
      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: "${{ github.actor }}-my-app"
          image: "registry:5000/my-app:${{ env.TAG }}"
          port: "8080"
          secrets: |
            - name: STRIPE_KEY
              secretName: kindling-secret-stripe-key
              key: value
```

See [Secrets Management](/docs/secrets) for details.
