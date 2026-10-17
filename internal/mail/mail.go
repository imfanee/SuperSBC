// Package mail sends plain text e-mail over SMTP (D-70). It is used for
// password reset links; when no SMTP host is configured Mailer.Enabled is
// false and callers fall back to the admin generated link.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Config is the SMTP configuration.
type Config struct {
	Host     string // empty disables e-mail
	Port     int    // 587 (STARTTLS), 465 (implicit TLS) or 25
	Username string
	Password string
	From     string // "SuperSBC <noreply@example.com>"
	// TLS: "starttls" (default for 587), "tls" (implicit, default for 465), "none"
	TLS string
}

// Mailer sends messages.
type Mailer struct {
	cfg Config
}

// New creates a mailer; Enabled reports whether it can send.
func New(cfg Config) *Mailer {
	if cfg.TLS == "" {
		switch cfg.Port {
		case 465:
			cfg.TLS = "tls"
		case 25:
			cfg.TLS = "none"
		default:
			cfg.TLS = "starttls"
		}
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &Mailer{cfg: cfg}
}

// Enabled reports whether an SMTP host is configured.
func (m *Mailer) Enabled() bool { return m != nil && m.cfg.Host != "" }

// Message is one e-mail: several recipients, an optional HTML body beside the
// plain text one, and an optional From that overrides the configured sender
// (the watchdog sends as "Super SBC", D-79).
type Message struct {
	To      []string
	From    string
	Subject string
	Text    string
	HTML    string
}

// Send delivers one plain text message.
func (m *Mailer) Send(ctx context.Context, to, subject, body string) error {
	return m.SendMessage(ctx, Message{To: []string{to}, Subject: subject, Text: body})
}

// SendMessage delivers one message, as multipart/alternative when it carries
// an HTML body.
func (m *Mailer) SendMessage(ctx context.Context, msg Message) error {
	if !m.Enabled() {
		return fmt.Errorf("e-mail is not configured (SBC_SMTP_HOST)")
	}
	if len(msg.To) == 0 {
		return fmt.Errorf("no recipient")
	}
	to, subject, body := strings.Join(msg.To, ", "), msg.Subject, msg.Text
	from := msg.From
	if from == "" {
		from = m.cfg.From
	}
	if from == "" {
		from = "SuperSBC <noreply@" + m.cfg.Host + ">"
	}
	addr := net.JoinHostPort(m.cfg.Host, fmt.Sprint(m.cfg.Port))
	head := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
	}
	var raw string
	if msg.HTML == "" {
		raw = strings.Join(append(head, "Content-Type: text/plain; charset=UTF-8", "", body), "\r\n")
	} else {
		b := "sbc-" + fmt.Sprint(time.Now().UnixNano())
		raw = strings.Join(append(head,
			"Content-Type: multipart/alternative; boundary="+b,
			"",
			"--"+b,
			"Content-Type: text/plain; charset=UTF-8",
			"",
			body,
			"--"+b,
			"Content-Type: text/html; charset=UTF-8",
			"",
			msg.HTML,
			"--"+b+"--",
			"",
		), "\r\n")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	if m.cfg.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	defer func() { _ = c.Close() }()
	if m.cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		} else {
			return fmt.Errorf("smtp server %s does not offer STARTTLS (set SBC_SMTP_TLS=none to send in clear)", m.cfg.Host)
		}
	}
	if m.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(envelope(from)); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, rcpt := range msg.To {
		if err := c.Rcpt(envelope(rcpt)); err != nil {
			return fmt.Errorf("smtp to %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(raw)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// envelope extracts the bare address from "Name <addr>".
func envelope(s string) string {
	if i := strings.LastIndex(s, "<"); i >= 0 {
		return strings.TrimSuffix(s[i+1:], ">")
	}
	return strings.TrimSpace(s)
}
