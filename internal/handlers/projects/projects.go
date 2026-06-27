package projects

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"processmanager/internal/handlers"
)

// Handler handles project pages.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers project routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /projects", h.require(h.List))
	mux.HandleFunc("POST /projects", h.require(h.Create))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows all projects.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	var projects []any
	if h.deps.AuthStore != nil {
		list, err := h.deps.AuthStore.ListProjects(r.Context())
		if err != nil {
			slog.Error("list projects failed", "error", err)
		} else {
			for _, p := range list {
				projects = append(projects, p)
			}
		}
	}
	handlers.Render(h.deps.Tmpl, w, "pages/projects/list", map[string]any{
		"Title":    "Projects",
		"Nav":      "projects",
		"Projects": projects,
	})
}

// Create handles project creation.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		handlers.RenderError(w, err, http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	namespace := strings.TrimSpace(r.FormValue("namespace"))
	domainHost := strings.TrimSpace(r.FormValue("domainHost"))
	projType := r.FormValue("type")
	if projType == "" {
		projType = "simple"
	}
	dbRequired := r.FormValue("dbRequired") == "on"
	authEnabled := r.FormValue("authEnabled") == "on"

	if name == "" || namespace == "" || domainHost == "" {
		handlers.RenderError(w, fmt.Errorf("name, namespace, and domain host are required"), http.StatusBadRequest)
		return
	}

	if h.deps.AuthStore == nil {
		handlers.RenderError(w, fmt.Errorf("auth store not configured"), http.StatusServiceUnavailable)
		return
	}

	p, err := h.deps.AuthStore.CreateProject(r.Context(), name, namespace, domainHost, projType, dbRequired, authEnabled)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/projects/create-response", p)
}
