# Project PN Platform-Neutral Central Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make ProcessManager's central auth service the single authentication provider for Project PN across web, Tauri, iOS, and Android.

**Architecture:** ProcessManager becomes a platform-neutral auth provider with JSON session endpoints that issue and verify opaque central bearer tokens. Project PN removes ownership of passwords, email verification, and local sessions; its clients log in against `auth.nacfson.cloud`, store the returned central token, and send it to Project PN APIs. Project PN backend validates central bearer tokens through the central auth service, upserts an app-local profile keyed by central user ID, and keeps all learning data in Project PN's database.

**Tech Stack:** Go 1.x, chi/net/http, pgx/Postgres, bcrypt, opaque SHA-256-hashed sessions, Expo React Native Web/Tauri/iOS/Android, SecureStore/localStorage, Kubernetes manifests.

---

## Scope

Included:
- Central auth JSON API in ProcessManager for login, session verification, logout, and current session identity.
- Project PN backend middleware that validates central bearer tokens by calling ProcessManager auth service.
- Project PN local `users` rows become app profiles keyed by central auth user IDs.
- Project PN frontend login uses central auth for all platforms.
- Project PN deployment points to the central auth service URL.
- Existing ProcessManager ingress auth remains usable for ProcessManager/other sites, but Project PN no longer depends on web-only `X-User-*` headers.

Not included:
- OAuth/OIDC standards compliance.
- Refresh tokens.
- Native browser handoff/deep-link login.
- Self-service central registration unless already added separately.
- Migrating Project PN learning data out of the Project PN database.

## File Structure

ProcessManager repo: `/Users/hyungjuyu/Projects/Brain/ProcessManager`
- Modify `internal/auth/handler.go`: add JSON session endpoints.
- Modify `internal/auth/session.go`: add bearer token parsing helper.
- Modify `cmd/auth/main.go`: register API routes under `/api/auth`.
- Modify `internal/auth/handler_test.go`: cover login, verify, me, logout JSON endpoints.
- Modify `k8s/auth/deployment.yaml`: allow CORS origins for Project PN if implemented in handler.
- Modify `k8s/project-pn/backend.yaml`: set central auth service URL.

Project PN repo: `/Users/hyungjuyu/Projects/iOS/Project_PN`
- Modify `backend/internal/config/config.go`: add `AuthMode` and `CentralAuthURL`.
- Create `backend/internal/auth/central_client.go`: verify central tokens with ProcessManager auth.
- Modify `backend/internal/auth/service.go`: add `EnsureCentralUser`.
- Modify `backend/internal/http/auth_middleware.go`: use central-token middleware in central mode.
- Modify `backend/internal/http/router.go`: register only Project PN profile routes in central mode; disable local password/session endpoints.
- Modify `backend/internal/http/auth_handler.go`: keep `/api/auth/me`, make logout revoke central token through central auth.
- Modify frontend `src/config.ts`, `src/api/auth.ts`, `src/api/client.ts`, and `App.tsx`: point login/logout/me at central auth where appropriate and keep Project PN API calls using the central bearer token.
- Update docs to remove stale OAuth/magic-link claims and describe central-token auth.

## Auth Contract

Central auth API owned by ProcessManager:

```text
POST /api/auth/session
Authorization: none
Body: { "email": "user@example.com", "password": "password" }
Response 200: { "token": "...", "expires_at": "...", "user": { "id": "...", "email": "...", "is_admin": false } }

GET /api/auth/session
Authorization: Bearer <central-token>
Response 200: { "user": { "id": "...", "email": "...", "is_admin": false }, "expires_at": "..." }

GET /api/auth/me
Authorization: Bearer <central-token>
Response 200: { "id": "...", "email": "...", "is_admin": false }

DELETE /api/auth/session
Authorization: Bearer <central-token>
Response 204
```

Project PN API:

```text
Authorization: Bearer <central-token>
```

Project PN validates that token against ProcessManager before handling protected domain routes.

## Task 1: Add ProcessManager Bearer Token Helpers

**Files:**
- Modify: `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/session.go`
- Test: `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/session_test.go`

- [ ] **Step 1: Write failing tests**

Create `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/session_test.go`:

