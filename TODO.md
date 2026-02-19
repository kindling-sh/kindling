# Kindling — Roadmap

## Generate: Helm-native deploy

When scanning a repo, detect existing Helm charts (`Chart.yaml`). Default
behavior: apply the chart directly to the cluster via `helm install/upgrade` in
the deploy step of the generated workflow — this avoids env drift by using the
chart as the source of truth at deploy time.

Run `helm template` during scanning to extract env vars, ports, service names,
and dependencies, then pass that rendered context to the LLM so it can:

1. Populate build steps with template-derived env vars
2. Generate a deploy step that runs `helm upgrade --install` instead of raw
   `kubectl apply`

Add a `--no-helm` override flag to fall back to DSE-based generation even when a
chart is present.

Also detect kustomize configs — same pattern: `kustomize build` to extract
context, deploy via `kubectl apply -k` by default.

---

## CLI: kindling export (production-ready manifests from cluster state)

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

## CLI: kindling diagnose (error surfacing + LLM remediation)

Scan the cluster for common errors and misconfigurations, surface them in a
human-readable report, and optionally pass them to an LLM for suggested next
steps.

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

## Generate: smarter ingress heuristics

If no existing manifests are found, proceed with current AI-based generation but
improve heuristics for identifying which services should get ingress routes:

- **Frontends**: React, Next.js, Vue, Angular, static file servers
- **SSR frameworks**: Rails views, Django templates, PHP
- **API gateways**: services named `gateway`, `api-gateway`, `bff`, etc.

Only services identified as user-facing get ingress entries by default.

---

## Generate: --ingress-all flag

Add a `--ingress-all` flag (or similar) to `kindling generate` that wires up
every service with a default ingress route including health endpoints (e.g.
`/healthz`, `/actuator/health`). Without the flag, only heuristically-identified
user-facing services get routes. This gives users an easy override when the
heuristics miss something.

---

## ~~CLI: kindling secrets subcommand~~ ✅

Implemented `kindling secrets` with four subcommands:

- `kindling secrets set <NAME> <VALUE>` — creates K8s Secret + local backup
- `kindling secrets list` — shows managed secret names and keys
- `kindling secrets delete <NAME>` — removes from cluster + local backup
- `kindling secrets restore` — re-creates K8s Secrets from local backup after
  cluster rebuild

---

## Generate: detect external credentials

During `kindling generate` repo scanning, detect references to external
credentials — env vars matching patterns like `*_API_KEY`, `*_SECRET`,
`*_TOKEN`, `*_DSN`, `*_CONNECTION_STRING` in source code, Dockerfiles,
docker-compose, and `.env` files.

For each detected external secret:

1. Emit a `# TODO: run kindling secrets set <name>` comment in the generated
   workflow
2. In interactive mode, prompt the user to provide the value immediately
3. Wire the secret ref into the generated K8s manifests as a `secretKeyRef`

---

## ~~Config: .kindling/secrets.yaml~~ ✅

Implemented as part of `kindling secrets`. File is stored at
`.kindling/secrets.yaml` (auto-gitignored). Values are base64-encoded.
`kindling secrets restore` reads this file and re-creates K8s Secrets.

---

## TLS + public exposure for OAuth

Support TLS with a publicly accessible IP/hostname for local dev environments so
external identity providers (Auth0, Okta, Firebase Auth, etc.) can call back into
the cluster.

1. `kindling expose` sets up a tunnel (cloudflared, ngrok, or similar) from a
   public HTTPS URL to the Kind cluster's ingress — the tunnel provider handles
   TLS termination
2. For direct IP exposure, deploy cert-manager with Let's Encrypt into the
   cluster
3. The generated workflow and DSE spec accept an optional `publicHost` field so
   ingress rules use the real hostname instead of `*.localhost`
4. `kindling generate` detects OAuth callback URLs, Auth0 config, OIDC discovery
   endpoints in source code and flags that TLS/public exposure is required,
   prompting the user to run `kindling expose`

---

## Expose: stable callback URL (tunnel URL relay)

Every time `kindling expose` connects, the tunnel gets a new random URL
(e.g. `https://abc123.trycloudflare.com`). External services that require a
callback URL (OAuth providers, payment webhooks, Slack bots, etc.) break
because the registered URL no longer matches. Updating the callback in every
external dashboard on each reconnect is a pain.

Provide a stable intermediate URL that stays the same on the developer's
machine and automatically relays to whatever the current tunnel URL is.

### Approach: lightweight redirect service

