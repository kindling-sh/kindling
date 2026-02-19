"""
Orders service — FastAPI.

POST /orders          create an order
GET  /orders          list recent orders
GET  /api/v1/health   health / readiness
GET  /api/v1/status   dependency connectivity
"""

import uuid
from datetime import datetime, timezone

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

import db
import event_queue as q

app = FastAPI(title="orders-service", docs_url=None, redoc_url=None)


# ── Models ───────────────────────────────────────────────────────

class OrderIn(BaseModel):
    product: str
    quantity: int = 1


class OrderOut(BaseModel):
    id: str
    product: str
    quantity: int
    status: str


# ── Routes ───────────────────────────────────────────────────────

@app.get("/api/v1/health")
def health():
    return {"up": True}


@app.get("/api/v1/status")
def status():
    return {
        "service": "orders",
        "ts": datetime.now(timezone.utc).isoformat(),
        "postgres": db.pg_ok(),
        "queue": q.queue_ok(),
    }


@app.post("/orders", status_code=201)
def create_order(body: OrderIn):
    if body.quantity < 1:
        raise HTTPException(400, "quantity must be >= 1")

    oid = uuid.uuid4().hex[:8]
    ok = db.insert_order(oid, body.product, body.quantity)
    if not ok:
        raise HTTPException(503, "database unavailable")

    q.publish_order_event(oid, body.product, body.quantity)
    return OrderOut(id=oid, product=body.product, quantity=body.quantity, status="confirmed")


@app.get("/orders")
def list_orders():
    return {"orders": db.recent_orders()}
