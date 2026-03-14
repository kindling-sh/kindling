# Kindling — Roadmap

**kindling is a development lifecycle tool.** It takes your project from
first commit to production deployment. CI is the entry point — you run
`kindling init` and have a working pipeline in minutes. But the real
value is everything that comes after: production-readiness guardrails
that catch security gaps, scalability issues, and container anti-patterns
before they become tech debt.

CI works out of the box. Guardrails are opt-in and configurable.

```
  analyze → generate → dev loop → harden → docs → promote
     │          │          │          │        │        │
     │      CI pipeline   │    production   eng    ship to
   check     (core)    build   readiness  artifacts  prod
  readiness            & test   (opt-in)  (opt-in)
```

---

## P0 — Remove every barrier to trying kindling

### One-liner install script

```bash
curl -sL https://kindling.dev/install | sh
```

Detect OS + arch, download the right binary, drop it in `/usr/local/bin`.

### ✅ Interactive ingress selection in `kindling generate`

Implemented on `feat/topology-editor`. `--ingress-all` flag for non-interactive use.

### ✅ MCP server detection in `generate`

Detects `mcp.json`, `mcp.config.json`, `@mcp.tool()`, `StdioServerTransport`,
`FastMCP`, `@modelcontextprotocol`. Each MCP server with its own Dockerfile
becomes a separate build+deploy step.

### ✅ Background workers as first-class deployments

Celery workers, Kafka consumers, RabbitMQ subscribers detected via source
patterns, Procfile, and docker-compose. Each worker gets a separate deploy step.

### ✅ Inter-service networking validation

Detects HTTP client calls, gRPC channels, service URL env vars, compose
depends_on graphs. Wires K8s Service DNS names for discovery.

### ✅ Agent context — `kindling intel`

Automatic lifecycle management. Backs up existing agent configs, writes kindling
context document, auto-restores after 1 hour of inactivity. Supports Copilot,
Claude Code, Cursor, Windsurf.

### ✅ Concurrent `kindling sync` sessions

Per-pod PID files, no global locks, no shared state. Parallel syncs work today.

### Make `generate` multi-agent-aware

Detection rules for agent frameworks (CrewAI, LangGraph, AutoGen, OpenAI Agents
SDK, Claude Agent SDK, LangChain, LlamaIndex, Strands). Auto-detect orchestration
patterns, emit per-agent services, wire message brokers and inter-service networking.

### Vector store dependency detection

RAG stacks are ubiquitous. Default: respect external services (Pinecone,
Weaviate Cloud, Qdrant Cloud). Surface required API keys in secrets detection.
Local option: note that pgvector/ChromaDB can run as kindling dependencies.

### Generate rules reference (`.kindling/generate-rules.md`)

Emit the full generate ruleset as readable Markdown alongside the workflow.
The agent context links to it. Covers Kaniko compat, dependency detection,
auto-injected env vars, health checks, build timeouts, Docker Compose handling.

---

## P0.5 — Production readiness guardrails (`kindling harden`)

This is the core expansion: kindling knows enough about the user's app to
prescribe secure, performant, production-ready patterns. Not just deploy
what they give us — tell them what's wrong and how to fix it.

**Guardrails are opt-in.** Users who just want CI never see them. Users who
want production readiness get a configurable, opinionated toolchain.

### Configuration (`.kindling/harden.yaml`)

```yaml
# .kindling/harden.yaml — production readiness configuration
severity: moderate    # gentle | moderate | strict
gate-deploy: false    # if true, `kindling deploy` blocks on errors

categories:
  security: true
  scalability: true
  performance: true
  containers: true
  observability: true
  ci-hygiene: true

# Per-rule overrides
overrides:
  no-root-container: error     # override default severity
  pin-base-images: off         # disable a specific rule
```

**Severity levels:**

| Level | Behavior |
|---|---|
| `gentle` | Info-only. Print suggestions, never block. Good for learning. |
| `moderate` | Warnings for important issues, errors for critical ones (secrets in YAML, root containers). Default. |
| `strict` | Everything that isn't production-ready is an error. For teams shipping to real infrastructure. |

### `kindling harden` command

```bash
kindling harden                    # scan cwd, apply configured severity
kindling harden --strict           # override severity for this run
kindling harden --fix              # auto-fix what can be fixed deterministically
kindling harden --category security  # run only security checks
kindling harden --init             # create default .kindling/harden.yaml
```

### Category: Security

Extends `checkSecurity()` already in `analyze.go`. New rules:

