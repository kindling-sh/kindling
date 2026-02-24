---
title: S3-Compatible File Uploads
description: Handle file uploads locally using MinIO as an S3-compatible object store.
---

# S3-Compatible File Uploads

Use **MinIO** as a local drop-in replacement for Amazon S3. Upload,
download, and manage files with the same SDK you'd use in production —
no AWS account or credentials needed.

---

## What you'll build

A file upload API that:

1. Accepts file uploads via multipart form
2. Stores files in MinIO (local S3-compatible storage)
3. Returns presigned download URLs
4. Runs locally with `kindling sync`

```
┌──────────┐     ┌───────────────┐     ┌───────────────┐
│ Browser  │────▶│  FastAPI       │────▶│  MinIO        │
│          │◀────│  Upload API   │     │  (local S3)   │
└──────────┘     └───────────────┘     └───────────────┘
```

---

## Project structure

```
upload-app/
├── Dockerfile
├── requirements.txt
└── main.py
```

### requirements.txt

```txt
fastapi==0.115.0
uvicorn[standard]==0.30.0
boto3==1.35.0
python-multipart==0.0.9
```

### main.py

```python
import os
from urllib.parse import urlparse

import boto3
from botocore.config import Config
from fastapi import FastAPI, UploadFile, HTTPException

app = FastAPI(title="File Upload API")

# kindling injects the MinIO connection URL
MINIO_URL = os.environ["FILE_STORE_URL"]

# Parse the connection URL for S3 client config
parsed = urlparse(MINIO_URL)
ENDPOINT = f"{parsed.scheme}://{parsed.hostname}:{parsed.port}"
ACCESS_KEY = parsed.username or "minioadmin"
SECRET_KEY = parsed.password or "minioadmin"
BUCKET = "uploads"

s3 = boto3.client(
    "s3",
    endpoint_url=ENDPOINT,
    aws_access_key_id=ACCESS_KEY,
    aws_secret_access_key=SECRET_KEY,
    config=Config(signature_version="s3v4"),
    region_name="us-east-1",
)


@app.on_event("startup")
async def ensure_bucket():
    """Create the uploads bucket if it doesn't exist."""
    try:
        s3.head_bucket(Bucket=BUCKET)
    except Exception:
        s3.create_bucket(Bucket=BUCKET)


@app.post("/upload")
async def upload_file(file: UploadFile):
    """Upload a file to MinIO."""
    if not file.filename:
        raise HTTPException(400, "No filename")

    content = await file.read()
    key = file.filename

    s3.put_object(
        Bucket=BUCKET,
        Key=key,
        Body=content,
        ContentType=file.content_type or "application/octet-stream",
    )

    return {
        "key": key,
        "size": len(content),
        "content_type": file.content_type,
    }


@app.get("/files")
async def list_files():
    """List all uploaded files."""
    response = s3.list_objects_v2(Bucket=BUCKET)
    files = []
    for obj in response.get("Contents", []):
        files.append({
            "key": obj["Key"],
            "size": obj["Size"],
            "modified": obj["LastModified"].isoformat(),
        })
    return files


@app.get("/files/{key:path}")
async def get_download_url(key: str, expires: int = 3600):
    """Get a presigned download URL for a file."""
    try:
        s3.head_object(Bucket=BUCKET, Key=key)
    except Exception:
        raise HTTPException(404, "File not found")

    url = s3.generate_presigned_url(
        "get_object",
        Params={"Bucket": BUCKET, "Key": key},
        ExpiresIn=expires,
    )
    return {"key": key, "download_url": url, "expires_in": expires}


@app.delete("/files/{key:path}")
async def delete_file(key: str):
    """Delete a file."""
    s3.delete_object(Bucket=BUCKET, Key=key)
    return {"deleted": key}


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
          name: upload-app
          context: ${{ github.workspace }}
          image: "${{ env.REGISTRY }}/upload-app:${{ env.TAG }}"

      - name: Deploy
        uses: kindling-sh/kindling/.github/actions/kindling-deploy@main
        with:
          name: ${{ github.actor }}-upload-app
          image: "${{ env.REGISTRY }}/upload-app:${{ env.TAG }}"
          port: "8000"
          ingress-host: "${{ github.actor }}-uploads.localhost"
          health-check-path: "/health"
          dependencies:
            - type: minio
              name: file-store
```

kindling auto-injects `FILE_STORE_URL` with credentials baked in.

### Try it

```bash
# Upload a file
curl -X POST "http://<you>-uploads.localhost/upload" \
  -F "file=@README.md"

# List files
curl "http://<you>-uploads.localhost/files"

# Get a download URL
curl "http://<you>-uploads.localhost/files/README.md"

# Delete
curl -X DELETE "http://<you>-uploads.localhost/files/README.md"
```

### Iterate

```bash
kindling sync -n <you>-upload-app -d .
# Add image resizing, virus scanning, folder structure —
# files persist in MinIO across syncs
```

---

## Tips

- **Same SDK in production**: swap `ENDPOINT` to your real S3/R2/GCS
  endpoint and the code works unchanged
- **MinIO Console**: expose MinIO's web UI on port 9001 to browse
  buckets visually: `kindling expose <you>-file-store 9001`
- Files persist across `kindling sync` but are cleared on
  `kindling reset` — use that to start fresh
