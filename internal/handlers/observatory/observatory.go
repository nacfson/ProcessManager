package observatory

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"processmanager/internal/handlers"
	"processmanager/internal/k8s"

	"k8s.io/apimachinery/pkg/api/resource"
)

type Handler struct {
	deps handlers.Deps
}

func New(deps handlers.Deps) *Handler {
	return &Handler{deps: deps}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /observatory", h.require(h.Overview))
	mux.HandleFunc("GET /observatory/projects/{name}", h.require(h.ProjectMetrics))
	mux.HandleFunc("GET /observatory/topology", h.require(h.Topology))
	
	// HTMX polling fragments
	mux.HandleFunc("GET /observatory/fragments/nodes", h.require(h.NodesFragment))
	mux.HandleFunc("GET /observatory/fragments/projects", h.require(h.ProjectsFragment))
	mux.HandleFunc("GET /observatory/fragments/warnings", h.require(h.WarningsFragment))
	mux.HandleFunc("GET /observatory/projects/{name}/fragments/resources", h.require(h.ResourcesFragment))
	mux.HandleFunc("GET /observatory/projects/{name}/fragments/events", h.require(h.ProjectEventsFragment))
}

func (h *Handler) require(next http.HandlerFunc) http.HandlerFunc {
	return handlers.RequireAuth(h.deps.AuthMW, next)
}

// NodeOverview combines node info with live metrics.
type NodeOverview struct {
	k8s.NodeInfo
	CPUPercent int // -1 if unavailable
	MemPercent int // -1 if unavailable
}

// ProjectOverview summarizes a project's live metrics.
type ProjectOverview struct {
	Name       string
	Namespace  string
	Processes  int
	ReadyPods  int32
	TotalPods  int32
	CPUPercent int // -1 if unavailable
	MemPercent int // -1 if unavailable
}

// PodResourceView combines pod info with live resource usage.
type PodResourceView struct {
	k8s.PodInfo
	CPUPercent int // -1 if unavailable
	MemPercent int // -1 if unavailable
	MemUsedMi  int64
	MemLimitMi int64
}

// TopologyEdge represents one Ingress -> Service -> Pods path.
type TopologyEdge struct {
	IngressHost string
	ServiceName string
	ReadyPods   int32
	TotalPods   int32
	Namespace   string
	Project     string
	Color       string
	Y           int
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodes, nodeMetrics, err := h.getNodesAndMetrics(ctx)
	if err != nil {
		slog.Error("observatory list nodes failed", "error", err)
	}

	projects, err := h.getProjectsOverview(ctx)
	if err != nil {
		slog.Error("observatory get projects failed", "error", err)
	}

	events, err := h.listAllEvents(ctx, 10)
	if err != nil {
		slog.Error("observatory list events failed", "error", err)
	}

	handlers.Render(h.deps.Tmpl, w, r, "pages/observatory/overview", map[string]any{
		"Title":       "Observatory - Cluster Overview",
		"Nav":         "observatory",
		"Nodes":       h.buildNodeOverviews(nodes, nodeMetrics),
		"Projects":    projects,
		"Events":      events,
		"ActiveTab":   "overview",
		"RefreshRate": "10s",
	})
}

func (h *Handler) NodesFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodes, nodeMetrics, err := h.getNodesAndMetrics(ctx)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/observatory/nodes-fragment", map[string]any{
		"Nodes": h.buildNodeOverviews(nodes, nodeMetrics),
	})
}

func (h *Handler) ProjectsFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := h.getProjectsOverview(ctx)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/observatory/projects-fragment", map[string]any{
		"Projects": projects,
	})
}

func (h *Handler) WarningsFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	events, err := h.listAllEvents(ctx, 10)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}
	handlers.RenderFragment(h.deps.Tmpl, w, "pages/observatory/warnings-fragment", map[string]any{
		"Events": events,
	})
}

func (h *Handler) ProjectMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectName := r.PathValue("name")
	
	project, err := h.deps.AuthStore.GetProjectByName(ctx, projectName)
	if err != nil || project == nil {
		handlers.RenderError(w, fmt.Errorf("project not found"), http.StatusNotFound)
		return
	}

	pods, podMetrics, err := h.getPodsAndMetrics(ctx, project.Namespace, "")
	if err != nil {
		slog.Error("observatory get pods failed", "error", err)
	}

	events, err := h.deps.Client.ListEvents(ctx, project.Namespace, 10)
	if err != nil {
		slog.Error("observatory list events failed", "error", err)
	}

	podViews := h.buildPodResourceViews(pods, podMetrics)
	
	// Aggregate request metrics (simulated/mock placeholder since nginx scraper is deferred)
	reqsSec := 12.3
	avgLatency := 142
	errRate := 0.1

	handlers.Render(h.deps.Tmpl, w, r, "pages/observatory/project-metrics", map[string]any{
		"Title":       "Observatory - Project Metrics",
		"Nav":         "observatory",
		"Project":     project,
		"Pods":        podViews,
		"Events":      events,
		"ReqsSec":     reqsSec,
		"AvgLatency":  avgLatency,
		"ErrRate":     errRate,
		"ActiveTab":   "project",
		"RefreshRate": "5s",
	})
}

