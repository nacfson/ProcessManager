package auth

import "testing"

func TestShouldIssueCookieForRedirect(t *testing.T) {
	if !shouldIssueCookieForRedirect("https://health.nacfson.cloud/") {
		t.Fatal("https should issue cookie")
	}
	if shouldIssueCookieForRedirect("nacfsonhealth://login-callback") {
		t.Fatal("custom scheme must skip cookie")
	}
	if shouldIssueCookieForRedirect("projectpn://auth") {
		t.Fatal("custom scheme must skip cookie")
	}
}

func TestAppendSessionTokenToRedirect(t *testing.T) {
	got := appendSessionTokenToRedirect("nacfsonhealth://login-callback", "sess")
	if got != "nacfsonhealth://login-callback?token=sess" {
		t.Fatalf("got %q", got)
	}
	got = appendSessionTokenToRedirect("nacfsonhealth://login-callback?x=1", "sess")
	if got != "nacfsonhealth://login-callback?token=sess&x=1" && got != "nacfsonhealth://login-callback?x=1&token=sess" {
		t.Fatalf("got %q", got)
	}
}
