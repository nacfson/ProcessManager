# Plan: Replace Password Auth with Magic-Link Login

## Background

### What is a magic link?

A magic link is passwordless login. The user enters only their email address. The server emails them a short-lived, single-use URL containing a cryptographically random token. Clicking the link proves the user controls that inbox, so it replaces both the password *and* the separate email-verification step. This is the same login flow used by Claude (Anthropic) and Slack.

A useful side effect: login and registration collapse into a single flow. If an unknown email requests a link, an account is created on the first successful click. There is no separate `/register` page anymore.

### Current state

The auth service (`cmd/auth`) implements custom email+password auth:

- `users` table with bcrypt `password_hash`, `is_admin`, `is_verified` (`internal/auth/store.go`).
- Opaque session tokens, SHA-256-hashed in the `sessions` table, delivered as the `nacfson_session` cookie or a bearer token (`internal/auth/session.go`).
- Registration sends a verification email via `net/smtp` using a token in the `email_verifications` table (`internal/auth/handler.go`).
- ingress-nginx protects the manager and project apps through `GET /verify`.
- Project PN logs in through `POST /api/auth/session` with email+password.
- Admins are seeded via `BOOTSTRAP_ADMIN_EMAIL`/`BOOTSTRAP_ADMIN_PASSWORD` and the `cmd/sync-admins` job.

Almost every building block for magic links already exists: the verification-token flow, SMTP sending, session issuance, the `rd` redirect allowlist (`internal/auth/redirect.go`), the rate limiter (`internal/auth/ratelimit.go`), and an unused `NewAuthCode()` helper (`internal/auth/authcode.go`). The work is mostly rewiring, not building new systems.

### What does NOT change

Sessions, cookies, `GET /verify` ingress auth, `RequireAuth` middleware, internal APIs, and the manager are untouched. Magic links only change how a session gets *created*, not how it is used.

## Target flow

```
Browser                      Auth service                    Email
   |  GET /login                  |                             |
   |----------------------------->|  (email-only form)          |
   |  POST /login {email, rd}     |                             |
   |----------------------------->|  upsert user if new,        |
   |                              |  store hashed token (15min),|
   |                              |  send link ----------------->|
   |  302 -> /link-sent           |                             |
   |                              |                             |
   |  user clicks link in email   |                             |
   |  GET /magic?token=..&rd=..   |                             |
   |----------------------------->|  interstitial page,         |
   |                              |  token NOT consumed yet     |
   |  POST /magic {token, rd}     |                             |
   |----------------------------->|  atomic consume, verify     |
   |                              |  user, create session       |
   |  Set-Cookie nacfson_session  |                             |
   |  302 -> validated rd         |                             |
```

The interstitial `GET /magic` page (a "Continue to sign in" button that POSTs) exists because corporate mail scanners prefetch GET links; if GET consumed the one-time token, the scanner would burn it before the user clicks.

## Design decisions

- **Token storage:** new `login_tokens` table storing SHA-256 *hashes* (the current `email_verifications` table stores raw tokens — this is an improvement). 32 random bytes, 15-minute TTL, single use, one active token per user. Reuse `NewAuthCode()`.
- **Atomic consume:** redeem with a single `DELETE ... WHERE token_hash=$1 AND expires_at > now() RETURNING user_id` so a token can never be used twice.
- **Anti-enumeration:** `POST /login` always answers "check your email", whether or not the account existed.
- **Rate limiting:** reuse the existing IP+email `loginLimiter` for link requests, plus a per-email resend throttle (~60s).
- **Dev fallback:** when `SMTP_HOST` is unset, print the link to stdout and show it on the link-sent page, exactly like the current registration flow.
- **First login = verification:** consuming a link sets `is_verified = true`, so the separate verify-email flow is deleted.

## Implementation steps

### Step 1: Store — `login_tokens` table and methods

Files: `internal/auth/store.go`, `internal/auth/store_test.go`

- Add to the schema:
  ```sql
  CREATE TABLE IF NOT EXISTS login_tokens (
      token_hash TEXT PRIMARY KEY,
      user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      expires_at TIMESTAMPTZ NOT NULL,
      created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  CREATE INDEX IF NOT EXISTS idx_login_tokens_user ON login_tokens(user_id);
  ```
- New methods, following the existing `email_verifications` style:
  - `CreateLoginToken(ctx, userID, tokenHash, expiresAt)`
  - `ConsumeLoginToken(ctx, tokenHash) (*LoginToken, error)` — the atomic delete-returning statement above.
  - `DeleteLoginTokensByUserID(ctx, userID)` — invalidate prior links on a new request.
  - `LatestLoginTokenCreatedAt(ctx, userID)` — for the resend throttle.
  - `DeleteExpiredLoginTokens(ctx)` — called from a ticker goroutine in `cmd/auth/main.go`.
- Tests: insert/consume/double-consume/expired cases (guarded by `AUTH_DATABASE_URL` like the existing integration tests).

### Step 2: Extract a shared mailer

Files: new `internal/auth/mailer.go`, `internal/auth/handler.go`

