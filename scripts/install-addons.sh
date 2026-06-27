#!/usr/bin/env bash
set -euo pipefail

# Install CNI, ingress-nginx, and cert-manager on the control plane.
# Assumes kubectl is configured.

CALICO_VERSION="${CALICO_VERSION:-v3.29.1}"
NGINX_VERSION="${NGINX_VERSION:-v1.12.0}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

echo "==> Installing Calico CNI"
kubectl apply -f "https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/calico.yaml"

echo "==> Waiting for Calico to be ready"
kubectl wait --for=condition=ready pod -l k8s-app=calico-node -n kube-system --timeout=120s || true

echo "==> Installing ingress-nginx"
kubectl apply -f "https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-${NGINX_VERSION}/deploy/static/provider/cloud/deploy.yaml"

echo "==> Installing cert-manager"
kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"

echo "==> Waiting for cert-manager"
kubectl wait --for=condition=ready pod -l app=cert-manager -n cert-manager --timeout=120s || true

echo "==> Addons installed"
echo "Now apply your ClusterIssuer manifest: kubectl apply -f k8s/cluster/clusterissuer.yaml"
