---
title: "LangGraph Stateful Agents"
description: Build stateful, multi-step agents with LangGraph using Redis for checkpointing and Postgres for conversation history.
---

# LangGraph Stateful Agents

Build agents that **remember state across steps** using LangGraph, with
Redis for checkpoint storage and Postgres for conversation history —
all running locally on kindling.

---

## Why LangGraph on kindling

LangGraph agents need persistent state — checkpoints between graph
nodes, conversation memory, tool call history. In production you'd
use managed Redis and Postgres. On kindling, those same services spin
up automatically in your local cluster, so you iterate on graph logic
without deploying anywhere.

The key benefit: your agent's state survives `kindling sync` restarts.
Edit a node's logic, sync, and pick up the same conversation mid-flow.

---

## What you'll build

A research agent that:

1. Takes a research question
2. Plans sub-questions (planner node)
3. Searches for answers (researcher node, loops until satisfied)
4. Synthesizes a final report (writer node)
5. Persists state in Redis so you can resume interrupted research

```
                    ┌──────────┐
                    │ Planner  │
                    └────┬─────┘
                         │
                    ┌────▼─────┐
              ┌────▶│Researcher│──────┐
              │     └────┬─────┘      │
              │          │ enough?    │
              │    no ───┘      yes ──┘
              │                       │
              └───────────────   ┌────▼─────┐
                                 │  Writer  │
                                 └──────────┘

  State checkpointed in Redis at every transition
  Conversation history stored in Postgres
```

---

## Project structure

```
research-agent/
├── Dockerfile
├── requirements.txt
├── main.py
├── graph.py
└── models.py
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
langgraph==0.2.0
langchain-openai==0.2.0
langchain-core==0.3.0
redis==5.1.0
asyncpg==0.30.0
```

### models.py

```python
from __future__ import annotations

from typing import Annotated
from typing_extensions import TypedDict
from langgraph.graph.message import add_messages


class ResearchState(TypedDict):
    """State passed between graph nodes."""
    messages: Annotated[list, add_messages]
    question: str
    sub_questions: list[str]
    findings: list[dict]
    iteration: int
    max_iterations: int
    report: str
```

### graph.py

```python
import json
import os

from langchain_openai import ChatOpenAI
from langgraph.graph import StateGraph, END

from models import ResearchState

llm = ChatOpenAI(model="gpt-4o-mini", api_key=os.environ["OPENAI_API_KEY"])


async def planner(state: ResearchState) -> dict:
    """Break the research question into sub-questions."""
    response = await llm.ainvoke([
        {"role": "system", "content": (
            "You are a research planner. Given a question, produce 3-5 "
            "focused sub-questions that together would answer the main question. "
            "Return a JSON array of strings."
        )},
        {"role": "user", "content": state["question"]},
    ])
    sub_questions = json.loads(response.content)
    return {
        "sub_questions": sub_questions,
        "messages": [{"role": "assistant", "content": f"Planning: {len(sub_questions)} sub-questions"}],
        "iteration": 0,
    }


async def researcher(state: ResearchState) -> dict:
    """Research the next sub-question."""
    idx = state["iteration"]
    sub_questions = state["sub_questions"]

    if idx >= len(sub_questions):
        return {"iteration": idx}

    sq = sub_questions[idx]
    response = await llm.ainvoke([
        {"role": "system", "content": (
            "You are a thorough researcher. Answer this specific sub-question "
            "with concrete facts and details. Be concise but comprehensive."
        )},
        {"role": "user", "content": sq},
    ])
    finding = {"question": sq, "answer": response.content}
    return {
        "findings": state.get("findings", []) + [finding],
        "iteration": idx + 1,
        "messages": [{"role": "assistant", "content": f"Researched: {sq}"}],
    }


def should_continue(state: ResearchState) -> str:
    """Decide whether to keep researching or write the report."""
    if state["iteration"] < len(state["sub_questions"]):
        return "researcher"
    return "writer"


async def writer(state: ResearchState) -> dict:
    """Synthesize findings into a final report."""
    findings_text = "\n\n".join(
        f"**{f['question']}**\n{f['answer']}" for f in state.get("findings", [])
    )
    response = await llm.ainvoke([
        {"role": "system", "content": (
            "You are a report writer. Synthesize the research findings into "
            "a clear, well-structured report that answers the original question."
        )},
        {"role": "user", "content": (
            f"Original question: {state['question']}\n\n"
            f"Research findings:\n{findings_text}"
        )},
    ])
    return {
        "report": response.content,
        "messages": [{"role": "assistant", "content": "Report complete."}],
    }


def build_graph() -> StateGraph:
    graph = StateGraph(ResearchState)
    graph.add_node("planner", planner)
    graph.add_node("researcher", researcher)
    graph.add_node("writer", writer)

    graph.set_entry_point("planner")
    graph.add_edge("planner", "researcher")
    graph.add_conditional_edges("researcher", should_continue)
    graph.add_edge("writer", END)

    return graph.compile()
```

