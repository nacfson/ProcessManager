package main

import (
	"testing"
)

func TestAuthDSNRequired(t *testing.T) {
	if err := validateAuthDSN(""); err == nil {
		t.Fatal("expected error for empty auth DSN")
	}
}

func TestAuthDSNRejectsDisable(t *testing.T) {
	if err := validateAuthDSN("postgres://user:pass@localhost/db?sslmode=disable"); err == nil {
		t.Fatal("expected error for sslmode=disable")
	}
}

func TestAuthDSNRequiresExplicitTLS(t *testing.T) {
	if err := validateAuthDSN("postgres://user:pass@localhost/db"); err == nil {
		t.Fatal("expected error for missing sslmode")
	}
}

func TestAuthDSNAccepted(t *testing.T) {
	if err := validateAuthDSN("postgres://user:pass@localhost/db?sslmode=require"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAuthDSNAcceptsVerifyFull(t *testing.T) {
	if err := validateAuthDSN("postgres://user:pass@localhost/db?sslmode=verify-full"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
