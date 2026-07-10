package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNewAuthCode(t *testing.T) {
	code, hash, err := NewAuthCode()
	if err != nil {
		t.Fatalf("NewAuthCode() error: %v", err)
	}
	if len(code) != 64 {
		t.Errorf("code length = %d, want 64", len(code))
	}
	if hash == "" {
		t.Error("hash is empty")
	}
	// Verify hash matches code
	h := sha256.Sum256([]byte(code))
	expectedHash := hex.EncodeToString(h[:])
	if hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHash)
	}

	// Verify uniqueness
	code2, _, err := NewAuthCode()
	if err != nil {
		t.Fatalf("second NewAuthCode() error: %v", err)
	}
	if code == code2 {
		t.Error("two codes should not be equal")
	}
}
