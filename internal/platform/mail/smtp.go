// Package mail is the server-side email transport: a plain SMTP client (STARTTLS, implicit TLS or none for a local
// relay) implementing notifications' EmailProvider. It is only active when SMTP_HOST is configured.
package mail

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config selects the SMTP relay. TLS is "starttls" (default), "tls" (implicit TLS, usually port 465) or "none".
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      string
}

// FromEnv reads SMTP_HOST, SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD, SMTP_FROM and SMTP_TLS. It returns nil when
// SMTP_HOST is empty, meaning email delivery is not configured.
func FromEnv(getenv func(string) string) (*Config, error) {
	host := strings.TrimSpace(getenv("SMTP_HOST"))
	if host == "" {
		return nil, nil
	}
	c := &Config{Host: host, Port: 587, Username: getenv("SMTP_USERNAME"), Password: getenv("SMTP_PASSWORD"), From: strings.TrimSpace(getenv("SMTP_FROM")), TLS: strings.ToLower(strings.TrimSpace(getenv("SMTP_TLS")))}
	if p := strings.TrimSpace(getenv("SMTP_PORT")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("mail: SMTP_PORT %q is not a valid port", p)
		}
		c.Port = n
	}
	if c.TLS == "" {
		c.TLS = "starttls"
	}
	if c.TLS != "starttls" && c.TLS != "tls" && c.TLS != "none" {
		return nil, fmt.Errorf("mail: SMTP_TLS must be starttls, tls or none")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return nil, fmt.Errorf("mail: SMTP_FROM must be a valid address (e.g. billing@example.com): %w", err)
	}
	return c, nil
}

// SMTP sends mail through the configured relay.
type SMTP struct {
	cfg Config
	// tlsConfig is replaceable in tests.
	tlsConfig *tls.Config
}

func NewSMTP(cfg Config) *SMTP {
	return &SMTP{cfg: cfg, tlsConfig: &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}}
}

// SendEmail implements the notifications EmailProvider interface.
func (s *SMTP) SendEmail(ctx context.Context, to, subject, bodyHTML string, attachmentName string, attachment []byte) error {
	rcpt, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil {
		return fmt.Errorf("mail: %q is not a valid recipient address", to)
	}
	msg := buildMessage(s.cfg.From, rcpt.Address, subject, bodyHTML, attachmentName, attachment)
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	d := net.Dialer{Timeout: 20 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mail: connecting to %s: %w", addr, err)
	}
	if s.cfg.TLS == "tls" {
		conn = tls.Client(conn, s.tlsConfig)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: SMTP greeting: %w", err)
	}
	defer c.Close()
	if s.cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("mail: the server does not offer STARTTLS; set SMTP_TLS=none only for a trusted local relay")
		}
		if err := c.StartTLS(s.tlsConfig); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(plainAuth{user: s.cfg.Username, pass: s.cfg.Password}); err != nil {
			return fmt.Errorf("mail: authentication failed: %w", err)
		}
	}
	from, _ := mail.ParseAddress(s.cfg.From)
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mail: sender rejected: %w", err)
	}
	if err := c.Rcpt(rcpt.Address); err != nil {
		return fmt.Errorf("mail: recipient rejected: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: message rejected: %w", err)
	}
	return c.Quit()
}

// plainAuth is PLAIN auth without net/smtp's refusal to send credentials over an unencrypted connection, because the
// caller already decided (SMTP_TLS) whether the link is protected; it is only used after STARTTLS or implicit TLS unless
// the operator chose "none" for a local relay.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}
func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("mail: unexpected server challenge")
	}
	return nil, nil
}

func buildMessage(from, to, subject, html, attName string, att []byte) []byte {
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	h("To", to)
	h("Subject", mimeHeader(subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	if len(att) == 0 {
		h("Content-Type", "text/html; charset=utf-8")
		h("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n" + wrap76(base64.StdEncoding.EncodeToString([]byte(html))))
		return []byte(b.String())
	}
	const boundary = "rechvix-mixed-8f2c"
	h("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrap76(base64.StdEncoding.EncodeToString([]byte(html))))
	b.WriteString("\r\n--" + boundary + "\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"" + strings.ReplaceAll(attName, `"`, "") + "\"\r\n\r\n")
	b.WriteString(wrap76(base64.StdEncoding.EncodeToString(att)))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String())
}

func mimeHeader(s string) string {
	for _, r := range s {
		if r > 126 || r < 32 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	b.WriteString(s + "\r\n")
	return b.String()
}

// Getenv is os.Getenv, named so callers read FromEnv(mail.Getenv).
var Getenv = os.Getenv
