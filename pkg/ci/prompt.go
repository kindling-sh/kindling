// Package ci — shared prompt building blocks for AI-assisted workflow generation.
//
// Every CI provider (GitHub Actions, GitLab CI) uses the same
// Kaniko-based build mechanism, dependency injection, and deploy philosophy.
// This file holds the platform-agnostic knowledge as const strings that each
// provider's SystemPrompt() method assembles together with its CI-specific
// syntax instructions.
package ci

// PromptDockerfileExistence is the shared rule about Dockerfile existence
// and self-containment that applies regardless of CI platform.
const PromptDockerfileExistence = `CRITICAL — Dockerfile existence and self-containment:
  a) Do NOT generate a build step for a service unless you can see a real
     Dockerfile for it in the repository structure. If a subdirectory has source code
     but no Dockerfile, skip it — do not assume one will appear at build time.
  b) The Dockerfile must be SELF-CONTAINED: it must build successfully from a fresh
     git clone with just "docker build". If the Dockerfile COPYs from directories
     that are build artifacts (e.g. dist/, build/, out/, target/), check whether the
     repo uses a build orchestrator like Nx, Turborepo, Bazel, or Lerna that requires
     a pre-build step (e.g. "npx nx docker-build"). If so, the Dockerfile is NOT
     self-contained — skip that service and add a comment explaining why:
       # SKIPPED: <service> requires 'npx nx docker-build' pre-step (not self-contained)
  c) Multi-stage Dockerfiles that run the build INSIDE the Dockerfile (e.g.
     FROM node:20 AS builder / RUN npm run build / FROM node:20-slim / COPY --from=builder)
     ARE self-contained and should be included.`

// PromptDeployInputs is the shared description of the kindling-deploy inputs
// that applies regardless of how the CI platform invokes them.
const PromptDeployInputs = `kindling-deploy inputs:
  name (required) — DSE metadata.name (typically <actor>-<service>)
  image (required) — Container image reference
  port (required) — Container port
  labels — Extra labels as YAML block
  env — Extra env vars as YAML block (Kubernetes []EnvVar list format)
  dependencies — Dependencies as YAML block
  ingress-host — Ingress hostname
  ingress-class — Ingress class name (default: nginx)
  health-check-path — HTTP health check path (default: /healthz)
  health-check-type — http (default), grpc, or none
  replicas — Number of replicas (default: 1)
  service-type — ClusterIP, NodePort, LoadBalancer (default: ClusterIP)
  wait — Wait for deployment rollout (default: true)

kindling-deploy field ordering (follow this order exactly):
  name, image, port, ingress-host, health-check-path, health-check-type, labels, env, dependencies,
  replicas, service-type, ingress-class, wait`

// PromptBuildInputs is the shared description of the kindling-build inputs.
const PromptBuildInputs = `kindling-build inputs:
  name (required) — Unique build name (used for signal files: /builds/<name>.*)
  context (required) — Path to the build context directory
  image (required) — Full image reference (registry/name:tag)
  exclude — tar --exclude patterns (space-separated, e.g. './ui ./.git')
  dockerfile — Path to Dockerfile relative to context (default: Dockerfile)
  timeout — Max seconds to wait for the build to complete (default: 300)`

// PromptHealthChecks is shared guidance on health check configuration.
const PromptHealthChecks = `Health check guidance:
- Include health-check-path when you can detect the endpoint from source code
- For Java/Spring Boot services, use health-check-path: "/actuator/health"
- health-check-type can be "http" (default), "grpc", or "none":
  • Use health-check-type: "grpc" for services that use gRPC (detect via .proto files,
    grpc imports, gRPC health check registration, protobuf code generation, or ports
    like 50051/9555/3550 that are conventionally gRPC). When type is grpc, omit health-check-path.
  • Use health-check-type: "none" for services with no health endpoint (e.g. load generators,
    batch jobs, or workers that don't expose an HTTP or gRPC health endpoint).
  • For HTTP services (Express, Flask, FastAPI, Gin, etc.), use the default "http" type.
  • gRPC indicators: imports of "google.golang.org/grpc", "grpc" (Python), "@grpc/grpc-js" (Node),
    "io.grpc" (Java), .proto files, protobuf codegen files (*_pb2.py, *.pb.go, *_grpc.pb.go),
    or proto/ directories in the repo.`

