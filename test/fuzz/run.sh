#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────
# run.sh — Full e2e fuzz-test for kindling generate
#
# For each repo:
#   1. Shallow-clone
#   2. Run kindling generate --dry-run
#   3. Validate the generated YAML
#   4. Static networking analysis (port mismatches, dangling refs)
#   5. Docker build each service image
#   6. kind load docker-image into the cluster
#   7. Convert workflow → DSE manifests, kubectl apply
#   8. Wait for all DSEs to become Ready
#   9. e2e networking validation: exec into pods, curl health
#      endpoints + inter-service URLs
#  10. Cleanup: delete DSE CRs (cascade deletes everything)
#
# Prerequisites:
#   - Kind cluster running with kindling operator installed
#     (run `kindling init` first, or let the GH Actions workflow
#      handle it)
#   - Docker, kubectl, kind on PATH
#   - Python 3 + pyyaml
#
# Usage:
#   ./run.sh <repos.txt> <output-dir> [kindling-binary]
#
# Env vars:
#   FUZZ_PROVIDER   LLM provider for generate (default: openai)
#   FUZZ_API_KEY    API key (falls back to OPENAI_API_KEY)
#   FUZZ_MODEL      Model override (optional)
#   FUZZ_CLUSTER    Kind cluster name (default: fuzz)
#   FUZZ_NAMESPACE  Namespace for DSE deployments (default: default)
#   SKIP_E2E        Set to 1 to skip cluster deploy (static only)
# ─────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

REPOS_FILE="${1:?Usage: run.sh <repos.txt> <output-dir> [kindling-binary]}"
OUTPUT_DIR="${2:?Usage: run.sh <repos.txt> <output-dir> [kindling-binary]}"
KINDLING="${3:-kindling}"
CLUSTER_NAME="${FUZZ_CLUSTER:-fuzz}"
NAMESPACE="${FUZZ_NAMESPACE:-default}"
TIMEOUT_BUILD=300   # 5 min per docker build
TIMEOUT_READY=180   # 3 min for DSE to become Ready
SKIP_E2E="${SKIP_E2E:-0}"

mkdir -p "$OUTPUT_DIR"
RESULTS="$OUTPUT_DIR/results.jsonl"
: > "$RESULTS"

# Counters
TOTAL=0; GENERATE_OK=0; YAML_OK=0; STATIC_NET_OK=0
BUILD_OK=0; DEPLOY_OK=0; E2E_OK=0

# ── Helpers ──────────────────────────────────────────────────────

log()  { echo "[$1] $2" >&2; }
now_ms() { python3 -c 'import time; print(int(time.time()*1000))'; }

# Write a JSON result line (uses python3 for proper escaping)
emit() {
  local repo="$1" stage="$2" status="$3" detail="$4" duration_ms="$5"
  local services_count="${6:-0}" issues="${7:-[]}" category="${8:-}"
  python3 -c "
import json, sys
obj = {
    'repo': sys.argv[1],
    'stage': sys.argv[2],
    'status': sys.argv[3],
    'detail': sys.argv[4],
    'duration_ms': int(sys.argv[5]),
    'services_count': int(sys.argv[6]),
    'issues': json.loads(sys.argv[7]),
}
if sys.argv[8]:
    obj['category'] = sys.argv[8]
print(json.dumps(obj))
" "$repo" "$stage" "$status" "$detail" "$duration_ms" "$services_count" "$issues" "$category" \
    >> "$RESULTS"
}

# ── Per-repo test ────────────────────────────────────────────────

