# sample-app

A tiny Go web server that demonstrates the full **kindling** developer loop —
push code, build on your laptop, deploy to your local Kind cluster, hit it on
localhost. Everything in this directory is designed to be **copied into your own
repo** as a starting point.

## What's inside

```
sample-app/
├── .github/workflows/
│   └── dev-deploy.yml       # GitHub Actions workflow (copy to your repo)
├── main.go                  # Go web server (Postgres + Redis)
├── Dockerfile               # Multi-stage Alpine build
├── dev-environment.yaml     # DevStagingEnvironment CR (manual apply)
├── go.mod / go.sum          # Go module
└── README.md                # ← you are here
```

| Endpoint | Description |
|---|---|
| `GET /` | Hello message |
| `GET /healthz` | Liveness / readiness probe |
| `GET /status` | Shows Postgres + Redis connectivity |

---

## Quick-start: Use this in your own project

### Prerequisites

Make sure you already have:

- A local Kind cluster running (`kind create cluster --name dev`)
- The **kindling** operator deployed in the cluster ([Getting Started](../../README.md#getting-started))
- A `GithubActionRunnerPool` CR applied with your GitHub username ([sample](../../config/samples/apps_v1alpha1_githubactionrunnerpool.yaml))
- The runner pod is registered and idle (`kubectl get pods`)

### Step 1 — Create a new GitHub repo

```bash
# Create a fresh repo (or use an existing one)
mkdir my-app && cd my-app
git init
```

### Step 2 — Copy the sample app files

```bash
# From the kindling repo root
cp -r examples/sample-app/* my-app/
cp -r examples/sample-app/.github my-app/
```

Your repo should now look like:

```
my-app/
├── .github/workflows/dev-deploy.yml
├── main.go
├── Dockerfile
├── dev-environment.yaml
├── go.mod
└── go.sum
```

### Step 3 — Configure your GitHub repo

1. **Create a GitHub PAT** with `repo` scope (Settings → Developer settings → Personal access tokens).

2. **Create the runner token Secret** on your Kind cluster (if you haven't already):

   ```bash
   kubectl create secret generic github-runner-token \
     --from-literal=github-token=ghp_YOUR_TOKEN_HERE
   ```

3. **Update the `GithubActionRunnerPool` CR** with your repo slug:

   ```yaml
   spec:
     githubUsername: "your-github-username"
     repository: "your-org/my-app"          # ← your new repo
   ```

   ```bash
   kubectl apply -f config/samples/apps_v1alpha1_githubactionrunnerpool.yaml
   ```

4. **Verify the runner is registered** — check the GitHub repo → Settings → Actions → Runners. You should see a runner with labels `[self-hosted, your-github-username]`.

### Step 4 — Customize the workflow (optional)

Open `.github/workflows/dev-deploy.yml` and tweak as needed:

- **`APP_NAME`** — change from `sample-app` to your app's name
- **`port`** — update if your app listens on a different port
- **`healthCheck.path`** — update if your health endpoint differs
- **`dependencies`** — add/remove services (postgres, redis, mysql, mongodb, rabbitmq, minio)

### Step 5 — Push and watch it deploy

```bash
cd my-app
git remote add origin git@github.com:your-org/my-app.git
git add -A
git commit -m "initial commit"
git push -u origin main
```

Now watch the magic:

1. GitHub receives the push and queues a workflow run
2. Your local self-hosted runner picks it up (`runs-on: [self-hosted, your-username]`)
3. The runner builds the Docker image using the host Docker socket
4. The runner applies a `DevStagingEnvironment` CR to your Kind cluster
5. The **kindling** operator reconciles: creates a Deployment, Service, Postgres, and Redis
6. Connection URLs (`DATABASE_URL`, `REDIS_URL`) are auto-injected into your app

### Step 6 — Verify

```bash
# Check everything came up
kubectl get devstagingenvironments
kubectl get pods

# Port-forward and hit the app
kubectl port-forward svc/<your-username>-dev 8080:8080
curl http://localhost:8080/healthz
curl http://localhost:8080/status | jq .
```

You should see both Postgres and Redis connected. 🎉

---

## Deploying manually (without a GitHub Actions push)

You can test the operator loop without pushing to GitHub:

```bash
# Build the image and load it into Kind
docker build -t sample-app:dev .
kind load docker-image sample-app:dev --name dev

# Apply the DevStagingEnvironment CR
kubectl apply -f dev-environment.yaml

# Wait for rollout, then port-forward
kubectl rollout status deployment/sample-app-dev --timeout=120s
kubectl port-forward svc/sample-app-dev 8080:8080

# Hit the endpoints
curl localhost:8080/healthz
curl localhost:8080/status
```

---

## Running locally (outside the operator)

```bash
go mod tidy
DATABASE_URL="postgres://devuser:devpass@localhost:5432/devdb?sslmode=disable" \
REDIS_URL="redis://localhost:6379/0" \
go run .
```
