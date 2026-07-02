package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type mockSessionValidator struct {
	getSessionFunc func(ctx context.Context, tokenHash string) (*Session, error)
}

func (m *mockSessionValidator) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	if m.getSessionFunc != nil {
		return m.getSessionFunc(ctx, tokenHash)
	}
	return nil, nil
}

func TestMiddleware_RequireAuth(t *testing.T) {
	tests := []struct {
		name           string
		cookieValue    string
		getSessionFunc func(ctx context.Context, tokenHash string) (*Session, error)
		wantStatus     int
		wantRedirect   string
		wantBody       string
		checkContext   bool
	}{
		{
			name:        "No session cookie",
			cookieValue: "",
			wantStatus:  http.StatusFound,
			wantRedirect: "https://auth.example.com/login?rd=" +
				"https%3A%2F%2Fapp.example.com%2Fdashboard",
		},
		{
			name:        "Validator error",
			cookieValue: "some-token",
			getSessionFunc: func(ctx context.Context, tokenHash string) (*Session, error) {
				return nil, errors.New("db error")
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "Internal server error\n",
		},
		{
			name:        "Session not found",
			cookieValue: "some-token",
			getSessionFunc: func(ctx context.Context, tokenHash string) (*Session, error) {
				return nil, nil
			},
			wantStatus:   http.StatusFound,
			wantRedirect: "https://auth.example.com/login?rd=" +
				"https%3A%2F%2Fapp.example.com%2Fdashboard",
		},
		{
			name:        "Valid session",
			cookieValue: "some-token",
			getSessionFunc: func(ctx context.Context, tokenHash string) (*Session, error) {
				return &Session{
					ID:        "sess-123",
					UserID:    "user-456",
					Email:     "user@example.com",
					TokenHash: HashSessionToken("some-token"),
					ExpiresAt: time.Now().Add(time.Hour),
				}, nil
			},
			wantStatus:   http.StatusOK,
			checkContext: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := &mockSessionValidator{getSessionFunc: tt.getSessionFunc}
			
			// Note: This will compile only after NewMiddleware is updated to accept SessionValidator.
			mw := NewMiddleware(validator, "auth.example.com", "example.com")

			handler := mw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.checkContext {
					sess := UserContext(r.Context())
					if sess == nil {
						t.Error("Expected session in context, got nil")
					} else if sess.UserID != "user-456" {
						t.Errorf("Expected UserID 'user-456', got %q", sess.UserID)
					}
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "https://app.example.com/dashboard", nil)
			if tt.cookieValue != "" {
				req.AddCookie(&http.Cookie{
					Name:  SessionCookieName,
					Value: tt.cookieValue,
				})
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Expected status %d, got %d", tt.wantStatus, rec.Code)
			}

			if tt.wantRedirect != "" {
				loc := rec.Header().Get("Location")
				if loc != tt.wantRedirect {
					t.Errorf("Expected redirect location %q, got %q", tt.wantRedirect, loc)
				}
			}

			if tt.wantBody != "" {
				if rec.Body.String() != tt.wantBody {
					t.Errorf("Expected body %q, got %q", tt.wantBody, rec.Body.String())
				}
			}

			if tt.name == "Session not found" {
				cookies := rec.Result().Cookies()
				found := false
				for _, c := range cookies {
					if c.Name == SessionCookieName {
						found = true
						if c.MaxAge != -1 {
							t.Errorf("Expected cookie to be cleared (MaxAge = -1), got MaxAge = %d", c.MaxAge)
						}
					}
				}
				if !found {
					t.Error("Expected session cookie to be cleared on response")
				}
			}
		})
	}
}
