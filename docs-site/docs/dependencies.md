---
sidebar_position: 5
title: Dependency Reference
description: All 15 auto-provisioned dependency types with connection URLs, env vars, and code examples.
---

# Dependency Reference

The kindling operator auto-provisions backing services alongside your
application. You declare dependencies in the `DevStagingEnvironment` CR
and the operator creates a Pod, Service, and credential Secret for each
one, then **injects connection-string environment variables** into your
app container automatically.

:::tip Dashboard
You can also manage dependencies visually in the dashboard: **Setup → App Designer** (topology editor). Draw edges from services to dependency nodes and deploy from the canvas. See [Dashboard](dashboard.md) for details.
:::

---

## How it works

```
                                       ┌──────────────────────────────────────┐
     DevStagingEnvironment CR          │  Operator auto-provisions:           │
  ┌──────────────────────────┐         │                                      │
  │ dependencies:            │         │  1. Deployment (postgres:16)         │
  │   - type: postgres       │ ──────▶ │  2. Service   (<name>-postgres)      │
  │     version: "16"        │         │  3. Secret    (<name>-postgres-creds) │
  │   - type: redis          │         │  4. ENV injection: DATABASE_URL      │
  └──────────────────────────┘         │                                      │
                                       │  (same for redis → REDIS_URL, etc.)  │
                                       └──────────────────────────────────────┘
```

When the operator processes a dependency, it:

1. Looks up the dependency type in its internal **registry** (image, port, default credentials)
2. Creates a **Deployment** running the service (e.g. `postgres:16`)
3. Creates a **ClusterIP Service** named `<cr-name>-<type>` (e.g. `myapp-postgres`)
4. Creates a **Secret** with all credential key/value pairs
5. Builds a **connection URL** using the in-cluster DNS name and injects it as an env var into your app container

Your application code just reads the injected env var — no connection
strings to hardcode, no service discovery to implement.

---

## Quick reference table

| Type | Env var injected | Connection URL format | Default port |
|---|---|---|---|
| `postgres` | `DATABASE_URL` | `postgres://devuser:devpass@<name>-postgres:5432/devdb?sslmode=disable` | 5432 |
| `redis` | `REDIS_URL` | `redis://<name>-redis:6379/0` | 6379 |
| `mysql` | `DATABASE_URL` | `mysql://devuser:devpass@<name>-mysql:3306/devdb` | 3306 |
| `mongodb` | `MONGO_URL` | `mongodb://devuser:devpass@<name>-mongodb:27017` | 27017 |
| `rabbitmq` | `AMQP_URL` | `amqp://devuser:devpass@<name>-rabbitmq:5672/` | 5672 |
| `minio` | `S3_ENDPOINT` | `http://<name>-minio:9000` | 9000 |
| `elasticsearch` | `ELASTICSEARCH_URL` | `http://<name>-elasticsearch:9200` | 9200 |
| `kafka` | `KAFKA_BROKER_URL` | `<name>-kafka:9092` | 9092 |
| `nats` | `NATS_URL` | `nats://<name>-nats:4222` | 4222 |
| `memcached` | `MEMCACHED_URL` | `<name>-memcached:11211` | 11211 |
| `cassandra` | `CASSANDRA_URL` | `<name>-cassandra:9042` | 9042 |
| `consul` | `CONSUL_HTTP_ADDR` | `http://<name>-consul:8500` | 8500 |
| `vault` | `VAULT_ADDR` | `http://<name>-vault:8200` | 8200 |
| `influxdb` | `INFLUXDB_URL` | `http://devuser:devpass123@<name>-influxdb:8086` | 8086 |
| `jaeger` | `JAEGER_ENDPOINT` | `http://<name>-jaeger:16686` | 16686 |

> `<name>` is the `metadata.name` from your DevStagingEnvironment CR.

---

## Overriding defaults

Every dependency supports these optional fields:

```yaml
dependencies:
  - type: postgres
    version: "15"              # Image tag (default: latest)
    image: "my-registry/pg:15" # Full image override
    port: 5433                 # Override default port
    envVarName: "PG_URL"       # Override injected env var name
    storageSize: "5Gi"         # PVC size for stateful deps
    env:                       # Override container env vars
      - name: POSTGRES_USER
        value: "custom_user"
      - name: POSTGRES_PASSWORD
        value: "custom_pass"
      - name: POSTGRES_DB
        value: "custom_db"
    resources:                 # CPU/memory limits
      cpuRequest: "100m"
      cpuLimit: "500m"
      memoryRequest: "256Mi"
      memoryLimit: "1Gi"
    shared: false              # See "Shared dependencies" below
    sharedKey: ""               # Groups shared instances (defaults to type)
```

