package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestRegistrationAndVerificationFlow(t *testing.T) {
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

	// Real templates from "../../web/templates/*.html"
	tmpl, err := template.New("").ParseGlob("../../web/templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}

	handler := &Handler{
		store:        store,
		cookieDomain: "nacfson.cloud",
		sessionTTL:   time.Hour,
		tmpl:         tmpl,
	}

	// Clear DB
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications CASCADE")

	// 1. GET /register form rendering
	req := httptest.NewRequest(http.MethodGet, "/register?rd=/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.RegisterForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("RegisterForm status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "/dashboard") {
		t.Errorf("RegisterForm rendered body missing return URL /dashboard")
	}

	// 2. POST /register validation checks
	// Test case: empty email
	form := url.Values{}
	form.Set("email", "")
	form.Set("password", "secret123")
	form.Set("confirm_password", "secret123")
	form.Set("return_to", "/dashboard")

	req = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Register(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("Register empty email status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=email+is+required") {
		t.Errorf("Register redirect location does not complain about email: %q", loc)
	}

	// Test case: empty password
	form.Set("email", "newuser@example.com")
	form.Set("password", "")
	form.Set("confirm_password", "secret123")
	req = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Register(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("Register empty password status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "error=password+is+required") {
		t.Errorf("Register redirect location does not complain about password: %q", loc)
	}

	// Test case: passwords do not match
	form.Set("password", "secret123")
	form.Set("confirm_password", "different")
	req = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Register(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("Register password mismatch status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "error=passwords+do+not+match") {
		t.Errorf("Register redirect location does not complain about password mismatch: %q", loc)
	}

	// 3. Successful registration
	email := "verified-user@example.com"
	form.Set("email", email)
	form.Set("password", "correctpass")
	form.Set("confirm_password", "correctpass")
	form.Set("return_to", "/dashboard")

	req = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Register(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("Register success status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "/verification-sent") {
		t.Fatalf("Register redirect location does not point to verification-sent: %q", loc)
	}

	// Check user is created with IsVerified = false
	u, err := store.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if u == nil {
		t.Fatalf("user was not created in DB")
	}
	if u.IsVerified {
		t.Errorf("newly registered user is verified, want unverified")
	}

	// Extract token from redirect URL or DB
	uUrl, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	token := uUrl.Query().Get("token")
	if token == "" {
		t.Fatalf("missing token in redirect URL")
	}

	// Check token is in DB
	ev, err := store.GetEmailVerification(ctx, token)
	if err != nil {
		t.Fatalf("GetEmailVerification: %v", err)
	}
	if ev == nil {
		t.Fatalf("verification token was not found in DB")
	}
	if ev.UserID != u.ID {
		t.Errorf("verification token belongs to user %q, want %q", ev.UserID, u.ID)
	}

	// 4. Render /verification-sent
	req = httptest.NewRequest(http.MethodGet, "/verification-sent?token="+token+"&rd=/dashboard", nil)
	rec = httptest.NewRecorder()
	handler.VerificationSent(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("VerificationSent status = %d, want %d", rec.Code, http.StatusOK)
	}
	// Since SMTP_HOST is not configured, it should contain the VerificationLink
	if !strings.Contains(rec.Body.String(), "/verify-email?token="+token) {
		t.Errorf("VerificationSent output missing verification link: %s", rec.Body.String())
	}

	// 5. Test unverified user login failure
	// Web Login
	loginForm := url.Values{}
	loginForm.Set("email", email)
	loginForm.Set("password", "correctpass")
	loginForm.Set("return_to", "/dashboard")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("Login for unverified user status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "error=email+not+verified") {
		t.Errorf("Login redirect location does not complain about verification: %q", loc)
	}

	// API Login
	loginBody := bytes.NewBufferString(`{"email":"` + email + `","password":"correctpass"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/session", loginBody)
	rec = httptest.NewRecorder()
	handler.APILogin(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("APILogin for unverified user status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unverified email") {
		t.Errorf("APILogin response body does not mention unverified email: %s", rec.Body.String())
	}

	// 6. Test Registration duplicate email error
	req = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Register(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("Register duplicate email status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "error=email+already+registered") {
		t.Errorf("Register redirect location does not complain about duplicate email: %q", loc)
	}

	// 7. VerifyEmail handler
	// Test case: missing token
	req = httptest.NewRequest(http.MethodGet, "/verify-email", nil)
	rec = httptest.NewRecorder()
	handler.VerifyEmail(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("VerifyEmail missing token status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "error=invalid+or+expired+verification+token") {
		t.Errorf("VerifyEmail missing token location wrong: %q", loc)
	}

	// Test case: invalid token
	req = httptest.NewRequest(http.MethodGet, "/verify-email?token=invalidtoken", nil)
	rec = httptest.NewRecorder()
	handler.VerifyEmail(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("VerifyEmail invalid token status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "error=invalid+or+expired+verification+token") {
		t.Errorf("VerifyEmail invalid token location wrong: %q", loc)
	}

	// Test case: valid token
	req = httptest.NewRequest(http.MethodGet, "/verify-email?token="+token+"&rd=/dashboard", nil)
	rec = httptest.NewRecorder()
	handler.VerifyEmail(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("VerifyEmail success status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	expectedRedirect := "https://" + handler.cookieDomain + "/dashboard"
	if loc != expectedRedirect {
		t.Errorf("VerifyEmail redirect to = %q, want %q", loc, expectedRedirect)
	}

	// Check user is verified in DB
	u, err = store.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if !u.IsVerified {
		t.Errorf("user is still unverified after verification")
	}

	// Check token is deleted from DB
	ev, err = store.GetEmailVerification(ctx, token)
	if err != nil {
		t.Fatalf("GetEmailVerification: %v", err)
	}
	if ev != nil {
		t.Errorf("email verification token was not deleted from DB")
	}

	// Check session cookie is set
	cookies := rec.Result().Cookies()
	var foundSessCookie bool
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			foundSessCookie = true
			if c.Value == "" {
				t.Errorf("session cookie value is empty")
			}
		}
	}
	if !foundSessCookie {
		t.Errorf("session cookie was not set after verification")
	}

	// 8. Test that verified user can now log in successfully
	loginBody = bytes.NewBufferString(`{"email":"` + email + `","password":"correctpass"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/session", loginBody)
	rec = httptest.NewRecorder()
	handler.APILogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("APILogin after verification status = %d, want 200", rec.Code)
	}
}
