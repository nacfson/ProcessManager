#!/usr/bin/env bash
# Standalone remote-server deploy for ProcessManager central auth.
# Secrets come from a Bitwarden Secure Note (password manager CLI: `bw`).
#
# On the server:
#   chmod +x deploy-auth.sh
#   export BW_SESSION="$(bw unlock --raw)"   # after: bw login
#   ./deploy-auth.sh
#   ./deploy-auth.sh "processmanager-remote-env"   # custom note name/id
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

IMAGE="${PROCESSMANAGER_IMAGE:-ghcr.io/nacfson/processmanager:latest}"
COMPOSE_FILE="${DIR}/docker-compose.yml"
ENV_FILE="${DIR}/.env"
NOTE_NAME_OR_ID="${1:-processmanager-remote-env}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: '$1' is required but not installed." >&2
    exit 1
  }
}

need docker
need bw
need curl
docker compose version >/dev/null 2>&1 || {
  echo "ERROR: Docker Compose plugin is required (docker compose)." >&2
  exit 1
}

if [[ -z "${BW_SESSION:-}" ]]; then
  echo "ERROR: BW_SESSION is unset." >&2
  echo "Run:" >&2
  echo "  bw login" >&2
  echo "  export BW_SESSION=\"\$(bw unlock --raw)\"" >&2
  echo "  $0 ${NOTE_NAME_OR_ID}" >&2
  exit 1
fi

# --- write compose file (idempotent) ---
cat >"$COMPOSE_FILE" <<'YAML'
services:
  postgres:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${POSTGRES_DB:-auth_db}
      POSTGRES_USER: ${POSTGRES_USER:-auth_user}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}
    volumes:
      - auth-pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $$POSTGRES_USER -d $$POSTGRES_DB"]
      interval: 5s
      timeout: 5s
      retries: 10
    networks:
      - authnet

  auth:
    image: ${PROCESSMANAGER_IMAGE:-ghcr.io/nacfson/processmanager:latest}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    entrypoint: ["./auth-service"]
    ports:
      - "${AUTH_HOST_PORT:-8080}:8080"
    environment:
      PORT: "8080"
      DATABASE_URL: ${DATABASE_URL:?DATABASE_URL is required}
      COOKIE_DOMAIN: ${COOKIE_DOMAIN:?COOKIE_DOMAIN is required}
      SESSION_TTL: ${SESSION_TTL:-168h}
      AUTH_INTERNAL_TOKEN: ${AUTH_INTERNAL_TOKEN:?AUTH_INTERNAL_TOKEN is required}
      BOOTSTRAP_ADMIN_EMAIL: ${BOOTSTRAP_ADMIN_EMAIL:-}
      BOOTSTRAP_ADMIN_PASSWORD: ${BOOTSTRAP_ADMIN_PASSWORD:-}
      AUTH_API_ALLOWED_ORIGINS: ${AUTH_API_ALLOWED_ORIGINS:-}
      AUTH_ALLOWED_REDIRECTS: ${AUTH_ALLOWED_REDIRECTS:-}
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:8080/healthz"]
      interval: 15s
      timeout: 3s
      retries: 5
      start_period: 15s
    networks:
      - authnet

networks:
  authnet:

volumes:
  auth-pg-data:
YAML

# --- pull .env from Bitwarden Secure Note ---
echo "==> pulling Secure Note '${NOTE_NAME_OR_ID}' from Bitwarden"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

if ! bw get notes "$NOTE_NAME_OR_ID" >"$TMP" 2>/dev/null; then
  if command -v jq >/dev/null 2>&1; then
    bw get item "$NOTE_NAME_OR_ID" | jq -r '.notes // empty' >"$TMP"
  else
    echo "ERROR: could not read Secure Note '${NOTE_NAME_OR_ID}'." >&2
    echo "Check the note name/id, or install jq for item-id fallback." >&2
    exit 1
  fi
fi

if [[ ! -s "$TMP" ]]; then
  echo "ERROR: Secure Note '${NOTE_NAME_OR_ID}' is empty." >&2
  exit 1
fi

# Keep KEY=VALUE lines only (drop comments / markers)
grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$TMP" >"${ENV_FILE}.tmp"
mv "${ENV_FILE}.tmp" "$ENV_FILE"
chmod 600 "$ENV_FILE"

if ! grep -qE '^POSTGRES_PASSWORD=' "$ENV_FILE" \
  || ! grep -qE '^DATABASE_URL=' "$ENV_FILE" \
  || ! grep -qE '^COOKIE_DOMAIN=' "$ENV_FILE" \
  || ! grep -qE '^AUTH_INTERNAL_TOKEN=' "$ENV_FILE"; then
  echo "ERROR: Secure Note must include POSTGRES_PASSWORD, DATABASE_URL, COOKIE_DOMAIN, AUTH_INTERNAL_TOKEN." >&2
  exit 1
fi

if grep -q 'REPLACE_WITH_' "$ENV_FILE"; then
  echo "ERROR: Secure Note still has REPLACE_WITH_* placeholders. Update the note in Bitwarden." >&2
  exit 1
fi

echo "    wrote ${ENV_FILE} ($(grep -cE '^[A-Za-z_][A-Za-z0-9_]*=' "$ENV_FILE") keys)"

# --- optional private GHCR login ---
if [[ -n "${GHCR_TOKEN:-}" ]]; then
  echo "==> docker login ghcr.io"
  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u "${GHCR_USER:-nacfson}" --password-stdin
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

echo "==> pull ${PROCESSMANAGER_IMAGE:-$IMAGE}"
docker pull "${PROCESSMANAGER_IMAGE:-$IMAGE}"

echo "==> compose up"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --remove-orphans
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps

PORT="${AUTH_HOST_PORT:-8080}"
echo "==> health check"
if curl -fsS "http://127.0.0.1:${PORT}/healthz"; then
  echo
  echo "Auth is up on :${PORT}"
  echo "Put https://auth.nacfson.cloud -> this host:${PORT}"
else
  echo
  echo "Health check failed. Logs:"
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" logs --tail=80 auth postgres
  exit 1
fi
