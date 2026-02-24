---
title: "MongoDB Atlas Vector Search"
description: Connect an agent to MongoDB Atlas for managed vector search while running the app locally on kindling.
---

# MongoDB Atlas Vector Search

Run your agent locally on kindling while connecting to **MongoDB Atlas**
for vector search. This pattern is ideal when you want managed vector
infrastructure but still need fast, local iteration on your agent logic.

---

## When to use this pattern

- Your team already uses Atlas in production and you want dev parity
- You need Atlas-specific features (Atlas Search indexes, $vectorSearch aggregation)
- You want vector data to persist across `kindling reset` cycles
- You're fine with a cloud dependency for the vector store

---

## What you'll build

A FastAPI agent that:

1. Stores document embeddings in an Atlas collection with a vector search index
2. Queries Atlas using `$vectorSearch` for semantic retrieval
3. Reranks results with **Voyage AI** for higher precision
4. Generates answers with an LLM
5. Runs locally with `kindling sync` for sub-second iteration

```
┌──────────┐     ┌───────────────┐     ┌──────────────────┐
│ Browser  │────▶│  FastAPI      │────▶│  MongoDB Atlas   │
│  :8000   │◀────│  Agent        │     │  (vector search) │
└──────────┘     └───────┬───────┘     └──────────────────┘
                         │
                  ┌──────▼──────┐
                  │ Voyage AI   │  ← rerank top results
                  └──────┬──────┘
                    ┌────▼─────┐
                    │ OpenAI   │  ← generate answer
                    │ API      │
                    └──────────┘
```

---

## Project structure

```
atlas-agent/
├── Dockerfile
├── requirements.txt
└── main.py
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
pymongo==4.8.0
openai==1.50.0
voyageai==0.3.0
python-multipart==0.0.9
```

### main.py

```python
import os

import voyageai
from fastapi import FastAPI, UploadFile
from pymongo import MongoClient
from openai import OpenAI

app = FastAPI(title="Atlas Vector Agent")

# Connection string from kindling secrets — points to Atlas, not a local mongo
MONGO_URI = os.environ["MONGO_URI"]
OPENAI_API_KEY = os.environ["OPENAI_API_KEY"]
VOYAGE_API_KEY = os.environ["VOYAGE_API_KEY"]

client = MongoClient(MONGO_URI)
db = client["agent_dev"]
collection = db["documents"]
oai = OpenAI(api_key=OPENAI_API_KEY)
vo = voyageai.Client(api_key=VOYAGE_API_KEY)


def get_embedding(text: str) -> list[float]:
    resp = oai.embeddings.create(input=text, model="text-embedding-3-small")
    return resp.data[0].embedding


@app.post("/ingest")
async def ingest(file: UploadFile):
    """Chunk a document, embed it, store in Atlas."""
    content = (await file.read()).decode("utf-8")
    # Simple chunking — 500 chars with 50 overlap
    chunks = []
    for i in range(0, len(content), 450):
        chunk = content[i : i + 500]
        chunks.append(chunk)

    docs = []
    for chunk in chunks:
        docs.append({
            "text": chunk,
            "source": file.filename,
            "embedding": get_embedding(chunk),
        })
    collection.insert_many(docs)
    return {"chunks": len(docs), "source": file.filename}


@app.get("/ask")
async def ask(q: str):
    """Semantic search via Atlas $vectorSearch, rerank with Voyage, then answer."""
    query_embedding = get_embedding(q)

    # Step 1: Over-fetch candidates from Atlas
    results = list(collection.aggregate([
        {
            "$vectorSearch": {
                "index": "vector_index",
                "path": "embedding",
                "queryVector": query_embedding,
                "numCandidates": 200,
                "limit": 20,
            }
        },
        {"$project": {"text": 1, "source": 1, "score": {"$meta": "vectorSearchScore"}}},
    ]))

    # Step 2: Rerank with Voyage for higher precision
    if results:
        texts = [doc["text"] for doc in results]
        reranking = vo.rerank(q, texts, model="rerank-2", top_k=4)
        top_docs = [results[r.index] for r in reranking.results]
    else:
        top_docs = []

    context = "\n\n".join([doc["text"] for doc in top_docs])

    response = oai.chat.completions.create(
        model="gpt-4o-mini",
        messages=[
            {"role": "system", "content": f"Answer based on this context:\n\n{context}"},
            {"role": "user", "content": q},
        ],
    )
    return {"question": q, "answer": response.choices[0].message.content}


@app.get("/health")
async def health():
    return {"status": "ok"}
```

---

## Atlas setup (one-time)

Before deploying on kindling, create a vector search index in Atlas:

1. Go to **Atlas → Database → Browse Collections → agent_dev.documents**
2. Click **Search Indexes → Create Index → JSON Editor**
3. Use this definition:

```json
{
  "fields": [
    {
      "type": "vector",
      "path": "embedding",
      "numDimensions": 1536,
      "similarity": "cosine"
    }
  ]
}
```

4. Name it `vector_index` and create it.

---

## kindling setup

### 1. Store credentials

```bash
# Your Atlas connection string (from Atlas → Connect → Drivers)
kindling secrets set MONGO_URI "mongodb+srv://user:pass@cluster.mongodb.net/?retryWrites=true"

# OpenAI key for embeddings + chat
kindling secrets set OPENAI_API_KEY sk-your-key

# Voyage AI key for reranking (https://dash.voyageai.com)
kindling secrets set VOYAGE_API_KEY pa-your-key
```

### 2. Workflow

Note: **no `dependencies` block** — the database is external. We only
need secrets injected as env vars.

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

      - name: Build agent image
        uses: kindling-sh/kindling/.github/actions/kindling-build@main
        with:
          name: atlas-agent
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/atlas-agent:${{ env.TAG }}"

      - name: Deploy agent
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-atlas-agent
          image: "${{ env.REGISTRY }}/atlas-agent:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-atlas.localhost"
          health-check-path: "/health"
          env: |
            - name: MONGO_URI
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-mongo-uri
                  key: value
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-openai-api-key
                  key: value
            - name: VOYAGE_API_KEY
              valueFrom:
                secretKeyRef:
                  name: kindling-secret-voyage-api-key
                  key: value
```

### 3. Iterate

```bash
kindling sync -n <you>-atlas-agent -d .
# Edit main.py — change the search pipeline, add reranking,
# tweak the system prompt — see results instantly
```

---

## Tips

- **Use a dedicated Atlas project** for dev so you can drop collections
  freely without affecting production
- **Atlas M0 (free tier)** supports vector search and is more than enough
  for local development
- **Voyage reranking** adds ~200ms per query but significantly improves
  answer relevance — adjust `top_k` and the over-fetch `limit` to tune
  the latency/quality tradeoff
- Add `- type: redis` if you want to cache frequent queries locally
