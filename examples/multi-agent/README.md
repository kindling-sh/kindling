# Multi-Agent Research Pipeline

A two-service multi-agent system where specialist agents collaborate
through shared Redis context to research a topic.

## Architecture

```
                    ┌─────────────┐
  POST /research →  │ Orchestrator │  ← Express :3000 (ingress)
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              ▼                         ▼
    ┌──────────────┐          ┌──────────────┐
    │  Researcher   │ ──────→ │    Critic     │
    │    Agent      │  Redis  │    Agent      │
    └──────────────┘          └──────────────┘
              │                         │
              └─────────┬───────────────┘
                    ┌───┴───┐
                    │ Redis │
                    └───────┘
```

**Orchestrator** — receives a research topic, dispatches to two
specialist agents in sequence, and returns the combined result.

**Researcher agent** — generates findings from a knowledge base,
caches them in Redis for downstream agents.

**Critic agent** — reads the researcher's cached findings from Redis,
produces a critical assessment of quality and coverage.

## Flow

1. User POSTs `{ "topic": "quantum computing" }` to orchestrator
2. Orchestrator calls worker's `/agent/researcher` endpoint
3. Researcher generates findings and caches to `research:{requestId}` in Redis
4. Orchestrator calls worker's `/agent/critic` endpoint
5. Critic reads researcher's findings from Redis, writes a review to `review:{requestId}`
6. Orchestrator reads the final review from Redis and returns everything

## Running with kindling

```bash
# From the examples/multi-agent directory:
kindling generate -k <openai-key> -r .
kindling deploy -f .kindling/dev-environment.yaml
kindling dashboard
```

## Services

| Service       | Port | Dependencies |
|---------------|------|-------------|
| orchestrator  | 3000 | redis, worker (via WORKER_URL) |
| worker        | 3001 | redis |

## Environment Variables

| Variable      | Service      | Description                  |
|---------------|-------------|------------------------------|
| REDIS_URL     | both        | Auto-injected by kindling    |
| WORKER_URL    | orchestrator | `http://worker:3001`         |
| PORT          | both        | Service port (3000 / 3001)   |
| INDEX_DELAY_MS| worker      | Researcher indexing delay (default: 10) |