| Rule | Default | Description |
|---|---|---|
| `no-hardcoded-secrets` | error | API keys/tokens inline in source or YAML |
| `secrets-via-kindling` | error | Secrets should use `kindling secrets set` → `secretKeyRef` |
| `no-env-files-in-git` | error | `.env` files tracked in git |
| `dependency-pinning` | warning | Unpinned deps in requirements.txt, package.json |
| `no-eval` | warning | `eval()` usage in source (code injection vector) |
| `no-shell-injection` | warning | `shell=True`, `os.system()` with potential user input |
| `no-sql-concatenation` | warning | SQL string building instead of parameterized queries |
| `cors-wildcard` | info | `Access-Control-Allow-Origin: *` |
| `debug-mode-off` | info | `DEBUG=True` in source |
| `gitignore-hygiene` | warning | `.gitignore` missing patterns for secrets/env files |

### Category: Container Best Practices

Extends `checkDockerSecurityPosture()`. New rules:

| Rule | Default | Description |
|---|---|---|
| `no-root-container` | error | Dockerfile missing `USER` directive — runs as root |
| `pin-base-images` | warning | `FROM node:latest` or `FROM python` without tag |
| `multi-stage-build` | info | Single-stage build includes build tools in production image |
| `minimal-base-image` | info | Using `ubuntu` when `alpine` or `distroless` would work |
| `no-secrets-in-layers` | error | `COPY .env`, `COPY *.key`, `COPY *.pem` in Dockerfile |
| `healthcheck-present` | warning | No HEALTHCHECK instruction in Dockerfile |
| `copy-specific-files` | info | `COPY . .` when specific paths would reduce layer size |
| `read-only-filesystem` | info | Container filesystem should be read-only where possible |
| `drop-capabilities` | info | Suggest `securityContext.capabilities.drop: [ALL]` |

### Category: Scalability

