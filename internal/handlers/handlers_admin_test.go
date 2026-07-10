package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"processmanager/internal/auth"
)

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	called := false
	handler := RequireAdmin(nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	sess := &auth.Session{IsAdmin: true, Email: "admin@test.com"}
	ctx := context.WithValue(context.Background(), auth.ContextUserKey, sess)
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler was not called for admin user")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequireAdmin_RejectsNonAdmin(t *testing.T) {
	called := false
	handler := RequireAdmin(nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	sess := &auth.Session{IsAdmin: false, Email: "user@test.com"}
	ctx := context.WithValue(context.Background(), auth.ContextUserKey, sess)
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Error("handler should not be called for non-admin")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestRequireAdmin_RejectsUnauthenticated(t *testing.T) {
	called := false
	handler := RequireAdmin(nil, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Error("handler should not be called without auth context")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}
