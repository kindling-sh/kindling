# Dependency Reference

The kindling operator auto-provisions backing services alongside your
application. You declare dependencies in the `DevStagingEnvironment` CR
and the operator creates a Pod, Service, and credential Secret for each
one, then **injects connection-string environment variables** into your
app container automatically.

This document covers every supported dependency type, the exact
environment variables your code must read, and working code examples in
Go, Python, and Node.js.

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

| Type | Env var injected | Connection URL format | Default port | Default image | Stateful (PVC) |
|---|---|---|---|---|---|
| `postgres` | `DATABASE_URL` | `postgres://devuser:devpass@<name>-postgres:5432/devdb?sslmode=disable` | 5432 | `postgres` | ✅ |
| `redis` | `REDIS_URL` | `redis://<name>-redis:6379/0` | 6379 | `redis` | ❌ |
| `mysql` | `DATABASE_URL` | `mysql://devuser:devpass@<name>-mysql:3306/devdb` | 3306 | `mysql` | ✅ |
| `mongodb` | `MONGO_URL` | `mongodb://devuser:devpass@<name>-mongodb:27017` | 27017 | `mongo` | ✅ |
| `rabbitmq` | `AMQP_URL` | `amqp://devuser:devpass@<name>-rabbitmq:5672/` | 5672 | `rabbitmq` | ❌ |
| `minio` | `S3_ENDPOINT` | `http://<name>-minio:9000` | 9000 | `minio/minio` | ✅ |
| `elasticsearch` | `ELASTICSEARCH_URL` | `http://<name>-elasticsearch:9200` | 9200 | `docker.elastic.co/elasticsearch/elasticsearch` | ✅ |
| `kafka` | `KAFKA_BROKER_URL` | `<name>-kafka:9092` | 9092 | `apache/kafka` | ✅ |
| `nats` | `NATS_URL` | `nats://<name>-nats:4222` | 4222 | `nats` | ❌ |
| `memcached` | `MEMCACHED_URL` | `<name>-memcached:11211` | 11211 | `memcached` | ❌ |
| `cassandra` | `CASSANDRA_URL` | `<name>-cassandra:9042` | 9042 | `cassandra` | ✅ |
| `consul` | `CONSUL_HTTP_ADDR` | `http://<name>-consul:8500` | 8500 | `hashicorp/consul` | ❌ |
| `vault` | `VAULT_ADDR` | `http://<name>-vault:8200` | 8200 | `hashicorp/vault` | ❌ |
| `influxdb` | `INFLUXDB_URL` | `http://devuser:devpass123@<name>-influxdb:8086` | 8086 | `influxdb` | ✅ |
| `jaeger` | `JAEGER_ENDPOINT` | `http://<name>-jaeger:16686` | 16686 | `jaegertracing/all-in-one` | ❌ |

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
```

When you override credential env vars (e.g. `POSTGRES_USER`), the
connection URL injected into your app automatically reflects the new
values.

---

## Detailed dependency specifications

### 1. PostgreSQL

**Type:** `postgres`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-postgres` | 1 replica, image `postgres:<version>` |
| Service | `<name>-postgres` | ClusterIP, port 5432 |
| Secret | `<name>-postgres-credentials` | All credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `DATABASE_URL` | `postgres://devuser:devpass@<name>-postgres:5432/devdb?sslmode=disable` | Full connection string |

**Environment variables set on the Postgres container itself:**

| Env var | Default value | Purpose |
|---|---|---|
| `POSTGRES_USER` | `devuser` | Superuser name |
| `POSTGRES_PASSWORD` | `devpass` | Superuser password |
| `POSTGRES_DB` | `devdb` | Default database |

**How to read `DATABASE_URL` in your code:**

Go:
```go
import (
    "database/sql"
    "os"
    _ "github.com/lib/pq"
)

func connectDB() (*sql.DB, error) {
    dsn := os.Getenv("DATABASE_URL")
    // dsn = "postgres://devuser:devpass@myapp-postgres:5432/devdb?sslmode=disable"
    return sql.Open("postgres", dsn)
}
```

