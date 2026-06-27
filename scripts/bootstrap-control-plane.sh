#!/usr/bin/env bash
set -euo pipefail

# Bootstrap a Kubernetes control plane on this node.
# Run as root or with sudo after bootstrap-common.sh.

POD_CIDR="${POD_CIDR:-10.244.0.0/16}"
CONTROL_PLANE_IP="${CONTROL_PLANE_IP:-}"
API_SERVER_PORT="${API_SERVER_PORT:-6443}"

if [[ -z "$CONTROL_PLANE_IP" ]]; then
  CONTROL_PLANE_IP=$(hostname -I | awk '{print $1}')
  echo "==> Auto-detected control plane IP: $CONTROL_PLANE_IP"
fi

echo "==> Initializing kubeadm"
kubeadm init \
  --pod-network-cidr="$POD_CIDR" \
  --apiserver-advertise-address="$CONTROL_PLANE_IP" \
  --apiserver-bind-port="$API_SERVER_PORT" \
  --node-name "$(hostname)" \
  --upload-certs

echo "==> Setting up kubectl for root"
mkdir -p /root/.kube
cp -i /etc/kubernetes/admin.conf /root/.kube/config
chown root:root /root/.kube/config

echo "==> Saving join command"
mkdir -p /root/k8s-join
kubeadm token create --print-join-command > /root/k8s-join/join-command.sh
chmod +x /root/k8s-join/join-command.sh

echo "==> Control plane ready"
echo "Join command saved to /root/k8s-join/join-command.sh"
