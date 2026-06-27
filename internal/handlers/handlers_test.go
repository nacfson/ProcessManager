package handlers

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRenderSetsContentTypeAndSucceeds(t *testing.T) {
	tmpl := template.Must(template.New("ok").Parse("<p>{{.}}</p>"))
	rec := httptest.NewRecorder()
	Render(tmpl, rec, "ok", "hello")

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<p>hello</p>") {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}
}

func TestRenderLogsMissingContentBlockAndContinues(t *testing.T) {
	var logBuf strings.Builder
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	tmpl := template.Must(template.New("page").Parse("<html>{{.ContentHTML}}</html>"))
	rec := httptest.NewRecorder()
	Render(tmpl, rec, "page", map[string]any{})

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<html></html>") {
		t.Errorf("expected blank content HTML to be rendered, got %q", rec.Body.String())
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "content template pre-render failed") {
		t.Errorf("expected content pre-render error to be logged, got %q", logs)
	}
	if !strings.Contains(logs, "page-content") {
		t.Errorf("expected log to name the missing content template, got %q", logs)
	}
}

func TestRenderFragmentSetsContentTypeAndSucceeds(t *testing.T) {
	tmpl := template.Must(template.New("frag").Parse("<span>{{.}}</span>"))
	rec := httptest.NewRecorder()
	RenderFragment(tmpl, rec, "frag", "world")

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<span>world</span>") {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}
}

func TestRenderKeepsContentTypeOnTemplateError(t *testing.T) {
	// Template that always fails to execute.
	tmpl := template.Must(template.New("bad").Parse("{{.MissingMethod}}"))
	rec := httptest.NewRecorder()
	Render(tmpl, rec, "bad", struct{}{})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type preserved on error, got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("expected user-safe error body, got %q", rec.Body.String())
	}
}

func TestRenderFragmentKeepsContentTypeOnTemplateError(t *testing.T) {
	tmpl := template.Must(template.New("bad").Parse("{{.MissingMethod}}"))
	rec := httptest.NewRecorder()
	RenderFragment(tmpl, rec, "bad", struct{}{})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type preserved on error, got %q", ct)
	}
}

func TestRenderErrorReturnsUserSafeLabelAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	RenderError(rec, errors.New("database connection refused"), http.StatusBadRequest)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("expected text/plain content type, got %q", ct)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "Error" {
		t.Errorf("expected concise user-safe label 'Error', got %q", body)
	}
}

func TestRenderErrorHandlesNilError(t *testing.T) {
	rec := httptest.NewRecorder()
	RenderError(rec, nil, http.StatusInternalServerError)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "Error" {
		t.Errorf("expected concise user-safe label 'Error', got %q", body)
	}
}
