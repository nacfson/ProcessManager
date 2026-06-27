package projects

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	req := httptest.NewRequest("POST", "/projects", strings.NewReader(form.Encode()))
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
	req := httptest.NewRequest("POST", "/projects", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

type fakeAuthStore struct {
	created *auth.Project
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
	if s.created == nil {
		return nil, nil
	}
	return []auth.Project{*s.created}, nil
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
	req := httptest.NewRequest("POST", "/projects", strings.NewReader(form.Encode()))
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
