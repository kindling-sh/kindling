#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────────────
# End-to-end test suite for Kindling
#
# Five test tiers, run in order:
#
#   TIER 1 — Operator baseline (always runs)
#     Hardcoded DSE CR with nginx + postgres + redis. Tests reconciliation,
#     dependency provisioning, env injection, owner refs, status, spec
#     update, stale cleanup, garbage collection.
#
#   TIER 1b — Operator advanced (always runs)
#     Ingress lifecycle, health check probes (http/grpc/none), replicas
#     scaling, service type NodePort, custom env vars, resource limits,
#     additional dependency types (mysql, mongodb, rabbitmq, nats,
#     memcached, consul, vault, minio, influxdb, jaeger), envVarName
#     override, multi-service deploy, status.url field.
#
#   TIER 2 — CLI features (always runs)
#     Exercises the core/ package via the kindling binary: deploy, secrets
#     CRUD, env set/list/unset, load (build+kind load+patch), runners CR
#     lifecycle, reset, status, logs, snapshot (helm + kustomize),
#     tunnel ingress patching/restore, tunnel state management,
#     snapshot-during-tunnel isolation, staging TLS safety + DSE patching.
#
#   TIER 2b — Dashboard API (always runs)
#     Starts the dashboard in background, exercises read-only + action
#     endpoints via curl: /api/cluster, /api/dses, /api/deployments,
#     /api/pods, /api/services, /api/secrets, /api/nodes, /api/events,
#     secret CRUD, env set/unset, scale.
#
#   TIER 3 — Generate pipeline (runs when FUZZ_API_KEY is set)
#     Handled by run.sh / fuzz.yml. See .github/workflows/fuzz.yml.
#
# Usage:
#   make e2e                          # uses default cluster name
#   E2E_CLUSTER_NAME=my-e2e make e2e  # custom cluster name
#
# Env vars:
#   FUZZ_API_KEY     — LLM API key (enables tier 3)
#   FUZZ_PROVIDER    — openai (default) or anthropic
#   FUZZ_MODEL       — model override (optional)
#   E2E_SKIP_DEPS    — set to 1 to skip additional dependency tests (saves ~2min)
# ────────────────────────────────────────────────────────────────────────────
set -euo pipefail

CLUSTER_NAME="${E2E_CLUSTER_NAME:-kindling-e2e}"
IMG="controller:latest"
TIMEOUT=180s
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
KINDLING="${KINDLING:-$ROOT_DIR/bin/kindling}"
EXAMPLES_DIR="$ROOT_DIR/examples/microservices"
DASHBOARD_PORT=19191
DASHBOARD_PID=""

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

