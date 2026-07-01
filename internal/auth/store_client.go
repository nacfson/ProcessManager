package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPAuthStore struct {
	client  *http.Client
	baseURL string
	token   string
}

func NewHTTPAuthStore(baseURL, token string) *HTTPAuthStore {
	return &HTTPAuthStore{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
	}
}

func (s *HTTPAuthStore) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	u := fmt.Sprintf("%s/api/internal/session?token_hash=%s", s.baseURL, url.QueryEscape(tokenHash))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		var errResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
			if msg, ok := errResp["error"]; ok && (msg == "invalid session" || msg == "missing session cookie") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("internal API authentication failed: verify AUTH_INTERNAL_TOKEN configuration")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return nil, fmt.Errorf("auth service error (status %d): %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	var sessResp struct {
		UserID    string    `json:"user_id"`
		Email     string    `json:"email"`
		IsAdmin   bool      `json:"is_admin"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &Session{
		UserID:    sessResp.UserID,
		Email:     sessResp.Email,
		IsAdmin:   sessResp.IsAdmin,
		ExpiresAt: sessResp.ExpiresAt,
		TokenHash: tokenHash,
	}, nil
}

func (s *HTTPAuthStore) CreateProject(ctx context.Context, name, namespace, domainHost, projType string, dbRequired, authEnabled bool) (*Project, error) {
	reqBody := struct {
		Name        string `json:"name"`
		Namespace   string `json:"namespace"`
		DomainHost  string `json:"domain_host"`
		Type        string `json:"type"`
		DBRequired  bool   `json:"db_required"`
		AuthEnabled bool   `json:"auth_enabled"`
	}{
		Name:        name,
		Namespace:   namespace,
		DomainHost:  domainHost,
		Type:        projType,
		DBRequired:  dbRequired,
		AuthEnabled: authEnabled,
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	u := s.baseURL + "/api/internal/projects"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return nil, fmt.Errorf("auth service error (status %d): %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	var pResp struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Namespace   string    `json:"namespace"`
		DomainHost  string    `json:"domain_host"`
		Type        string    `json:"type"`
		DBRequired  bool      `json:"db_required"`
		AuthEnabled bool      `json:"auth_enabled"`
		CreatedAt   time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &Project{
		ID:          pResp.ID,
		Name:        pResp.Name,
		Namespace:   pResp.Namespace,
		DomainHost:  pResp.DomainHost,
		Type:        pResp.Type,
		DBRequired:  pResp.DBRequired,
		AuthEnabled: pResp.AuthEnabled,
		CreatedAt:   pResp.CreatedAt,
	}, nil
}

func (s *HTTPAuthStore) ListProjects(ctx context.Context) ([]Project, error) {
	u := s.baseURL + "/api/internal/projects"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return nil, fmt.Errorf("auth service error (status %d): %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	var pResps []struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Namespace   string    `json:"namespace"`
		DomainHost  string    `json:"domain_host"`
		Type        string    `json:"type"`
		DBRequired  bool      `json:"db_required"`
		AuthEnabled bool      `json:"auth_enabled"`
		CreatedAt   time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pResps); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	projects := make([]Project, len(pResps))
	for i, pResp := range pResps {
		projects[i] = Project{
			ID:          pResp.ID,
			Name:        pResp.Name,
			Namespace:   pResp.Namespace,
			DomainHost:  pResp.DomainHost,
			Type:        pResp.Type,
			DBRequired:  pResp.DBRequired,
			AuthEnabled: pResp.AuthEnabled,
			CreatedAt:   pResp.CreatedAt,
		}
	}
	return projects, nil
}

func (s *HTTPAuthStore) ListUsers(ctx context.Context) ([]User, error) {
	u := s.baseURL + "/api/internal/users"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return nil, fmt.Errorf("auth service error (status %d): %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	var uResps []struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		IsAdmin   bool      `json:"is_admin"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&uResps); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	users := make([]User, len(uResps))
	for i, uResp := range uResps {
		users[i] = User{
			ID:        uResp.ID,
			Email:     uResp.Email,
			IsAdmin:   uResp.IsAdmin,
			CreatedAt: uResp.CreatedAt,
		}
	}
	return users, nil
}

func (s *HTTPAuthStore) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	u := fmt.Sprintf("%s/api/internal/audit-events?limit=%d", s.baseURL, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return nil, fmt.Errorf("auth service error (status %d): %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	var eResps []struct {
		ID           string    `json:"id"`
		ActorEmail   string    `json:"actor_email"`
		Action       string    `json:"action"`
		ResourceType string    `json:"resource_type"`
		ResourceID   string    `json:"resource_id"`
		CreatedAt    time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&eResps); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	events := make([]AuditEvent, len(eResps))
	for i, eResp := range eResps {
		events[i] = AuditEvent{
			ID:           eResp.ID,
			ActorEmail:   eResp.ActorEmail,
			Action:       eResp.Action,
			ResourceType: eResp.ResourceType,
			ResourceID:   eResp.ResourceID,
			CreatedAt:    eResp.CreatedAt,
		}
	}
	return events, nil
}

func (s *HTTPAuthStore) LogAuditEvent(ctx context.Context, actorEmail, action, resourceType, resourceID string) error {
	reqBody := struct {
		ActorEmail   string `json:"actor_email"`
		Action       string `json:"action"`
		ResourceType string `json:"resource_type"`
		ResourceID   string `json:"resource_id"`
	}{
		ActorEmail:   actorEmail,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	u := s.baseURL + "/api/internal/audit-events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		if msg, ok := errResp["error"]; ok {
			return fmt.Errorf("auth service error (status %d): %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	return nil
}