Python:
```python
import os
import psycopg2

DATABASE_URL = os.environ["DATABASE_URL"]
# "postgres://devuser:devpass@myapp-postgres:5432/devdb?sslmode=disable"
conn = psycopg2.connect(DATABASE_URL)
```

Node.js:
```javascript
const { Pool } = require("pg");

const pool = new Pool({
  connectionString: process.env.DATABASE_URL,
  // "postgres://devuser:devpass@myapp-postgres:5432/devdb?sslmode=disable"
});
```

**CR example:**
```yaml
dependencies:
  - type: postgres
    version: "16"
```

---

### 2. Redis

**Type:** `redis`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-redis` | 1 replica, image `redis:<version>` |
| Service | `<name>-redis` | ClusterIP, port 6379 |
| Secret | `<name>-redis-credentials` | Connection URL |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `REDIS_URL` | `redis://<name>-redis:6379/0` | Full connection string |

**No extra environment variables** on the Redis container (runs with defaults).

**How to read `REDIS_URL` in your code:**

Go:
```go
import (
    "os"
    "github.com/redis/go-redis/v9"
)

func connectRedis() *redis.Client {
    opt, _ := redis.ParseURL(os.Getenv("REDIS_URL"))
    // "redis://myapp-redis:6379/0"
    return redis.NewClient(opt)
}
```

Python:
```python
import os
import redis

REDIS_URL = os.environ["REDIS_URL"]
# "redis://myapp-redis:6379/0"
r = redis.from_url(REDIS_URL)
```

Node.js:
```javascript
const { createClient } = require("redis");

const client = createClient({ url: process.env.REDIS_URL });
// "redis://myapp-redis:6379/0"
await client.connect();
```

**CR example:**
```yaml
dependencies:
  - type: redis
```

---

### 3. MySQL

**Type:** `mysql`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-mysql` | 1 replica, image `mysql:<version>` |
| Service | `<name>-mysql` | ClusterIP, port 3306 |
| Secret | `<name>-mysql-credentials` | All credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `DATABASE_URL` | `mysql://devuser:devpass@<name>-mysql:3306/devdb` | Full connection string |

**Environment variables set on the MySQL container:**

| Env var | Default value | Purpose |
|---|---|---|
| `MYSQL_ROOT_PASSWORD` | `devpass` | Root password |
| `MYSQL_DATABASE` | `devdb` | Database created on init |
| `MYSQL_USER` | `devuser` | Non-root user |
| `MYSQL_PASSWORD` | `devpass` | Non-root user password |

**How to read `DATABASE_URL` in your code:**

Go:
```go
import (
    "database/sql"
    "net/url"
    "os"
    _ "github.com/go-sql-driver/mysql"
)

func connectDB() (*sql.DB, error) {
    rawURL := os.Getenv("DATABASE_URL")
    // "mysql://devuser:devpass@myapp-mysql:3306/devdb"
    u, _ := url.Parse(rawURL)
    password, _ := u.User.Password()
    dsn := fmt.Sprintf("%s:%s@tcp(%s)%s", u.User.Username(), password, u.Host, u.Path)
    return sql.Open("mysql", dsn)
}
```

Python:
```python
import os
from urllib.parse import urlparse
import mysql.connector

url = urlparse(os.environ["DATABASE_URL"])
conn = mysql.connector.connect(
    host=url.hostname,
    port=url.port,
    user=url.username,
    password=url.password,
    database=url.path.lstrip("/"),
)
```

Node.js:
```javascript
const mysql = require("mysql2/promise");

// Parse mysql://devuser:devpass@myapp-mysql:3306/devdb
const url = new URL(process.env.DATABASE_URL);
const pool = mysql.createPool({
  host: url.hostname,
  port: url.port,
  user: url.username,
  password: url.password,
  database: url.pathname.slice(1),
});
```