// PromptDependencyDetection is the shared rules for detecting backing services
// from source code and dependency manifests.
const PromptDependencyDetection = `Supported dependency types for the "dependencies" input (YAML list under the input):
  postgres, redis, mysql, mongodb, rabbitmq, minio, elasticsearch,
  kafka, nats, memcached, cassandra, consul, vault, influxdb, jaeger

Detect which dependencies to include by analyzing imports, packages, and env var
references across ALL common languages:

- Go:       "github.com/lib/pq"/"database/sql" → postgres, "github.com/go-redis" → redis,
            "go.mongodb.org/mongo-driver" → mongodb, "github.com/streadway/amqp" → rabbitmq,
            "github.com/segmentio/kafka-go" → kafka, "github.com/nats-io/nats.go" → nats,
            "github.com/minio/minio-go" → minio, "github.com/elastic/go-elasticsearch" → elasticsearch,
            "github.com/hashicorp/vault" → vault, "github.com/hashicorp/consul" → consul
- Node/TS:  "pg"/"pg-promise" → postgres, "ioredis"/"redis" → redis, "mysql2" → mysql,
            "mongoose"/"mongodb" → mongodb, "amqplib" → rabbitmq, "kafkajs" → kafka,
            "nats" → nats, "memcached"/"memjs" → memcached, "@elastic/elasticsearch" → elasticsearch,
            "minio" → minio, "cassandra-driver" → cassandra
- Python:   "psycopg2"/"asyncpg"/"sqlalchemy" → postgres, "redis"/"aioredis" → redis,
            "pymysql"/"mysqlclient" → mysql, "pymongo"/"motor" → mongodb,
            "pika"/"aio-pika" → rabbitmq, "kafka-python"/"confluent-kafka" → kafka,
            "nats-py" → nats, "pymemcache" → memcached, "elasticsearch" → elasticsearch,
            "boto3"/"minio" → minio, "cassandra-driver" → cassandra, "hvac" → vault
- Java/Kotlin: "org.postgresql" → postgres, "jedis"/"lettuce" → redis, "mysql-connector" → mysql,
            "mongo-java-driver" → mongodb, "spring-boot-starter-amqp" → rabbitmq,
            "spring-kafka" → kafka, "spring-data-elasticsearch" → elasticsearch,
            "spring-cloud-vault" → vault, "spring-cloud-consul" → consul
- Rust:     "tokio-postgres"/"diesel" → postgres, "redis" → redis, "sqlx" + mysql feature → mysql,
            "mongodb" → mongodb, "lapin" → rabbitmq, "rdkafka" → kafka
- Ruby:     "pg" gem → postgres, "redis" gem → redis, "mysql2" gem → mysql,
            "mongo"/"mongoid" → mongodb, "bunny" → rabbitmq, "sidekiq" → redis
- PHP:      "predis"/"phpredis" → redis, "doctrine/dbal" → postgres or mysql,
            "php-amqplib" → rabbitmq, "mongodb/mongodb" → mongodb
- C#/.NET:  "Npgsql" → postgres, "StackExchange.Redis" → redis,
            "MySqlConnector" → mysql, "MongoDB.Driver" → mongodb,
            "RabbitMQ.Client" → rabbitmq, "Confluent.Kafka" → kafka,
            "NATS.Client" → nats, "Elasticsearch.Net" → elasticsearch
- Elixir:   "postgrex"/"ecto" → postgres, "redix" → redis, "amqp" → rabbitmq,
            "kafka_ex" → kafka, "mongodb_driver" → mongodb
- docker-compose.yml service names (postgres, redis, mysql, mongo, rabbitmq, etc.)
- Environment variable references in code (DATABASE_URL, REDIS_URL, MONGO_URL, etc.)

CRITICAL — Cloud-managed database SDKs do NOT map to local dependencies:
Libraries for cloud-managed databases (e.g. Google AlloyDB, Cloud SQL, AWS RDS,
DynamoDB, Azure Cosmos DB) connect to REMOTE cloud services, not local containers.
Do NOT add a local dependency just because you see a cloud-database SDK.
Only add a local dependency when the service genuinely connects to a local database
instance (e.g. via DATABASE_URL, localhost, or docker-compose-style service names).
Examples of SDKs that should NOT trigger local dependencies:
  - google-cloud-alloydb, cloud-sql-python-connector → NOT local postgres
  - boto3.dynamodb, @aws-sdk/client-dynamodb → NOT local mongodb
  - @azure/cosmos → NOT local mongodb
  - langchain-postgres + alloydb → NOT local postgres (uses AlloyDB connector)`

