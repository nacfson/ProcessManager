package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

const (
	authCSRFCookieName = "nacfson_auth_csrf"
	authCSRFHeaderName = "X-CSRF-Token"
	authCSRFFormField  = "csrf_token"
)

// EnsureCSRFToken returns the request's auth CSRF token, creating one when
// needed. The cookie is parent-domain scoped so a trusted sibling application
// can submit a protected logout request back to the auth service.
func EnsureCSRFToken(w http.ResponseWriter, r *http.Request, domain string) string {
	if token := CSRFToken(r); token != "" {
		return token
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("failed to generate auth CSRF token: %v", err)
		return ""
	}
	token := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     authCSRFCookieName,
		Value:    token,
		Domain:   domain,
		Path:     "/",
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: false,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

// CSRFToken extracts the auth CSRF token from the request cookie.
func CSRFToken(r *http.Request) string {
	cookie, err := r.Cookie(authCSRFCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// ValidCSRFToken validates the double-submit token from a form or header.
func ValidCSRFToken(r *http.Request) bool {
	cookieToken := CSRFToken(r)
	if cookieToken == "" {
		return false
	}

	requestToken := r.Header.Get(authCSRFHeaderName)
	if requestToken == "" {
		requestToken = r.FormValue(authCSRFFormField)
	}
	return requestToken != "" &&
		subtle.ConstantTimeCompare([]byte(cookieToken), []byte(requestToken)) == 1
}

func (h *Handler) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !ValidCSRFToken(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}
