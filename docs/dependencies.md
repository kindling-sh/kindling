# Dependencies — Internal Reference

This document describes all 15 supported dependency types, the
auto-injection system, connection URL construction, init container
readiness probes, and the operator's dependency registry.

Source: `internal/controller/devstagingenvironment_controller.go`,
`api/v1alpha1/devstagingenvironment_types.go`

---

## Dependency lifecycle

When a dependency is declared in a DSE spec:

```yaml
spec:
  dependencies:
    - name: db
      type: postgres
```

The operator:

1. **Creates a credential Secret** — contains default user/password
2. **Creates a Deployment** — runs the dependency container with
   credentials mounted as env vars
3. **Creates a Service** — exposes the dependency on its default port
4. **Injects env vars** — adds the connection URL to the app container
5. **Adds init container** — busybox TCP probe blocks app until dep is ready

---

## Supported types (15)

### postgres

| Field | Value |
|---|---|
| Default image | `postgres:15` |
| Default port | 5432 |
| Auto-injected env var | `DATABASE_URL` |
| Connection URL format | `postgresql://user:password@<svc>:5432/devdb?sslmode=disable` |
| Container env vars | `POSTGRES_USER=user`, `POSTGRES_PASSWORD=password`, `POSTGRES_DB=devdb` |
| Notes | Most common dependency. PVC optional via `storage` field. |

### redis

| Field | Value |
|---|---|
| Default image | `redis:7` |
| Default port | 6379 |
| Auto-injected env var | `REDIS_URL` |
| Connection URL format | `redis://<svc>:6379/0` |
| Container env vars | None (no auth by default) |
| Notes | No password by default. Set via `config: {"requirepass": "..."}`. |

### mysql

| Field | Value |
|---|---|
| Default image | `mysql:8` |
| Default port | 3306 |
| Auto-injected env var | `DATABASE_URL` |
| Connection URL format | `mysql://user:password@tcp(<svc>:3306)/devdb` |
| Container env vars | `MYSQL_ROOT_PASSWORD=password`, `MYSQL_DATABASE=devdb`, `MYSQL_USER=user`, `MYSQL_PASSWORD=password` |
| Notes | Uses TCP URL format for Go compatibility. |

### mongodb

| Field | Value |
|---|---|
| Default image | `mongo:7` |
| Default port | 27017 |
| Auto-injected env var | `MONGO_URL` |
| Connection URL format | `mongodb://user:password@<svc>:27017/devdb` |
| Container env vars | `MONGO_INITDB_ROOT_USERNAME=user`, `MONGO_INITDB_ROOT_PASSWORD=password`, `MONGO_INITDB_DATABASE=devdb` |

### rabbitmq

| Field | Value |
|---|---|
| Default image | `rabbitmq:3-management` |
| Default port | 5672 |
| Auto-injected env var | `AMQP_URL` |
| Connection URL format | `amqp://user:password@<svc>:5672/` |
| Container env vars | `RABBITMQ_DEFAULT_USER=user`, `RABBITMQ_DEFAULT_PASS=password` |
| Notes | Management UI on port 15672 (not exposed by default). |

### minio

| Field | Value |
|---|---|
| Default image | `minio/minio:latest` |
| Default port | 9000 |
| Auto-injected env var | `S3_ENDPOINT` |
| Connection URL format | `http://<svc>:9000` |
| Container env vars | `MINIO_ROOT_USER=minioadmin`, `MINIO_ROOT_PASSWORD=minioadmin` |
| Container args | `["server", "/data"]` |
| Notes | S3-compatible object storage. Console on port 9001. |

### elasticsearch

| Field | Value |
|---|---|
| Default image | `elasticsearch:8.12.0` |
| Default port | 9200 |
| Auto-injected env var | `ELASTICSEARCH_URL` |
| Connection URL format | `http://<svc>:9200` |
| Container env vars | `discovery.type=single-node`, `xpack.security.enabled=false` |
| Notes | Security disabled for dev. Memory-hungry — consider resource limits. |

### kafka

| Field | Value |
|---|---|
| Default image | `bitnami/kafka:latest` |
| Default port | 9092 |
| Auto-injected env var | `KAFKA_BROKER_URL` |
| Connection URL format | `<svc>:9092` |
| Container env vars | `KAFKA_CFG_NODE_ID=0`, `KAFKA_CFG_PROCESS_ROLES=controller,broker`, `KAFKA_CFG_LISTENERS=...` |
| Notes | KRaft mode (no ZooKeeper). Bitnami image handles single-node setup. |

### nats

| Field | Value |
|---|---|
| Default image | `nats:latest` |
| Default port | 4222 |
| Auto-injected env var | `NATS_URL` |
| Connection URL format | `nats://<svc>:4222` |
| Notes | Lightweight. Monitoring on port 8222. |

### memcached

| Field | Value |
|---|---|
| Default image | `memcached:latest` |
| Default port | 11211 |
| Auto-injected env var | `MEMCACHED_URL` |
| Connection URL format | `<svc>:11211` |
| Notes | No authentication, no persistence. Pure cache. |

### dynamodb

