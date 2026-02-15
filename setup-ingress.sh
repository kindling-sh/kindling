#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────
# setup-ingress.sh — Install ingress-nginx on a Kind cluster
#
# This script deploys the ingress-nginx controller with the
# Kind-specific patches so that it binds to the host ports
# mapped in kind-config.yaml (80 → 80, 443 → 443).
#
# Usage:
#   ./setup-ingress.sh
#
# Prerequisites:
#   - Kind cluster created with kind-config.yaml
#   - kubectl configured to talk to the Kind cluster
# ─────────────────────────────────────────────────────────────────
set -euo pipefail

echo "📦 Installing ingress-nginx for Kind..."

kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

echo "⏳ Waiting for ingress-nginx controller to be ready..."

kubectl wait --namespace ingress-nginx \
  --for=condition=ready pod \
  --selector=app.kubernetes.io/component=controller \
  --timeout=120s

echo "✅ ingress-nginx is ready!"
echo ""
echo "Your Kind cluster now routes:"
echo "  http://<host>.localhost  →  Ingress → Service → Pod"
echo ""
echo "Configured hosts:"
echo "  http://sample-app.localhost      → sample-app"
echo "  http://gateway.localhost         → microservices gateway"
echo "  http://orders.localhost          → microservices orders"
echo "  http://inventory.localhost       → microservices inventory"
