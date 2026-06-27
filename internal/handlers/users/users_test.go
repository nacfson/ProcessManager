package users

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func mustTemplate(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := handlers.NewTemplate()
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	return tmpl
}
