"""Thin Redis wrapper — just the bits we need for the event queue."""

import json
import redis as _redis

from config import settings

_client = None


def _get():
    global _client
    if _client is None:
        if not settings.QUEUE_URL:
            return None
        _client = _redis.from_url(settings.QUEUE_URL, decode_responses=True)
    return _client


def publish_order_event(order_id: str, product: str, qty: int):
    r = _get()
    if r is None:
        return
    r.lpush(
        "order_events",
        json.dumps(
            {"order_id": order_id, "product": product, "quantity": qty}
        ),
    )


def queue_ok() -> str:
    r = _get()
    if r is None:
        return "not configured (QUEUE_URL unset)"
    try:
        r.ping()
        return "connected"
    except Exception as exc:
        return f"error: {exc}"
