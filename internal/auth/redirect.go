package auth

import (
	"fmt"
	"net/url"
	"strings"
)

func validateReturnTo(returnTo, cookieDomain string, allowedRedirects []string) (string, error) {
	for _, pattern := range allowedRedirects {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		lowerReturnTo := strings.ToLower(strings.TrimSpace(returnTo))
		
		uReturn, err1 := url.Parse(lowerReturnTo)
		uPattern, err2 := url.Parse(pattern)
		if err1 == nil && err2 == nil {
			if uReturn.Scheme == uPattern.Scheme && uReturn.Host == uPattern.Host {
				rPath := strings.TrimSuffix(uReturn.Path, "/")
				pPath := strings.TrimSuffix(uPattern.Path, "/")
				if rPath == pPath || strings.HasPrefix(rPath, pPath+"/") {
					return returnTo, nil
				}
			}
		}
	}

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
		for _, seg := range strings.Split(u.Path, "/") {
			if seg == ".." {
				return "", fmt.Errorf("relative redirect must not contain .. segments")
			}
		}
		return defaultURL + u.Path, nil
	}

	if u.Scheme != "https" {
		return "", fmt.Errorf("redirect must use https")
	}

	host := strings.ToLower(u.Hostname())
	cookieDomain = strings.ToLower(strings.TrimPrefix(cookieDomain, "."))
	if host == cookieDomain {
		return u.String(), nil
	}

	// Only direct subdomains are allowed (exactly one additional label).
	hostLabels := strings.Split(host, ".")
	cookieLabels := strings.Split(cookieDomain, ".")
	if len(hostLabels) != len(cookieLabels)+1 {
		return "", fmt.Errorf("redirect host not allowed")
	}
	for i := range cookieLabels {
		if hostLabels[len(hostLabels)-1-i] != cookieLabels[len(cookieLabels)-1-i] {
			return "", fmt.Errorf("redirect host not allowed")
		}
	}

	return u.String(), nil
}
