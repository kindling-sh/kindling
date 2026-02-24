---
title: Background Workers
description: Run background jobs with a Redis task queue and a separate worker service.
---

# Background Workers

Most apps need to do work outside the request cycle — sending emails,
processing uploads, syncing data. This guide sets up a **task queue**
with Redis and a dedicated worker, both running locally on kindling.

---

## What you'll build

Two services sharing a Redis queue:

1. **API** — accepts requests and enqueues jobs
2. **Worker** — pulls jobs from Redis and processes them

```
┌──────────┐     ┌───────────────┐     ┌───────────┐     ┌───────────────┐
│ Browser  │────▶│  API          │────▶│  Redis    │◀────│  Worker       │
│          │◀────│  (FastAPI)    │     │  (queue)  │     │  (processor)  │
└──────────┘     └───────────────┘     └───────────┘     └───────────────┘
```

---

## Project structure

```
task-app/
├── api/
│   ├── Dockerfile
│   ├── requirements.txt
│   └── main.py
└── worker/
    ├── Dockerfile
    ├── requirements.txt
    └── main.py
```

### api/requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
redis==5.1.0
```

### api/main.py

```python
import json
import os
import uuid

import redis
from fastapi import FastAPI

app = FastAPI(title="Task API")
r = redis.from_url(os.environ["TASK_QUEUE_URL"])

QUEUE_NAME = "tasks"


@app.post("/tasks")
async def create_task(task_type: str, payload: dict = {}):
    """Enqueue a background job."""
    task_id = str(uuid.uuid4())[:8]
    task = {"id": task_id, "type": task_type, "payload": payload, "status": "queued"}
    r.lpush(QUEUE_NAME, json.dumps(task))
    r.hset(f"task:{task_id}", mapping={"status": "queued", "type": task_type})
    return task


@app.get("/tasks/{task_id}")
async def get_task(task_id: str):
    """Check the status of a task."""
    data = r.hgetall(f"task:{task_id}")
    if not data:
        return {"error": "Task not found"}
    return {k.decode(): v.decode() for k, v in data.items()}


@app.get("/health")
async def health():
    r.ping()
    return {"status": "ok"}
```

### worker/requirements.txt

```txt
redis==5.1.0
```

### worker/main.py

```python
import json
import os
import time

import redis

r = redis.from_url(os.environ["TASK_QUEUE_URL"])
QUEUE_NAME = "tasks"


def process_task(task: dict):
    """Process a single task. Replace with your real logic."""
    task_type = task["type"]
    task_id = task["id"]

    r.hset(f"task:{task_id}", "status", "processing")
    print(f"Processing {task_type} task {task_id}...")

    # Simulate work
    if task_type == "email":
        time.sleep(1)
        print(f"  Sent email to {task['payload'].get('to', 'unknown')}")
    elif task_type == "resize":
        time.sleep(2)
        print(f"  Resized image {task['payload'].get('filename', 'unknown')}")
    else:
        time.sleep(0.5)
        print(f"  Completed generic task")

    r.hset(f"task:{task_id}", "status", "complete")
    print(f"Task {task_id} complete")


def main():
    print("Worker started, waiting for tasks...")
    while True:
        # BRPOP blocks until a task is available (5 second timeout)
        result = r.brpop(QUEUE_NAME, timeout=5)
        if result:
            _, raw = result
            task = json.loads(raw)
            try:
                process_task(task)
            except Exception as e:
                print(f"Task {task.get('id')} failed: {e}")
                r.hset(f"task:{task['id']}", "status", "failed")


if __name__ == "__main__":
    main()
```

### Dockerfiles

**api/Dockerfile**

```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
```

**worker/Dockerfile**

```dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
CMD ["python", "main.py"]
```

---

## kindling setup

### Workflow

Both services share the same Redis instance via the dependency name.

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

      - name: Build API
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: task-api
          context: ${{ github.workspace }}/api
          image: "${{ env.REGISTRY }}/task-api:${{ env.TAG }}"

      - name: Build Worker
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: task-worker
          context: ${{ github.workspace }}/worker
          image: "${{ env.REGISTRY }}/task-worker:${{ env.TAG }}"

      - name: Deploy API
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-task-api
          image: "${{ env.REGISTRY }}/task-api:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-tasks.localhost"
          health-check-path: "/health"
          dependencies:
            - type: redis
              name: task-queue

      - name: Deploy Worker
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-task-worker
          image: "${{ env.REGISTRY }}/task-worker:${{ env.TAG }}"
          port: "8000"
          dependencies:
            - type: redis
              name: task-queue
```

Both services get `TASK_QUEUE_URL` injected automatically, pointing to
the same Redis instance.

### Try it

```bash
# Enqueue an email task
curl -X POST "http://<you>-tasks.localhost/tasks?task_type=email" \
  -H "Content-Type: application/json" \
  -d '{"to": "alice@example.com", "subject": "Hello"}'

# Check status
curl "http://<you>-tasks.localhost/tasks/abc123"

# Watch the worker process it
kindling logs <you>-task-worker
```

### Iterate

```bash
# Edit the worker logic — add a new task type, change processing
kindling sync -n <you>-task-worker -d worker/

# Edit the API — add bulk enqueue, priority queues
kindling sync -n <you>-task-api -d api/
```

---

## Next steps

- Add **priority queues** by using multiple Redis lists (`tasks:high`, `tasks:low`)
- Add **retry logic** by re-enqueuing failed tasks with a delay
- Switch to RabbitMQ (`- type: rabbitmq`) if you need acknowledgments
  and dead-letter queues