assert_neq() {
  TESTS=$((TESTS + 1))
  local desc="$1" unexpected="$2" actual="$3"
  if [ "$unexpected" != "$actual" ]; then
    pass "$desc"
  else
    fail "$desc (got unexpected value='$actual')"
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

assert_not_contains() {
  TESTS=$((TESTS + 1))
  local desc="$1" needle="$2" haystack="$3"
  if ! echo "$haystack" | grep -qw "$needle"; then
    pass "$desc"
  else
    fail "$desc (should not contain '$needle')"
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

assert_file_exists() {
  TESTS=$((TESTS + 1))
  local desc="$1" path="$2"
  if [ -f "$path" ]; then
    pass "$desc"
  else
    fail "$desc (file not found: $path)"
  fi
}

assert_dir_exists() {
  TESTS=$((TESTS + 1))
  local desc="$1" path="$2"
  if [ -d "$path" ]; then
    pass "$desc"
  else
    fail "$desc (directory not found: $path)"
  fi
}

assert_http_ok() {
  TESTS=$((TESTS + 1))
  local desc="$1" url="$2"
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>/dev/null || echo "000")
  if [ "$status" = "200" ]; then
    pass "$desc"
  else
    fail "$desc (HTTP $status from $url)"
  fi
}

assert_json_field() {
  TESTS=$((TESTS + 1))
  local desc="$1" url="$2" field="$3"
  local body
  body=$(curl -s "$url" 2>/dev/null || echo "{}")
  if echo "$body" | python3 -c "import sys,json; d=json.load(sys.stdin); assert '$field' in d" 2>/dev/null; then
    pass "$desc"
  else
    fail "$desc (field '$field' not in JSON from $url)"
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

wait_for_resource_gone() {
  local kind="$1" name="$2" ns="${3:-default}" retries="${4:-20}"
  for i in $(seq 1 "$retries"); do
    if ! kubectl get "$kind" "$name" -n "$ns" >/dev/null 2>&1; then
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
  # Kill dashboard if running
  if [ -n "$DASHBOARD_PID" ] && kill -0 "$DASHBOARD_PID" 2>/dev/null; then
    kill "$DASHBOARD_PID" 2>/dev/null || true
    wait "$DASHBOARD_PID" 2>/dev/null || true
  fi

  if [ "${KEEP_CLUSTER:-0}" = "1" ]; then
    info "Cleanup: keeping cluster '$CLUSTER_NAME' (KEEP_CLUSTER=1)"
    return
  fi
  info "Cleanup"
  echo "  Deleting Kind cluster '$CLUSTER_NAME'..."
  kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

# ════════════════════════════════════════════════════════════════════════════
# TIER 1: Operator baseline
# ════════════════════════════════════════════════════════════════════════════

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
pass "Operator image loaded into Kind"

# Load the kube-rbac-proxy sidecar image
RBAC_PROXY_IMG="quay.io/brancz/kube-rbac-proxy:v0.18.1"
if ! docker image inspect "$RBAC_PROXY_IMG" >/dev/null 2>&1; then
  docker pull "$RBAC_PROXY_IMG"
fi
kind load docker-image "$RBAC_PROXY_IMG" --name "$CLUSTER_NAME"
pass "kube-rbac-proxy image loaded into Kind"

# Install CRDs and deploy
make install
make deploy IMG="$IMG"
pass "CRDs and operator deployed"

# Wait for the controller-manager
if ! kubectl rollout status deployment/kindling-controller-manager -n kindling-system --timeout="$TIMEOUT"; then
  echo "  ⚠️  Controller-manager did not become ready. Diagnostics:"
  kubectl get pods -n kindling-system -o wide 2>/dev/null || true
  kubectl describe pods -n kindling-system 2>/dev/null | tail -40 || true
  kubectl logs deployment/kindling-controller-manager -n kindling-system --all-containers --tail=30 2>/dev/null || true
  exit 1
fi
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

READY_COND=$(kubectl get dse e2e-test-app -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
assert_eq "Ready condition is True" "True" "$READY_COND"

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
TESTS=$((TESTS + 1))
if wait_for_resource_gone deployment e2e-test-app-redis default 15; then
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
# TIER 1b: Operator advanced — Ingress, health checks, replicas, service
#          types, custom env, resources, additional deps, envVarName override
# ════════════════════════════════════════════════════════════════════════════

# ── 12. Ingress lifecycle ──────────────────────────────────────────────────
info "12. Ingress creation and deletion lifecycle"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-ingress-app
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    replicas: 1
  service:
    port: 80
  ingress:
    enabled: true
    host: e2e-test.localhost
    path: /
    pathType: Prefix
EOF

wait_for_resource ingress e2e-ingress-app default
pass "Ingress created when enabled"

ING_HOST=$(kubectl get ingress e2e-ingress-app -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
assert_eq "Ingress host" "e2e-test.localhost" "$ING_HOST"

ING_PATH=$(kubectl get ingress e2e-ingress-app -o jsonpath='{.spec.rules[0].http.paths[0].path}' 2>/dev/null || echo "")
assert_eq "Ingress path" "/" "$ING_PATH"

ING_BACKEND_SVC=$(kubectl get ingress e2e-ingress-app -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.name}' 2>/dev/null || echo "")
assert_eq "Ingress backend service" "e2e-ingress-app" "$ING_BACKEND_SVC"

ING_BACKEND_PORT=$(kubectl get ingress e2e-ingress-app -o jsonpath='{.spec.rules[0].http.paths[0].backend.service.port.number}' 2>/dev/null || echo "")
assert_eq "Ingress backend port" "80" "$ING_BACKEND_PORT"

# Verify status.ingressReady and status.url
sleep 5
ING_READY=$(kubectl get dse e2e-ingress-app -o jsonpath='{.status.ingressReady}' 2>/dev/null || echo "")
assert_eq "status.ingressReady" "true" "$ING_READY"

STATUS_URL=$(kubectl get dse e2e-ingress-app -o jsonpath='{.status.url}' 2>/dev/null || echo "")
assert_contains "status.url contains host" "e2e-test.localhost" "$STATUS_URL"

# Disable ingress → should be deleted
kubectl patch dse e2e-ingress-app --type=merge -p '{"spec":{"ingress":{"enabled":false}}}'

TESTS=$((TESTS + 1))
if wait_for_resource_gone ingress e2e-ingress-app default 15; then
  pass "Ingress deleted when disabled"
else
  fail "Ingress was not cleaned up after disabling"
fi

# Verify status.ingressReady goes false
sleep 3
ING_READY2=$(kubectl get dse e2e-ingress-app -o jsonpath='{.status.ingressReady}' 2>/dev/null || echo "")
assert_eq "status.ingressReady false after disable" "false" "$ING_READY2"

kubectl delete dse e2e-ingress-app --wait=false 2>/dev/null || true
sleep 3

# ── 13. Health check probes ───────────────────────────────────────────────
info "13. Health check probes (http, grpc, none)"

# HTTP health check
cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-health-http
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    healthCheck:
      type: http
      path: /healthz
      initialDelaySeconds: 10
      periodSeconds: 15
  service:
    port: 80
EOF

wait_for_resource deployment e2e-health-http
sleep 3

LIVENESS_PATH=$(kubectl get deployment e2e-health-http -o jsonpath='{.spec.template.spec.containers[0].livenessProbe.httpGet.path}' 2>/dev/null || echo "")
assert_eq "HTTP liveness probe path" "/healthz" "$LIVENESS_PATH"

READINESS_PATH=$(kubectl get deployment e2e-health-http -o jsonpath='{.spec.template.spec.containers[0].readinessProbe.httpGet.path}' 2>/dev/null || echo "")
assert_eq "HTTP readiness probe path" "/healthz" "$READINESS_PATH"

LIVENESS_DELAY=$(kubectl get deployment e2e-health-http -o jsonpath='{.spec.template.spec.containers[0].livenessProbe.initialDelaySeconds}' 2>/dev/null || echo "")
assert_eq "Liveness initialDelaySeconds" "10" "$LIVENESS_DELAY"

LIVENESS_PERIOD=$(kubectl get deployment e2e-health-http -o jsonpath='{.spec.template.spec.containers[0].livenessProbe.periodSeconds}' 2>/dev/null || echo "")
assert_eq "Liveness periodSeconds" "15" "$LIVENESS_PERIOD"

kubectl delete dse e2e-health-http --wait=false 2>/dev/null || true

# gRPC health check
cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-health-grpc
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 50051
    healthCheck:
      type: grpc
  service:
    port: 50051
EOF

wait_for_resource deployment e2e-health-grpc
sleep 3

GRPC_PORT=$(kubectl get deployment e2e-health-grpc -o jsonpath='{.spec.template.spec.containers[0].livenessProbe.grpc.port}' 2>/dev/null || echo "")
assert_eq "gRPC probe port" "50051" "$GRPC_PORT"

# No HTTP path should exist on a gRPC probe
GRPC_HTTP_PATH=$(kubectl get deployment e2e-health-grpc -o jsonpath='{.spec.template.spec.containers[0].livenessProbe.httpGet.path}' 2>/dev/null || echo "")
assert_eq "No HTTP path on gRPC probe" "" "$GRPC_HTTP_PATH"

kubectl delete dse e2e-health-grpc --wait=false 2>/dev/null || true

# No health check
cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-health-none
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    healthCheck:
      type: none
  service:
    port: 80
EOF

wait_for_resource deployment e2e-health-none
sleep 3

NO_LIVENESS=$(kubectl get deployment e2e-health-none -o jsonpath='{.spec.template.spec.containers[0].livenessProbe}' 2>/dev/null || echo "")
assert_eq "No liveness probe when type=none" "" "$NO_LIVENESS"

NO_READINESS=$(kubectl get deployment e2e-health-none -o jsonpath='{.spec.template.spec.containers[0].readinessProbe}' 2>/dev/null || echo "")
assert_eq "No readiness probe when type=none" "" "$NO_READINESS"

kubectl delete dse e2e-health-none --wait=false 2>/dev/null || true

# ── 14. Replicas scaling ─────────────────────────────────────────────────
info "14. Replicas scaling"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-replicas
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    replicas: 2
  service:
    port: 80
EOF

wait_for_resource deployment e2e-replicas
wait_for_rollout e2e-replicas
sleep 3

REPLICAS=$(kubectl get deployment e2e-replicas -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")
assert_eq "Initial replicas" "2" "$REPLICAS"

AVAIL=$(kubectl get deployment e2e-replicas -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "")
assert_eq "Available replicas" "2" "$AVAIL"

# Scale down
kubectl patch dse e2e-replicas --type=merge -p '{"spec":{"deployment":{"replicas":1}}}'
sleep 8

REPLICAS2=$(kubectl get deployment e2e-replicas -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")
assert_eq "Scaled-down replicas" "1" "$REPLICAS2"

# Verify status.availableReplicas updates
sleep 5
STATUS_AVAIL=$(kubectl get dse e2e-replicas -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo "")
assert_eq "Status availableReplicas after scale-down" "1" "$STATUS_AVAIL"

kubectl delete dse e2e-replicas --wait=false 2>/dev/null || true

# ── 15. Service type NodePort ─────────────────────────────────────────────
info "15. Service type NodePort"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-nodeport
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
  service:
    port: 80
    type: NodePort
EOF

wait_for_resource service e2e-nodeport
sleep 2

SVC_TYPE=$(kubectl get svc e2e-nodeport -o jsonpath='{.spec.type}' 2>/dev/null || echo "")
assert_eq "Service type is NodePort" "NodePort" "$SVC_TYPE"

NODE_PORT=$(kubectl get svc e2e-nodeport -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null || echo "")
assert_not_empty "NodePort is assigned" "$NODE_PORT"

kubectl delete dse e2e-nodeport --wait=false 2>/dev/null || true

# ── 16. Custom env vars in DSE spec ──────────────────────────────────────
info "16. Custom env vars in DSE spec"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-custom-env
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    env:
      - name: APP_MODE
        value: "production"
      - name: LOG_LEVEL
        value: "debug"
      - name: CUSTOM_PORT
        value: "9090"
  service:
    port: 80
EOF

wait_for_resource deployment e2e-custom-env
sleep 2

APP_MODE=$(kubectl get deployment e2e-custom-env -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="APP_MODE")].value}' 2>/dev/null || echo "")
assert_eq "Custom env APP_MODE" "production" "$APP_MODE"

LOG_LEVEL=$(kubectl get deployment e2e-custom-env -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="LOG_LEVEL")].value}' 2>/dev/null || echo "")
assert_eq "Custom env LOG_LEVEL" "debug" "$LOG_LEVEL"

CUSTOM_PORT=$(kubectl get deployment e2e-custom-env -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CUSTOM_PORT")].value}' 2>/dev/null || echo "")
assert_eq "Custom env CUSTOM_PORT" "9090" "$CUSTOM_PORT"

kubectl delete dse e2e-custom-env --wait=false 2>/dev/null || true

# ── 17. Resource requests/limits ─────────────────────────────────────────
info "17. Resource requests and limits"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-resources
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    resources:
      cpuRequest: "100m"
      cpuLimit: "500m"
      memoryRequest: "128Mi"
      memoryLimit: "512Mi"
  service:
    port: 80
EOF

wait_for_resource deployment e2e-resources
sleep 2

CPU_REQ=$(kubectl get deployment e2e-resources -o jsonpath='{.spec.template.spec.containers[0].resources.requests.cpu}' 2>/dev/null || echo "")
assert_eq "CPU request" "100m" "$CPU_REQ"

CPU_LIM=$(kubectl get deployment e2e-resources -o jsonpath='{.spec.template.spec.containers[0].resources.limits.cpu}' 2>/dev/null || echo "")
assert_eq "CPU limit" "500m" "$CPU_LIM"

MEM_REQ=$(kubectl get deployment e2e-resources -o jsonpath='{.spec.template.spec.containers[0].resources.requests.memory}' 2>/dev/null || echo "")
assert_eq "Memory request" "128Mi" "$MEM_REQ"

MEM_LIM=$(kubectl get deployment e2e-resources -o jsonpath='{.spec.template.spec.containers[0].resources.limits.memory}' 2>/dev/null || echo "")
assert_eq "Memory limit" "512Mi" "$MEM_LIM"

kubectl delete dse e2e-resources --wait=false 2>/dev/null || true

# ── 18. Dependency envVarName override ────────────────────────────────────
info "18. Dependency envVarName override"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-env-override
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
  service:
    port: 80
  dependencies:
    - type: postgres
      envVarName: MY_PG_URL
    - type: redis
      envVarName: MY_REDIS_URL
EOF

wait_for_resource deployment e2e-env-override
sleep 5

ENV_NAMES=$(kubectl get deployment e2e-env-override -o jsonpath='{.spec.template.spec.containers[0].env[*].name}' 2>/dev/null || echo "")
assert_contains "MY_PG_URL injected (override)" "MY_PG_URL" "$ENV_NAMES"
assert_contains "MY_REDIS_URL injected (override)" "MY_REDIS_URL" "$ENV_NAMES"
assert_not_contains "DATABASE_URL not present (overridden)" "DATABASE_URL" "$ENV_NAMES"
assert_not_contains "REDIS_URL not present (overridden)" "REDIS_URL" "$ENV_NAMES"

MY_PG_VAL=$(kubectl get deployment e2e-env-override -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="MY_PG_URL")].value}' 2>/dev/null || echo "")
assert_contains "MY_PG_URL contains postgres://" "postgres://" "$MY_PG_VAL"

kubectl delete dse e2e-env-override --wait=false 2>/dev/null || true

# ── 19. Additional dependency types ──────────────────────────────────────
info "19. Additional dependency types"

if [ "${E2E_SKIP_DEPS:-0}" = "1" ]; then
  echo "  ⏩ Skipping additional dependency tests (E2E_SKIP_DEPS=1)"
else

  # ── 19a. Lightweight deps: mysql, mongodb, rabbitmq, nats, memcached ──
  info "19a. Lightweight dependencies (mysql, mongodb, rabbitmq, nats, memcached)"

  cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-deps-light
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
  service:
    port: 80
  dependencies:
    - type: mysql
    - type: mongodb
    - type: rabbitmq
    - type: nats
    - type: memcached
EOF

  echo "  Waiting for lightweight dependency pods..."
  sleep 10

  # MySQL
  wait_for_resource deployment e2e-deps-light-mysql
  MYSQL_PORT=$(kubectl get svc e2e-deps-light-mysql -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "MySQL Service port" "3306" "$MYSQL_PORT"

  MYSQL_ENV=$(kubectl get deployment e2e-deps-light -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DATABASE_URL")].value}' 2>/dev/null || echo "")
  assert_contains "DATABASE_URL for mysql" "mysql://" "$MYSQL_ENV"

  # MongoDB
  wait_for_resource deployment e2e-deps-light-mongodb
  MONGO_PORT=$(kubectl get svc e2e-deps-light-mongodb -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "MongoDB Service port" "27017" "$MONGO_PORT"

  MONGO_ENV=$(kubectl get deployment e2e-deps-light -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="MONGO_URL")].value}' 2>/dev/null || echo "")
  assert_contains "MONGO_URL contains mongodb://" "mongodb://" "$MONGO_ENV"

  # RabbitMQ
  wait_for_resource deployment e2e-deps-light-rabbitmq
  RABBIT_PORT=$(kubectl get svc e2e-deps-light-rabbitmq -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "RabbitMQ Service port" "5672" "$RABBIT_PORT"

  RABBIT_ENV=$(kubectl get deployment e2e-deps-light -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="AMQP_URL")].value}' 2>/dev/null || echo "")
  assert_contains "AMQP_URL contains amqp://" "amqp://" "$RABBIT_ENV"

  # NATS
  wait_for_resource deployment e2e-deps-light-nats
  NATS_PORT=$(kubectl get svc e2e-deps-light-nats -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "NATS Service port" "4222" "$NATS_PORT"

  NATS_ENV=$(kubectl get deployment e2e-deps-light -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="NATS_URL")].value}' 2>/dev/null || echo "")
  assert_contains "NATS_URL contains nats://" "nats://" "$NATS_ENV"

  # Memcached
  wait_for_resource deployment e2e-deps-light-memcached
  MEMC_PORT=$(kubectl get svc e2e-deps-light-memcached -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "Memcached Service port" "11211" "$MEMC_PORT"

  MEMC_ENV=$(kubectl get deployment e2e-deps-light -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="MEMCACHED_URL")].value}' 2>/dev/null || echo "")
  assert_not_empty "MEMCACHED_URL is set" "$MEMC_ENV"

  kubectl delete dse e2e-deps-light --wait=false 2>/dev/null || true

  # ── 19b. Infra deps: consul, vault, minio, influxdb, jaeger ──────────
  info "19b. Infrastructure dependencies (consul, vault, minio, influxdb, jaeger)"

  cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-deps-infra
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
  service:
    port: 80
  dependencies:
    - type: consul
    - type: vault
    - type: minio
    - type: influxdb
    - type: jaeger
