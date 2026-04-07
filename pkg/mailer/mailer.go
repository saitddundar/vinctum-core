package mailer

import (
	"fmt"
	"net/smtp"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	BaseURL  string // e.g. "https://vinctum.app" for verification links
}

type Mailer struct {
	cfg Config
}

func New(cfg Config) *Mailer {
	return &Mailer{cfg: cfg}
}

func (m *Mailer) SendVerification(to, token string) error {
	link := fmt.Sprintf("%s/verify?token=%s", m.cfg.BaseURL, token)

	subject := "Verify your Vinctum account"
	body := fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family: sans-serif; background: #0a0a0a; color: #e5e5e5; padding: 40px;">
  <div style="max-width: 480px; margin: 0 auto;">
    <h2 style="color: #fff;">Welcome to Vinctum</h2>
    <p>Click the button below to verify your email address:</p>
    <a href="%s" style="display: inline-block; background: #2563eb; color: #fff; padding: 12px 24px; border-radius: 6px; text-decoration: none; margin: 16px 0;">Verify Email</a>
    <p style="color: #888; font-size: 13px;">If you didn't create an account, you can safely ignore this email.</p>
    <p style="color: #888; font-size: 13px;">This link expires in 24 hours.</p>
  </div>
</body>
</html>`, link)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		m.cfg.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	return smtp.SendMail(addr, auth, m.cfg.From, []string{to}, []byte(msg))
}