```go
package auth

import (
	"net/http/httptest"
	"testing"
)

func TestBearerToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer abc123")

	if got := BearerToken(req); got != "abc123" {
		t.Fatalf("expected bearer token abc123, got %q", got)
	}
}

func TestBearerTokenRejectsNonBearer(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic abc123")

	if got := BearerToken(req); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
go test ./internal/auth -run TestBearerToken -count=1
```

Expected: FAIL because `BearerToken` is undefined.

- [ ] **Step 3: Implement helper**

Add to `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/session.go`:

```go
func BearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}
```

`session.go` already imports `net/http` and `strings`, so no new imports should be required.

- [ ] **Step 4: Run passing tests**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
go test ./internal/auth -run TestBearerToken -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
git add internal/auth/session.go internal/auth/session_test.go
git commit -m "feat(auth): parse bearer session tokens"
```

## Task 2: Add ProcessManager JSON Session API

**Files:**
- Modify: `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/handler.go`
- Test: `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/handler_test.go`

- [ ] **Step 1: Write failing API tests**

Create `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/handler_test.go`:

```go
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func testHandler(t *testing.T) (*Handler, *Store) {
	t.Helper()
	dsn := getenvOrSkip(t, "AUTH_DATABASE_URL")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect auth db: %v", err)
	}
	t.Cleanup(pool.Close)

	store := NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate auth db: %v", err)
	}
	h, err := NewHandler(store, "nacfson.cloud", time.Hour)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return h, store
}

func getenvOrSkip(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Skipf("%s is required", key)
	}
	return value
}

func TestAPISessionLifecycle(t *testing.T) {
	h, store := testHandler(t)
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	email := "api-session@example.com"
	user, err := store.CreateUser(ctx, email, string(hash), false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_ = user

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/session", bytes.NewBufferString(`{"email":"api-session@example.com","password":"password123"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d body=%s", loginRec.Code, loginRec.Body.String())
	}

	var loginBody apiSessionResponse
	if err := json.NewDecoder(loginRec.Body).Decode(&loginBody); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if loginBody.Token == "" || loginBody.User.Email != email {
		t.Fatalf("unexpected login body: %+v", loginBody)
	}

	verifyReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	verifyReq.Header.Set("Authorization", "Bearer "+loginBody.Token)
	verifyRec := httptest.NewRecorder()
	mux.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify expected 200, got %d body=%s", verifyRec.Code, verifyRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodDelete, "/api/auth/session", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+loginBody.Token)
	logoutRec := httptest.NewRecorder()
	mux.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout expected 204, got %d body=%s", logoutRec.Code, logoutRec.Body.String())
	}
}
```

Add missing imports when writing the test:

```go
import (
	"os"
	"strings"
)
```

- [ ] **Step 2: Run failing tests**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
go test ./internal/auth -run TestAPISessionLifecycle -count=1
```

Expected: FAIL because the API route/types do not exist. If `AUTH_DATABASE_URL` is unset, the test skips; run it against a local auth DB before deployment.

- [ ] **Step 3: Add API response/request types**

Add to `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/handler.go`:

```go
type apiLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type apiUserResponse struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

type apiSessionResponse struct {
	Token     string          `json:"token,omitempty"`
	ExpiresAt time.Time       `json:"expires_at"`
	User      apiUserResponse `json:"user"`
}
```

- [ ] **Step 4: Register JSON routes**

In `RegisterRoutes`, add:

```go
mux.HandleFunc("POST /api/auth/session", h.APILogin)
mux.HandleFunc("GET /api/auth/session", h.APISession)
mux.HandleFunc("GET /api/auth/me", h.APIMe)
mux.HandleFunc("DELETE /api/auth/session", h.APILogout)
```

- [ ] **Step 5: Implement JSON handlers**

Add to `/Users/hyungjuyu/Projects/Brain/ProcessManager/internal/auth/handler.go`:

```go
func (h *Handler) APILogin(w http.ResponseWriter, r *http.Request) {
	var req apiLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil || user.PasswordHash == "" {
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, hash, err := NewSessionToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	sess, err := h.store.CreateSession(r.Context(), user.ID, hash, h.sessionTTL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeAPIJSON(w, http.StatusOK, apiSessionResponse{
		Token:     token,
		ExpiresAt: sess.ExpiresAt,
		User:      apiUserResponse{ID: user.ID, Email: user.Email, IsAdmin: user.IsAdmin},
	})
}

func (h *Handler) APISession(w http.ResponseWriter, r *http.Request) {
	token := BearerToken(r)
	sess, err := h.store.GetSessionByTokenHash(r.Context(), HashSessionToken(token))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if sess == nil {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeAPIJSON(w, http.StatusOK, apiSessionResponse{
		ExpiresAt: sess.ExpiresAt,
		User:      apiUserResponse{ID: sess.UserID, Email: sess.Email, IsAdmin: sess.IsAdmin},
	})
}

func (h *Handler) APIMe(w http.ResponseWriter, r *http.Request) {
	h.APISession(w, r)
}

func (h *Handler) APILogout(w http.ResponseWriter, r *http.Request) {
	token := BearerToken(r)
	if token == "" {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.store.DeleteSession(r.Context(), HashSessionToken(token)); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAPIJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeAPIJSON(w, status, map[string]string{"error": message})
}
```

Update imports in `handler.go` to include:

```go
"encoding/json"
```

- [ ] **Step 6: Run passing tests**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
go test ./internal/auth -run TestAPISessionLifecycle -count=1
```

Expected: PASS or SKIP only if `AUTH_DATABASE_URL` is not set.

- [ ] **Step 7: Commit**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
git add internal/auth/handler.go internal/auth/handler_test.go
git commit -m "feat(auth): add JSON session API"
```

## Task 3: Project PN Config Points To Central Auth

**Files:**
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/config/config.go`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/cmd/api/main.go`
- Test: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Create `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/config/config_test.go`:

```go
package config

import "testing"

func TestCentralAuthConfigDefaults(t *testing.T) {
	t.Setenv("AUTH_MODE", "")
	t.Setenv("CENTRAL_AUTH_URL", "")

	cfg := Load()

	if cfg.AuthMode != "local" {
		t.Fatalf("expected default auth mode local, got %q", cfg.AuthMode)
	}
	if cfg.CentralAuthURL != "" {
		t.Fatalf("expected empty central auth url by default, got %q", cfg.CentralAuthURL)
	}
}

func TestCentralAuthConfigFromEnv(t *testing.T) {
	t.Setenv("AUTH_MODE", "central")
	t.Setenv("CENTRAL_AUTH_URL", "https://auth.nacfson.cloud")

	cfg := Load()

	if cfg.AuthMode != "central" {
		t.Fatalf("expected auth mode central, got %q", cfg.AuthMode)
	}
	if cfg.CentralAuthURL != "https://auth.nacfson.cloud" {
		t.Fatalf("expected central auth url, got %q", cfg.CentralAuthURL)
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/config -run TestCentralAuthConfig -count=1
```

Expected: FAIL because fields do not exist.

- [ ] **Step 3: Add config fields**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/config/config.go`, add constants:

```go
defaultAuthMode       = "local"
defaultCentralAuthURL = ""
```

Add fields to `Config`:

```go
AuthMode       string
CentralAuthURL string
```

Add assignments in `Load()`:

```go
AuthMode:       envOrDefault("AUTH_MODE", defaultAuthMode),
CentralAuthURL: envOrDefault("CENTRAL_AUTH_URL", defaultCentralAuthURL),
```

- [ ] **Step 4: Pass config into router**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/cmd/api/main.go`, update dependencies:

```go
Handler: httpapi.NewRouter(httpapi.Dependencies{
	DB:             pool,
	Words:          wordsService,
	Auth:           authService,
	AuthMode:       cfg.AuthMode,
	CentralAuthURL: cfg.CentralAuthURL,
	AllowedOrigins: cfg.AllowedOrigins,
}),
```

