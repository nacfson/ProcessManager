package auth

import (
	"net/http/httptest"
	"testing"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "missing"},
		{name: "bearer", header: "Bearer abc123", want: "abc123"},
		{name: "case insensitive", header: "bearer abc123", want: "abc123"},
		{name: "trims token", header: "Bearer   abc123  ", want: "abc123"},
		{name: "wrong scheme", header: "Basic abc123"},
		{name: "empty bearer", header: "Bearer "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if got := BearerToken(req); got != tt.want {
				t.Fatalf("BearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
