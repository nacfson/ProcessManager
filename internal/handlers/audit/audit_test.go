package audit

import (
	"context"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"processmanager/internal/auth"
	"processmanager/internal/handlers"
)

func adminRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	sess := &auth.Session{IsAdmin: true, Email: "admin@test.com"}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextUserKey, sess))
}

func TestListRenders(t *testing.T) {
	deps := handlers.Deps{
		Domain: "test.local",
		Tmpl:   mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, adminRequest("GET", "/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Activity") {
		t.Errorf("missing title")
	}
}

func TestListRendersErrorOnAuditFailure(t *testing.T) {
	store := &failingAuthStore{}
	deps := handlers.Deps{
		Domain:    "test.local",
		AuthStore: store,
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, adminRequest("GET", "/activity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Failed to load activity") {
		t.Errorf("expected error message in body, got %q", body)
	}
	if strings.Contains(body, "No activity yet.") {
		t.Errorf("expected empty state to be suppressed when error is shown")
	}
}

type failingAuthStore struct{}

func (s *failingAuthStore) CreateProject(_ context.Context, _, _, _, _ string, _, _ bool) (*auth.Project, error) {
	return nil, errors.New("not implemented")
}

func (s *failingAuthStore) GetProjectByName(_ context.Context, _ string) (*auth.Project, error) {
	return nil, nil
}

func (s *failingAuthStore) ListProjects(_ context.Context) ([]auth.Project, error) {
	return nil, nil
}

func (s *failingAuthStore) ListUsers(_ context.Context) ([]auth.User, error) {
	return nil, nil
}

func (s *failingAuthStore) ListAuditEvents(_ context.Context, _ int) ([]auth.AuditEvent, error) {
	return nil, errors.New("database unavailable")
}

func mustTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := handlers.NewTemplate()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	return tmpl
}
