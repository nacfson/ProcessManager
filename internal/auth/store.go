package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists auth platform data in the shared auth DB.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a Store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Migrate creates the auth schema if it does not exist.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return err
	}

	// Migration logic: add is_verified if not present
	alterQuery := `
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 
				FROM information_schema.columns 
				WHERE table_name='users' AND column_name='is_verified'
			) THEN
				ALTER TABLE users ADD COLUMN is_verified BOOLEAN NOT NULL DEFAULT true;
			END IF;
		END
		$$;
	`
	_, err = s.pool.Exec(ctx, alterQuery)
	return err
}

// User represents an account.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	IsAdmin      bool
	IsVerified   bool
	CreatedAt    time.Time
}

// UserExists returns true if at least one user exists.
func (s *Store) UserExists(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check user exists: %w", err)
	}
	return exists, nil
}

// CreateUser inserts a new user.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string, isAdmin bool, isVerified bool) (*User, error) {
	id := uuid.Must(uuid.NewRandom()).String()
	var u User
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, password_hash, is_admin, is_verified) VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, email, password_hash, is_admin, is_verified, created_at`,
		id, email, passwordHash, isAdmin, isVerified,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

// GetUserByEmail fetches a user by email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, is_admin, is_verified, created_at FROM users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.IsVerified, &u.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

// ListUsers returns all users ordered by creation time.
// It intentionally does not load password hashes.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, email, is_admin, is_verified, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.IsAdmin, &u.IsVerified, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// EmailVerification represents a pending user email verification.
type EmailVerification struct {
	UserID    string
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateEmailVerification inserts a new email verification token.
func (s *Store) CreateEmailVerification(ctx context.Context, userID, token string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO email_verifications (user_id, token, expires_at) VALUES ($1, $2, $3)`,
		userID, token, expiresAt)
	if err != nil {
		return fmt.Errorf("create email verification: %w", err)
	}
	return nil
}

// GetEmailVerification fetches an email verification by token.
func (s *Store) GetEmailVerification(ctx context.Context, token string) (*EmailVerification, error) {
	var ev EmailVerification
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, token, expires_at, created_at FROM email_verifications WHERE token = $1`,
		token,
	).Scan(&ev.UserID, &ev.Token, &ev.ExpiresAt, &ev.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get email verification: %w", err)
	}
	return &ev, nil
}

// DeleteEmailVerification removes an email verification token.
func (s *Store) DeleteEmailVerification(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM email_verifications WHERE token = $1`, token)
	if err != nil {
		return fmt.Errorf("delete email verification: %w", err)
	}
	return nil
}

// VerifyUser marks a user as verified.
func (s *Store) VerifyUser(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET is_verified = true WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("verify user: %w", err)
	}
	return nil
}
// DeleteUser removes a user by ID.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}

// UpdateUserPassword updates the user's password hash.
func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, userID)
	return err
}

// DeleteEmailVerificationsByUserID deletes all email verifications for a user.
func (s *Store) DeleteEmailVerificationsByUserID(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM email_verifications WHERE user_id = $1`, userID)
	return err
}

// LoginToken is a pending one-time sign-in token.
type LoginToken struct {
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateLoginToken inserts a new hashed magic-link token.
func (s *Store) CreateLoginToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO login_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("create login token: %w", err)
	}
	return nil
}

// ConsumeLoginToken atomically deletes and returns a non-expired login token.
// Returns (nil, nil) when the token is unknown, expired, or already consumed.
func (s *Store) ConsumeLoginToken(ctx context.Context, tokenHash string) (*LoginToken, error) {
	var lt LoginToken
	err := s.pool.QueryRow(ctx,
		`DELETE FROM login_tokens
		 WHERE token_hash = $1 AND expires_at > now()
		 RETURNING user_id, token_hash, expires_at, created_at`,
		tokenHash,
	).Scan(&lt.UserID, &lt.TokenHash, &lt.ExpiresAt, &lt.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("consume login token: %w", err)
	}
	return &lt, nil
}

// DeleteLoginTokensByUserID removes all outstanding login tokens for a user.
func (s *Store) DeleteLoginTokensByUserID(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM login_tokens WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete login tokens by user: %w", err)
	}
	return nil
}

// LatestLoginTokenCreatedAt returns the most recent login-token creation time for a user.
// Returns (nil, nil) when the user has no outstanding tokens.
func (s *Store) LatestLoginTokenCreatedAt(ctx context.Context, userID string) (*time.Time, error) {
	var createdAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT max(created_at) FROM login_tokens WHERE user_id = $1`,
		userID,
	).Scan(&createdAt)
	if err != nil {
		return nil, fmt.Errorf("latest login token created at: %w", err)
	}
	return createdAt, nil
}

