#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────
# setup-ingress.sh — Install Traefik ingress controller and the
# in-cluster image registry on a Kind cluster.
#
# This script:
#   1. Deploys Traefik as a DaemonSet with hostNetwork so it binds
#      to the node's ports 80/443 (same model as the old
#      ingress-nginx Kind deployment).
#   2. Deploys a registry:2 pod with hostNetwork so containerd
#      (via the mirror in kind-config.yaml) and Kaniko pods can
#      both reach it.
#
# Usage:
#   ./setup-ingress.sh
#
# Prerequisites:
#   - Kind cluster created with kind-config.yaml
#   - kubectl configured to talk to the Kind cluster
# ─────────────────────────────────────────────────────────────────
set -euo pipefail

# ── In-cluster image registry ──────────────────────────────────────
echo "📦 Deploying in-cluster image registry..."
kubectl apply -f config/registry/registry.yaml

# Configure containerd registry mirror on Kind nodes (config_path mode
# for containerd 2.x).  This makes containerd resolve "registry:5000"
# to localhost:5000 where the hostNetwork registry pod is listening.
REGISTRY_DIR="/etc/containerd/certs.d/registry:5000"
for node in $(kind get nodes --name "${KIND_CLUSTER_NAME:-dev}" 2>/dev/null); do
  docker exec "$node" mkdir -p "$REGISTRY_DIR"
  docker exec -i "$node" sh -c "cat > ${REGISTRY_DIR}/hosts.toml" <<EOF
[host."http://localhost:5000"]
  capabilities = ["pull", "resolve", "push"]
EOF
done

echo "⏳ Waiting for registry to be ready..."
kubectl wait --for=condition=available deployment/registry --timeout=60s
echo "✅ Registry is ready at registry:5000 (in-cluster)"

# ── Ingress controller (Traefik) ──────────────────────────────────
echo ""
echo "📦 Installing Traefik ingress controller for Kind..."

# Create the traefik namespace
kubectl create namespace traefik --dry-run=client -o yaml | kubectl apply -f -

# Deploy Traefik as a DaemonSet with hostNetwork on nodes labeled
# ingress-ready=true (set by kind-config.yaml). This binds Traefik
# directly to the host's port 80/443 — the same model ingress-nginx
# used for Kind.
kubectl apply -f - <<'MANIFEST'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: traefik
  namespace: traefik
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: traefik
rules:
  - apiGroups: [""]
    resources: ["services", "endpoints", "secrets", "namespaces", "nodes"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["extensions", "networking.k8s.io"]
    resources: ["ingresses", "ingressclasses"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["extensions", "networking.k8s.io"]
    resources: ["ingresses/status"]
    verbs: ["update"]
  - apiGroups: ["traefik.io"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["discovery.k8s.io"]
    resources: ["endpointslices"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: traefik
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: traefik
subjects:
  - kind: ServiceAccount
    name: traefik
    namespace: traefik
---
apiVersion: networking.k8s.io/v1
kind: IngressClass
metadata:
  name: traefik
  annotations:
    ingressclass.kubernetes.io/is-default-class: "true"
spec:
  controller: traefik.io/ingress-controller
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: traefik
  namespace: traefik
  labels:
    app.kubernetes.io/name: traefik
    app.kubernetes.io/component: controller
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: traefik
  template:
    metadata:
      labels:
        app.kubernetes.io/name: traefik
        app.kubernetes.io/component: controller
    spec:
      serviceAccountName: traefik
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      nodeSelector:
        ingress-ready: "true"
      tolerations:
        - key: node-role.kubernetes.io/control-plane
          operator: Exists
          effect: NoSchedule
        - key: node-role.kubernetes.io/master
          operator: Exists
          effect: NoSchedule
      containers:
        - name: traefik
          image: traefik:v3.6
          args:
            - --entrypoints.web.address=:80
            - --entrypoints.websecure.address=:443
            - --providers.kubernetesingress
            - --providers.kubernetesingress.ingressclass=traefik
            - --providers.kubernetesingress.allowemptyservices=true
            - --ping=true
            - --log.level=INFO
          ports:
            - name: web
              containerPort: 80
              hostPort: 80
              protocol: TCP
            - name: websecure
              containerPort: 443
              hostPort: 443
              protocol: TCP
          securityContext:
            capabilities:
              add: [NET_BIND_SERVICE]
              drop: [ALL]
          readinessProbe:
            httpGet:
              path: /ping
              port: 8080
            initialDelaySeconds: 2
            periodSeconds: 5
          livenessProbe:
            httpGet:
              path: /ping
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
MANIFEST

echo "⏳ Waiting for Traefik controller to be ready..."

# Wait for the DaemonSet to schedule at least one pod before checking readiness
echo "   Waiting for Traefik pod to be scheduled..."
for i in $(seq 1 30); do
  if kubectl get pods -n traefik -l app.kubernetes.io/name=traefik --no-headers 2>/dev/null | grep -q .; then
    break
  fi
  sleep 2
done

kubectl wait --namespace traefik \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/name=traefik \
  --timeout=120s

echo "✅ Traefik ingress controller is ready!"
echo ""
echo "Your Kind cluster now routes:"
echo "  http://<host>.localhost  →  Ingress → Service → Pod"
echo ""
echo "Image builds use Kaniko → registry:5000 (no Docker daemon needed)"
