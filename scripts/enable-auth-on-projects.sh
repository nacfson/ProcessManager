#!/usr/bin/env bash
set -euo pipefail

# Enables ingress-nginx auth protection on existing project ingresses.
# Run this only after auth-service is running and projects are registered.

echo "==> Enabling auth on project-pn"
kubectl apply -f k8s/project-pn/ingress.yaml

echo "==> Enabling auth on portfolio-web"
kubectl apply -f k8s/portfolio-web/ingress.yaml

echo "==> Auth enabled on existing projects."
