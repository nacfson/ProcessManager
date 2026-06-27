package home

import (
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"processmanager/internal/handlers"
	"processmanager/internal/k8s/fake"
)

func TestHomeRenders(t *testing.T) {
	deps := handlers.Deps{
		Client:    fake.NewClient(),
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
	if !strings.Contains(rec.Body.String(), "Dashboard") {
		t.Errorf("missing dashboard title")
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
