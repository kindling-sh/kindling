---
title: "OpenAI Agents SDK"
description: Build a tool-calling agent with the OpenAI Agents SDK and iterate on it locally with kindling.
---

# OpenAI Agents SDK

Build a **tool-calling agent** with the [OpenAI Agents SDK](https://github.com/openai/openai-agents-python)
and deploy it locally on kindling. This is the simplest agent pattern —
a single service that orchestrates LLM calls with tool definitions.

---

## What you'll build

A customer support agent that:

1. Looks up orders in Postgres
2. Checks shipping status via a mock API
3. Issues refunds (writes back to the database)
4. Uses the Agents SDK's built-in conversation loop and handoff

```
┌──────────┐     ┌───────────────┐     ┌──────────────┐
│ Browser  │────▶│  FastAPI       │────▶│  Postgres    │
│  :8000   │◀────│  Support Agent │     │  (orders DB) │
└──────────┘     └───────┬───────┘     └──────────────┘
                         │
                    ┌────▼─────┐
                    │ OpenAI   │
                    │ API      │
                    └──────────┘
```

---

## Project structure

```
support-agent/
├── Dockerfile
├── requirements.txt
├── main.py
├── agent.py
├── tools.py
└── seed.sql
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
openai-agents==0.1.0
asyncpg==0.30.0
```

### seed.sql

Applied automatically by Postgres init — or run manually.

```sql
CREATE TABLE IF NOT EXISTS orders (
    id SERIAL PRIMARY KEY,
    customer_email TEXT NOT NULL,
    product TEXT NOT NULL,
    quantity INT DEFAULT 1,
    total NUMERIC(10, 2) NOT NULL,
    status TEXT DEFAULT 'processing',
    tracking_number TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS refunds (
    id SERIAL PRIMARY KEY,
    order_id INT REFERENCES orders(id),
    reason TEXT NOT NULL,
    amount NUMERIC(10, 2) NOT NULL,
    status TEXT DEFAULT 'pending',
    created_at TIMESTAMPTZ DEFAULT now()
);

INSERT INTO orders (customer_email, product, quantity, total, status, tracking_number) VALUES
    ('alice@example.com', 'Wireless Keyboard', 1, 79.99, 'shipped', 'TRK-001-ABC'),
    ('alice@example.com', 'USB-C Hub', 2, 69.98, 'delivered', 'TRK-002-DEF'),
    ('bob@example.com', 'Standing Desk Mat', 1, 49.99, 'processing', NULL),
    ('bob@example.com', 'Monitor Riser', 1, 39.99, 'shipped', 'TRK-003-GHI'),
    ('carol@example.com', 'Noise Cancelling Headphones', 1, 199.99, 'delivered', 'TRK-004-JKL');
```

### tools.py

```python
import asyncpg


async def lookup_orders(pool: asyncpg.Pool, email: str) -> list[dict]:
    """Find all orders for a customer by email."""
    rows = await pool.fetch(
        "SELECT id, product, quantity, total, status, tracking_number, created_at "
        "FROM orders WHERE customer_email = $1 ORDER BY created_at DESC",
        email,
    )
    return [dict(r) for r in rows]


async def get_order(pool: asyncpg.Pool, order_id: int) -> dict | None:
    """Get a single order by ID."""
    row = await pool.fetchrow("SELECT * FROM orders WHERE id = $1", order_id)
    return dict(row) if row else None


async def check_shipping(pool: asyncpg.Pool, order_id: int) -> dict:
    """Check shipping status for an order."""
    row = await pool.fetchrow(
        "SELECT status, tracking_number FROM orders WHERE id = $1", order_id
    )
    if not row:
        return {"error": "Order not found"}

    status_map = {
        "processing": "Order is being prepared. No tracking number yet.",
        "shipped": f"Order is in transit. Tracking: {row['tracking_number']}",
        "delivered": f"Order has been delivered. Tracking: {row['tracking_number']}",
    }
    return {
        "order_id": order_id,
        "status": row["status"],
        "detail": status_map.get(row["status"], "Unknown status"),
    }


async def issue_refund(
    pool: asyncpg.Pool, order_id: int, reason: str
) -> dict:
    """Issue a refund for an order."""
    order = await get_order(pool, order_id)
    if not order:
        return {"error": "Order not found"}

    if order["status"] == "processing":
        return {"error": "Cannot refund an order that hasn't shipped yet. Cancel instead."}

    async with pool.acquire() as conn:
        refund_id = await conn.fetchval(
            "INSERT INTO refunds (order_id, reason, amount) VALUES ($1, $2, $3) RETURNING id",
            order_id, reason, float(order["total"]),
        )
        await conn.execute(
            "UPDATE orders SET status = 'refunded' WHERE id = $1", order_id
        )

    return {
        "refund_id": refund_id,
        "order_id": order_id,
        "amount": float(order["total"]),
        "status": "pending",
    }
```

### agent.py

```python
from agents import Agent, function_tool
import asyncpg

from tools import lookup_orders, check_shipping, issue_refund, get_order


def create_support_agent(pool: asyncpg.Pool) -> Agent:
    """Build the support agent with tools bound to the database pool."""

    @function_tool
    async def find_orders(email: str) -> str:
        """Look up all orders for a customer by their email address."""
        orders = await lookup_orders(pool, email)
        if not orders:
            return f"No orders found for {email}"
        lines = []
        for o in orders:
            lines.append(
                f"Order #{o['id']}: {o['product']} (x{o['quantity']}) "
                f"— ${o['total']} — {o['status']}"
            )
        return "\n".join(lines)

    @function_tool
    async def shipping_status(order_id: int) -> str:
        """Check the shipping status and tracking info for an order."""
        result = await check_shipping(pool, order_id)
        if "error" in result:
            return result["error"]
        return result["detail"]

    @function_tool
    async def process_refund(order_id: int, reason: str) -> str:
        """Issue a refund for an order. Requires a reason."""
        result = await issue_refund(pool, order_id, reason)
        if "error" in result:
            return f"Refund failed: {result['error']}"
        return (
            f"Refund #{result['refund_id']} created for order #{order_id}. "
            f"Amount: ${result['amount']:.2f}. Status: {result['status']}."
        )

    @function_tool
    async def order_details(order_id: int) -> str:
        """Get full details for a specific order by ID."""
        order = await get_order(pool, order_id)
        if not order:
            return "Order not found"
        return (
            f"Order #{order['id']}\n"
            f"Customer: {order['customer_email']}\n"
            f"Product: {order['product']} (x{order['quantity']})\n"
            f"Total: ${order['total']}\n"
            f"Status: {order['status']}\n"
            f"Tracking: {order['tracking_number'] or 'N/A'}"
        )

    return Agent(
        name="Support Agent",
        instructions=(
            "You are a helpful customer support agent. You can look up orders, "
            "check shipping status, and process refunds. Always confirm the "
            "customer's email first, then help with their request. Be concise "
            "and friendly. If a refund is requested, confirm the order details "
            "and reason before processing."
        ),
        tools=[find_orders, shipping_status, process_refund, order_details],
    )
```

### main.py

```python
import os
from contextlib import asynccontextmanager

import asyncpg
from agents import Runner
from fastapi import FastAPI

from agent import create_support_agent

DATABASE_URL = os.environ["SUPPORT_DB_URL"]

# The Agents SDK reads OPENAI_API_KEY from the environment automatically


@asynccontextmanager
async def lifespan(app: FastAPI):
    app.state.pool = await asyncpg.create_pool(DATABASE_URL, min_size=2, max_size=5)

    # Run seed SQL if the orders table is empty
    async with app.state.pool.acquire() as conn:
        count = await conn.fetchval("SELECT count(*) FROM information_schema.tables WHERE table_name = 'orders'")
        if count == 0:
            with open("seed.sql") as f:
                await conn.execute(f.read())

    app.state.agent = create_support_agent(app.state.pool)
    yield
    await app.state.pool.close()


app = FastAPI(title="Support Agent", lifespan=lifespan)


@app.post("/chat")
async def chat(message: str, thread_id: str = "default"):
    """Send a message to the support agent."""
    result = await Runner.run(app.state.agent, message)
    return {
        "response": result.final_output,
        "thread_id": thread_id,
    }


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

### 1. Store your API key

```bash
kindling secrets set OPENAI_API_KEY sk-your-key
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

      - name: Build support agent
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: support-agent
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/support-agent:${{ env.TAG }}"

      - name: Deploy support agent
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-support-agent
          image: "${{ env.REGISTRY }}/support-agent:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-support.localhost"
          health-check-path: "/health"
          dependencies:
            - type: postgres
              name: support-db
          env: |
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value
```

kindling auto-injects `SUPPORT_DB_URL` pointing to the local Postgres.

### 3. Seed the database

After the first deploy, seed the orders table:

```bash
kindling expose <you>-support-db 5432
psql "postgresql://postgres:postgres@localhost:5432/postgres" -f seed.sql
```

Or let the app seed itself on first startup (the `lifespan` handler checks
for the orders table).

### 4. Iterate

```bash
kindling sync -n <you>-support-agent -d .
# Edit agent.py — add new tools, change instructions, adjust
# the refund policy — changes apply instantly
```

---

## Testing the agent

```bash
# Look up orders
curl -X POST "http://<you>-support.localhost/chat" \
  -d "message=I need help with my order. My email is alice@example.com"

# Check shipping
curl -X POST "http://<you>-support.localhost/chat" \
  -d "message=Where is order 1?"

# Request a refund
curl -X POST "http://<you>-support.localhost/chat" \
  -d "message=I want to return order 2, the hub stopped working"
```

---

## Tips

- **Add tools iteratively**: start with `find_orders` alone, test it,
  then add `shipping_status`, test again, then `process_refund`. Sync
  picks up each change instantly.
- **Handoffs**: the Agents SDK supports handoffs between agents. Add a
  `billing_agent` or `technical_agent` and hand off based on the
  customer's request — same deployment, just more `Agent` instances.
- **Guardrails**: use the SDK's `input_guardrails` to block prompt
  injection or enforce content policies before the agent runs.
- **Tracing**: set `OPENAI_AGENTS_TRACING_ENABLED=true` in your env
  block to get OpenAI-hosted traces of every agent run.