1. On first `kindling expose`, provision a stable hostname — either:
   - **Self-hosted relay**: a tiny, free-tier-friendly redirect service
     (Cloudflare Worker, Vercel edge function, or a shared kindling relay
     at `<username>.relay.kindling.dev`) that stores the current tunnel URL
     and 307-redirects all requests to it
   - **Local DNS alias**: for simpler setups, a local `/etc/hosts` entry +
     a small in-cluster nginx that proxies to the tunnel URL — works for
     services that call back on the local network
   - **Custom domain with tunnel provider**: if the user has a domain,
     configure cloudflared named tunnel or ngrok custom domain so the URL
     is always the same (requires paid tier — document as the "just works"
     option)

2. When `kindling expose` reconnects with a new tunnel URL, it automatically
   pushes the new URL to the relay — the stable hostname never changes

3. Store the stable URL in a local config (`~/.kindling/relay.yaml`) so it
   persists across sessions. Print it prominently:
   ```
   ✅ Tunnel active
      Tunnel URL:  https://abc123.trycloudflare.com
      Stable URL:  https://jeff.relay.kindling.dev  ← use this for callbacks
   ```

### Relay update flow

```
kindling expose
  → starts tunnel → gets random URL
  → PUT https://relay.kindling.dev/api/update { url: "<tunnel-url>" }
  → relay stores mapping: jeff → <tunnel-url>

External service calls https://jeff.relay.kindling.dev/auth/callback
  → relay looks up jeff → 307 redirect to https://abc123.trycloudflare.com/auth/callback
```

### Flags

- `--relay` — enable the stable relay URL (first time: provisions hostname)
- `--relay-domain <host>` — use a custom domain instead of the shared relay
- `--no-relay` — disable relay, use raw tunnel URL only

### Considerations

- **Security**: relay should verify ownership (simple API key stored in
  `~/.kindling/relay.yaml`) so nobody can hijack your hostname
- **Latency**: 307 redirect adds one round-trip; alternatively the relay
  can reverse-proxy instead of redirect (slightly more infra but invisible
  to the external service)
- **POST callbacks**: OAuth and webhooks use POST — 307 preserves method,
  but some clients don't follow redirects on POST. Reverse-proxy mode
  avoids this entirely
- **Free tier sustainability**: a Cloudflare Worker handles this trivially
  within free tier limits for individual devs

---

## CLI: kindling add view (ingress path routing)

When a tunnel is active (`kindling expose`), the patched ingress typically only
routes the base path (`/`) to the selected service. If a developer adds a new
view or API endpoint and pushes, traffic to that path may 404 because the
ingress has no matching rule for it.

`kindling add view` lets you add path-based routing rules to the active ingress
without editing YAML or redeploying:

```
kindling add view /api --service orders --port 8080
kindling add view /admin
kindling add view /docs --service gateway
```

### Behavior

1. Finds the ingress currently patched by the tunnel (look for
   `kindling.dev/original-host` annotation) — or accepts `--ingress <name>`
   explicitly
2. Adds a new `paths` entry under the matching host rule with the given path,
   pathType `Prefix`, and backend service/port
3. If `--service` and `--port` are omitted, reuses the existing backend from the
   base `/` rule (most single-service apps only need the path)
4. If the tunnel is running, the new path is immediately reachable at the public
   URL (e.g. `https://<tunnel-host>/api`)
5. Works without a tunnel too — adds the path to any ingress in the namespace

### Flags

- `--service` — backend service name (default: same as existing `/` rule)
- `--port` — backend service port (default: same as existing `/` rule)
- `--ingress` — target a specific ingress by name
- `--namespace` / `-n` — namespace (default: `default`)
- `--path-type` — `Prefix` (default) or `Exact`

### Related

- `kindling add view --list` — show all paths on the active ingress
- `kindling add view --remove /api` — remove a previously added path rule

---

## Multi-platform CI support (break vendor lock-in)

Kindling is currently GitHub-only (Actions runners, GitHub PATs, GitHub-specific
composite actions). Expand to support other Git platforms and CI systems so teams
aren't locked into a single vendor.

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

---

## OSS infrastructure (deprioritized)

Low priority — do when there's actual community interest:

- `CONTRIBUTING.md` with dev setup, test instructions, PR expectations, DCO signoff
- `CODE_OF_CONDUCT.md` (Contributor Covenant v2.1)
- Issue & PR templates (`.github/ISSUE_TEMPLATE/`, PR template)
- Dynamic README badges (CI status, release, Go Report Card, coverage)
- Homebrew tap (`brews:` section in `.goreleaser.yml`)
- MkDocs Material docs site + GitHub Pages deploy workflow