The SMTP send block is duplicated twice inside `Register`. Extract a `Mailer` with `Send(to, subject, body) error` and `Configured() bool`, reading `SMTP_HOST/PORT/USER/PASS|PASSWORD/FROM` exactly as today. Keep the async `go func()` send with error logging. Add the magic-link email content: subject "Sign in to nacfson.cloud", plaintext body with the link and "expires in 15 minutes; if you didn't request this, ignore it".

### Step 3: Handlers — passwordless login flow

File: `internal/auth/handler.go`

Routes after this step:

```go
mux.HandleFunc("GET /login", h.LoginForm)         // email-only form
mux.HandleFunc("POST /login", h.RequestMagicLink) // replaces password Login
mux.HandleFunc("GET /link-sent", h.LinkSent)      // replaces /verification-sent
mux.HandleFunc("GET /magic", h.MagicForm)         // interstitial Continue page
mux.HandleFunc("POST /magic", h.MagicConsume)     // consume token, create session
mux.HandleFunc("POST /logout", h.Logout)          // unchanged
// /register and /verify-email become permanent redirects to /login
```

- `RequestMagicLink`: normalize + validate email (`mail.ParseAddress`), validate `return_to` via `validateReturnTo`, rate limit, create the user with empty password hash if missing, enforce the resend throttle (silently succeed if throttled), delete old tokens, store a new hashed token, email or print the link, redirect to `/link-sent`.
- `MagicForm` (GET): render the Continue page without touching the token.
- `MagicConsume` (POST): `ConsumeLoginToken`; on failure redirect to `/login?error=link expired or already used`; on success `VerifyUser`, `NewSessionToken` + `CreateSession`, `SetSessionCookie`, redirect to the validated `rd` — the same tail as today's `VerifyEmail`.
- Delete the password `Login`, `Register`, `RegisterForm`, `VerificationSent`, and `VerifyEmail` handlers; drop the `bcrypt` import from this file.
- Update `handler_test.go`: request-link for existing/new user, bad email, rate limiting, resend throttle, GET /magic does not consume, POST /magic happy path (cookie set, user verified, redirect), expired/reused token, redirect validation.

### Step 4: UI templates

Files: `web/templates/`

- `login.html`: keep the glassmorphic panel; single email input + "Send sign-in link" button; remove the password field and the register link; keep hidden `return_to`.
- New `link_sent.html`: "Check your email — we sent a sign-in link. It expires in 15 minutes." Render the raw link only in the no-SMTP dev fallback.
- New `magic.html`: "Continue to sign in" button posting hidden `token` + `rd`; error state for a missing token.
- Delete `register.html` and `verification_sent.html`.

### Step 5: JSON API for external consumers (Project PN)

File: `internal/auth/handler.go`

- Add `POST /api/auth/magic-link` `{ "email": "...", "redirect": "..." }` → always 202 (anti-enumeration), same CORS gate (`allowAPIOrigin`) and rate limiter. The emailed link points at the central `/magic` page; after consuming, the shared cookie is set and the consumer can rely on it or poll `GET /api/auth/session`.
- Keep `GET /api/auth/session`, `GET /api/auth/me`, `DELETE /api/auth/session` unchanged.
- Keep the password-based `POST /api/auth/session` temporarily (marked deprecated) because Project PN logs in with email+password today. It is removed in Step 7 after PN migrates.

### Step 6: Bootstrap, sync-admins, and Kubernetes

Files: `cmd/auth/main.go`, `cmd/sync-admins/main.go`, `k8s/auth/*`

- `bootstrapAdmin` creates the `BOOTSTRAP_ADMIN_EMAIL` user with an empty password hash, `is_admin=true`, `is_verified=true`; stop reading `BOOTSTRAP_ADMIN_PASSWORD` and remove it from `bootstrap-secret.yaml`. The admin then signs in via magic link like everyone else.
- `cmd/sync-admins`: the YAML becomes a plain list of admin emails; upsert verified admins with no password; drop bcrypt.
- Add `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM` to the auth Deployment from a Secret. **This is the main operational risk: without a working SMTP relay nobody can log in** (existing sessions keep working for their 7-day TTL). Document the requirement in the README.
- Start the expired-token cleanup ticker in `cmd/auth/main.go`.

### Step 7: Cleanup (after Project PN migrates)

- Remove the password-based `POST /api/auth/session` and the `Password` field from `apiLoginRequest`.
- Remove `UpdateUserPassword`; drop the `email_verifications` table (superseded by `login_tokens`) and the `password_hash` column with guarded migrations.
- Remove `golang.org/x/crypto/bcrypt` from `go.mod` if nothing references it; `go mod tidy`; full test run.

## Security checklist

- Tokens: ≥32 random bytes, stored hashed, 15-minute TTL, single-use via atomic consume, one active token per user.
- GET never consumes the token (scanner-safe interstitial).
- Uniform "check your email" response — no account enumeration.
- Rate limits on link requests (IP+email) plus a per-email resend throttle.
- `rd` still funneled through `validateReturnTo` at every step.
- Session cookie unchanged: HttpOnly, Secure, SameSite=Lax, hashed token in DB.
