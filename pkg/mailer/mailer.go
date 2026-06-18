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
<head>
<meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background-color: #050505; color: #e5e5e5; margin: 0; padding: 40px 20px; line-height: 1.6;">
  <div style="max-width: 500px; margin: 0 auto; background: #111111; border: 1px solid #222222; border-radius: 16px; padding: 40px; text-align: center;">
    
    <div style="margin-bottom: 30px;">
      <h1 style="margin: 0; color: #ffffff; font-size: 26px; font-weight: 800; letter-spacing: 2px;">VINCTUM</h1>
      <div style="height: 3px; width: 40px; background: #3b82f6; margin: 12px auto 0; border-radius: 2px;"></div>
    </div>

    <h2 style="color: #ffffff; font-size: 20px; margin-bottom: 16px; font-weight: 600;">Initialize Your Node</h2>
    <p style="color: #a3a3a3; font-size: 15px; margin-bottom: 32px; padding: 0 10px;">Welcome to the decentralized network. To secure your identity and enable end-to-end encrypted transfers, please verify your email.</p>
    
    <a href="%s" style="display: inline-block; background: #3b82f6; color: #ffffff; font-weight: 600; font-size: 15px; padding: 14px 36px; border-radius: 8px; text-decoration: none;">Verify Identity</a>
    
    <div style="margin-top: 40px; padding-top: 24px; border-top: 1px solid #222222;">
      <p style="color: #666666; font-size: 12px; margin-bottom: 8px;">If you didn't initiate this request, safely ignore this transmission.</p>
      <p style="color: #666666; font-size: 12px; margin: 0;">Verification link expires in 24 hours.</p>
    </div>
  </div>
</body>
</html>`, link)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		m.cfg.From, to, subject, body)

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)

	return smtp.SendMail(addr, auth, m.cfg.From, []string{to}, []byte(msg))
}
