# Kindling — Roadmap

Prioritized for mass adoption. The ordering is: harden what people will
touch first → remove friction → reach new audiences → deepen the product.

---

## P0 — Remove every barrier to trying kindling

These are the things that stop someone from going from "that looks cool" to
"I have it running" in under 3 minutes.

### Homebrew formula

`brew install kindling` — single command, no curl gymnastics. This is table
stakes for any CLI tool targeting macOS developers. File a Homebrew core
formula PR or host a tap (`kindling-sh/homebrew-tap`).

```bash
brew tap kindling-sh/tap
brew install kindling
```

### One-liner install script

For Linux and CI environments:

```bash
curl -sL https://kindling.dev/install | sh
```

Detect OS + arch, download the right binary from GitHub Releases, drop it in
`/usr/local/bin`. Should also work in Dockerfiles and GitHub Actions runners.

### 3-minute quickstart guarantee

Time the quickstart end-to-end. If it takes longer than 3 minutes, cut steps.
Put the time in the README: "From zero to a deployed app in under 3 minutes."

- Pre-bake more defaults so fewer flags are required
- Detect GitHub remote from `.git/config` to skip `--repo` flag
- Auto-detect GitHub username from `gh auth status` or git config

### README hero demo

Add a screen-recording GIF or hosted demo to the README so people can see
kindling working before they commit to installing it. First impression matters
more than anything else on a GitHub repo page.

### Harden `kindling generate` (wild-repo fuzz testing)

`kindling generate` is the first thing a new user will run on their own repo.
If it crashes, spits out invalid YAML, or silently produces garbage, that's
the last time they use kindling. This has to be solid *before* Show HN.

Clone a large corpus of real-world repos, run `kindling generate` against each
one, and record structured results to surface failure modes.

**Per-repo result record:**

| Field | Description |
|---|---|
| `repo` | GitHub URL |
| `language` | Primary language (from GitHub API) |
| `size_kb` | Repo size |
| `has_dockerfile` | Whether a Dockerfile exists |
| `services_detected` | Number of services `generate` found |
| `exit_code` | `kindling generate` exit code |
| `dse_valid` | Whether a valid `dev-environment.yaml` was produced |
| `workflow_valid` | Whether the generated workflow YAML parses |
| `failure_category` | `no_dockerfile`, `env_parse_error`, `unsupported_lang`, `crash`, `timeout`, etc. |

