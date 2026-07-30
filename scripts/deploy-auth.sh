#!/usr/bin/env bash
set -euo pipefail

# Deploys the central auth service, shared app DB, and manager RBAC/secret updates.
# Run from the project root.
# Builds and pushes ghcr.io/nacfson/processmanager:latest (requires docker login to ghcr.io).
# Magic-link login requires a working SMTP relay configured in k8s/auth/smtp-secret.yaml.

IMAGE_TAG="${IMAGE_TAG:-ghcr.io/nacfson/processmanager:latest}"

echo "==> Building and pushing image ${IMAGE_TAG}"
docker buildx build --platform linux/amd64 -t "${IMAGE_TAG}" --push .

echo "==> Applying auth and data namespaces"
kubectl apply -f k8s/auth/namespace.yaml
kubectl apply -f k8s/data/namespace.yaml

if [[ -n "${GHCR_TOKEN:-}" ]]; then
  echo "==> Ensuring GHCR image pull secrets"
  for ns in nacfson-auth nacfson-personal; do
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    kubectl create secret docker-registry ghcr-pull \
      --docker-server=ghcr.io \
      --docker-username=nacfson \
      --docker-password="$GHCR_TOKEN" \
      -n "$ns" \
      --dry-run=client -o yaml | kubectl apply -f -
  done
fi

echo "==> Applying auth secrets and databases"
kubectl apply -f k8s/auth/secret.yaml
kubectl apply -f k8s/auth/bootstrap-secret.yaml
kubectl apply -f k8s/auth/smtp-secret.yaml
kubectl apply -f k8s/auth/postgres.yaml
kubectl apply -f k8s/data/secret.yaml
kubectl apply -f k8s/data/postgres.yaml

echo "==> Copying shared app DB secret into manager namespace"
kubectl get secret shared-app-db-secrets -n nacfson-data -o yaml --show-managed-fields=false | \
  sed '/^  creationTimestamp:/d;/^  resourceVersion:/d;/^  uid:/d;s/namespace: nacfson-data/namespace: nacfson-personal/' | \
  kubectl apply -f -

echo "==> Applying auth service"
kubectl apply -f k8s/auth/selfsigned-clusterissuer.yaml
kubectl apply -f k8s/auth/service.yaml
kubectl apply -f k8s/auth/ingress.yaml
kubectl apply -f k8s/auth/deployment.yaml

echo "==> Applying manager RBAC and auth DB secret"
kubectl apply -f k8s/manager/rbac.yaml
kubectl apply -f k8s/manager/auth-db-secret.yaml

echo "==> Waiting for auth database"
kubectl rollout status statefulset/auth-db -n nacfson-auth --timeout=120s

echo "==> Waiting for shared app database"
kubectl rollout status statefulset/shared-app-db -n nacfson-data --timeout=120s

echo "==> Waiting for auth service"
kubectl rollout status deployment/auth-service -n nacfson-auth --timeout=120s

echo "==> Restarting manager to pick up auth DB connection"
kubectl rollout restart deployment/processmanager -n nacfson-personal
kubectl rollout status deployment/processmanager -n nacfson-personal --timeout=120s

echo "==> Auth foundation deployed. Next: register existing projects and annotate their ingresses."
echo "    Note: magic-link login requires a working SMTP relay (k8s/auth/smtp-secret.yaml)."
