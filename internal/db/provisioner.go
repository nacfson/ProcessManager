package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Provisioner creates and drops per-project databases and users on a shared Postgres cluster.
type Provisioner struct {
	pool *pgxpool.Pool
}

// NewProvisioner creates a Provisioner using an admin connection pool.
func NewProvisioner(pool *pgxpool.Pool) *Provisioner {
	return &Provisioner{pool: pool}
}

// Provision creates a database and role for the given project name.
// It returns the database name, user name, password, and a DSN.
func (p *Provisioner) Provision(ctx context.Context, projectName string) (database, user, password, dsn string, err error) {
	database = sanitizeName(projectName)
	user = database
	password, err = generatePassword(32)
	if err != nil {
		return "", "", "", "", fmt.Errorf("generate password: %w", err)
	}

	// Use a single connection so CREATE USER and CREATE DATABASE can be issued safely.
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return "", "", "", "", fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// Create role if it does not exist.
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE USER %s WITH PASSWORD %s", pgx.Identifier{user}.Sanitize(), quoteLiteral(password))); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", "", "", "", fmt.Errorf("create user: %w", err)
		}
		// If the user exists, reset the password.
		if _, err := conn.Exec(ctx, fmt.Sprintf("ALTER USER %s WITH PASSWORD %s", pgx.Identifier{user}.Sanitize(), quoteLiteral(password))); err != nil {
			return "", "", "", "", fmt.Errorf("alter user password: %w", err)
		}
	}

	// Create database if it does not exist.
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", pgx.Identifier{database}.Sanitize(), pgx.Identifier{user}.Sanitize())); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return "", "", "", "", fmt.Errorf("create database: %w", err)
		}
	}

	// Lock down the database.
	if _, err := conn.Exec(ctx, fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC", pgx.Identifier{database}.Sanitize())); err != nil {
		return "", "", "", "", fmt.Errorf("revoke public: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("GRANT ALL ON DATABASE %s TO %s", pgx.Identifier{database}.Sanitize(), pgx.Identifier{user}.Sanitize())); err != nil {
		return "", "", "", "", fmt.Errorf("grant user: %w", err)
	}

	dsn = fmt.Sprintf("postgres://%s:%s@shared-app-db.nacfson-data.svc.cluster.local:5432/%s?sslmode=require", user, password, database)
	return database, user, password, dsn, nil
}

// ListDatabases returns names of databases managed by the provisioner.
func (p *Provisioner) ListDatabases(ctx context.Context) ([]string, error) {
	rows, err := p.pool.Query(ctx, "SELECT datname FROM pg_database WHERE datistemplate = false AND datname NOT IN ('postgres', 'template0', 'template1')")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// Deprovision drops the project's database and role.
func (p *Provisioner) Deprovision(ctx context.Context, projectName string) error {
	database := sanitizeName(projectName)
	user := database

	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// Terminate existing connections.
	if _, err := conn.Exec(ctx, fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid()",
		quoteLiteral(database),
	)); err != nil {
		return fmt.Errorf("terminate backends: %w", err)
	}

	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgx.Identifier{database}.Sanitize())); err != nil {
		return fmt.Errorf("drop database: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP USER IF EXISTS %s", pgx.Identifier{user}.Sanitize())); err != nil {
		return fmt.Errorf("drop user: %w", err)
	}
	return nil
}

func sanitizeName(name string) string {
	// Project names are expected to be DNS labels; ensure the DB identifier is safe.
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	// PostgreSQL identifiers are limited to 63 bytes.
	s := b.String()
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func generatePassword(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes)[:length], nil
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
