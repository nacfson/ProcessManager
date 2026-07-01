package databases

import (
	"net/http"

	"processmanager/internal/handlers"
)

// Handler handles database pages.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers database routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /databases", h.require(h.List))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows provisioned databases.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	var dbs []string
	if h.deps.Provisioner != nil {
		list, err := h.deps.Provisioner.ListDatabases(r.Context())
		if err != nil {
			handlers.RenderError(w, err, http.StatusInternalServerError)
			return
		}
		dbs = list
	}
	handlers.Render(h.deps.Tmpl, w, r, "pages/databases/list", map[string]any{
		"Title":     "Databases",
		"Nav":       "databases",
		"Databases": dbs,
	})
}
