package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPAuthStore(t *testing.T) {
	token := "test-token"
	ctx := context.Background()

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		switch r.URL.Path {
		case "/api/internal/session":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			tokenHash := r.URL.Query().Get("token_hash")
			if tokenHash == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing token_hash"})
				return
			}
			if tokenHash == "invalid" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid session"})
				return
			}
			resp := map[string]any{
				"user_id":    "user-123",
				"email":     "user@example.com",
				"is_admin":   true,
				"expires_at": time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case "/api/internal/projects":
			if r.Method == http.MethodPost {
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				resp := map[string]any{
					"id":           "proj-123",
					"name":         req["name"],
					"namespace":    req["namespace"],
					"domain_host":  req["domain_host"],
					"type":         req["type"],
					"db_required":  req["db_required"],
					"auth_enabled": req["auth_enabled"],
					"created_at":   time.Now().Format(time.RFC3339Nano),
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(resp)
			} else if r.Method == http.MethodGet {
				resp := []map[string]any{
					{
						"id":           "proj-123",
						"name":         "project-1",
						"namespace":    "ns-1",
						"domain_host":  "domain-1",
						"type":         "simple",
						"db_required":  false,
						"auth_enabled": true,
						"created_at":   time.Now().Format(time.RFC3339Nano),
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}

		case "/api/internal/users":
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			resp := []map[string]any{
				{
					"id":         "user-123",
					"email":      "user@example.com",
					"is_admin":   true,
					"created_at": time.Now().Format(time.RFC3339Nano),
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case "/api/internal/audit-events":
			if r.Method == http.MethodPost {
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "created"})
			} else if r.Method == http.MethodGet {
				resp := []map[string]any{
					{
						"id":            "audit-123",
						"actor_email":   "user@example.com",
						"action":        "test-action",
						"resource_type": "test-resource",
						"resource_id":   "test-id",
						"created_at":    time.Now().Format(time.RFC3339Nano),
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			} else {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}

		default:
			if strings.HasPrefix(r.URL.Path, "/api/internal/projects/") {
				name := strings.TrimPrefix(r.URL.Path, "/api/internal/projects/")
				if name == "missing" {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				resp := map[string]any{
					"id":           "proj-123",
					"name":         name,
					"namespace":    "ns-1",
					"domain_host":  "domain-1",
					"type":         "simple",
					"db_required":  false,
					"auth_enabled": true,
					"created_at":   time.Now().Format(time.RFC3339Nano),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store := NewHTTPAuthStore(server.URL, token)

	t.Run("GetSessionByTokenHash Success", func(t *testing.T) {
		sess, err := store.GetSessionByTokenHash(ctx, "valid-hash")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sess == nil {
			t.Fatalf("expected session, got nil")
		}
		if sess.UserID != "user-123" || sess.Email != "user@example.com" || !sess.IsAdmin {
			t.Errorf("unexpected session fields: %+v", sess)
		}
	})

	t.Run("GetSessionByTokenHash Unauthorized (invalid session)", func(t *testing.T) {
		sess, err := store.GetSessionByTokenHash(ctx, "invalid")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sess != nil {
			t.Errorf("expected nil session, got %+v", sess)
		}
	})

	t.Run("GetSessionByTokenHash Unauthorized (invalid token)", func(t *testing.T) {
		badStore := NewHTTPAuthStore(server.URL, "wrong-token")
		sess, err := badStore.GetSessionByTokenHash(ctx, "valid-hash")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if sess != nil {
			t.Errorf("expected nil session on authentication failure, got %+v", sess)
		}
		expectedErr := "internal API authentication failed: verify AUTH_INTERNAL_TOKEN configuration"
		if err.Error() != expectedErr {
			t.Errorf("expected error %q, got %q", expectedErr, err.Error())
		}
	})


	t.Run("CreateProject Success", func(t *testing.T) {
		p, err := store.CreateProject(ctx, "new-project", "ns-new", "new.example.com", "simple", false, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Name != "new-project" || p.Namespace != "ns-new" || p.DomainHost != "new.example.com" || p.Type != "simple" || p.DBRequired || !p.AuthEnabled {
			t.Errorf("unexpected project fields: %+v", p)
		}
	})

	t.Run("ListProjects Success", func(t *testing.T) {
		projects, err := store.ListProjects(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(projects) != 1 {
			t.Fatalf("expected 1 project, got %d", len(projects))
		}
		if projects[0].Name != "project-1" {
			t.Errorf("expected project name project-1, got %s", projects[0].Name)
		}
	})

	t.Run("GetProjectByName Success", func(t *testing.T) {
		p, err := store.GetProjectByName(ctx, "project-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil {
			t.Fatalf("expected project, got nil")
		}
		if p.Name != "project-1" || p.Namespace != "ns-1" {
			t.Errorf("unexpected project fields: %+v", p)
		}
	})

	t.Run("GetProjectByName NotFound", func(t *testing.T) {
		p, err := store.GetProjectByName(ctx, "missing")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p != nil {
			t.Errorf("expected nil project, got %+v", p)
		}
	})

	t.Run("ListUsers Success", func(t *testing.T) {
		users, err := store.ListUsers(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(users) != 1 {
			t.Fatalf("expected 1 user, got %d", len(users))
		}
		if users[0].Email != "user@example.com" {
			t.Errorf("expected email user@example.com, got %s", users[0].Email)
		}
	})

	t.Run("ListAuditEvents Success", func(t *testing.T) {
		events, err := store.ListAuditEvents(ctx, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 audit event, got %d", len(events))
		}
		if events[0].Action != "test-action" {
			t.Errorf("expected action test-action, got %s", events[0].Action)
		}
	})

	t.Run("LogAuditEvent Success", func(t *testing.T) {
		err := store.LogAuditEvent(ctx, "user@example.com", "test-action", "test-resource", "test-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