**CR example:**
```yaml
dependencies:
  - type: mysql
    version: "8.0"
```

---

### 4. MongoDB

**Type:** `mongodb`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-mongodb` | 1 replica, image `mongo:<version>` |
| Service | `<name>-mongodb` | ClusterIP, port 27017 |
| Secret | `<name>-mongodb-credentials` | Credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `MONGO_URL` | `mongodb://devuser:devpass@<name>-mongodb:27017` | Full connection string |

**Environment variables set on the MongoDB container:**

| Env var | Default value | Purpose |
|---|---|---|
| `MONGO_INITDB_ROOT_USERNAME` | `devuser` | Root username |
| `MONGO_INITDB_ROOT_PASSWORD` | `devpass` | Root password |

**How to read `MONGO_URL` in your code:**

Go:
```go
import (
    "context"
    "os"
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

func connectMongo() (*mongo.Client, error) {
    uri := os.Getenv("MONGO_URL")
    // "mongodb://devuser:devpass@myapp-mongodb:27017"
    return mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
}
```

Python:
```python
import os
from pymongo import MongoClient

MONGO_URL = os.environ["MONGO_URL"]
# "mongodb://devuser:devpass@myapp-mongodb:27017"
client = MongoClient(MONGO_URL)
db = client["mydb"]
```

Node.js:
```javascript
const { MongoClient } = require("mongodb");

const client = new MongoClient(process.env.MONGO_URL);
// "mongodb://devuser:devpass@myapp-mongodb:27017"
await client.connect();
const db = client.db("mydb");
```

**CR example:**
```yaml
dependencies:
  - type: mongodb
```

---

### 5. RabbitMQ

**Type:** `rabbitmq`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-rabbitmq` | 1 replica, image `rabbitmq:3-management` (default) |
| Service | `<name>-rabbitmq` | ClusterIP, ports 5672 (AMQP) + 15672 (management) |
| Secret | `<name>-rabbitmq-credentials` | Credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `AMQP_URL` | `amqp://devuser:devpass@<name>-rabbitmq:5672/` | AMQP connection string |

**Environment variables set on the RabbitMQ container:**

| Env var | Default value | Purpose |
|---|---|---|
| `RABBITMQ_DEFAULT_USER` | `devuser` | Admin username |
| `RABBITMQ_DEFAULT_PASS` | `devpass` | Admin password |

**Additional ports:** Management UI on port `15672`.

**How to read `AMQP_URL` in your code:**

Go:
```go
import (
    "os"
    amqp "github.com/rabbitmq/amqp091-go"
)

func connectRabbitMQ() (*amqp.Connection, error) {
    return amqp.Dial(os.Getenv("AMQP_URL"))
    // "amqp://devuser:devpass@myapp-rabbitmq:5672/"
}
```

Python:
```python
import os
import pika

params = pika.URLParameters(os.environ["AMQP_URL"])
# "amqp://devuser:devpass@myapp-rabbitmq:5672/"
connection = pika.BlockingConnection(params)
channel = connection.channel()
```

Node.js:
```javascript
const amqp = require("amqplib");

const conn = await amqp.connect(process.env.AMQP_URL);
// "amqp://devuser:devpass@myapp-rabbitmq:5672/"
const channel = await conn.createChannel();
```

**CR example:**
```yaml
dependencies:
  - type: rabbitmq
```

---

### 6. MinIO (S3-compatible object store)

**Type:** `minio`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-minio` | 1 replica, image `minio/minio`, args `server /data` |
| Service | `<name>-minio` | ClusterIP, port 9000 |
| Secret | `<name>-minio-credentials` | Credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `S3_ENDPOINT` | `http://<name>-minio:9000` | S3 API endpoint |
| `S3_ACCESS_KEY` | `minioadmin` | Access key |
| `S3_SECRET_KEY` | `minioadmin` | Secret key |

