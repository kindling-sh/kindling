"""Postgres helpers — schema init + CRUD."""

import psycopg2
import psycopg2.extras

from config import settings

_conn = None


def _get_conn():
    global _conn
    if _conn is None or _conn.closed:
        if not settings.DATABASE_URL:
            return None
        _conn = psycopg2.connect(settings.DATABASE_URL)
        _conn.autocommit = True
        _bootstrap(_conn)
    return _conn


def _bootstrap(conn):
    with conn.cursor() as cur:
        cur.execute("""
            CREATE TABLE IF NOT EXISTS orders (
                id         TEXT PRIMARY KEY,
                product    TEXT NOT NULL,
                quantity   INTEGER NOT NULL DEFAULT 1,
                status     TEXT NOT NULL DEFAULT 'pending',
                created_at TIMESTAMPTZ DEFAULT NOW()
            )
        """)


def insert_order(order_id: str, product: str, qty: int) -> bool:
    conn = _get_conn()
    if conn is None:
        return False
    with conn.cursor() as cur:
        cur.execute(
            "INSERT INTO orders (id, product, quantity, status) VALUES (%s, %s, %s, 'confirmed')",
            (order_id, product, qty),
        )
    return True


def recent_orders(limit: int = 50):
    conn = _get_conn()
    if conn is None:
        return []
    with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
        cur.execute(
            "SELECT id, product, quantity, status, created_at FROM orders "
            "ORDER BY created_at DESC LIMIT %s",
            (limit,),
        )
        rows = cur.fetchall()
    for r in rows:
        if r.get("created_at"):
            r["created_at"] = r["created_at"].isoformat()
    return rows


def pg_ok() -> str:
    conn = _get_conn()
    if conn is None:
        return "not configured (DATABASE_URL unset)"
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT 1")
        return "connected"
    except Exception as exc:
        return f"error: {exc}"
