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

func TestAPIAuthSessionLifecycle(t *testing.T) {
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

	password := "correct horse battery staple"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	email := strings.ToLower("api-test-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-") + "@example.test")
	user, err := store.CreateUser(ctx, email, string(hash), false, true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	handler := &Handler{
		store:        store,
		cookieDomain: "nacfson.cloud",
		sessionTTL:   time.Hour,
	}

	loginBody := bytes.NewBufferString(`{"email":"` + email + `","password":"` + password + `"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/session", loginBody)
	loginRec := httptest.NewRecorder()
	handler.APILogin(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("APILogin status = %d body = %s", loginRec.Code, loginRec.Body.String())
	}

	var login apiSessionResponse
	if err := json.NewDecoder(loginRec.Body).Decode(&login); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if login.Token == "" {
		t.Fatal("login response token is empty")
	}
	if login.User.ID != user.ID || login.User.Email != email {
		t.Fatalf("login user = %#v, want id %q email %q", login.User, user.ID, email)
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessionReq.Header.Set("Authorization", "Bearer "+login.Token)
	sessionRec := httptest.NewRecorder()
	handler.APISession(sessionRec, sessionReq)
	if sessionRec.Code != http.StatusOK {
		t.Fatalf("APISession status = %d body = %s", sessionRec.Code, sessionRec.Body.String())
	}

	var session apiSessionResponse
	if err := json.NewDecoder(sessionRec.Body).Decode(&session); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if session.Token != "" {
		t.Fatalf("session response should not echo token, got %q", session.Token)
	}
	if session.User.ID != user.ID || session.User.Email != email {
		t.Fatalf("session user = %#v, want id %q email %q", session.User, user.ID, email)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+login.Token)
	meRec := httptest.NewRecorder()
	handler.APIMe(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("APIMe status = %d body = %s", meRec.Code, meRec.Body.String())
	}

	var me apiUserResponse
	if err := json.NewDecoder(meRec.Body).Decode(&me); err != nil {
		t.Fatalf("decode me response: %v", err)
	}
	if me.ID != user.ID || me.Email != email {
		t.Fatalf("me = %#v, want id %q email %q", me, user.ID, email)
	}

	logoutReq := httptest.NewRequest(http.MethodDelete, "/api/auth/session", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+login.Token)
	logoutRec := httptest.NewRecorder()
	handler.APILogout(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("APILogout status = %d body = %s", logoutRec.Code, logoutRec.Body.String())
	}

	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	afterLogoutReq.Header.Set("Authorization", "Bearer "+login.Token)
	afterLogoutRec := httptest.NewRecorder()
	handler.APISession(afterLogoutRec, afterLogoutReq)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("APISession after logout status = %d body = %s", afterLogoutRec.Code, afterLogoutRec.Body.String())
	}
}

func TestAPILoginRejectsBadJSON(t *testing.T) {
	handler := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session", strings.NewReader("{"))
	rec := httptest.NewRecorder()

	handler.APILogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("APILogin status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAPIOptionsAllowsConfiguredOrigin(t *testing.T) {
	handler := &Handler{apiAllowedOrigins: []string{"https://pn.nacfson.cloud"}}
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/session", nil)
	req.Header.Set("Origin", "https://pn.nacfson.cloud")
	rec := httptest.NewRecorder()

	handler.APIOptions(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("APIOptions status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://pn.nacfson.cloud" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want Authorization", got)
	}
}

func TestAPIOptionsRejectsUnconfiguredOrigin(t *testing.T) {
	handler := &Handler{apiAllowedOrigins: []string{"https://pn.nacfson.cloud"}}
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/session", nil)
	req.Header.Set("Origin", "https://other.example")
	rec := httptest.NewRecorder()

	handler.APIOptions(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("APIOptions status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestClientIPPrefersXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.10")

	got := clientIP(req, 1)
	if got != "198.51.100.10" {
		t.Fatalf("clientIP = %q, want %q", got, "198.51.100.10")
	}
}

func TestClientIPFallsBackToXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "203.0.113.7")

	got := clientIP(req, 1)
	if got != "203.0.113.7" {
		t.Fatalf("clientIP = %q, want %q", got, "203.0.113.7")
	}
}

func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	got := clientIP(req, 1)
	if got != "10.0.0.1" {
		t.Fatalf("clientIP = %q, want %q", got, "10.0.0.1")
	}
}