**Environment variables set on the MinIO container:**

| Env var | Default value | Purpose |
|---|---|---|
| `MINIO_ROOT_USER` | `minioadmin` | Root access key |
| `MINIO_ROOT_PASSWORD` | `minioadmin` | Root secret key |

**How to read `S3_ENDPOINT` + credentials in your code:**

Go:
```go
import (
    "os"
    "github.com/minio/minio-go/v7"
    "github.com/minio/minio-go/v7/pkg/credentials"
)

func connectMinIO() (*minio.Client, error) {
    endpoint := os.Getenv("S3_ENDPOINT") // "http://myapp-minio:9000"
    // Strip the scheme for the minio client
    host := strings.TrimPrefix(endpoint, "http://")
    return minio.New(host, &minio.Options{
        Creds:  credentials.NewStaticV4(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), ""),
        Secure: false,
    })
}
```

Python:
```python
import os
import boto3

s3 = boto3.client(
    "s3",
    endpoint_url=os.environ["S3_ENDPOINT"],      # "http://myapp-minio:9000"
    aws_access_key_id=os.environ["S3_ACCESS_KEY"],
    aws_secret_access_key=os.environ["S3_SECRET_KEY"],
)
```

Node.js:
```javascript
const { S3Client } = require("@aws-sdk/client-s3");

const s3 = new S3Client({
  endpoint: process.env.S3_ENDPOINT,      // "http://myapp-minio:9000"
  credentials: {
    accessKeyId: process.env.S3_ACCESS_KEY,
    secretAccessKey: process.env.S3_SECRET_KEY,
  },
  forcePathStyle: true,
  region: "us-east-1",
});
```

**CR example:**
```yaml
dependencies:
  - type: minio
```

---

### 7. Elasticsearch

**Type:** `elasticsearch`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-elasticsearch` | 1 replica, image `docker.elastic.co/elasticsearch/elasticsearch:8.12.0` |
| Service | `<name>-elasticsearch` | ClusterIP, ports 9200 (HTTP) + 9300 (transport) |
| Secret | `<name>-elasticsearch-credentials` | Config key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `ELASTICSEARCH_URL` | `http://<name>-elasticsearch:9200` | HTTP API endpoint |

**Environment variables set on the Elasticsearch container:**

| Env var | Default value | Purpose |
|---|---|---|
| `discovery.type` | `single-node` | Disable cluster discovery |
| `xpack.security.enabled` | `false` | Disable auth for dev |
| `ES_JAVA_OPTS` | `-Xms256m -Xmx256m` | JVM heap limits |

**How to read `ELASTICSEARCH_URL` in your code:**

Go:
```go
import (
    "os"
    "github.com/elastic/go-elasticsearch/v8"
)

func connectES() (*elasticsearch.Client, error) {
    return elasticsearch.NewClient(elasticsearch.Config{
        Addresses: []string{os.Getenv("ELASTICSEARCH_URL")},
        // "http://myapp-elasticsearch:9200"
    })
}
```

Python:
```python
import os
from elasticsearch import Elasticsearch

ELASTICSEARCH_URL = os.environ["ELASTICSEARCH_URL"]
# "http://myapp-elasticsearch:9200"
es = Elasticsearch([ELASTICSEARCH_URL])
```

Node.js:
```javascript
const { Client } = require("@elastic/elasticsearch");

const client = new Client({
  node: process.env.ELASTICSEARCH_URL,
  // "http://myapp-elasticsearch:9200"
});
```

**CR example:**
```yaml
dependencies:
  - type: elasticsearch
```

---

### 8. Kafka (KRaft mode)

