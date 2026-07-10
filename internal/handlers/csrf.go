package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

const (
	csrfCookieName = "nacfson_csrf"
	csrfHeaderName = "X-CSRF-Token"
	csrfFormField  = "csrf_token"
)

// CSRFMiddleware implements the double-submit cookie pattern.
// For GET/HEAD/OPTIONS, it ensures a CSRF cookie exists.
// For POST/PUT/PATCH/DELETE, it validates the token from form field or header matches the cookie.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			ensureCSRFCookie(w, r)
			next.ServeHTTP(w, r)
		default:
			cookie, err := r.Cookie(csrfCookieName)
			if err != nil || cookie.Value == "" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			token := r.Header.Get(csrfHeaderName)
			if token == "" {
				token = r.FormValue(csrfFormField)
			}
			if !hmac.Equal([]byte(token), []byte(cookie.Value)) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		}
	})
}

func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(csrfCookieName); err == nil {
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Printf("failed to generate CSRF token: %v", err)
		return
	}
	token := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false, // Must be readable by JavaScript (HTMX needs it)
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
}

// CSRFToken extracts the current CSRF token from the request cookie.
func CSRFToken(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
