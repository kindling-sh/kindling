# ── Install deps ─────────────────────────────────────────────────
FROM python:3.12-slim AS deps

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# ── Runtime ──────────────────────────────────────────────────────
FROM python:3.12-slim

COPY --from=deps /install /usr/local
WORKDIR /app
COPY *.py ./

# APP_PORT is read by config.py — default 5000
EXPOSE 5000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "5000"]