- [ ] **Step 5: Run passing tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/config -run TestCentralAuthConfig -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add backend/internal/config/config.go backend/internal/config/config_test.go backend/cmd/api/main.go
git commit -m "feat(auth): configure central auth provider"
```

## Task 4: Project PN Auth Service Upserts Central Profiles

**Files:**
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/service.go`
- Test: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/service_test.go`

- [ ] **Step 1: Write failing tests**

Append to `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/service_test.go`:

```go
func TestEnsureCentralUserCreatesProfile(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	user, err := svc.EnsureCentralUser(ctx, "11111111-1111-1111-1111-111111111111", "Central@Example.COM")
	if err != nil {
		t.Fatalf("ensure central user: %v", err)
	}

	if user.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("expected central user id, got %q", user.ID)
	}
	if user.Email != "central@example.com" {
		t.Fatalf("expected normalized email, got %q", user.Email)
	}
	if user.EmailVerifiedAt == nil {
		t.Fatal("expected central user to be verified")
	}
	if user.NativeLanguage != "ko" || user.TargetLanguage != "en" {
		t.Fatalf("unexpected language defaults: %+v", user)
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/auth -run TestEnsureCentralUserCreatesProfile -count=1
```

Expected: FAIL because `EnsureCentralUser` is undefined.

- [ ] **Step 3: Implement `EnsureCentralUser`**

Add to `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/service.go` after `Authenticate`:

```go
func (s *Service) EnsureCentralUser(ctx context.Context, centralUserID, emailAddr string) (User, error) {
	emailAddr = NormalizeEmail(emailAddr)
	if centralUserID == "" || emailAddr == "" {
		return User{}, ErrInvalidToken
	}

	targetLang, err := s.resolveTargetLang("")
	if err != nil {
		return User{}, err
	}
	nativeLang, err := s.resolveDefinitionLang("")
	if err != nil {
		return User{}, err
	}
	uiLang, err := s.resolveUILang("")
	if err != nil {
		return User{}, err
	}

	var user User
	var verifiedAt *time.Time
	err = s.pool.QueryRow(ctx, `
		insert into users (id, email, password_hash, email_verified_at, native_language, target_language, ui_language)
		values ($1::uuid, $2, null, now(), $3, $4, $5)
		on conflict (id) do update set
			email = excluded.email,
			email_verified_at = coalesce(users.email_verified_at, excluded.email_verified_at),
			updated_at = now()
		returning id::text, email, email_verified_at, native_language, target_language, ui_language
	`, centralUserID, emailAddr, nativeLang, targetLang, uiLang).Scan(
		&user.ID,
		&user.Email,
		&verifiedAt,
		&user.NativeLanguage,
		&user.TargetLanguage,
		&user.UILanguage,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("auth: upsert central user: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		insert into user_languages (user_id, target_language, display_language, is_active)
		values ($1::uuid, $2, $3, true)
		on conflict (user_id, target_language) do nothing
	`, user.ID, user.TargetLanguage, user.NativeLanguage)
	if err != nil {
		return User{}, fmt.Errorf("auth: upsert central user language: %w", err)
	}

	user.EmailVerifiedAt = verifiedAt
	user.ActiveLanguage, _ = s.activeUserLanguage(ctx, user.ID)
	return user, nil
}
```

- [ ] **Step 4: Run passing tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/auth -run TestEnsureCentralUserCreatesProfile -count=1
```

Expected: PASS or SKIP only if `DATABASE_URL` is not set.

- [ ] **Step 5: Commit**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add backend/internal/auth/service.go backend/internal/auth/service_test.go
git commit -m "feat(auth): map central users to app profiles"
```

## Task 5: Project PN Central Auth Client Verifies Tokens

**Files:**
- Create: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/central_client.go`
- Test: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/central_client_test.go`

- [ ] **Step 1: Write failing client tests**

Create `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/central_client_test.go`:

```go
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCentralClientVerify(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/session" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token123" {
			t.Fatalf("unexpected auth header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"expires_at":"2030-01-01T00:00:00Z","user":{"id":"11111111-1111-1111-1111-111111111111","email":"central@example.com","is_admin":false}}`))
	}))
	defer server.Close()

	client := NewCentralClient(server.URL, server.Client())
	session, err := client.Verify(context.Background(), "token123")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if session.User.ID != "11111111-1111-1111-1111-111111111111" || session.User.Email != "central@example.com" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if session.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expected future expiry, got %s", session.ExpiresAt)
	}
}

func TestCentralClientVerifyUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL, server.Client())
	if _, err := client.Verify(context.Background(), "bad-token"); err == nil {
		t.Fatal("expected error for unauthorized token")
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/auth -run TestCentralClient -count=1
```

Expected: FAIL because `NewCentralClient` does not exist.

- [ ] **Step 3: Implement central client**

Create `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/auth/central_client.go`:

```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type CentralUser struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

type CentralSession struct {
	ExpiresAt time.Time   `json:"expires_at"`
	User      CentralUser `json:"user"`
}

type CentralClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewCentralClient(baseURL string, httpClient *http.Client) *CentralClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &CentralClient{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

func (c *CentralClient) Verify(ctx context.Context, token string) (CentralSession, error) {
	if strings.TrimSpace(token) == "" {
		return CentralSession{}, ErrInvalidToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/auth/session", nil)
	if err != nil {
		return CentralSession{}, fmt.Errorf("central auth: build verify request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CentralSession{}, fmt.Errorf("central auth: verify request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return CentralSession{}, ErrInvalidToken
	}
	if resp.StatusCode != http.StatusOK {
		return CentralSession{}, fmt.Errorf("central auth: verify status %d", resp.StatusCode)
	}

	var session CentralSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return CentralSession{}, fmt.Errorf("central auth: decode verify response: %w", err)
	}
	if session.User.ID == "" || session.User.Email == "" {
		return CentralSession{}, ErrInvalidToken
	}
	return session, nil
}
```

- [ ] **Step 4: Run passing tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/auth -run TestCentralClient -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add backend/internal/auth/central_client.go backend/internal/auth/central_client_test.go
git commit -m "feat(auth): verify central auth sessions"
```

## Task 6: Project PN Backend Uses Central Token Middleware

**Files:**
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/auth_middleware.go`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/router.go`
- Test: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/auth_handler_test.go`

- [ ] **Step 1: Add dependencies**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/router.go`, add fields:

```go
AuthMode       string
CentralAuthURL string
CentralAuth    *auth.CentralClient
```

At the start of `NewRouter`, add:

```go
authMode := deps.AuthMode
if authMode == "" {
	authMode = "local"
}
centralAuth := deps.CentralAuth
if centralAuth == nil && deps.CentralAuthURL != "" {
	centralAuth = auth.NewCentralClient(deps.CentralAuthURL, nil)
}
```

- [ ] **Step 2: Add central middleware**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/auth_middleware.go`, add:

```go
func centralAuthMiddleware(appAuth *auth.Service, central *auth.CentralClient) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if central == nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			token := bearerToken(r)
			session, err := central.Verify(r.Context(), token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			user, err := appAuth.EnsureCentralUser(r.Context(), session.User.ID, session.User.Email)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
		})
	}
}

func authMiddlewareForMode(appAuth *auth.Service, central *auth.CentralClient, mode string) func(http.Handler) http.Handler {
	if mode == "central" {
		return centralAuthMiddleware(appAuth, central)
	}
	return authMiddleware(appAuth)
}
```

- [ ] **Step 3: Use selected middleware in router**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/router.go`, replace protected `authMiddleware(deps.Auth)` calls with:

```go
authMiddlewareForMode(deps.Auth, centralAuth, authMode)
```

Keep `requireVerified()` after the selected middleware for learning routes.

- [ ] **Step 4: Disable local password routes in central mode**

In the `/api/auth` route block, register local password endpoints only when `authMode != "central"`:

```go
authRouter.Get("/language-options", ah.languageOptions)
if authMode != "central" {
	authRouter.With(authIPRateLimit(), authEmailRateLimit()).Post("/register", ah.register)
	authRouter.With(authIPRateLimit(), authEmailRateLimit()).Post("/login", ah.login)
	authRouter.With(authIPRateLimit(), authEmailRateLimit()).Post("/verify-email/request", ah.requestVerificationEmail)
	authRouter.With(consumeIPRateLimit()).Get("/verify-email", ah.verifyEmail)
}
authRouter.Group(func(protected chi.Router) {
	protected.Use(authMiddlewareForMode(deps.Auth, centralAuth, authMode))
	protected.Get("/me", ah.me)
	protected.Post("/logout", ah.logout)
})
```

- [ ] **Step 5: Write HTTP tests with fake central auth server**

Append to `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/internal/http/auth_handler_test.go`:

