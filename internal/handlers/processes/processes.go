package processes

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"processmanager/internal/handlers"
	"processmanager/internal/k8s"
	"processmanager/internal/models"
)

// Handler handles process pages and fragments.
type Handler struct {
	deps handlers.Deps
}

// New creates a Handler.
func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

// RegisterRoutes registers process routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /processes", h.require(h.List))
	mux.HandleFunc("GET /processes/new", h.require(h.NewForm))
	mux.HandleFunc("POST /processes", h.require(h.Create))
	mux.HandleFunc("POST /processes/{name}/delete", h.require(h.Delete))
	mux.HandleFunc("GET /processes/{name}/logs", h.require(h.Logs))
	mux.HandleFunc("GET /processes/{name}/logs/stream", h.require(h.LogStream))
	mux.HandleFunc("GET /processes/{name}/status", h.require(h.Status))
	mux.HandleFunc("GET /processes/{name}/detail", h.require(h.Detail))
	mux.HandleFunc("GET /processes/{name}/pods", h.require(h.PodsFragment))
	mux.HandleFunc("GET /processes/{name}/events", h.require(h.DetailEventsFragment))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

// ProcessViewModel wraps models.Process with views-specific fields like restarts.
type ProcessViewModel struct {
	models.Process
	Restarts int32
}

// List shows all processes.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	processes, err := h.deps.Client.ListProcesses(r.Context(), h.deps.Namespace)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	viewModels := make([]ProcessViewModel, len(processes))
	for i, p := range processes {
		p.PublicURL = fmt.Sprintf("https://%s.%s", p.Name, h.deps.Domain)
		pods, err := h.deps.Client.ListPods(r.Context(), h.deps.Namespace, p.Name)
		var restarts int32
		if err == nil {
			for _, pod := range pods {
				restarts += pod.Restarts
			}
		} else {
			slog.Warn("failed to list pods for process", "process", p.Name, "error", err)
		}
		viewModels[i] = ProcessViewModel{
			Process:  p,
			Restarts: restarts,
		}
	}
	handlers.Render(h.deps.Tmpl, w, r, "pages/processes/list", map[string]any{
		"Title":     "Processes",
		"Nav":       "processes",
		"Namespace": h.deps.Namespace,
		"Domain":    h.deps.Domain,
		"Processes": viewModels,
	})
}

// NewForm shows the create form.
func (h *Handler) NewForm(w http.ResponseWriter, r *http.Request) {
	handlers.Render(h.deps.Tmpl, w, r, "pages/processes/form", map[string]any{
		"Title":     "New Process",
		"Nav":       "processes",
		"Namespace": h.deps.Namespace,
		"Domain":    h.deps.Domain,
	})
}

// Create handles process creation.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		handlers.RenderError(w, err, http.StatusBadRequest)
		return
	}
	p := models.Process{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Image:       strings.TrimSpace(r.FormValue("image")),
		Namespace:   h.deps.Namespace,
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
		handlers.RenderError(w, err, http.StatusBadRequest)
		return
	}
	if err := h.deps.Client.CreateProcess(r.Context(), p, h.deps.Domain); err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	p.PublicURL = fmt.Sprintf("https://%s.%s", p.Name, h.deps.Domain)
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/row", ProcessViewModel{Process: p, Restarts: 0})
}

// Delete removes a process.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := h.deps.Client.DeleteProcess(r.Context(), h.deps.Namespace, name); err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Logs returns container logs page.
func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	logs, err := h.deps.Client.GetPodLogs(r.Context(), h.deps.Namespace, name)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.Render(h.deps.Tmpl, w, r, "pages/processes/logs", map[string]any{
		"Title": name + " Logs",
		"Nav":   "processes",
		"Name":  name,
		"Logs":  logs,
	})
}

// LogStream returns HTML-escaped log text for HTMX polling.
func (h *Handler) LogStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	logs, err := h.deps.Client.GetPodLogs(r.Context(), h.deps.Namespace, name)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html.EscapeString(logs)))
}

// Status returns a status badge fragment for polling.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := h.deps.Client.GetProcess(r.Context(), h.deps.Namespace, name)
	if err != nil {
		handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/status", map[string]any{
			"Status": "error",
			"Label":  "Error",
		})
		return
	}
	status := "pending"
	label := fmt.Sprintf("%d/%d", p.Ready, p.Total)
	if p.Ready > 0 && p.Ready == p.Total {
		status = "ready"
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/status", map[string]any{
		"Status": status,
		"Label":  label,
	})
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

// Detail renders the process detail page.
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	detail, err := h.deps.Client.GetProcessDetail(r.Context(), h.deps.Namespace, name)
	if err != nil {
		if err == models.ErrProcessNotFound {
			handlers.RenderError(w, err, http.StatusNotFound)
		} else {
			handlers.RenderError(w, err, http.StatusInternalServerError)
		}
		return
	}
	logs, err := h.deps.Client.GetPodLogs(r.Context(), h.deps.Namespace, name)
	if err != nil {
		slog.Warn("failed to fetch pod logs", "process", name, "error", err)
		logs = "No logs available or no pods running."
	}
	publicURL := fmt.Sprintf("https://%s.%s", name, h.deps.Domain)
	detail.Process.PublicURL = publicURL

	handlers.Render(h.deps.Tmpl, w, r, "pages/processes/detail", map[string]any{
		"Title":     name + " - Process Detail",
		"Nav":       "processes",
		"Detail":    detail,
		"PublicURL": publicURL,
		"Logs":      logs,
	})
}

// PodsFragment renders the pods fragment for polling.
func (h *Handler) PodsFragment(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pods, err := h.deps.Client.ListPods(r.Context(), h.deps.Namespace, name)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/pods-fragment", map[string]any{
		"Detail": map[string]any{
			"Pods": pods,
		},
	})
}

// DetailEventsFragment renders the events fragment for polling.
func (h *Handler) DetailEventsFragment(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	allEvents, err := h.deps.Client.ListEvents(r.Context(), h.deps.Namespace, 0)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	var events []k8s.EventInfo
	for _, e := range allEvents {
		parts := strings.SplitN(e.InvolvedObject, "/", 2)
		if len(parts) < 2 {
			continue
		}
		objName := parts[1]
		if objName == name || strings.HasPrefix(objName, name+"-") {
			events = append(events, e)
		}
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/processes/detail-events-fragment", map[string]any{
		"Detail": map[string]any{
			"Events": events,
		},
	})
}
