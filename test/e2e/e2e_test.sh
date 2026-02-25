#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────────────
# End-to-end test suite for Kindling
#
# Three test tiers, run in order:
#
#   TIER 1 — Operator baseline (always runs)
#     Hardcoded DSE CR with nginx + postgres + redis. Tests reconciliation,
#     dependency provisioning, env injection, owner refs, status, spec
#     update, stale cleanup, garbage collection.
#
#   TIER 2 — CLI features (always runs)
#     Exercises the core/ package via the kindling binary: secrets CRUD,
#     env set/list/unset, load (build+kind load+patch), runners CR
#     lifecycle, reset, status.
#
#   TIER 3 — Generate pipeline (runs when FUZZ_API_KEY is set)
#     Runs `kindling generate` against examples/microservices, validates
#     the generated YAML, builds images, deploys from generated output,
#     and validates the deployments come up healthy. Skipped without an
#     API key so the test suite works offline.
#
# Usage:
#   make e2e                          # uses default cluster name
#   E2E_CLUSTER_NAME=my-e2e make e2e  # custom cluster name
#
# Env vars:
#   FUZZ_API_KEY     — LLM API key (enables tier 3)
#   FUZZ_PROVIDER    — openai (default) or anthropic
#   FUZZ_MODEL       — model override (optional)
# ────────────────────────────────────────────────────────────────────────────
set -euo pipefail

CLUSTER_NAME="${E2E_CLUSTER_NAME:-kindling-e2e}"
IMG="controller:latest"
TIMEOUT=180s
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
KINDLING="${KINDLING:-$ROOT_DIR/bin/kindling}"
EXAMPLES_DIR="$ROOT_DIR/examples/microservices"

# ── Helpers ─────────────────────────────────────────────────────────────────

pass() { echo "  ✅ $*"; }
fail() { echo "  ❌ $*"; FAILURES=$((FAILURES + 1)); }
info() { echo ""; echo "━━━ $* ━━━"; }

FAILURES=0
TESTS=0

assert_eq() {
  TESTS=$((TESTS + 1))
  local desc="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    pass "$desc"
  else
    fail "$desc (expected='$expected', got='$actual')"
  fi
}

assert_contains() {
  TESTS=$((TESTS + 1))
  local desc="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -q "$needle"; then
    pass "$desc"
  else
    fail "$desc (expected to contain '$needle')"
  fi
}

assert_not_empty() {
  TESTS=$((TESTS + 1))
  local desc="$1" value="$2"
  if [ -n "$value" ]; then
    pass "$desc"
  else
    fail "$desc (was empty)"
  fi
}

wait_for_rollout() {
  local name="$1" ns="${2:-default}"
  kubectl rollout status "deployment/$name" -n "$ns" --timeout="$TIMEOUT" 2>/dev/null
}

