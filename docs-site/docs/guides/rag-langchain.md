---
title: "RAG Agent with LangChain"
description: Build a retrieval-augmented generation agent using LangChain with local Postgres pgvector and Redis caching on kindling.
---

# RAG Agent with LangChain

Build a retrieval-augmented generation (RAG) agent that runs entirely on
your laptop — Postgres with pgvector for embeddings, Redis for response
caching, and LangChain orchestrating it all. No cloud vector database
required.

---

## What you'll build

A FastAPI service that:

1. Ingests documents and stores embeddings in Postgres via **pgvector**
2. Retrieves relevant context for user queries using semantic search
3. Generates answers with an LLM using the retrieved context
4. Caches frequent queries in Redis for sub-second responses

```
┌─────────────┐     ┌──────────────────┐     ┌──────────────┐
│   Browser    │────▶│   FastAPI + LC   │────▶│  OpenAI API  │
│  :8000       │◀────│   RAG Agent      │◀────│  (external)  │
└─────────────┘     └───────┬──────────┘     └──────────────┘
                        │        │
                   ┌────▼──┐  ┌──▼───┐
                   │Postgres│  │Redis │
                   │pgvector│  │cache │
                   └────────┘  └──────┘
```

---

## Project structure

```
rag-agent/
├── Dockerfile
├── requirements.txt
├── main.py              # FastAPI app + RAG chain
├── ingest.py            # Document ingestion script
└── .github/
    └── workflows/
        └── dev-deploy.yml
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
langchain==0.3.0
langchain-openai==0.2.0
langchain-postgres==0.0.12
pgvector==0.3.5
psycopg2-binary==2.9.9
redis==5.0.0
python-multipart==0.0.9
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

### main.py

```python
import os
import hashlib
import json

import redis
from fastapi import FastAPI, UploadFile
from langchain_openai import ChatOpenAI, OpenAIEmbeddings
from langchain_postgres import PGVector
from langchain.chains import RetrievalQA
from langchain.text_splitter import RecursiveCharacterTextSplitter

app = FastAPI(title="RAG Agent")

# Connection URLs are auto-injected by kindling
DATABASE_URL = os.environ["DATABASE_URL"]
REDIS_URL = os.environ["REDIS_URL"]
OPENAI_API_KEY = os.environ["OPENAI_API_KEY"]

# Initialize components
embeddings = OpenAIEmbeddings(api_key=OPENAI_API_KEY)
vectorstore = PGVector(
    connection=DATABASE_URL,
    embeddings=embeddings,
    collection_name="documents",
)
llm = ChatOpenAI(model="gpt-4o-mini", api_key=OPENAI_API_KEY)
cache = redis.from_url(REDIS_URL)


@app.post("/ingest")
async def ingest(file: UploadFile):
    """Split a document into chunks and store embeddings."""
    content = (await file.read()).decode("utf-8")
    splitter = RecursiveCharacterTextSplitter(chunk_size=500, chunk_overlap=50)
    chunks = splitter.split_text(content)
    vectorstore.add_texts(chunks, metadatas=[{"source": file.filename}] * len(chunks))
    return {"chunks": len(chunks), "source": file.filename}


@app.get("/ask")
async def ask(q: str):
    """Answer a question using RAG with Redis caching."""
    cache_key = f"rag:{hashlib.sha256(q.encode()).hexdigest()[:16]}"
    cached = cache.get(cache_key)
    if cached:
        return json.loads(cached)

    qa = RetrievalQA.from_chain_type(
        llm=llm,
        retriever=vectorstore.as_retriever(search_kwargs={"k": 4}),
    )
    result = qa.invoke(q)
    response = {"question": q, "answer": result["result"]}
    cache.setex(cache_key, 3600, json.dumps(response))
    return response


@app.get("/health")
async def health():
    return {"status": "ok"}
```

---

## kindling setup

### 1. Store your API key

```bash
kindling secrets set OPENAI_API_KEY sk-your-key-here
```

### 2. Deploy with the workflow

The `dependencies` block is where kindling shines — Postgres and Redis
are auto-provisioned with zero configuration, and connection URLs are
injected into your container automatically.

```yaml
# .github/workflows/dev-deploy.yml
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

      - name: Build RAG agent image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: rag-agent
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/rag-agent:${{ env.TAG }}"

      - name: Deploy RAG agent
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-rag-agent
          image: "${{ env.REGISTRY }}/rag-agent:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-rag.localhost"
          health-check-path: "/health"
          dependencies: |
            - type: postgres
            - type: redis
          env: |
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value
```

### 3. Try it

```bash
# Ingest a document
curl -F "file=@README.md" http://<you>-rag.localhost/ingest

# Ask a question
curl "http://<you>-rag.localhost/ask?q=what+does+this+project+do"
```

---

## Iterate with sync

Edit `main.py` locally — change the chunk size, swap the retriever,
add a reranker — and see it live in seconds:

```bash
kindling sync -n <you>-rag-agent -d .
# Edit main.py → changes appear instantly
# Ctrl+C → deployment rolls back
```

---

## Why local matters for RAG

- **Embedding latency** — pgvector runs on localhost, so vector search
  is single-digit milliseconds instead of a cloud round-trip
- **Free iteration** — tune chunk sizes, overlap, retrieval `k` values,
  and prompt templates without burning cloud credits
- **Data stays local** — ingest proprietary docs without sending them to
  a third-party vector database
- **Full observability** — `kindling logs` shows exactly what's happening
  in Postgres, Redis, and your app simultaneously

---

## Next steps

- Add [Jaeger](../dependencies.md) for tracing LangChain spans:
  `- type: jaeger`
- Use `kindling expose` to test with OAuth-protected endpoints
- Scale up to a [multi-service architecture](./multi-service.md) with
  a separate ingestion worker
