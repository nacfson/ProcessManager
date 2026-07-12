package settings

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"processmanager/internal/auth"
	"processmanager/internal/handlers"
)

type fakeAuthStore struct{}

func (s *fakeAuthStore) CreateProject(_ context.Context, _, _, _, _ string, _, _ bool) (*auth.Project, error) {
	return nil, nil
}

func (s *fakeAuthStore) GetProjectByName(_ context.Context, _ string) (*auth.Project, error) {
	return nil, nil
}

func (s *fakeAuthStore) ListProjects(_ context.Context) ([]auth.Project, error) {
	return []auth.Project{
		{Name: "test-ns", Namespace: "test-ns"},
	}, nil
}

func (s *fakeAuthStore) ListUsers(_ context.Context) ([]auth.User, error) {
	return nil, nil
}

func (s *fakeAuthStore) ListAuditEvents(_ context.Context, _ int) ([]auth.AuditEvent, error) {
	return nil, nil
}

func TestSettingsRenders(t *testing.T) {
	deps := handlers.Deps{
		AuthStore: &fakeAuthStore{},
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Settings") {
		t.Errorf("missing title")
	}
	if !strings.Contains(body, "test-ns") {
		t.Errorf("missing namespace")
	}
	if !strings.Contains(body, "test.local") {
		t.Errorf("missing domain")
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
