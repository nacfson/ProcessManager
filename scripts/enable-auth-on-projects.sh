#!/usr/bin/env bash
set -euo pipefail

# Enables ingress-nginx auth protection on project ingresses that use the
# manager-style gateway. Project PN keeps its SPA shell public and validates
# shared session cookies in its own backend.
# Run this only after auth-service is running and projects are registered.

echo "==> Applying Project PN ingress (backend cookie auth)"
kubectl apply -f k8s/project-pn/ingress.yaml

echo "==> Enabling auth on portfolio-web"
kubectl apply -f k8s/portfolio-web/ingress.yaml

echo "==> Auth enabled on existing projects."
