package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterCheckDoesNotRecord(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	defer l.Stop()
	ip := "10.0.0.1"
	email := "user@example.com"

	for i := 0; i < 10; i++ {
		if !l.check(ip, email) {
			t.Fatalf("check should remain under limit without recorded failures")
		}
	}
	if len(l.attempts) != 0 {
		t.Fatalf("check must not record attempts, got %d keys", len(l.attempts))
	}
}

func TestLoginLimiterFailedPathIncrements(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	defer l.Stop()
	ip := "10.0.0.2"
	email := "user@example.com"
	key := rateLimitKey{identifier: ip, email: email}

	l.record(ip, email)
	if len(l.attempts[key]) != 1 {
		t.Fatalf("expected 1 recorded attempt, got %d", len(l.attempts[key]))
	}
}

func TestLoginLimiterLocksOutAfterMaxFailures(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	defer l.Stop()
	ip := "10.0.0.3"
	email := "user@example.com"

	for i := 0; i < 3; i++ {
		if !l.check(ip, email) {
			t.Fatalf("check %d should be allowed before max failures", i+1)
		}
		l.record(ip, email)
	}
	if l.check(ip, email) {
		t.Fatal("check should deny after max failures")
	}
}

func TestLoginLimiterEvictsStaleKeys(t *testing.T) {
	l := NewLoginLimiter(3, 50*time.Millisecond)
	defer l.Stop()
	ip := "10.0.0.4"
	email := "user@example.com"
	key := rateLimitKey{identifier: ip, email: email}

	l.record(ip, email)
	if len(l.attempts) != 1 {
		t.Fatalf("expected 1 key after record, got %d", len(l.attempts))
	}
	old := l.attempts[key][0]

	time.Sleep(60 * time.Millisecond)

	if !l.check(ip, email) {
		t.Fatal("check after window should be allowed")
	}
	if len(l.attempts) != 0 {
		t.Fatalf("expected stale entry to be evicted, got %d keys", len(l.attempts))
	}

	l.record(ip, email)
	times := l.attempts[key]
	if len(times) != 1 {
		t.Fatalf("expected 1 fresh entry after re-record, got %d", len(times))
	}
	if !times[0].After(old) {
		t.Fatal("expected fresh timestamp to be after stale timestamp")
	}
}
