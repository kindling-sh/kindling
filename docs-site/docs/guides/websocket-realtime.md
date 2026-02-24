---
title: WebSocket Real-Time
description: Build a real-time app with WebSockets and Redis pub/sub on kindling.
---

# WebSocket Real-Time

Build a live-updating app with **WebSockets** backed by **Redis pub/sub**
so updates push to every connected client instantly. Everything runs
locally on kindling.

---

## What you'll build

A real-time notification feed:

1. **API** posts events (new order, status change, etc.)
2. **Redis pub/sub** fans events out to all subscribers
3. **WebSocket server** pushes events to connected browsers

```
┌──────────┐  WS   ┌───────────────┐  pub/sub  ┌───────────┐
│ Browser  │◀─────▶│  FastAPI       │◀─────────▶│  Redis    │
│          │       │  (WS + HTTP)  │           │           │
└──────────┘       └───────────────┘           └───────────┘
     │                    ▲
     │ HTTP POST /events  │
     └────────────────────┘
```

---

## Project structure

```
realtime-app/
├── Dockerfile
├── requirements.txt
├── main.py
└── static/
    └── index.html
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
redis==5.1.0
websockets==13.0
```

### main.py

```python
import asyncio
import json
import os

import redis.asyncio as aioredis
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles

app = FastAPI(title="Real-Time Feed")
REDIS_URL = os.environ["PUBSUB_CACHE_URL"]
CHANNEL = "events"


class ConnectionManager:
    """Track active WebSocket connections."""

    def __init__(self):
        self.active: list[WebSocket] = []

    async def connect(self, ws: WebSocket):
        await ws.accept()
        self.active.append(ws)

    def disconnect(self, ws: WebSocket):
        self.active.remove(ws)

    async def broadcast(self, message: str):
        for ws in self.active[:]:
            try:
                await ws.send_text(message)
            except Exception:
                self.active.remove(ws)


manager = ConnectionManager()


@app.on_event("startup")
async def start_subscriber():
    """Background task that listens to Redis pub/sub and broadcasts."""

    async def _listen():
        r = aioredis.from_url(REDIS_URL)
        pubsub = r.pubsub()
        await pubsub.subscribe(CHANNEL)
        async for msg in pubsub.listen():
            if msg["type"] == "message":
                await manager.broadcast(msg["data"].decode())

    asyncio.create_task(_listen())


@app.websocket("/ws")
async def websocket_endpoint(ws: WebSocket):
    await manager.connect(ws)
    try:
        while True:
            # Keep connection alive; client can also send messages
            await ws.receive_text()
    except WebSocketDisconnect:
        manager.disconnect(ws)


@app.post("/events")
async def publish_event(event_type: str, message: str = ""):
    """Publish an event to all connected clients via Redis pub/sub."""
    r = aioredis.from_url(REDIS_URL)
    payload = json.dumps({"type": event_type, "message": message})
    await r.publish(CHANNEL, payload)
    await r.close()
    return {"published": True, "type": event_type}


@app.get("/", response_class=HTMLResponse)
async def index():
    return open("static/index.html").read()


@app.get("/health")
async def health():
    return {"status": "ok"}
```

### static/index.html

```html
<!DOCTYPE html>
<html>
<head>
  <title>Live Feed</title>
  <style>
    body { font-family: system-ui; max-width: 600px; margin: 40px auto; }
    #feed { list-style: none; padding: 0; }
    #feed li { padding: 8px 12px; margin: 4px 0; background: #f0f4f8; border-radius: 6px; }
    .type { font-weight: 600; color: #2563eb; }
  </style>
</head>
<body>
  <h1>Live Event Feed</h1>
  <p id="status">Connecting...</p>
  <ul id="feed"></ul>
  <script>
    const ws = new WebSocket(`ws://${location.host}/ws`);
    const feed = document.getElementById("feed");
    const status = document.getElementById("status");

    ws.onopen = () => { status.textContent = "Connected"; };
    ws.onclose = () => { status.textContent = "Disconnected"; };

    ws.onmessage = (e) => {
      const event = JSON.parse(e.data);
      const li = document.createElement("li");
      li.innerHTML = `<span class="type">${event.type}</span> ${event.message}`;
      feed.prepend(li);
    };
  </script>
</body>
</html>
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

### Workflow

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
          name: realtime-app
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/realtime-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-realtime-app
          image: "${{ env.REGISTRY }}/realtime-app:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-realtime.localhost"
          health-check-path: "/health"
          dependencies:
            - type: redis
              name: pubsub-cache
```

### Try it

1. Open `http://<you>-realtime.localhost` in a browser tab
2. Publish events from another terminal:

```bash
# Publish events
curl -X POST "http://<you>-realtime.localhost/events?event_type=order&message=Order+%23123+placed"
curl -X POST "http://<you>-realtime.localhost/events?event_type=deploy&message=v2.1+deployed+to+staging"
```

Events appear in the browser instantly — no refresh needed.

### Iterate

```bash
kindling sync -n <you>-realtime-app -d .
# Edit the HTML, add event filtering, change the pub/sub channel
# structure — WebSocket connections reconnect automatically
```

---

## Tips

- **Multiple channels**: use separate Redis channels per event type
  (`events:orders`, `events:deploys`) and let clients subscribe to
  specific ones
- **Scaling**: with Redis pub/sub, you can run multiple replicas of the
  WebSocket server and every client still gets every event
- Open **two browser tabs** side by side to verify broadcast works
