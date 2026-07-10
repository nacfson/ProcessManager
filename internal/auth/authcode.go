package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// NewAuthCode generates a short-lived, one-time-use authorization code.
// Returns the raw code (to send in the redirect) and its SHA-256 hash (for storage).
func NewAuthCode() (code string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate auth code: %w", err)
	}
	code = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(code))
	hash = hex.EncodeToString(sum[:])
	return code, hash, nil
}
