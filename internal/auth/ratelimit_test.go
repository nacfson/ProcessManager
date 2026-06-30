package auth

import (
	"net"
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	ip := net.ParseIP("10.0.0.1")
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
