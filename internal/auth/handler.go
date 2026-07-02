package auth

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	allowedRedirects  []string
	tmpl              *template.Template
	loginLimiter      *loginLimiter
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
func NewHandler(store *Store, cookieDomain string, sessionTTL time.Duration, loginLimiter *loginLimiter, apiAllowedOrigins []string, allowedRedirects []string) (*Handler, error) {
	tmpl, err := template.New("").ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{
		store:             store,
		cookieDomain:      cookieDomain,
		sessionTTL:        sessionTTL,
		apiAllowedOrigins: apiAllowedOrigins,
		allowedRedirects:  allowedRedirects,
		tmpl:              tmpl,
		loginLimiter:      loginLimiter,
	}, nil
}

// clientIP returns the client IP for rate-limiting purposes, trusting the
// rightmost address from X-Forwarded-For for the configured number of proxies,
// then falling back to X-Real-IP and finally r.RemoteAddr.
func clientIP(r *http.Request, trustedProxies int) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" && trustedProxies > 0 {
		parts := strings.Split(xff, ",")
		if len(parts) >= trustedProxies {
			ip := strings.TrimSpace(parts[len(parts)-trustedProxies])
			if host, _, err := net.SplitHostPort(ip); err == nil {
				ip = host
			}
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if host, _, err := net.SplitHostPort(xri); err == nil {
			xri = host
		}
		if net.ParseIP(xri) != nil {
			return xri
		}
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	return host
}

func (h *Handler) loginAllowed(r *http.Request, email string) bool {
	if h.loginLimiter == nil {
		return true
	}
	identifier := clientIP(r, 1)
	if ip := net.ParseIP(identifier); ip != nil {
		identifier = ip.String()
	}
	return h.loginLimiter.check(identifier, email)
}

func (h *Handler) recordFailedLogin(r *http.Request, email string) {
	if h.loginLimiter == nil {
		return
	}
	identifier := clientIP(r, 1)
	if ip := net.ParseIP(identifier); ip != nil {
		identifier = ip.String()
	}
	h.loginLimiter.record(identifier, email)
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

	// Internal APIs
	mux.Handle("GET /api/internal/session", h.verifyInternalToken(http.HandlerFunc(h.InternalSession)))
	mux.Handle("GET /api/internal/projects", h.verifyInternalToken(http.HandlerFunc(h.InternalListProjects)))
	mux.Handle("POST /api/internal/projects", h.verifyInternalToken(http.HandlerFunc(h.InternalCreateProject)))
	mux.Handle("GET /api/internal/users", h.verifyInternalToken(http.HandlerFunc(h.InternalListUsers)))
	mux.Handle("POST /api/internal/audit-events", h.verifyInternalToken(http.HandlerFunc(h.InternalLogAudit)))
	mux.Handle("GET /api/internal/audit-events", h.verifyInternalToken(http.HandlerFunc(h.InternalListAudit)))
}