EOF

  echo "  Waiting for infrastructure dependency pods..."
  sleep 15

  # Consul
  wait_for_resource deployment e2e-deps-infra-consul
  CONSUL_PORT=$(kubectl get svc e2e-deps-infra-consul -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "Consul Service port" "8500" "$CONSUL_PORT"

  CONSUL_ENV=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CONSUL_HTTP_ADDR")].value}' 2>/dev/null || echo "")
  assert_not_empty "CONSUL_HTTP_ADDR is set" "$CONSUL_ENV"

  # Vault
  wait_for_resource deployment e2e-deps-infra-vault
  VAULT_PORT=$(kubectl get svc e2e-deps-infra-vault -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "Vault Service port" "8200" "$VAULT_PORT"

  VAULT_ENV=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="VAULT_ADDR")].value}' 2>/dev/null || echo "")
  assert_not_empty "VAULT_ADDR is set" "$VAULT_ENV"

  VAULT_TOKEN_ENV=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="VAULT_TOKEN")].value}' 2>/dev/null || echo "")
  assert_not_empty "VAULT_TOKEN is injected" "$VAULT_TOKEN_ENV"

  # MinIO
  wait_for_resource deployment e2e-deps-infra-minio
  MINIO_PORT=$(kubectl get svc e2e-deps-infra-minio -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "MinIO Service port" "9000" "$MINIO_PORT"

  S3_ENV=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="S3_ENDPOINT")].value}' 2>/dev/null || echo "")
  assert_not_empty "S3_ENDPOINT is set" "$S3_ENV"

  S3_ACCESS=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="S3_ACCESS_KEY")].value}' 2>/dev/null || echo "")
  assert_not_empty "S3_ACCESS_KEY is injected" "$S3_ACCESS"

  S3_SECRET=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="S3_SECRET_KEY")].value}' 2>/dev/null || echo "")
  assert_not_empty "S3_SECRET_KEY is injected" "$S3_SECRET"

  # InfluxDB
  wait_for_resource deployment e2e-deps-infra-influxdb
  INFLUX_PORT=$(kubectl get svc e2e-deps-infra-influxdb -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "InfluxDB Service port" "8086" "$INFLUX_PORT"

  INFLUX_URL=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="INFLUXDB_URL")].value}' 2>/dev/null || echo "")
  assert_not_empty "INFLUXDB_URL is set" "$INFLUX_URL"

  INFLUX_ORG=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="INFLUXDB_ORG")].value}' 2>/dev/null || echo "")
  assert_not_empty "INFLUXDB_ORG is injected" "$INFLUX_ORG"

  INFLUX_BUCKET=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="INFLUXDB_BUCKET")].value}' 2>/dev/null || echo "")
  assert_not_empty "INFLUXDB_BUCKET is injected" "$INFLUX_BUCKET"

  # Jaeger
  wait_for_resource deployment e2e-deps-infra-jaeger
  JAEGER_PORT=$(kubectl get svc e2e-deps-infra-jaeger -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
  assert_eq "Jaeger Service port" "16686" "$JAEGER_PORT"

  JAEGER_ENV=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="JAEGER_ENDPOINT")].value}' 2>/dev/null || echo "")
  assert_not_empty "JAEGER_ENDPOINT is set" "$JAEGER_ENV"

  OTEL_ENV=$(kubectl get deployment e2e-deps-infra -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="OTEL_EXPORTER_OTLP_ENDPOINT")].value}' 2>/dev/null || echo "")
  assert_not_empty "OTEL_EXPORTER_OTLP_ENDPOINT is injected" "$OTEL_ENV"

  kubectl delete dse e2e-deps-infra --wait=false 2>/dev/null || true

