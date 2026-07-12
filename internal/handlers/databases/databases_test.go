package databases

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
	mux.ServeHTTP(rec, adminRequest("GET", "/databases", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Databases") {
		t.Errorf("missing title")
	}
}

func TestListDatabaseError(t *testing.T) {
	deps := handlers.Deps{
		Provisioner: &errProvisioner{err: errors.New("list failed")},
	}
	h := New(deps)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, adminRequest("GET", "/databases", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Error") {
		t.Errorf("expected error body, got %q", rec.Body.String())
	}
}

type errProvisioner struct {
	err error
}

func (p *errProvisioner) ListDatabases(ctx context.Context) ([]string, error) {
	return nil, p.err
}

func mustTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := handlers.NewTemplate()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	return tmpl
}
