package auth

import (
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
)

// Mailer sends plaintext emails via SMTP using environment configuration.
type Mailer struct {
	cookieDomain string
}

// NewMailer creates a Mailer. cookieDomain is used for the default From address.
func NewMailer(cookieDomain string) *Mailer {
	return &Mailer{cookieDomain: cookieDomain}
}

// Configured reports whether SMTP_HOST is set.
func (m *Mailer) Configured() bool {
	return os.Getenv("SMTP_HOST") != ""
}

// Send delivers a plaintext email using SMTP_* environment variables.
// SMTP_HOST, SMTP_PORT (default "25"), SMTP_USER, SMTP_PASS (fallback SMTP_PASSWORD),
// and SMTP_FROM (default "noreply@" + cookieDomain) match the previous inline sender.
func (m *Mailer) Send(to, subject, body string) error {
	smtpHost := os.Getenv("SMTP_HOST")
	if smtpHost == "" {
		return fmt.Errorf("SMTP_HOST is not configured")
	}
	smtpPort := os.Getenv("SMTP_PORT")
	if smtpPort == "" {
		smtpPort = "25"
	}
	addr := smtpHost
	if !strings.Contains(smtpHost, ":") {
		addr = net.JoinHostPort(smtpHost, smtpPort)
	}
	smtpUser := os.Getenv("SMTP_USER")
	var auth smtp.Auth
	if smtpUser != "" {
		smtpPass := os.Getenv("SMTP_PASS")
		if smtpPass == "" {
			smtpPass = os.Getenv("SMTP_PASSWORD")
		}
		auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	}
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = "noreply@" + m.cookieDomain
	}
	msg := []byte("To: " + to + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")
	return smtp.SendMail(addr, auth, from, []string{to}, msg)
}