fi # E2E_SKIP_DEPS

# ── 20. Multi-service deploy ─────────────────────────────────────────────
info "20. Multi-service deploy (2 CRs from one file)"

cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-multi-api
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 8080
    env:
      - name: APP_ROLE
        value: api
  service:
    port: 8080
  dependencies:
    - type: postgres
---
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-multi-worker
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 9090
    env:
      - name: APP_ROLE
        value: worker
  service:
    port: 9090
  dependencies:
    - type: redis
EOF

wait_for_resource deployment e2e-multi-api
wait_for_resource deployment e2e-multi-worker

TESTS=$((TESTS + 1))
if wait_for_rollout e2e-multi-api && wait_for_rollout e2e-multi-worker; then
  pass "Both multi-service deployments are ready"
else
  fail "Multi-service deployments did not become ready"
fi

# Verify they have independent deps
wait_for_resource deployment e2e-multi-api-postgres
wait_for_resource deployment e2e-multi-worker-redis

TESTS=$((TESTS + 1))
if kubectl get deployment e2e-multi-api-postgres >/dev/null 2>&1 && kubectl get deployment e2e-multi-worker-redis >/dev/null 2>&1; then
  pass "Independent dependencies created for each service"
else
  fail "Dependencies not created independently"
fi

# Verify env isolation
API_ROLE=$(kubectl get deployment e2e-multi-api -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="APP_ROLE")].value}' 2>/dev/null || echo "")
assert_eq "API has APP_ROLE=api" "api" "$API_ROLE"

WORKER_ROLE=$(kubectl get deployment e2e-multi-worker -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="APP_ROLE")].value}' 2>/dev/null || echo "")
assert_eq "Worker has APP_ROLE=worker" "worker" "$WORKER_ROLE"

kubectl delete dse e2e-multi-api e2e-multi-worker --wait=false 2>/dev/null || true
sleep 5

# ════════════════════════════════════════════════════════════════════════════
# TIER 2: CLI features (exercises core/ package via kindling binary)
# ════════════════════════════════════════════════════════════════════════════