| Rule | Default | Description |
|---|---|---|
| `resource-limits` | warning | No CPU/memory limits on containers |
| `graceful-shutdown` | warning | No SIGTERM handler — pods get SIGKILL after 30s |
| `connection-pooling` | info | Multiple workers connecting to DB without pooling |
| `stateless-services` | info | Local file writes in HTTP handlers (won't survive pod restart) |
| `horizontal-scaling` | info | No HPA configured — suggest adding one for HTTP services |
| `readiness-probe` | warning | No readiness probe — traffic hits pods before they're ready |

### Category: Performance

| Rule | Default | Description |
|---|---|---|
| `no-sync-io-in-async` | warning | Blocking I/O in async handlers (Python asyncio, Node.js) |
| `n-plus-one-queries` | info | Loop-based DB queries instead of batch operations |
| `missing-indexes` | info | DB migrations without index creation on foreign keys |
| `cache-headers` | info | Static assets served without cache-control headers |
| `compression` | info | No gzip/brotli compression on API responses |

### Category: Observability

| Rule | Default | Description |
|---|---|---|
| `structured-logging` | info | Using `print()` / `console.log()` instead of structured logger |
| `request-id-tracing` | info | No request ID propagation across services |
| `error-reporting` | info | No error tracking integration (Sentry, Rollbar, etc.) |
| `metrics-endpoint` | info | No `/metrics` endpoint for Prometheus scraping |

### Category: CI Hygiene

| Rule | Default | Description |
|---|---|---|
| `duplicate-auto-injected-env` | error | `DATABASE_URL` in env when postgres is a dependency |
| `missing-health-check-path` | warning | No health-check-path on HTTP service DSE |
| `workflow-valid` | error | Generated workflow YAML doesn't parse |
| `dse-valid` | error | DSE YAML has schema violations |

### Deploy-time integration

When `gate-deploy: true` is set in config, `kindling deploy` runs the
harden engine before applying. Errors block the deploy. Warnings print
but proceed. This makes guardrails a CI/CD gate — not just a suggestion.

```
$ kindling deploy -f dev-environment.yaml

  ▸ Running production readiness checks

  ❌ OPENAI_API_KEY is set as a plain value — use 'kindling secrets set' instead
  ⚠️  No health-check-path on service 'api'
  ⚠️  api/Dockerfile runs as root — add USER directive

  ❌ Deploy blocked — 1 error(s). Fix and retry, or set gate-deploy: false
```

### Agent integration

Add a "Production Readiness" section to the intel context document so coding
agents know the rules during development — before `harden` even runs.

---

## P0.5 — Engineering artifacts (`kindling docs`)

When code is built at speed — especially with AI — the engineering artifacts
that make a codebase maintainable don't get created. `kindling docs` generates
them from the actual codebase, not from imagination.

### `kindling docs` command

```bash
kindling docs                       # generate all artifact types
kindling docs --type spec           # just the spec sheet
kindling docs --type api            # just API documentation
kindling docs --type runbook        # just the operations runbook
kindling docs --type onboarding     # just the onboarding guide
kindling docs --type adr            # architecture decision records
kindling docs --output ./docs       # custom output directory
kindling docs --format markdown     # markdown (default) or html
```

### Artifact types

**1. Spec sheet** (`spec.md`)
- System purpose and architecture overview
- Service inventory (name, language, port, dependencies, entry point)
- Dependency map (which services depend on what)
- Environment variables (name, source, which service uses it)
- External integrations (APIs, OAuth providers, webhooks)
- Data flow diagram (text-based, from inter-service call detection)

**2. API documentation** (`api.md`)
- Per-service endpoint inventory (from route detection in source)
- Request/response schemas (from type hints, TypeScript types, Go structs)
- Authentication requirements
- Health check endpoints

**3. Operations runbook** (`runbook.md`)
- How to deploy (the kindling commands)
- How to debug (kindling diagnose, logs, debug attach)
- Common failure modes and fixes
- Secret rotation procedures
- Scaling guidance

**4. Onboarding guide** (`onboarding.md`)
- Prerequisites (Docker, Kind, kindling)
- Step-by-step "get it running" instructions
- Architecture diagram
- Key files and what they do
- Development workflow (edit → sync → test → push)

**5. Architecture decision records** (`adr/`)
- Generated from detected technology choices
- "Why Postgres over MySQL" (from dependency declarations)
- "Why microservices over monolith" (from multi-service layout)
- "Why Kaniko over Docker-in-Docker" (from kindling's build protocol)
- Template for future ADRs

### How it works

1. Reuse `scanRepo()` from `generate.go` — same codebase analysis
2. Extend with route detection (FastAPI decorators, Express routes,
   Gin handlers, etc.)  
3. Feed structured context to LLM (same `callGenAI()` pattern)
4. Output to `.kindling/docs/` (default) or custom directory
5. Regenerate on demand — always based on current code, never stale

### Flags

- `--api-key` / `-k` — GenAI API key (same as generate)
- `--ai-provider` — openai or anthropic (same as generate)
- `--type` — specific artifact type (default: all)
- `--output` / `-o` — output directory
- `--format` — markdown or html
- `--no-ai` — deterministic-only output (no LLM, just structured extraction)

---

## P1 — Dashboard & UX improvements

### Dashboard: API Explorer as core DSE view

Make the API explorer the default view when clicking a DSE. Show all services
as addressable targets with ports and health endpoints pre-populated. Surface
inter-service calls for request tracing.

### Dashboard: interactive service health resolution

When the dashboard detects a failing service, diagnose the root cause inline
and surface a fix action:

| Failure | Inline action |
|---|---|
| Missing secret | Text input → `kindling secrets set` |
| CrashLoopBackOff | Show logs + "Restart"/"Edit env" buttons |
| ImagePullBackOff | "Rebuild" button → `kindling load` |
| Port conflict | "Change port" input → patch deployment |

### Consistent CLI working directory

All commands should detect/require the project root consistently. Commands
that need the project root should error clearly when run from the wrong
directory.

---

## P1 — Content & visibility

### 3-minute quickstart guarantee

Time the quickstart end-to-end. Pre-bake defaults, auto-detect GitHub remote
and username. Put the time in the README.

### Fuzz test `kindling generate` against wild repos

Clone real-world repos, run `generate`, record structured results.
Quality gates: ≤15% crash rate, ≥80% success on repos with Dockerfiles.

### Show HN

Polish README, record demo. Tuesday–Thursday, 8–10am ET.

### Blog posts

Each post has a "the hard way → the kindling way" arc:

**Getting Started:**
- "Zero to Staging in 5 Minutes"
- "Stop Paying for CI You Already Own"
- "Replaced docker-compose with a Kubernetes Operator"

**Language Tutorials:**
- FastAPI + Postgres
- Next.js + Redis
- Rails + Sidekiq
- Go Microservices (4 services, 3 databases)
- Rust + Multi-Stage Builds

**Feature Deep Dives:**
- "How kindling generate Actually Works"
- "15 Dependencies, Zero Configuration"
- "Managing Secrets in Local Kubernetes"
- "OAuth on Localhost with kindling expose"
- "The Build-Agent Sidecar: Containers Without Docker"

**Real-World Scenarios:**
- Stripe Webhooks Locally
- Multi-Service E-Commerce
- Debugging CI Failures When the Runner Is on Your Desk
- Live Environment Variable Updates Without Redeploying

### Community

- r/kubernetes, r/devops, r/selfhosted — help people, mention kindling when relevant
- CNCF Slack, Kubernetes Slack (`#kind`, `#local-dev`)
- CFPs: DevOpsDays, KubeCon, SeaGL

---

## P2 — `kindling promote`: dev → production

Everything above is about making the dev environment perfect. `promote`
is what happens next: the app works locally, ship it to a real cluster.

```bash
kindling promote                                              # interactive
kindling promote --export helm                                # Helm chart
kindling promote --export kustomize                           # Kustomize overlay
kindling promote --context prod --registry ghcr.io/myorg      # direct deploy
```

### Core features

1. **Cluster state export** — read dev cluster, produce production-ready
   Helm chart or Kustomize overlay. Secret values redacted with placeholders.
2. **Direct deploy** — push images to target registry, apply manifests,
   install cert-manager, issue Let's Encrypt certs, wait for rollout.
3. **TLS auto-configuration** — cert-manager detection/installation,
   ClusterIssuer creation, Certificate resources per ingress service.
4. **DNS guidance** — print exact DNS records needed before deploy,
   verify resolution after.

---

## P2 — More example apps

| App | Ecosystem | Purpose |
|---|---|---|
| Rails + Sidekiq | Ruby | Large community, Docker adoption |
| Django + Celery | Python | Massive, underserved by local K8s |
| Spring Boot | Java | Enterprise developers |
| Next.js + Redis | Node/React | Biggest frontend framework |
| Laravel | PHP | Still enormous |
| FastAPI + Postgres | Python | Growing fast, modern audience |

Each example: realistic (uses a database, has a real UI), self-contained,
documented with its own README.

---

## P3 — `kindling diagnose` (runtime issue detection)

`kindling analyze` checks before deploy. `kindling diagnose` checks after
deploy. Already partially implemented — extend with:

### Error detection

- RBAC issues (Forbidden, missing RoleBindings)
- Image pull errors (wrong tag, private repo without imagePullSecrets)
- CrashLoopBackOff with exit codes + last N log lines
- Pending pods (resource limits, affinity, taints)
- Service selector/port mismatches
- Ingress routing gaps
- ConfigMap/Secret missing refs
- Probe failures from pod events

### `--fix` flag

Send collected errors + resource YAML to LLM for remediation suggestions.
Concrete `kubectl` or `kindling` commands. Offline without `--fix`.

---

## P3.5 — Dev loop completeness

### Integration test runner (`kindling test`)

```bash
kindling test --service orders --command "pytest tests/integration"
```

Exec into running pod, inherit env vars (DATABASE_URL, etc.), stream output,
return exit code. `--watch` reruns on sync.

### ✅ In-cluster debugger (`kindling debug`)

Already implemented. Patches deployment with debug agent, port-forwards,
prints VS Code launch.json config. `--stop` restores original state.

---

## P4 — Strategic integrations

### VS Code extension

Status panel, deploy button, logs view, tunnel control. 70%+ market share.

### Devcontainer config

`.devcontainer/` for Gitpod/Codespaces. Zero local setup for first experience.

### GitHub Marketplace

Publish `kindling-build` and `kindling-deploy` as verified Marketplace actions.

---

## P5 — Multi-platform CI & cluster providers

### CI platforms

- ✅ GitHub Actions
- ✅ GitLab CI
- Bitbucket Pipelines (planned)
- Gitea Actions (planned)

### Cluster providers

Abstract `ClusterProvider` interface (`kind`, `k3d`, `minikube`).
Default is Kind. `--cluster-provider` flag for alternatives.

---

## P6 — `kindling export` (production manifests)

Generate Helm chart or Kustomize overlay from live cluster state. Every
user-created resource converted to clean K8s primitives. Kindling-specific
and Kind-specific resources filtered out. Secret values redacted.

---

## P7 — Expose improvements

### Stable callback URL

Relay service at `<username>.relay.kindling.dev` that stores current tunnel
URL and 307-redirects. Stable URL for OAuth callbacks and webhooks.

### Live service switching

Re-target tunnel to a different service without restarting:
```bash
kindling expose --service gateway   # re-patch ingress, tunnel stays
```

---

## P7.5 — Topology editor improvements

### Live cluster state overlay

Overlay real-time pod status on topology nodes — green/yellow/red dots,
restart count badges, last deploy timestamps, resource usage sparklines.

### Already implemented

- File-first architecture (`.kindling/environments/*.yaml` is source of truth)
- Ingress config in the editor (toggle per service)

---

## P8 — Education

- University CS/DevOps programs
- Bootcamp lab adoption
- "kindling 101" curriculum materials
- KubeAcademy / Linux Foundation training integration

---

## P9 — Contributor experience & OSS infrastructure

- `CONTRIBUTING.md` with dev setup, test instructions, PR expectations
- `good-first-issue` labels, contributor shout-outs in release notes
- Issue & PR templates, CODE_OF_CONDUCT.md
- Dynamic README badges (CI status, coverage, Go Report Card)

---

## Future — Native macOS app (Tauri)

Package dashboard as standalone macOS app. System tray with live cluster
status, native notifications, menu bar quick actions, auto-start on login.

**Prerequisites:** Dashboard feature set stable, demand from non-CLI users.
