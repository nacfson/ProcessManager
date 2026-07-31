#!/usr/bin/env bash
# Build the processmanager image and export a transferable tarball.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

IMAGE="${PROCESSMANAGER_IMAGE:-processmanager:latest}"
PLATFORM="${PLATFORM:-linux/amd64}"
OUT="${1:-processmanager-latest.tar.gz}"

echo "==> Building ${IMAGE} (${PLATFORM})"
docker buildx build --platform "${PLATFORM}" -t "${IMAGE}" --load .

echo "==> Saving ${OUT}"
docker save "${IMAGE}" | gzip > "${OUT}"
echo "Wrote ${OUT}"
echo "On the remote host: gunzip -c ${OUT} | docker load"