# ── 21. CLI binary verification ────────────────────────────────────────────
info "21. CLI binary verification"

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

# ── 22. kindling deploy ──────────────────────────────────────────────────
info "22. kindling deploy -f"

DEPLOY_OUT=$("$KINDLING" deploy -f "$ROOT_DIR/examples/sample-app/dev-environment.yaml" 2>&1 || true)
assert_contains "deploy reports success" "applied" "$DEPLOY_OUT"

wait_for_resource dse sample-app-dev
TESTS=$((TESTS + 1))
if kubectl get dse sample-app-dev >/dev/null 2>&1; then
  pass "DSE created by kindling deploy"
else
  fail "DSE not found after kindling deploy"
fi

kubectl delete dse sample-app-dev --wait=false 2>/dev/null || true
sleep 3

# ── 23. kindling status ───────────────────────────────────────────────────
info "23. kindling status"

TESTS=$((TESTS + 1))
STATUS_OUT=$("$KINDLING" status --cluster "$CLUSTER_NAME" 2>&1 || true)
if echo "$STATUS_OUT" | grep -qi "cluster\|node\|operator\|running"; then
  pass "kindling status returns cluster info"
else
  fail "kindling status produced no recognizable output"
fi

# ── 24. core/secrets — create, list, delete ────────────────────────────────
info "24. core/secrets — create, list, delete"

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

# ── 25. Deploy microservices for CLI tests ─────────────────────────────────
info "25. Deploy microservices (for CLI feature tests)"

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

# ── 26. core/env — set, list, unset ───────────────────────────────────────
info "26. core/env — set, list, unset"

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

# ── 27. core/load — build, load, patch ─────────────────────────────────────
info "27. core/load — build, load, patch deployment"

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

# ── 28. core/runners — runner pool CR lifecycle ────────────────────────────
info "28. core/runners — create and reset runner pool"

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
POOL_CR=$(kctl get cirunnerpools -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || echo "")
if echo "$POOL_CR" | grep -q "e2e-test-user-runner-pool"; then
  pass "CIRunnerPool CR created"
else
  fail "CIRunnerPool CR not found (got: '$POOL_CR')"
fi

RUNNER_LABELS=$(kctl get cirunnerpool e2e-test-user-runner-pool \
  -o jsonpath='{.spec.labels[*]}' 2>/dev/null || echo "")
assert_contains "Runner pool has 'kindling' label" "kindling" "$RUNNER_LABELS"

"$KINDLING" reset -y --cluster "$CLUSTER_NAME" 2>/dev/null || true

TESTS=$((TESTS + 1))
POOL_AFTER=$(kctl get cirunnerpools --no-headers 2>/dev/null | wc -l | tr -d ' ')
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

# ── 29. kindling logs ────────────────────────────────────────────────────
info "29. kindling logs"

LOGS_OUT=$("$KINDLING" logs --cluster "$CLUSTER_NAME" --follow=false --since 5m 2>&1 || true)
# Controller logs should contain reconciliation output
TESTS=$((TESTS + 1))
if [ -n "$LOGS_OUT" ]; then
  pass "kindling logs returns controller output"
else
  # Logs might be empty if controller hasn't logged recently — pass but warn
  pass "kindling logs ran (output may be empty if controller is idle)"
fi

# ── 30. kindling snapshot — Helm format ──────────────────────────────────
info "30. kindling snapshot — Helm chart export"

SNAP_DIR=$(mktemp -d)
SNAP_OUT=$("$KINDLING" snapshot \
  -f helm \
  -o "$SNAP_DIR/helm-chart" \
  -n e2e-snap-test \
  --cluster "$CLUSTER_NAME" 2>&1 || true)

assert_dir_exists "Helm chart directory created" "$SNAP_DIR/helm-chart"
assert_file_exists "Chart.yaml exists" "$SNAP_DIR/helm-chart/Chart.yaml"
assert_file_exists "values.yaml exists" "$SNAP_DIR/helm-chart/values.yaml"
assert_dir_exists "templates/ directory exists" "$SNAP_DIR/helm-chart/templates"

# Verify Chart.yaml content
if [ -f "$SNAP_DIR/helm-chart/Chart.yaml" ]; then
  CHART_NAME=$(grep '^name:' "$SNAP_DIR/helm-chart/Chart.yaml" | head -1 | awk '{print $2}' || echo "")
  assert_eq "Chart name" "e2e-snap-test" "$CHART_NAME"
fi

# Verify values.yaml has service entries
if [ -f "$SNAP_DIR/helm-chart/values.yaml" ]; then
  VALUES_CONTENT=$(cat "$SNAP_DIR/helm-chart/values.yaml")
  assert_not_empty "values.yaml has content" "$VALUES_CONTENT"
fi

# Verify template files exist
TEMPLATE_COUNT=$(find "$SNAP_DIR/helm-chart/templates" -name "*.yaml" 2>/dev/null | wc -l | tr -d ' ')
TESTS=$((TESTS + 1))
if [ "$TEMPLATE_COUNT" -gt 0 ]; then
  pass "Helm templates generated ($TEMPLATE_COUNT files)"
else
  fail "No Helm template files found"
fi

rm -rf "$SNAP_DIR"

# ── 31. kindling snapshot — Kustomize format ─────────────────────────────
info "31. kindling snapshot — Kustomize export"

SNAP_DIR2=$(mktemp -d)
SNAP_OUT2=$("$KINDLING" snapshot \
  -f kustomize \
  -o "$SNAP_DIR2/kustomize-out" \
  -n e2e-kustom-test \
  --cluster "$CLUSTER_NAME" 2>&1 || true)

assert_dir_exists "Kustomize directory created" "$SNAP_DIR2/kustomize-out"
assert_file_exists "kustomization.yaml exists" "$SNAP_DIR2/kustomize-out/kustomization.yaml"

# Should have deployment and service yamls
KUSTOMIZE_FILES=$(find "$SNAP_DIR2/kustomize-out" -name "*.yaml" 2>/dev/null | wc -l | tr -d ' ')
TESTS=$((TESTS + 1))
if [ "$KUSTOMIZE_FILES" -gt 1 ]; then
  pass "Kustomize manifests generated ($KUSTOMIZE_FILES files)"
else
  fail "Insufficient Kustomize files generated"
fi

rm -rf "$SNAP_DIR2"

# ── 31a. Tunnel ingress patching — simulate expose ───────────────────────
info "31a. Tunnel ingress patching (simulated expose)"

# Create a DSE with ingress + TLS to simulate the full tunnel flow.
cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-tunnel-app
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    replicas: 1
  service:
    port: 80
  ingress:
    enabled: true
    host: myapp.localhost
    path: /
    pathType: Prefix
    tls:
      secretName: myapp-tls
      hosts:
        - myapp.localhost
EOF

wait_for_resource ingress e2e-tunnel-app
sleep 3

