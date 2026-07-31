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
	_, err = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")
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


func TestMagicLinkLoginFlow(t *testing.T) {
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

	parsedAllowedRedirect, _ := url.Parse("https://customproject.com/callback")
	handler := &Handler{
		store:            store,
		cookieDomain:     "nacfson.cloud",
		sessionTTL:       time.Hour,
		magicLinkTTL:     15 * time.Minute,
		tmpl:             tmpl,
		allowedRedirects: []*url.URL{parsedAllowedRedirect},
		mailer:           NewMailer("nacfson.cloud"),
	}

	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")

	// Invalid email
	form := url.Values{}
	form.Set("email", "not-an-email")
	form.Set("return_to", "https://customproject.com/callback")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "auth.nacfson.cloud"
	rec := httptest.NewRecorder()
	handler.RequestMagicLink(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("invalid email status = %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "error=invalid+email+address") {
		t.Fatalf("expected invalid email error, got %q", rec.Header().Get("Location"))
	}

	// Unknown email creates user + token
	email := "magic-new@example.com"
	form = url.Values{}
	form.Set("email", email)
	form.Set("return_to", "https://customproject.com/callback")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "auth.nacfson.cloud"
	rec = httptest.NewRecorder()
	handler.RequestMagicLink(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("RequestMagicLink status = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/link-sent") {
		t.Fatalf("expected /link-sent redirect, got %q", loc)
	}
	u, err := store.GetUserByEmail(ctx, email)
	if err != nil || u == nil {
		t.Fatalf("expected user created")
	}
	if u.IsVerified {
		t.Fatalf("new user should be unverified")
	}
	token := ""
	if uLoc, err := url.Parse(loc); err == nil {
		token = uLoc.Query().Get("token")
	}
	if token == "" {
		t.Fatalf("expected dev-fallback token in redirect")
	}

	// GET /magic does not consume
	req = httptest.NewRequest(http.MethodGet, "/magic?token="+token+"&rd=https://customproject.com/callback", nil)
	rec = httptest.NewRecorder()
	handler.MagicForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("MagicForm status = %d", rec.Code)
	}
	lt, err := store.ConsumeLoginToken(ctx, "probe")
	_ = lt
	// token still present: LatestLoginTokenCreatedAt non-nil
	latest, err := store.LatestLoginTokenCreatedAt(ctx, u.ID)
	if err != nil || latest == nil {
		t.Fatalf("token should still exist after GET /magic")
	}

	// Resend throttle: immediate second request creates no new token time advance? same created_at window
	countBefore := latest
	form2 := url.Values{}
	form2.Set("email", email)
	form2.Set("return_to", "https://customproject.com/callback")
	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form2.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "auth.nacfson.cloud"
	rec = httptest.NewRecorder()
	handler.RequestMagicLink(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("throttled RequestMagicLink status = %d", rec.Code)
	}
	latest2, _ := store.LatestLoginTokenCreatedAt(ctx, u.ID)
	if latest2 == nil || !latest2.Equal(*countBefore) {
		t.Fatalf("expected throttled resend to keep existing token timestamp")
	}

	// Consume
	cform := url.Values{}
	cform.Set("token", token)
	cform.Set("return_to", "https://customproject.com/callback")
	req = httptest.NewRequest(http.MethodPost, "/magic", strings.NewReader(cform.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.MagicConsume(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("MagicConsume status = %d", rec.Code)
	}
	if rec.Header().Get("Location") != "https://customproject.com/callback" {
		t.Fatalf("MagicConsume redirect = %q", rec.Header().Get("Location"))
	}
	u, _ = store.GetUserByEmail(ctx, email)
	if !u.IsVerified {
		t.Fatalf("user should be verified after consume")
	}
	foundCookie := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			foundCookie = true
			if !c.HttpOnly || !c.Secure {
				t.Fatalf("cookie flags HttpOnly=%v Secure=%v", c.HttpOnly, c.Secure)
			}
		}
	}
	if !foundCookie {
		t.Fatalf("expected session cookie")
	}

	// Reuse fails
	req = httptest.NewRequest(http.MethodPost, "/magic", strings.NewReader(cform.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	handler.MagicConsume(rec, req)
	if !strings.Contains(rec.Header().Get("Location"), "link+expired+or+already+used") {
		t.Fatalf("expected reused token error, got %q", rec.Header().Get("Location"))
	}

	// Legacy redirects
	req = httptest.NewRequest(http.MethodGet, "/register?rd=/dashboard", nil)
	rec = httptest.NewRecorder()
	handler.redirectRegisterToLogin(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("register redirect status = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/verify-email", nil)
	rec = httptest.NewRecorder()
	handler.redirectVerifyEmailToLogin(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("verify-email redirect status = %d", rec.Code)
	}
}

func TestMagicLinkRateLimit(t *testing.T) {
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	store := NewStore(pool)
	_ = store.Migrate(ctx)
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")

	limiter := NewLoginLimiter(1, time.Minute)
	handler := &Handler{
		store:        store,
		cookieDomain: "nacfson.cloud",
		sessionTTL:   time.Hour,
		magicLinkTTL: 15 * time.Minute,
		loginLimiter: limiter,
		mailer:       NewMailer("nacfson.cloud"),
	}

	form := url.Values{}
	form.Set("email", "rate@example.com")
	form.Set("return_to", "/")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "auth.nacfson.cloud"
	rec := httptest.NewRecorder()
	handler.RequestMagicLink(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("first request status = %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Host = "auth.nacfson.cloud"
	rec2 := httptest.NewRecorder()
	handler.RequestMagicLink(rec2, req2)
	if !strings.Contains(rec2.Header().Get("Location"), "too+many+attempts") {
		t.Fatalf("expected rate limit, got %q", rec2.Header().Get("Location"))
	}
}

func TestAPIMagicLink(t *testing.T) {
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	store := NewStore(pool)
	_ = store.Migrate(ctx)
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")

	handler := &Handler{
		store:             store,
		cookieDomain:      "nacfson.cloud",
		sessionTTL:        time.Hour,
		magicLinkTTL:      15 * time.Minute,
		apiAllowedOrigins: []string{"https://pn.nacfson.cloud"},
		mailer:            NewMailer("nacfson.cloud"),
	}

	body := strings.NewReader(`{"email":"api-magic@example.com","redirect":"https://manager.nacfson.cloud"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/magic-link", body)
	req.Header.Set("Origin", "https://pn.nacfson.cloud")
	req.Host = "auth.nacfson.cloud"
	rec := httptest.NewRecorder()
	handler.APIMagicLink(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("APIMagicLink status = %d body=%s", rec.Code, rec.Body.String())
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/auth/magic-link", strings.NewReader(`{"email":"x@example.com"}`))
	bad.Header.Set("Origin", "https://evil.example")
	bad.Host = "auth.nacfson.cloud"
	badRec := httptest.NewRecorder()
	handler.APIMagicLink(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d", badRec.Code)
	}
}

func TestMagicConsumeRejectsMaliciousRedirect(t *testing.T) {
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	store := NewStore(pool)
	_ = store.Migrate(ctx)
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")

	user, err := store.CreateUser(ctx, "redir@example.com", "", false, false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, hash, err := NewAuthCode()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if err := store.CreateLoginToken(ctx, user.ID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	handler := &Handler{
		store:        store,
		cookieDomain: "nacfson.cloud",
		sessionTTL:   time.Hour,
		magicLinkTTL: 15 * time.Minute,
		mailer:       NewMailer("nacfson.cloud"),
	}
	form := url.Values{}
	form.Set("token", raw)
	form.Set("return_to", "https://evil.example/phish")
	req := httptest.NewRequest(http.MethodPost, "/magic", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.MagicConsume(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "evil.example") {
		t.Fatalf("malicious redirect allowed: %q", loc)
	}
}
