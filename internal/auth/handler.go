package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
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
	allowedRedirects  []*url.URL
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

	var parsedRedirects []*url.URL
	for _, r := range allowedRedirects {
		u, err := url.Parse(strings.ReplaceAll(strings.TrimSpace(r), "\\", "/"))
		if err != nil {
			return nil, fmt.Errorf("invalid allowed redirect URL %q: %w", r, err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("allowed redirect URL %q must specify scheme and host", r)
		}
		parsedRedirects = append(parsedRedirects, u)
	}

	return &Handler{
		store:             store,
		cookieDomain:      cookieDomain,
		sessionTTL:        sessionTTL,
		apiAllowedOrigins: apiAllowedOrigins,
		allowedRedirects:  parsedRedirects,
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
	mux.HandleFunc("GET /register", h.RegisterForm)
	mux.HandleFunc("POST /register", h.Register)
	mux.HandleFunc("GET /verification-sent", h.VerificationSent)
	mux.HandleFunc("GET /verify-email", h.VerifyEmail)
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
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
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
		log.Printf("Login GetUserByEmail error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
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
	if !user.IsVerified {
		h.redirectLogin(w, r, "email not verified")
		return
	}

	token, hash, err := NewSessionToken()
	if err != nil {
		log.Printf("Login NewSessionToken error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.store.CreateSession(r.Context(), user.ID, hash, h.sessionTTL); err != nil {
		log.Printf("Login CreateSession error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	SetSessionCookie(w, token, h.sessionTTL, h.cookieDomain)
	http.Redirect(w, r, validatedReturnTo, http.StatusFound)
}

// RegisterForm renders the registration page.
func (h *Handler) RegisterForm(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("rd")
	if returnTo == "" {
		returnTo = "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.tmpl.ExecuteTemplate(w, "register.html", map[string]any{
		"ReturnTo": returnTo,
		"Error":    r.URL.Query().Get("error"),
	})
}

// Register processes a user registration.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
	if err := r.ParseForm(); err != nil {
		q := "?error=" + url.QueryEscape("invalid request")
		http.Redirect(w, r, "/register"+q, http.StatusFound)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")
	returnTo := r.FormValue("return_to")
	if returnTo == "" {
		returnTo = r.URL.Query().Get("rd")
	}
	if returnTo == "" {
		returnTo = "/"
	}

	if email == "" {
		h.redirectRegister(w, r, "email is required")
		return
	}

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		h.redirectRegister(w, r, "invalid email address")
		return
	}

	if password == "" {
		h.redirectRegister(w, r, "password is required")
		return
	}
	if password != confirmPassword {
		h.redirectRegister(w, r, "passwords do not match")
		return
	}

	existing, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		log.Printf("Register GetUserByEmail error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		h.redirectRegister(w, r, "email already registered")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Register bcrypt GenerateFromPassword error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	user, err := h.store.CreateUser(r.Context(), email, string(hash), false, false)
	if err != nil {
		log.Printf("Register CreateUser error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Printf("Register rand.Read error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(tokenBytes)

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	err = h.store.CreateEmailVerification(r.Context(), user.ID, token, expiresAt)
	if err != nil {
		log.Printf("Register CreateEmailVerification error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	verificationLink := fmt.Sprintf("http://%s/verify-email?token=%s&rd=%s", r.Host, token, url.QueryEscape(returnTo))

	smtpHost := os.Getenv("SMTP_HOST")
	if smtpHost != "" {
		smtpPort := os.Getenv("SMTP_PORT")
		if smtpPort == "" {
			smtpPort = "25"
		}
		addr := smtpHost
		if !strings.Contains(smtpHost, ":") {
			addr = net.JoinHostPort(smtpHost, smtpPort)
		}
		smtpUser := os.Getenv("SMTP_USER")
		var auth smtp.Auth
		if smtpUser != "" {
			smtpPass := os.Getenv("SMTP_PASS")
			if smtpPass == "" {
				smtpPass = os.Getenv("SMTP_PASSWORD")
			}
			auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
		}
		from := os.Getenv("SMTP_FROM")
		if from == "" {
			from = "noreply@" + h.cookieDomain
		}
		subject := "Verify your email address"
		body := fmt.Sprintf("Please verify your email address by clicking the following link:\n\n%s", verificationLink)
		msg := []byte("To: " + email + "\r\n" +
			"From: " + from + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"\r\n" +
			body + "\r\n")

		go func() {
			if err := smtp.SendMail(addr, auth, from, []string{email}, msg); err != nil {
				log.Printf("failed to send verification email to %s: %v", email, err)
			}
		}()
	} else {
		fmt.Println(verificationLink)
	}

	q := "?token=" + url.QueryEscape(token) + "&rd=" + url.QueryEscape(returnTo)
	http.Redirect(w, r, "/verification-sent"+q, http.StatusFound)
}

// VerificationSent renders the verification sent page.
func (h *Handler) VerificationSent(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("rd")
	if returnTo == "" {
		returnTo = "/"
	}
	token := r.URL.Query().Get("token")

	data := map[string]any{
		"ReturnTo": returnTo,
	}

	if os.Getenv("SMTP_HOST") == "" {
		data["VerificationLink"] = fmt.Sprintf("/verify-email?token=%s&rd=%s", url.QueryEscape(token), url.QueryEscape(returnTo))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.tmpl.ExecuteTemplate(w, "verification_sent.html", data)
}

// VerifyEmail verifies the email token and logs the user in.
func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/register?error="+url.QueryEscape("invalid or expired verification token"), http.StatusFound)
		return
	}

	ev, err := h.store.GetEmailVerification(r.Context(), token)
	if err != nil {
		log.Printf("VerifyEmail GetEmailVerification error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if ev == nil || ev.ExpiresAt.Before(time.Now()) {
		http.Redirect(w, r, "/register?error="+url.QueryEscape("invalid or expired verification token"), http.StatusFound)
		return
	}

	err = h.store.VerifyUser(r.Context(), ev.UserID)
	if err != nil {
		log.Printf("VerifyEmail VerifyUser error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	sessToken, hash, err := NewSessionToken()
	if err != nil {
		log.Printf("VerifyEmail NewSessionToken error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.store.CreateSession(r.Context(), ev.UserID, hash, h.sessionTTL); err != nil {
		log.Printf("VerifyEmail CreateSession error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	_ = h.store.DeleteEmailVerification(r.Context(), token)

	SetSessionCookie(w, sessToken, h.sessionTTL, h.cookieDomain)

	returnTo := r.URL.Query().Get("rd")
	validatedReturnTo, err := validateReturnTo(returnTo, h.cookieDomain, nil)
	if err != nil {
		validatedReturnTo = "/"
	}
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
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
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
		log.Printf("APILogin GetUserByEmail error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
	if !user.IsVerified {
		writeAPIError(w, http.StatusUnauthorized, "unverified email")
		return
	}

	token, hash, err := NewSessionToken()
	if err != nil {
		log.Printf("APILogin NewSessionToken error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	sess, err := h.store.CreateSession(r.Context(), user.ID, hash, h.sessionTTL)
	if err != nil {
		log.Printf("APILogin CreateSession error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("APILogout DeleteSession error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("Verify GetSessionByTokenHash error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if sess == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// If the project does not require auth, allow through. Otherwise check membership.
	project, err := h.store.GetProjectByHost(r.Context(), host)
	if err != nil {
		log.Printf("Verify GetProjectByHost error: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if project != nil && project.AuthEnabled && !sess.IsAdmin {
		roles, err := h.store.GetProjectRoles(r.Context(), project.ID, sess.UserID)
		if err != nil {
			log.Printf("Verify GetProjectRoles error: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
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
		log.Printf("apiSession GetSessionByTokenHash error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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

func (h *Handler) redirectRegister(w http.ResponseWriter, r *http.Request, message string) {
	returnTo := r.FormValue("return_to")
	if returnTo == "" {
		returnTo = r.URL.Query().Get("rd")
	}
	if returnTo == "" {
		returnTo = "/"
	}
	q := "?error=" + url.QueryEscape(message) + "&rd=" + url.QueryEscape(returnTo)
	http.Redirect(w, r, "/register"+q, http.StatusFound)
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
		log.Printf("InternalSession GetSessionByTokenHash error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("InternalListProjects ListProjects error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("InternalCreateProject CreateProject error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("InternalListUsers ListUsers error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("InternalLogAudit LogAuditEvent error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
		log.Printf("InternalListAudit ListAuditEvents error: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Internal server error")
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
