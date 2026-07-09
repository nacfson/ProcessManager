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

	parsedAllowedRedirect, _ := url.Parse("https://customproject.com/callback")
	handler := &Handler{
		store:            store,
		cookieDomain:     "nacfson.cloud",
		sessionTTL:      time.Hour,
		tmpl:             tmpl,
		allowedRedirects: []*url.URL{parsedAllowedRedirect},
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

	// 6. Test Re-registration of unverified user (prevents deadlock)
	req = httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.Register(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("Re-register unverified user status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	if !strings.Contains(loc, "/verification-sent") {
		t.Fatalf("Re-register redirect location does not point to verification-sent: %q", loc)
	}

	// Verify old token is deleted from DB
	oldToken := token
	oldEv, err := store.GetEmailVerification(ctx, oldToken)
	if err != nil {
		t.Fatalf("GetEmailVerification for old token: %v", err)
	}
	if oldEv != nil {
		t.Errorf("old verification token was not deleted from DB after re-registration")
	}

	// Extract new token from redirect URL or DB
	uUrl, err = url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	token = uUrl.Query().Get("token")
	if token == "" {
		t.Fatalf("missing token in redirect URL")
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

	// Test case: valid token with whitelisted rd
	req = httptest.NewRequest(http.MethodGet, "/verify-email?token="+token+"&rd=https://customproject.com/callback", nil)
	rec = httptest.NewRecorder()
	handler.VerifyEmail(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("VerifyEmail success status = %d, want 302", rec.Code)
	}
	loc = rec.Header().Get("Location")
	expectedRedirect := "https://customproject.com/callback"
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

	// 9. Test Registration duplicate email error (for verified user)
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
}

func TestLoginFormRedirectWhitelist(t *testing.T) {
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

	tmpl, err := template.New("").ParseGlob("../../web/templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}

	parsedAllowed, _ := url.Parse("https://customproject.com/callback")
	handler := &Handler{
		store:            store,
		cookieDomain:     "nacfson.cloud",
		sessionTTL:      time.Hour,
		tmpl:             tmpl,
		allowedRedirects: []*url.URL{parsedAllowed},
	}

	// 1. Test LoginForm with whitelisted rd
	req := httptest.NewRequest(http.MethodGet, "/login?rd=https://customproject.com/callback", nil)
	rec := httptest.NewRecorder()
	handler.LoginForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("LoginForm status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "https://customproject.com/callback") {
		t.Errorf("LoginForm output missing whitelisted return URL")
	}

	// 2. Test LoginForm with non-whitelisted external rd (should fallback to default)
	req = httptest.NewRequest(http.MethodGet, "/login?rd=https://evil.com/callback", nil)
	rec = httptest.NewRecorder()
	handler.LoginForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("LoginForm status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "https://evil.com/callback") {
		t.Errorf("LoginForm output contains rejected return URL")
	}
	if !strings.Contains(rec.Body.String(), "https://nacfson.cloud") {
		t.Errorf("LoginForm output missing default return URL")
	}
}


func TestVerifyEmailAllowedRedirects(t *testing.T) {
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
	_ = store.Migrate(ctx)

	// Clear DB
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications CASCADE")

	// Create an unverified user
	user, err := store.CreateUser(ctx, "redirect-test@example.com", "hash", false, false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Create email verification
	token := "valid-token-for-redirect"
	err = store.CreateEmailVerification(ctx, user.ID, token, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("create email verification: %v", err)
	}

	// Parse allowed redirect
	uRedirect, err := url.Parse("https://external.example.com/callback")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}

	handler := &Handler{
		store:            store,
		cookieDomain:     "nacfson.cloud",
		sessionTTL:       time.Hour,
		allowedRedirects: []*url.URL{uRedirect},
	}

	// Request with a whitelisted redirect URL that is external
	req := httptest.NewRequest(http.MethodGet, "/verify-email?token="+token+"&rd=https://external.example.com/callback", nil)
	rec := httptest.NewRecorder()
	handler.VerifyEmail(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("VerifyEmail status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://external.example.com/callback" {
		t.Errorf("expected redirect to https://external.example.com/callback, got %q", loc)
	}
}

func TestRegistrationRateLimiting(t *testing.T) {
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
	_ = store.Migrate(ctx)

	// Clear DB
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications CASCADE")

	// Set up loginLimiter with a very low limit (e.g. 1 max attempt)
	limiter := NewLoginLimiter(1, 1*time.Minute)

	handler := &Handler{
		store:        store,
		cookieDomain: "nacfson.cloud",
		sessionTTL:   time.Hour,
		loginLimiter: limiter,
	}

	email := "limiter-test@example.com"

	// First registration attempt - fails validation (password too short)
	form := url.Values{}
	form.Set("email", email)
	form.Set("password", "short")
	form.Set("confirm_password", "short")

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.Register(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected 302 redirect for validation error, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=password+must+be+at+least+8+characters") {
		t.Errorf("expected password validation error, got %q", loc)
	}

	// Second registration attempt - should be rate limited immediately
	form.Set("password", "longenoughpassword")
	form.Set("confirm_password", "longenoughpassword")
	req2 := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	handler.Register(rec2, req2)

	if rec2.Code != http.StatusFound {
		t.Errorf("expected 302 redirect for rate limit, got %d", rec2.Code)
	}
	loc2 := rec2.Header().Get("Location")
	if !strings.Contains(loc2, "error=too+many+attempts") {
		t.Errorf("expected rate limited error, got %q", loc2)
	}
}

func TestRegisterUnverifiedEmailRecreatesVerification(t *testing.T) {
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
	_ = store.Migrate(ctx)

	// Clear DB
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications CASCADE")

	handler := &Handler{
		store:        store,
		cookieDomain: "nacfson.cloud",
		sessionTTL:   time.Hour,
	}

	email := "unverified-recreate@example.com"

	// 1. Initial register
	form := url.Values{}
	form.Set("email", email)
	form.Set("password", "firstpassword")
	form.Set("confirm_password", "firstpassword")

	req1 := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	handler.Register(rec1, req1)

	if rec1.Code != http.StatusFound {
		t.Fatalf("initial Register status = %d, want 302", rec1.Code)
	}
	loc1 := rec1.Header().Get("Location")
	uUrl1, _ := url.Parse(loc1)
	token1 := uUrl1.Query().Get("token")
	if token1 == "" {
		t.Fatalf("expected verification token")
	}

	// Verify token is in DB
	ev1, err := store.GetEmailVerification(ctx, token1)
	if err != nil || ev1 == nil {
		t.Fatalf("expected verification to exist in DB")
	}

	// 2. Register again with same email but new password
	form2 := url.Values{}
	form2.Set("email", email)
	form2.Set("password", "secondpassword")
	form2.Set("confirm_password", "secondpassword")

	req2 := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	handler.Register(rec2, req2)

	if rec2.Code != http.StatusFound {
		t.Fatalf("second Register status = %d, want 302", rec2.Code)
	}
	loc2 := rec2.Header().Get("Location")
	uUrl2, _ := url.Parse(loc2)
	token2 := uUrl2.Query().Get("token")
	if token2 == "" {
		t.Fatalf("expected new verification token")
	}

	if token1 == token2 {
		t.Errorf("expected different verification token, got same: %s", token1)
	}

	// Verify old token is deleted
	ev1Check, err := store.GetEmailVerification(ctx, token1)
	if err != nil {
		t.Fatalf("GetEmailVerification error: %v", err)
	}
	if ev1Check != nil {
		t.Errorf("expected old verification token to be deleted")
	}

	// Verify new token is in DB
	ev2, err := store.GetEmailVerification(ctx, token2)
	if err != nil || ev2 == nil {
		t.Fatalf("expected new verification token to exist in DB")
	}

	// Verify password hash is updated and works with second password
	user, err := store.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("secondpassword"))
	if err != nil {
		t.Errorf("password was not updated to secondpassword: %v", err)
	}
}

func TestVerifyInternalToken_UsesConstantTimeCompare(t *testing.T) {
	t.Setenv("AUTH_INTERNAL_TOKEN", "test-secret-token")

	store := &Store{}
	h := &Handler{store: store}

	mux := http.NewServeMux()
	mux.Handle("GET /api/internal/test", h.verifyInternalToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// Valid token
	req := httptest.NewRequest("GET", "/api/internal/test", nil)
	req.Header.Set("Authorization", "Bearer test-secret-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("valid token: expected 200, got %d", rec.Code)
	}

	// Invalid token
	req = httptest.NewRequest("GET", "/api/internal/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("invalid token: expected 401, got %d", rec.Code)
	}

	// Missing token
	req = httptest.NewRequest("GET", "/api/internal/test", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: expected 401, got %d", rec.Code)
	}
}
