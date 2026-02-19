"""
Runtime settings — read once at import time.

PG_DSN should be a full postgres:// connection string.
QUEUE_URL points at the Redis instance used for the order_events list.
"""

import os


class Settings:
    APP_PORT: int = int(os.getenv("APP_PORT", "5000"))
    PG_DSN: str = os.getenv("PG_DSN", "")
    QUEUE_URL: str = os.getenv("QUEUE_URL", "")
    LOG_LEVEL: str = os.getenv("LOG_LEVEL", "info")


settings = Settings()