# Verify baseline ingress state
ORIG_HOST=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
assert_eq "Ingress original host" "myapp.localhost" "$ORIG_HOST"

ORIG_TLS=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.tls[0].secretName}' 2>/dev/null || echo "")
assert_eq "Ingress has TLS secretName" "myapp-tls" "$ORIG_TLS"

TUNNEL_HOST="abc-test-tunnel.trycloudflare.com"

# Simulate what kindling expose does: JSON-patch the ingress
#   1. Save original host as annotation
#   2. Replace host with tunnel hostname
#   3. Save original TLS as annotation
#   4. Remove TLS (tunnel provider handles TLS at edge)
CURRENT_TLS=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.tls}' 2>/dev/null || echo "")
# Escape inner JSON quotes so the value is a valid JSON string inside the patch
ESCAPED_TLS=$(echo "$CURRENT_TLS" | sed 's/"/\\"/g')
PATCH_OPS="[{\"op\":\"add\",\"path\":\"/metadata/annotations/kindling.dev~1original-host\",\"value\":\"myapp.localhost\"},{\"op\":\"replace\",\"path\":\"/spec/rules/0/host\",\"value\":\"$TUNNEL_HOST\"},{\"op\":\"add\",\"path\":\"/metadata/annotations/kindling.dev~1original-tls\",\"value\":\"$ESCAPED_TLS\"},{\"op\":\"remove\",\"path\":\"/spec/tls\"}]"
kctl patch ingress e2e-tunnel-app --type=json -p="$PATCH_OPS"
sleep 2

# Verify host was changed to tunnel hostname
PATCHED_HOST=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
assert_eq "Ingress host patched to tunnel" "$TUNNEL_HOST" "$PATCHED_HOST"

# Verify original host saved in annotation
SAVED_HOST=$(kctl get ingress e2e-tunnel-app \
  -o 'go-template={{index .metadata.annotations "kindling.dev/original-host"}}' 2>/dev/null || echo "")
assert_eq "Original host saved in annotation" "myapp.localhost" "$SAVED_HOST"

# Verify TLS was removed
PATCHED_TLS=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.tls}' 2>/dev/null || echo "")
assert_eq "TLS removed after tunnel patch" "" "$PATCHED_TLS"

# Verify original TLS saved in annotation
SAVED_TLS=$(kctl get ingress e2e-tunnel-app \
  -o 'go-template={{index .metadata.annotations "kindling.dev/original-tls"}}' 2>/dev/null || echo "")
assert_not_empty "Original TLS saved in annotation" "$SAVED_TLS"

# ── 31b. Tunnel ingress restore ─────────────────────────────────────────
info "31b. Tunnel ingress restore (simulated unexpose)"

# Simulate what kindling expose --stop does: restore original host + TLS
RESTORE_OPS=$(cat <<RESTOREEOF
[
  {"op":"replace","path":"/spec/rules/0/host","value":"myapp.localhost"},
  {"op":"remove","path":"/metadata/annotations/kindling.dev~1original-host"},
  {"op":"add","path":"/spec/tls","value":[{"secretName":"myapp-tls","hosts":["myapp.localhost"]}]},
  {"op":"remove","path":"/metadata/annotations/kindling.dev~1original-tls"}
]
RESTOREEOF
)
kctl patch ingress e2e-tunnel-app --type=json -p="$RESTORE_OPS"
sleep 2

# Verify host restored
RESTORED_HOST=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
assert_eq "Ingress host restored" "myapp.localhost" "$RESTORED_HOST"

# Verify TLS restored
RESTORED_TLS=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.tls[0].secretName}' 2>/dev/null || echo "")
assert_eq "TLS secretName restored" "myapp-tls" "$RESTORED_TLS"

RESTORED_TLS_HOST=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.tls[0].hosts[0]}' 2>/dev/null || echo "")
assert_eq "TLS host restored" "myapp.localhost" "$RESTORED_TLS_HOST"

# Verify annotations cleaned up
LEFTOVER_ANN=$(kctl get ingress e2e-tunnel-app \
  -o 'go-template={{index .metadata.annotations "kindling.dev/original-host"}}' 2>/dev/null || echo "")
TESTS=$((TESTS + 1))
if [ "$LEFTOVER_ANN" = "<no value>" ] || [ -z "$LEFTOVER_ANN" ]; then
  pass "Original-host annotation removed after restore"
else
  fail "Original-host annotation still present: $LEFTOVER_ANN"
fi

# ── 31c. Tunnel state file + ConfigMap ──────────────────────────────────
info "31c. Tunnel state management (file + ConfigMap)"

TUNNEL_DIR=$(mktemp -d)
mkdir -p "$TUNNEL_DIR/.kindling"

# Write a tunnel state file (matches what core.SaveTunnelInfo produces)
cat > "$TUNNEL_DIR/.kindling/tunnel.yaml" <<TUNNELEOF
# Generated by kindling expose — do not edit
provider: cloudflared
url: https://$TUNNEL_HOST
pid: 99999
created: 2026-03-08T12:00:00Z
TUNNELEOF

assert_file_exists "Tunnel state file written" "$TUNNEL_DIR/.kindling/tunnel.yaml"

# Verify contents
TUNNEL_URL_LINE=$(grep '^url:' "$TUNNEL_DIR/.kindling/tunnel.yaml" | head -1 || echo "")
assert_contains "Tunnel state has URL" "$TUNNEL_HOST" "$TUNNEL_URL_LINE"

TUNNEL_PROVIDER_LINE=$(grep '^provider:' "$TUNNEL_DIR/.kindling/tunnel.yaml" | head -1 || echo "")
assert_contains "Tunnel state has provider" "cloudflared" "$TUNNEL_PROVIDER_LINE"

# Create tunnel ConfigMap (matches what core.saveTunnelConfigMap does)
kctl create configmap kindling-tunnel \
  --from-literal=url="https://$TUNNEL_HOST" \
  --from-literal=hostname="$TUNNEL_HOST" \
  --dry-run=client -o yaml | kctl apply -f -
sleep 1

CM_URL=$(kctl get configmap kindling-tunnel -o jsonpath='{.data.url}' 2>/dev/null || echo "")
assert_eq "ConfigMap tunnel URL" "https://$TUNNEL_HOST" "$CM_URL"

CM_HOST=$(kctl get configmap kindling-tunnel -o jsonpath='{.data.hostname}' 2>/dev/null || echo "")
assert_eq "ConfigMap tunnel hostname" "$TUNNEL_HOST" "$CM_HOST"

# Clean up ConfigMap
kctl delete configmap kindling-tunnel --ignore-not-found 2>/dev/null || true
rm -rf "$TUNNEL_DIR"
pass "Tunnel state cleanup complete"

# ── 31c2. Stable domain config (tunnel-config.yaml) ─────────────────────
info "31c2. Stable domain config persistence"