// DeleteExpiredLoginTokens removes expired login tokens and returns the deleted row count.
func (s *Store) DeleteExpiredLoginTokens(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM login_tokens WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired login tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

// EnsureAdmin upserts a verified admin user with no password.
func (s *Store) EnsureAdmin(ctx context.Context, email string) error {
	existing, err := s.GetUserByEmail(ctx, email)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err := s.CreateUser(ctx, email, "", true, true)
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE users SET is_admin = true, is_verified = true WHERE id = $1`,
		existing.ID)
	return err
}

// CreateSession inserts a session.
func (s *Store) CreateSession(ctx context.Context, userID, tokenHash string, lifetime time.Duration) (*Session, error) {
	id := uuid.Must(uuid.NewRandom()).String()
	expiresAt := time.Now().UTC().Add(lifetime)
	var sess Session
	err := s.pool.QueryRow(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, token_hash, expires_at`,
		id, userID, tokenHash, expiresAt,
	).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &sess, nil
}

// GetSessionByTokenHash looks up a non-expired session and returns the user details.
func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx,
		`SELECT s.id, s.user_id, u.email, u.is_admin, s.token_hash, s.expires_at
		 FROM sessions s JOIN users u ON s.user_id = u.id
		 WHERE s.token_hash = $1 AND s.expires_at > now()`,
		tokenHash,
	).Scan(&sess.ID, &sess.UserID, &sess.Email, &sess.IsAdmin, &sess.TokenHash, &sess.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &sess, nil
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// Project represents a registered website project.
type Project struct {
	ID          string
	Name        string
	Namespace   string
	DomainHost  string
	Type        string
	DBRequired  bool
	AuthEnabled bool
	CreatedAt   time.Time
}

// CreateProject registers a project.
func (s *Store) CreateProject(ctx context.Context, name, namespace, domainHost, projType string, dbRequired, authEnabled bool) (*Project, error) {
	id := uuid.Must(uuid.NewRandom()).String()
	var p Project
	err := s.pool.QueryRow(ctx,
		`INSERT INTO projects (id, name, namespace, domain_host, type, db_required, auth_enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, name, namespace, domain_host, type, db_required, auth_enabled, created_at`,
		id, name, namespace, domainHost, projType, dbRequired, authEnabled,
	).Scan(&p.ID, &p.Name, &p.Namespace, &p.DomainHost, &p.Type, &p.DBRequired, &p.AuthEnabled, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	return &p, nil
}

// GetProjectByHost fetches a project by its domain host.
func (s *Store) GetProjectByHost(ctx context.Context, host string) (*Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, namespace, domain_host, type, db_required, auth_enabled, created_at FROM projects WHERE domain_host = $1`,
		host,
	).Scan(&p.ID, &p.Name, &p.Namespace, &p.DomainHost, &p.Type, &p.DBRequired, &p.AuthEnabled, &p.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get project: %w", err)
	}
	return &p, nil
}

// GetProjectByName fetches a project by name.
func (s *Store) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, namespace, domain_host, type, db_required, auth_enabled, created_at FROM projects WHERE name = $1`,
		name,
	).Scan(&p.ID, &p.Name, &p.Namespace, &p.DomainHost, &p.Type, &p.DBRequired, &p.AuthEnabled, &p.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get project by name: %w", err)
	}
	return &p, nil
}

// ListProjects returns all registered projects.
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, namespace, domain_host, type, db_required, auth_enabled, created_at FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Namespace, &p.DomainHost, &p.Type, &p.DBRequired, &p.AuthEnabled, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// AddProjectMember grants a role on a project.
func (s *Store) AddProjectMember(ctx context.Context, projectID, userID, role string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		projectID, userID, role)
	return err
}

// GetProjectRoles returns the roles a user has on a project.
func (s *Store) GetProjectRoles(ctx context.Context, projectID, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// AuditEvent represents a recorded action.
type AuditEvent struct {
	ID           string
	ActorEmail   string
	Action       string
	ResourceType string
	ResourceID   string
	CreatedAt    time.Time
}

// LogAuditEvent records an action.
func (s *Store) LogAuditEvent(ctx context.Context, actorEmail, action, resourceType, resourceID string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO audit_events (actor_email, action, resource_type, resource_id) VALUES ($1, $2, $3, $4)`,
		actorEmail, action, resourceType, resourceID)
	return err
}

// ListAuditEvents returns recent audit events.
func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, actor_email, action, resource_type, resource_id, created_at FROM audit_events ORDER BY created_at DESC LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()

	var events []AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.ActorEmail, &e.Action, &e.ResourceType, &e.ResourceID, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

const schema = `
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT,
    is_admin BOOLEAN NOT NULL DEFAULT false,
    is_verified BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    namespace TEXT NOT NULL,
    domain_host TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('simple','advanced')),
    db_required BOOLEAN NOT NULL DEFAULT false,
    auth_enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS project_members (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner','editor','viewer')),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_project_members_user ON project_members(user_id);

CREATE TABLE IF NOT EXISTS audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_email TEXT,
    action TEXT NOT NULL,
    resource_type TEXT,
    resource_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at DESC);

CREATE TABLE IF NOT EXISTS email_verifications (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_email_verifications_user ON email_verifications(user_id);

CREATE TABLE IF NOT EXISTS login_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_login_tokens_user ON login_tokens(user_id);
`
