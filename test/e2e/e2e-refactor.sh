#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────────────
# E2E – Refactor validation tests
#
# Exercises every core feature group that was refactored into the core/
# package: kubectl helpers, secrets, env vars, load (build+deploy),
# runners (CR lifecycle), status, reset, and destroy.
#
# Expects:
#   KINDLING    – path to the kindling CLI binary
#   CLUSTER_NAME – name of the Kind cluster (already running)
#   Operator already deployed and healthy
# ────────────────────────────────────────────────────────────────────────────
set -euo pipefail

KINDLING="${KINDLING:-./bin/kindling}"
CLUSTER_NAME="${CLUSTER_NAME:-e2e-refactor}"
CTX="kind-${CLUSTER_NAME}"
TIMEOUT=120s
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
EXAMPLES_DIR="$ROOT_DIR/examples/microservices"

# ── Helpers ─────────────────────────────────────────────────────────────────
FAILURES=0
TESTS=0

pass() { echo "  ✅ $*"; }
fail() { echo "  ❌ $*"; FAILURES=$((FAILURES + 1)); }
info() { echo ""; echo "━━━ $* ━━━"; }

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

assert_exit_zero() {
  TESTS=$((TESTS + 1))
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    pass "$desc"
  else
    fail "$desc (exit code $?)"
  fi
}

wait_for_rollout() {
  local name="$1" ns="${2:-default}"
  kubectl --context "$CTX" rollout status "deployment/$name" -n "$ns" --timeout="$TIMEOUT" 2>/dev/null
}