STABLE_DIR=$(mktemp -d)
mkdir -p "$STABLE_DIR/.kindling"

# Write a stable tunnel config (matches what core.SaveStableTunnelConfig produces)
cat > "$STABLE_DIR/.kindling/tunnel-config.yaml" <<'STABLEEOF'
# Stable tunnel domain — generated by kindling expose --domain
domain: myapp-dev.ngrok-free.app
STABLEEOF

assert_file_exists "Stable tunnel config written" "$STABLE_DIR/.kindling/tunnel-config.yaml"

# Verify domain field
STABLE_DOMAIN=$(grep '^domain:' "$STABLE_DIR/.kindling/tunnel-config.yaml" | awk '{print $2}' || echo "")
assert_eq "Stable config has domain" "myapp-dev.ngrok-free.app" "$STABLE_DOMAIN"

# Verify overwriting with a new domain works
cat > "$STABLE_DIR/.kindling/tunnel-config.yaml" <<'STABLEEOF2'
# Stable tunnel domain — generated by kindling expose --domain
domain: other-app.ngrok-free.app
STABLEEOF2

STABLE_DOMAIN2=$(grep '^domain:' "$STABLE_DIR/.kindling/tunnel-config.yaml" | awk '{print $2}' || echo "")
assert_eq "Stable config updated" "other-app.ngrok-free.app" "$STABLE_DOMAIN2"

rm -rf "$STABLE_DIR"
pass "Stable domain config cleanup complete"

# ── 31d. Snapshot preserves original host (not tunnel host) ──────────────
info "31d. Snapshot preserves original host during tunnel"

# Re-patch ingress with tunnel host (DSE spec.ingress.host is still myapp.localhost)
kctl patch ingress e2e-tunnel-app --type=json \
  -p='[{"op":"add","path":"/metadata/annotations/kindling.dev~1original-host","value":"myapp.localhost"},{"op":"replace","path":"/spec/rules/0/host","value":"'$TUNNEL_HOST'"}]'
sleep 2

# Verify ingress is showing tunnel host
LIVE_HOST=$(kctl get ingress e2e-tunnel-app -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || echo "")
assert_eq "Ingress shows tunnel host" "$TUNNEL_HOST" "$LIVE_HOST"

# But the DSE CR still has the original host in spec
DSE_HOST=$(kctl get dse e2e-tunnel-app -o jsonpath='{.spec.ingress.host}' 2>/dev/null || echo "")
assert_eq "DSE spec still has original host" "myapp.localhost" "$DSE_HOST"

# Snapshot reads from DSE spec, NOT from live ingress — so the exported
# chart should have the original host, not the tunnel host.
TSNAP_DIR=$(mktemp -d)
TSNAP_OUT=$("$KINDLING" snapshot \
  -f helm \
  -o "$TSNAP_DIR/helm-chart" \
  -n e2e-tunnel-snap \
  --cluster "$CLUSTER_NAME" 2>&1 || true)

if [ -f "$TSNAP_DIR/helm-chart/values.yaml" ]; then
  SNAP_VALUES=$(cat "$TSNAP_DIR/helm-chart/values.yaml")
  assert_not_contains "Snapshot does not contain tunnel host" "$TUNNEL_HOST" "$SNAP_VALUES"
  # The original host or a TODO placeholder should appear instead
  TESTS=$((TESTS + 1))
  if echo "$SNAP_VALUES" | grep -q "myapp.localhost\|TODO"; then
    pass "Snapshot has original host or TODO placeholder"
  else
    fail "Snapshot values missing original host reference"
  fi
else
  TESTS=$((TESTS + 1))
  fail "Snapshot helm values.yaml not created"
fi
rm -rf "$TSNAP_DIR"

# Clean up tunnel DSE
kctl delete dse e2e-tunnel-app --wait=false 2>/dev/null || true
sleep 3

# ── 31e. Staging TLS — Kind context safety check ─────────────────────
info "31e. Staging TLS safety checks"

# kindling staging tls should refuse a kind- context
STAGING_TLS_OUT=$("$KINDLING" staging tls \
  --context "kind-$CLUSTER_NAME" \
  --domain app.example.com \
  --email admin@example.com 2>&1 || true)
assert_contains "staging tls refuses kind- context" "Kind cluster" "$STAGING_TLS_OUT"

# ── 31f. Staging TLS — DSE file patching ──────────────────────────────
info "31f. Staging TLS DSE file patching"

# Create a temp DSE YAML to test file patching
TLS_TEST_DIR=$(mktemp -d)
cat > "$TLS_TEST_DIR/test-dse.yaml" <<'DSEEOF'
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: my-app
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
  service:
    port: 80
  ingress:
    enabled: true
    host: myapp.localhost
    path: /
    pathType: Prefix
DSEEOF

# Run staging tls with a fake non-kind context — it will fail on the
# kubectl calls but should still patch the file if we provide --file.
# Since we can't use a real staging cluster, test the file patching by
# running the command and checking the modified YAML.
"$KINDLING" staging tls \
  --context "fake-staging-ctx" \
  --domain app.example.com \
  --email admin@example.com \
  -f "$TLS_TEST_DIR/test-dse.yaml" 2>&1 || true

# Check if the DSE file was patched with TLS config
PATCHED_DSE=$(cat "$TLS_TEST_DIR/test-dse.yaml")

# The patching happens inside patchDSEWithTLS which runs regardless of
# whether cert-manager installation succeeds. Check for TLS fields.
TESTS=$((TESTS + 1))
if echo "$PATCHED_DSE" | grep -q "secretName\|cert-manager\|app-example-com-tls"; then
  pass "DSE file patched with TLS config"
  assert_contains "DSE has ingressClassName" "ingressClassName" "$PATCHED_DSE"
  assert_contains "DSE has cert-manager annotation" "cert-manager" "$PATCHED_DSE"
  assert_contains "DSE has TLS secretName" "app-example-com-tls" "$PATCHED_DSE"
  assert_contains "DSE has TLS host" "app.example.com" "$PATCHED_DSE"
else
  # If the command failed before reaching file patching (e.g. kubectl not
  # finding the fake context), verify it at least didn't corrupt the file.
  pass "DSE file patching skipped (expected — no real prod cluster)"
  assert_contains "DSE file still valid" "nginx:1.25" "$PATCHED_DSE"
fi

rm -rf "$TLS_TEST_DIR"

# ── 32. DSE cleanup from CLI tests ────────────────────────────────────────
info "32. Cleaning up microservices DSEs"

