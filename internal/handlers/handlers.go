package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"processmanager/internal/auth"
	"processmanager/internal/k8s"
	"processmanager/internal/models"
)

// Handler holds HTTP handlers for the dashboard.
type Handler struct {
	client    *k8s.Client
	namespace string
	domain    string
	tmpl      *template.Template
	authMW    *auth.Middleware
}

// New creates a new Handler.
func New(client *k8s.Client, namespace, domain string, authMW *auth.Middleware) (*Handler, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{
		client:    client,
		namespace: namespace,
		domain:    domain,
		tmpl:      tmpl,
		authMW:    authMW,
	}, nil
}

// RegisterRoutes registers all routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	require := func(next http.HandlerFunc) http.HandlerFunc {
		if h.authMW == nil {
			return next
		}
		return h.authMW.RequireAuth(next).ServeHTTP
	}

	mux.HandleFunc("GET /", require(h.List))
	mux.HandleFunc("GET /processes/new", require(h.NewForm))
	mux.HandleFunc("POST /processes", require(h.Create))
	mux.HandleFunc("POST /processes/{name}/delete", require(h.Delete))
	mux.HandleFunc("GET /processes/{name}/logs", require(h.Logs))
	mux.HandleFunc("GET /healthz", h.Healthz)
}

// List shows all processes.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	processes, err := h.client.ListProcesses(r.Context(), h.namespace)
	if err != nil {
		h.renderError(w, err)
		return
	}
	for i := range processes {
		processes[i].PublicURL = fmt.Sprintf("https://%s.%s", processes[i].Name, h.domain)
	}
	data := map[string]any{
		"Namespace": h.namespace,
		"Domain":    h.domain,
		"Processes": processes,
	}
	h.render(w, "list.html", data)
}

// NewForm shows the create form.
func (h *Handler) NewForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, "form.html", map[string]any{
		"Namespace": h.namespace,
		"Domain":    h.domain,
	})
}

// Create handles process creation.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, err)
		return
	}

	p := models.Process{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Image:       strings.TrimSpace(r.FormValue("image")),
		Namespace:   h.namespace,
		Arch:        r.FormValue("arch"),
		MemoryLimit: r.FormValue("memoryLimit"),
		Env:         parseEnv(r.FormValue("env")),
	}
	if r.FormValue("port") != "" {
		var port int32
		if _, err := fmt.Sscanf(r.FormValue("port"), "%d", &port); err == nil {
			p.Port = port
		}
	}
	if r.FormValue("replicas") != "" {
		var replicas int32
		if _, err := fmt.Sscanf(r.FormValue("replicas"), "%d", &replicas); err == nil {
			p.Replicas = replicas
		}
	}

	if err := p.Validate(); err != nil {
		h.renderError(w, err)
		return
	}

	if err := h.client.CreateProcess(r.Context(), p, h.domain); err != nil {
		h.renderError(w, err)
		return
	}

	h.renderFragment(w, "process-row.html", p)
}

// Delete removes a process.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.client.DeleteProcess(r.Context(), h.namespace, name); err != nil {
		h.renderError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Logs returns container logs.
func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	logs, err := h.client.GetPodLogs(r.Context(), h.namespace, name)
	if err != nil {
		h.renderError(w, err)
		return
	}
	h.render(w, "logs.html", map[string]any{
		"Name": name,
		"Logs": logs,
	})
}

// Healthz is a simple health check.
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) renderFragment(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) renderError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(err.Error()))
}

func parseEnv(input string) map[string]string {
	env := make(map[string]string)
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return env
}
