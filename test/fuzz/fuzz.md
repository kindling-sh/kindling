# Fuzz Test Results — Copilot Instructions

This file tells you how to interpret, triage, and act on the output of
the kindling fuzz test suite (`test/fuzz/run.sh`). The fuzz workflow
uploads an artifact named `fuzz-results-<run-id>/` containing the files
described below.

---

## Artifact layout

```
fuzz-results/
  summary.json          # aggregate pass/fail counts
  results.jsonl         # one JSON object per (repo, stage) pair
  action-items.json     # structured list of issues to investigate
  workflows/<repo>.yml  # the generated GitHub Actions workflow
  dse/<repo>.yaml       # the generated DevStagingEnvironment manifests
  logs/<repo>.*         # stderr from generate, build, deploy, etc.
```

## How to read `summary.json`

```json
{
  "total": 5,
  "generate_ok": 5,      // kindling generate exited 0
  "yaml_ok": 5,           // output was valid YAML with a jobs key
  "static_net_ok": 0,     // passed static analysis with 0 issues
  "build_ok": 0,          // Docker build succeeded (full mode only)
  "deploy_ok": 0,         // DSE rollout succeeded (full mode only)
  "e2e_ok": 0,            // networking checks passed (full mode only)
  "generate_rate": "100.0%",
  "e2e_rate": "?%"        // "?" means no e2e was attempted
}
```

The key success metric is `generate_rate`. It must stay ≥ 80%.

## How to read `results.jsonl`

Each line is a JSON object representing one stage for one repo:

```json
{
  "repo": "https://github.com/owner/repo",
  "stage": "generate | yaml_validate | static_analysis | docker_build | deploy | rollout | e2e",
  "status": "pass | fail | warn | skip",
  "detail": "human-readable explanation",
  "duration_ms": 12345,
  "services_count": 3,
  "issues": [ ... ]
}
```

Stages execute in order. A `fail` at any stage skips subsequent stages
for that repo.

## How to read `action-items.json`

`summarize.py --action-items` produces this file. It contains a flat
list of actionable findings:

```json
[
  {
    "severity": "error | warning | info",
    "repo": "repo-name",
    "stage": "generate",
    "summary": "short description of the problem",
    "detail": "extended context including error messages",
    "files_to_check": ["cli/cmd/generate.go", "cli/cmd/genai.go"],
    "suggested_action": "what to investigate or fix"
  }
]
```

---

## Triage rules

Apply these rules **in priority order** when analyzing fuzz results:

### 1. Generate failures (`stage=generate, status=fail`)

**Severity: error** — This means `kindling generate` crashed or
returned non-zero for a real-world repo.

**What to do:**
- Read `logs/<repo>.generate.stderr` for the error message.
- Common root causes:
  - LLM returned unparseable output → check `cli/cmd/genai.go`
    (the prompt and response parsing logic).
  - Timeout or API error → transient, re-run before investigating.
  - Panic/nil pointer → check `cli/cmd/generate.go` and the
    repo-analysis functions.
- If the error is `context deadline exceeded` or `rate limit`, it is
  transient — note it but don't file a code fix.
- If the error is a parsing failure, look at the raw LLM response in
  stderr and check whether the prompt in `genai.go` needs to be more
  explicit about the output format.

### 2. YAML validation failures (`stage=yaml_validate, status=fail`)

**Severity: error** — The generated workflow is not valid YAML or
is missing the `jobs` key.

**What to do:**
- Read `workflows/<repo>.yml` to see the malformed output.
- This almost always means the LLM returned markdown fences, extra
  commentary, or truncated output.
- Fix: tighten the prompt or response-cleaning logic in
  `cli/cmd/genai.go`.

### 3. Static analysis warnings (`stage=static_analysis, status=warn`)

**Severity: info** (usually) — Static analysis flagged issues in the
generated manifests.

**Issue types and what they mean:**

| Issue type | Meaning | Action needed? |
|---|---|---|
| `missing_health_check` | Service has no health-check-path | Usually OK — most repos don't expose `/healthz`. No fix needed unless the repo clearly has one. |
| `port_mismatch` | Container port in DSE doesn't match what the Dockerfile EXPOSEs | **Investigate** — read `dse/<repo>.yaml` and compare with the repo's Dockerfile. May indicate the LLM guessed the wrong port. Fix prompt or post-processing in `genai.go`. |
| `dangling_service_ref` | An env var references a service name that doesn't exist in the manifest | **Investigate** — the LLM generated a cross-service URL pointing to a service it didn't create a DSE for. Check the `dse/<repo>.yaml`. |
| `missing_dependency` | A service uses a database/cache but no dependency was declared | **Investigate** — check if the repo's docker-compose shows the dependency. If so, the LLM missed it. |

### 4. Docker build failures (`stage=docker_build, status=fail`)

**Severity: warning** — Only present in `full` mode.

**What to do:**
- Read `logs/<repo>.build.<service>.log` for the build error.
- The fuzz runner already attempts an LLM-assisted Dockerfile fix and
  retry (see `fix-dockerfile.py`). If it still fails, the Dockerfile
  likely needs platform-specific dependencies or a private base image.
- These are **not necessarily kindling bugs** — they're repo-specific
  build issues. Only investigate if the same build error pattern
  appears across multiple repos.

