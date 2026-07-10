package projects

import (
	"context"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		Namespace: "test-ns",
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Projects") {
		t.Errorf("missing title")
	}
}

func TestCreateValidationError(t *testing.T) {
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	form := url.Values{}
	form.Set("name", "")
	form.Set("namespace", "")
	form.Set("domainHost", "")

	rec := httptest.NewRecorder()
	req := adminRequest("POST", "/projects", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateRequiresAuthStore(t *testing.T) {
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	form := url.Values{}
	form.Set("name", "portfolio")
	form.Set("namespace", "nacfson-personal")
	form.Set("domainHost", "portfolio.nacfson.cloud")
	form.Set("type", "simple")

	rec := httptest.NewRecorder()
	req := adminRequest("POST", "/projects", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

type fakeAuthStore struct {
	created       *auth.Project
	ErrListProjects error
}

func (s *fakeAuthStore) CreateProject(_ context.Context, name, namespace, domainHost, projType string, dbRequired, authEnabled bool) (*auth.Project, error) {
	p := &auth.Project{
		ID:          "test-id",
		Name:        name,
		Namespace:   namespace,
		DomainHost:  domainHost,
		Type:        projType,
		DBRequired:  dbRequired,
		AuthEnabled: authEnabled,
	}
	s.created = p
	return p, nil
}

func (s *fakeAuthStore) ListProjects(_ context.Context) ([]auth.Project, error) {
	if s.ErrListProjects != nil {
		return nil, s.ErrListProjects
	}
	if s.created == nil {
		return nil, nil
	}
	return []auth.Project{*s.created}, nil
}

func (s *fakeAuthStore) ListUsers(_ context.Context) ([]auth.User, error) {
	return nil, nil
}

func (s *fakeAuthStore) ListAuditEvents(_ context.Context, _ int) ([]auth.AuditEvent, error) {
	return nil, nil
}

func TestListSurfacesError(t *testing.T) {
	store := &fakeAuthStore{ErrListProjects: errors.New("list failed")}
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		AuthStore: store,
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Failed to load projects") {
		t.Errorf("expected error message in body, got %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "No projects yet") {
		t.Errorf("empty state should not appear alongside an error")
	}
}

func TestListEmptyStateHasDeleteTarget(t *testing.T) {
	store := &fakeAuthStore{}
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		AuthStore: store,
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "id=\"project-list-empty\"") {
		t.Errorf("expected empty state to have id project-list-empty for OOB delete, got %q", body)
	}
	if !strings.Contains(body, "No projects yet") {
		t.Errorf("expected empty state message, got %q", body)
	}
}

func TestCreateHappyPath(t *testing.T) {
	store := &fakeAuthStore{}
	deps := handlers.Deps{
		Namespace: "test-ns",
		Domain:    "test.local",
		AuthStore: store,
		Tmpl:      mustTemplate(t),
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	form := url.Values{}
	form.Set("name", "portfolio")
	form.Set("namespace", "nacfson-personal")
	form.Set("domainHost", "portfolio.nacfson.cloud")
	form.Set("type", "advanced")
	form.Set("dbRequired", "on")
	// authEnabled is intentionally omitted to verify unchecked state.

	rec := httptest.NewRecorder()
	req := adminRequest("POST", "/projects", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected row fragment 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "portfolio") {
		t.Errorf("expected response body to contain project name")
	}
	if !strings.Contains(body, "hx-swap-oob=\"delete:#project-list-empty\"") {
		t.Errorf("expected response body to contain OOB delete for #project-list-empty")
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("expected no redirect, got Location %q", rec.Header().Get("Location"))
	}
	if store.created == nil {
		t.Fatalf("expected project to be created")
	}
	if store.created.Name != "portfolio" {
		t.Errorf("expected name portfolio, got %q", store.created.Name)
	}
	if store.created.Namespace != "nacfson-personal" {
		t.Errorf("expected namespace nacfson-personal, got %q", store.created.Namespace)
	}
	if store.created.DomainHost != "portfolio.nacfson.cloud" {
		t.Errorf("expected domain host portfolio.nacfson.cloud, got %q", store.created.DomainHost)
	}
	if store.created.Type != "advanced" {
		t.Errorf("expected type advanced, got %q", store.created.Type)
	}
	if !store.created.DBRequired {
		t.Errorf("expected db required to be true")
	}
	if store.created.AuthEnabled {
		t.Errorf("expected auth enabled to be false when checkbox is unchecked")
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