**Type:** `kafka`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-kafka` | 1 replica, `apache/kafka:latest`, KRaft (no ZooKeeper) |
| Service | `<name>-kafka` | ClusterIP, ports 9092 (broker) + 9093 (controller) |
| Secret | `<name>-kafka-credentials` | Config key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `KAFKA_BROKER_URL` | `<name>-kafka:9092` | Broker bootstrap address |

**Environment variables set on the Kafka container:**

| Env var | Default value | Purpose |
|---|---|---|
| `KAFKA_NODE_ID` | `1` | Broker node ID |
| `KAFKA_PROCESS_ROLES` | `broker,controller` | Combined mode |
| `KAFKA_CONTROLLER_QUORUM_VOTERS` | `1@localhost:9093` | KRaft quorum |
| `KAFKA_LISTENERS` | `PLAINTEXT://:9092,CONTROLLER://:9093` | Listener config |
| `KAFKA_LISTENER_SECURITY_PROTOCOL_MAP` | `PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT` | Protocol map |
| `KAFKA_CONTROLLER_LISTENER_NAMES` | `CONTROLLER` | Controller listener |
| `CLUSTER_ID` | `kindling-dev-kafka-cluster` | KRaft cluster ID |

**How to read `KAFKA_BROKER_URL` in your code:**

Go:
```go
import (
    "os"
    "github.com/segmentio/kafka-go"
)

func newKafkaWriter(topic string) *kafka.Writer {
    return &kafka.Writer{
        Addr:  kafka.TCP(os.Getenv("KAFKA_BROKER_URL")),
        // "myapp-kafka:9092"
        Topic: topic,
    }
}
```

Python:
```python
import os
from kafka import KafkaProducer

KAFKA_BROKER_URL = os.environ["KAFKA_BROKER_URL"]
# "myapp-kafka:9092"
producer = KafkaProducer(bootstrap_servers=[KAFKA_BROKER_URL])
```

Node.js:
```javascript
const { Kafka } = require("kafkajs");

const kafka = new Kafka({
  brokers: [process.env.KAFKA_BROKER_URL],
  // ["myapp-kafka:9092"]
});
const producer = kafka.producer();
await producer.connect();
```

**CR example:**
```yaml
dependencies:
  - type: kafka
```

---

### 9. NATS

**Type:** `nats`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-nats` | 1 replica, image `nats:<version>` |
| Service | `<name>-nats` | ClusterIP, port 4222 |
| Secret | `<name>-nats-credentials` | Connection URL |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `NATS_URL` | `nats://<name>-nats:4222` | NATS connection string |

**No extra environment variables** on the NATS container.

**How to read `NATS_URL` in your code:**

Go:
```go
import (
    "os"
    "github.com/nats-io/nats.go"
)

func connectNATS() (*nats.Conn, error) {
    return nats.Connect(os.Getenv("NATS_URL"))
    // "nats://myapp-nats:4222"
}
```

Python:
```python
import os
import nats

async def connect_nats():
    nc = await nats.connect(os.environ["NATS_URL"])
    # "nats://myapp-nats:4222"
    return nc
```

Node.js:
```javascript
const { connect } = require("nats");

const nc = await connect({ servers: process.env.NATS_URL });
// "nats://myapp-nats:4222"
```

**CR example:**
```yaml
dependencies:
  - type: nats
```

---

### 10. Memcached

**Type:** `memcached`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-memcached` | 1 replica, image `memcached:<version>` |
| Service | `<name>-memcached` | ClusterIP, port 11211 |
| Secret | `<name>-memcached-credentials` | Connection URL |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `MEMCACHED_URL` | `<name>-memcached:11211` | host:port address |

**No extra environment variables** on the Memcached container.

**How to read `MEMCACHED_URL` in your code:**

Go:
```go
import (
    "os"
    "github.com/bradfitz/gomemcache/memcache"
)

func connectMemcached() *memcache.Client {
    return memcache.New(os.Getenv("MEMCACHED_URL"))
    // "myapp-memcached:11211"
}
```

Python:
```python
import os
from pymemcache.client.base import Client