// PromptDependencyAutoInjection is the shared rules about auto-injected
// connection URLs from declared dependencies.
const PromptDependencyAutoInjection = `CRITICAL — Dependency connection URLs are auto-injected:
When you declare a dependency in the "dependencies" input, the kindling operator
AUTOMATICALLY injects the corresponding connection URL environment variable into the
application container. You MUST NOT include these env vars in the "env" input — they
will be duplicated, and any secretKeyRef will fail because no such secret exists.

Auto-injected env vars by dependency type:
  postgres       → DATABASE_URL  (e.g. postgres://devuser:devpass@<name>-postgres:5432/devdb?sslmode=disable)
  redis          → REDIS_URL     (e.g. redis://<name>-redis:6379/0)
  mysql          → DATABASE_URL  (e.g. mysql://devuser:devpass@<name>-mysql:3306/devdb)
  mongodb        → MONGO_URL     (e.g. mongodb://devuser:devpass@<name>-mongodb:27017)
  rabbitmq       → AMQP_URL      (e.g. amqp://devuser:devpass@<name>-rabbitmq:5672)
  minio          → S3_ENDPOINT   (e.g. http://<name>-minio:9000)
  elasticsearch  → ELASTICSEARCH_URL (e.g. http://<name>-elasticsearch:9200)
  kafka          → KAFKA_BROKER_URL  (e.g. <name>-kafka:9092)
  nats           → NATS_URL      (e.g. nats://<name>-nats:4222)
  memcached      → MEMCACHED_URL (e.g. <name>-memcached:11211)
  cassandra      → CASSANDRA_URL (e.g. <name>-cassandra:9042)
  consul         → CONSUL_HTTP_ADDR (e.g. http://<name>-consul:8500)
  vault          → VAULT_ADDR    (e.g. http://<name>-vault:8200)
  influxdb       → INFLUXDB_URL  (e.g. http://<name>-influxdb:8086)
  jaeger         → JAEGER_ENDPOINT (e.g. http://<name>-jaeger:16686)

So if you write "dependencies: postgres, redis", do NOT also write:
  env: |
    - name: DATABASE_URL
      valueFrom:
        secretKeyRef: ...     ← WRONG, will fail
    - name: REDIS_URL
      value: "redis://..."    ← WRONG, duplicates auto-injected value

The ONLY env vars that belong in the "env" input are:
  1. Truly external credentials (API keys, tokens, third-party DSNs) as secretKeyRef
  2. App configuration that is NOT a dependency connection URL (e.g. NODE_ENV, LOG_LEVEL)
  3. Env vars that reference an auto-injected URL via variable expansion, e.g.:
       - name: ADDITIONAL_DB
         value: "$(DATABASE_URL)&options=extra"
  4. When the app uses a DIFFERENT env var name than the auto-injected one, map it
     using variable expansion. For example, if the app expects CELERY_BROKER_URL but
     the dependency is rabbitmq (which auto-injects AMQP_URL):
       - name: CELERY_BROKER_URL
         value: "$(AMQP_URL)"
     Similarly for CELERY_RESULT_BACKEND when redis auto-injects REDIS_URL:
       - name: CELERY_RESULT_BACKEND
         value: "$(REDIS_URL)"
     Check the app's source code, docker-compose environment, and .env files to
     discover what env var names the app actually uses for each dependency connection.
     If the app's name differs from the auto-injected name, add the mapping.

  IMPORTANT: If an env var uses $(VARIABLE) expansion referencing an auto-injected
  URL, the corresponding dependency MUST be declared in that service's dependencies
  block. The variable will not exist unless the dependency is declared.`

// PromptBuildTimeout is shared guidance on build timeout configuration.
const PromptBuildTimeout = `Build timeout guidance:
The default kindling-build timeout is 300 seconds (5 minutes). This is sufficient for
interpreted languages and lightweight compiled languages (Go, TypeScript, Python, Ruby,
PHP). However, languages with heavy compilation toolchains need a longer timeout.
Set timeout: "900" (15 minutes) on the build step for services written in:
  - Rust (cargo builds from scratch without a cache layer)
  - Java/Kotlin (Maven/Gradle dependency download + compilation)
  - C#/.NET (dotnet restore + publish)
  - Elixir (Mix deps.get + compilation of dependencies)
Only add the timeout input when it differs from the default 300.`

