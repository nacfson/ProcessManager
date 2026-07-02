package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStore(t *testing.T) {
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect auth db: %v", err)
	}
	defer pool.Close()

	store := NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate auth db: %v", err)
	}

	// TRUNCATE tables to be hermetic
	_, err = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications CASCADE")
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	// Test case: CreateUser and verification status
	t.Run("CreateUser with verification status", func(t *testing.T) {
		u1, err := store.CreateUser(ctx, "unverified@example.com", "hash1", false, false)
		if err != nil {
			t.Fatalf("create unverified user: %v", err)
		}
		if u1.IsVerified {
			t.Errorf("expected is_verified = false, got true")
		}

		u2, err := store.CreateUser(ctx, "verified@example.com", "hash2", true, true)
		if err != nil {
			t.Fatalf("create verified user: %v", err)
		}
		if !u2.IsVerified {
			t.Errorf("expected is_verified = true, got false")
		}

		// Verify GetUserByEmail returns correct verification status
		fetched1, err := store.GetUserByEmail(ctx, "unverified@example.com")
		if err != nil {
			t.Fatalf("get user by email: %v", err)
		}
		if fetched1.IsVerified {
			t.Errorf("fetched user: expected is_verified = false, got true")
		}

		fetched2, err := store.GetUserByEmail(ctx, "verified@example.com")
		if err != nil {
			t.Fatalf("get user by email: %v", err)
		}
		if !fetched2.IsVerified {
			t.Errorf("fetched user: expected is_verified = true, got false")
		}
	})

	t.Run("VerifyUser", func(t *testing.T) {
		u, err := store.CreateUser(ctx, "to-verify@example.com", "hash", false, false)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if u.IsVerified {
			t.Fatalf("expected initial verification status false")
		}

		err = store.VerifyUser(ctx, u.ID)
		if err != nil {
			t.Fatalf("verify user: %v", err)
		}

		fetched, err := store.GetUserByEmail(ctx, "to-verify@example.com")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if !fetched.IsVerified {
			t.Errorf("expected user to be verified, but fetched as unverified")
		}
	})

	t.Run("EmailVerification CRUD", func(t *testing.T) {
		u, err := store.CreateUser(ctx, "verify-token@example.com", "hash", false, false)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}

		token := "some-secret-token"
		expiresAt := time.Now().Add(1 * time.Hour).Truncate(time.Microsecond) // PostgreSQL timestamp precision is typically microsecond

		// Create
		err = store.CreateEmailVerification(ctx, u.ID, token, expiresAt)
		if err != nil {
			t.Fatalf("create email verification: %v", err)
		}

		// Get
		ev, err := store.GetEmailVerification(ctx, token)
		if err != nil {
			t.Fatalf("get email verification: %v", err)
		}
		if ev == nil {
			t.Fatalf("expected to find email verification, got nil")
		}
		if ev.UserID != u.ID {
			t.Errorf("expected user ID %s, got %s", u.ID, ev.UserID)
		}
		if ev.Token != token {
			t.Errorf("expected token %s, got %s", token, ev.Token)
		}
		// Compare expiresAt, allowing timezone / precision differences
		if !ev.ExpiresAt.Equal(expiresAt) {
			t.Errorf("expected expiresAt %v, got %v", expiresAt, ev.ExpiresAt)
		}

		// Delete
		err = store.DeleteEmailVerification(ctx, token)
		if err != nil {
			t.Fatalf("delete email verification: %v", err)
		}

		// Get after delete
		evDeleted, err := store.GetEmailVerification(ctx, token)
		if err != nil {
			t.Fatalf("get email verification after delete: %v", err)
		}
		if evDeleted != nil {
			t.Errorf("expected nil after deletion, got %+v", evDeleted)
		}
	})

	t.Run("EmailVerification ON DELETE CASCADE", func(t *testing.T) {
		u, err := store.CreateUser(ctx, "cascade-verify@example.com", "hash", false, false)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}

		token := "cascade-token-123"
		expiresAt := time.Now().Add(1 * time.Hour).Truncate(time.Microsecond)

		err = store.CreateEmailVerification(ctx, u.ID, token, expiresAt)
		if err != nil {
			t.Fatalf("create email verification: %v", err)
		}

		// Delete the user
		_, err = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", u.ID)
		if err != nil {
			t.Fatalf("delete user: %v", err)
		}

		// Verify the verification token is gone
		ev, err := store.GetEmailVerification(ctx, token)
		if err != nil {
			t.Fatalf("get email verification: %v", err)
		}
		if ev != nil {
			t.Errorf("expected verification token to be deleted via CASCADE, but it still exists")
		}
	})

	t.Run("EmailVerification duplicate token error", func(t *testing.T) {
		u1, err := store.CreateUser(ctx, "dup1@example.com", "hash", false, false)
		if err != nil {
			t.Fatalf("create user 1: %v", err)
		}
		u2, err := store.CreateUser(ctx, "dup2@example.com", "hash", false, false)
		if err != nil {
			t.Fatalf("create user 2: %v", err)
		}

		token := "duplicate-token"
		expiresAt := time.Now().Add(1 * time.Hour).Truncate(time.Microsecond)

		err = store.CreateEmailVerification(ctx, u1.ID, token, expiresAt)
		if err != nil {
			t.Fatalf("create email verification: %v", err)
		}

		// Attempting to create another email verification with the same token should fail
		err = store.CreateEmailVerification(ctx, u2.ID, token, expiresAt)
		if err == nil {
			t.Errorf("expected error when creating email verification with duplicate token, got nil")
		}
	})
}
