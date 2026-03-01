# CI Workflow Generation

This document describes the AI-powered CI workflow generation system,
the prompt architecture, Kaniko build protocol, and provider abstraction.

Source: `pkg/ci/`, `cli/cmd/generate.go`, `cli/cmd/analyze.go`

---

## Overview

`kindling generate` produces two artifacts:
1. A CI workflow file (`.github/workflows/dev-deploy.yml` or `.gitlab-ci.yml`)
2. A DSE YAML file (`.kindling/dev-environment.yaml`)

The generation pipeline:

```
scan project → build prompt → call AI → parse response → write files
```

---

## Project scanning

### Detection functions (`generate.go`)

```go
detectDockerfiles(root string) []string
```
Walks the project tree looking for `Dockerfile`, `Dockerfile.*`, and
`*.dockerfile`. Returns relative paths.

```go
detectLanguages(root string) []string
```
Checks for language markers:
- `go.mod` → Go
- `package.json` → JavaScript/TypeScript
- `requirements.txt` / `Pipfile` / `pyproject.toml` → Python
- `Gemfile` → Ruby
- `pom.xml` / `build.gradle` → Java
- `Cargo.toml` → Rust
- `mix.exs` → Elixir
- `composer.json` → PHP
- `*.csproj` → C#/.NET

```go
detectDependencies(root string) []DependencyHint
```
Scans code files for import patterns that suggest infrastructure deps:
- `psycopg2`, `pg`, `sequelize` → postgres
- `redis`, `ioredis` → redis
- `pymongo`, `mongoose` → mongodb
- etc.

```go
detectServices(root string) []ServiceHint
```
Identifies multi-service architectures by looking for multiple
Dockerfiles, `docker-compose.yml` service definitions, or directory
structures like `services/`, `apps/`, `packages/`.

```go
detectPorts(root string) []int
```
Extracts port numbers from Dockerfiles (`EXPOSE`), code (`listen(PORT)`),
and config files.

### Readiness analysis (`analyze.go`)

The analyze command runs 7 check categories:

| Check | What it looks for |
|---|---|
| Dockerfiles | Existence, syntax validity, Kaniko compatibility |
| Dependencies | Infrastructure deps in code vs. DSE spec |
| Secrets | Hardcoded API keys, passwords, tokens in code |
| CI Workflow | Existing workflow files, validation |
| Agents | Multi-service/multi-agent architectures |
| Ports | Exposed ports in Dockerfiles and code |
| Health checks | `/health`, `/healthz`, `/ready` endpoints |

Each check produces a status (✅ pass, ⚠️ warning, ❌ fail) and
actionable suggestions.

---

## Prompt architecture (`pkg/ci/prompt.go`)

The prompt system is designed to be platform-agnostic. It defines
constants that both the GitHub and GitLab providers consume.

### Prompt structure

```
[System prompt — role + constraints]
[Platform-specific instructions — from provider]
[Kaniko build protocol — signal file format]
[Project context — from scanning]
[Output format instructions — YAML blocks]
```

### Key prompt constants

