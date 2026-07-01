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

	"processmanager/internal/handlers"
	"processmanager/internal/k8s"
	"processmanager/internal/k8s/fake"
	"processmanager/internal/models"
)

func TestHomeRenders(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	ctx := context.Background()

	// 1. Mock processes.
	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-1",
		Namespace: "test-ns",
		Ready:     1,
		Total:     1,
	}, "test.local")

	// 2. Mock nodes.
	fc.Nodes = []k8s.NodeInfo{
		{
			Name:        "node-1",
			Arch:        "amd64",
			Ready:       true,
			CapacityCPU: "4",
			CapacityMem: "8Gi",
		},
	}

	// 3. Mock pods (for restarts).
	fc.Pods = map[string][]k8s.PodInfo{
		"proc-1": {
			{
				Name:     "proc-1-pod-1",
				Restarts: 2,
			},
		},
	}

	// 4. Mock warning events.
	fc.Events = []k8s.EventInfo{
		{
			Type:           "Warning",
			Reason:         "FailedMount",
			Message:        "MountVolume.SetUp failed",
			InvolvedObject: "Pod/proc-1-pod-1",
			LastTimestamp:  time.Now().Add(-5 * time.Minute),
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		Namespace: "test-ns",
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

func TestHomeRendersProcessHealthFragment(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)
	ctx := context.Background()

	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-healthy",
		Namespace: "test-ns",
		Ready:     2,
		Total:     2,
	}, "test.local")

	_ = fc.CreateProcess(ctx, models.Process{
		Name:      "proc-degraded",
		Namespace: "test-ns",
		Ready:     1,
		Total:     2,
	}, "test.local")

	fc.Pods = map[string][]k8s.PodInfo{
		"proc-healthy": {
			{Name: "pod-h1", Restarts: 0},
		},
		"proc-degraded": {
			{Name: "pod-d1", Restarts: 5},
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		Namespace: "test-ns",
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
	if !strings.Contains(body, "proc-healthy") {
		t.Errorf("missing proc-healthy process name")
	}
	if !strings.Contains(body, "proc-degraded") {
		t.Errorf("missing proc-degraded process name")
	}
	if !strings.Contains(body, "5 restarts") {
		t.Errorf("missing restart count for degraded process")
	}
}

func TestHomeRendersEventsFragment(t *testing.T) {
	fc := fake.NewClient().(*fake.Client)

	fc.Events = []k8s.EventInfo{
		{
			Type:           "Warning",
			Reason:         "BackOff",
			Message:        "Back-off restarting failed container",
			InvolvedObject: "Pod/proc-2-pod-1",
			LastTimestamp:  time.Now().Add(-10 * time.Minute),
		},
	}

	deps := handlers.Deps{
		Client:    fc,
		Namespace: "test-ns",
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

func TestHomeSurfacesListError(t *testing.T) {
	fc := &fake.Client{ErrListProcesses: errors.New("list failed")}
	deps := handlers.Deps{
		Client:    fc,
		Namespace: "test-ns",
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
