package auth

import "testing"

func TestValidateReturnTo(t *testing.T) {
	cookieDomain := "nacfson.cloud"
	allowedRedirects := []string{"http://localhost:8081", "https://customproject.com"}
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty returns default", "", "https://nacfson.cloud", false},
		{"allowed path", "/processes", "https://nacfson.cloud/processes", false},
		{"allowed host", "https://manager.nacfson.cloud/foo", "https://manager.nacfson.cloud/foo", false},
		{"allowed direct subdomain", "https://manager.nacfson.cloud", "https://manager.nacfson.cloud", false},
		{"rejected external host", "https://evil.com", "", true},
		{"rejected schemeless", "//evil.com", "", true},
		{"rejected suffix bypass", "https://evil.com.nacfson.cloud", "", true},
		{"rejected nested subdomain", "https://foo.bar.nacfson.cloud", "", true},
		{"rejected path traversal", "/../../etc/passwd", "", true},
		{"allowed whitelisted localhost url", "http://localhost:8081", "http://localhost:8081", false},
		{"allowed whitelisted external domain", "https://customproject.com/auth/callback", "https://customproject.com/auth/callback", false},
		{"rejected localhost port mismatch", "http://localhost:8082", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateReturnTo(tt.input, cookieDomain, allowedRedirects)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateReturnTo(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("validateReturnTo(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