wait_for_resource() {
  local kind="$1" name="$2" ns="${3:-default}" retries=30
  for i in $(seq 1 "$retries"); do
    if kubectl get "$kind" "$name" -n "$ns" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

# ── Cleanup trap ────────────────────────────────────────────────────────────

kctl() {
  kubectl --context "kind-${CLUSTER_NAME}" "$@"
}

cleanup() {
  info "Cleanup"
  echo "  Deleting Kind cluster '$CLUSTER_NAME'..."
  kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

# ── 1. Create the cluster ──────────────────────────────────────────────────
info "1. Creating Kind cluster '$CLUSTER_NAME'"

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "  Cluster already exists, deleting first..."
  kind delete cluster --name "$CLUSTER_NAME"
fi

kind create cluster --name "$CLUSTER_NAME" --wait 60s
kubectl cluster-info --context "kind-${CLUSTER_NAME}" >/dev/null 2>&1
pass "Kind cluster is running"

# ── 2. Build and deploy the operator ───────────────────────────────────────
info "2. Building and deploying operator"

cd "$ROOT_DIR"

# Build the operator image (skip if CI already built it)
if docker image inspect "$IMG" >/dev/null 2>&1; then
  pass "Operator image already exists (skipping build)"
else
  make docker-build IMG="$IMG"
  pass "Operator image built"
fi

# Load it into the Kind cluster
kind load docker-image "$IMG" --name "$CLUSTER_NAME"
pass "Image loaded into Kind"

# Install CRDs and deploy
make install
make deploy IMG="$IMG"
pass "CRDs and operator deployed"

# Wait for the controller-manager
kubectl rollout status deployment/kindling-controller-manager -n kindling-system --timeout="$TIMEOUT"
pass "Controller manager is running"

# ── 3. Apply a test DevStagingEnvironment CR ───────────────────────────────
info "3. Applying test DevStagingEnvironment"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-test-app
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    replicas: 1
    healthCheck:
      path: /
  service:
    port: 80
    type: ClusterIP
  dependencies:
    - type: postgres
    - type: redis
EOF
pass "CR applied"

# ── 4. Validate child resources ────────────────────────────────────────────
info "4. Validating child resources"

# App Deployment
wait_for_resource deployment e2e-test-app
TESTS=$((TESTS + 1))
if wait_for_rollout e2e-test-app; then
  pass "App Deployment is ready"
else
  fail "App Deployment did not become ready"
fi

# App Service
wait_for_resource service e2e-test-app
TESTS=$((TESTS + 1))
SVC_PORT=$(kubectl get svc e2e-test-app -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
assert_eq "App Service port" "80" "$SVC_PORT"

# Postgres Deployment
wait_for_resource deployment e2e-test-app-postgres
TESTS=$((TESTS + 1))
if wait_for_rollout e2e-test-app-postgres; then
  pass "Postgres Deployment is ready"
else
  fail "Postgres Deployment did not become ready"
fi

# Postgres Service
wait_for_resource service e2e-test-app-postgres
PG_PORT=$(kubectl get svc e2e-test-app-postgres -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
assert_eq "Postgres Service port" "5432" "$PG_PORT"

# Redis Deployment
wait_for_resource deployment e2e-test-app-redis
TESTS=$((TESTS + 1))
if wait_for_rollout e2e-test-app-redis; then
  pass "Redis Deployment is ready"
else
  fail "Redis Deployment did not become ready"
fi

# Redis Service
wait_for_resource service e2e-test-app-redis
REDIS_PORT=$(kubectl get svc e2e-test-app-redis -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
assert_eq "Redis Service port" "6379" "$REDIS_PORT"

# ── 5. Validate init containers (dependency wait) ─────────────────────────
info "5. Validating init containers"

INIT_CONTAINERS=$(kubectl get deployment e2e-test-app -o jsonpath='{.spec.template.spec.initContainers[*].name}' 2>/dev/null || echo "")
assert_contains "Init container for postgres exists" "wait-for-postgres" "$INIT_CONTAINERS"
assert_contains "Init container for redis exists" "wait-for-redis" "$INIT_CONTAINERS"

# ── 6. Validate env var injection ──────────────────────────────────────────
info "6. Validating env var injection"

ENV_VARS=$(kubectl get deployment e2e-test-app -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
assert_contains "DATABASE_URL is injected" "DATABASE_URL" "$ENV_VARS"
assert_contains "REDIS_URL is injected" "REDIS_URL" "$ENV_VARS"

DATABASE_URL=$(kubectl get deployment e2e-test-app -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DATABASE_URL")].value}' 2>/dev/null || echo "")
assert_contains "DATABASE_URL contains postgres://" "postgres://" "$DATABASE_URL"

REDIS_URL=$(kubectl get deployment e2e-test-app -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="REDIS_URL")].value}' 2>/dev/null || echo "")
assert_contains "REDIS_URL contains redis://" "redis://" "$REDIS_URL"

# ── 7. Validate owner references (garbage collection) ─────────────────────
info "7. Validating owner references"

for resource in deployment/e2e-test-app deployment/e2e-test-app-postgres deployment/e2e-test-app-redis \
                service/e2e-test-app service/e2e-test-app-postgres service/e2e-test-app-redis; do
  OWNER=$(kubectl get "$resource" -o jsonpath='{.metadata.ownerReferences[0].name}' 2>/dev/null || echo "")
  TESTS=$((TESTS + 1))
  if [ "$OWNER" = "e2e-test-app" ]; then
    pass "$resource owned by e2e-test-app"
  else
    fail "$resource not owned by e2e-test-app (owner='$OWNER')"
  fi
done

# ── 8. Validate status conditions ─────────────────────────────────────────
info "8. Validating CR status"

# Give the status a moment to converge
sleep 5

DEPLOY_READY=$(kubectl get dse e2e-test-app -o jsonpath='{.status.deploymentReady}' 2>/dev/null || echo "")
assert_eq "status.deploymentReady" "true" "$DEPLOY_READY"

SVC_READY=$(kubectl get dse e2e-test-app -o jsonpath='{.status.serviceReady}' 2>/dev/null || echo "")
assert_eq "status.serviceReady" "true" "$SVC_READY"

DEPS_READY=$(kubectl get dse e2e-test-app -o jsonpath='{.status.dependenciesReady}' 2>/dev/null || echo "")
assert_eq "status.dependenciesReady" "true" "$DEPS_READY"

# ── 9. Test spec update ───────────────────────────────────────────────────
info "9. Testing spec update (change image)"

kubectl patch dse e2e-test-app --type=merge -p '{"spec":{"deployment":{"image":"nginx:1.24"}}}'
sleep 5

UPDATED_IMAGE=$(kubectl get deployment e2e-test-app -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
assert_eq "Deployment image updated" "nginx:1.24" "$UPDATED_IMAGE"

# ── 10. Test dependency removal (stale cleanup) ───────────────────────────
info "10. Testing dependency removal (stale cleanup)"

kubectl patch dse e2e-test-app --type=merge -p '{"spec":{"dependencies":[{"type":"postgres"}]}}'

# Redis resources should be cleaned up
RETRIES=15
REDIS_GONE=false
for i in $(seq 1 "$RETRIES"); do
  if ! kubectl get deployment e2e-test-app-redis >/dev/null 2>&1; then
    REDIS_GONE=true
    break
  fi
  sleep 2
done

TESTS=$((TESTS + 1))
if [ "$REDIS_GONE" = "true" ]; then
  pass "Redis Deployment pruned after removal from spec"
else
  fail "Redis Deployment was not cleaned up"
fi

# Postgres should still exist
TESTS=$((TESTS + 1))
if kubectl get deployment e2e-test-app-postgres >/dev/null 2>&1; then
  pass "Postgres Deployment still exists"
else
  fail "Postgres Deployment was incorrectly removed"
fi

# ── 11. Test CR deletion ──────────────────────────────────────────────────
info "11. Testing CR deletion (garbage collection)"

kubectl delete dse e2e-test-app --wait=false

# Wait for child resources to be garbage-collected
RETRIES=20
ALL_GONE=false
for i in $(seq 1 "$RETRIES"); do
  REMAINING=$(kubectl get deploy -l app.kubernetes.io/instance=e2e-test-app --no-headers 2>/dev/null | wc -l | tr -d ' ')
  if [ "$REMAINING" = "0" ]; then
    ALL_GONE=true
    break
  fi
  sleep 2
done

TESTS=$((TESTS + 1))
if [ "$ALL_GONE" = "true" ]; then
  pass "All child resources garbage-collected after CR deletion"
else
  fail "Some child resources remain after CR deletion"
fi

# ════════════════════════════════════════════════════════════════════════════
# TIER 2: CLI features (exercises core/ package via kindling binary)
# ════════════════════════════════════════════════════════════════════════════

# ── 12. CLI binary verification ────────────────────────────────────────────
info "12. CLI binary verification"

TESTS=$((TESTS + 1))
if [ -x "$KINDLING" ]; then
  pass "CLI binary exists and is executable"
else
  # Try building it
  echo "  CLI not found at $KINDLING — building..."
  cd "$ROOT_DIR" && make cli
  if [ -x "$KINDLING" ]; then
    pass "CLI binary built successfully"
  else
    fail "CLI binary not found and build failed"
    # Can't run CLI tests without the binary — skip to summary
    info "Skipping CLI tests (no binary)"
    # Jump to summary
    info "Summary"
    echo ""
    echo "  Tests run: $TESTS"
    echo "  Failures:  $FAILURES"
    echo ""
    if [ "$FAILURES" -gt 0 ]; then
      echo "❌ E2E FAILED"
      exit 1
    else
      echo "✅ E2E PASSED"
      exit 0
    fi
  fi
fi

VERSION_OUT=$("$KINDLING" version 2>&1 || true)
assert_not_empty "CLI version outputs something" "$VERSION_OUT"

# ── 13. kindling status ───────────────────────────────────────────────────
info "13. kindling status"

TESTS=$((TESTS + 1))
STATUS_OUT=$("$KINDLING" status --cluster "$CLUSTER_NAME" 2>&1 || true)
if echo "$STATUS_OUT" | grep -qi "cluster\|node\|operator\|running"; then
  pass "kindling status returns cluster info"
else
  fail "kindling status produced no recognizable output"
fi

# ── 14. core/secrets — create, list, delete ────────────────────────────────
info "14. core/secrets — create, list, delete"

"$KINDLING" secrets set E2E_TEST_KEY e2e-test-value --cluster "$CLUSTER_NAME" 2>/dev/null || true

TESTS=$((TESTS + 1))
SECRET_DATA=$(kctl get secret kindling-secret-e2e-test-key -o jsonpath='{.data}' 2>/dev/null || echo "")
if [ -n "$SECRET_DATA" ]; then
  pass "Secret 'kindling-secret-e2e-test-key' created in cluster"
else
  fail "Secret 'kindling-secret-e2e-test-key' not found"
fi

LABEL=$(kctl get secret kindling-secret-e2e-test-key -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' 2>/dev/null || echo "")
assert_eq "Secret has managed-by=kindling label" "kindling" "$LABEL"

LIST_OUT=$("$KINDLING" secrets list --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "secrets list shows our secret" "kindling-secret-e2e-test-key" "$LIST_OUT"

"$KINDLING" secrets set ANOTHER_KEY another-value --cluster "$CLUSTER_NAME" 2>/dev/null || true
TESTS=$((TESTS + 1))
if kctl get secret kindling-secret-another-key >/dev/null 2>&1; then
  pass "Second secret created"
else
  fail "Second secret not found"
fi

"$KINDLING" secrets delete E2E_TEST_KEY --cluster "$CLUSTER_NAME" 2>/dev/null || true
TESTS=$((TESTS + 1))
if ! kctl get secret kindling-secret-e2e-test-key >/dev/null 2>&1; then
  pass "Secret deleted successfully"
else
  fail "Secret still exists after delete"
fi

"$KINDLING" secrets delete ANOTHER_KEY --cluster "$CLUSTER_NAME" 2>/dev/null || true

# ── 15. Deploy microservices for CLI tests ─────────────────────────────────
info "15. Deploy microservices (for CLI feature tests)"

for svc_dir in gateway orders inventory ui; do
  SVC_IMAGE="ms-${svc_dir}:dev"
  docker build -t "$SVC_IMAGE" "$EXAMPLES_DIR/$svc_dir" -q
  kind load docker-image "$SVC_IMAGE" --name "$CLUSTER_NAME"
done
pass "All microservice images built and loaded"

for cr in "$EXAMPLES_DIR"/deploy/*.yaml; do
  kctl apply -f "$cr"
done
pass "All DSE CRs applied"

# Give the operator time to create dependency pods (Postgres, Redis, MongoDB)
echo "  Waiting for dependency pods to schedule..."
sleep 15

for dep in microservices-orders-dev microservices-inventory-dev microservices-gateway-dev microservices-ui-dev; do
  TESTS=$((TESTS + 1))
  if wait_for_resource deployment "$dep" && kubectl rollout status "deployment/$dep" --timeout=300s 2>/dev/null; then
    pass "$dep is ready"
  else
    fail "$dep did not become ready ($(kubectl get deployment "$dep" -o jsonpath='{.status.conditions[*].message}' 2>/dev/null || echo 'unknown'))"
  fi
done

# ── 16. core/env — set, list, unset ───────────────────────────────────────
info "16. core/env — set, list, unset"

"$KINDLING" env set microservices-gateway-dev E2E_VAR=hello E2E_VAR2=world --cluster "$CLUSTER_NAME" 2>/dev/null || true
sleep 3

LIST_ENV_OUT=$("$KINDLING" env list microservices-gateway-dev --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "E2E_VAR appears in env list" "E2E_VAR" "$LIST_ENV_OUT"
assert_contains "E2E_VAR2 appears in env list" "E2E_VAR2" "$LIST_ENV_OUT"

GATEWAY_ENV=$(kctl get deployment microservices-gateway-dev \
  -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
assert_contains "E2E_VAR in deployment spec" "E2E_VAR" "$GATEWAY_ENV"

"$KINDLING" env unset microservices-gateway-dev E2E_VAR --cluster "$CLUSTER_NAME" 2>/dev/null || true
sleep 3
GATEWAY_ENV2=$(kctl get deployment microservices-gateway-dev \
  -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
TESTS=$((TESTS + 1))
if echo "$GATEWAY_ENV2" | grep -q "E2E_VAR2"; then
  pass "E2E_VAR2 still present after selective unset"
else
  fail "E2E_VAR2 was accidentally removed"
fi

"$KINDLING" env unset microservices-gateway-dev E2E_VAR2 --cluster "$CLUSTER_NAME" 2>/dev/null || true

# ── 17. core/load — build, load, patch ─────────────────────────────────────
info "17. core/load — build, load, patch deployment"

LOAD_OUT=$("$KINDLING" load \
  --service microservices-gateway-dev \
  --context "$EXAMPLES_DIR/gateway" \
  --namespace default \
  --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "load reports image built" "built" "$LOAD_OUT"

sleep 5
GATEWAY_IMAGE=$(kctl get deployment microservices-gateway-dev \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
TESTS=$((TESTS + 1))
if echo "$GATEWAY_IMAGE" | grep -q "microservices-gateway-dev:"; then
  pass "Gateway deployment image updated by kindling load"
else
  fail "Gateway image not updated (got: $GATEWAY_IMAGE)"
fi

# ── 18. core/runners — runner pool CR lifecycle ────────────────────────────
info "18. core/runners — create and reset runner pool"

"$KINDLING" runners \
  -u e2e-test-user \
  -r e2e-test-user/fake-repo \
  -t ghp_faketoken1234567890 \
  --cluster "$CLUSTER_NAME" 2>/dev/null || true

TESTS=$((TESTS + 1))
if kctl get secret github-runner-token >/dev/null 2>&1; then
  pass "github-runner-token secret created"
else
  fail "github-runner-token secret not found"
fi

TESTS=$((TESTS + 1))
POOL_CR=$(kctl get githubactionrunnerpools -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
if echo "$POOL_CR" | grep -q "e2e-test-user-runner-pool"; then
  pass "GithubActionRunnerPool CR created"
else
  fail "GithubActionRunnerPool CR not found (got: '$POOL_CR')"
fi

RUNNER_LABELS=$(kctl get githubactionrunnerpool e2e-test-user-runner-pool \
  -o jsonpath='{.spec.labels[*]}' 2>/dev/null || echo "")
assert_contains "Runner pool has 'kindling' label" "kindling" "$RUNNER_LABELS"

"$KINDLING" reset -y --cluster "$CLUSTER_NAME" 2>/dev/null || true

TESTS=$((TESTS + 1))
POOL_AFTER=$(kctl get githubactionrunnerpools --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$POOL_AFTER" = "0" ]; then
  pass "All runner pools removed by reset"
else
  fail "Runner pools still exist after reset ($POOL_AFTER remaining)"
fi

TESTS=$((TESTS + 1))
if ! kctl get secret github-runner-token >/dev/null 2>&1; then
  pass "github-runner-token secret removed by reset"
else
  fail "github-runner-token secret still exists after reset"
fi

# ── 19. DSE cleanup from CLI tests ────────────────────────────────────────
info "19. Cleaning up microservices DSEs"

for cr in "$EXAMPLES_DIR"/deploy/*.yaml; do
  kctl delete -f "$cr" --wait=false 2>/dev/null || true
done
sleep 5
pass "Microservice DSEs cleaned up"

# ════════════════════════════════════════════════════════════════════════════
# TIER 3: Generate pipeline
# ════════════════════════════════════════════════════════════════════════════
# The full generate → static analysis → deploy → e2e pipeline is now
# handled by run.sh (invoked as a separate workflow step against
# repos-e2e.txt). See .github/workflows/fuzz.yml.
#
# This keeps e2e_test.sh focused on operator + CLI correctness while
# run.sh exercises the generate pipeline across multiple real-world repos.
# ════════════════════════════════════════════════════════════════════════════

# ── Summary ────────────────────────────────────────────────────────────────
info "Summary"
echo ""
echo "  Tests run: $TESTS"
echo "  Failures:  $FAILURES"
echo ""

if [ "$FAILURES" -gt 0 ]; then
  echo "❌ E2E FAILED"
  exit 1
else
  echo "✅ E2E PASSED"
  exit 0
fi