// PromptKanakoPatching is the shared guidance on Kaniko vs Docker BuildKit
// compatibility and required Dockerfile patches. Uses HOSTARCH as a
// placeholder for the concrete architecture (replaced at runtime).
const PromptKanakoPatching = `CRITICAL — Kaniko vs Docker BuildKit compatibility:
kindling-build uses Kaniko, NOT Docker BuildKit. Kaniko does NOT support:
  - Automatic BuildKit platform ARGs: BUILDPLATFORM, TARGETPLATFORM, TARGETARCH, TARGETOS, TARGETVARIANT
  - FROM --platform=${BUILDPLATFORM} syntax
If a Dockerfile uses any of these, the build WILL fail because the ARG values will be empty.

IMPORTANT: Only patch what is specifically broken. Do NOT touch ARG lines that are NOT
BuildKit platform ARGs. Application-level ARGs (ARG APP_PATH, ARG BASE_IMAGE, etc.)
are perfectly valid in Kaniko and MUST be left alone.

When you detect a Dockerfile that uses BuildKit platform ARGs, you MUST generate a
"Patch Dockerfile for Kaniko" step BEFORE the build step for that service.
The patch step should:
  1. Remove "--platform=${BUILDPLATFORM}" from any FROM line
  2. Remove the ARG TARGETPLATFORM, ARG TARGETARCH, ARG BUILDPLATFORM, ARG TARGETOS declarations
  3. Replace any usage of $TARGETARCH or ${TARGETARCH} with the concrete architecture: HOSTARCH
  4. Replace any usage of $TARGETPLATFORM or ${TARGETPLATFORM} with linux/HOSTARCH
  5. Replace any usage of $BUILDPLATFORM or ${BUILDPLATFORM} with linux/HOSTARCH
  6. Replace any usage of $TARGETOS or ${TARGETOS} with linux
All 6 replacement steps are REQUIRED. Do NOT omit step 6 ($TARGETOS → linux).

Additional Kaniko compatibility issues that require Dockerfile patching:

Go VCS stamping:
Kaniko does NOT have a .git directory. Go 1.18+ embeds VCS info by default, which
causes "error obtaining VCS status: exit status 128" and fails the build.
When a Go Dockerfile contains "go build" WITHOUT "-buildvcs=false", you MUST add a
patch step to inject it.

Poetry install without --no-root:
When ANY Dockerfile uses "poetry install" WITHOUT "--no-root", Poetry tries to
install the current project as a package — this fails if README.md or other metadata
files are missing from the build context. You MUST ALWAYS patch "poetry install" to
"poetry install --no-root" in EVERY Dockerfile that uses poetry.

RUN --mount=type=cache:
Kaniko ignores --mount=type=cache flags (they're BuildKit-only cache mounts).
The build will still work but without caching. No patching is needed — safe to leave as-is.

npm cache permissions:
Kaniko's filesystem snapshotting changes ownership of /root/.npm between layers,
causing "EACCES: permission denied" errors on npm install, npm run build, npm ci, etc.
When ANY Dockerfile uses npm, you MUST patch it to redirect the npm cache to /tmp/.npm
by inserting an ENV line after the FROM line.

ONLY generate a Kaniko patch step when the Dockerfile has one or more of these specific issues:
  1. BuildKit platform ARGs (TARGETARCH, BUILDPLATFORM, etc.)
  2. "poetry install" without "--no-root"
  3. npm usage (needs npm_config_cache redirect)
  4. "go build" without "-buildvcs=false"
If NONE of these apply, do NOT generate a patch step — the Dockerfile works as-is.

Common Dockerfile pitfalls to be aware of when reviewing repo structure:
  - Go: if go.sum is missing, the Dockerfile must run "go mod tidy" before "go build"
  - Node/TS: if package-lock.json is missing, "npm ci" will fail — use "npm install" instead
  - Rust: use "rust:1-alpine" (latest stable) to avoid MSRV breakage
  - PHP: composer.json "name" field must use vendor/package format or composer install fails
  - Elixir: use "elixir:1.16-alpine" or newer; runtime image needs libstdc++, openssl, ncurses-libs
These are Dockerfile concerns, not workflow concerns — but if you detect these languages,
be aware that build failures are often caused by these issues.`

// PromptDockerCompose is the shared guidance on using docker-compose.yml as
// the source of truth for multi-service repos.
const PromptDockerCompose = `CRITICAL — docker-compose.yml is the source of truth for multi-service repos:
When a docker-compose.yml exists, you MUST use it to determine the following for
EVERY service (not just the main one):

  a) Build context and Dockerfile path:
     Check the "build" section for each service. Use "context" as the build
     context and "dockerfile" (relative to context) as the dockerfile input.
     If context is "." (repo root), use the workspace root as context and
     set the dockerfile input to the path of the Dockerfile relative to the root.

  b) Dependencies (depends_on):
     Map each docker-compose depends_on entry to the corresponding kindling
     dependency type. Apply this to EVERY service, not just the main one.

  c) Environment variables:
     Check the "environment" section for EVERY service. If the app uses different
     env var names than the auto-injected ones (e.g. CELERY_BROKER_URL instead of
     AMQP_URL), add env var mappings to ALL services that need them, not just one.`

