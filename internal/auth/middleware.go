package auth

import (
	"context"
	"net/http"
	"net/url"
)

// contextKey is used for request context values.
type contextKey int

const contextUserKey contextKey = iota

// UserContext returns the authenticated user from the request context.
func UserContext(ctx context.Context) *Session {
	s, _ := ctx.Value(contextUserKey).(*Session)
	return s
}

// Middleware protects HTTP handlers by validating the session cookie.
// If the session is invalid, it redirects to the auth login page.
type Middleware struct {
	store        *Store
	authHost     string
	cookieDomain string
}

// NewMiddleware creates an auth middleware.
func NewMiddleware(store *Store, authHost, cookieDomain string) *Middleware {
	return &Middleware{
		store:        store,
		authHost:     authHost,
		cookieDomain: cookieDomain,
	}
}

// RequireAuth wraps an http.Handler and requires a valid session.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := SessionCookie(r)
		if token == "" {
			m.redirect(w, r)
			return
		}
		sess, err := m.store.GetSessionByTokenHash(r.Context(), HashSessionToken(token))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if sess == nil {
			ClearSessionCookie(w, m.cookieDomain)
			m.redirect(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextUserKey, sess)))
	})
}

// OptionalAuth is like RequireAuth but allows unauthenticated requests.
func (m *Middleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := SessionCookie(r)
		if token != "" {
			if sess, err := m.store.GetSessionByTokenHash(r.Context(), HashSessionToken(token)); err == nil && sess != nil {
				r = r.WithContext(context.WithValue(r.Context(), contextUserKey, sess))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) redirect(w http.ResponseWriter, r *http.Request) {
	returnURL := "https://" + r.Host + r.URL.RequestURI()
	loginURL := "https://" + m.authHost + "/login?rd=" + url.QueryEscape(returnURL)
	http.Redirect(w, r, loginURL, http.StatusFound)
}
