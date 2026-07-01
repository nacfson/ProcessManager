package home

import (
	"context"
	"log/slog"
	"net/http"

	"processmanager/internal/handlers"
	"processmanager/internal/models"
)

// HealthItem represents health status for a process.
type HealthItem struct {
	Name         string
	Ready        int32
	Total        int32
	Restarts     int32
	HealthStatus string // "healthy", "degraded", "down"
}

// Handler renders the dashboard home page.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers the home route and fragments.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", handlers.RequireAuth(h.deps.AuthMW, h.Index))
	mux.HandleFunc("GET /home/health", handlers.RequireAuth(h.deps.AuthMW, h.Health))
	mux.HandleFunc("GET /home/events", handlers.RequireAuth(h.deps.AuthMW, h.Events))
}

// Index shows dashboard.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var errMsg string

	nodes, err := h.deps.Client.ListNodes(ctx)
	if err != nil {
		slog.Error("list nodes failed", "error", err)
		errMsg = "Failed to load dashboard data"
	}

	processes, err := h.deps.Client.ListProcesses(ctx, h.deps.Namespace)
	var healthItems []HealthItem
	if err != nil {
		slog.Error("list processes failed", "error", err)
		errMsg = "Failed to load dashboard data"
	} else {
		healthItems, err = h.buildHealthItems(ctx, processes)
		if err != nil {
			slog.Error("build health items failed", "error", err)
			errMsg = "Failed to load dashboard data"
		}
	}

	events, err := h.deps.Client.ListEvents(ctx, h.deps.Namespace, 20)
	if err != nil {
		slog.Error("list events failed", "error", err)
		errMsg = "Failed to load dashboard data"
	}

	projectCount := 0
	if h.deps.AuthStore != nil {
		if projects, err := h.deps.AuthStore.ListProjects(ctx); err == nil {
			projectCount = len(projects)
		} else {
			slog.Error("list projects failed", "error", err)
			errMsg = "Failed to load dashboard data"
		}
	}

	handlers.Render(h.deps.Tmpl, w, r, "pages/home/home", map[string]any{
		"Title":        "Dashboard",
		"Nav":          "home",
		"Namespace":    h.deps.Namespace,
		"Domain":       h.deps.Domain,
		"Nodes":        nodes,
		"Processes":    healthItems,
		"Events":       events,
		"ProjectCount": projectCount,
		"Error":        errMsg,
	})
}

// Health renders the health fragment.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	processes, err := h.deps.Client.ListProcesses(ctx, h.deps.Namespace)
	if err != nil {
		slog.Error("list processes failed", "error", err)
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	healthItems, err := h.buildHealthItems(ctx, processes)
	if err != nil {
		slog.Error("build health items failed", "error", err)
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/home/health-fragment", map[string]any{
		"Processes": healthItems,
	})
}

// Events renders the events fragment.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	events, err := h.deps.Client.ListEvents(ctx, h.deps.Namespace, 20)
	if err != nil {
		slog.Error("list events failed", "error", err)
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/home/events-fragment", map[string]any{
		"Events": events,
	})
}

// buildHealthItems aggregates pod details per process.
func (h *Handler) buildHealthItems(ctx context.Context, processes []models.Process) ([]HealthItem, error) {
	healthItems := make([]HealthItem, 0, len(processes))
	for _, p := range processes {
		pods, err := h.deps.Client.ListPods(ctx, h.deps.Namespace, p.Name)
		if err != nil {
			return nil, err
		}

		var restarts int32
		for _, pod := range pods {
			restarts += pod.Restarts
		}

		status := "healthy"
		if p.Ready == 0 && p.Total > 0 {
			status = "down"
		} else if p.Ready < p.Total || restarts > 0 {
			status = "degraded"
		}

		healthItems = append(healthItems, HealthItem{
			Name:         p.Name,
			Ready:        p.Ready,
			Total:        p.Total,
			Restarts:     restarts,
			HealthStatus: status,
		})
	}
	return healthItems, nil
}