func (h *Handler) ResourcesFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectName := r.PathValue("name")
	project, err := h.deps.AuthStore.GetProjectByName(ctx, projectName)
	if err != nil || project == nil {
		handlers.RenderError(w, fmt.Errorf("project not found"), http.StatusNotFound)
		return
	}

	pods, podMetrics, err := h.getPodsAndMetrics(ctx, project.Namespace, "")
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	handlers.RenderFragment(h.deps.Tmpl, w, "pages/observatory/resources-fragment", map[string]any{
		"Pods": h.buildPodResourceViews(pods, podMetrics),
	})
}

func (h *Handler) ProjectEventsFragment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectName := r.PathValue("name")
	project, err := h.deps.AuthStore.GetProjectByName(ctx, projectName)
	if err != nil || project == nil {
		handlers.RenderError(w, fmt.Errorf("project not found"), http.StatusNotFound)
		return
	}

	events, err := h.deps.Client.ListEvents(ctx, project.Namespace, 10)
	if err != nil {
		handlers.RenderError(w, err, http.StatusInternalServerError)
		return
	}

	handlers.RenderFragment(h.deps.Tmpl, w, "pages/observatory/project-events-fragment", map[string]any{
		"Events": events,
	})
}

func (h *Handler) Topology(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespaces, err := h.deps.ManagedNamespaces(ctx)
	if err != nil {
		slog.Error("observatory list namespaces failed", "error", err)
	}

	// Fetch projects to map namespace -> project name
	projMap := make(map[string]string)
	if h.deps.AuthStore != nil {
		if projs, err := h.deps.AuthStore.ListProjects(ctx); err == nil {
			for _, p := range projs {
				projMap[p.Namespace] = p.Name
			}
		}
	}

	var edges []TopologyEdge
	projectColors := []string{"#3b82f6", "#a855f7", "#14b8a6", "#f97316", "#ec4899", "#84cc16"}
	projectColorMap := make(map[string]string)
	colorIdx := 0

	for _, ns := range namespaces {
		ingresses, err := h.deps.Client.ListIngresses(ctx, ns)
		if err != nil {
			continue
		}
		services, err := h.deps.Client.ListServices(ctx, ns)
		if err != nil {
			continue
		}

		svcMap := make(map[string]k8s.ServiceInfo)
		for _, svc := range services {
			svcMap[svc.Name] = svc
		}

		for _, ing := range ingresses {
			svc, ok := svcMap[ing.ServiceName]
			ready, total := int32(0), int32(0)
			if ok {
				ready = svc.ReadyPods
				total = svc.TotalPods
			}
			
			projectName := projMap[ns]
			if projectName == "" {
				projectName = "unknown"
			}
			color, hasColor := projectColorMap[projectName]
			if !hasColor {
				color = projectColors[colorIdx%len(projectColors)]
				projectColorMap[projectName] = color
				colorIdx++
			}

			edges = append(edges, TopologyEdge{
				IngressHost: ing.Host,
				ServiceName: ing.ServiceName,
				ReadyPods:   ready,
				TotalPods:   total,
				Namespace:   ns,
				Project:     projectName,
				Color:       color,
				Y:           len(edges)*80 + 60,
			})
		}
	}

	svgHeight := len(edges)*80 + 40
	if svgHeight < 200 {
		svgHeight = 200
	}

	handlers.Render(h.deps.Tmpl, w, r, "pages/observatory/topology", map[string]any{
		"Title":       "Observatory - Service Topology",
		"Nav":         "observatory",
		"Edges":       edges,
		"ActiveTab":   "topology",
		"RefreshRate": "30s",
		"SVGHeight":   svgHeight,
	})
}

// Internal helpers

func (h *Handler) getNodesAndMetrics(ctx context.Context) ([]k8s.NodeInfo, []k8s.NodeMetrics, error) {
	nodes, err := h.deps.Client.ListNodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	metrics, err := h.deps.Client.GetNodeMetrics(ctx)
	if err != nil {
		slog.Warn("failed to fetch node metrics", "error", err)
	}
	return nodes, metrics, nil
}

func (h *Handler) buildNodeOverviews(nodes []k8s.NodeInfo, metrics []k8s.NodeMetrics) []NodeOverview {
	metricMap := make(map[string]k8s.NodeMetrics)
	for _, m := range metrics {
		metricMap[m.Name] = m
	}

	var res []NodeOverview
	for _, node := range nodes {
		cpuPercent, memPercent := -1, -1
		if m, ok := metricMap[node.Name]; ok {
			cpuCap := parseCPUQuantity(node.AllocCPU)
			if cpuCap == 0 {
				cpuCap = parseCPUQuantity(node.CapacityCPU)
			}
			memCap := parseMemQuantity(node.AllocMem)
			if memCap == 0 {
				memCap = parseMemQuantity(node.CapacityMem)
			}

			if cpuCap > 0 {
				cpuPercent = int((m.CPUNano * 100) / cpuCap)
			}
			if memCap > 0 {
				memPercent = int((m.MemBytes * 100) / memCap)
			}
		}
		res = append(res, NodeOverview{
			NodeInfo:   node,
			CPUPercent: cpuPercent,
			MemPercent: memPercent,
		})
	}
	return res
}