// PromptDevStagingPhilosophy is the shared rules about dev staging environment
// philosophy — keeping things simple, no external secrets, dev-safe defaults.
const PromptDevStagingPhilosophy = `CRITICAL — Dev staging environment philosophy:
This generates a LOCAL DEV environment, NOT production. The goal is for the app
to start and be usable with ZERO manual secret setup. Follow these rules:

1. Dependency connection URLs and passwords are ALREADY handled by the operator.
   When you declare a dependency (postgres, redis, etc.), the operator auto-injects
   the connection URL (DATABASE_URL, REDIS_URL, etc.) AND manages the dependency
   container's credentials internally. Do NOT add any of these to the env block:
   - DATABASE_URL, REDIS_URL, MONGO_URL, AMQP_URL, S3_ENDPOINT, etc.
   - POSTGRES_PASSWORD, DATABASE_PASSWORD, REDIS_PASSWORD, MYSQL_PASSWORD, etc.
   - POSTGRES_USER, POSTGRES_DB, MYSQL_USER, MYSQL_DATABASE, etc.

2. App-level secrets (SECRET_KEY, SESSION_SECRET, JWT_SECRET, UTILS_SECRET,
   ENCRYPTION_KEY, etc.) should be set as plain env vars with a generated
   dev-safe random hex value. Example:
     - name: SECRET_KEY
       value: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
   Generate a DIFFERENT 64-char hex string for each secret.

3. Optional external integrations should be OMITTED entirely from the env block.
   These are not needed for local dev and including them as secretKeyRef will
   cause the pod to fail with CreateContainerConfigError. Skip:
   - Cloud storage (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, GCS_*, AZURE_STORAGE_*)
   - Monitoring/APM (DD_API_KEY, SENTRY_DSN, NEW_RELIC_*, DATADOG_*)
   - Email/SMS (SMTP_*, SENDGRID_*, TWILIO_*, MAILGUN_*)
   - OAuth providers (SLACK_CLIENT_*, GOOGLE_CLIENT_*, GITHUB_CLIENT_*, AUTH0_*)
   - Analytics (SEGMENT_*, MIXPANEL_*, AMPLITUDE_*)
   Instead, if the app has a config option to disable these features, set it.
   For example: FILE_STORAGE=local instead of FILE_STORAGE=s3.

4. Only use valueFrom.secretKeyRef for credentials that are BOTH:
   (a) absolutely required for the app to start (it will crash without them), AND
   (b) truly external (not provided by an in-cluster dependency)
   This should be RARE in a dev environment.

5. ALWAYS check .env.sample, .env.example, .env.development, and similar files
   for REQUIRED configuration. These files list every env var the app expects.
   For each variable found:
   - Skip it if it's an auto-injected dependency URL (DATABASE_URL, REDIS_URL, etc.)
   - Skip it if it's a dependency credential (POSTGRES_PASSWORD, etc.)
   - Skip it if it's an optional external integration (AWS_*, DD_*, SENTRY_*, etc.)
   - For app secrets (SECRET_KEY, SESSION_SECRET, etc.) → set a random 64-char hex value
   - For URL vars that reference the app itself (URL, BASE_URL, APP_URL, COLLABORATION_URL,
     etc.) → set to "http://<actor>-<name>.localhost"
   - For feature flags / storage config → set the local/dev option (e.g. FILE_STORAGE=local)
   - For remaining config → set a sensible dev default
   Missing a required env var is the #1 cause of pods crashing on startup. When in doubt,
   include it with a dev-safe default rather than omitting it.

The "env" input is a YAML block that maps directly to a Kubernetes []EnvVar list.
You MUST use standard Kubernetes EnvVar list format (NOT a map/dict).

Correct format for env vars:
  env: |
    - name: SECRET_KEY
      value: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
    - name: NODE_ENV
      value: "production"
    - name: FILE_STORAGE
      value: "local"

WRONG format (this will cause a CRD validation error):
  env: |
    SECRET_KEY:
      value: "some-value"

If any secretKeyRef IS used, those secrets are managed by
"kindling secrets set <NAME> <VALUE>" and stored as Kubernetes Secrets.
Include a comment noting which secrets need to be set.`