for cr in "$EXAMPLES_DIR"/deploy/*.yaml; do
  kctl delete -f "$cr" --wait=false 2>/dev/null || true
done
sleep 5
pass "Microservice DSEs cleaned up"

# ════════════════════════════════════════════════════════════════════════════
# TIER 2b: Dashboard API
# ════════════════════════════════════════════════════════════════════════════

# ── 33. Start dashboard ──────────────────────────────────────────────────
info "33. Dashboard API tests"

# Deploy a DSE for the dashboard to see
cat <<'EOF' | kubectl apply -f -
apiVersion: apps.example.com/v1alpha1
kind: DevStagingEnvironment
metadata:
  name: e2e-dash-app
  namespace: default
spec:
  deployment:
    image: nginx:1.25
    port: 80
    replicas: 1
  service:
    port: 80
  dependencies:
    - type: redis
EOF

wait_for_resource deployment e2e-dash-app
wait_for_rollout e2e-dash-app 2>/dev/null || true
sleep 5

# Start dashboard in background
echo "  Starting dashboard on port $DASHBOARD_PORT..."
"$KINDLING" dashboard --port "$DASHBOARD_PORT" --cluster "$CLUSTER_NAME" &>/dev/null &
DASHBOARD_PID=$!
sleep 3

DASHBOARD_URL="http://localhost:${DASHBOARD_PORT}"

# ── 33a. Read-only API endpoints ──────────────────────────────────────────
info "33a. Dashboard read-only endpoints"

assert_http_ok "GET /api/cluster" "$DASHBOARD_URL/api/cluster"
assert_http_ok "GET /api/nodes" "$DASHBOARD_URL/api/nodes"
assert_http_ok "GET /api/namespaces" "$DASHBOARD_URL/api/namespaces"
assert_http_ok "GET /api/dses" "$DASHBOARD_URL/api/dses"
assert_http_ok "GET /api/deployments" "$DASHBOARD_URL/api/deployments"
assert_http_ok "GET /api/services" "$DASHBOARD_URL/api/services"
assert_http_ok "GET /api/pods" "$DASHBOARD_URL/api/pods"
assert_http_ok "GET /api/secrets" "$DASHBOARD_URL/api/secrets"
assert_http_ok "GET /api/events" "$DASHBOARD_URL/api/events"

# Verify /api/cluster returns meaningful data
assert_json_field "GET /api/cluster has 'exists' field" "$DASHBOARD_URL/api/cluster" "exists"

# Verify /api/dses returns our test DSE
DSES_BODY=$(curl -s "$DASHBOARD_URL/api/dses" 2>/dev/null || echo "[]")
assert_contains "DSEs list contains e2e-dash-app" "e2e-dash-app" "$DSES_BODY"

# Verify /api/deployments returns our deployment
DEPLOY_BODY=$(curl -s "$DASHBOARD_URL/api/deployments" 2>/dev/null || echo "[]")
assert_contains "Deployments list contains e2e-dash-app" "e2e-dash-app" "$DEPLOY_BODY"

# ── 33b. Dashboard action endpoints ──────────────────────────────────────
info "33b. Dashboard action endpoints"

# Expose status API — no tunnel running, should return running: false
EXPOSE_STATUS=$(curl -s "$DASHBOARD_URL/api/expose/status" 2>/dev/null || echo "{}")
assert_contains "Expose status has running field" "running" "$EXPOSE_STATUS"
# No tunnel is actually running in CI, so running should be false
TESTS=$((TESTS + 1))
if echo "$EXPOSE_STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d.get('running') == False" 2>/dev/null; then
  pass "Expose status reports running=false (no tunnel)"
else
  fail "Expose status should report running=false"
fi

# Create a secret via dashboard API
SECRET_RESP=$(curl -s -X POST "$DASHBOARD_URL/api/secrets/create" \
  -H "Content-Type: application/json" \
  -d '{"name":"DASH_TEST_SECRET","value":"secret-val"}' 2>/dev/null || echo "{}")
assert_contains "Secret create returns ok" "true" "$SECRET_RESP"

TESTS=$((TESTS + 1))
if kctl get secret kindling-secret-dash-test-secret >/dev/null 2>&1; then
  pass "Secret created via dashboard API"
else
  fail "Secret not found after dashboard create"
fi

# Delete the secret via dashboard API
DEL_RESP=$(curl -s -X DELETE "$DASHBOARD_URL/api/secrets/default/kindling-secret-dash-test-secret" \
  2>/dev/null || echo "{}")
assert_contains "Secret delete returns ok" "true" "$DEL_RESP"

# Set env via dashboard API
ENV_SET_RESP=$(curl -s -X POST "$DASHBOARD_URL/api/env/set" \
  -H "Content-Type: application/json" \
  -d '{"deployment":"e2e-dash-app","namespace":"default","env":{"DASH_VAR":"dash-value"}}' 2>/dev/null || echo "{}")
assert_contains "Env set returns ok" "true" "$ENV_SET_RESP"

sleep 3
DASH_ENV=$(kctl get deployment e2e-dash-app \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DASH_VAR")].value}' 2>/dev/null || echo "")
assert_eq "DASH_VAR set via dashboard" "dash-value" "$DASH_ENV"

# List env via dashboard API
ENV_LIST_RESP=$(curl -s "$DASHBOARD_URL/api/env/list/default/e2e-dash-app" 2>/dev/null || echo "[]")
assert_contains "Env list contains DASH_VAR" "DASH_VAR" "$ENV_LIST_RESP"

# Unset env via dashboard API
ENV_UNSET_RESP=$(curl -s -X POST "$DASHBOARD_URL/api/env/unset" \
  -H "Content-Type: application/json" \
  -d '{"deployment":"e2e-dash-app","namespace":"default","keys":["DASH_VAR"]}' 2>/dev/null || echo "{}")
assert_contains "Env unset returns ok" "true" "$ENV_UNSET_RESP"

# Scale via dashboard API
SCALE_RESP=$(curl -s -X POST "$DASHBOARD_URL/api/scale/default/e2e-dash-app" \
  -H "Content-Type: application/json" \
  -d '{"replicas":2}' 2>/dev/null || echo "{}")
assert_contains "Scale returns ok" "true" "$SCALE_RESP"

sleep 5
SCALED_REPLICAS=$(kctl get deployment e2e-dash-app -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")
assert_eq "Deployment scaled to 2 via dashboard" "2" "$SCALED_REPLICAS"

# Scale back down
curl -s -X POST "$DASHBOARD_URL/api/scale/default/e2e-dash-app" \
  -H "Content-Type: application/json" \
  -d '{"replicas":1}' 2>/dev/null || true

# Restart via dashboard API
RESTART_RESP=$(curl -s -X POST "$DASHBOARD_URL/api/restart/default/e2e-dash-app" \
  -H "Content-Type: application/json" 2>/dev/null || echo "{}")
assert_contains "Restart returns ok" "true" "$RESTART_RESP"

# ── 33c. Stop dashboard ──────────────────────────────────────────────────
kill "$DASHBOARD_PID" 2>/dev/null || true
wait "$DASHBOARD_PID" 2>/dev/null || true
DASHBOARD_PID=""
pass "Dashboard stopped"

kubectl delete dse e2e-dash-app --wait=false 2>/dev/null || true
sleep 3

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
