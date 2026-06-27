package home

import (
	"net/http"

	"processmanager/internal/handlers"
)

// Handler renders the dashboard home page.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers the home route.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", handlers.RequireAuth(h.deps.AuthMW, h.Index))
}

// Index shows summary cards.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	processCount := 0
	if procs, err := h.deps.Client.ListProcesses(ctx, h.deps.Namespace); err == nil {
		processCount = len(procs)
	}
	projectCount := 0
	if h.deps.AuthStore != nil {
		if projects, err := h.deps.AuthStore.ListProjects(ctx); err == nil {
			projectCount = len(projects)
		}
	}
	handlers.Render(h.deps.Tmpl, w, "pages/home/home", map[string]any{
		"Title":        "Dashboard",
		"Nav":          "home",
		"Namespace":    h.deps.Namespace,
		"Domain":       h.deps.Domain,
		"ProcessCount": processCount,
		"ProjectCount": projectCount,
	})
}
