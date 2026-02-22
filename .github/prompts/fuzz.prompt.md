# Fuzz Test Results Analyzer

Analyze the latest fuzz test results from CI and report bugs with suggested fixes.

## Instructions

You are analyzing fuzz test artifacts from the kindling project's CI pipeline.
Follow these steps exactly, using terminal commands. Work through each step
sequentially — do not skip steps or summarize prematurely.

### Step 1 — Find the latest fuzz runs

Run this in the terminal to list recent fuzz workflow runs:

```
cd /Users/jeffvincent/dev/kindling && gh run list --workflow=fuzz.yml --limit=5
```

Identify the most recent **completed** run for each job type (`Fuzz (e2e)` and
`Fuzz (static)` / `Fuzz (curated)`). Note their run IDs and statuses.

### Step 2 — Download artifacts

For each completed run, download the artifact:

```
gh run download <RUN_ID> --dir /tmp/fuzz-results-<RUN_ID>
```

Artifacts are named `fuzz-results-<run_id>` (static) or `fuzz-e2e-results-<run_id>` (e2e).
After downloading, `ls` the extracted directory to find the actual results folder.

### Step 3 — Read the summary

For each downloaded artifact, read these files:

1. `summary.json` — overall pass/fail counts
2. `results.jsonl` — per-repo, per-stage results (one JSON object per line)
3. `action-items.json` — pre-computed action items (if present)

Parse `results.jsonl` line-by-line (it is NOT a JSON array). Use:
```
cat <path>/results.jsonl | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if line:
        r = json.loads(line)
        print(f\"{r['stage']:20s} {r['status']:8s} {r['repo'].split('/')[-1]:40s} {r.get('detail','')[:80]}\")
"
```

### Step 4 — Investigate failures

For every repo that has a `fail` or `skip` status at any stage:

**Generate failures**: Read `logs/<repo>.generate.log` for LLM errors or timeouts.

**Static analysis warnings/errors**: Read the `issues` array in the `static_analysis`
result line. Cross-reference with the generated workflow at `workflows/<repo>.yml`.

**Docker build failures**: Read these log files in order:
1. `logs/<repo>.build.<service>.log` — first build attempt stderr
2. `logs/<repo>.fix.<service>.log` — LLM fix attempt output
3. `logs/<repo>.build.<service>.retry.log` — retry build attempt (if exists)

For each build failure, identify:
- What Dockerfile instruction failed (COPY, RUN, etc.)
- Whether the fix-dockerfile.py LLM correctly diagnosed the issue
- Whether the root cause is in the repo's Dockerfile, the generate prompt, or the fuzz harness

**Rollout failures**: Check if the images were built successfully first. If builds
passed but rollout failed, it's likely a missing env var or dependency issue in the
generated workflow.

**E2E failures/skips**: Usually caused by upstream rollout failures. Only investigate
if rollout passed but e2e failed.

### Step 5 — Classify and report

For each bug found, classify it into one of these categories:

| Category | Description | Files to check |
|----------|-------------|----------------|
| **generate-prompt** | LLM produced incorrect workflow YAML | `cli/cmd/generate.go` |
| **generate-infra** | LLM call failed (timeout, rate limit, parse error) | `cli/cmd/genai.go` |
| **fuzz-harness** | Bug in the test pipeline itself (run.sh, analyze.py) | `test/fuzz/run.sh`, `test/fuzz/analyze.py` |
| **dockerfile-fix** | fix-dockerfile.py failed to correct a build issue | `test/fuzz/fix-dockerfile.py` |
| **repo-specific** | Repo is genuinely incompatible (archived, broken Dockerfile) | `test/fuzz/repos.txt` or `repos-e2e.txt` |
| **operator** | DSE controller failed to reconcile | `internal/controller/` |

### Step 6 — Output format

Present your findings as:

**Run Summary**
- Run ID, type (static/e2e), timestamp, overall pass rates

**Bugs Found** (ordered by severity)
For each bug:
- 🔴/🟡/🟢 severity indicator
- Repo name and failure stage
- Root cause (1-2 sentences)
- Category from the table above
- Suggested fix with specific file and line references
- If the fix is a code change, describe exactly what to change

**Repos to Remove** (if any repos are dead, archived, or fundamentally incompatible)

**Pass Rate Trend** (if multiple runs are available, note if rates are improving or degrading)

## Key file locations

- Workflow: `.github/workflows/fuzz.yml`
- Test harness: `test/fuzz/run.sh` (main pipeline, ~640 lines)
- Static analyzer: `test/fuzz/analyze.py` (workflow parser + validator)
- Dockerfile fixer: `test/fuzz/fix-dockerfile.py` (LLM-powered pre-build fix)
- Summarizer: `test/fuzz/summarize.py` (generates action-items.json)
- Generate prompt: `cli/cmd/generate.go` (system prompt starts at `buildGeneratePrompt`)
- LLM caller: `cli/cmd/genai.go`
- Static repo list: `test/fuzz/repos.txt`
- E2E repo list: `test/fuzz/repos-e2e.txt`
