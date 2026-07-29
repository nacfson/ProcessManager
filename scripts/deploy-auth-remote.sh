#!/usr/bin/env bash
# Standalone remote-server deploy for ProcessManager central auth.
# Paste this file ON THE SERVER, then:
#   chmod +x deploy-auth.sh
#   nano .env          # fill secrets first (see REQUIRED below)
#   ./deploy-auth.sh
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

IMAGE="${PROCESSMANAGER_IMAGE:-ghcr.io/nacfson/processmanager:latest}"
COMPOSE_FILE="${DIR}/docker-compose.yml"
ENV_FILE="${DIR}/.env"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: '$1' is required but not installed." >&2
    exit 1
  }
}

need docker
docker compose version >/dev/null 2>&1 || {
  echo "ERROR: Docker Compose plugin is required (docker compose)." >&2
  exit 1
}

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

# --- ensure .env exists ---
if [[ ! -f "$ENV_FILE" ]]; then
  cat >"$ENV_FILE" <<'ENV'
PROCESSMANAGER_IMAGE=ghcr.io/nacfson/processmanager:latest
AUTH_HOST_PORT=8080

POSTGRES_DB=auth_db
POSTGRES_USER=auth_user
POSTGRES_PASSWORD=REPLACE_WITH_ROTATED_PASSWORD

DATABASE_URL=postgres://auth_user:REPLACE_WITH_ROTATED_PASSWORD@postgres:5432/auth_db?sslmode=disable

COOKIE_DOMAIN=nacfson.cloud
SESSION_TTL=168h
AUTH_INTERNAL_TOKEN=REPLACE_WITH_STRONG_TOKEN

BOOTSTRAP_ADMIN_EMAIL=admin@nacfson.cloud
BOOTSTRAP_ADMIN_PASSWORD=REPLACE_WITH_STRONG_PASSWORD

AUTH_API_ALLOWED_ORIGINS=https://pn.nacfson.cloud,https://manager.nacfson.cloud
AUTH_ALLOWED_REDIRECTS=http://localhost:8081
ENV
  chmod 600 "$ENV_FILE"
  echo "Created $ENV_FILE — edit REPLACE_* values, then re-run:"
  echo "  nano $ENV_FILE"
  echo "  $0"
  exit 1
fi

chmod 600 "$ENV_FILE"

if grep -q 'REPLACE_WITH_' "$ENV_FILE"; then
  echo "ERROR: $ENV_FILE still contains REPLACE_WITH_* placeholders." >&2
  echo "Edit them first: nano $ENV_FILE" >&2
  exit 1
fi

# --- optional private GHCR login ---
# export GHCR_TOKEN=ghp_... before running if the package is private
if [[ -n "${GHCR_TOKEN:-}" ]]; then
  echo "==> docker login ghcr.io"
  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u "${GHCR_USER:-nacfson}" --password-stdin
fi

echo "==> pull $IMAGE"
# shellcheck disable=SC1090
set -a
# export vars for compose interpolation
# (compose also reads --env-file; exporting helps PROCESSMANAGER_IMAGE)
# shellcheck source=/dev/null
source "$ENV_FILE"
set +a
docker pull "${PROCESSMANAGER_IMAGE:-$IMAGE}"

echo "==> compose up"
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --remove-orphans
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps

PORT="$(grep -E '^AUTH_HOST_PORT=' "$ENV_FILE" | cut -d= -f2- || true)"
PORT="${PORT:-8080}"
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
