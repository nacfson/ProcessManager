#!/usr/bin/env bash
# Deploy Compose auth stack to a remote server (workflow from remote setup chat).
#
# Usage:
#   ./scripts/deploy-remote.sh user@host
#   ./scripts/deploy-remote.sh user@host ~/processmanager
#   REMOTE_DIR=/opt/processmanager ./scripts/deploy-remote.sh user@host
#
# What it does:
#   1. rsync docker-compose.yml + helper scripts to the remote host
#   2. optionally rsync .env if present locally (never overwrites remote .env unless --force-env)
#   3. on remote: docker pull GHCR image + docker compose up -d
#
# Prerequisites on remote: Docker + Compose plugin.
# Prerequisites locally: rsync, ssh. Optional: local .env already filled.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FORCE_ENV=0
REMOTE=""
REMOTE_DIR_DEFAULT="~/processmanager"

usage() {
  cat <<'EOF'
Usage: ./scripts/deploy-remote.sh [--force-env] user@host [remote-dir]

  --force-env   overwrite remote .env with local .env (default: keep remote .env)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --force-env)
      FORCE_ENV=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "unknown flag: $1" >&2
      usage >&2
      exit 1
      ;;
    *)
      if [[ -z "$REMOTE" ]]; then
        REMOTE="$1"
      else
        REMOTE_DIR_DEFAULT="$1"
      fi
      shift
      ;;
  esac
done

if [[ -z "$REMOTE" ]]; then
  usage >&2
  exit 1
fi

REMOTE_DIR="${REMOTE_DIR:-$REMOTE_DIR_DEFAULT}"
IMAGE="${PROCESSMANAGER_IMAGE:-ghcr.io/nacfson/processmanager:latest}"

echo "==> Ensuring remote directory ${REMOTE}:${REMOTE_DIR}"
ssh "$REMOTE" "mkdir -p ${REMOTE_DIR}"

echo "==> rsync compose + scripts"
RSYNC_PATHS=(
  docker-compose.yml
  .env.example
  scripts/remote-up.sh
  scripts/pull-env-from-bitwarden.sh
)
rsync -avz --progress "${RSYNC_PATHS[@]}" "${REMOTE}:${REMOTE_DIR}/"

# Flatten scripts into remote dir for easy ./remote-up.sh usage, and keep scripts/ too.
ssh "$REMOTE" "cd ${REMOTE_DIR} && mkdir -p scripts && \
  if [[ -f remote-up.sh ]]; then mv -f remote-up.sh scripts/; fi; \
  if [[ -f pull-env-from-bitwarden.sh ]]; then mv -f pull-env-from-bitwarden.sh scripts/; fi; \
  chmod +x scripts/*.sh 2>/dev/null || true"

if [[ -f .env ]]; then
  if [[ "$FORCE_ENV" -eq 1 ]]; then
    echo "==> rsync local .env -> remote (overwrite)"
    rsync -avz --chmod=F600 .env "${REMOTE}:${REMOTE_DIR}/.env"
  else
    echo "==> checking remote .env"
    if ssh "$REMOTE" "test -f ${REMOTE_DIR}/.env"; then
      echo "    remote .env exists — keeping it (pass --force-env to overwrite)"
    else
      echo "    remote .env missing — copying local .env"
      rsync -avz --chmod=F600 .env "${REMOTE}:${REMOTE_DIR}/.env"
    fi
  fi
else
  echo "==> no local .env; remote must already have .env or use Bitwarden pull"
fi

echo "==> remote pull + compose up (${IMAGE})"
ssh -t "$REMOTE" "bash -s" <<EOF
set -euo pipefail
cd ${REMOTE_DIR}
export PROCESSMANAGER_IMAGE='${IMAGE}'
if [[ ! -f .env ]]; then
  echo "ERROR: ${REMOTE_DIR}/.env is missing." >&2
  echo "Create it from .env.example, or run:" >&2
  echo "  ./scripts/pull-env-from-bitwarden.sh processmanager-remote-env" >&2
  exit 1
fi
chmod 600 .env
docker pull "\${PROCESSMANAGER_IMAGE}"
if [[ -x ./scripts/remote-up.sh ]]; then
  ./scripts/remote-up.sh
else
  docker compose --env-file .env up -d --remove-orphans
  docker compose ps
fi
echo
curl -fsS "http://127.0.0.1:\${AUTH_HOST_PORT:-8080}/healthz" && echo "  auth healthy" || echo "  health check failed (is AUTH_HOST_PORT open locally?)"
EOF

echo "==> done"
echo "Auth should be reachable on the server at port 8080 (put auth.nacfson.cloud in front of it)."
