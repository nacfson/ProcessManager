package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func TestInternalAPI(t *testing.T) {
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect auth db: %v", err)
	}
	defer pool.Close()

	store := NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate auth db: %v", err)
	}

	// TRUNCATE tables to be hermetic
	_, err = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications CASCADE")
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	// Create test user and session
	email := strings.ToLower("internal-api-test-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-") + "@example.test")
	password := "password123"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	user, err := store.CreateUser(ctx, email, string(hash), false, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessionToken, tokenHash, err := NewSessionToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}

	_, err = store.CreateSession(ctx, user.ID, tokenHash, 1*time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	handler := &Handler{
		store:        store,
		cookieDomain: "localhost",
		sessionTTL:   time.Hour,
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	internalToken := "test-secret-token"
	t.Setenv("AUTH_INTERNAL_TOKEN", internalToken)
	sessionInternalToken := "test-session-secret-token"
	t.Setenv("AUTH_SESSION_INTERNAL_TOKEN", sessionInternalToken)

	// Helper to send request and return response recorder
	sendReq := func(method, path string, body []byte, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// 1. GET /api/internal/session
	t.Run("GET /api/internal/session", func(t *testing.T) {
		// Unauthorized
		rec := sendReq(http.MethodGet, "/api/internal/session?token_hash="+tokenHash, nil, "bad-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Bad Request - missing token_hash
		rec = sendReq(http.MethodGet, "/api/internal/session", nil, internalToken, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}

		// Unauthorized - invalid token_hash
		rec = sendReq(http.MethodGet, "/api/internal/session?token_hash=invalid", nil, internalToken, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodGet, "/api/internal/session?token_hash="+tokenHash, nil, internalToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		if resp["user_id"] != user.ID {
			t.Errorf("expected user_id %s, got %v", user.ID, resp["user_id"])
		}
		if resp["email"] != email {
			t.Errorf("expected email %s, got %v", email, resp["email"])
		}
	})

	t.Run("GET /api/internal/cookie-session", func(t *testing.T) {
		rec := sendReq(
			http.MethodGet,
			"/api/internal/cookie-session",
			nil,
			sessionInternalToken,
			&http.Cookie{Name: SessionCookieName, Value: sessionToken},
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		if resp["user_id"] != user.ID {
			t.Errorf("expected user_id %s, got %v", user.ID, resp["user_id"])
		}

		rec = sendReq(
			http.MethodGet,
			"/api/internal/cookie-session",
			nil,
			sessionInternalToken,
			nil,
		)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("missing cookie status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	// 2. GET /api/internal/projects
	t.Run("GET /api/internal/projects", func(t *testing.T) {
		// Unauthorized
		rec := sendReq(http.MethodGet, "/api/internal/projects", nil, "bad-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodGet, "/api/internal/projects", nil, internalToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var projects []map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &projects); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
	})

	// 3. POST /api/internal/projects
	t.Run("POST /api/internal/projects", func(t *testing.T) {
		projReq := map[string]interface{}{
			"name":         "test-project",
			"namespace":    "test-ns",
			"domain_host":  "test.example.com",
			"type":         "simple",
			"db_required":  false,
			"auth_enabled": true,
		}
		body, _ := json.Marshal(projReq)

		// Unauthorized
		rec := sendReq(http.MethodPost, "/api/internal/projects", body, "bad-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodPost, "/api/internal/projects", body, internalToken, nil)
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("expected 200/201, got %d body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		if resp["name"] != "test-project" {
			t.Errorf("expected project name test-project, got %v", resp["name"])
		}
	})

	// 3b. GET /api/internal/projects/{name}
	t.Run("GET /api/internal/projects/{name}", func(t *testing.T) {
		// Missing/unknown project returns 404
		rec := sendReq(http.MethodGet, "/api/internal/projects/nonexistent", nil, internalToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 for unknown project, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodGet, "/api/internal/projects/test-project", nil, internalToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		if resp["name"] != "test-project" {
			t.Errorf("expected project name test-project, got %v", resp["name"])
		}
		if resp["namespace"] != "test-ns" {
			t.Errorf("expected namespace test-ns, got %v", resp["namespace"])
		}
	})

	// 4. GET /api/internal/users
	t.Run("GET /api/internal/users", func(t *testing.T) {
		// Unauthorized
		rec := sendReq(http.MethodGet, "/api/internal/users", nil, "bad-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodGet, "/api/internal/users", nil, internalToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var users []map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		found := false
		for _, u := range users {
			if u["email"] == email {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find email %s in user list", email)
		}
	})

	// 5. POST /api/internal/audit-events
	t.Run("POST /api/internal/audit-events", func(t *testing.T) {
		auditReq := map[string]interface{}{
			"actor_email":   email,
			"action":        "test-action",
			"resource_type": "test-resource",
			"resource_id":   "test-id",
		}
		body, _ := json.Marshal(auditReq)

		// Unauthorized
		rec := sendReq(http.MethodPost, "/api/internal/audit-events", body, "bad-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodPost, "/api/internal/audit-events", body, internalToken, nil)
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("expected 200/201, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	// 6. GET /api/internal/audit-events
	t.Run("GET /api/internal/audit-events", func(t *testing.T) {
		// Unauthorized
		rec := sendReq(http.MethodGet, "/api/internal/audit-events", nil, "bad-token", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}

		// Success
		rec = sendReq(http.MethodGet, "/api/internal/audit-events?limit=5", nil, internalToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var events []map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		if len(events) == 0 {
			t.Error("expected at least 1 audit event, got 0")
		} else {
			found := false
			for _, e := range events {
				if e["actor_email"] == email && e["action"] == "test-action" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected to find audit event with action test-action and email %s", email)
			}
		}
	})
}
