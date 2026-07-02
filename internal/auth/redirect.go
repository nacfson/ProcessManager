package auth

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

func validateReturnTo(returnTo, cookieDomain string, allowedRedirects []string) (string, error) {
	normalizedURL := strings.ReplaceAll(strings.TrimSpace(returnTo), "\\", "/")

	u, err := url.Parse(normalizedURL)
	if err != nil {
		return "", fmt.Errorf("invalid redirect URL: %w", err)
	}

	for _, seg := range strings.Split(u.Path, "/") {
		if seg == ".." {
			return "", fmt.Errorf("redirect must not contain .. segments")
		}
	}

	for _, pattern := range allowedRedirects {
		patternNormalized := strings.ReplaceAll(strings.TrimSpace(pattern), "\\", "/")
		
		uReturn := u
		uPattern, err2 := url.Parse(patternNormalized)
		if err2 == nil {
			if strings.ToLower(uReturn.Scheme) == strings.ToLower(uPattern.Scheme) && 
			   strings.ToLower(uReturn.Host) == strings.ToLower(uPattern.Host) {
				rPath := uReturn.Path
				if rPath == "" {
					rPath = "/"
				}
				rPath = path.Clean(rPath)

				pPath := uPattern.Path
				if pPath == "" {
					pPath = "/"
				}
				pPath = path.Clean(pPath)
				
				rPath = strings.TrimSuffix(rPath, "/")
				pPath = strings.TrimSuffix(pPath, "/")
				
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
