package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewTemplateLoadsComponents(t *testing.T) {
	tmpl, err := NewTemplate()
	if err != nil {
		t.Fatalf("NewTemplate failed: %v", err)
	}
	if tmpl.Lookup("components/layout") == nil {
		t.Fatalf("layout template not loaded")
	}
}

func TestRequireAuthWithoutMiddleware(t *testing.T) {
	called := false
	h := RequireAuth(nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || !called {
		t.Errorf("expected handler to be called without middleware, got %d", rec.Code)
	}
}
