package observatory

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"processmanager/internal/auth"
	"processmanager/internal/handlers"
	"processmanager/internal/k8s"
	"processmanager/internal/k8s/fake"
)

type mockAuthStore struct {
	projects []auth.Project
	err      error
}

func (m *mockAuthStore) CreateProject(ctx context.Context, name, namespace, domainHost, projType string, dbRequired, authEnabled bool) (*auth.Project, error) {
	return nil, nil
}

func (m *mockAuthStore) GetProjectByName(ctx context.Context, name string) (*auth.Project, error) {
	for _, p := range m.projects {
		if p.Name == name {
			return &p, nil
		}
	}
	return nil, nil
}

func (m *mockAuthStore) ListProjects(ctx context.Context) ([]auth.Project, error) {
	return m.projects, m.err
}

func (m *mockAuthStore) ListUsers(ctx context.Context) ([]auth.User, error) {
	return nil, nil
}

func (m *mockAuthStore) ListAuditEvents(ctx context.Context, limit int) ([]auth.AuditEvent, error) {
	return nil, nil
}

func TestOverview_RenderSuccess(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	fc.Nodes = []k8s.NodeInfo{
		{
			Name:        "zlUbuntu",
			Arch:        "amd64",
			Ready:       true,
			AllocCPU:    "4",
			AllocMem:    "8Gi",
		},
	}
	fc.NodeMetrics = []k8s.NodeMetrics{
		{
			Name:     "zlUbuntu",
			CPUNano:  2000000000, // 2 cores (50%)
			MemBytes: 4294967296, // 4 GiB (50%)
		},
	}
	fc.Events = []k8s.EventInfo{
		{
			Namespace:      "portfolio-web",
			Type:           "Warning",
			Reason:         "FailedMount",
			Message:        "MountVolume.SetUp failed for volume",
			LastTimestamp:  time.Now(),
			InvolvedObject: "pod/portfolio-1234",
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "portfolio", Namespace: "portfolio-web"}}},
		Domain:    "nacfson.cloud",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "zlUbuntu") {
		t.Errorf("missing node name in rendered output")
	}
	if !strings.Contains(body, "50%") {
		t.Errorf("missing metrics percentage calculations")
	}
	if !strings.Contains(body, "MountVolume.SetUp failed") {
		t.Errorf("missing warning events in rendered output")
	}
}

func TestProjectMetrics_RenderSuccess(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	fc.Pods = map[string][]k8s.PodInfo{
		"portfolio-web/": {
			{
				Name:       "portfolio-1234",
				Namespace:  "portfolio-web",
				Phase:      "Running",
				LimitsCPU:  "1",
				LimitsMem:  "512Mi",
				CreatedAt:  time.Now().Add(-10 * time.Minute),
			},
		},
	}
	fc.PodMetrics = map[string][]k8s.PodMetrics{
		"portfolio-web": {
			{
				Name:      "portfolio-1234",
				Namespace: "portfolio-web",
				CPUNano:   250000000,  // 0.25 core (25%)
				MemBytes:  134217728,  // 128 MiB (25%)
			},
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "portfolio", Namespace: "portfolio-web"}}},
		Domain:    "nacfson.cloud",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory/projects/portfolio", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "portfolio-1234") {
		t.Errorf("missing pod name in project metrics page")
	}
	if !strings.Contains(body, "25%") {
		t.Errorf("missing expected resource percentage calculation")
	}
}

func TestTopology_RenderSuccess(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	fc.Ingresses = map[string][]k8s.IngressInfo{
		"portfolio-web": {
			{
				Name:        "portfolio-ing",
				Namespace:   "portfolio-web",
				Host:        "portfolio.nacfson.cloud",
				ServiceName: "portfolio-svc",
				ServicePort: 8080,
			},
		},
	}
	fc.Services = map[string][]k8s.ServiceInfo{
		"portfolio-web": {
			{
				Name:      "portfolio-svc",
				Namespace: "portfolio-web",
				ReadyPods: 1,
				TotalPods: 1,
			},
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "portfolio", Namespace: "portfolio-web"}}},
		Domain:    "nacfson.cloud",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory/topology", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "portfolio.nacfson.cloud") {
		t.Errorf("missing ingress host in topology page")
	}
	if !strings.Contains(body, "portfolio-svc") {
		t.Errorf("missing service name in topology page")
	}
}

func TestFragments_AutoRefresh(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "portfolio", Namespace: "portfolio-web"}}},
		Domain:    "nacfson.cloud",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// 1. Nodes Fragment
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory/fragments/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for nodes fragment, got %d", rec.Code)
	}

	// 2. Projects Fragment
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory/fragments/projects", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for projects fragment, got %d", rec.Code)
	}

	// 3. Warnings Fragment
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory/fragments/warnings", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for warnings fragment, got %d", rec.Code)
	}
}

func TestDegradesGracefully_NoMetrics(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	fc.Nodes = []k8s.NodeInfo{
		{
			Name: "test-node",
			Arch: "amd64",
		},
	}
	// Do not define fc.NodeMetrics (returns nil), simulates missing metrics-server

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "portfolio", Namespace: "portfolio-web"}}},
		Domain:    "nacfson.cloud",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/observatory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "N/A") {
		t.Errorf("expected graceful degradation fallback 'N/A' for metrics")
	}
}

func mustTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := handlers.NewTemplate()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	return tmpl
}