func (h *Handler) getProjectsOverview(ctx context.Context) ([]ProjectOverview, error) {
	if h.deps.AuthStore == nil {
		return nil, nil
	}
	projects, err := h.deps.AuthStore.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	var res []ProjectOverview
	for _, p := range projects {
		procs, err := h.deps.Client.ListProcesses(ctx, p.Namespace)
		if err != nil {
			continue
		}

		pods, podMetrics, err := h.getPodsAndMetrics(ctx, p.Namespace, "")
		if err != nil {
			continue
		}

		var ready, total int32
		var cpuTotal, memTotal int64
		for _, pod := range pods {
			total++
			if pod.Phase == "Running" {
				ready++
			}
		}

		hasMetrics := false
		for _, pm := range podMetrics {
			cpuTotal += pm.CPUNano
			memTotal += pm.MemBytes
			hasMetrics = true
		}

		cpuLimitTotal, memLimitTotal := int64(0), int64(0)
		for _, pod := range pods {
			cpuLimitTotal += parseCPUQuantity(pod.LimitsCPU)
			memLimitTotal += parseMemQuantity(pod.LimitsMem)
		}

		cpuPercent, memPercent := -1, -1
		if hasMetrics {
			if cpuLimitTotal > 0 {
				cpuPercent = int((cpuTotal * 100) / cpuLimitTotal)
			} else {
				cpuPercent = 0
			}
			if memLimitTotal > 0 {
				memPercent = int((memTotal * 100) / memLimitTotal)
			} else {
				memPercent = 0
			}
		}

		res = append(res, ProjectOverview{
			Name:       p.Name,
			Namespace:  p.Namespace,
			Processes:  len(procs),
			ReadyPods:  ready,
			TotalPods:  total,
			CPUPercent: cpuPercent,
			MemPercent: memPercent,
		})
	}
	return res, nil
}

func (h *Handler) getPodsAndMetrics(ctx context.Context, namespace, processName string) ([]k8s.PodInfo, []k8s.PodMetrics, error) {
	pods, err := h.deps.Client.ListPods(ctx, namespace, processName)
	if err != nil {
		return nil, nil, err
	}
	metrics, err := h.deps.Client.GetPodMetrics(ctx, namespace)
	if err != nil {
		slog.Warn("failed to fetch pod metrics", "namespace", namespace, "error", err)
	}
	return pods, metrics, nil
}

func (h *Handler) buildPodResourceViews(pods []k8s.PodInfo, metrics []k8s.PodMetrics) []PodResourceView {
	metricMap := make(map[string]k8s.PodMetrics)
	for _, m := range metrics {
		metricMap[m.Name] = m
	}

	var res []PodResourceView
	for _, pod := range pods {
		cpuPercent, memPercent := -1, -1
		var memUsedMi, memLimitMi int64
		
		cpuLimit := parseCPUQuantity(pod.LimitsCPU)
		memLimit := parseMemQuantity(pod.LimitsMem)
		memLimitMi = memLimit / (1024 * 1024)

		if m, ok := metricMap[pod.Name]; ok {
			memUsedMi = m.MemBytes / (1024 * 1024)
			if cpuLimit > 0 {
				cpuPercent = int((m.CPUNano * 100) / cpuLimit)
			} else {
				cpuPercent = 0
			}
			if memLimit > 0 {
				memPercent = int((m.MemBytes * 100) / memLimit)
			} else {
				memPercent = 0
			}
		}
		res = append(res, PodResourceView{
			PodInfo:    pod,
			CPUPercent: cpuPercent,
			MemPercent: memPercent,
			MemUsedMi:  memUsedMi,
			MemLimitMi: memLimitMi,
		})
	}
	return res
}

func (h *Handler) listAllEvents(ctx context.Context, limit int) ([]k8s.EventInfo, error) {
	namespaces, err := h.deps.ManagedNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	var all []k8s.EventInfo
	for _, ns := range namespaces {
		events, err := h.deps.Client.ListEvents(ctx, ns, 0)
		if err != nil {
			return nil, err
		}
		for i := range events {
			events[i].Namespace = ns
		}
		all = append(all, events...)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].LastTimestamp.After(all[j].LastTimestamp)
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func parseCPUQuantity(qStr string) int64 {
	if qStr == "" {
		return 0
	}
	q, err := resource.ParseQuantity(qStr)
	if err != nil {
		return 0
	}
	return q.ScaledValue(resource.Nano)
}

func parseMemQuantity(qStr string) int64 {
	if qStr == "" {
		return 0
	}
	q, err := resource.ParseQuantity(qStr)
	if err != nil {
		return 0
	}
	return q.Value()
}
