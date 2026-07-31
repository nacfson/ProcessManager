package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalCookieSessionRequiresScopedTokenAndCookie(t *testing.T) {
	t.Setenv("AUTH_SESSION_INTERNAL_TOKEN", "session-verifier-token")
	handler := &Handler{}
	mux := http.NewServeMux()
	mux.Handle("GET /api/internal/cookie-session",
		handler.verifySessionInternalToken(http.HandlerFunc(handler.InternalCookieSession)))

	tests := []struct {
		name       string
		authHeader string
		cookie     string
		wantStatus int
	}{
		{
			name:       "missing internal token",
			cookie:     "session-value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid internal token",
			authHeader: "Bearer wrong-token",
			cookie:     "session-value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing session cookie",
			authHeader: "Bearer session-verifier-token",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/internal/cookie-session", nil)
			if test.authHeader != "" {
				req.Header.Set("Authorization", test.authHeader)
			}
			if test.cookie != "" {
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: test.cookie})
			}
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestInternalCookieSessionDoesNotAcceptTokenHashQuery(t *testing.T) {
	t.Setenv("AUTH_SESSION_INTERNAL_TOKEN", "session-verifier-token")
	handler := &Handler{}
	mux := http.NewServeMux()
	mux.Handle("GET /api/internal/cookie-session",
		handler.verifySessionInternalToken(http.HandlerFunc(handler.InternalCookieSession)))

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/internal/cookie-session?token_hash=precomputed-hash",
		nil,
	)
	req.Header.Set("Authorization", "Bearer session-verifier-token")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestProtectedAuthRoutesRequireCSRF(t *testing.T) {
	handler := &Handler{}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, path := range []string{"/login", "/magic", "/logout"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}

func TestAPILogoutCookieRequiresCSRF(t *testing.T) {
	handler := &Handler{cookieDomain: "nacfson.cloud"}
	req := httptest.NewRequest(http.MethodDelete, "/api/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-value"})
	rec := httptest.NewRecorder()

	handler.APILogout(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAPIOptionsAllowsCredentialedRequests(t *testing.T) {
	handler := &Handler{apiAllowedOrigins: []string{"https://pn.nacfson.cloud"}}
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/session", nil)
	req.Header.Set("Origin", "https://pn.nacfson.cloud")
	rec := httptest.NewRecorder()

	handler.APIOptions(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, X-CSRF-Token" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
}
