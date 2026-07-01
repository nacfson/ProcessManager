package settings

import (
	"net/http"

	"processmanager/internal/handlers"
)

// Handler handles settings pages.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers settings routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /settings", h.require(h.Index))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

// Index shows current platform settings.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	handlers.Render(h.deps.Tmpl, w, r, "pages/settings/settings", map[string]any{
		"Title":     "Settings",
		"Nav":       "settings",
		"Namespace": h.deps.Namespace,
		"Domain":    h.deps.Domain,
	})
}
