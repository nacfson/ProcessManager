package main

import (
	"testing"
)

func TestAuthDSNRequired(t *testing.T) {
	if err := validateAuthDSN(""); err == nil {
		t.Fatal("expected error for empty auth DSN")
	}
}

func TestAuthDSNAccepted(t *testing.T) {
	if err := validateAuthDSN("postgres://user:pass@localhost/db"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
