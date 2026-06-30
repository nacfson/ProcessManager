package auth

import (
	"fmt"
	"net/url"
	"strings"
)

func validateReturnTo(returnTo, cookieDomain string) (string, error) {
	defaultURL := "https://" + cookieDomain
	if returnTo == "" {
		return defaultURL, nil
	}

	u, err := url.Parse(returnTo)
	if err != nil {
		return "", fmt.Errorf("invalid redirect URL: %w", err)
	}

	// Relative paths are allowed.
	if u.Scheme == "" && u.Host == "" {
		if !strings.HasPrefix(u.Path, "/") {
			return "", fmt.Errorf("relative redirect must start with /")
		}
		return defaultURL + u.Path, nil
	}

	if u.Scheme != "https" {
		return "", fmt.Errorf("redirect must use https")
	}

	host := strings.ToLower(u.Hostname())
	cookieDomain = strings.ToLower(strings.TrimPrefix(cookieDomain, "."))
	if host != cookieDomain && !strings.HasSuffix(host, "."+cookieDomain) {
		return "", fmt.Errorf("redirect host not allowed")
	}

	return u.String(), nil
}
