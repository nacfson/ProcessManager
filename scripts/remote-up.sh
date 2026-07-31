#!/usr/bin/env bash
# Start the remote Compose auth stack.
#
# Modes:
#   ./scripts/remote-up.sh              # uses ./.env if present
#   ./scripts/remote-up.sh --bws        # inject secrets via `bws run`
#   ./scripts/remote-up.sh --profile manager
#   ./scripts/remote-up.sh --down
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

USE_BWS=0
DOWN=0
COMPOSE_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bws)
      USE_BWS=1
      shift
      ;;
    --down)
      DOWN=1
      shift
      ;;
    --profile)
      COMPOSE_ARGS+=(--profile "$2")
      shift 2
      ;;
    *)
      COMPOSE_ARGS+=("$1")
      shift
      ;;
  esac
done

run_compose() {
  if [[ "$USE_BWS" -eq 1 ]]; then
    if ! command -v bws >/dev/null 2>&1; then
      echo "bws not found. Install Bitwarden Secrets Manager CLI: https://bitwarden.com/help/secrets-manager-cli/" >&2
      exit 1
    fi
    if [[ -z "${BWS_ACCESS_TOKEN:-}" ]]; then
      echo "BWS_ACCESS_TOKEN is required for --bws mode" >&2
      exit 1
    fi
    BWS_ARGS=(run)
    if [[ -n "${BWS_PROJECT_ID:-}" ]]; then
      BWS_ARGS+=(--project-id "${BWS_PROJECT_ID}")
    fi
    bws "${BWS_ARGS[@]}" -- docker compose "${COMPOSE_ARGS[@]}" "$@"
  else
    if [[ -f .env ]]; then
      docker compose --env-file .env "${COMPOSE_ARGS[@]}" "$@"
    else
      echo "warning: .env not found; relying on exported environment variables" >&2
      docker compose "${COMPOSE_ARGS[@]}" "$@"
    fi
  fi
}

if [[ "$DOWN" -eq 1 ]]; then
  run_compose down
  exit 0
fi

run_compose up -d --remove-orphans
run_compose ps
echo
echo "Auth health: curl -fsS http://127.0.0.1:${AUTH_HOST_PORT:-8080}/healthz"