### 5. Deploy/rollout failures (`stage=rollout, status=fail`)

**Severity: warning** — The DSE was applied but pods didn't become
Ready.

**What to do:**
- Read `logs/<repo>.rollout.<service>.log` and
  `logs/<repo>.podlogs.<service>.log`.
- Common causes: missing env vars, the container crashes at startup,
  or dependencies (postgres, redis) aren't available.
- Check `dse/<repo>.yaml` — are the env vars reasonable? Does it
  reference `$(DATABASE_URL)` but the operator doesn't resolve that?
- If the DSE manifest is correct but the app just crashes, it's a
  repo issue, not a kindling bug.

### 6. E2E networking failures (`stage=e2e, status=fail`)

**Severity: warning** — Health check or cross-service curl failed.

**What to do:**
- Check the issues array for `self_health` vs `cross_service` type.
- `self_health` failure: the service is running but doesn't respond
  on the expected port/path. Check port in `dse/<repo>.yaml`.
- `cross_service` failure: service A can't reach service B. Check
  the env var URLs in the DSE manifest — is the service name and
  port correct?

---

## When to fix kindling code vs. skip

| Signal | Action |
|--------|--------|
| Same failure across ≥3 repos | **Fix kindling** — systematic issue in generate/prompt |
| Failure only on 1 exotic repo | **Skip** — likely repo-specific edge case |
| Transient API error | **Re-run** — not a code issue |
| `missing_health_check` info warnings | **Ignore** — expected for most repos |
| Generate rate < 80% | **Investigate urgently** — regression in generate quality |
| Generate rate ≥ 95% | **Healthy** — only look at individual failures if curious |

## Key source files

| File | Purpose |
|------|---------|
| `cli/cmd/generate.go` | Orchestrates repo analysis → LLM call → output |
| `cli/cmd/genai.go` | LLM prompt construction, API call, response parsing |
| `cli/cmd/helpers.go` | Repo scanning (Dockerfiles, compose, manifests) |
| `api/v1alpha1/devstagingenvironment_types.go` | DSE CRD schema |
| `test/fuzz/run.sh` | The fuzz test runner |
| `test/fuzz/analyze.py` | Static analysis of generated workflows → DSE manifests |
| `test/fuzz/fix-dockerfile.py` | LLM-assisted Dockerfile repair |
| `test/fuzz/find-repos.py` | Automated repo discovery |
| `test/fuzz/summarize.py` | Results summarization + action items |

## Workflow

The fuzz test runs as `.github/workflows/fuzz.yml` (workflow_dispatch).
To trigger it:

```bash
gh workflow run fuzz.yml -f mode=static -f provider=openai -f repos=curated
```

Results are uploaded as the `fuzz-results-<run-id>` artifact. Download
and inspect with:

```bash
gh run download <run-id> -n fuzz-results-<run-id>
```

---

## Automated analysis steps

When asked to analyze fuzz results, follow these steps in order:

### 1. Find the latest runs

```bash
cd /Users/jeffvincent/dev/kindling && gh run list --workflow=fuzz.yml --limit=5
```

Identify the most recent completed run for each job type (`Fuzz (e2e)`
and `Fuzz (static)` / `Fuzz (curated)`).

### 2. Download artifacts

```bash
gh run download <RUN_ID> --dir /tmp/fuzz-results-<RUN_ID>
```

Then `ls` the extracted directory to find the results folder.

### 3. Parse the results

```bash
cat <path>/results.jsonl | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if line:
        r = json.loads(line)
        print(f\"{r['stage']:20s} {r['status']:8s} {r['repo'].split('/')[-1]:40s} {r.get('detail','')[:80]}\")
"
```

Also read `summary.json` for overall pass rates and `action-items.json`
if present.

### 4. Investigate failures

For each repo with `fail` or `skip` status:

- **Generate failures**: Read `logs/<repo>.generate.stderr`
- **Build failures**: Read `logs/<repo>.build.<service>.log`, then
  `logs/<repo>.fix.<service>.log`, then `logs/<repo>.build.<service>.retry.log`
- **Rollout failures**: Check if images built first, then read pod logs
- **Static issues**: Cross-reference `issues` array with `workflows/<repo>.yml`

### 5. Classify each bug

| Category | Description | Files to check |
|----------|-------------|----------------|
| **generate-prompt** | LLM produced incorrect workflow | `cli/cmd/generate.go` |
| **generate-infra** | LLM call failed (timeout, parse) | `cli/cmd/genai.go` |
| **fuzz-harness** | Bug in the test pipeline | `test/fuzz/run.sh`, `test/fuzz/analyze.py` |
| **dockerfile-fix** | fix-dockerfile.py failed | `test/fuzz/fix-dockerfile.py` |
| **repo-specific** | Repo is incompatible | `test/fuzz/repos.txt` or `repos-e2e.txt` |
| **operator** | DSE controller failed | `internal/controller/` |

### 6. Report format

Present findings as:

- **Run Summary**: ID, type, timestamp, pass rates
- **Bugs Found** (by severity): 🔴/🟡/🟢 + repo + stage + root cause
  + category + suggested fix with file/line references
- **Repos to Remove**: dead, archived, or fundamentally incompatible
- **Pass Rate Trend**: improving or degrading across runs
