#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────────────
# End-to-end test suite for Kindling
#
# Spins up a dedicated Kind cluster, deploys the operator, applies the
# sample-app DevStagingEnvironment CR, validates that all resources come
# up healthy, then tears everything down.
#
# Usage:
#   make e2e                          # uses default cluster name
#   E2E_CLUSTER_NAME=my-e2e make e2e  # custom cluster name
# ────────────────────────────────────────────────────────────────────────────
set -euo pipefail

CLUSTER_NAME="${E2E_CLUSTER_NAME:-kindling-e2e}"
IMG="controller:latest"
TIMEOUT=120s
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

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

# Build the operator image
make docker-build IMG="$IMG"
pass "Operator image built"

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
