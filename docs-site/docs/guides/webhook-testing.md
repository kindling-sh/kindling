---
title: Webhook Testing
description: Test inbound webhooks from third-party services locally using kindling tunnels.
---

# Webhook Testing

Third-party services (Stripe, GitHub, Shopify, Twilio) need a public
URL to deliver webhooks. This guide shows how to receive webhooks in
your local kindling environment using `kindling expose`.

---

## The problem

Your app runs on `*.localhost`. Stripe can't reach that. You need a
public HTTPS URL that forwards to your local cluster.

```
┌──────────────┐     ┌──────────────────┐     ┌───────────────┐
│ Stripe /     │────▶│  Tunnel          │────▶│  Your app     │
│ GitHub /     │     │  (kindling       │     │  on kindling  │
│ Shopify      │     │   expose)        │     │  :8000        │
└──────────────┘     └──────────────────┘     └───────────────┘
```

---

## What you'll build

A webhook receiver that:

1. Accepts POST requests from any external service
2. Verifies signatures (using Stripe as an example)
3. Logs and stores events in Postgres
4. Is reachable from the internet via `kindling expose`

---

## Project structure

```
webhook-app/
├── Dockerfile
├── requirements.txt
└── main.py
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
stripe==10.0.0
asyncpg==0.30.0
```

### main.py

```python
import json
import os
from contextlib import asynccontextmanager

import asyncpg
import stripe
from fastapi import FastAPI, Request, HTTPException

DATABASE_URL = os.environ["WEBHOOK_DB_URL"]
STRIPE_WEBHOOK_SECRET = os.environ.get("STRIPE_WEBHOOK_SECRET", "")

stripe.api_key = os.environ.get("STRIPE_KEY", "")


@asynccontextmanager
async def lifespan(app: FastAPI):
    app.state.pool = await asyncpg.create_pool(DATABASE_URL, min_size=2, max_size=5)
    async with app.state.pool.acquire() as conn:
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS webhook_events (
                id SERIAL PRIMARY KEY,
                source TEXT NOT NULL,
                event_type TEXT NOT NULL,
                payload JSONB NOT NULL,
                verified BOOLEAN DEFAULT false,
                created_at TIMESTAMPTZ DEFAULT now()
            )
        """)
    yield
    await app.state.pool.close()


app = FastAPI(title="Webhook Receiver", lifespan=lifespan)


@app.post("/webhooks/stripe")
async def stripe_webhook(request: Request):
    """Receive and verify Stripe webhook events."""
    payload = await request.body()
    sig_header = request.headers.get("stripe-signature", "")

    try:
        event = stripe.Webhook.construct_event(
            payload, sig_header, STRIPE_WEBHOOK_SECRET
        )
    except stripe.error.SignatureVerificationError:
        raise HTTPException(400, "Invalid signature")

    # Store the verified event
    async with app.state.pool.acquire() as conn:
        await conn.execute(
            "INSERT INTO webhook_events (source, event_type, payload, verified) "
            "VALUES ($1, $2, $3, $4)",
            "stripe", event["type"], json.dumps(event), True,
        )

    print(f"Stripe event: {event['type']} ({event['id']})")
    return {"received": True}


@app.post("/webhooks/github")
async def github_webhook(request: Request):
    """Receive GitHub webhook events (push, PR, issue, etc.)."""
    payload = await request.json()
    event_type = request.headers.get("X-GitHub-Event", "unknown")

    async with app.state.pool.acquire() as conn:
        await conn.execute(
            "INSERT INTO webhook_events (source, event_type, payload) "
            "VALUES ($1, $2, $3)",
            "github", event_type, json.dumps(payload),
        )

    print(f"GitHub event: {event_type}")
    return {"received": True}


@app.post("/webhooks/generic")
async def generic_webhook(request: Request):
    """Catch-all endpoint for any webhook source."""
    payload = await request.json()

    async with app.state.pool.acquire() as conn:
        await conn.execute(
            "INSERT INTO webhook_events (source, event_type, payload) "
            "VALUES ($1, $2, $3)",
            "generic", "incoming", json.dumps(payload),
        )

    print(f"Generic webhook received: {json.dumps(payload)[:100]}")
    return {"received": True}


@app.get("/webhooks/events")
async def list_events(source: str = None, limit: int = 20):
    """List recent webhook events for debugging."""
    pool = app.state.pool
    if source:
        rows = await pool.fetch(
            "SELECT id, source, event_type, verified, created_at FROM webhook_events "
            "WHERE source = $1 ORDER BY created_at DESC LIMIT $2",
            source, limit,
        )
    else:
        rows = await pool.fetch(
            "SELECT id, source, event_type, verified, created_at FROM webhook_events "
            "ORDER BY created_at DESC LIMIT $1",
            limit,
        )
    return [dict(r) for r in rows]


@app.get("/health")
async def health():
    return {"status": "ok"}
```

### Dockerfile

```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
```

---

## kindling setup

### 1. Store secrets

```bash
kindling secrets set STRIPE_KEY sk_test_xxxxx
kindling secrets set STRIPE_WEBHOOK_SECRET whsec_xxxxx
```

### 2. Workflow

```yaml
name: dev-deploy
on:
  push:
    branches: [main]
  workflow_dispatch:

env:
  REGISTRY: registry:5000
  TAG: ${{ github.actor }}-${{ github.sha }}

jobs:
  deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]
    steps:
      - uses: actions/checkout@v4
      - run: rm -rf /builds/*

      - name: Build
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: webhook-app
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/webhook-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-webhook-app
          image: "${{ env.REGISTRY }}/webhook-app:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-webhooks.localhost"
          health-check-path: "/health"
          dependencies:
            - type: postgres
              name: webhook-db
          env: |
            - name: STRIPE_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-stripe-key
                  key: value
            - name: STRIPE_WEBHOOK_SECRET
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-stripe-webhook-secret
                  key: value
```

### 3. Create a public tunnel

```bash
kindling expose <you>-webhook-app
# Output: https://abc123.trycloudflare.com
```

This gives you a public HTTPS URL that forwards to your local app.

### 4. Register the webhook URL

**Stripe:**
Go to Stripe Dashboard → Developers → Webhooks → Add Endpoint.
Use `https://abc123.trycloudflare.com/webhooks/stripe`.

**GitHub:**
Go to your repo → Settings → Webhooks → Add Webhook.
Use `https://abc123.trycloudflare.com/webhooks/github`.

**Any service:**
Use `https://abc123.trycloudflare.com/webhooks/generic` as the
catch-all endpoint.

### 5. Debug

```bash
# See what events came in
curl "http://<you>-webhooks.localhost/webhooks/events"

# Filter by source
curl "http://<you>-webhooks.localhost/webhooks/events?source=stripe"

# Watch logs in real time
kindling logs <you>-webhook-app -f
```

---

## Iterate

```bash
kindling sync -n <you>-webhook-app -d .
# Add a new endpoint for Shopify webhooks, change event processing
# logic, add signature verification — tunnel stays connected
```

The tunnel URL remains stable across syncs, so you don't need to
re-register the webhook endpoint with the external service.

---

## Tips

- **Replay events**: use the Stripe CLI (`stripe trigger payment_intent.succeeded`)
  or GitHub's "Redeliver" button to replay events during development
- **Multiple providers**: add as many `/webhooks/<provider>` endpoints
  as you need — they all share the same tunnel
- The events table doubles as an **audit log** — query it to verify
  your app handled events correctly