```go
func TestCentralAuthModeMe(t *testing.T) {
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer central-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"expires_at":"2030-01-01T00:00:00Z","user":{"id":"55555555-5555-5555-5555-555555555555","email":"central@example.com","is_admin":false}}`))
	}))
	defer central.Close()

	router, _ := testAuthRouter(t)
	_ = router

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is required for central auth HTTP tests")
	}
	ctx := context.Background()
	migrationsPath := "file://" + repoPath(t, "db", "migrations")
	if err := migrations.Up(migrationsPath, databaseURL); err != nil {
		t.Fatalf("up migration: %v", err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)

	cfg := config.Load()
	authSvc := auth.New(pool, email.NewLog(), auth.Options{
		SessionTTL:             time.Hour,
		EmailVerificationTTL:   24 * time.Hour,
		DefaultDefinitionLang:  cfg.DefaultDefinitionLang,
		DefaultTargetLang:      cfg.DefaultTargetLang,
		DefaultUILang:          cfg.UILang,
		AllowedDefinitionLangs: cfg.AllowedDefinitionLangs,
		AllowedTargetLangs:     cfg.AllowedTargetLangs,
		AllowedUILangs:         cfg.AllowedUILangs,
		AppPublicURL:           "http://localhost:8080",
	})
	wordsSvc := words.New(pool, enrich.NewOpenAI("", "", ""), cfg.DefaultUserID, cfg.DefaultTargetLang, cfg.DefaultDefinitionLang)
	router = NewRouter(Dependencies{
		DB:             pool,
		Words:          wordsSvc,
		Auth:           authSvc,
		AuthMode:       "central",
		CentralAuthURL: central.URL,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer central-token")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 6: Run tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./internal/http -run TestCentralAuthModeMe -count=1
```

Expected: PASS or SKIP only if `DATABASE_URL` is not set.

- [ ] **Step 7: Commit**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add backend/internal/http/auth_middleware.go backend/internal/http/router.go backend/internal/http/auth_handler_test.go
git commit -m "feat(auth): use central tokens for API auth"
```

## Task 7: Project PN Frontend Logs In Through Central Auth

**Files:**
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/config.ts`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/api/auth.ts`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/api/client.ts`
- Test: `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/api/auth.test.ts`

- [ ] **Step 1: Add frontend config**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/config.ts`, add:

```ts
export type AuthMode = 'local' | 'central';

export function resolveAuthMode(): AuthMode {
  return process.env.EXPO_PUBLIC_AUTH_MODE === 'central' ? 'central' : 'local';
}

export function resolveCentralAuthBaseUrl(): string {
  return process.env.EXPO_PUBLIC_CENTRAL_AUTH_URL || 'https://auth.nacfson.cloud';
}

export const AUTH_MODE = resolveAuthMode();
export const CENTRAL_AUTH_BASE_URL = resolveCentralAuthBaseUrl();
```

- [ ] **Step 2: Write auth API test**

Create `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/api/auth.test.ts`:

```ts
jest.mock('./storage', () => ({
  sessionStorage: {
    getToken: jest.fn(),
    setToken: jest.fn(),
    removeToken: jest.fn(),
  },
}));

describe('central auth API', () => {
  const originalMode = process.env.EXPO_PUBLIC_AUTH_MODE;
  const originalURL = process.env.EXPO_PUBLIC_CENTRAL_AUTH_URL;

  beforeEach(() => {
    jest.resetModules();
    process.env.EXPO_PUBLIC_AUTH_MODE = 'central';
    process.env.EXPO_PUBLIC_CENTRAL_AUTH_URL = 'https://auth.example.test';
    global.fetch = jest.fn(async () => ({
      ok: true,
      text: async () => JSON.stringify({
        token: 'central-token',
        expires_at: '2030-01-01T00:00:00Z',
        user: { id: 'u1', email: 'user@example.com', is_admin: false },
      }),
    })) as jest.Mock;
  });

  afterEach(() => {
    process.env.EXPO_PUBLIC_AUTH_MODE = originalMode;
    process.env.EXPO_PUBLIC_CENTRAL_AUTH_URL = originalURL;
    jest.restoreAllMocks();
  });

  it('stores token returned by central auth login', async () => {
    const { login } = await import('./auth');
    const { sessionStorage } = await import('./storage');

    await login('user@example.com', 'password123');

    expect(global.fetch).toHaveBeenCalledWith(
      'https://auth.example.test/api/auth/session',
      expect.objectContaining({ method: 'POST' }),
    );
    expect(sessionStorage.setToken).toHaveBeenCalledWith('central-token');
  });
});
```

- [ ] **Step 3: Update login/logout to call central auth in central mode**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/src/api/auth.ts`, import central config:

```ts
import { AUTH_MODE, CENTRAL_AUTH_BASE_URL, DEFAULT_DEFINITION_LANGUAGE_CODE, DEFAULT_LANGUAGE_CODE } from '../config';
```

Replace `login` with:

```ts
export async function login(email: string, password: string): Promise<SessionResponse> {
  const body: LoginRequest = { email, password };
  if (AUTH_MODE === 'central') {
    const response = await fetch(`${CENTRAL_AUTH_BASE_URL}/api/auth/session`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const parsed = (await response.json()) as SessionResponse;
    if (!response.ok) {
      throw new Error((parsed as unknown as { error?: string }).error ?? 'Sign-in failed');
    }
    await sessionStorage.setToken(parsed.token);
    return parsed;
  }
  const session = await postJson<SessionResponse>('/api/auth/login', body, noAuth);
  await sessionStorage.setToken(session.token);
  return session;
}
```

Replace `logout` with:

```ts
export async function logout(): Promise<void> {
  const token = await sessionStorage.getToken();
  try {
    if (AUTH_MODE === 'central' && token) {
      await fetch(`${CENTRAL_AUTH_BASE_URL}/api/auth/session`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      });
      return;
    }
    await postNoContent('/api/auth/logout');
  } finally {
    await sessionStorage.removeToken();
  }
}
```

- [ ] **Step 4: Run frontend test**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/frontend
npm test -- --runInBand src/api/auth.test.ts
```

Expected: PASS.

- [ ] **Step 5: Typecheck**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/frontend
npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add frontend/src/config.ts frontend/src/api/auth.ts frontend/src/api/client.ts frontend/src/api/auth.test.ts
git commit -m "feat(auth): log in through central auth"
```

## Task 8: Deployment Configuration Uses Central Auth Everywhere

**Files:**
- Modify: `/Users/hyungjuyu/Projects/Brain/ProcessManager/k8s/project-pn/backend.yaml`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/scripts/deploy-remote.sh`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/.env.example`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/.env.example`

- [ ] **Step 1: Set backend env in Kubernetes**

In `/Users/hyungjuyu/Projects/Brain/ProcessManager/k8s/project-pn/backend.yaml`, add:

```yaml
            - name: AUTH_MODE
              value: "central"
            - name: CENTRAL_AUTH_URL
              value: "http://auth-service.nacfson-auth.svc.cluster.local:8080"
```

- [ ] **Step 2: Build web with central auth env**

In `/Users/hyungjuyu/Projects/iOS/Project_PN/scripts/deploy-remote.sh`, replace:

```bash
EXPO_PUBLIC_API_BASE_URL= npm run web:export
```

with:

```bash
EXPO_PUBLIC_API_BASE_URL= EXPO_PUBLIC_AUTH_MODE=central EXPO_PUBLIC_CENTRAL_AUTH_URL=https://auth.nacfson.cloud npm run web:export
```

- [ ] **Step 3: Document local env examples**

Append to `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/.env.example`:

```text
# Auth mode: local uses Project PN bearer auth; central validates ProcessManager auth tokens.
AUTH_MODE=local
CENTRAL_AUTH_URL=
```

Append to `/Users/hyungjuyu/Projects/iOS/Project_PN/frontend/.env.example`:

```text
# Auth mode: local uses Project PN /api/auth/login; central uses auth.nacfson.cloud.
EXPO_PUBLIC_AUTH_MODE=local
EXPO_PUBLIC_CENTRAL_AUTH_URL=https://auth.nacfson.cloud
```

- [ ] **Step 4: Validate manifests**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
python3 - <<'PY'
from pathlib import Path
import yaml
for path in ["k8s/project-pn/backend.yaml"]:
    with Path(path).open() as f:
        yaml.safe_load(f)
    print(f"ok {path}")
PY
```

Expected:

```text
ok k8s/project-pn/backend.yaml
```

- [ ] **Step 5: Commit**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
git add k8s/project-pn/backend.yaml
git commit -m "feat(project-pn): use central auth provider"

cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add scripts/deploy-remote.sh frontend/.env.example backend/.env.example
git commit -m "chore(auth): configure central auth deployment"
```

## Task 9: Documentation Update

**Files:**
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/docs/backend-overview.md`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/docs/backend-flows.md`
- Modify: `/Users/hyungjuyu/Projects/iOS/Project_PN/deploy/NETWORK_SETUP.md`
- Modify: `/Users/hyungjuyu/Projects/Brain/ProcessManager/README.md`

- [ ] **Step 1: Document central-token architecture in Project PN overview**

Add to `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/docs/backend-overview.md`:

```markdown
## Authentication

Project PN supports `local` and `central` auth modes. `local` is for development and uses Project PN's legacy bearer sessions. `central` is the target architecture: clients authenticate with ProcessManager at `auth.nacfson.cloud`, store the returned central bearer token, and send it to Project PN APIs. Project PN validates the token with ProcessManager and maps the central user ID to a local app profile row.
```

- [ ] **Step 2: Document central flow**

Add to `/Users/hyungjuyu/Projects/iOS/Project_PN/backend/docs/backend-flows.md`:

```markdown
## Central Auth Flow

1. Client posts email/password to `https://auth.nacfson.cloud/api/auth/session`.
2. ProcessManager validates credentials against the central auth DB and returns an opaque bearer token.
3. Client stores the token in localStorage on web or SecureStore on native.
4. Client calls Project PN APIs with `Authorization: Bearer <central-token>`.
5. Project PN calls ProcessManager `GET /api/auth/session` to validate the token.
6. Project PN upserts a local app profile keyed by the central user ID.
7. Project PN domain handlers use the local profile ID for decks, words, review state, and settings.
```

- [ ] **Step 3: Document ProcessManager provider role**

Add to `/Users/hyungjuyu/Projects/Brain/ProcessManager/README.md`:

```markdown
### Central Auth API

ProcessManager auth provides both ingress external-auth and JSON bearer-session APIs. Project PN uses the JSON APIs for platform-neutral auth across web, desktop, iOS, and Android.
```

- [ ] **Step 4: Commit docs**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN
git add backend/docs/backend-overview.md backend/docs/backend-flows.md deploy/NETWORK_SETUP.md
git commit -m "docs(auth): explain central token auth"

cd /Users/hyungjuyu/Projects/Brain/ProcessManager
git add README.md
git commit -m "docs(auth): describe central auth API"
```

## Task 10: Full Verification

**Files:**
- Verify only.

- [ ] **Step 1: ProcessManager tests**

```bash
cd /Users/hyungjuyu/Projects/Brain/ProcessManager
go test ./...
```

Expected: PASS. Auth integration tests may skip if `AUTH_DATABASE_URL` is not configured.

- [ ] **Step 2: Project PN backend tests**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/backend
go test ./...
```

Expected: PASS. DB integration tests may skip if `DATABASE_URL` is not configured.

- [ ] **Step 3: Project PN frontend tests and typecheck**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/frontend
npm test -- --runInBand
npx tsc --noEmit
```

Expected: PASS.

- [ ] **Step 4: Build all-platform-compatible web bundle**

```bash
cd /Users/hyungjuyu/Projects/iOS/Project_PN/frontend
EXPO_PUBLIC_API_BASE_URL= EXPO_PUBLIC_AUTH_MODE=central EXPO_PUBLIC_CENTRAL_AUTH_URL=https://auth.nacfson.cloud npm run web:export
```

Expected: PASS.

## Task 11: Deployment Smoke Test

**Files:**
- Verify deployed behavior.

- [ ] **Step 1: Login through central auth API**

```bash
curl -sS -X POST https://auth.nacfson.cloud/api/auth/session \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@nacfson.cloud","password":"<password>"}'
```

Expected: JSON response with `token`, `expires_at`, and `user`.

- [ ] **Step 2: Verify central token against Project PN**

```bash
curl -sS https://pn.nacfson.cloud/api/auth/me \
  -H "Authorization: Bearer <token-from-step-1>"
```

Expected: HTTP 200 JSON with Project PN app profile fields and the central user email.

- [ ] **Step 3: Verify the web app uses the same path**

Open `https://pn.nacfson.cloud`, sign in with central auth credentials on the Project PN login screen, and add one word.

Expected: The app loads, the word is saved, and refresh keeps the user authenticated because the central token is stored by the Project PN client.

## Self-Review

Spec coverage:
- No web-only limitation: central bearer token works for web, Tauri, iOS, and Android.
- ProcessManager provides auth: Tasks 1, 2, 8, 9, and 11.
- Project PN consumes central auth: Tasks 3, 4, 5, 6, 7, 8, and 11.
- Existing local mode preserved for development: Tasks 3, 6, 7, and 8.

Placeholder scan:
- The plan avoids placeholder instructions and gives exact paths, commands, and code snippets for each code-changing task.

Type consistency:
- Auth modes are `local` and `central`.
- Central auth endpoint is `/api/auth/session`.
- Project PN always sends `Authorization: Bearer <central-token>` to its own backend in central mode.
