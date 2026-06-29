package auth

import (
	"encoding/json"
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
	store             *Store
	cookieDomain      string
	sessionTTL        time.Duration
	apiAllowedOrigins []string
	tmpl              *template.Template
}

type apiLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type apiSessionResponse struct {
	Token     string          `json:"token,omitempty"`
	ExpiresAt time.Time       `json:"expires_at"`
	User      apiUserResponse `json:"user"`
}

type apiUserResponse struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

// NewHandler creates an auth handler.
func NewHandler(store *Store, cookieDomain string, sessionTTL time.Duration, apiAllowedOrigins ...[]string) (*Handler, error) {
	tmpl, err := template.New("").ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	var allowedOrigins []string
	if len(apiAllowedOrigins) > 0 {
		allowedOrigins = apiAllowedOrigins[0]
	}
	return &Handler{
		store:             store,
		cookieDomain:      cookieDomain,
		sessionTTL:        sessionTTL,
		apiAllowedOrigins: allowedOrigins,
		tmpl:              tmpl,
	}, nil
}

// RegisterRoutes registers auth routes on the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.LoginForm)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("GET /verify", h.Verify)
	mux.HandleFunc("OPTIONS /api/auth/session", h.APIOptions)
	mux.HandleFunc("POST /api/auth/session", h.APILogin)
	mux.HandleFunc("GET /api/auth/session", h.APISession)
	mux.HandleFunc("DELETE /api/auth/session", h.APILogout)
	mux.HandleFunc("OPTIONS /api/auth/me", h.APIOptions)
	mux.HandleFunc("GET /api/auth/me", h.APIMe)
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

// APILogin authenticates a user and returns a bearer session token.
func (h *Handler) APILogin(w http.ResponseWriter, r *http.Request) {
	if !h.allowAPIOrigin(w, r) {
		return
	}
	var req apiLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		writeAPIError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if user == nil || user.PasswordHash == "" {
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, hash, err := NewSessionToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess, err := h.store.CreateSession(r.Context(), user.ID, hash, h.sessionTTL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess.Email = user.Email
	sess.IsAdmin = user.IsAdmin

	writeAPIJSON(w, http.StatusOK, apiSessionResponse{
		Token:     token,
		ExpiresAt: sess.ExpiresAt,
		User:      apiUserFromSession(sess),
	})
}

// APISession returns the bearer session and user details.
func (h *Handler) APISession(w http.ResponseWriter, r *http.Request) {
	if !h.allowAPIOrigin(w, r) {
		return
	}
	sess, ok := h.apiSession(w, r)
	if !ok {
		return
	}
	writeAPIJSON(w, http.StatusOK, apiSessionResponse{
		ExpiresAt: sess.ExpiresAt,
		User:      apiUserFromSession(sess),
	})
}

// APIMe returns the authenticated bearer user.
func (h *Handler) APIMe(w http.ResponseWriter, r *http.Request) {
	if !h.allowAPIOrigin(w, r) {
		return
	}
	sess, ok := h.apiSession(w, r)
	if !ok {
		return
	}
	writeAPIJSON(w, http.StatusOK, apiUserFromSession(sess))
}

// APILogout revokes the bearer session.
func (h *Handler) APILogout(w http.ResponseWriter, r *http.Request) {
	if !h.allowAPIOrigin(w, r) {
		return
	}
	token := BearerToken(r)
	if token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.store.DeleteSession(r.Context(), HashSessionToken(token)); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// APIOptions handles preflight checks for the central auth JSON API.
func (h *Handler) APIOptions(w http.ResponseWriter, r *http.Request) {
	if !h.allowAPIOrigin(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (h *Handler) apiSession(w http.ResponseWriter, r *http.Request) (*Session, bool) {
	token := BearerToken(r)
	if token == "" {
		writeAPIError(w, http.StatusUnauthorized, "missing bearer token")
		return nil, false
	}
	sess, err := h.store.GetSessionByTokenHash(r.Context(), HashSessionToken(token))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if sess == nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid session")
		return nil, false
	}
	return sess, true
}

func (h *Handler) allowAPIOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range h.apiAllowedOrigins {
		if origin == allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "300")
			return true
		}
	}
	writeAPIError(w, http.StatusForbidden, "origin is not allowed")
	return false
}

func apiUserFromSession(sess *Session) apiUserResponse {
	return apiUserResponse{
		ID:      sess.UserID,
		Email:   sess.Email,
		IsAdmin: sess.IsAdmin,
	}
}

func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeAPIJSON(w, status, map[string]string{"error": message})
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
