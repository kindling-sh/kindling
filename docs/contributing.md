# Contributing

This document covers development environment setup, building,
module layout, code organization, and release process.

---

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Go | 1.25+ | Operator + CLI |
| Docker Desktop | Latest | Kind cluster runtime |
| Kind | 0.20+ | Local K8s clusters |
| kubectl | 1.28+ | Cluster interaction |
| Node.js | 18+ | Dashboard frontend |
| kustomize | 5+ | Operator manifests |
| controller-gen | Latest | CRD + RBAC generation |

Install dev tools:
```bash
make install-tools   # installs controller-gen, kustomize, setup-envtest
```

---

## Repository layout

```
kindling/
├── go.mod                 ← operator module
├── go.sum
├── Makefile               ← build targets
├── Dockerfile             ← operator container image
├── kind-config.yaml       ← Kind cluster configuration
├── setup-ingress.sh       ← registry + ingress setup script
├── PROJECT                ← kubebuilder project metadata
│
├── api/v1alpha1/          ← CRD type definitions
│   ├── devstagingenvironment_types.go
│   ├── githubactionrunnerpool_types.go
│   ├── groupversion_info.go
│   └── zz_generated.deepcopy.go
│
├── internal/controller/   ← operator reconciliation logic
│   ├── devstagingenvironment_controller.go
│   ├── cirunnerpool_controller.go
│   └── suite_test.go
│
├── cmd/main.go            ← operator entry point
│
├── pkg/ci/                ← shared CI provider package
│   ├── interfaces.go      ← Provider, RunnerAdapter, WorkflowGenerator
│   ├── registry.go        ← thread-safe provider registry
│   ├── base.go            ← BaseRunnerAdapter (shared logic)
│   ├── prompt.go          ← AI prompt constants
│   ├── github.go          ← GitHub Actions provider
│   └── gitlab.go          ← GitLab CI provider
│
├── cli/                   ← CLI module (separate go.mod)
│   ├── go.mod
│   ├── main.go
│   ├── cmd/               ← cobra commands
│   │   ├── dashboard-ui/  ← React frontend
│   │   └── *.go
│   └── core/              ← business logic
│
├── config/                ← kustomize manifests
│   ├── crd/               ← CRD bases + patches
│   ├── default/           ← default overlay
│   ├── manager/           ← operator deployment
│   ├── rbac/              ← RBAC resources
│   ├── registry/          ← in-cluster registry
│   └── samples/           ← example CRs
│
├── docs/                  ← internal documentation (this dir)
├── docs-site/             ← Docusaurus public docs
│
├── test/
│   ├── e2e/               ← end-to-end tests
│   └── fuzz/              ← fuzz test targets
│
└── bin/                   ← built binaries + dev tools
    ├── controller-gen
    ├── kustomize
    ├── setup-envtest
    ├── kindling            ← CLI binary
    └── k8s/               ← envtest K8s binaries
```

---

## Three Go modules

### 1. Operator module (`go.mod` at root)

```
module github.com/jeffvincent/kindling
go 1.25

require (
    sigs.k8s.io/controller-runtime v0.23.1
    k8s.io/api v0.35.0
    k8s.io/apimachinery v0.35.0
    k8s.io/client-go v0.35.0
)
```

Contains: API types, controllers, `pkg/ci`, operator entry point.

### 2. CLI module (`cli/go.mod`)

```
module github.com/jeffvincent/kindling/cli
go 1.25

require (
    github.com/spf13/cobra v1.8.0
    github.com/fsnotify/fsnotify v1.9.0
    github.com/gorilla/mux v1.8.1
)

replace github.com/jeffvincent/kindling/pkg/ci => ../pkg/ci
```

Contains: All CLI commands, core logic, dashboard.

The `replace` directive lets the CLI import `pkg/ci` without
publishing it as a separate module.

### 3. CI package (`pkg/ci/`)

Not a separate module — it's part of the operator module but
consumed by the CLI via `replace`. Contains provider interfaces,
registry, prompt constants, and GitHub/GitLab implementations.

---

## Building

### CLI

```bash
# Development build
cd cli && go build -o kindling .

# Or via Makefile
make cli

# With version info
cd cli && go build -ldflags "-X main.version=0.8.1" -o kindling .

# Install to $GOPATH/bin
cd cli && go install .
```

### Operator

```bash
# Local binary (for testing)
go build -o manager cmd/main.go

# Container image
make docker-build IMG=kindling-controller:latest

# Or via Makefile
make build
```

### CRD manifests

```bash
# Generate CRD YAML from Go types
make manifests

# Generate deepcopy methods
make generate
```

These use `controller-gen` and must be run after changing
`api/v1alpha1/*_types.go`.

### Dashboard frontend

