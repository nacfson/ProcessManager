package auth

import (
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	ip := "10.0.0.1"
	email := "user@example.com"

	for i := 0; i < 3; i++ {
		if !l.allow(ip, email) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.allow(ip, email) {
		t.Fatal("4th attempt should be rate limited")
	}
}

func TestLoginLimiterEvictsStaleKeys(t *testing.T) {
	l := NewLoginLimiter(3, 50*time.Millisecond)
	ip := "10.0.0.2"
	email := "user@example.com"

	if !l.allow(ip, email) {
		t.Fatal("first attempt should be allowed")
	}
	if len(l.attempts) != 1 {
		t.Fatalf("expected 1 key after attempt, got %d", len(l.attempts))
	}
	old := l.attempts[ip+"|"+email][0]

	time.Sleep(60 * time.Millisecond)

	if !l.allow(ip, email) {
		t.Fatal("attempt after window should be allowed")
	}
	// The stale entry was evicted during filtering; only the fresh attempt remains.
	times := l.attempts[ip+"|"+email]
	if len(times) != 1 {
		t.Fatalf("expected 1 fresh entry after eviction, got %d", len(times))
	}
	if !times[0].After(old) {
		t.Fatal("expected stale timestamp to be replaced by a fresh one")
	}
}
