package home

import (
	"context"
	"errors"
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
	"processmanager/internal/models"
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

func TestHomeRendersNodes(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	fc.Nodes = []k8s.NodeInfo{
		{
			Name:        "node-test-1",
			Arch:        "amd64",
			Ready:       true,
			CapacityCPU: "4",
			CapacityMem: "8Gi",
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "test-ns", Namespace: "test-ns"}}},
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "node-test-1") {
		t.Errorf("missing node-test-1 in rendered nodes")
	}
}

func TestHomeRendersProcessHealth(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	ctx := context.Background()

	// Healthy process (Ready == Total)
	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-healthy",
		Namespace: "test-ns",
		Ready:     2,
		Total:     2,
	}, "test.local")

	// Degraded process (Ready < Total)
	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-degraded",
		Namespace: "test-ns",
		Ready:     1,
		Total:     2,
	}, "test.local")

	// Down process (Ready == 0 && Total > 0)
	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-down",
		Namespace: "test-ns",
		Ready:     0,
		Total:     1,
	}, "test.local")

	// Scaled-down process (Ready == 0 && Total == 0) -> Should be "healthy"
	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-scaleddown",
		Namespace: "test-ns",
		Ready:     0,
		Total:     0,
	}, "test.local")

	// Process with 1/1 replicas but having restarts > 0 in its pods -> Should be "degraded"
	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-restarts-degraded",
		Namespace: "test-ns",
		Ready:     1,
		Total:     1,
	}, "test.local")
	fc.Pods = map[string][]k8s.PodInfo{
		"test-ns/proc-restarts-degraded": {
			{
				Name:     "proc-restarts-degraded-pod-1",
				Phase:    "Running",
				Restarts: 3,
			},
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "test-ns", Namespace: "test-ns"}}},
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/home/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Check status-dot classes are present as expected
	if !strings.Contains(body, "status-dot--healthy") {
		t.Errorf("expected status-dot--healthy class to be present")
	}
	if !strings.Contains(body, "status-dot--degraded") {
		t.Errorf("expected status-dot--degraded class to be present")
	}
	if !strings.Contains(body, "status-dot--down") {
		t.Errorf("expected status-dot--down class to be present")
	}
	if !strings.Contains(body, "proc-scaleddown") {
		t.Errorf("missing proc-scaleddown process name")
	}
	if !strings.Contains(body, "proc-restarts-degraded") {
		t.Errorf("missing proc-restarts-degraded process name")
	}

	// Verify that proc-restarts-degraded has degraded status
	idx := strings.Index(body, "/projects/test-ns/processes/proc-restarts-degraded/detail")
	if idx == -1 {
		t.Errorf("expected proc-restarts-degraded detail link to be present")
	} else {
		start := idx - 200
		if start < 0 {
			start = 0
		}
		snippet := body[start:idx]
		if !strings.Contains(snippet, "status-dot--degraded") {
			t.Errorf("expected proc-restarts-degraded to have degraded status, but status-dot--degraded not found near it")
		}
	}
}

func TestHomeRendersEventsFragment(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)

	fc.Events = []k8s.EventInfo{
		{
			Namespace:      "test-ns",
			Type:           "Warning",
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container",
			InvolvedObject: "Pod/proc-2-pod-1",
			LastTimestamp:  time.Now().Add(-10 * time.Minute),
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "test-ns", Namespace: "test-ns"}}},
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/home/events", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Back-off restarting failed container") {
		t.Errorf("missing event warning message")
	}
	if !strings.Contains(body, "Pod/proc-2-pod-1") {
		t.Errorf("missing event involved object")
	}
}

func TestHomeRenders(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	ctx := context.Background()

	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-1",
		Namespace: "test-ns",
		Ready:     1,
		Total:     1,
	}, "test.local")

	fc.Nodes = []k8s.NodeInfo{
		{
			Name:        "node-1",
			Arch:        "amd64",
			Ready:       true,
			CapacityCPU: "4",
			CapacityMem: "8Gi",
		},
	}

	fc.Events = []k8s.EventInfo{
		{
			Namespace:      "test-ns",
			Type:           "Warning",
			Reason:         "FailedMount",
			Message:        "MountVolume.SetUp failed",
			InvolvedObject: "Pod/proc-1-pod-1",
			LastTimestamp:  time.Now().Add(-5 * time.Minute),
		},
	}

	// Mock AuthStore with 3 projects
	as := &mockAuthStore{
		projects: []auth.Project{
			{ID: "1", Name: "Proj 1", Namespace: "test-ns"},
			{ID: "2", Name: "Proj 2", Namespace: "test-ns"},
			{ID: "3", Name: "Proj 3", Namespace: "test-ns"},
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		AuthStore: as,
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Dashboard") {
		t.Errorf("missing dashboard title")
	}
	if !strings.Contains(body, "node-1") {
		t.Errorf("missing node-1 name")
	}
	if !strings.Contains(body, "proc-1") {
		t.Errorf("missing proc-1 process name")
	}
	if !strings.Contains(body, "MountVolume.SetUp failed") {
		t.Errorf("missing warning event message")
	}
}

func TestHomeSurfacesListError(t *testing.T) {
	fc := &fake.Client{ErrListProcesses: errors.New("list failed")}
	deps := handlers.Deps{
		Client:    fc,
		AuthStore: &mockAuthStore{projects: []auth.Project{{Name: "test-ns", Namespace: "test-ns"}}},
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Failed to load dashboard data") {
		t.Errorf("expected error message in body, got %q", rec.Body.String())
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
