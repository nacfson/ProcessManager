package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestEnsureCSRFTokenSetsParentDomainCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://auth.nacfson.cloud/login", nil)
	rec := httptest.NewRecorder()

	token := EnsureCSRFToken(rec, req, "nacfson.cloud")
	if len(token) != 64 {
		t.Fatalf("CSRF token length = %d, want 64", len(token))
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("response cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != authCSRFCookieName || cookie.Value != token {
		t.Fatalf("CSRF cookie = %#v, want name/value %q/%q", cookie, authCSRFCookieName, token)
	}
	if cookie.Domain != "nacfson.cloud" {
		t.Fatalf("CSRF cookie domain = %q, want nacfson.cloud", cookie.Domain)
	}
	if cookie.HttpOnly {
		t.Fatal("CSRF cookie must be readable by trusted browser code")
	}
	if !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("CSRF cookie security attributes = secure:%v samesite:%v", cookie.Secure, cookie.SameSite)
	}
}

func TestEnsureCSRFTokenReusesExistingCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://auth.nacfson.cloud/login", nil)
	req.AddCookie(&http.Cookie{Name: authCSRFCookieName, Value: "existing-token"})
	rec := httptest.NewRecorder()

	if got := EnsureCSRFToken(rec, req, "nacfson.cloud"); got != "existing-token" {
		t.Fatalf("EnsureCSRFToken = %q, want existing-token", got)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("existing CSRF cookie should not be replaced")
	}
}

func TestValidCSRFTokenAcceptsFormAndHeader(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*http.Request)
	}{
		{
			name: "form",
			setup: func(req *http.Request) {
				form := url.Values{"csrf_token": {"same-token"}}
				req.Body = http.NoBody
				req.URL.RawQuery = form.Encode()
			},
		},
		{
			name: "header",
			setup: func(req *http.Request) {
				req.Header.Set(authCSRFHeaderName, "same-token")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(""))
			req.AddCookie(&http.Cookie{Name: authCSRFCookieName, Value: "same-token"})
			test.setup(req)
			if !ValidCSRFToken(req) {
				t.Fatal("ValidCSRFToken = false, want true")
			}
		})
	}
}

func TestValidCSRFTokenRejectsMissingOrMismatchedToken(t *testing.T) {
	for _, test := range []struct {
		name         string
		cookieValue  string
		requestValue string
	}{
		{name: "missing cookie", requestValue: "same-token"},
		{name: "missing request token", cookieValue: "same-token"},
		{name: "mismatch", cookieValue: "same-token", requestValue: "different-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/logout", nil)
			if test.cookieValue != "" {
				req.AddCookie(&http.Cookie{Name: authCSRFCookieName, Value: test.cookieValue})
			}
			if test.requestValue != "" {
				req.Header.Set(authCSRFHeaderName, test.requestValue)
			}
			if ValidCSRFToken(req) {
				t.Fatal("ValidCSRFToken = true, want false")
			}
		})
	}
}
