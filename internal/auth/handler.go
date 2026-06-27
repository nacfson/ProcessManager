package auth

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Handler implements the central auth HTTP API.
type Handler struct {
	store         *Store
	cookieDomain  string
	sessionTTL    time.Duration
	tmpl          *template.Template
}

// NewHandler creates an auth handler.
func NewHandler(store *Store, cookieDomain string, sessionTTL time.Duration) (*Handler, error) {
	tmpl, err := template.New("").ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{
		store:        store,
		cookieDomain: cookieDomain,
		sessionTTL:   sessionTTL,
		tmpl:         tmpl,
	}, nil
}

// RegisterRoutes registers auth routes on the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.LoginForm)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("GET /verify", h.Verify)
	mux.HandleFunc("GET /healthz", h.Healthz)
}

// LoginForm renders the login page.
func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("rd")
	if returnTo == "" {
		returnTo = "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.tmpl.ExecuteTemplate(w, "login.html", map[string]any{
		"ReturnTo": returnTo,
		"Error":    r.URL.Query().Get("error"),
	})
}

// Login authenticates a user and sets the session cookie.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.redirectLogin(w, r, "invalid request")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	password := r.FormValue("password")
	returnTo := r.FormValue("return_to")
	if returnTo == "" {
		returnTo = "https://" + h.cookieDomain
	}

	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil || user.PasswordHash == "" {
		h.redirectLogin(w, r, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		h.redirectLogin(w, r, "invalid credentials")
		return
	}

	token, hash, err := NewSessionToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.store.CreateSession(r.Context(), user.ID, hash, h.sessionTTL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	SetSessionCookie(w, token, h.sessionTTL, h.cookieDomain)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// Logout clears the session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if token := SessionCookie(r); token != "" {
		_ = h.store.DeleteSession(r.Context(), HashSessionToken(token))
	}
	ClearSessionCookie(w, h.cookieDomain)
	http.Redirect(w, r, "https://"+h.cookieDomain, http.StatusFound)
}

// Verify is called by ingress-nginx auth-url for every request.
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	if host == "" {
		host = r.Header.Get("X-Original-Host")
	}
	if host == "" {
		host = r.Host
	}

	token := SessionCookie(r)
	if token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	sess, err := h.store.GetSessionByTokenHash(r.Context(), HashSessionToken(token))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// If the project does not require auth, allow through. Otherwise check membership.
	project, err := h.store.GetProjectByHost(r.Context(), host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if project != nil && project.AuthEnabled && !sess.IsAdmin {
		roles, err := h.store.GetProjectRoles(r.Context(), project.ID, sess.UserID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(roles) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("X-User-Roles", FormatRoles(roles))
	} else {
		w.Header().Set("X-User-Roles", "")
	}

	w.Header().Set("X-User-Id", sess.UserID)
	w.Header().Set("X-User-Email", sess.Email)
	w.WriteHeader(http.StatusOK)
}

// Healthz is a simple health endpoint.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handler) redirectLogin(w http.ResponseWriter, r *http.Request, message string) {
	returnTo := r.FormValue("return_to")
	if returnTo == "" {
		returnTo = r.URL.Query().Get("rd")
	}
	q := "?error=" + url.QueryEscape(message)
	if returnTo != "" {
		q += "&rd=" + url.QueryEscape(returnTo)
	}
	http.Redirect(w, r, "/login"+q, http.StatusFound)
}
