<p align="center">
  <img src="https://img.shields.io/badge/Kubernetes-Operator-326CE5?logo=kubernetes&logoColor=white" alt="Kubernetes Operator" />
  <img src="https://img.shields.io/badge/Go-1.20-00ADD8?logo=go&logoColor=white" alt="Go 1.20" />
  <img src="https://img.shields.io/badge/kubebuilder-v4-blueviolet" alt="Kubebuilder v4" />
  <img src="https://img.shields.io/badge/License-MIT-green" alt="MIT" />
</p>

# kindling

**Push code. Your laptop builds it. Your laptop runs it. Zero cloud CI minutes.**

`kindling` is a Kubernetes operator that turns every developer's local [Kind](https://kind.sigs.k8s.io/) cluster into a personal, self-hosted GitHub Actions CI/CD environment. When you push to GitHub, the CI job is routed *back to your own machine*, where it builds the app container and deploys an ephemeral staging environment — all in seconds, all on localhost.

---

## Why?

Most teams pay for cloud CI runners that:

- 🐢 Queue behind other jobs
- 💸 Burn money on compute you already have under your desk
- 🔄 Build artifacts you then have to pull back down to test locally

**What if the runner was already on your laptop?**

`kindling` flips the model. Each developer runs a lightweight Kind cluster. Inside it, a self-hosted GitHub Actions runner polls for CI jobs triggered by *their* pushes. When a job arrives, the runner builds the container using the host Docker socket (the same daemon Kind itself uses), then the operator deploys the result as a full staging environment — Deployment, Service, and optional Ingress — right on localhost.

```
┌─────────────────────────────────────────────────────┐
│  Developer's Machine                                │
│                                                     │
│  ┌───────────────── Kind Cluster ─────────────────┐ │
│  │                                                 │ │
│  │  ┌─────────────────┐   ┌─────────────────────┐ │ │
│  │  │ kindling    │   │ GithubActionRunner  │ │ │
│  │  │ controller       │   │ Pool (runner pod    │ │ │
│  │  │                  │   │ + Docker socket)    │ │ │
│  │  └────────┬─────────┘   └──────────┬──────────┘ │ │
│  │           │  creates               │ polls GH   │ │
│  │           ▼                        │ builds app  │ │
│  │  ┌─────────────────┐              │             │ │
│  │  │ DevStagingEnv   │◄─────────────┘             │ │
│  │  │ (Deployment +   │  applies CR after build    │ │
│  │  │  Service + Ing) │                            │ │
│  │  └─────────────────┘                            │ │
│  └─────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
         ▲                          │
         │  git push                │ runs-on: [self-hosted, <username>]
         │                          ▼
    ┌────────────┐          ┌───────────────┐
    │  Developer │          │  GitHub.com   │
    │  (git)     │          │  Actions      │
    └────────────┘          └───────────────┘
```

---

## Custom Resources

The operator manages two CRDs in the `apps.example.com/v1alpha1` group:

### `GithubActionRunnerPool`