---

## Shared dependencies

By default, **every DSE gets its own dedicated instance** of each dependency
it declares (`<dse-name>-redis`, `<dse-name>-postgres`, etc.). If several
services in the same project each just need a cheap, ephemeral
cache/session-store (a very common pattern — five services each declaring
`redis` for the same kind of workload), that means five separate Redis
pods running locally for no real isolation benefit.

Set `shared: true` on a dependency to converge it with every other DSE
that also sets `shared: true` for the same `type` (and the same
`sharedKey`, if set) — instead of five dedicated Redis pods, you get one:

```yaml
dependencies:
  - type: redis
    shared: true
```

All services with `shared: true` for `redis` connect to the **same**
Redis instance (`shared-redis`). Redis specifically gets a small bonus:
each DSE is assigned its own logical Redis DB index (0-15, derived from
the DSE name) so keys don't collide between services even though they
share the same instance — no code changes needed, it's baked into the
injected `REDIS_URL` (`redis://shared-redis:6379/<n>`). Other dependency
types don't get automatic per-tenant isolation — if that matters for a
non-Redis dependency, use separate `sharedKey` groups instead.

Use `sharedKey` to split shared instances into separate groups instead of
one cluster-wide instance per type — e.g. a `billing` group and an
`analytics` group each get their own shared Redis:

```yaml
# billing service
dependencies:
  - type: redis
    shared: true
    sharedKey: billing

# analytics service
dependencies:
  - type: redis
    shared: true
    sharedKey: analytics
```

Shared instances are **not** owned by any single DSE — deleting one DSE
that references a shared dependency doesn't take it down for the others.
They're also not deleted automatically once nothing references them
anymore; clean those up explicitly:

```bash
kindling deps list-shared    # see shared instances + who references them
kindling deps prune-shared   # delete shared instances nothing references
```

`kindling generate --shared-deps redis,postgres` marks matching
dependency types as `shared: true` across every detected service in the
AI-generated workflow, instead of editing each service's YAML by hand.

> Note: this only affects the **local Kind dev cluster** (where the
> operator provisions a dedicated dependency per DSE). `kindling snapshot`'s
> production Helm/Kustomize export already deduplicates dependencies by
> type across the whole chart, so a graduated production deployment always
> shares one instance per type regardless of this setting.

---

## Detailed specifications

### PostgreSQL

**Type:** `postgres` · **Port:** 5432 · **Env:** `DATABASE_URL`

```yaml
dependencies:
  - type: postgres
    version: "16"
```

**Connection string:** `postgres://devuser:devpass@<name>-postgres:5432/devdb?sslmode=disable`

<details>
<summary>Code examples</summary>

**Go:**
```go
dsn := os.Getenv("DATABASE_URL")
db, err := sql.Open("postgres", dsn)
```

**Python:**
```python
conn = psycopg2.connect(os.environ["DATABASE_URL"])
```

**Node.js:**
```javascript
const pool = new Pool({ connectionString: process.env.DATABASE_URL });
```

</details>

---

### Redis

**Type:** `redis` · **Port:** 6379 · **Env:** `REDIS_URL`

```yaml
dependencies:
  - type: redis
```

**Connection string:** `redis://<name>-redis:6379/0`

<details>
<summary>Code examples</summary>

**Go:**
```go
opt, _ := redis.ParseURL(os.Getenv("REDIS_URL"))
client := redis.NewClient(opt)
```

**Python:**
```python
r = redis.from_url(os.environ["REDIS_URL"])
```

**Node.js:**
```javascript
const client = createClient({ url: process.env.REDIS_URL });
```

</details>

---

### MySQL

**Type:** `mysql` · **Port:** 3306 · **Env:** `DATABASE_URL`

```yaml
dependencies:
  - type: mysql
    version: "8.0"
```

**Connection string:** `mysql://devuser:devpass@<name>-mysql:3306/devdb`

---

### MongoDB

**Type:** `mongodb` · **Port:** 27017 · **Env:** `MONGO_URL`

```yaml
dependencies:
  - type: mongodb
```

