# AGENTS.md

## Cursor Cloud specific instructions

### What this repo is
Go monorepo (`go 1.26`) building three binaries from `cmd/`: `manager` (HTMX dashboard), `auth` (central auth service backed by Postgres), and `sync-admins` (one-shot admin reconcile job). Everything under `k8s/` is deployment config for images built in other repos, not buildable here. See `README.md` for full architecture.

### Standard commands (already documented / obvious)
- Build: `go build ./...`
- Lint: `go vet ./...`
- Test: `go test ./...` (all unit tests run against an in-memory fake Kubernetes client and need no cluster or DB).
- Dependencies are refreshed automatically on startup by the update script (`go mod download`).

### Running the services locally (non-obvious wiring)
The dashboard depends on the auth service, which depends on Postgres. Run all three:

1. Postgres (installed via apt in the cloud VM, cluster `16 main`):
   - Start: `sudo pg_ctlcluster 16 main start`
   - DB `auth_db` with `postgres`/`postgres`; DSN: `postgres://postgres:postgres@localhost:5432/auth_db?sslmode=disable`.
2. Auth service (runs its own migrations + bootstraps an admin when `BOOTSTRAP_ADMIN_*` are set):
   ```
   DATABASE_URL="postgres://postgres:postgres@localhost:5432/auth_db?sslmode=disable" \
   COOKIE_DOMAIN=nacfson.cloud AUTH_INTERNAL_TOKEN=devtoken \
   BOOTSTRAP_ADMIN_EMAIL=admin@nacfson.cloud BOOTSTRAP_ADMIN_PASSWORD=admin12345 \
   PORT=8081 go run ./cmd/auth
   ```
3. Manager with the in-memory fake Kubernetes client (no cluster needed). `AUTH_INTERNAL_TOKEN` MUST match the auth service's value or session validation fails:
   ```
   AUTH_INTERNAL_TOKEN=devtoken PORT=8080 go run ./cmd/manager \
     -fake-k8s -auth-service-url=http://localhost:8081 \
     -auth-host=auth.nacfson.cloud -domain=nacfson.cloud
   ```
- The auth service calls `template.ParseGlob("web/templates/*.html")`, so it (and the manager) must be started from the repo root.

### Non-obvious gotchas
- Session (`nacfson_session`) and CSRF (`nacfson_csrf`) cookies are `Secure` and the session cookie is scoped to domain `nacfson.cloud`. Browsers only store/send them over HTTPS on `*.nacfson.cloud` hosts. To exercise the UI in a real browser you must front the Go services with an HTTPS reverse proxy and add hosts entries, e.g. `127.0.0.1 manager.nacfson.cloud auth.nacfson.cloud`, with the manager on `manager.nacfson.cloud` and auth on `auth.nacfson.cloud` (both on 443). The manager's login redirect always targets `https://<auth-host>` on the standard port unless `-ingress-https-port` is set.
- The dashboard loads HTMX from `https://unpkg.com/htmx.org@1.9.12` with a Subresource Integrity (`integrity=`) hash. The currently served file does not match that pinned hash, so browsers refuse to execute HTMX; every `hx-post`/`hx-get` mutation form then silently falls back to a plain GET and does nothing. Read-only pages (login, dashboard, lists, detail views) still render fine. For automated end-to-end testing of create/delete actions, drive the HTTP API directly (form POST with the `csrf_token` field equal to the `nacfson_csrf` cookie), not the browser.
- CSRF uses a double-submit cookie: POST/PUT/PATCH/DELETE require `csrf_token` (form field or `X-CSRF-Token` header) to equal the `nacfson_csrf` cookie. GET first sets that cookie.
- Manager logs `SHARED_DATABASE_URL not set; database listing disabled` and still runs; that shared Postgres is optional (only powers the Databases feature).

### Minimal end-to-end sanity check
Login -> create project -> register process (creates Deployment/Service/Ingress in the fake k8s client) via the API:
```
curl -sk -c jar.txt --data-urlencode email=admin@nacfson.cloud --data-urlencode password=admin12345 \
  --data-urlencode return_to=https://manager.nacfson.cloud/ https://auth.nacfson.cloud/login
curl -sk -b jar.txt -c jar.txt -o /dev/null https://manager.nacfson.cloud/projects
CSRF=$(grep nacfson_csrf jar.txt | awk '{print $NF}')
curl -sk -b jar.txt --data-urlencode name=demo --data-urlencode namespace=demo-ns \
  --data-urlencode domainHost=demo.nacfson.cloud --data-urlencode type=simple \
  --data-urlencode csrf_token=$CSRF https://manager.nacfson.cloud/projects
curl -sk -b jar.txt --data-urlencode name=hello --data-urlencode image=nginx:latest \
  --data-urlencode port=80 --data-urlencode replicas=1 --data-urlencode arch=amd64 \
  --data-urlencode memoryLimit=128Mi --data-urlencode csrf_token=$CSRF \
  https://manager.nacfson.cloud/projects/demo/processes
```
