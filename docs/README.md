# Internal Documentation

This directory contains **internal technical documentation** for kindling
maintainers and contributors. It is not published to the docs site.

For user-facing documentation, see [`docs-site/`](../docs-site/).

---

## Document Index

| Document | What it covers |
|---|---|
| [architecture.md](architecture.md) | System design, two-loop model, operator pattern, cluster topology, build protocol, networking, design decisions |
| [operator-internals.md](operator-internals.md) | Controller reconciliation logic, CRD types, dependency provisioning, status updates, RBAC, watchers |
| [cli-internals.md](cli-internals.md) | Every CLI command with flags, implementation details, code paths, error handling, module structure |
| [dashboard-internals.md](dashboard-internals.md) | Dashboard server, all API endpoints, frontend pages, topology editor architecture |
| [ci-workflows.md](ci-workflows.md) | GitHub Actions + GitLab CI generation, AI prompt system, Kaniko builds, provider abstraction |
| [dependencies.md](dependencies.md) | All 15 dependency types, auto-injection, connection URLs, init containers, operator registry |
| [secrets-internals.md](secrets-internals.md) | Secret flow end-to-end, naming conventions, dual persistence, pre-flight checking |
| [user-flows.md](user-flows.md) | Complete user journey walkthroughs: analyze → generate → push → sync → expose → status |
| [testing.md](testing.md) | Test strategy, unit/e2e/fuzz tests, running tests, test fixtures, CI test matrix |
| [contributing.md](contributing.md) | Dev environment setup, building, module layout, PR process, release process |

---

## Architecture at a glance

```
analyze → generate → dev loop → promote
   ↓          ↓          ↓          ↓
 readiness  workflow   push/sync  production
 check      via AI     iterate    (coming soon)
```

kindling is a Kubernetes operator + CLI that runs your entire dev
environment locally on a Kind cluster. The operator reconciles
`DevStagingEnvironment` CRs into Deployments, Services, Ingresses,
and auto-provisioned dependencies. The CLI wraps the full developer
workflow from project analysis through CI workflow generation,
building, deploying, live-syncing, and tunneling.

## Module layout

```
kindling/                    ← operator module (github.com/jeffvincent/kindling)
├── api/v1alpha1/            ← CRD type definitions (DSE, CIRunnerPool)
├── internal/controller/     ← operator reconciliation logic
├── pkg/ci/                  ← shared CI provider package (github, gitlab)
├── cli/                     ← CLI module (github.com/jeffvincent/kindling/cli)
│   ├── cmd/                 ← all cobra commands + dashboard UI
│   └── core/                ← business logic (kubectl, secrets, tunnel, etc.)
├── config/                  ← kustomize manifests for operator deployment
├── docs/                    ← this directory (internal docs)
└── docs-site/               ← Docusaurus public docs site
```
