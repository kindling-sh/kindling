---
title: "Multi-Agent System with CrewAI"
description: Deploy a CrewAI multi-agent system as microservices with NATS messaging on kindling.
---

# Multi-Agent System with CrewAI

Deploy a CrewAI multi-agent system where specialized agents run as
separate services, communicating through NATS. Each agent has its own
tools, memory, and scaling — all running locally on your laptop.

---

## What you'll build

Three services forming an AI research pipeline:

1. **Researcher agent** — takes a topic, searches the web, collects sources
2. **Writer agent** — receives research, produces a structured draft
3. **API gateway** — accepts requests, dispatches to agents via NATS, returns results

```
┌──────────┐     ┌───────────┐     ┌────────────────┐     ┌──────────┐
│ Browser  │────▶│  Gateway  │────▶│     NATS       │────▶│Researcher│
│          │◀────│  :8000    │◀────│  (pub/sub)     │────▶│  Agent   │
└──────────┘     └───────────┘     └────────────────┘     └──────────┘
                                          │
                                   ┌──────▼──────┐
                                   │   Writer    │
                                   │   Agent    │
                                   └─────────────┘
```

---

## Project structure

```
crewai-agents/
├── gateway/
│   ├── Dockerfile
│   ├── requirements.txt
│   └── main.py
├── researcher/
│   ├── Dockerfile
│   ├── requirements.txt
│   └── main.py
└── writer/
    ├── Dockerfile
    ├── requirements.txt
    └── main.py
```

### gateway/main.py

```python
import os
import json
import asyncio

import nats
from fastapi import FastAPI

app = FastAPI(title="Agent Gateway")
NATS_URL = os.environ["NATS_URL"]


@app.post("/research")
async def research(topic: str):
    """Dispatch a research request and wait for the final result."""
    nc = await nats.connect(NATS_URL)

    # Send topic to researcher
    future = asyncio.get_event_loop().create_future()

    async def on_result(msg):
        future.set_result(json.loads(msg.data.decode()))

    await nc.subscribe("results.final", cb=on_result)
    await nc.publish("tasks.research", json.dumps({"topic": topic}).encode())

    result = await asyncio.wait_for(future, timeout=120)
    await nc.close()
    return result


@app.get("/health")
async def health():
    return {"status": "ok"}
```

### researcher/main.py

```python
import os
import json
import asyncio

import nats
from crewai import Agent, Task, Crew
from crewai_tools import SerperDevTool

NATS_URL = os.environ["NATS_URL"]
OPENAI_API_KEY = os.environ["OPENAI_API_KEY"]
os.environ["OPENAI_API_KEY"] = OPENAI_API_KEY  # CrewAI reads from env


async def main():
    nc = await nats.connect(NATS_URL)

    search = SerperDevTool()
    researcher = Agent(
        role="Senior Research Analyst",
        goal="Find comprehensive, accurate information on a given topic",
        backstory="Expert at finding and synthesizing information from multiple sources.",
        tools=[search],
        verbose=True,
    )

    async def on_task(msg):
        data = json.loads(msg.data.decode())
        topic = data["topic"]

        task = Task(
            description=f"Research the topic: {topic}. Find key facts, recent developments, and expert opinions.",
            expected_output="A detailed research brief with sources.",
            agent=researcher,
        )
        crew = Crew(agents=[researcher], tasks=[task])
        result = crew.kickoff()

        # Pass research to writer
        await nc.publish("tasks.write", json.dumps({
            "topic": topic,
            "research": str(result),
        }).encode())

    await nc.subscribe("tasks.research", cb=on_task)
    print("Researcher agent listening on tasks.research")
    await asyncio.Event().wait()


if __name__ == "__main__":
    asyncio.run(main())
```

### writer/main.py

```python
import os
import json
import asyncio

import nats
from crewai import Agent, Task, Crew

NATS_URL = os.environ["NATS_URL"]
OPENAI_API_KEY = os.environ["OPENAI_API_KEY"]
os.environ["OPENAI_API_KEY"] = OPENAI_API_KEY


async def main():
    nc = await nats.connect(NATS_URL)

    writer = Agent(
        role="Content Writer",
        goal="Transform research into clear, engaging content",
        backstory="Skilled at taking raw research and producing polished articles.",
        verbose=True,
    )

    async def on_task(msg):
        data = json.loads(msg.data.decode())

        task = Task(
            description=f"Write a well-structured article about '{data['topic']}' using this research:\n\n{data['research']}",
            expected_output="A polished article with introduction, body sections, and conclusion.",
            agent=writer,
        )
        crew = Crew(agents=[writer], tasks=[task])
        result = crew.kickoff()

        await nc.publish("results.final", json.dumps({
            "topic": data["topic"],
            "article": str(result),
        }).encode())

    await nc.subscribe("tasks.write", cb=on_task)
    print("Writer agent listening on tasks.write")
    await asyncio.Event().wait()


if __name__ == "__main__":
    asyncio.run(main())
```

---

## kindling setup

### 1. Store API keys

```bash
kindling secrets set OPENAI_API_KEY sk-your-key
kindling secrets set SERPER_API_KEY your-serper-key  # for web search
```

### 2. Workflow

Each agent is its own service with its own DSE. They share NATS for
messaging — kindling auto-provisions it and injects `NATS_URL` into
every service that declares it as a dependency.

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

      # -- Build all images --
      - name: Build gateway
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: gateway
          context: ${{ github.workspace }}/gateway
          image: "${{ env.REGISTRY }}/gateway:${{ env.TAG }}"

      - name: Build researcher
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: researcher
          context: ${{ github.workspace }}/researcher
          image: "${{ env.REGISTRY }}/researcher:${{ env.TAG }}"

      - name: Build writer
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: writer
          context: ${{ github.workspace }}/writer
          image: "${{ env.REGISTRY }}/writer:${{ env.TAG }}"

      # -- Deploy agents first (they listen on NATS) --
      - name: Deploy researcher
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-researcher
          image: "${{ env.REGISTRY }}/researcher:${{ env.TAG }}"
          port: "8001"
          health-check-type: "none"
          dependencies: |
            - type: nats
          env: |
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value
            - name: SERPER_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-serper-api-key
                  key: value

      - name: Deploy writer
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-writer
          image: "${{ env.REGISTRY }}/writer:${{ env.TAG }}"
          port: "8002"
          health-check-type: "none"
          dependencies: |
            - type: nats
          env: |
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value

      # -- Deploy gateway last (it publishes to NATS) --
      - name: Deploy gateway
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-gateway
          image: "${{ env.REGISTRY }}/gateway:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-agents.localhost"
          health-check-path: "/health"
          dependencies: |
            - type: nats
          env: |
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value
```

### 3. Try it

```bash
curl -X POST "http://<you>-agents.localhost/research?topic=quantum+computing+2026"
```

### 4. Iterate on a single agent

Edit just the researcher's prompt or tools without redeploying the whole system:

```bash
kindling sync -n <you>-researcher -d ./researcher
```

---

## Why kindling fits multi-agent

- **Isolation** — each agent is a separate pod with its own logs, scaling, and crash boundary
- **NATS is free** — `- type: nats` and it's running, ~15 MB overhead
- **Independent sync** — edit one agent's code, sync just that service, others keep running
- **Observability** — `kindling logs -n <you>-researcher` shows just that agent's chain-of-thought
- **Add agents easily** — new agent = new directory + new build/deploy step, everything else stays the same
