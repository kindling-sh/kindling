# Testing — Internal Reference

This document describes the test strategy, test locations, running
tests, and the fuzz testing infrastructure.

Source: `cli/cmd/*_test.go`, `cli/core/*_test.go`,
`internal/controller/*_test.go`, `pkg/ci/*_test.go`, `test/`

---

## Test strategy

### Unit tests

Core business logic is tested with standard Go unit tests. Tests
mock external dependencies (kubectl, Docker, Kind) to run without
a cluster.

### Integration tests (e2e)

End-to-end tests in `test/e2e/` require a running Kind cluster.
They exercise the full stack: CLI → operator → K8s resources.

### Fuzz tests

Property-based testing in `test/fuzz/` for input validation and
parsing logic.

---

## Test locations

### CLI command tests (`cli/cmd/`)

| File | Tests |
|---|---|
| `commands_test.go` | Command registration, flag parsing, help text |
| `dashboard_test.go` | Dashboard API endpoint unit tests |
| `generate_test.go` | Workflow generation, project scanning, detection functions |
| `sync_test.go` | Runtime detection, profile selection, debounce logic |

### CLI core tests (`cli/core/`)

Test files mirror the source files:
- `kubectl_test.go` — kubectl wrapper tests
- `secrets_test.go` — secret CRUD, naming convention
- `runners_test.go` — runner pool creation
- `tunnel_test.go` — tunnel provider detection, URL parsing
- `env_test.go` — env var management
- `load_test.go` — build + load pipeline

### Operator controller tests (`internal/controller/`)

| File | Tests |
|---|---|
| `devstagingenvironment_controller_test.go` | DSE reconciliation, dependency provisioning, status updates, orphan pruning |
| `cirunnerpool_controller_test.go` | Runner pool reconciliation, provider adapter delegation |
| `suite_test.go` | Test suite setup with envtest (fake K8s API server) |

### CI package tests (`pkg/ci/`)

| File | Tests |
|---|---|
| `github_test.go` | GitHub adapter, runner config, workflow generation |
| `gitlab_test.go` | GitLab adapter, runner config, workflow generation |
| `registry_test.go` | Provider registration, thread safety |
| `base_test.go` | DNS name sanitization, label formatting |

### E2E tests (`test/e2e/`)

Full-stack tests that require a running cluster:
- Cluster initialization
- DSE deployment and status
- Dependency provisioning
- Service accessibility
- Ingress routing

### Fuzz tests (`test/fuzz/`)

Fuzz targets for:
- YAML parsing (malformed DSE specs)
- Secret name sanitization (edge cases)
- URL construction (injection attacks)
- Runtime detection (unexpected /proc/1/cmdline content)

---

## Running tests

### Unit tests (no cluster required)

```bash
# All CLI tests
cd cli && go test ./...

# Specific package
cd cli && go test ./cmd/ -v
cd cli && go test ./core/ -v

# Specific test
cd cli && go test ./cmd/ -run TestSyncRuntimeDetection -v

# Operator controller tests (uses envtest)
go test ./internal/controller/ -v

# CI package tests
go test ./pkg/ci/ -v

# All unit tests
make test
```

### With coverage

```bash
# CLI coverage
cd cli && go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Operator coverage
go test ./internal/controller/ -coverprofile=coverage.out
go tool cover -html=coverage.out

# Full project coverage
make test-coverage
```

### E2E tests (cluster required)

```bash
# Ensure cluster is running
kindling status

# Run e2e tests
go test ./test/e2e/ -v -timeout 300s
```

E2E tests are slow (~5 minutes) because they wait for real deployments
to become ready.

### Fuzz tests

```bash
# Run fuzz target for 30 seconds
go test ./test/fuzz/ -fuzz=FuzzDSEParsing -fuzztime=30s

# Run fuzz target with corpus
go test ./test/fuzz/ -fuzz=FuzzSecretNameSanitization -fuzztime=60s
```

Fuzz corpus files are stored in `test/fuzz/testdata/`.

---

## Test infrastructure

### envtest (operator tests)

Operator controller tests use `controller-runtime/pkg/envtest` to
spin up a lightweight API server without a full cluster:

```go
func TestMain(m *testing.M) {
    testEnv = &envtest.Environment{
        CRDDirectoryPaths: []string{
            filepath.Join("..", "..", "config", "crd", "bases"),
        },
    }

    cfg, _ := testEnv.Start()
    // ... set up manager, register controllers
    code := m.Run()
    testEnv.Stop()
    os.Exit(code)
}
```

This provides:
- Real K8s API server (etcd + apiserver binaries)
- CRD registration from kustomize bases
- No kubelet, no scheduler, no controller-manager
- Fast startup (~2s) compared to Kind (~30s)

The envtest binaries are stored in `bin/k8s/` and managed by
`setup-envtest`.

### Test helpers

Common test patterns:

```go
// Create a DSE and wait for reconciliation
func createDSE(t *testing.T, spec DSESpec) *DSE {
    dse := &DSE{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-" + rand.String(5),
            Namespace: "default",
        },
        Spec: spec,
    }
    Expect(k8sClient.Create(ctx, dse)).To(Succeed())

    // Wait for status update
    Eventually(func() bool {
        k8sClient.Get(ctx, client.ObjectKeyFromObject(dse), dse)
        return dse.Status.DeploymentReady
    }, timeout, interval).Should(BeTrue())

    return dse
}
```

### Mocking kubectl

CLI tests mock kubectl by replacing the executor:

```go
func TestSecretsSet(t *testing.T) {
    // Capture kubectl calls
    var calls [][]string
    core.SetKubectlExecutor(func(args ...string) (string, error) {
        calls = append(calls, args)
        return "", nil
    })
    defer core.ResetKubectlExecutor()

    // Run command
    cmd := NewSecretsSetCmd()
    cmd.SetArgs([]string{"MY_KEY", "my_value"})
    cmd.Execute()

    // Assert kubectl was called correctly
    assert.Contains(t, calls[0], "create")
    assert.Contains(t, calls[0], "secret")
}
```

---

## Makefile test targets

```makefile
test:              ## Run all unit tests
    go test ./... -count=1
    cd cli && go test ./... -count=1

test-coverage:     ## Run tests with coverage
    go test ./... -coverprofile=coverage.out
    cd cli && go test ./... -coverprofile=cli-coverage.out

test-e2e:          ## Run e2e tests (requires cluster)
    go test ./test/e2e/ -v -timeout 300s

test-fuzz:         ## Run fuzz tests for 60s each
    go test ./test/fuzz/ -fuzz=. -fuzztime=60s

lint:              ## Run linters
    golangci-lint run ./...
    cd cli && golangci-lint run ./...
```

---

## CI test matrix

Tests run in the generated CI workflow:

```yaml
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - run: make test
      - run: make lint
```

E2E tests are not run in CI by default (they require Kind + Docker).
They're intended for local development and pre-release validation.
