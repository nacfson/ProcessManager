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
	_, err = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")
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

	t.Run("UpdateUserPassword and DeleteEmailVerificationsByUserID", func(t *testing.T) {
		u, err := store.CreateUser(ctx, "unverified-updates@example.com", "hash1", false, false)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}

		err = store.CreateEmailVerification(ctx, u.ID, "tok1", time.Now().Add(1*time.Hour))
		if err != nil {
			t.Fatalf("create email verification 1: %v", err)
		}
		err = store.CreateEmailVerification(ctx, u.ID, "tok2", time.Now().Add(1*time.Hour))
		if err != nil {
			t.Fatalf("create email verification 2: %v", err)
		}

		err = store.UpdateUserPassword(ctx, u.ID, "hash2")
		if err != nil {
			t.Fatalf("update password: %v", err)
		}

		fetched, err := store.GetUserByEmail(ctx, "unverified-updates@example.com")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		if fetched.PasswordHash != "hash2" {
			t.Errorf("expected password_hash 'hash2', got %q", fetched.PasswordHash)
		}

		err = store.DeleteEmailVerificationsByUserID(ctx, u.ID)
		if err != nil {
			t.Fatalf("delete verifications: %v", err)
		}

		ev1, err := store.GetEmailVerification(ctx, "tok1")
		if err != nil {
			t.Fatalf("get verification 1: %v", err)
		}
		if ev1 != nil {
			t.Errorf("expected verification tok1 to be deleted, got %+v", ev1)
		}

		ev2, err := store.GetEmailVerification(ctx, "tok2")
		if err != nil {
			t.Fatalf("get verification 2: %v", err)
		}
		if ev2 != nil {
			t.Errorf("expected verification tok2 to be deleted, got %+v", ev2)
		}
	})
}


func TestLoginTokens(t *testing.T) {
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	store := NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, _ = pool.Exec(ctx, "TRUNCATE users, sessions, projects, project_members, audit_events, email_verifications, login_tokens CASCADE")

	u, err := store.CreateUser(ctx, "token-user@example.com", "", false, false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, hash, err := NewAuthCode()
	if err != nil {
		t.Fatalf("auth code: %v", err)
	}
	_ = raw
	expires := time.Now().UTC().Add(time.Hour)
	if err := store.CreateLoginToken(ctx, u.ID, hash, expires); err != nil {
		t.Fatalf("create login token: %v", err)
	}

	latest, err := store.LatestLoginTokenCreatedAt(ctx, u.ID)
	if err != nil || latest == nil {
		t.Fatalf("expected latest created_at")
	}

	lt, err := store.ConsumeLoginToken(ctx, hash)
	if err != nil || lt == nil {
		t.Fatalf("consume: %v %#v", err, lt)
	}
	if lt.UserID != u.ID {
		t.Fatalf("user id mismatch")
	}
	lt2, err := store.ConsumeLoginToken(ctx, hash)
	if err != nil {
		t.Fatalf("second consume err: %v", err)
	}
	if lt2 != nil {
		t.Fatalf("expected nil on second consume")
	}

	raw2, hash2, _ := NewAuthCode()
	_ = raw2
	if err := store.CreateLoginToken(ctx, u.ID, hash2, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	lt3, err := store.ConsumeLoginToken(ctx, hash2)
	if err != nil || lt3 != nil {
		t.Fatalf("expired consume should be nil, got %#v err=%v", lt3, err)
	}
	n, err := store.DeleteExpiredLoginTokens(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected expired rows deleted, got %d", n)
	}

	u2, err := store.CreateUser(ctx, "token-user-2@example.com", "", false, false)
	if err != nil {
		t.Fatalf("create user2: %v", err)
	}
	_, hA, _ := NewAuthCode()
	_, hB, _ := NewAuthCode()
	_ = store.CreateLoginToken(ctx, u.ID, hA, time.Now().Add(time.Hour))
	_ = store.CreateLoginToken(ctx, u2.ID, hB, time.Now().Add(time.Hour))
	if err := store.DeleteLoginTokensByUserID(ctx, u.ID); err != nil {
		t.Fatalf("delete by user: %v", err)
	}
	if latest, _ := store.LatestLoginTokenCreatedAt(ctx, u.ID); latest != nil {
		t.Fatalf("expected no tokens for u")
	}
	if latest, _ := store.LatestLoginTokenCreatedAt(ctx, u2.ID); latest == nil {
		t.Fatalf("expected tokens for u2 to remain")
	}
}