// PromptOAuth is the shared OAuth/public exposure guidance.
const PromptOAuth = `OAuth / public exposure:
If the repository uses OAuth or OIDC (Auth0, Okta, Firebase Auth, NextAuth, etc.),
services that handle OAuth callbacks need a publicly accessible URL instead of
*.localhost. When the user indicates a public URL is available, use it for the
ingress-host of the auth-handling service. If no public URL is specified but OAuth
patterns are detected, add a comment noting:
  # NOTE: OAuth detected — run 'kindling expose' for a public HTTPS URL`

// PromptMultiAgentArchitecture is the shared guidance on detecting and handling
// multi-agent AI architectures (MCP servers, orchestrators, workers, vector stores).
const PromptMultiAgentArchitecture = `Multi-agent architecture support:
Modern AI applications use multi-agent frameworks that produce a common deployment
topology: orchestrator services + worker services + vector stores + message brokers
+ API keys. When the user prompt includes a "Detected multi-agent architecture"
section, use it to inform your workflow generation:

MCP servers:
  MCP (Model Context Protocol) servers are small Python or Node.js services that
  expose tools to AI agents. Treat each MCP server as a SEPARATE first-class service
  with its own build and deploy step. MCP servers typically listen on a port (HTTP/SSE
  mode) or run as stdio processes. For HTTP/SSE MCP servers, configure a port and
  health check. For stdio-only MCP servers, use health-check-type: "none".

Agent frameworks:
  When agent frameworks are detected (CrewAI, LangGraph, AutoGen, OpenAI Agents SDK,
  Anthropic Claude SDK, Strands), the app likely has:
  - An orchestrator/supervisor service (the main entry point)
  - Potentially separate worker services with their own entry points
  - Heavy reliance on API keys (OPENAI_API_KEY, ANTHROPIC_API_KEY, etc.)
  Emit one build+deploy per service that has its own Dockerfile. Wire up API keys
  as secretKeyRef entries (these ARE required for the app to function, unlike optional
  integrations).

Vector stores:
  When vector store dependencies are detected (chromadb, pgvector, pinecone, weaviate,
  qdrant, milvus), handle them as follows:
  - pgvector: add "postgres" dependency (the operator provisions PostgreSQL; pgvector
    extension must be in the Dockerfile or init script)
  - chromadb: if running as a separate service, treat as a deployable service with
    its own Dockerfile. If used as an embedded library, no extra dependency needed.
  - pinecone, weaviate, qdrant (cloud-hosted): these connect to external APIs, so
    surface their API keys (PINECONE_API_KEY, WEAVIATE_API_KEY, QDRANT_API_KEY) as
    secretKeyRef entries. Do NOT add them as local dependencies.

Background workers:
  Celery workers, Kafka consumers, RabbitMQ subscribers, and async task processors
  should be deployed as SEPARATE services (not just dependencies). Look for:
  - Celery: separate deployment with the celery command as entrypoint. Wire up
    redis or rabbitmq as the broker dependency.
  - Kafka consumers: separate deployment that reads from topics. Add kafka dependency.
  - AMQP subscribers: separate deployment. Add rabbitmq dependency.
  Each worker needs its own deploy step with appropriate dependencies and env vars.

Inter-service networking:
  When multiple services in an agent architecture call each other (orchestrator → worker,
  agent → MCP server, API → worker), wire up the env vars for service discovery using
  Kubernetes DNS names: $ACTOR-<service-name>:<port>. Scan source code for HTTP client
  calls, gRPC channel targets, or env vars ending in _URL, _ADDR, _ENDPOINT that
  reference other services.`

// PromptFinalValidation is the shared final validation checklist.
const PromptFinalValidation = `FINAL VALIDATION — before outputting the YAML, verify:
  1. Every deploy step that uses $(AMQP_URL) in its env MUST have "- type: rabbitmq"
     in its dependencies. Every step using $(REDIS_URL) MUST have "- type: redis".
     Every step using $(DATABASE_URL) MUST have "- type: postgres" (or mysql).
     A $(VAR) reference without the matching dependency will cause a runtime crash.
  2. Every build step's "context" matches the docker-compose "build.context" for that
     service. If context is "." or the repo root, use the workspace root path.

Return ONLY the raw YAML content of the workflow file. No markdown code fences,
no explanation text, no commentary. Just the YAML.`
