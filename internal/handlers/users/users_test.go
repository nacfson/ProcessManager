package users

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"processmanager/internal/auth"
	"processmanager/internal/handlers"
)

func TestListRenders(t *testing.T) {
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Users") {
		t.Errorf("missing title")
	}
}

type fakeAuthStore struct {
	ErrListUsers error
}

func (s *fakeAuthStore) CreateProject(_ context.Context, _ string, _ string, _ string, _ string, _ bool, _ bool) (*auth.Project, error) {
	return nil, nil
}

func (s *fakeAuthStore) ListProjects(_ context.Context) ([]auth.Project, error) {
	return nil, nil
}

func (s *fakeAuthStore) ListUsers(_ context.Context) ([]auth.User, error) {
	if s.ErrListUsers != nil {
		return nil, s.ErrListUsers
	}
	return nil, nil
}

func (s *fakeAuthStore) ListAuditEvents(_ context.Context, _ int) ([]auth.AuditEvent, error) {
	return nil, nil
}

func TestListSurfacesError(t *testing.T) {
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		AuthStore: &fakeAuthStore{ErrListUsers: errors.New("list failed")},
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Failed to load users") {
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