test_repo() {
  local repo_url="$1"
  local repo_name
  repo_name=$(echo "$repo_url" | sed 's|.*/||; s|\.git$||')
  local clone_dir="$OUTPUT_DIR/repos/$repo_name"
  local workflow_file="$OUTPUT_DIR/workflows/${repo_name}.yml"
  local dse_file="$OUTPUT_DIR/dse/${repo_name}.yaml"
  local repo_log="$OUTPUT_DIR/logs/${repo_name}"

  TOTAL=$((TOTAL + 1))
  log "REPO" "════════════════════════════════════════"
  log "REPO" "[$TOTAL] $repo_url"

  # ── 1. Clone ─────────────────────────────────────────────────
  rm -rf "$clone_dir"
  mkdir -p "$clone_dir" "$OUTPUT_DIR/workflows" "$OUTPUT_DIR/dse" "$OUTPUT_DIR/logs"
  local t0; t0=$(now_ms)

  if ! git clone --depth=1 --single-branch -q "$repo_url" "$clone_dir" 2>/dev/null; then
    local dur=$(( $(now_ms) - t0 ))
    emit "$repo_url" "clone" "fail" "git clone failed" "$dur"
    log "FAIL" "clone failed — skipping"
    rm -rf "$clone_dir"
    return
  fi

  # ── 2. Generate workflow (the thing we're testing) ───────────
  t0=$(now_ms)
  local gen_stderr="$repo_log.generate.stderr"

  if "$KINDLING" generate \
      --repo-path "$clone_dir" \
      --dry-run \
      --provider "${FUZZ_PROVIDER:-openai}" \
      --api-key "${FUZZ_API_KEY:-$OPENAI_API_KEY}" \
      ${FUZZ_MODEL:+--model "$FUZZ_MODEL"} \
      > "$workflow_file" 2>"$gen_stderr"; then
    local dur=$(( $(now_ms) - t0 ))
    GENERATE_OK=$((GENERATE_OK + 1))
    emit "$repo_url" "generate" "pass" "" "$dur"
    log "PASS" "generate (${dur}ms)"
  else
    local dur=$(( $(now_ms) - t0 ))
    local err
    err=$(head -5 "$gen_stderr" | tr '\n' ' ' | cut -c1-200)
    emit "$repo_url" "generate" "fail" "$err" "$dur"
    log "FAIL" "generate — $err"
    rm -rf "$clone_dir"
    return
  fi

  # ── 3. Validate YAML ────────────────────────────────────────
  t0=$(now_ms)
  if ! python3 -c "
import yaml, sys
with open('$workflow_file') as f:
    data = yaml.safe_load(f)
if not isinstance(data, dict):
    sys.exit(1)
if 'jobs' not in data:
    sys.exit(1)
" 2>/dev/null; then
    local dur=$(( $(now_ms) - t0 ))
    emit "$repo_url" "yaml_validate" "fail" "invalid YAML or missing jobs key" "$dur"
    log "FAIL" "invalid YAML"
    rm -rf "$clone_dir"
    return
  fi
  local dur=$(( $(now_ms) - t0 ))
  YAML_OK=$((YAML_OK + 1))
  emit "$repo_url" "yaml_validate" "pass" "" "$dur"

  # ── 4. Static analysis + emit DSE CRs ───────────────────────
  #     analyze.py parses the generated workflow, cross-validates
  #     networking (the core test of generate quality), and writes
  #     DSE manifests we can kubectl apply directly.
  t0=$(now_ms)
  local analysis
  analysis=$(python3 "$SCRIPT_DIR/analyze.py" \
    "$workflow_file" "$clone_dir" \
    --emit-dse "$dse_file" \
    --prefix "$repo_name" \
    2>/dev/null) || true

  if [ -n "$analysis" ]; then
    local svc_count net_issues issue_count
    svc_count=$(echo "$analysis" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('services_count',0))" 2>/dev/null || echo 0)
    net_issues=$(echo "$analysis" | python3 -c "import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get('issues',[])))" 2>/dev/null || echo "[]")
    issue_count=$(echo "$net_issues" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)

    dur=$(( $(now_ms) - t0 ))
    if [ "$issue_count" -eq 0 ]; then
      STATIC_NET_OK=$((STATIC_NET_OK + 1))
      emit "$repo_url" "static_analysis" "pass" "${svc_count} services, 0 issues" "$dur" "$svc_count" "$net_issues"
      log "PASS" "static analysis — ${svc_count} services, 0 issues"
    else
      emit "$repo_url" "static_analysis" "warn" "${svc_count} services, ${issue_count} issues" "$dur" "$svc_count" "$net_issues"
      log "WARN" "static analysis — ${svc_count} services, ${issue_count} issues"
    fi
  else
    dur=$(( $(now_ms) - t0 ))
    emit "$repo_url" "static_analysis" "skip" "analyze.py failed" "$dur"
    log "SKIP" "static analysis failed"
    # Can't continue to e2e without DSE manifests
    rm -rf "$clone_dir"
    return
  fi

  # ── If SKIP_E2E, stop here ──────────────────────────────────
  if [ "$SKIP_E2E" = "1" ]; then
    log "SKIP" "e2e disabled (SKIP_E2E=1)"
    rm -rf "$clone_dir"
    return
  fi

  # ── 5. Docker build images using contexts from the workflow ──
  local builds_json
  builds_json=$(echo "$analysis" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for b in d.get('builds', []):
    print(json.dumps(b))
" 2>/dev/null) || true

  if [ -z "$builds_json" ]; then
    emit "$repo_url" "docker_build" "skip" "no build steps in workflow" "0"
    log "SKIP" "no build steps"
    rm -rf "$clone_dir"
    return
  fi

  local all_builds_ok=true
  while IFS= read -r build_line; do
    local build_name build_ctx build_df img_tag
    build_name=$(echo "$build_line" | python3 -c "import sys,json; print(json.load(sys.stdin)['name'])" 2>/dev/null)
    build_ctx=$(echo "$build_line" | python3 -c "import sys,json; print(json.load(sys.stdin)['context'])" 2>/dev/null)
    build_df=$(echo "$build_line" | python3 -c "import sys,json; print(json.load(sys.stdin).get('dockerfile',''))" 2>/dev/null)
    img_tag="${repo_name}-${build_name}:test"

    local build_dir="$clone_dir/$build_ctx"
    local dockerfile=""

    # Resolve Dockerfile path: prefer explicit 'dockerfile' field, then default
    if [ -n "$build_df" ]; then
      # dockerfile field is relative to context
      if [ -f "$build_dir/$build_df" ]; then
        dockerfile="$build_dir/$build_df"
      elif [ -f "$clone_dir/$build_df" ]; then
        # Also try relative to clone root
        dockerfile="$clone_dir/$build_df"
      fi
    fi

    if [ -z "$dockerfile" ]; then
      # Fall back to Dockerfile at context root
      if [ -f "$build_dir/Dockerfile" ]; then
        dockerfile="$build_dir/Dockerfile"
      elif [ -f "$build_dir/dockerfile" ]; then
        dockerfile="$build_dir/dockerfile"
      fi
    fi

    if [ -z "$dockerfile" ]; then
      local df_desc="${build_df:-${build_ctx}/Dockerfile}"
      emit "$repo_url" "docker_build" "skip" "${build_name}: no Dockerfile at ${df_desc}" "0"
      log "SKIP" "build ${build_name} — no Dockerfile at ${df_desc}"
      all_builds_ok=false
      continue
    fi

    # ── 5a. LLM pre-build analysis: fix the Dockerfile before building ──
    local original_dockerfile="${dockerfile}.original"
    cp "$dockerfile" "$original_dockerfile"

    log "FIX" "analyzing Dockerfile for ${build_name} via LLM..."
    local fixed_dockerfile
    if fixed_dockerfile=$(python3 "$SCRIPT_DIR/fix-dockerfile.py" \
        --dockerfile "$dockerfile" \
        --context-dir "$build_dir" 2>"$repo_log.fix.${build_name}.log"); then
      echo "$fixed_dockerfile" > "$dockerfile"
      log "FIX" "LLM pre-build fix applied for ${build_name}"
    else
      log "FIX" "LLM pre-build analysis failed — using original Dockerfile"
      cp "$original_dockerfile" "$dockerfile"
    fi

    # ── 5b. Build (attempt 1) ──────────────────────────────────
    t0=$(now_ms)
    local build_success=false

    if timeout "$TIMEOUT_BUILD" docker build -t "$img_tag" -f "$dockerfile" "$build_dir" \
        >"$repo_log.build.${build_name}.log" 2>&1; then
      dur=$(( $(now_ms) - t0 ))
      build_success=true
      log "PASS" "build ${build_name} (${dur}ms)"
    else
      dur=$(( $(now_ms) - t0 ))
      local build_err
      build_err=$(tail -20 "$repo_log.build.${build_name}.log" 2>/dev/null | tr '\n' ' ' | cut -c1-500)
      log "FAIL" "build ${build_name} — attempting LLM retry fix..."

      # ── 5c. Retry: feed error back to LLM ─────────────────────
      local retry_dockerfile
      if retry_dockerfile=$(python3 "$SCRIPT_DIR/fix-dockerfile.py" \
          --dockerfile "$original_dockerfile" \
          --context-dir "$build_dir" \
          --build-error "$build_err" 2>>"$repo_log.fix.${build_name}.log"); then
        echo "$retry_dockerfile" > "$dockerfile"
        log "FIX" "LLM retry fix applied for ${build_name} — rebuilding..."

        t0=$(now_ms)
        if timeout "$TIMEOUT_BUILD" docker build -t "$img_tag" -f "$dockerfile" "$build_dir" \
            >"$repo_log.build.${build_name}.retry.log" 2>&1; then
          dur=$(( $(now_ms) - t0 ))
          build_success=true
          log "PASS" "build ${build_name} (retry, ${dur}ms)"
        else
          dur=$(( $(now_ms) - t0 ))
          log "FAIL" "build ${build_name} — retry also failed"
        fi
      else
        log "FIX" "LLM retry fix failed — no more attempts"
      fi
    fi

    # Restore original so we don't leave modified files around
    cp "$original_dockerfile" "$dockerfile"
    rm -f "$original_dockerfile"

    if $build_success; then
      BUILD_OK=$((BUILD_OK + 1))
      emit "$repo_url" "docker_build" "pass" "${build_name} (${dur}ms)" "$dur"

      # Load into Kind cluster
      if ! kind load docker-image "$img_tag" --name "$CLUSTER_NAME" >/dev/null 2>&1; then
        emit "$repo_url" "kind_load" "fail" "${build_name}" "0"
        log "FAIL" "kind load ${build_name}"
        all_builds_ok=false
      fi
    else
      all_builds_ok=false
      build_err=$(tail -20 "$repo_log.build.${build_name}.log" 2>/dev/null | tr '\n' ' ' | cut -c1-500)
      emit "$repo_url" "docker_build" "fail" "${build_name} — $build_err" "$dur"
      log "FAIL" "build ${build_name}"
      echo "  ┌─── $build_name build failure (last 30 lines) ───" >&2
      tail -30 "$repo_log.build.${build_name}.log" 2>/dev/null | sed 's/^/  │   /' >&2 || true
      echo "  └────────────────────────────────────────" >&2
    fi
  done <<< "$builds_json"

  if ! $all_builds_ok; then
    log "WARN" "some builds failed — deploying what we can"
  fi

  # ── 6. Apply DSE manifests to the Kind cluster ──────────────
  if [ ! -f "$dse_file" ] || [ ! -s "$dse_file" ]; then
    emit "$repo_url" "deploy" "skip" "no DSE manifests generated" "0"
    log "SKIP" "no DSE manifests"
    rm -rf "$clone_dir"
    return
  fi

  t0=$(now_ms)
  if kubectl apply -f "$dse_file" -n "$NAMESPACE" \
      >"$repo_log.deploy.log" 2>&1; then
    dur=$(( $(now_ms) - t0 ))
    emit "$repo_url" "deploy" "pass" "DSE manifests applied" "$dur"
    log "PASS" "kubectl apply DSE manifests"
  else
    dur=$(( $(now_ms) - t0 ))
    local deploy_err
    deploy_err=$(tail -5 "$repo_log.deploy.log" 2>/dev/null | tr '\n' ' ' | cut -c1-200)
    emit "$repo_url" "deploy" "fail" "$deploy_err" "$dur"
    log "FAIL" "kubectl apply — $deploy_err"
    cleanup_dse "$repo_name"
    rm -rf "$clone_dir"
    return
  fi

  # ── 7. Wait for all DSEs to become Ready ────────────────────
  t0=$(now_ms)
  local dse_names
  dse_names=$(kubectl get dse -n "$NAMESPACE" \
    -l "kindling-fuzz/prefix=$repo_name" \
    -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)

  local all_ready=true
  for dse_name in $dse_names; do
    log "WAIT" "waiting for DSE $dse_name (up to ${TIMEOUT_READY}s)..."

    # Wait for deployment rollout
    if ! kubectl rollout status "deployment/$dse_name" \
        -n "$NAMESPACE" --timeout="${TIMEOUT_READY}s" \
        >"$repo_log.rollout.${dse_name}.log" 2>&1; then
      all_ready=false

      # ── Collect diagnostics and print them inline ────────────
      kubectl get pods -n "$NAMESPACE" -l "app=$dse_name" -o wide \
        >"$repo_log.pods.${dse_name}.log" 2>&1 || true
      kubectl describe pods -n "$NAMESPACE" -l "app=$dse_name" \
        >"$repo_log.describe.${dse_name}.log" 2>&1 || true
      kubectl logs -n "$NAMESPACE" -l "app=$dse_name" --tail=50 --all-containers \
        >"$repo_log.podlogs.${dse_name}.log" 2>&1 || true
      kubectl get events -n "$NAMESPACE" --sort-by=.lastTimestamp \
        --field-selector "involvedObject.name=$dse_name" \
        >"$repo_log.events.${dse_name}.log" 2>&1 || true

      # Categorise the failure from pod status + events
      local fail_category="unknown"
      local fail_reason=""

      # Check pod container statuses for known patterns
      local pod_status_json
      pod_status_json=$(kubectl get pods -n "$NAMESPACE" -l "app=$dse_name" \
        -o jsonpath='{range .items[0].status.containerStatuses[*]}{.state}{"\n"}{end}' 2>/dev/null) || true

      if echo "$pod_status_json" | grep -qi "ImagePullBackOff\|ErrImagePull"; then
        fail_category="image_pull"
        fail_reason="Image pull failed — image may not exist in the cluster registry"
      elif echo "$pod_status_json" | grep -qi "CrashLoopBackOff"; then
        fail_category="crash_loop"
        # Extract exit code and last log line for crash reason
        local exit_code
        exit_code=$(kubectl get pods -n "$NAMESPACE" -l "app=$dse_name" \
          -o jsonpath='{.items[0].status.containerStatuses[0].lastState.terminated.exitCode}' 2>/dev/null) || true
        local last_log
        last_log=$(tail -3 "$repo_log.podlogs.${dse_name}.log" 2>/dev/null | tr '\n' ' ' | cut -c1-300)
        fail_reason="CrashLoopBackOff (exit $exit_code): $last_log"
      elif echo "$pod_status_json" | grep -qi "OOMKilled"; then
        fail_category="oom_killed"
        fail_reason="Container was OOMKilled — needs more memory"
      elif echo "$pod_status_json" | grep -qi "CreateContainerConfigError"; then
        fail_category="config_error"
        fail_reason="Container config error — missing ConfigMap, Secret, or env var"
      fi

      # If still unknown, check for common log patterns
      if [ "$fail_category" = "unknown" ]; then
        local log_tail
        log_tail=$(tail -20 "$repo_log.podlogs.${dse_name}.log" 2>/dev/null || true)
        if echo "$log_tail" | grep -qiE "connection refused|ECONNREFUSED|could not connect|no such host"; then
          fail_category="missing_dependency"
          fail_reason="App can't reach a dependency (DB, cache, or upstream service)"
        elif echo "$log_tail" | grep -qiE "FATAL|panic|Traceback|Error:.*not found|MODULE_NOT_FOUND"; then
          fail_category="app_crash"
          fail_reason="App crashed on startup"
        elif echo "$log_tail" | grep -qiE "database.*does not exist|relation.*does not exist|OperationalError"; then
          fail_category="missing_database"
          fail_reason="Database not initialised or missing"
        elif echo "$log_tail" | grep -qiE "REDIS_URL|MONGO|AMQP|RABBITMQ|KAFKA"; then
          fail_category="missing_dependency"
          fail_reason="Missing backing service (Redis/Mongo/RabbitMQ/Kafka)"
        fi
      fi

      # Fall back to last meaningful pod log line
      if [ "$fail_category" = "unknown" ]; then
        fail_reason=$(tail -5 "$repo_log.podlogs.${dse_name}.log" 2>/dev/null | tr '\n' ' ' | cut -c1-300)
        [ -z "$fail_reason" ] && fail_reason="no pod logs available"
      fi

      log "FAIL" "rollout $dse_name [$fail_category]"

      # ── Print diagnostics inline so they appear in GH Actions ──
      echo "  ┌─── $dse_name rollout diagnostics [$fail_category] ───" >&2
      echo "  │ Reason: $fail_reason" >&2
      echo "  │" >&2
      echo "  │ Pod status:" >&2
      sed 's/^/  │   /' "$repo_log.pods.${dse_name}.log" 2>/dev/null >&2 || true
      echo "  │" >&2
      echo "  │ Container logs (last 30 lines):" >&2
      tail -30 "$repo_log.podlogs.${dse_name}.log" 2>/dev/null | sed 's/^/  │   /' >&2 || true
      echo "  │" >&2
      echo "  │ Events:" >&2
      tail -10 "$repo_log.events.${dse_name}.log" 2>/dev/null | sed 's/^/  │   /' >&2 || true
      echo "  └────────────────────────────────────────" >&2

      emit "$repo_url" "rollout" "fail" "$fail_reason" "0" "0" "[]" "$fail_category"
    else
      log "PASS" "rollout $dse_name ready"
    fi
  done

  dur=$(( $(now_ms) - t0 ))
  if $all_ready; then
    DEPLOY_OK=$((DEPLOY_OK + 1))
    emit "$repo_url" "rollout" "pass" "all DSEs ready" "$dur"
    log "PASS" "all DSEs ready (${dur}ms)"
  else
    emit "$repo_url" "rollout" "partial" "some DSEs failed to roll out" "$dur"
    log "WARN" "partial rollout"
  fi

  # ── 8. e2e networking validation ─────────────────────────────
  #     For each service, port-forward from the host and curl:
  #       a) its own health check (sanity)
  #       b) every cross-service URL (the real test — via the
  #          service's ClusterIP, reached from a helper pod)
  #     We port-forward to avoid depending on wget/curl in the
  #     container (distroless, scratch, alpine-minimal, etc.)
  t0=$(now_ms)
  local e2e_pass=0 e2e_fail=0 e2e_issues="["

  for dse_name in $dse_names; do
    # Get a running pod for this DSE
    local pod
    pod=$(kubectl get pods -n "$NAMESPACE" -l "app=$dse_name" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) || true
    if [ -z "$pod" ]; then
      log "SKIP" "e2e $dse_name — no pod found"
      continue
    fi

    # Check pod is Running
    local phase
    phase=$(kubectl get pod "$pod" -n "$NAMESPACE" \
      -o jsonpath='{.status.phase}' 2>/dev/null) || true
    if [ "$phase" != "Running" ]; then
      log "SKIP" "e2e $dse_name — pod $phase"
      continue
    fi

    # Get service info from the DSE analysis
    local svc_port svc_health svc_envs
    svc_port=$(echo "$analysis" | python3 -c "
import sys, json
d = json.load(sys.stdin)
name = '$dse_name'
for svc in d.get('services', []):
    if svc['name'] == name:
        print(svc.get('port', '8080'))
        break
else:
    print('8080')
" 2>/dev/null || echo "8080")

    svc_health=$(echo "$analysis" | python3 -c "
import sys, json
d = json.load(sys.stdin)
name = '$dse_name'
for svc in d.get('services', []):
    if svc['name'] == name:
        print(svc.get('health_check_path', '/'))
        break
else:
    print('/')
" 2>/dev/null || echo "/")

    # a) Self health check via port-forward
    log "E2E" "$dse_name → self :${svc_port}${svc_health}"
    local local_port self_result
    local_port=$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()")

    kubectl port-forward "pod/$pod" -n "$NAMESPACE" \
      "${local_port}:${svc_port}" >/dev/null 2>&1 &
    local pf_pid=$!
    sleep 1  # give port-forward time to bind

    self_result=$(curl -sf -o /dev/null -w "%{http_code}" \
      --max-time 5 "http://localhost:${local_port}${svc_health}" 2>/dev/null) || self_result="FAIL"
    kill "$pf_pid" 2>/dev/null; wait "$pf_pid" 2>/dev/null || true

    if [[ "$self_result" =~ ^[23] ]]; then
      e2e_pass=$((e2e_pass + 1))
      log "PASS" "e2e $dse_name → self health OK ($self_result)"
    else
      e2e_fail=$((e2e_fail + 1))
      [ "$e2e_issues" != "[" ] && e2e_issues="$e2e_issues,"
      e2e_issues="$e2e_issues{\"service\":\"$dse_name\",\"type\":\"self_health\",\"detail\":\"health check returned $self_result\"}"
      log "FAIL" "e2e $dse_name → self health FAIL ($self_result)"
    fi

    # b) Cross-service: test every URL-like env var via port-forward
    #    to the target service's ClusterIP
    svc_envs=$(echo "$analysis" | python3 -c "
import sys, json, re
d = json.load(sys.stdin)
name = '$dse_name'
for svc in d.get('services', []):
    if svc['name'] == name:
        for k, v in svc.get('env', {}).items():
            # Clean github.actor templates
            v = re.sub(r'\\\$\{\{\s*github\.actor\s*\}\}-', '', v)
            v = re.sub(r'\\\$\{\{[^}]*\}\}', '', v)
            # Only test HTTP(S) URLs
            if re.match(r'https?://', v):
                print(f'{k}={v}')
        break
" 2>/dev/null) || true

    while IFS= read -r env_line; do
      [ -z "$env_line" ] && continue
      local env_name="${env_line%%=*}"
      local env_url="${env_line#*=}"

      # Extract target service name and port from URL
      local target_name target_port url_path
      target_name=$(echo "$env_url" | sed -E 's|https?://||; s|:[0-9]+.*||')
      target_port=$(echo "$env_url" | sed -nE 's|.*:([0-9]+).*|\1|p')
      url_path=$(echo "$env_url" | sed -nE 's|https?://[^/]+(/.*)|\1|p')
      [ -z "$target_port" ] && target_port="80"

      # If no path, try the target service's health path
      if [ -z "$url_path" ]; then
        url_path=$(echo "$analysis" | python3 -c "
import sys, json
d = json.load(sys.stdin)
t = '$target_name'
for svc in d.get('services', []):
    if svc['name'] == t:
        print(svc.get('health_check_path', '/'))
        break
else:
    print('/')
" 2>/dev/null || echo "/")
      fi

      # Find the target pod and port-forward to it
      local target_pod
      target_pod=$(kubectl get pods -n "$NAMESPACE" -l "app=$target_name" \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null) || true

      if [ -z "$target_pod" ]; then
        log "SKIP" "e2e $dse_name → $env_name — target pod '$target_name' not found"
        continue
      fi

      log "E2E" "$dse_name → $env_name=$target_name:${target_port}${url_path}"

      local cross_local_port cross_result
      cross_local_port=$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()")

      kubectl port-forward "pod/$target_pod" -n "$NAMESPACE" \
        "${cross_local_port}:${target_port}" >/dev/null 2>&1 &
      local cross_pf_pid=$!
      sleep 1

      cross_result=$(curl -sf -o /dev/null -w "%{http_code}" \
        --max-time 5 "http://localhost:${cross_local_port}${url_path}" 2>/dev/null) || cross_result="FAIL"
      kill "$cross_pf_pid" 2>/dev/null; wait "$cross_pf_pid" 2>/dev/null || true

      if [[ "$cross_result" =~ ^[23] ]]; then
        e2e_pass=$((e2e_pass + 1))
        log "PASS" "e2e $dse_name → $env_name OK ($cross_result)"
      else
        e2e_fail=$((e2e_fail + 1))
        [ "$e2e_issues" != "[" ] && e2e_issues="$e2e_issues,"
        e2e_issues="$e2e_issues{\"service\":\"$dse_name\",\"type\":\"cross_service\",\"env\":\"$env_name\",\"url\":\"${target_name}:${target_port}${url_path}\",\"detail\":\"returned $cross_result\"}"
        log "FAIL" "e2e $dse_name → $env_name FAIL ($cross_result)"
      fi
    done <<< "$svc_envs"
  done

  e2e_issues="$e2e_issues]"
  dur=$(( $(now_ms) - t0 ))

  if [ "$e2e_fail" -eq 0 ] && [ "$e2e_pass" -gt 0 ]; then
    E2E_OK=$((E2E_OK + 1))
    emit "$repo_url" "e2e" "pass" "${e2e_pass} checks passed, 0 failed" "$dur" "0" "$e2e_issues"
    log "PASS" "e2e networking — ${e2e_pass} passed, 0 failed"
  elif [ "$e2e_pass" -eq 0 ] && [ "$e2e_fail" -eq 0 ]; then
    emit "$repo_url" "e2e" "skip" "no testable endpoints" "$dur"
    log "SKIP" "e2e — no testable endpoints"
  else
    emit "$repo_url" "e2e" "fail" "${e2e_pass} passed, ${e2e_fail} failed" "$dur" "0" "$e2e_issues"
    log "FAIL" "e2e networking — ${e2e_pass} passed, ${e2e_fail} failed"
  fi

  # ── 9. Cleanup ──────────────────────────────────────────────
  cleanup_dse "$repo_name"
  rm -rf "$clone_dir"
}

# ── Cleanup DSEs for a repo ──────────────────────────────────────

cleanup_dse() {
  local prefix="$1"
  log "CLEAN" "deleting DSEs with prefix=$prefix"
  kubectl delete dse -n "$NAMESPACE" -l "kindling-fuzz/prefix=$prefix" \
    --timeout=60s >/dev/null 2>&1 || true
  # Wait a moment for cascade deletion
  sleep 3
  # Force-delete any lingering pods
  kubectl delete pods -n "$NAMESPACE" -l "kindling-fuzz/prefix=$prefix" \
    --force --grace-period=0 >/dev/null 2>&1 || true
}

# ── Main ─────────────────────────────────────────────────────────

log "START" "Fuzz testing kindling generate (full e2e)"
log "INFO" "Repos: $REPOS_FILE"
log "INFO" "Output: $OUTPUT_DIR"
log "INFO" "Kindling: $($KINDLING version 2>/dev/null || echo "$KINDLING")"
log "INFO" "Cluster: $CLUSTER_NAME"
log "INFO" "Namespace: $NAMESPACE"
log "INFO" "Skip e2e: $SKIP_E2E"

# Verify cluster is reachable (unless skip_e2e)
if [ "$SKIP_E2E" != "1" ]; then
  if ! kubectl cluster-info --context "kind-$CLUSTER_NAME" >/dev/null 2>&1; then
    log "FATAL" "Kind cluster '$CLUSTER_NAME' is not reachable."
    log "FATAL" "Run: kindling init   or   FUZZ_CLUSTER=dev ./run.sh ..."
    log "FATAL" "Or set SKIP_E2E=1 for static-only mode."
    exit 1
  fi
  # Verify CRDs are installed
  if ! kubectl get crd devstagingenvironments.apps.example.com >/dev/null 2>&1; then
    log "FATAL" "DSE CRD not installed. Run: kindling init"
    exit 1
  fi
  log "PASS" "cluster reachable, CRDs installed"
fi

while IFS= read -r line; do
  # Skip comments and blank lines
  line=$(echo "$line" | sed 's/#.*//' | xargs)
  [ -z "$line" ] && continue
  test_repo "$line"
done < "$REPOS_FILE"

# ── Summary ──────────────────────────────────────────────────────

log "DONE" "════════════════════════════════════════"
log "DONE" "Total repos:         $TOTAL"
log "DONE" "Generate OK:         $GENERATE_OK / $TOTAL"
log "DONE" "Valid YAML:          $YAML_OK / $GENERATE_OK"
log "DONE" "Static analysis OK:  $STATIC_NET_OK / $YAML_OK"
log "DONE" "Docker build OK:     $BUILD_OK"
log "DONE" "Deploy (DSE) OK:     $DEPLOY_OK / $YAML_OK"
log "DONE" "e2e networking OK:   $E2E_OK / $DEPLOY_OK"
log "DONE" "════════════════════════════════════════"

# Write summary JSON
cat > "$OUTPUT_DIR/summary.json" <<EOF
{
  "total": $TOTAL,
  "generate_ok": $GENERATE_OK,
  "yaml_ok": $YAML_OK,
  "static_net_ok": $STATIC_NET_OK,
  "build_ok": $BUILD_OK,
  "deploy_ok": $DEPLOY_OK,
  "e2e_ok": $E2E_OK,
  "generate_rate": "$(python3 -c "print(f'{$GENERATE_OK/$TOTAL*100:.1f}' if $TOTAL > 0 else '?')")%",
  "e2e_rate": "$(python3 -c "print(f'{$E2E_OK/$DEPLOY_OK*100:.1f}' if $DEPLOY_OK > 0 else '?')")%"
}
EOF

log "DONE" "Results: $RESULTS"
log "DONE" "Summary: $OUTPUT_DIR/summary.json"