```go
const SystemPrompt = `You are a CI/CD workflow generator for kindling...`

const KanikoBuildProtocol = `
Builds use Kaniko, not Docker. The workflow must:
1. Create a tarball of the build context
2. Write the destination image to a .dest file
3. Touch a .request file to trigger the build
4. Poll for .done file
5. Read .exitcode for result
`

const KanikoConstraints = `
Kaniko compatibility:
- No TARGETARCH, BUILDPLATFORM ARGs (they'll be empty)
- No .git directory — Go builds need -buildvcs=false
- Poetry needs --no-root
- npm needs ENV npm_config_cache=/tmp/.npm
- RUN --mount=type=cache is ignored
`

const DSEFormat = `
Generate a DevStagingEnvironment YAML with:
- One service per Dockerfile
- Dependencies inferred from code analysis
- Correct ports from EXPOSE directives
- No duplicated auto-injected env vars
`
```

### Provider-specific prompts

Each provider adds platform-specific instructions:

**GitHub Actions:**
```
- Use 'runs-on: self-hosted' with label 'kindling'
- Build steps use /builds/ volume shared with build-agent sidecar
- Use kubectl in the runner to apply DSE CR
- Secrets via ${{ secrets.NAME }} mapped to K8s secretKeyRef
```

**GitLab CI:**
```
- Use tags: [kindling] for runner selection
- Build steps use /builds/ shared volume
- Use kubectl for DSE CR application
- Variables via CI/CD settings mapped to K8s secretKeyRef
```

---

## Provider abstraction (`pkg/ci/`)

### Interface hierarchy

```go
// Provider is the top-level registry entry
type Provider interface {
    Name() string
    RunnerAdapter() RunnerAdapter
    WorkflowGenerator() WorkflowGenerator
}

// RunnerAdapter handles runner pod configuration
type RunnerAdapter interface {
    RunnerImage() string
    WorkDir() string
    TokenKey() string
    EnvVars(spec) []corev1.EnvVar
    RegistrationScript(spec) string
    Labels(spec) string
}

// WorkflowGenerator handles workflow file generation
type WorkflowGenerator interface {
    GeneratePrompt(projectInfo ProjectInfo) string
    ParseResponse(response string) (*WorkflowOutput, error)
    OutputPath() string
}
```

### Provider registry (`registry.go`)

Thread-safe provider registration:

```go
var (
    providers = make(map[string]Provider)
    mu        sync.RWMutex
)

func Register(name string, p Provider) {
    mu.Lock()
    defer mu.Unlock()
    providers[name] = p
}

func GetProvider(name string) (Provider, error) {
    mu.RLock()
    defer mu.RUnlock()
    p, ok := providers[name]
    if !ok {
        return nil, fmt.Errorf("unknown provider: %s", name)
    }
    return p, nil
}
```

Providers self-register in `init()`:
```go
func init() {
    Register("github", &GitHubProvider{})
    Register("gitlab", &GitLabProvider{})
}
```

### Base adapter (`base.go`)

Shared logic for all providers:

```go
type BaseRunnerAdapter struct{}

func (b *BaseRunnerAdapter) SanitizeDNSName(name string) string {
    // K8s DNS label rules: lowercase, alphanumeric, hyphens
    // Max 63 characters
    name = strings.ToLower(name)
    name = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(name, "-")
    name = strings.Trim(name, "-")
    if len(name) > 63 {
        name = name[:63]
    }
    return name
}
```

---

## GitHub Actions provider (`github.go`)

### Runner configuration

```go
func (g *GitHubRunnerAdapter) RunnerImage() string {
    return "ghcr.io/actions/actions-runner:latest"
}

func (g *GitHubRunnerAdapter) TokenKey() string {
    return "RUNNER_TOKEN"
}

func (g *GitHubRunnerAdapter) RegistrationScript(spec) string {
    return fmt.Sprintf(`
        ./config.sh \
            --url https://github.com/%s/%s \
            --token $RUNNER_TOKEN \
            --labels %s \
            --unattended \
            --replace
        ./run.sh
    `, spec.Owner, spec.Repository, g.Labels(spec))
}
```

### Workflow generation

The GitHub workflow generator produces a standard Actions YAML:

```yaml
name: kindling-dev-deploy
on:
  push:
    branches: [main, develop]

jobs:
  build-and-deploy:
    runs-on: [self-hosted, kindling]
    steps:
      - uses: actions/checkout@v4

      - name: Build <service>
        run: |
          tar czf /builds/<service>.tar.gz -C . .
          echo "registry:5000/<service>:${{ github.sha }}" > /builds/<service>.dest
          touch /builds/<service>.request
          while [ ! -f /builds/<service>.done ]; do sleep 2; done
          exit $(cat /builds/<service>.exitcode)

      - name: Deploy
        run: |
          kubectl apply -f .kindling/dev-environment.yaml
```

---

## GitLab CI provider (`gitlab.go`)

### Runner configuration

```go
func (g *GitLabRunnerAdapter) RunnerImage() string {
    return "gitlab/gitlab-runner:latest"
}

func (g *GitLabRunnerAdapter) TokenKey() string {
    return "REGISTRATION_TOKEN"
}
```

### Workflow generation

The GitLab generator produces a `.gitlab-ci.yml`:

```yaml
stages:
  - build
  - deploy

build:
  stage: build
  tags: [kindling]
  script:
    - tar czf /builds/<service>.tar.gz -C . .
    - echo "registry:5000/<service>:${CI_COMMIT_SHA}" > /builds/<service>.dest
    - touch /builds/<service>.request
    - while [ ! -f /builds/<service>.done ]; do sleep 2; done
    - exit $(cat /builds/<service>.exitcode)

deploy:
  stage: deploy
  tags: [kindling]
  script:
    - kubectl apply -f .kindling/dev-environment.yaml
```

---

## AI call flow (`generate.go`)

```go
func generateWorkflow(apiKey, root, provider, model string) error {
    // 1. Scan project
    info := scanProject(root)

    // 2. Get provider
    p := ci.GetProvider(provider)

    // 3. Build prompt
    prompt := p.WorkflowGenerator().GeneratePrompt(info)

    // 4. Call OpenAI
    response := callOpenAI(apiKey, model, prompt)

    // 5. Parse response
    output := p.WorkflowGenerator().ParseResponse(response)

    // 6. Write files
    writeFile(output.WorkflowPath, output.WorkflowContent)
    writeFile(".kindling/dev-environment.yaml", output.DSEContent)
}
```

The AI response is expected to contain fenced code blocks with YAML.
The parser extracts blocks by looking for ` ```yaml ` markers and
determines which is the workflow vs. DSE by content inspection
(checking for `apiVersion: apps.example.com`).

---

## Kaniko build protocol — complete reference

### Signal files

All files live in `/builds/` (shared volume between runner and sidecar).

| File | Written by | Purpose |
|---|---|---|
| `<name>.tar.gz` | Runner | Build context tarball |
| `<name>.dest` | Runner | Target image reference |
| `<name>.dockerfile` | Runner | Dockerfile path (optional, defaults to `Dockerfile`) |
| `<name>.request` | Runner | Trigger — sidecar starts build on detection |
| `<name>.log` | Sidecar | Build output log |
| `<name>.exitcode` | Sidecar | Build exit code (`0` = success) |
| `<name>.done` | Sidecar | Completion signal — runner stops polling |

### Build agent sidecar loop

```bash
while true; do
  for req in /builds/*.request; do
    name="${req%.request}"

    context="/builds/${name}.tar.gz"
    dest=$(cat "/builds/${name}.dest")

    # Run Kaniko
    /kaniko/executor \
      --context "tar://${context}" \
      --destination "${dest}" \
      --cache=true \
      --cache-repo=registry:5000/cache \
      --insecure \
      --push-retry=3 \
      2>&1 | tee "/builds/${name}.log"

    echo $? > "/builds/${name}.exitcode"
    touch "/builds/${name}.done"
    rm "/builds/${name}.request"
  done
  sleep 2
done
```

### Kaniko pod spec

```yaml
containers:
  - name: build-agent
    image: bitnami/kubectl:latest
    command: ["/bin/sh", "-c", "<sidecar loop script>"]
    volumeMounts:
      - name: builds
        mountPath: /builds
      - name: kaniko
        mountPath: /kaniko
  - name: kaniko
    image: gcr.io/kaniko-project/executor:latest
    # Kaniko binary is shared via volume
    volumeMounts:
      - name: kaniko
        mountPath: /kaniko
```