### main.py

```python
import os
import uuid
from contextlib import asynccontextmanager

import asyncpg
import redis.asyncio as aioredis
from fastapi import FastAPI

from graph import build_graph

REDIS_URL = os.environ["RESEARCH_CACHE_URL"]
DATABASE_URL = os.environ["RESEARCH_DB_URL"]


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Set up Redis for LangGraph checkpointing
    app.state.redis = aioredis.from_url(REDIS_URL)
    # Set up Postgres for conversation history
    app.state.pool = await asyncpg.create_pool(DATABASE_URL, min_size=2, max_size=5)

    # Create history table
    async with app.state.pool.acquire() as conn:
        await conn.execute("""
            CREATE TABLE IF NOT EXISTS research_sessions (
                id TEXT PRIMARY KEY,
                question TEXT NOT NULL,
                report TEXT,
                status TEXT DEFAULT 'running',
                created_at TIMESTAMPTZ DEFAULT now(),
                completed_at TIMESTAMPTZ
            )
        """)

    app.state.graph = build_graph()
    yield
    await app.state.pool.close()
    await app.state.redis.close()


app = FastAPI(title="Research Agent", lifespan=lifespan)


@app.post("/research")
async def start_research(question: str, max_iterations: int = 5):
    """Start a new research session."""
    session_id = str(uuid.uuid4())[:8]

    # Store session in Postgres
    async with app.state.pool.acquire() as conn:
        await conn.execute(
            "INSERT INTO research_sessions (id, question) VALUES ($1, $2)",
            session_id, question,
        )

    # Run the graph
    result = await app.state.graph.ainvoke({
        "question": question,
        "messages": [],
        "sub_questions": [],
        "findings": [],
        "iteration": 0,
        "max_iterations": max_iterations,
        "report": "",
    })

    # Update session with results
    async with app.state.pool.acquire() as conn:
        await conn.execute(
            "UPDATE research_sessions SET report = $1, status = 'complete', "
            "completed_at = now() WHERE id = $2",
            result["report"], session_id,
        )

    return {
        "session_id": session_id,
        "question": question,
        "sub_questions": result["sub_questions"],
        "report": result["report"],
    }


@app.get("/research/{session_id}")
async def get_session(session_id: str):
    """Retrieve a past research session."""
    async with app.state.pool.acquire() as conn:
        row = await conn.fetchrow(
            "SELECT * FROM research_sessions WHERE id = $1", session_id
        )
    if not row:
        return {"error": "Session not found"}
    return dict(row)


@app.get("/research")
async def list_sessions():
    """List recent research sessions."""
    async with app.state.pool.acquire() as conn:
        rows = await conn.fetch(
            "SELECT id, question, status, created_at FROM research_sessions "
            "ORDER BY created_at DESC LIMIT 20"
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

### 1. Store your API key

```bash
kindling secrets set OPENAI_API_KEY sk-your-key
```

### 2. Workflow

Both Redis and Postgres are local kindling dependencies — no cloud
services needed.

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

      - name: Build research agent
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: research-agent
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/research-agent:${{ env.TAG }}"

      - name: Deploy research agent
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-research-agent
          image: "${{ env.REGISTRY }}/research-agent:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-research.localhost"
          health-check-path: "/health"
          dependencies:
            - type: postgres
              name: research-db
            - type: redis
              name: research-cache
          env: |
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value
```

kindling auto-injects `RESEARCH_DB_URL` and `RESEARCH_CACHE_URL` as
env vars.

### 3. Iterate on graph logic

```bash
kindling sync -n <you>-research-agent -d .
# Edit graph.py — add a new node, change the routing logic,
# tweak prompts — state persists across syncs
```

---

## Tips

- **Add nodes incrementally**: start with planner → writer, then insert
  researcher in between. Sync after each change to test.
- **Inspect state**: hit `GET /research/{session_id}` to see the full
  report and verify your graph produced the right output.
- **Redis persistence**: data survives `kindling sync` but not
  `kindling reset`. Use Postgres for anything you want to keep.
- **Swap to LangGraph Cloud later**: the graph definition is the same —
  only the checkpoint backend changes from local Redis to LangGraph's
  managed persistence.
