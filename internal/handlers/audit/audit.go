package audit

import (
	"log/slog"
	"net/http"

	"processmanager/internal/handlers"
)

// Handler handles activity pages.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers activity routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /activity", h.requireAdmin(h.List))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

func (h *Handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAdmin(h.deps.AuthMW, next)
}

// List shows recent activity.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	var events []any
	var errMsg string
	if h.deps.AuthStore != nil {
		list, err := h.deps.AuthStore.ListAuditEvents(r.Context(), 50)
		if err != nil {
			slog.Error("list audit events failed", "error", err)
			errMsg = "Failed to load activity"
		} else {
			for _, e := range list {
				events = append(events, e)
			}
		}
	}
	handlers.Render(h.deps.Tmpl, w, r, "pages/activity/list", map[string]any{
		"Title":  "Activity",
		"Nav":    "activity",
		"Events": events,
		"Error":  errMsg,
	})
}