host, port = os.environ["MEMCACHED_URL"].split(":")
# "myapp-memcached:11211"
client = Client((host, int(port)))
```

Node.js:
```javascript
const Memcached = require("memcached");

const client = new Memcached(process.env.MEMCACHED_URL);
// "myapp-memcached:11211"
```

**CR example:**
```yaml
dependencies:
  - type: memcached
```

---

### 11. Cassandra

**Type:** `cassandra`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-cassandra` | 1 replica, image `cassandra:<version>` |
| Service | `<name>-cassandra` | ClusterIP, port 9042 |
| Secret | `<name>-cassandra-credentials` | Config key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `CASSANDRA_URL` | `<name>-cassandra:9042` | host:port contact point |

**Environment variables set on the Cassandra container:**

| Env var | Default value | Purpose |
|---|---|---|
| `CASSANDRA_CLUSTER_NAME` | `DevCluster` | Cluster name |
| `CASSANDRA_DC` | `dc1` | Datacenter name |
| `MAX_HEAP_SIZE` | `256M` | JVM max heap |
| `HEAP_NEWSIZE` | `64M` | JVM young gen |

**How to read `CASSANDRA_URL` in your code:**

Go:
```go
import (
    "os"
    "strings"
    "github.com/gocql/gocql"
)

func connectCassandra() (*gocql.Session, error) {
    parts := strings.SplitN(os.Getenv("CASSANDRA_URL"), ":", 2)
    // "myapp-cassandra:9042"
    cluster := gocql.NewCluster(parts[0])
    cluster.Port, _ = strconv.Atoi(parts[1])
    cluster.Keyspace = "system"
    return cluster.CreateSession()
}
```

Python:
```python
import os
from cassandra.cluster import Cluster

host, port = os.environ["CASSANDRA_URL"].split(":")
# "myapp-cassandra:9042"
cluster = Cluster([host], port=int(port))
session = cluster.connect()
```

Node.js:
```javascript
const cassandra = require("cassandra-driver");

const [host, port] = process.env.CASSANDRA_URL.split(":");
// "myapp-cassandra:9042"
const client = new cassandra.Client({
  contactPoints: [host],
  protocolOptions: { port: parseInt(port) },
  localDataCenter: "dc1",
});
await client.connect();
```

**CR example:**
```yaml
dependencies:
  - type: cassandra
```

---

### 12. Consul

**Type:** `consul`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-consul` | 1 replica, `hashicorp/consul`, args `agent -dev -client=0.0.0.0` |
| Service | `<name>-consul` | ClusterIP, port 8500 |
| Secret | `<name>-consul-credentials` | Connection URL |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `CONSUL_HTTP_ADDR` | `http://<name>-consul:8500` | HTTP API address |

**No extra environment variables** on the Consul container.

**How to read `CONSUL_HTTP_ADDR` in your code:**

Go:
```go
import (
    "os"
    consul "github.com/hashicorp/consul/api"
)

func connectConsul() (*consul.Client, error) {
    config := consul.DefaultConfig()
    config.Address = os.Getenv("CONSUL_HTTP_ADDR")
    // "http://myapp-consul:8500"
    return consul.NewClient(config)
}
```

Python:
```python
import os
import consul

CONSUL_HTTP_ADDR = os.environ["CONSUL_HTTP_ADDR"]
# "http://myapp-consul:8500"
c = consul.Consul(host=CONSUL_HTTP_ADDR.split("//")[1].split(":")[0], port=8500)
```

Node.js:
```javascript
const Consul = require("consul");

const url = new URL(process.env.CONSUL_HTTP_ADDR);
// "http://myapp-consul:8500"
const client = new Consul({ host: url.hostname, port: url.port });
```

> **Note:** Many Consul client libraries also read the `CONSUL_HTTP_ADDR`
> env var natively — you may not need to parse it at all.

**CR example:**
```yaml
dependencies:
  - type: consul
```

---

### 13. Vault

