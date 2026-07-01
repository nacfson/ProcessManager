package users

import (
	"log/slog"
	"net/http"

	"processmanager/internal/handlers"
)

// Handler handles user pages.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers user routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /users", h.require(h.List))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

// List shows all users.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	var users []any
	var errMsg string
	if h.deps.AuthStore != nil {
		if list, err := h.deps.AuthStore.ListUsers(r.Context()); err == nil {
			for _, u := range list {
				users = append(users, u)
			}
		} else {
			slog.Error("list users failed", "error", err)
			errMsg = "Failed to load users"
		}
	}
	handlers.Render(h.deps.Tmpl, w, r, "pages/users/list", map[string]any{
		"Title": "Users",
		"Nav":   "users",
		"Users": users,
		"Error": errMsg,
	})
}
