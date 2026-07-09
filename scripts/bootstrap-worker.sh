#!/usr/bin/env bash
set -euo pipefail

# Join this node to an existing Kubernetes control plane.
# Run as root or with sudo after bootstrap-common.sh.

JOIN_COMMAND="${JOIN_COMMAND:-}"

if [[ -z "$JOIN_COMMAND" ]]; then
  echo "ERROR: JOIN_COMMAND is not set."
  echo "Copy the join command from the control plane (/root/k8s-join/join-command.sh) and run:"
  echo "  JOIN_COMMAND='kubeadm join ...' ./bootstrap-worker.sh"
  exit 1
fi

echo "==> Joining cluster"
eval "$JOIN_COMMAND"

echo "==> Worker node joined"