// LoginForm renders the login page.
func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("rd")
	validatedReturnTo, err := validateReturnTo(returnTo, h.cookieDomain, h.allowedRedirects)
	if err != nil {
		validatedReturnTo, _ = validateReturnTo("", h.cookieDomain, h.allowedRedirects)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.tmpl.ExecuteTemplate(w, "login.html", map[string]any{
		"ReturnTo": validatedReturnTo,
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
	if !h.loginAllowed(r, email) {
		h.redirectLogin(w, r, "too many attempts")
		return
	}
	password := r.FormValue("password")
	returnTo := r.FormValue("return_to")
	validatedReturnTo, err := validateReturnTo(returnTo, h.cookieDomain, h.allowedRedirects)
	if err != nil {
		h.redirectLogin(w, r, "invalid request")
		return
	}

	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil || user.PasswordHash == "" {
		h.recordFailedLogin(r, email)
		h.redirectLogin(w, r, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		h.recordFailedLogin(r, email)
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
	http.Redirect(w, r, validatedReturnTo, http.StatusFound)
}

// Logout clears the session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if token := SessionCookie(r); token != "" {
		_ = h.store.DeleteSession(r.Context(), HashSessionToken(token))
	}
	ClearSessionCookie(w, h.cookieDomain)
	validatedReturnTo, _ := validateReturnTo("", h.cookieDomain, h.allowedRedirects)
	http.Redirect(w, r, validatedReturnTo, http.StatusFound)
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
	if !h.loginAllowed(r, email) {
		writeAPIError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}

	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if user == nil || user.PasswordHash == "" {
		h.recordFailedLogin(r, email)
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		h.recordFailedLogin(r, email)
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
	validatedReturnTo, err := validateReturnTo(returnTo, h.cookieDomain, h.allowedRedirects)
	if err != nil {
		validatedReturnTo, _ = validateReturnTo("", h.cookieDomain, h.allowedRedirects)
	}

	q := "?error=" + url.QueryEscape(message)
	q += "&rd=" + url.QueryEscape(validatedReturnTo)
	http.Redirect(w, r, "/login"+q, http.StatusFound)
}


type internalSessionResponse struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	IsAdmin   bool      `json:"is_admin"`
	ExpiresAt time.Time `json:"expires_at"`
}

type internalProjectResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Namespace   string    `json:"namespace"`
	DomainHost  string    `json:"domain_host"`
	Type        string    `json:"type"`
	DBRequired  bool      `json:"db_required"`
	AuthEnabled bool      `json:"auth_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

type internalCreateProjectRequest struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	DomainHost  string `json:"domain_host"`
	Type        string `json:"type"`
	DBRequired  bool   `json:"db_required"`
	AuthEnabled bool   `json:"auth_enabled"`
}

type internalUserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type internalAuditEventRequest struct {
	ActorEmail   string `json:"actor_email"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

func (h *Handler) verifyInternalToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := os.Getenv("AUTH_INTERNAL_TOKEN")
		if expected == "" {
			writeAPIError(w, http.StatusUnauthorized, "internal token not configured")
			return
		}
		token := BearerToken(r)
		if token == "" || token != expected {
			writeAPIError(w, http.StatusUnauthorized, "invalid internal token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) InternalSession(w http.ResponseWriter, r *http.Request) {
	tokenHash := r.URL.Query().Get("token_hash")
	if tokenHash == "" {
		writeAPIError(w, http.StatusBadRequest, "missing token_hash parameter")
		return
	}

	sess, err := h.store.GetSessionByTokenHash(r.Context(), tokenHash)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sess == nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid session")
		return
	}

	writeAPIJSON(w, http.StatusOK, internalSessionResponse{
		UserID:    sess.UserID,
		Email:     sess.Email,
		IsAdmin:   sess.IsAdmin,
		ExpiresAt: sess.ExpiresAt,
	})
}

func (h *Handler) InternalListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]internalProjectResponse, 0, len(projects))
	for _, p := range projects {
		resp = append(resp, internalProjectResponse{
			ID:          p.ID,
			Name:        p.Name,
			Namespace:   p.Namespace,
			DomainHost:  p.DomainHost,
			Type:        p.Type,
			DBRequired:  p.DBRequired,
			AuthEnabled: p.AuthEnabled,
			CreatedAt:   p.CreatedAt,
		})
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (h *Handler) InternalCreateProject(w http.ResponseWriter, r *http.Request) {
	var req internalCreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	p, err := h.store.CreateProject(r.Context(), req.Name, req.Namespace, req.DomainHost, req.Type, req.DBRequired, req.AuthEnabled)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeAPIJSON(w, http.StatusCreated, internalProjectResponse{
		ID:          p.ID,
		Name:        p.Name,
		Namespace:   p.Namespace,
		DomainHost:  p.DomainHost,
		Type:        p.Type,
		DBRequired:  p.DBRequired,
		AuthEnabled: p.AuthEnabled,
		CreatedAt:   p.CreatedAt,
	})
}

func (h *Handler) InternalListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]internalUserResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, internalUserResponse{
			ID:        u.ID,
			Email:     u.Email,
			IsAdmin:   u.IsAdmin,
			CreatedAt: u.CreatedAt,
		})
	}
	writeAPIJSON(w, http.StatusOK, resp)
}

func (h *Handler) InternalLogAudit(w http.ResponseWriter, r *http.Request) {
	var req internalAuditEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.store.LogAuditEvent(r.Context(), req.ActorEmail, req.Action, req.ResourceType, req.ResourceID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeAPIJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

type internalAuditEventResponse struct {
	ID           string    `json:"id"`
	ActorEmail   string    `json:"actor_email"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h *Handler) InternalListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	events, err := h.store.ListAuditEvents(r.Context(), limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]internalAuditEventResponse, 0, len(events))
	for _, e := range events {
		resp = append(resp, internalAuditEventResponse{
			ID:           e.ID,
			ActorEmail:   e.ActorEmail,
			Action:       e.Action,
			ResourceType: e.ResourceType,
			ResourceID:   e.ResourceID,
			CreatedAt:    e.CreatedAt,
		})
	}
	writeAPIJSON(w, http.StatusOK, resp)
}
