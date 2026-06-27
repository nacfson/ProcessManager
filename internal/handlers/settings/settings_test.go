package settings

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"processmanager/internal/handlers"
)

func TestSettingsRenders(t *testing.T) {
	deps := handlers.Deps{
		Namespace: "test-ns",
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
