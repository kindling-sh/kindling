# Find Repos — Copilot Instructions

This file tells you how to discover, validate, and curate new repos
for the kindling fuzz test suite. Use `find-repos.py` for automated
discovery, then manually validate the best candidates.

---

## What makes a good fuzz repo

A repo is a good candidate for `kindling generate` fuzz testing if it has:

1. **Self-contained Dockerfile(s)** — builds from a fresh `git clone`
   with just `docker build`. No pre-build steps required outside the
   Dockerfile. (Monorepos that run `turbo prune` *inside* the Dockerfile
   are fine — the key is that `docker build` works standalone.)
2. **Multiple Dockerfiles in subdirectories** — each service in its own
   dir with its own Dockerfile. `kindling generate` scans the repo tree;
   docker-compose is just a signal, not a requirement.
3. **Real application** — not a framework, library, or tutorial scaffold.
4. **Recent activity** — pushed within the last year, not archived.
5. **Reasonable size** — not a massive monorepo (< 500MB).
6. **Backing dependencies** — uses postgres, redis, mongodb, etc.
   (exercises the dependency detection in the generate prompt).
7. **Multiple services** (bonus) — tests multi-service workflow generation.

### Stretch repos

Some repos are buildable but exercise pipeline features we don't handle
yet. These are *valuable* — they surface bugs with clear fix paths.
Look for repos with:

- `ARG` before `FROM` (parameterised base images)
- `HEALTHCHECK` instructions (should map to K8s readinessProbe)
- `RUN --mount=type=cache/secret` (BuildKit features Kaniko can't do)
- `COPY --link` or `COPY --chmod` (BuildKit flags to strip)
- Compose `build.args` / `build.target` (not passed to Kaniko)
- Compose `env_file` (not loaded by pipeline)
- Compose `healthcheck` (not mapped to K8s probes)

## What to avoid

| Pattern | Why |
|---------|-----|
| Monorepos where Dockerfiles COPY build artifacts | `COPY dist/ .` means a pre-build step is required outside Docker |
| Example/tutorial repos with no Dockerfiles | LLM hallucinates Dockerfiles in subdirs |
| Repos with only `docker-compose.yml` (no Dockerfiles) | Nothing to build — just pulls images |
| Repos requiring private base images | Build fails in CI without auth |
| Repos > 500MB | Clone + build too slow for fuzz budget |
| Framework repos (express, django, flask itself) | Not real apps — weird structure |

**Note:** Monorepos with Turborepo/pnpm are fine IF the Dockerfiles are
self-contained (e.g. `turbo prune --docker` inside the Dockerfile).
The anti-pattern is `COPY dist/ .` or `COPY build/ .` — that means
something outside Docker must run first.

---

## Repo lists

| File | Purpose | Used by |
|------|---------|---------|
| `repos.txt` | Curated list for static fuzz testing | `fuzz.yml` → Fuzz (static/curated) job |
| `repos-e2e.txt` | Small list for full e2e (Kind cluster) | `fuzz.yml` → Fuzz (e2e) job |
| `repos-candidates.txt` | Auto-discovered candidates (not committed) | Manual review only |

### Size targets

- `repos.txt`: 5–30 repos. Broad coverage across languages and patterns.
  Static-only, so more repos is fine (each takes ~15s to generate + validate).
- `repos-e2e.txt`: 3–5 repos. These run full Docker build + Kind deploy,
  so each takes 3–5 minutes. Pick repos with proven self-contained builds.

---

## Automated discovery

### Running find-repos.py

```bash
cd /Users/jeffvincent/dev/kindling/test/fuzz
python3 find-repos.py --token "$GITHUB_TOKEN" --limit 50
```

This searches GitHub using three strategies:
1. Repos with `microservices` topic per language
2. Repos with `docker` topic per language
3. Repos mentioning docker-compose in README

Then **deep-validates** each candidate by actually reading Dockerfiles,
scoring buildability 0-100, and detecting pipeline edge cases.

Output goes to `repos-candidates.txt` (not committed to git).

### Reading the output

The candidates file has four tiers:

| Tier | Score | Meaning |
|------|-------|---------|
| **RECOMMENDED** | ≥ 60 | Self-contained, no pipeline gaps — should build cleanly |
| **STRETCH** | ≥ 40 + edge cases | Buildable but exercises unimplemented features. Each 🧪 lists the gap and which file to fix |
| **MAYBE** | 40-59 | Might work, worth a manual look |
| **SKIPPED** | < 40 | Monorepo, no Dockerfiles, or too many red flags |

Each repo is annotated with:
- Score, star count, language
- 🟢 green flags (multi-stage, install steps, EXPOSE ports)
- 🔴 red flags (COPY from build artifacts, private base images)
- 🟡 yellow flags (fixable Kaniko issues)
- 🧪 edge cases with fix paths (e.g. "run.sh: pass --build-arg")

---

## Manual validation workflow

When asked to find new fuzz repos, follow these steps:

### 1. Run discovery

```bash
cd /Users/jeffvincent/dev/kindling/test/fuzz
python3 find-repos.py --token "$GITHUB_TOKEN" --limit 50
```

If no `GITHUB_TOKEN` is set, you're limited to 10 API requests/min.

### 2. Review candidates

Read `repos-candidates.txt` and focus on RECOMMENDED and STRETCH tiers.
For each promising repo, verify:

```bash
# Check repo structure — look for Dockerfiles
gh api repos/<owner>/<repo>/contents --jq '.[].name'

# Read a Dockerfile — check it's self-contained
gh api repos/<owner>/<repo>/contents/<path>/Dockerfile \
  --jq '.content' | base64 -D

# If there's a compose, check service topology (optional — compose is just a signal)
gh api repos/<owner>/<repo>/contents/docker-compose.yml \
  --jq '.content' | base64 -D
```

**Key things to check in Dockerfiles:**
- `FROM ... AS builder` + `COPY --from=builder` = multi-stage, self-contained ✅
- `RUN npm install / pip install / go build` = has dependency install step ✅
- `COPY dist/ .` or `COPY build/ .` = requires pre-build step 🔴
- `RUN --mount=type=cache` = BuildKit feature, fixable for Kaniko 🟡
- `ARG` before `FROM` = parameterised base image, fixable 🟡
- `HEALTHCHECK` = not mapped to K8s probes yet, fixable 🟡
- No `RUN` instructions at all = placeholder Dockerfile 🔴

**Repos without compose are fine** — `kindling generate` scans the repo
tree for Dockerfiles. GCP microservices-demo has 11 services and no
compose at all. The Dockerfiles are what matter.

### 3. Cross-check against existing lists

```bash
# See what's already in repos.txt and repos-e2e.txt
cat repos.txt repos-e2e.txt | grep -v '^#' | grep -v '^$' | sort
```

Don't add duplicates. Also check if the repo was previously removed
(search git log for the URL).

### 4. Test a candidate

Run `kindling generate` on a candidate to see if it produces valid output:

```bash
# Clone and generate
git clone --depth 1 https://github.com/<owner>/<repo> /tmp/fuzz-test-repo
kindling generate --api-key "$OPENAI_API_KEY" --repo-path /tmp/fuzz-test-repo --dry-run
```

If the output looks reasonable (correct services, ports, dependencies),
the repo is a good candidate.

### 5. Add to the appropriate list

- **For static testing** (`repos.txt`): Add with a comment describing
  the repo's language, service count, and why it's interesting:
  ```
  # Python Django + Celery + Redis + Postgres (4 services, compose with build)
  https://github.com/owner/repo
  ```

- **For e2e testing** (`repos-e2e.txt`): Only add if the Dockerfile(s)
  build successfully with plain `docker build` and the app starts with
  just dependency env vars. Test locally first if possible.

### 6. Report format

Present findings as:

- **Candidates Found**: total discovered, total validated
- **Recommended Additions**: for each repo:
  - URL, language, star count
  - Service count and dependency types
  - Which list to add it to (repos.txt vs repos-e2e.txt)
  - Any caveats (e.g. "needs npm cache fix for Kaniko")
- **Rejected**: repos checked but not suitable, with reason

---

## Key source files

| File | Purpose |
|------|---------|
| `test/fuzz/find-repos.py` | Automated GitHub search + validation |
| `test/fuzz/repos.txt` | Curated list for static fuzz |
| `test/fuzz/repos-e2e.txt` | Small list for e2e fuzz |
| `test/fuzz/run.sh` | Fuzz runner (consumes repo lists) |
| `test/fuzz/analyze.py` | Validates generated workflows |
| `cli/cmd/generate.go` | The generate prompt (what repos test) |
