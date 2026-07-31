#!/usr/bin/env bash
# Build and push a multi-arch processmanager image to GHCR (amd64 + arm64).
#
# Usage:
#   export GHCR_TOKEN=ghp_...   # PAT with write:packages
#   ./scripts/push-ghcr.sh
#   ./scripts/push-ghcr.sh v0.1.0
#
# Env overrides:
#   GHCR_OWNER   (default: nacfson)
#   GHCR_IMAGE   (default: processmanager)
#   PLATFORM     (default: linux/amd64,linux/arm64)
#   GHCR_USER    (default: GHCR_OWNER)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

OWNER="${GHCR_OWNER:-nacfson}"
NAME="${GHCR_IMAGE:-processmanager}"
USER_NAME="${GHCR_USER:-$OWNER}"
PLATFORM="${PLATFORM:-linux/amd64,linux/arm64}"
REGISTRY="ghcr.io"
IMAGE_BASE="${REGISTRY}/${OWNER}/${NAME}"
VERSION_TAG="${1:-}"
BUILDER_NAME="${BUILDX_BUILDER:-processmanager-multiarch}"

if [[ -z "${GHCR_TOKEN:-}" ]]; then
  echo "GHCR_TOKEN is required (GitHub PAT with write:packages)" >&2
  exit 1
fi

echo "==> Logging in to ${REGISTRY} as ${USER_NAME}"
printf '%s' "${GHCR_TOKEN}" | docker login "${REGISTRY}" -u "${USER_NAME}" --password-stdin

# Multi-platform push needs a docker-container builder (not the default docker driver).
if ! docker buildx inspect "${BUILDER_NAME}" >/dev/null 2>&1; then
  echo "==> Creating buildx builder ${BUILDER_NAME}"
  docker buildx create --name "${BUILDER_NAME}" --driver docker-container --use
else
  docker buildx use "${BUILDER_NAME}"
fi
docker buildx inspect --bootstrap >/dev/null

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
echo "On amd64 or arm64 hosts:"
echo "  docker pull ${IMAGE_BASE}:latest"
echo "  # Docker picks the matching arch automatically from the manifest list"