**Type:** `vault`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-vault` | 1 replica, `hashicorp/vault`, args `server -dev` |
| Service | `<name>-vault` | ClusterIP, port 8200 |
| Secret | `<name>-vault-credentials` | Credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `VAULT_ADDR` | `http://<name>-vault:8200` | Vault API address |
| `VAULT_TOKEN` | `dev-root-token` | Dev mode root token |

**Environment variables set on the Vault container:**

| Env var | Default value | Purpose |
|---|---|---|
| `VAULT_DEV_ROOT_TOKEN_ID` | `dev-root-token` | Root token for dev mode |
| `VAULT_DEV_LISTEN_ADDRESS` | `0.0.0.0:8200` | Listen address |

**How to read `VAULT_ADDR` + `VAULT_TOKEN` in your code:**

Go:
```go
import (
    "os"
    vault "github.com/hashicorp/vault/api"
)

func connectVault() (*vault.Client, error) {
    config := vault.DefaultConfig()
    config.Address = os.Getenv("VAULT_ADDR")
    // "http://myapp-vault:8200"
    client, err := vault.NewClient(config)
    if err != nil {
        return nil, err
    }
    client.SetToken(os.Getenv("VAULT_TOKEN"))
    // "dev-root-token"
    return client, nil
}
```

Python:
```python
import os
import hvac

client = hvac.Client(
    url=os.environ["VAULT_ADDR"],      # "http://myapp-vault:8200"
    token=os.environ["VAULT_TOKEN"],    # "dev-root-token"
)
```

Node.js:
```javascript
const vault = require("node-vault")({
  apiVersion: "v1",
  endpoint: process.env.VAULT_ADDR,   // "http://myapp-vault:8200"
  token: process.env.VAULT_TOKEN,     // "dev-root-token"
});
```

> **Note:** Most Vault client libraries natively read `VAULT_ADDR` and
> `VAULT_TOKEN` from the environment — explicit configuration is often unnecessary.

**CR example:**
```yaml
dependencies:
  - type: vault
```

---

### 14. InfluxDB

**Type:** `influxdb`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-influxdb` | 1 replica, image `influxdb:<version>` |
| Service | `<name>-influxdb` | ClusterIP, port 8086 |
| Secret | `<name>-influxdb-credentials` | All credential key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `INFLUXDB_URL` | `http://devuser:devpass123@<name>-influxdb:8086` | API endpoint with auth |
| `INFLUXDB_ORG` | `devorg` | Default organization |
| `INFLUXDB_BUCKET` | `devbucket` | Default bucket |

**Environment variables set on the InfluxDB container:**

| Env var | Default value | Purpose |
|---|---|---|
| `DOCKER_INFLUXDB_INIT_MODE` | `setup` | Auto-setup on first run |
| `DOCKER_INFLUXDB_INIT_USERNAME` | `devuser` | Admin username |
| `DOCKER_INFLUXDB_INIT_PASSWORD` | `devpass123` | Admin password |
| `DOCKER_INFLUXDB_INIT_ORG` | `devorg` | Default org |
| `DOCKER_INFLUXDB_INIT_BUCKET` | `devbucket` | Default bucket |

**How to read `INFLUXDB_URL` + metadata in your code:**

Go:
```go
import (
    "os"
    influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

func connectInfluxDB() influxdb2.Client {
    // Parse auth from URL or use a token-based approach
    url := os.Getenv("INFLUXDB_URL")  // "http://devuser:devpass123@myapp-influxdb:8086"
    org := os.Getenv("INFLUXDB_ORG")  // "devorg"
    bucket := os.Getenv("INFLUXDB_BUCKET") // "devbucket"

    // For InfluxDB 2.x, you typically use a token. In dev mode, use
    // the URL directly for the HTTP client:
    client := influxdb2.NewClient("http://myapp-influxdb:8086", "")
    return client
}
```

