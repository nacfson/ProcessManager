# Remote Compose Deploy

Single-instance central auth for a private app (no home-lab Kubernetes required).

## What you get

| Service | Purpose |
|---------|---------|
| `postgres` | Auth database |
| `auth` | Central auth (`./auth-service`) on host port `8080` |
| `manager` (optional profile) | Dashboard with `USE_FAKE_K8S=true` |

## 1. Export the image (build machine)

```bash
./scripts/export-image.sh processmanager-latest.tar.gz
scp processmanager-latest.tar.gz user@remote:~/
```

## 2. Load on the remote server

```bash
gunzip -c processmanager-latest.tar.gz | docker load
# also copy docker-compose.yml, .env.example, and scripts/ onto the host
```

## 3. Inject secrets

### Option A — `.env` file

```bash
cp .env.example .env
chmod 600 .env
# edit passwords / COOKIE_DOMAIN / AUTH_INTERNAL_TOKEN / ...
./scripts/remote-up.sh
```

### Option B — Bitwarden Secure Note (password manager)

Store the whole `.env` body in one Secure Note, then pull it onto the server.

1. Open Bitwarden → **Add item** → **Secure note**
2. Name it e.g. `processmanager-remote-env`
3. Paste the `KEY=value` block from [`bitwarden-secure-note.example.env`](bitwarden-secure-note.example.env) (replace every `REPLACE_*` / sample secret)
4. Save the note
5. On the remote server:

```bash
# one-time: install bw CLI, then login
bw login
export BW_SESSION="$(bw unlock --raw)"

# write ./.env from the secure note (name or item id)
./scripts/pull-env-from-bitwarden.sh "processmanager-remote-env"
./scripts/remote-up.sh
```

Manual alternative (no script):

```bash
bw get notes "processmanager-remote-env" | grep -E '^[A-Za-z_][A-Za-z0-9_]*=' > .env
chmod 600 .env
```

Re-pull after you rotate secrets in the note, then `./scripts/remote-up.sh` again (or `docker compose up -d`).

### Option C — Bitwarden Secrets Manager (`bws run`)

Create **one secret per key** (names must match `.env.example`: `POSTGRES_PASSWORD`, `DATABASE_URL`, …). No `.env` file on disk.

```bash
export BWS_ACCESS_TOKEN=...          # machine account token (chmod 600 file ok)
export BWS_PROJECT_ID=...            # optional but recommended
./scripts/remote-up.sh --bws
```

Equivalent raw command:

```bash
bws run --project-id "$BWS_PROJECT_ID" -- docker compose up -d
```

| Approach | Best when |
|----------|-----------|
| Secure Note + `.env` | You want one editable blob in Bitwarden |
| Secrets Manager + `bws run` | You want no `.env` file left on the server |
## 4. Point your other process at auth

```env
AUTH_MODE=central
CENTRAL_AUTH_URL=https://auth.your.domain
# or on the same docker network: http://auth:8080
```

Public JSON API:

- `POST /api/auth/session`
- `GET /api/auth/session`
- `GET /api/auth/me`
- `DELETE /api/auth/session`

## Notes

- Compose Postgres uses `sslmode=disable` in `DATABASE_URL`. Put TLS at the reverse proxy.
- Full process deploy/scale still needs a real Kubernetes API; the optional manager profile is UI-only with fake k8s.
- Keep only `BWS_ACCESS_TOKEN` (or `.env`) on the server — never bake secrets into the image.
