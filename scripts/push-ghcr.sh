#!/usr/bin/env bash
# Build and push the processmanager image to GitHub Container Registry (GHCR).
#
# Usage:
#   export GHCR_TOKEN=ghp_...   # PAT with write:packages (and read:packages)
#   ./scripts/push-ghcr.sh
#   ./scripts/push-ghcr.sh v0.1.0   # also tag a version
#
# Env overrides:
#   GHCR_OWNER   (default: nacfson)
#   GHCR_IMAGE   (default: processmanager)
#   PLATFORM     (default: linux/amd64)
#   GHCR_USER    (default: GHCR_OWNER) login username
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OWNER="${GHCR_OWNER:-nacfson}"
NAME="${GHCR_IMAGE:-processmanager}"
USER_NAME="${GHCR_USER:-$OWNER}"
PLATFORM="${PLATFORM:-linux/amd64}"
REGISTRY="ghcr.io"
IMAGE_BASE="${REGISTRY}/${OWNER}/${NAME}"
VERSION_TAG="${1:-}"

if [[ -z "${GHCR_TOKEN:-}" ]]; then
  echo "GHCR_TOKEN is required (GitHub PAT with write:packages)" >&2
  exit 1
fi

echo "==> Logging in to ${REGISTRY} as ${USER_NAME}"
printf '%s' "${GHCR_TOKEN}" | docker login "${REGISTRY}" -u "${USER_NAME}" --password-stdin

TAGS=("${IMAGE_BASE}:latest")
if [[ -n "${VERSION_TAG}" ]]; then
  TAGS+=("${IMAGE_BASE}:${VERSION_TAG}")
fi

TAG_ARGS=()
for t in "${TAGS[@]}"; do
  TAG_ARGS+=(-t "$t")
done

echo "==> Building and pushing ${TAGS[*]} (${PLATFORM})"
docker buildx build \
  --platform "${PLATFORM}" \
  "${TAG_ARGS[@]}" \
  --push \
  .

echo "Pushed:"
for t in "${TAGS[@]}"; do
  echo "  $t"
done
echo
echo "On the remote server:"
echo "  export PROCESSMANAGER_IMAGE=${IMAGE_BASE}:latest"
echo "  docker pull ${IMAGE_BASE}:latest"
echo "  ./scripts/remote-up.sh"