Python:
```python
import os
from influxdb_client import InfluxDBClient

INFLUXDB_URL = os.environ["INFLUXDB_URL"]   # includes auth
INFLUXDB_ORG = os.environ["INFLUXDB_ORG"]   # "devorg"
INFLUXDB_BUCKET = os.environ["INFLUXDB_BUCKET"]  # "devbucket"

# Strip auth from URL for the client
from urllib.parse import urlparse
parsed = urlparse(INFLUXDB_URL)
base_url = f"{parsed.scheme}://{parsed.hostname}:{parsed.port}"

client = InfluxDBClient(url=base_url, org=INFLUXDB_ORG)
```

Node.js:
```javascript
const { InfluxDB } = require("@influxdata/influxdb-client");

const url = new URL(process.env.INFLUXDB_URL);
const baseUrl = `${url.protocol}//${url.hostname}:${url.port}`;
const org = process.env.INFLUXDB_ORG;    // "devorg"
const bucket = process.env.INFLUXDB_BUCKET; // "devbucket"

const client = new InfluxDB({ url: baseUrl });
```

**CR example:**
```yaml
dependencies:
  - type: influxdb
```

---

### 15. Jaeger

**Type:** `jaeger`

**What the operator creates:**

| Resource | Name | Details |
|---|---|---|
| Deployment | `<name>-jaeger` | 1 replica, `jaegertracing/all-in-one:latest` |
| Service | `<name>-jaeger` | ClusterIP, ports 16686 (UI) + 4317 (OTLP gRPC) + 4318 (OTLP HTTP) |
| Secret | `<name>-jaeger-credentials` | Config key/value pairs |

**Environment variables injected into your app container:**

| Env var | Value | Description |
|---|---|---|
| `JAEGER_ENDPOINT` | `http://<name>-jaeger:16686` | Jaeger UI / query API |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://<name>-jaeger:4317` | OpenTelemetry collector (gRPC) |

**Environment variables set on the Jaeger container:**

| Env var | Default value | Purpose |
|---|---|---|
| `COLLECTOR_OTLP_ENABLED` | `true` | Enable OTLP collector |

**How to read the tracing endpoints in your code:**

Go:
```go
import (
    "context"
    "os"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/trace"
)

func initTracer() (*trace.TracerProvider, error) {
    endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
    // "http://myapp-jaeger:4317"
    exporter, err := otlptracegrpc.New(context.Background(),
        otlptracegrpc.WithEndpoint(endpoint),
        otlptracegrpc.WithInsecure(),
    )
    if err != nil {
        return nil, err
    }
    tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
    otel.SetTracerProvider(tp)
    return tp, nil
}
```

Python:
```python
import os
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

endpoint = os.environ["OTEL_EXPORTER_OTLP_ENDPOINT"]
# "http://myapp-jaeger:4317"
exporter = OTLPSpanExporter(endpoint=endpoint, insecure=True)
provider = TracerProvider()
provider.add_span_processor(BatchSpanProcessor(exporter))
trace.set_tracer_provider(provider)
```

Node.js:
```javascript
const { NodeTracerProvider } = require("@opentelemetry/sdk-trace-node");
const { OTLPTraceExporter } = require("@opentelemetry/exporter-trace-otlp-grpc");
const { BatchSpanProcessor } = require("@opentelemetry/sdk-trace-base");

const exporter = new OTLPTraceExporter({
  url: process.env.OTEL_EXPORTER_OTLP_ENDPOINT,
  // "http://myapp-jaeger:4317"
});
const provider = new NodeTracerProvider();
provider.addSpanProcessor(new BatchSpanProcessor(exporter));
provider.register();
```

> **Note:** Many OpenTelemetry SDKs automatically read
> `OTEL_EXPORTER_OTLP_ENDPOINT` from the environment, so you may not
> need to configure it explicitly.

**CR example:**
```yaml
dependencies:
  - type: jaeger
```

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
# Inventory service
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