**Repo selection strategy:**
- GitHub trending repos across top 10–15 languages
- Repos with a `Dockerfile` (most relevant)
- Repos with `docker-compose.yml` (multi-service)
- Monorepos with multiple services in subdirectories
- Long-tail languages (should never crash, even if generate can't help)

**Quality gates (must hit before going public):**
- ≤15% crash rate — every failure is a clean error message, never a panic
- ≥80% success rate on repos that already have a Dockerfile
- Top 10 failure modes identified and fixed

### Interactive ingress selection in `kindling generate`

During `kindling generate`, after discovering all services in a multi-service
repo, prompt the user to select which services should get ingress routes instead
of trying to auto-detect user-facing services. Present a checklist of discovered
services and let the developer pick.

For non-interactive / CI usage, add a `--ingress-all` flag that wires up every
service with a default ingress route.

---

## P1 — Content & visibility (get in front of developers)

The tool can be perfect and nobody will use it if they don't know it exists.
Content is the growth engine.

### Show HN

Submit a "Show HN" post. Polish the README and demo first. This is a
one-shot — make it count. Best posted Tuesday–Thursday, 8–10am ET.

### Tutorial: "How to run GitHub Actions locally on Kubernetes"

Targets high-traffic search queries. Naturally leads to kindling as the
solution. Optimize for SEO — this is the kind of thing people Google when
they're frustrated with cloud CI wait times.

### Tutorial: "Local Kubernetes CI/CD with Kind"

Similar SEO play, different search intent. Cross-post both tutorials to
Dev.to, Hashnode, and Medium.

### YouTube walkthrough

Record a video: `git clone` → working deploy in under 5 minutes. Cut
short-form clips for Twitter/LinkedIn. Developer tools live or die by
whether people can *see* them working.

### Blog posts

Each post has a "the hard way → the kindling way" arc or is a hands-on
tutorial. Publish on the docs site blog, cross-post to Dev.to / Hashnode /
Medium.

**Getting Started / Onboarding:**

- [ ] "Zero to Staging in 5 Minutes: Your First kindling Environment"
  — The canonical quickstart walkthrough: `init` → `runners` → `generate` → `git push` → app on localhost.
- [ ] "Stop Paying for CI You Already Own"
  — Cloud CI costs, queue times, artifact round-trips. Real billing comparison, then demo the self-hosted runner model.
- [ ] "I Replaced My docker-compose Dev Stack with a Kubernetes Operator"
  — Migrate a typical `docker-compose up` workflow to kindling. What you gain (CI integration, dependency auto-provisioning, ingress routing) and what stays the same.

**Language / Framework Tutorials:**

- [ ] "Deploy a FastAPI + Postgres App to Your Laptop with One Git Push"
  — Python tutorial: `kindling generate` detects `requirements.txt` + compose, auto-provisions Postgres, injects `DATABASE_URL`.
- [ ] "Next.js + Redis on Localhost Kubernetes — No Cloud Required"
  — Node tutorial: SSR app with Redis caching. `generate` detects frontend for ingress, wires Redis, handles multi-stage Dockerfile.
- [ ] "From Rails Monolith to Local Kubernetes in 10 Minutes"
  — Ruby tutorial: Rails + Postgres + Redis (Sidekiq). Highlight `.env.example` scanning and credential detection.
- [ ] "Go Microservices the Easy Way: 4 Services, 3 Databases, Zero YAML by Hand"
  — Use `examples/microservices/`. Show `generate` producing the full workflow for Gateway + Orders + Inventory + UI.
- [ ] "Deploying a Rust Web Service with HEALTHCHECK and Multi-Stage Builds"
  — Rust Actix/Axum app. Highlight Kaniko handling multi-stage builds and long compile times with build timeout guidance.

**Feature Deep Dives:**

- [ ] "How kindling generate Actually Works: AI Meets Repo Scanning"
  — Under-the-hood walkthrough of the 8-stage pipeline: repo scan → compose analysis → Helm render → .env scan → credential detection → OAuth detection → prompt assembly → AI call.
- [ ] "15 Dependencies, Zero Configuration: Auto-Provisioning from Postgres to Jaeger"
  — Tour all 15 dependency types. Single DSE YAML provisioning Postgres, Redis, Kafka, Elasticsearch, and Vault with auto-injected connection env vars.
- [ ] "Managing Secrets in Local Kubernetes Without Losing Your Mind"
  — Tutorial: `secrets set` → local backup → `destroy` → `init` → `secrets restore`. How `secretKeyRef` wiring works in generated workflows.
- [ ] "OAuth on Localhost: Testing Auth0 Callbacks Without Deploying to the Cloud"
  — Tutorial: `kindling expose` with cloudflared, configure Auth0 callback URL, push code, test the full OAuth flow locally. TLS-aware ingress patching.
- [ ] "The Build-Agent Sidecar: How kindling Builds Containers Without Docker"
  — Deep dive into the signal-file protocol, Kaniko one-shot pods, and the `/builds/` volume. Why this architecture keeps the runner container stock and unprivileged.

**Real-World Scenarios:**

- [ ] "Testing Stripe Webhooks Locally with kindling expose"
  — Stripe needs a public URL for webhooks. `kindling secrets set STRIPE_KEY` + `kindling expose` → public tunnel → webhook hits localhost pod.
- [ ] "Local Staging for a Multi-Service E-Commerce App"
  — End-to-end: clone a real compose-based app, run `kindling generate`, push, see the full stack on localhost with ingress routing.
- [ ] "Debugging CI Failures Faster When the Runner Is on Your Desk"
  — The feedback loop: push → build fails → `kindling logs` → fix → push again. Compare to waiting 8 minutes for a cloud runner re-queue.
- [ ] "One Cluster, Multiple Repos: Using kindling reset to Switch Projects"
  — Tutorial: `runners` for repo A → work → `reset` → `runners` for repo B. Cluster stays warm, just the runner re-points.
- [ ] "Live Environment Variable Updates Without Redeploying"
  — Tutorial: `kindling env set` / `list` / `unset` to hot-swap config on a running deployment. Feature flag toggling during development.

**Ops / Architecture:**

- [ ] "Why We Chose Kubebuilder: Building a Kubernetes Operator for Dev Environments"
  — CRD design, reconcile loops, OwnerReferences for garbage collection, spec-hash annotations to avoid unnecessary writes.
- [ ] "Kaniko Layer Caching on localhost: How kindling Makes Rebuilds Fast"
  — `registry:5000/cache`, first-build vs rebuild times, tuning Docker Desktop resources for different stack sizes.
- [ ] "Helm Charts Meet AI: How kindling Renders Your Chart Before Generating a Workflow"
  — How `kindling generate --model o3` uses `helm template` output as authoritative context for ports and env vars.
- [ ] "From docker-compose.yml to Kubernetes — What the AI Actually Sees"
  — How `build.context`, `depends_on`, and `environment` blocks get mapped to `kindling-build` inputs, dependency types, and env var overrides.

**Hot Takes / Opinion:**

- [ ] "Your Laptop Is the Best CI Runner You're Not Using"
  — Economics and DX of local-first CI. Apple Silicon benchmarks vs cloud runners. When cloud CI still makes sense.
- [ ] "Stop Writing GitHub Actions YAML by Hand"
  — 3 real repos, run `kindling generate` on each, compare AI-generated workflow to what a human would write.
- [ ] "The Case for Ephemeral Staging Environments That Don't Cost Anything"
  — Compare kindling's local staging to Vercel previews, Railway, Render PR environments. Trade-offs: cost ($0) vs collaboration (single-developer).

### Community presence

- Answer questions on r/kubernetes, r/devops, r/selfhosted — mention
  kindling when genuinely relevant (not spam, actually help people)
- Join CNCF Slack and Kubernetes Slack (`#kind`, `#local-dev`) and be useful
- Submit CFP to DevOpsDays, KubeCon ("Zero-to-deploy local K8s CI/CD in 5
  minutes"), SeaGL, CNCF community group virtual meetups

---

## P2 — More example apps (every framework = a new audience)

Each example app gives a different language community a reason to discover
kindling. A Rails developer won't try kindling until they see a Rails example.
A Spring Boot developer won't try it until they see a Java example.

- [ ] **Rails** example app (Ruby ecosystem — huge community, lots of Docker adoption)
- [ ] **Django** example app (Python ecosystem — massive, underserved by local K8s tools)
- [ ] **Spring Boot** example app (Java ecosystem — enterprise developers)
- [ ] **Next.js** example app (React/Node ecosystem — biggest frontend framework)
- [ ] **Laravel** example app (PHP ecosystem — still enormous)
- [ ] **FastAPI** example app (Python — growing fast, modern audience)

Each example should be:
1. Realistic (not a hello-world — use a database, have a real UI)
2. Self-contained (copy the directory, push, done)
3. Documented with its own README

---

## P3 — CLI: kindling diagnose (make Kubernetes less scary)

This is the adoption unlock for developers who aren't Kubernetes experts.
Most people who try local K8s hit a wall of cryptic errors and give up.
`kindling diagnose` catches them before they quit.

```
kindling diagnose
kindling diagnose --fix
```

### Error detection

Walk all user-namespace resources and collect:

- **RBAC issues** — pods failing with `Forbidden`, `Unauthorized`; missing
  RoleBindings, ClusterRoleBindings
- **Image pull errors** — `ErrImagePull`, `ImagePullBackOff` (wrong tag,
  missing registry creds, private repo without `imagePullSecrets`)
- **CrashLoopBackOff** — repeated restarts with exit codes; pull last N log
  lines for context (extends what `kindling status` already does)
- **Pending pods** — unschedulable due to resource limits, node affinity,
  taint/toleration mismatches
- **Service mismatches** — Service selector doesn't match any pod labels,
  or targetPort doesn't match container port
- **Ingress routing gaps** — ingress backend references a Service that
  doesn't exist or has no ready endpoints
- **ConfigMap/Secret missing refs** — pod env or volume references a
  ConfigMap or Secret that doesn't exist
- **Resource quota / LimitRange violations**
- **Probe failures** — liveness/readiness probes failing (from pod events)

### Output

Plain-text report grouped by severity:

```
❌ ERRORS
  deployment/orders — CrashLoopBackOff (exit 1)
    last log: "error: DATABASE_URL not set"

  pod/search-abc123 — ImagePullBackOff
    image: kindling/search-service:latest — not found in local registry

⚠️  WARNINGS
  service/gateway — targetPort 3000 doesn't match any container port (found: 8080)

  ingress/app — backend "ui-service" has 0 ready endpoints
```

### LLM integration (`--fix`)

When `--fix` is passed, send the collected errors + relevant resource YAML
to an LLM and print suggested remediation steps:

- Concrete `kubectl` or `kindling` commands to fix each issue
- YAML patches for misconfigured resources
- Explanations of *why* the error occurred (helpful for learning K8s)

Use the same LLM provider already configured for `kindling generate` (OpenAI /
Anthropic / local). Keep the LLM call optional — `kindling diagnose` without
`--fix` is fully offline and instant.

### Flags

- `--fix` — pass errors to LLM for remediation suggestions
- `--namespace` / `-n` — scope to a namespace (default: `default`)
- `--json` — output as JSON (for CI integration)
- `--watch` — re-run every N seconds until errors clear

---

## P4 — Strategic integrations (meet developers where they are)

### VS Code extension

Wraps the CLI with a native VS Code experience: status panel, deploy button,
logs view, tunnel control. VS Code has 70%+ market share — being in the
marketplace puts kindling in front of every developer browsing for K8s tools.

### Devcontainer config

Ship a `.devcontainer/` config so people can try kindling in Gitpod or
GitHub Codespaces with zero local setup. Removes Docker/Kind/kubectl as
prerequisites entirely for the first experience.

### GitHub Marketplace

Publish `kindling-build` and `kindling-deploy` as verified GitHub Marketplace
actions. Discoverability in the marketplace is free distribution.

---

## P5 — Multi-platform CI support (break vendor lock-in)

Kindling is currently GitHub-only (Actions runners, GitHub PATs, GitHub-specific
composite actions). Expanding to other platforms unlocks the majority of
developers who aren't on GitHub Actions.

### Git platforms

- **GitLab** — support GitLab repos, GitLab runner registration, and
  `.gitlab-ci.yml` generation via `kindling generate`
- **Bitbucket** — Bitbucket Pipelines runner registration and
  `bitbucket-pipelines.yml` generation
- **Gitea / Forgejo** — self-hosted Git; register Gitea Actions runners (Gitea
  Actions is Act-compatible, so much of the GitHub Actions plumbing carries over)

### CI systems

- **GitLab CI** — generate `.gitlab-ci.yml` with Kaniko build + kubectl deploy
  stages; register a GitLab Runner in the Kind cluster
- **CircleCI** — generate `.circleci/config.yml`; self-hosted runner support
- **Jenkins** — generate `Jenkinsfile`; deploy a Jenkins agent pod in-cluster
- **Drone / Woodpecker** — lightweight self-hosted CI; generate `.drone.yml` /
  `.woodpecker.yml`

### Implementation approach

1. Abstract the runner pool CRD — add a `spec.platform` field
   (`github | gitlab | gitea | ...`) so the operator provisions the correct
   runner type
2. `kindling runners --platform gitlab` creates a GitLab Runner registration
   instead of a GitHub Actions runner
3. `kindling generate` detects the remote origin to infer the platform, or
   accepts `--platform` explicitly
4. Factor composite actions into platform-agnostic build/deploy steps that emit
   the right CI config format per platform
5. Keep GitHub as the default — zero breaking changes for existing users

### Provider abstractions (code layer)

Before adding any new platform, introduce Go interfaces that decouple
the core logic from GitHub and Kind specifically. This is the prerequisite
engineering work that makes P5 features possible without shotgun surgery.

**CI provider interface** (`core/providers/ci.go`):

```go
type CIProvider interface {
    Name() string                                    // "github", "gitlab", etc.
    CreateRunnerPool(cfg RunnerPoolConfig) error      // provision runner in cluster
    DeleteRunnerPool(name string) error
    GenerateWorkflow(ctx GenerateContext) ([]byte, error) // emit platform-native CI config
    DetectFromRemote(remoteURL string) bool           // "is this repo on my platform?"
}
```

**Cluster provider interface** (`core/providers/cluster.go`):

```go
type ClusterProvider interface {
    Name() string                                     // "kind", "k3d", "minikube"
    Create(cfg ClusterConfig) error
    Destroy(name string) error
    LoadImage(image, cluster string) error             // provider-specific image loading
    GetKubeconfig(cluster string) (string, error)
    RegistryEndpoint() string                          // in-cluster registry address
}
```

**Migration path:**
1. Define the interfaces
2. Implement `GitHubProvider` and `KindProvider` as the defaults — wrapping
   exactly the logic that exists today in `core/` and the operator
3. Wire them through a `ProviderRegistry` so CLI commands and the operator
   resolve the active provider at startup
4. Second providers (GitLab, k3d) validate the abstraction
5. Existing behavior is unchanged — `kindling init` still means Kind,
   `kindling runners` still means GitHub, unless `--platform` / `--cluster-provider`
   is passed

---

## P6 — CLI: kindling export (production-ready manifests)

Generate a Helm chart or Kustomize overlay from the live cluster that gives
teams a working (or near-working) foundation for deploying to a real environment.
The key insight: by the time a developer has iterated in kindling, the cluster
already contains battle-tested Deployments, Services, Ingresses, ConfigMaps,
Secrets, etc. — export snapshots those into portable, production-grade manifests.

```
kindling export helm   [--output ./chart]
kindling export kustomize [--output ./k8s]
```

### What gets exported

Every user-created resource in the target namespace(s), converted to clean
K8s primitives:

- Deployments (with image tags, resource requests/limits, env vars, probes)
- Services (ClusterIP, NodePort mapping → LoadBalancer/ClusterIP for prod)
- Ingress — only the actively referenced ingress (the one currently routing
  traffic to exported services), not every ingress in the namespace
  (host/path rules, TLS stubs for cert-manager)
- ConfigMaps and Secrets (secret values redacted with `# TODO: set me`
  placeholders)
- PersistentVolumeClaims
- ServiceAccounts, Roles, RoleBindings (if present)
- HorizontalPodAutoscalers, NetworkPolicies, CronJobs

### What gets filtered out

Everything kindling-specific or Kind-specific that doesn't belong in
production:

- `DevStagingEnvironment` and `GithubActionRunnerPool` CRs
- The kindling operator Deployment, ServiceAccount, RBAC
- Runner pods and runner-related Secrets (PAT token, etc.)
- `kindling-tunnel` ConfigMap and tunnel annotations
  (`kindling.dev/original-host`, `kindling.dev/original-tls`)
- Kind-specific resources (local-path-provisioner, kindnet, etc.)
- `kube-system` and `local-path-storage` namespaces entirely
- Admission webhooks added by kindling
- Managed-by labels/annotations that reference kindling

### Helm output (`kindling export helm`)

Generates a valid `Chart.yaml` + `templates/` directory:

1. Each resource becomes a template file (`deployment-orders.yaml`, etc.)
2. Key values are parameterized into `values.yaml` — image tags, replica
   counts, resource limits, ingress hosts, env var values
3. Secret values become `{{ .Values.secrets.<name> }}` refs so they can be
   supplied at install time
4. Adds standard Helm labels (`app.kubernetes.io/managed-by: Helm`, chart
   version, etc.)
5. NodePort services are converted to ClusterIP (prod typically uses a real
   LB or ingress controller)

### Kustomize output (`kindling export kustomize`)

Generates a `kustomization.yaml` + `base/` resource files:

1. Raw resource YAML in `base/`
2. `kustomization.yaml` with `resources:` listing
3. Placeholder patches in `overlays/production/` for values that need to
   change per environment (image tags, replicas, ingress hosts)

### Cleanup / normalization

- Strip `status`, `metadata.resourceVersion`, `metadata.uid`,
  `metadata.creationTimestamp`, `metadata.generation`,
  `metadata.managedFields`, `kubectl.kubernetes.io/last-applied-configuration`
- Strip cluster-assigned `spec.clusterIP` from Services
- Normalize `metadata.namespace` (parameterize or omit so it's set at
  deploy time)
- Replace `localhost`-based ingress hosts with `# TODO: set production host`
- Add resource requests/limits if missing (with sensible defaults or comments)

### Flags

- `--output` / `-o` — output directory (default: `./kindling-export/`)
- `--namespace` / `-n` — namespace to export (default: `default`)
- `--all-namespaces` — export all non-system namespaces
- `--include-secrets` — include Secret values in plaintext (off by default)
- `--dry-run` — print what would be exported without writing files

---

## P7 — Expose improvements

### Stable callback URL (tunnel URL relay)

Every time `kindling expose` connects, the tunnel gets a new random URL.
External services that require a callback URL (OAuth, webhooks, Slack) break
because the registered URL no longer matches.

Provide a stable intermediate URL that stays the same and automatically
relays to whatever the current tunnel URL is.

**Approach: lightweight redirect service**

1. On first `kindling expose`, provision a stable hostname — either:
   - **Self-hosted relay**: a tiny Cloudflare Worker or Vercel edge function
     at `<username>.relay.kindling.dev` that stores the current tunnel URL
     and 307-redirects all requests to it
   - **Custom domain with tunnel provider**: configure cloudflared named
     tunnel or ngrok custom domain so the URL is always the same (requires
     paid tier — document as the "just works" option)

2. When `kindling expose` reconnects with a new tunnel URL, it automatically
   pushes the new URL to the relay — the stable hostname never changes

3. Store the stable URL in a local config (`~/.kindling/relay.yaml`) so it
   persists across sessions. Print it prominently:
   ```
   ✅ Tunnel active
      Tunnel URL:  https://abc123.trycloudflare.com
      Stable URL:  https://jeff.relay.kindling.dev  ← use this for callbacks
   ```

**Flags:**
- `--relay` — enable the stable relay URL (first time: provisions hostname)
- `--relay-domain <host>` — use a custom domain instead of the shared relay
- `--no-relay` — disable relay, use raw tunnel URL only

### Live service switching

Allow re-targeting the tunnel to a different service while it stays up:

```
kindling expose --service orders       # initial — starts tunnel, routes to orders
kindling expose --service gateway      # re-patch ingress, tunnel stays
```

If a tunnel is already running (pid file exists, process alive), skip starting
a new tunnel — just re-patch the ingress host/rules.

### Ingress path routing (`kindling add view`)

Add path-based routing rules to the active ingress without editing YAML:

```
kindling add view /api --service orders --port 8080
kindling add view /admin
kindling add view --list
kindling add view --remove /api
```

---

## P8 — Education angle

- [ ] Reach out to university CS / DevOps programs about using kindling in
  coursework (Southern Oregon University, Rogue Community College, etc.)
- [ ] Contact bootcamps (online and local) about adopting kindling for labs
- [ ] Create a "kindling 101" curriculum / workshop materials that instructors
  can pick up and run with
- [ ] Pitch to KubeAcademy / Linux Foundation training as a practical lab tool

---

## P9 — Contributor experience & OSS infrastructure

### Contributor experience

- [ ] Add `good-first-issue` labels on GitHub for approachable tasks
- [ ] `CONTRIBUTING.md` with dev setup, test instructions, PR expectations
- [ ] Shout out contributors in release notes

### OSS infrastructure (do when there's community interest)

- `CODE_OF_CONDUCT.md` (Contributor Covenant v2.1)
- Issue & PR templates (`.github/ISSUE_TEMPLATE/`, PR template)
- Dynamic README badges (CI status, release, Go Report Card, coverage)
- MkDocs Material docs site + GitHub Pages deploy workflow
