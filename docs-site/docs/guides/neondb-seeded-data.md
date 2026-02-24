---
title: "NeonDB Pre-Seeded Dev Data"
description: Use Neon's branching to give every developer a pre-seeded Postgres database that resets instantly.
---

# NeonDB Pre-Seeded Dev Data

Connect your kindling environment to a **Neon** database branch so
every developer gets a pre-seeded copy of real schema and sample data
without maintaining seed scripts or fixtures.

---

## Why Neon + kindling

| Challenge | How this solves it |
| --- | --- |
| Seed scripts drift from production schema | Neon branch copies the real schema |
| Seeding takes minutes on every reset | Neon branches create in &lt;1 second |
| Shared dev database causes conflicts | Every developer gets their own branch |
| Local Postgres eats RAM | Database runs in Neon, not your laptop |

If you want a fully local database instead, use kindling's built-in
`- type: postgres` dependency — it auto-provisions Postgres inside Kind.

---

## What you'll build

A FastAPI service that:

1. Connects to a Neon database branch with pre-loaded product catalog data
2. Exposes REST endpoints for browsing and searching products
3. Runs locally with `kindling sync` for instant code iteration

```
┌──────────┐     ┌───────────────┐     ┌──────────────────┐
│ Browser  │────▶│  FastAPI      │────▶│  Neon Postgres    │
│  :8000   │◀────│  Product API  │     │  (dev branch)    │
└──────────┘     └───────────────┘     └──────────────────┘
```

---

## Project structure

```
product-api/
├── Dockerfile
├── requirements.txt
├── seed.sql
└── main.py
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
asyncpg==0.30.0
python-dotenv==1.0.1
```

### seed.sql

Run this once against your Neon main branch to populate sample data:

```sql
CREATE TABLE IF NOT EXISTS products (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    price NUMERIC(10, 2) NOT NULL,
    category TEXT NOT NULL,
    in_stock BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT now()
);

INSERT INTO products (name, description, price, category) VALUES
    ('Wireless Keyboard', 'Low-profile mechanical keyboard with Bluetooth', 79.99, 'electronics'),
    ('Standing Desk Mat', 'Anti-fatigue mat for standing desks, 20x34 inches', 49.99, 'office'),
    ('USB-C Hub', '7-in-1 hub with HDMI, USB-A, SD card, ethernet', 34.99, 'electronics'),
    ('Noise Cancelling Headphones', 'Over-ear, 30hr battery, ANC', 199.99, 'electronics'),
    ('Ergonomic Mouse', 'Vertical mouse with adjustable DPI', 44.99, 'electronics'),
    ('Desk Lamp', 'LED lamp with 5 brightness levels and USB charging port', 29.99, 'office'),
    ('Webcam HD', '1080p webcam with auto-focus and ring light', 59.99, 'electronics'),
    ('Cable Management Kit', 'Clips, sleeves, and ties for under-desk cables', 14.99, 'office'),
    ('Monitor Riser', 'Bamboo stand with storage drawer', 39.99, 'office'),
    ('Portable Charger', '20000mAh power bank with PD fast charging', 24.99, 'electronics');

CREATE INDEX IF NOT EXISTS idx_products_category ON products (category);
```

### main.py

```python
import os
from contextlib import asynccontextmanager

import asyncpg
from fastapi import FastAPI, HTTPException

DATABASE_URL = os.environ["DATABASE_URL"]


@asynccontextmanager
async def lifespan(app: FastAPI):
    app.state.pool = await asyncpg.create_pool(DATABASE_URL, min_size=2, max_size=5)
    yield
    await app.state.pool.close()


app = FastAPI(title="Product Catalog", lifespan=lifespan)


@app.get("/products")
async def list_products(category: str | None = None, in_stock: bool = True):
    """List products with optional category filter."""
    pool = app.state.pool
    if category:
        rows = await pool.fetch(
            "SELECT * FROM products WHERE category = $1 AND in_stock = $2 ORDER BY name",
            category,
            in_stock,
        )
    else:
        rows = await pool.fetch(
            "SELECT * FROM products WHERE in_stock = $1 ORDER BY name", in_stock
        )
    return [dict(r) for r in rows]


@app.get("/products/{product_id}")
async def get_product(product_id: int):
    pool = app.state.pool
    row = await pool.fetchrow("SELECT * FROM products WHERE id = $1", product_id)
    if not row:
        raise HTTPException(404, "Product not found")
    return dict(row)


@app.get("/products/search/{query}")
async def search_products(query: str):
    """Full-text search across name and description."""
    pool = app.state.pool
    rows = await pool.fetch(
        """SELECT *, ts_rank(
            to_tsvector('english', name || ' ' || coalesce(description, '')),
            plainto_tsquery('english', $1)
        ) AS rank
        FROM products
        WHERE to_tsvector('english', name || ' ' || coalesce(description, ''))
              @@ plainto_tsquery('english', $1)
        ORDER BY rank DESC""",
        query,
    )
    return [dict(r) for r in rows]


@app.get("/health")
async def health():
    pool = app.state.pool
    await pool.fetchval("SELECT 1")
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

## Neon setup (one-time)

### 1. Create a project & seed the main branch

```bash
# Install the Neon CLI
brew install neonctl

# Create a project (or use an existing one)
neonctl projects create --name kindling-dev

# Get the connection string for the main branch
neonctl connection-string --project-id <project-id>

# Seed with sample data
psql "$(neonctl connection-string --project-id <project-id>)" -f seed.sql
```

### 2. Create a dev branch

Each developer creates their own branch from main. Branches are
copy-on-write and instant — no data is copied until you write.

```bash
neonctl branches create \
  --project-id <project-id> \
  --name dev-$(whoami) \
  --parent main

# Get this branch's connection string
neonctl connection-string --project-id <project-id> --branch dev-$(whoami)
```

---

## kindling setup

### 1. Store the connection string

```bash
# Use your dev branch connection string from above
kindling secrets set DATABASE_URL "postgresql://user:pass@ep-cool-name.us-east-2.aws.neon.tech/neondb?sslmode=require"
```

### 2. Workflow

No `dependencies` block needed — the database is external.

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

      - name: Build product API
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: product-api
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/product-api:${{ env.TAG }}"

      - name: Deploy product API
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-product-api
          image: "${{ env.REGISTRY }}/product-api:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-products.localhost"
          health-check-path: "/health"
          env: |
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-database-url
                  key: value
```

### 3. Iterate

```bash
kindling sync -n <you>-product-api -d .
# Edit query logic, add new endpoints, change response shapes
# Changes appear instantly — no rebuild needed
```

---

## Resetting dev data

One of the biggest advantages of Neon branches: reset is instant.

```bash
# Delete the old branch
neonctl branches delete --project-id <project-id> --branch dev-$(whoami)

# Create a fresh one from main (includes all seed data)
neonctl branches create \
  --project-id <project-id> \
  --name dev-$(whoami) \
  --parent main
```

This takes under a second regardless of data size — no re-seeding needed.

---

## When to use local Postgres instead

| Use Neon branches | Use kindling `- type: postgres` |
| --- | --- |
| Need production-like schema | Prototyping from scratch |
| Team shares a seed dataset | Working offline |
| Want instant branch reset | Want zero external dependencies |
| Testing against Neon-specific features | Don't need persistent data across resets |

To switch to local Postgres, remove the `DATABASE_URL` secret and add
a dependency to your workflow:

```yaml
dependencies:
  - type: postgres
    name: product-db
```

kindling will auto-inject `PRODUCT_DB_URL` as an env var pointing to
the local Postgres instance.
