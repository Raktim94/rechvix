package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal plaintext SMTP server capturing one message.
func fakeSMTP(t *testing.T) (port int, got func() (from, to, data string)) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var from, to, data string
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { c.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			up := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				w("250 fake")
			case strings.HasPrefix(up, "MAIL FROM:"):
				mu.Lock()
				from = strings.TrimSpace(line[10:])
				mu.Unlock()
				w("250 ok")
			case strings.HasPrefix(up, "RCPT TO:"):
				mu.Lock()
				to = strings.TrimSpace(line[8:])
				mu.Unlock()
				w("250 ok")
			case up == "DATA":
				w("354 go")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				mu.Lock()
				data = b.String()
				mu.Unlock()
				w("250 queued")
			case up == "QUIT":
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() (string, string, string) {
		mu.Lock()
		defer mu.Unlock()
		return from, to, data
	}
}

func TestSendEmailDeliversHTMLWithoutTLSOnLocalRelay(t *testing.T) {
	port, got := fakeSMTP(t)
	s := NewSMTP(Config{Host: "127.0.0.1", Port: port, From: "billing@example.com", TLS: "none"})
	if err := s.SendEmail(context.Background(), "Cliente <cliente@example.org>", "Factura FAC/1 — gracias", "<p>Hola</p>", "", nil); err != nil {
		t.Fatal(err)
	}
	from, to, data := got()
	if !strings.Contains(from, "billing@example.com") || !strings.Contains(to, "cliente@example.org") {
		t.Fatalf("envelope: %q %q", from, to)
	}
	if !strings.Contains(data, "Subject: =?UTF-8?B?") || !strings.Contains(data, "Content-Type: text/html") {
		t.Fatalf("headers: %s", data)
	}
	body := data[strings.Index(data, "\r\n\r\n")+4:]
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", ""))
	if err != nil || string(dec) != "<p>Hola</p>" {
		t.Fatalf("body %q (%v)", dec, err)
	}
}

func TestSendEmailRejectsBadRecipient(t *testing.T) {
	s := NewSMTP(Config{Host: "127.0.0.1", Port: 1, From: "a@b.co", TLS: "none"})
	if err := s.SendEmail(context.Background(), "not an address", "x", "y", "", nil); err == nil {
		t.Fatal("a malformed recipient must be refused before connecting")
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if c, err := FromEnv(get); c != nil || err != nil {
		t.Fatalf("no SMTP_HOST means not configured: %v %v", c, err)
	}
	env["SMTP_HOST"], env["SMTP_FROM"] = "smtp.example.com", "Billing <billing@example.com>"
	c, err := FromEnv(get)
	if err != nil || c.Port != 587 || c.TLS != "starttls" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	env["SMTP_PORT"] = strconv.Itoa(70000)
	if _, err := FromEnv(get); err == nil {
		t.Fatal("bad port must be refused")
	}
	env["SMTP_PORT"], env["SMTP_TLS"] = "465", "maybe"
	if _, err := FromEnv(get); err == nil {
		t.Fatal("bad TLS mode must be refused")
	}
	env["SMTP_TLS"], env["SMTP_FROM"] = "tls", "nope"
	if _, err := FromEnv(get); err == nil {
		t.Fatal("bad From must be refused")
	}
}

func TestAttachmentMessage(t *testing.T) {
	m := string(buildMessage("a@b.co", "c@d.co", "Hi", "<p>x</p>", "invoice.pdf", []byte("PDFDATA")))
	if !strings.Contains(m, "multipart/mixed") || !strings.Contains(m, `filename="invoice.pdf"`) || !strings.Contains(m, base64.StdEncoding.EncodeToString([]byte("PDFDATA"))) {
		t.Fatalf("attachment missing: %s", m)
	}
}