Declares a self-hosted runner pool bound to a **single developer** and a **single GitHub repository**. The operator creates a Deployment running the [official GitHub Actions runner image](https://github.com/actions/runner) with access to the host Docker socket (or an optional DinD sidecar), and registers it with GitHub using the developer's username as a runner label.

```yaml
apiVersion: apps.example.com/v1alpha1
kind: GithubActionRunnerPool
metadata:
  name: jeff-runner-pool
spec:
  githubUsername: "jeffvincent"              # routes jobs to this dev's cluster
  repository: "myorg/myrepo"                 # repo to poll for workflow runs
  tokenSecretRef:
    name: github-runner-token                # Secret with a GitHub PAT
  replicas: 1                                # one runner per developer
  dockerMode: socket                         # socket (default) | dind | none
  serviceAccountName: runner-deployer        # needs RBAC to create DevStagingEnvironments
  labels: [linux, x64]                       # extra runner labels
  resources:
    cpuRequest: "500m"
    memoryLimit: "4Gi"
```

Your GitHub Actions workflow routes jobs to a specific developer's machine:

```yaml
# In your app repo: .github/workflows/dev-deploy.yml
on: push

jobs:
  build-and-deploy:
    runs-on: [self-hosted, "${{ github.actor }}"]
    steps:
      - uses: actions/checkout@v4

      - name: Build container image
        run: docker build -t myapp:${{ github.sha }} .

      - name: Deploy to local Kind cluster
        run: |
          kubectl apply -f - <<EOF
          apiVersion: apps.example.com/v1alpha1
          kind: DevStagingEnvironment
          metadata:
            name: ${{ github.actor }}-dev
          spec:
            deployment:
              image: myapp:${{ github.sha }}
              port: 8080
            service:
              port: 8080
          EOF
```

<details>
<summary><strong>Full GithubActionRunnerPool spec reference</strong></summary>

| Field | Default | Description |
|---|---|---|
| `githubUsername` | *(required)* | Developer's GitHub handle — auto-added as a runner label |
| `repository` | *(required)* | GitHub repo slug (`org/repo`) |
| `tokenSecretRef` | *(required)* | Reference to a Secret holding a GitHub PAT |
| `replicas` | `1` | Number of runner pods |
| `runnerImage` | `ghcr.io/actions/actions-runner:latest` | Runner container image |
| `dockerMode` | `socket` | Docker strategy: `socket` (host mount, lightest), `dind` (sidecar), or `none` |
| `labels` | `[]` | Extra runner labels (`self-hosted` + username always added) |
| `runnerGroup` | `"Default"` | GitHub runner group |
| `resources` | `nil` | CPU/memory requests and limits |
| `serviceAccountName` | `""` | SA for the runner pod (needs DevStagingEnv RBAC) |
| `workDir` | `/runner/_work` | Runner working directory |
| `githubURL` | `https://github.com` | Override for GitHub Enterprise Server |
| `env` | `[]` | Extra environment variables |
| `volumeMounts` | `[]` | Additional volume mounts |
| `volumes` | `[]` | Additional volumes |

</details>

### `DevStagingEnvironment`

Declares a complete ephemeral staging environment: a **Deployment**, a **Service**, an optional **Ingress**, and zero or more **Dependencies** (databases, caches, queues). The operator auto-provisions each dependency and injects connection env vars (e.g. `DATABASE_URL`, `REDIS_URL`) into the app container — no manual wiring required.

Typically created by the CI workflow step *inside* the runner pod after building the container image.

```yaml
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: jeff-dev
spec:
  deployment:
    image: myapp:abc123
    replicas: 1
    port: 8080
    healthCheck:
      path: /healthz
  service:
    port: 8080
    type: ClusterIP
  ingress:
    enabled: true
    host: jeff-dev.localhost
  dependencies:
    - type: postgres
      version: "16"
    - type: redis
```

With the above, the operator creates:
- A **Deployment + Service** for PostgreSQL 16 — injects `DATABASE_URL=postgres://devuser:devpass@jeff-dev-postgres:5432/devdb?sslmode=disable`
- A **Deployment + Service** for Redis — injects `REDIS_URL=redis://jeff-dev-redis:6379/0`
- A **credentials Secret** per dependency
- All connection env vars merged into the app container automatically

#### Supported dependency types

| Type | Default Image | Default Port | Injected Env Var | Notes |
|---|---|---|---|---|
| `postgres` | `postgres:16` | 5432 | `DATABASE_URL` | Auto-creates `devdb` with user `devuser` |
| `redis` | `redis:latest` | 6379 | `REDIS_URL` | Stateless, no persistence |
| `mysql` | `mysql:latest` | 3306 | `DATABASE_URL` | Auto-creates `devdb` with user `devuser` |
| `mongodb` | `mongo:latest` | 27017 | `MONGO_URL` | Root user `devuser` |
| `rabbitmq` | `rabbitmq:3-management` | 5672 | `AMQP_URL` | Includes management UI |
| `minio` | `minio/minio:latest` | 9000 | `S3_ENDPOINT` | Also injects `S3_ACCESS_KEY` + `S3_SECRET_KEY` |

<details>
<summary><strong>Full DevStagingEnvironment spec reference</strong></summary>

| Field | Default | Description |
|---|---|---|
| `deployment.image` | *(required)* | Container image to run |
| `deployment.port` | *(required)* | Container port |
| `deployment.replicas` | `1` | Pod replica count |
| `deployment.command` | `[]` | Override container entrypoint |
| `deployment.args` | `[]` | Entrypoint arguments |
| `deployment.env` | `[]` | Environment variables |
| `deployment.resources` | `nil` | CPU/memory requests and limits |
| `deployment.healthCheck.path` | `/healthz` | HTTP health check path |
| `deployment.healthCheck.initialDelaySeconds` | `5` | Delay before first probe |
| `deployment.healthCheck.periodSeconds` | `10` | Probe interval |
| `service.port` | *(required)* | Service port |
| `service.targetPort` | container port | Target port on the pod |
| `service.type` | `ClusterIP` | `ClusterIP` / `NodePort` / `LoadBalancer` |
| `ingress.enabled` | `false` | Create an Ingress resource |
| `ingress.host` | `""` | Hostname for the Ingress rule |
| `ingress.path` | `/` | URL path prefix |
| `ingress.pathType` | `Prefix` | `Prefix` / `Exact` / `ImplementationSpecific` |
| `ingress.ingressClassName` | `nil` | Ingress class (e.g. `nginx`) |
| `ingress.tls` | `nil` | TLS termination config |
| `ingress.annotations` | `{}` | Extra Ingress annotations |
| `dependencies[].type` | *(required)* | `postgres`, `redis`, `mysql`, `mongodb`, `rabbitmq`, or `minio` |
| `dependencies[].version` | latest | Image tag (e.g. `"16"`, `"7.2"`) |
| `dependencies[].image` | per-type default | Override the container image entirely |
| `dependencies[].port` | per-type default | Override the service port |
| `dependencies[].env` | `[]` | Extra/override env vars for the dependency container |
| `dependencies[].envVarName` | per-type default | Override the injected env var name (e.g. `MY_DB_URL`) |
| `dependencies[].storageSize` | `1Gi` | PVC size for stateful dependencies |
| `dependencies[].resources` | `nil` | CPU/memory requests and limits |

</details>

---

## Getting Started

### Prerequisites

| Tool | Version |
|---|---|
| [Go](https://go.dev/dl/) | 1.20+ |
| [Kind](https://kind.sigs.k8s.io/) | 0.20+ |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.28+ |
| [Docker](https://docs.docker.com/get-docker/) | 24+ |

### 1. Create a local Kind cluster

```bash
kind create cluster --name dev
```

### 2. Install the CRDs

```bash
make install
```

### 3. Create the GitHub token Secret

Generate a [GitHub Personal Access Token](https://github.com/settings/tokens) with **`repo`** scope, then:

```bash
kubectl create secret generic github-runner-token \
  --from-literal=github-token=ghp_YOUR_TOKEN_HERE
```

### 4. Run the operator

Run locally against your Kind cluster:

```bash
make run
```

**Or** build and deploy into the cluster:

```bash
make docker-build IMG=kindling:latest
kind load docker-image kindling:latest --name dev
make deploy IMG=kindling:latest
```

### 5. Create your runner pool

Edit `config/samples/apps_v1alpha1_githubactionrunnerpool.yaml` with your GitHub username and repo, then:

```bash
kubectl apply -f config/samples/apps_v1alpha1_githubactionrunnerpool.yaml
```

Verify the runner is online:

```bash
kubectl get githubactionrunnerpools
```

```
NAME              USER          REPOSITORY     REPLICAS   READY   AGE
jeff-runner-pool  jeffvincent   myorg/myrepo   1          1       30s
```

The runner will appear on your repository's **Settings → Actions → Runners** page on GitHub.

### 6. Push code and watch it deploy

Push a commit to your repo. The GitHub Actions workflow routes the job to your local runner, which builds the container and applies a `DevStagingEnvironment` CR. The operator takes it from there:

```bash
kubectl get devstagingenvironments
```

```
NAME       IMAGE            REPLICAS   AVAILABLE   READY   AGE
jeff-dev   myapp:abc123     1          1           true    15s
```

Access your app:

```bash
kubectl port-forward svc/jeff-dev 8080:8080
# → http://localhost:8080
```

> **💡 Tip:** Instead of port-forwarding, you can set up an Ingress for a cleaner experience. The `DevStagingEnvironment` CRD supports Ingress out of the box — see the [sample CR](config/samples/apps_v1alpha1_devstagingenvironment.yaml) for an example with `ingress.enabled: true` and a custom hostname like `jeff-dev.localhost`. Pair it with an Ingress controller (e.g. [ingress-nginx](https://kind.sigs.k8s.io/docs/user/ingress/#ingress-nginx)) in your Kind cluster for automatic routing.

### Try the sample app (end-to-end)

The repo includes a ready-to-go [sample app](examples/sample-app/) with a GitHub Actions workflow, a Dockerfile, and a `DevStagingEnvironment` CR. Copy it into a new repo to see the full loop in action:

```bash
# 1. Create a new project and copy the sample app into it
mkdir my-app && cd my-app && git init
cp -r /path/to/kindling/examples/sample-app/* .
cp -r /path/to/kindling/examples/sample-app/.github .

# 2. Point your GithubActionRunnerPool at the new repo
#    (edit repository: "your-org/my-app" in the CR and re-apply)

# 3. Push
git remote add origin git@github.com:your-org/my-app.git
git add -A && git commit -m "initial commit" && git push -u origin main
```

Your local runner picks up the workflow, builds the image, applies the CR, and the operator provisions the Deployment + Postgres + Redis. Then:

```bash
kubectl port-forward svc/<your-username>-dev 8080:8080
curl http://localhost:8080/status | jq .
```

See the full walkthrough in the [sample app README](examples/sample-app/README.md).

---

## Project Layout

```
.
├── api/v1alpha1/                        # CRD type definitions
│   ├── devstagingenvironment_types.go   #   DevStagingEnvironment spec & status
│   ├── githubactionrunnerpool_types.go  #   GithubActionRunnerPool spec & status
│   ├── groupversion_info.go             #   apps.example.com/v1alpha1 registration
│   └── zz_generated.deepcopy.go         #   auto-generated DeepCopy methods
├── cmd/main.go                          # Operator entrypoint & controller wiring
├── examples/sample-app/                 # Reference app — copy into your own repo
│   ├── .github/workflows/dev-deploy.yml #   GH Actions workflow: build + deploy to Kind
│   ├── main.go                          #   Go web server (Postgres + Redis)
│   ├── Dockerfile                       #   Multi-stage build
│   ├── dev-environment.yaml             #   DevStagingEnvironment CR for manual apply
│   ├── go.mod                           #   Separate module
│   └── README.md                        #   Full clone → configure → push guide
├── internal/controller/
│   ├── devstagingenvironment_controller.go   # Reconciler → Deployment + Service + Ingress + Deps
│   └── githubactionrunnerpool_controller.go  # Reconciler → Runner Deployment + Docker
├── config/
│   ├── crd/bases/                       # Generated CRD manifests
│   ├── rbac/                            # ClusterRoles, bindings, service accounts
│   ├── manager/                         # Operator Deployment manifest
│   ├── samples/                         # Example CRs to get started
│   └── default/                         # Kustomize overlay for full deployment
├── Dockerfile                           # Multi-stage build for the operator image
├── Makefile                             # Build, test, generate, deploy targets
└── PROJECT                              # Kubebuilder project metadata
```

---

## How It All Fits Together

```
  git push ──► GitHub Actions ──► runs-on: [self-hosted, jeffvincent]
                                          │
                                          ▼
                            ┌──── Kind Cluster (laptop) ─────┐
                            │                                 │
                            │  Runner Pod    /var/run/docker.sock  │
                            │  ┌────────────┐  (host mount)  │
                            │  │ checkout   │                │
                            │  │ build      │◄── host Docker │
                            │  │ kubectl    │    daemon      │
                            │  │ apply CR   │                │
                            │  └─────┬──────┘                 │
                            │        │ creates                │
                            │        ▼                        │
                            │  DevStagingEnvironment CR       │
                            │        │                        │
                            │        │ operator reconciles    │
                            │        ▼                        │
                            │  ┌──────────────────────┐       │
                            │  │ Deployment (app)     │       │
                            │  │ Service              │       │
                            │  │ Ingress (optional)   │       │
                            │  └──────────────────────┘       │
                            │                                 │
                            │  localhost:8080 ◄── port-forward│
                            └─────────────────────────────────┘
```

1. **Developer creates a Kind cluster** on their laptop and deploys the operator + a `GithubActionRunnerPool` CR with their GitHub username.

2. **The operator spins up a runner Deployment** with the GitHub Actions runner image and mounts the host Docker socket (or a DinD sidecar, if configured). The runner self-registers with GitHub using labels `[self-hosted, <username>]`.

3. **Developer pushes code.** The GitHub Actions workflow fires with `runs-on: [self-hosted, <username>]`, routing the job to *that developer's laptop*.

4. **The runner executes the job**: checks out the code, builds a Docker image via the mounted Docker socket, and runs `kubectl apply` to create a `DevStagingEnvironment` CR.

5. **The operator reconciles the `DevStagingEnvironment`**: creates a Deployment running the freshly built image, a Service, and optionally an Ingress — all on the local Kind cluster.

6. **The developer hits localhost** via `kubectl port-forward` or an Ingress controller, seeing their changes running in a real Kubernetes environment instantly.

---

## Development

```bash
# Regenerate deepcopy methods and CRD/RBAC manifests after editing types
make generate manifests

# Run the full test suite
make test

# Format and vet
make fmt vet

# Build the operator binary
make build

# Build the Docker image
make docker-build IMG=kindling:dev
```

### Modifying the API

If you edit files under `api/v1alpha1/`, always regenerate:

```bash
make generate manifests
```

This updates `zz_generated.deepcopy.go`, the CRD YAML in `config/crd/bases/`, and RBAC roles in `config/rbac/`.

---

## Uninstalling

```bash
# Remove the operator and all managed resources
make undeploy

# Remove only the CRDs (and all CRs)
make uninstall

# Delete the Kind cluster entirely
kind delete cluster --name dev
```

---

## Roadmap

- [ ] Automatic TTL-based cleanup of stale `DevStagingEnvironment` CRs
- [ ] Live status integration — update `GithubActionRunnerPool.status.activeJob` via the GitHub API
- [x] Multi-service environments — deploy databases, caches, and queues alongside the app
- [ ] CLI bootstrap tool — one command to create cluster + install operator + register runner
- [ ] Webhook receiver for GitHub push events as an alternative to long-polling

---

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a PR.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes and run `make test`
4. Commit (`git commit -m 'Add amazing feature'`)
5. Push (`git push origin feature/amazing-feature`)
6. Open a Pull Request

---

## License

MIT License — Copyright 2024.

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