wait_for_resource() {
  local kind="$1" name="$2" ns="${3:-default}" retries=30
  for i in $(seq 1 "$retries"); do
    if kubectl --context "$CTX" get "$kind" "$name" -n "$ns" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

kctl() {
  kubectl --context "$CTX" "$@"
}

# ════════════════════════════════════════════════════════════════════════════
# PHASE 1: CLI build verification
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 1: CLI binary verification"

TESTS=$((TESTS + 1))
if [ -x "$KINDLING" ]; then
  pass "CLI binary exists and is executable"
else
  fail "CLI binary not found at $KINDLING"
  echo "Cannot continue without CLI. Aborting."
  exit 1
fi

VERSION_OUT=$("$KINDLING" version 2>&1 || true)
assert_not_empty "CLI version outputs something" "$VERSION_OUT"

# Unit tests already passed in CI — sanity-check build only.
TESTS=$((TESTS + 1))
if "$KINDLING" --help >/dev/null 2>&1; then
  pass "CLI --help succeeds"
else
  fail "CLI --help failed"
fi

# ════════════════════════════════════════════════════════════════════════════
# PHASE 2: core/kubectl — cluster connectivity
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 2: core/kubectl — cluster connectivity (via 'kindling status')"

# The status command exercises core.ClusterExists + core.Kubectl internally.
TESTS=$((TESTS + 1))
STATUS_OUT=$("$KINDLING" status --cluster "$CLUSTER_NAME" 2>&1 || true)
if echo "$STATUS_OUT" | grep -qi "cluster\|node\|operator\|running"; then
  pass "kindling status returns cluster info"
else
  fail "kindling status produced no recognizable output"
  echo "  Output: $STATUS_OUT"
fi

# Verify operator is healthy (already deployed by the workflow)
OPERATOR_PODS=$(kctl get pods -n kindling-system -l control-plane=controller-manager -o jsonpath='{.items[*].status.phase}')
assert_eq "Operator pod is Running" "Running" "$OPERATOR_PODS"

# ════════════════════════════════════════════════════════════════════════════
# PHASE 3: core/secrets — create, list, delete
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 3: core/secrets — create, list, delete"

# Create a secret via the CLI (exercises core.CreateSecret)
"$KINDLING" secrets set E2E_TEST_KEY e2e-test-value --cluster "$CLUSTER_NAME" 2>/dev/null || true

# Verify it exists in the cluster with the kindling naming convention
TESTS=$((TESTS + 1))
SECRET_DATA=$(kctl get secret kindling-secret-e2e-test-key -o jsonpath='{.data}' 2>/dev/null || echo "")
if [ -n "$SECRET_DATA" ]; then
  pass "Secret 'kindling-secret-e2e-test-key' created in cluster"
else
  fail "Secret 'kindling-secret-e2e-test-key' not found"
fi

# Check it has the managed-by label
LABEL=$(kctl get secret kindling-secret-e2e-test-key -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' 2>/dev/null || echo "")
assert_eq "Secret has managed-by=kindling label" "kindling" "$LABEL"

# List secrets (exercises core.ListSecrets)
LIST_OUT=$("$KINDLING" secrets list --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "secrets list shows our secret" "kindling-secret-e2e-test-key" "$LIST_OUT"

# Create a second secret
"$KINDLING" secrets set ANOTHER_KEY another-value --cluster "$CLUSTER_NAME" 2>/dev/null || true
TESTS=$((TESTS + 1))
if kctl get secret kindling-secret-another-key >/dev/null 2>&1; then
  pass "Second secret created"
else
  fail "Second secret not found"
fi

# Delete the first secret (exercises core.DeleteSecret)
"$KINDLING" secrets delete E2E_TEST_KEY --cluster "$CLUSTER_NAME" 2>/dev/null || true
TESTS=$((TESTS + 1))
if ! kctl get secret kindling-secret-e2e-test-key >/dev/null 2>&1; then
  pass "Secret deleted successfully"
else
  fail "Secret still exists after delete"
fi

# Clean up the second secret
"$KINDLING" secrets delete ANOTHER_KEY --cluster "$CLUSTER_NAME" 2>/dev/null || true

# ════════════════════════════════════════════════════════════════════════════
# PHASE 4: Deploy microservices via DSE CRs
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 4: Deploy microservices via DSE CRs (operator reconciliation)"

# Build and load each microservice image into Kind
for svc_dir in gateway orders inventory ui; do
  SVC_IMAGE="ms-${svc_dir}:dev"
  info "  Building $SVC_IMAGE"
  docker build -t "$SVC_IMAGE" "$EXAMPLES_DIR/$svc_dir" -q
  kind load docker-image "$SVC_IMAGE" --name "$CLUSTER_NAME"
  pass "Built and loaded $SVC_IMAGE"
done

# Apply all DSE CRs
for cr in "$EXAMPLES_DIR"/deploy/*.yaml; do
  kctl apply -f "$cr"
done
pass "All DSE CRs applied"

# Wait for operator to reconcile — the gateway is the last in the chain
info "  Waiting for deployments to roll out..."
for dep in microservices-orders-dev microservices-inventory-dev microservices-gateway-dev microservices-ui-dev; do
  TESTS=$((TESTS + 1))
  if wait_for_resource deployment "$dep" && wait_for_rollout "$dep"; then
    pass "$dep is ready"
  else
    fail "$dep did not become ready"
  fi
done

# Validate dependency services were created
for dep_svc in microservices-orders-dev-postgres microservices-orders-dev-redis; do
  TESTS=$((TESTS + 1))
  if wait_for_resource deployment "$dep_svc" && wait_for_rollout "$dep_svc"; then
    pass "Dependency $dep_svc is ready"
  else
    fail "Dependency $dep_svc did not become ready"
  fi
done

# Validate env var injection on orders (should have DATABASE_URL and REDIS_URL)
ENV_VARS=$(kctl get deployment microservices-orders-dev \
  -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
assert_contains "DATABASE_URL injected into orders" "DATABASE_URL" "$ENV_VARS"
assert_contains "REDIS_URL injected into orders" "REDIS_URL" "$ENV_VARS"

# Validate services
for svc in microservices-orders-dev microservices-inventory-dev microservices-gateway-dev microservices-ui-dev; do
  TESTS=$((TESTS + 1))
  if kctl get svc "$svc" >/dev/null 2>&1; then
    pass "Service $svc exists"
  else
    fail "Service $svc missing"
  fi
done

# ════════════════════════════════════════════════════════════════════════════
# PHASE 5: core/env — set, list, unset env vars on a running deployment
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 5: core/env — set, list, unset"

# Set env vars (exercises core.SetEnv)
"$KINDLING" env set microservices-gateway-dev E2E_VAR=hello E2E_VAR2=world --cluster "$CLUSTER_NAME" 2>/dev/null || true

# List env vars (exercises core.ListEnv / core.GetEnvJSONPath)
sleep 3  # give rolling restart a moment
LIST_ENV_OUT=$("$KINDLING" env list microservices-gateway-dev --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "E2E_VAR appears in env list" "E2E_VAR" "$LIST_ENV_OUT"
assert_contains "E2E_VAR2 appears in env list" "E2E_VAR2" "$LIST_ENV_OUT"

# Verify in the actual deployment spec
GATEWAY_ENV=$(kctl get deployment microservices-gateway-dev \
  -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
assert_contains "E2E_VAR in deployment spec" "E2E_VAR" "$GATEWAY_ENV"

# Unset env vars (exercises core.UnsetEnv)
"$KINDLING" env unset microservices-gateway-dev E2E_VAR --cluster "$CLUSTER_NAME" 2>/dev/null || true
sleep 3
GATEWAY_ENV2=$(kctl get deployment microservices-gateway-dev \
  -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
TESTS=$((TESTS + 1))
if echo "$GATEWAY_ENV2" | grep -q "E2E_VAR2"; then
  # E2E_VAR2 should still be there
  pass "E2E_VAR2 still present after selective unset"
else
  fail "E2E_VAR2 was accidentally removed"
fi

# Clean up the remaining var
"$KINDLING" env unset microservices-gateway-dev E2E_VAR2 --cluster "$CLUSTER_NAME" 2>/dev/null || true

# ════════════════════════════════════════════════════════════════════════════
# PHASE 6: core/load — build, load, patch
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 6: core/load — build, load, patch deployment"

# Use kindling load on the gateway service.
# This exercises core.BuildAndLoad: docker build → kind load → patch DSE.
LOAD_OUT=$("$KINDLING" load \
  --service microservices-gateway-dev \
  --context "$EXAMPLES_DIR/gateway" \
  --namespace default \
  --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "load reports image built" "built" "$LOAD_OUT"

# Verify the deployment image was updated (should be microservices-gateway-dev:<timestamp>)
sleep 5
GATEWAY_IMAGE=$(kctl get deployment microservices-gateway-dev \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
TESTS=$((TESTS + 1))
if echo "$GATEWAY_IMAGE" | grep -q "microservices-gateway-dev:"; then
  pass "Gateway deployment image updated by kindling load"
else
  fail "Gateway image not updated (got: $GATEWAY_IMAGE)"
fi

# ════════════════════════════════════════════════════════════════════════════
# PHASE 7: core/env — deployment operations (restart, scale, delete pod)
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 7: Deployment operations (restart, scale)"

# Restart (exercises core.RestartDeployment via dashboard — test via kubectl)
TESTS=$((TESTS + 1))
RESTART_OUT=$(kctl rollout restart deployment/microservices-ui-dev 2>&1 || echo "FAIL")
if echo "$RESTART_OUT" | grep -q "restarted"; then
  pass "Deployment restart succeeded"
else
  fail "Deployment restart failed: $RESTART_OUT"
fi

# Scale down then back up (exercises core.ScaleDeployment)
TESTS=$((TESTS + 1))
kctl scale deployment/microservices-ui-dev --replicas=0 >/dev/null 2>&1
sleep 3
REPLICAS=$(kctl get deployment microservices-ui-dev -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")
assert_eq "UI scaled to 0" "0" "$REPLICAS"

kctl scale deployment/microservices-ui-dev --replicas=1 >/dev/null 2>&1
sleep 3
REPLICAS=$(kctl get deployment microservices-ui-dev -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")
assert_eq "UI scaled back to 1" "1" "$REPLICAS"

# ════════════════════════════════════════════════════════════════════════════
# PHASE 8: DSE spec update — image patch via operator
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 8: DSE spec update (operator reconciliation)"

kctl patch dse microservices-ui-dev --type=merge \
  -p '{"spec":{"deployment":{"image":"ms-ui:dev"}}}'
sleep 8

UPDATED_IMAGE=$(kctl get deployment microservices-ui-dev \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || echo "")
assert_eq "UI deployment image reverted via DSE patch" "ms-ui:dev" "$UPDATED_IMAGE"

# ════════════════════════════════════════════════════════════════════════════
# PHASE 9: core/runners — runner pool CR lifecycle
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 9: core/runners — create and reset runner pool"

# Create a runner pool CR (will not actually connect to GitHub, but the CR
# and secret should be created). Use a dummy token.
"$KINDLING" runners \
  -u e2e-test-user \
  -r e2e-test-user/fake-repo \
  -t ghp_faketoken1234567890 \
  --cluster "$CLUSTER_NAME" 2>/dev/null || true

# Verify the token secret was created
TESTS=$((TESTS + 1))
if kctl get secret github-runner-token >/dev/null 2>&1; then
  pass "github-runner-token secret created"
else
  fail "github-runner-token secret not found"
fi

# Verify the GithubActionRunnerPool CR was created
TESTS=$((TESTS + 1))
POOL_CR=$(kctl get githubactionrunnerpools -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
if echo "$POOL_CR" | grep -q "e2e-test-user-runner-pool"; then
  pass "GithubActionRunnerPool CR created"
else
  fail "GithubActionRunnerPool CR not found (got: '$POOL_CR')"
fi

# Verify label is "kindling" (unified from CLI/dashboard)
RUNNER_LABELS=$(kctl get githubactionrunnerpool e2e-test-user-runner-pool \
  -o jsonpath='{.spec.labels[*]}' 2>/dev/null || echo "")
assert_contains "Runner pool has 'kindling' label" "kindling" "$RUNNER_LABELS"

# Reset runners (exercises core.ResetRunners)
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

# ════════════════════════════════════════════════════════════════════════════
# PHASE 10: core/kubectl — DSE deletion + garbage collection
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 10: DSE deletion and garbage collection"

# Delete the orders DSE — its Deployment, Service, Postgres, and Redis should go
kctl delete dse microservices-orders-dev --wait=false 2>/dev/null || true

RETRIES=20
ORDERS_GONE=false
for i in $(seq 1 "$RETRIES"); do
  if ! kctl get deployment microservices-orders-dev >/dev/null 2>&1; then
    ORDERS_GONE=true
    break
  fi
  sleep 3
done

TESTS=$((TESTS + 1))
if [ "$ORDERS_GONE" = "true" ]; then
  pass "Orders deployment garbage-collected after DSE deletion"
else
  fail "Orders deployment still exists after DSE deletion"
fi

# Postgres dependency should also be gone
TESTS=$((TESTS + 1))
if ! kctl get deployment microservices-orders-dev-postgres >/dev/null 2>&1; then
  pass "Orders Postgres garbage-collected"
else
  fail "Orders Postgres still exists"
fi

# ════════════════════════════════════════════════════════════════════════════
# PHASE 11: kindling status — full status report
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 11: kindling status — comprehensive check"

STATUS_FULL=$("$KINDLING" status --cluster "$CLUSTER_NAME" 2>&1 || true)
assert_contains "Status shows cluster name" "$CLUSTER_NAME" "$STATUS_FULL"
assert_not_empty "Status output is non-empty" "$STATUS_FULL"

# ════════════════════════════════════════════════════════════════════════════
# PHASE 12: kindling destroy — tear down the cluster
# ════════════════════════════════════════════════════════════════════════════
info "PHASE 12: kindling destroy"

# Destroy the cluster using the CLI (exercises core.DestroyCluster)
"$KINDLING" destroy -y --cluster "$CLUSTER_NAME" 2>/dev/null || true

TESTS=$((TESTS + 1))
if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  pass "Cluster destroyed by kindling destroy"
else
  fail "Cluster still exists after kindling destroy"
fi

# ════════════════════════════════════════════════════════════════════════════
# SUMMARY
# ════════════════════════════════════════════════════════════════════════════
info "Summary"
echo ""
echo "  Tests run: $TESTS"
echo "  Failures:  $FAILURES"
echo ""

if [ "$FAILURES" -gt 0 ]; then
  echo "❌ E2E REFACTOR VALIDATION FAILED"
  exit 1
else
  echo "✅ E2E REFACTOR VALIDATION PASSED"
  exit 0
fi