```bash
cd cli/cmd/dashboard-ui
npm install
npm run build    # production build → dist/
npm run dev      # dev server with HMR
```

---

## Development workflow

### Making operator changes

1. Edit `internal/controller/*.go` or `api/v1alpha1/*_types.go`
2. If types changed: `make manifests generate`
3. Run tests: `go test ./internal/controller/ -v`
4. Build: `make docker-build IMG=kindling-controller:dev`
5. Deploy to local cluster: `make deploy IMG=kindling-controller:dev`

### Making CLI changes

1. Edit `cli/cmd/*.go` or `cli/core/*.go`
2. Run tests: `cd cli && go test ./... -v`
3. Build: `cd cli && go build -o kindling .`
4. Test manually against local cluster

### Making dashboard changes

1. Start Go server: `cd cli && go run . dashboard`
2. Start Vite dev server: `cd cli/cmd/dashboard-ui && npm run dev`
3. Edit React components — HMR reloads automatically
4. Build for embedding: `cd cli/cmd/dashboard-ui && npm run build`
5. Rebuild CLI: `cd cli && go build -o kindling .`

### Making pkg/ci changes

1. Edit `pkg/ci/*.go`
2. Run tests: `go test ./pkg/ci/ -v`
3. Both operator and CLI consume this package
4. Rebuild whichever needs the change

---

## Code style

### Go

- Standard `gofmt` formatting
- `golangci-lint` for linting
- Error wrapping with `fmt.Errorf("context: %w", err)`
- Table-driven tests
- No global state except provider registry

### TypeScript

- Prettier for formatting
- ESLint for linting
- Functional components with hooks
- TypeScript strict mode

### Commit messages

```
<type>: <short description>

Types:
  feat:     New feature
  fix:      Bug fix
  docs:     Documentation only
  refactor: Code change that neither fixes nor adds
  test:     Adding or updating tests
  chore:    Build, CI, tooling changes
```

---

## Makefile reference

Key targets:

```makefile
# Building
build:             ## Build operator binary
cli:               ## Build CLI binary
docker-build:      ## Build operator Docker image

# Code generation
manifests:         ## Generate CRD manifests + RBAC
generate:          ## Generate deepcopy methods

# Testing
test:              ## Run all unit tests
test-coverage:     ## Run tests with coverage report
test-e2e:          ## Run e2e tests (requires cluster)
lint:              ## Run linters

# Deployment
install:           ## Install CRDs into cluster
uninstall:         ## Remove CRDs from cluster
deploy:            ## Deploy operator to cluster
undeploy:          ## Remove operator from cluster

# Tools
install-tools:     ## Install dev dependencies
controller-gen:    ## Download controller-gen
kustomize:         ## Download kustomize
envtest:           ## Download setup-envtest

# Convenience
run:               ## Run operator locally (outside cluster)
help:              ## Show this help
```

### Image configuration

```makefile
IMG ?= controller:latest
```

Override with: `make docker-build IMG=myregistry/kindling:v1.0.0`

---

## Release process

### Version bump

1. Update `version` in `cli/cmd/version.go`
2. Update `CHANGELOG.md`
3. Run tests: `make test`
4. Build: `make cli`
5. Tag: `git tag v0.9.0`
6. Push: `git push origin v0.9.0`

### Binary distribution

The CLI binary is the primary distribution artifact:

```bash
# Build for multiple platforms
GOOS=darwin GOARCH=arm64 go build -o kindling-darwin-arm64 ./cli
GOOS=darwin GOARCH=amd64 go build -o kindling-darwin-amd64 ./cli
GOOS=linux GOARCH=amd64  go build -o kindling-linux-amd64  ./cli
GOOS=linux GOARCH=arm64  go build -o kindling-linux-arm64  ./cli
```

### Operator image

```bash
make docker-build IMG=ghcr.io/jeffvincent/kindling:v0.9.0
make docker-push  IMG=ghcr.io/jeffvincent/kindling:v0.9.0
```

---

## Project configuration files

### kind-config.yaml

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "ingress-ready=true"
    extraPortMappings:
      - containerPort: 80
        hostPort: 80
        protocol: TCP
      - containerPort: 443
        hostPort: 443
        protocol: TCP
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."registry:5000"]
      endpoint = ["http://registry:5000"]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:5001"]
      endpoint = ["http://registry:5000"]
```

### PROJECT (kubebuilder)

```yaml
domain: example.com
layout:
  - go.kubebuilder.io/v4
projectName: kindling
repo: github.com/jeffvincent/kindling
resources:
  - api:
      crdVersion: v1
      namespaced: true
    group: apps
    kind: DevStagingEnvironment
    version: v1alpha1
  - api:
      crdVersion: v1
      namespaced: true
    group: apps
    kind: CIRunnerPool
    version: v1alpha1
version: "3"
```