| Field | Value |
|---|---|
| Default image | `amazon/dynamodb-local:latest` |
| Default port | 8000 |
| Auto-injected env var | `DYNAMODB_ENDPOINT` |
| Connection URL format | `http://<svc>:8000` |
| Notes | Local DynamoDB emulator. Needs AWS SDK configured to use custom endpoint. |

### cassandra

| Field | Value |
|---|---|
| Default image | `cassandra:latest` |
| Default port | 9042 |
| Auto-injected env var | `CASSANDRA_URL` |
| Connection URL format | `<svc>:9042` |
| Notes | Slow startup (~30s). Init container timeout may need increasing. |

### etcd

| Field | Value |
|---|---|
| Default image | `bitnami/etcd:latest` |
| Default port | 2379 |
| Auto-injected env var | `ETCD_URL` |
| Connection URL format | `http://<svc>:2379` |
| Container env vars | `ALLOW_NONE_AUTHENTICATION=yes` |

### consul

| Field | Value |
|---|---|
| Default image | `consul:latest` |
| Default port | 8500 |
| Auto-injected env var | `CONSUL_URL` |
| Connection URL format | `http://<svc>:8500` |
| Container args | `["agent", "-dev", "-client=0.0.0.0"]` |

### vault

| Field | Value |
|---|---|
| Default image | `vault:latest` |
| Default port | 8200 |
| Auto-injected env var | `VAULT_ADDR` |
| Connection URL format | `http://<svc>:8200` |
| Container env vars | `VAULT_DEV_ROOT_TOKEN_ID=devtoken` |
| Container args | `["server", "-dev"]` |
| Notes | Dev mode — no persistent storage, root token is `devtoken`. |

---

## Auto-injection rules

### Environment variable injection

When a dependency is declared, the operator injects its connection URL
as an environment variable into every service container in the DSE:

```go
for _, dep := range dse.Spec.Dependencies {
    defaults := dependencyDefaults[dep.Type]
    envVarName := dep.EnvVarName  // use custom if specified
    if envVarName == "" {
        envVarName = defaults.EnvVar  // otherwise use default
    }
    url := buildConnectionURL(dep.Name, dep.Type, svcName(dse, dep))
    container.Env = append(container.Env, corev1.EnvVar{
        Name:  envVarName,
        Value: url,
    })
}
```

**Critical rule:** Never duplicate auto-injected env vars in the
`spec.env[]` block. The operator handles injection automatically.
Duplicating causes confusion about which value takes precedence.

### Service name construction

The K8s Service for a dependency is named:

```
<dse-name>-<dep-name>
```

Example: DSE `my-app` with dependency `db` → Service `my-app-db`.

This name appears in the connection URL as the hostname.

### Custom overrides

Every default can be overridden in the dependency spec:

```yaml
dependencies:
  - name: db
    type: postgres
    image: postgres:16          # override default postgres:15
    port: 5433                  # override default 5432
    envVarName: PG_URL          # override default DATABASE_URL
    env:
      - name: POSTGRES_DB
        value: mydb             # override default devdb
    version: "16"               # alternative to full image override
    storage: 5Gi                # add PVC
    config:
      max_connections: "200"    # type-specific config
```

---

## Init container readiness

For each dependency, the operator adds a busybox init container to
the application Deployment:

```yaml
initContainers:
  - name: wait-for-db
    image: busybox:1.36
    command:
      - sh
      - -c
      - |
        echo "Waiting for my-app-db:5432..."
        until nc -z my-app-db 5432; do
          echo "Still waiting..."
          sleep 2
        done
        echo "my-app-db is ready!"
```

### Why init containers over readiness probes?

- **Ordering guarantee** — init containers run sequentially before
  app containers start. The app never sees "connection refused".
- **Simple TCP check** — no need for protocol-specific health checks.
  If the port accepts connections, the dependency is ready.
- **Visible in logs** — init container logs show exactly what's being
  waited on and how long it took.
- **No application changes** — the app doesn't need retry logic for
  initial connection.

### Timeout behavior

Init containers have no built-in timeout — they'll wait indefinitely.
If a dependency pod is crashlooping, the app init container will keep
retrying. This is intentional: the operator's status will show
`dependenciesReady: false`, and the user can investigate via
`kindling status` or `kindling logs`.

---

## Orphan cleanup

When a dependency is removed from the DSE spec, the operator prunes
the orphaned resources. See [Orphan pruning](operator-internals.md#orphan-pruning)
in the operator internals doc.

---

## Credential management

Each dependency gets a credential Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-app-db-credentials
  labels:
    app.kubernetes.io/part-of: my-app
    app.kubernetes.io/component: db
    app.kubernetes.io/managed-by: kindling
type: Opaque
data:
  username: dXNlcg==      # "user"
  password: cGFzc3dvcmQ=  # "password"
```

The Secret is:
- Created alongside the dependency Deployment
- Referenced in the dependency container's env vars
- Owned by the DSE CR (garbage collected on deletion)
- NOT the same as user-managed secrets (`kindling secrets set`)

User-managed secrets (API keys, OAuth tokens) are separate and
referenced via `secretKeyRef` in the CI workflow or DSE spec.