**Connection string:** `mongodb://devuser:devpass@<name>-mongodb:27017`

---

### RabbitMQ

**Type:** `rabbitmq` · **Port:** 5672 · **Env:** `AMQP_URL`

```yaml
dependencies:
  - type: rabbitmq
```

**Connection string:** `amqp://devuser:devpass@<name>-rabbitmq:5672/`

Management UI available on port `15672`.

---

### MinIO (S3-compatible)

**Type:** `minio` · **Port:** 9000 · **Env:** `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`

```yaml
dependencies:
  - type: minio
```

**Endpoint:** `http://<name>-minio:9000`

---

### Elasticsearch

**Type:** `elasticsearch` · **Port:** 9200 · **Env:** `ELASTICSEARCH_URL`

```yaml
dependencies:
  - type: elasticsearch
```

**Connection string:** `http://<name>-elasticsearch:9200`

Runs in single-node mode with security disabled for dev.

---

### Kafka (KRaft mode)

**Type:** `kafka` · **Port:** 9092 · **Env:** `KAFKA_BROKER_URL`

```yaml
dependencies:
  - type: kafka
```

**Broker:** `<name>-kafka:9092`

Runs in KRaft mode (no ZooKeeper required).

---

### NATS

**Type:** `nats` · **Port:** 4222 · **Env:** `NATS_URL`

```yaml
dependencies:
  - type: nats
```

**Connection string:** `nats://<name>-nats:4222`

---

### Memcached

**Type:** `memcached` · **Port:** 11211 · **Env:** `MEMCACHED_URL`

```yaml
dependencies:
  - type: memcached
```

**Address:** `<name>-memcached:11211`

---

### Cassandra

**Type:** `cassandra` · **Port:** 9042 · **Env:** `CASSANDRA_URL`

```yaml
dependencies:
  - type: cassandra
```

**Contact point:** `<name>-cassandra:9042`

---

### Consul

**Type:** `consul` · **Port:** 8500 · **Env:** `CONSUL_HTTP_ADDR`

```yaml
dependencies:
  - type: consul
```

**Address:** `http://<name>-consul:8500`

---

### Vault

**Type:** `vault` · **Port:** 8200 · **Env:** `VAULT_ADDR`, `VAULT_TOKEN`

```yaml
dependencies:
  - type: vault
```

**Address:** `http://<name>-vault:8200`
**Dev root token:** `dev-root-token`

---

### InfluxDB

**Type:** `influxdb` · **Port:** 8086 · **Env:** `INFLUXDB_URL`, `INFLUXDB_ORG`, `INFLUXDB_BUCKET`

```yaml
dependencies:
  - type: influxdb
```

**Connection string:** `http://devuser:devpass123@<name>-influxdb:8086`

---

### Jaeger

**Type:** `jaeger` · **Port:** 16686 · **Env:** `JAEGER_ENDPOINT`, `OTEL_EXPORTER_OTLP_ENDPOINT`

```yaml
dependencies:
  - type: jaeger
```

**UI endpoint:** `http://<name>-jaeger:16686`
**OTLP gRPC:** `http://<name>-jaeger:4317`

:::tip
Many OpenTelemetry SDKs automatically read `OTEL_EXPORTER_OTLP_ENDPOINT` from the environment — you may not need to configure it explicitly.
:::

---

## Multiple dependencies

You can combine any number of dependencies in a single CR:

```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: my-platform
spec:
  deployment:
    image: registry:5000/my-platform:latest
    port: 8080
  service:
    port: 8080
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
    - type: elasticsearch
    - type: kafka
    - type: vault
```

All five connection env vars (`DATABASE_URL`, `REDIS_URL`,
`ELASTICSEARCH_URL`, `KAFKA_BROKER_URL`, `VAULT_ADDR`, `VAULT_TOKEN`)
are injected into the app container simultaneously.

---

## Cross-service dependencies

When service A needs to connect to service B's dependency (e.g.
Inventory reading from Orders' Redis queue), use an explicit `env`
override in your deployment spec:

```yaml
spec:
  deployment:
    image: registry:5000/inventory:latest
    port: 8082
    env:
      - name: REDIS_URL
        value: "redis://myuser-orders-redis:6379/0"
  dependencies:
    - type: mongodb
```

The `env` block on the deployment spec lets you reference any service's
dependency by its predictable DNS name: `<cr-name>-<dep-type>`.
