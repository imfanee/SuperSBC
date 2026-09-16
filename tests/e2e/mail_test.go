//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts one message on 172.28.0.1:2525 (the dev .env points
// SBC_SMTP_HOST there) and returns its body.
func fakeSMTP(t *testing.T) (<-chan string, func()) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "172.28.0.1:2525")
	if err != nil {
		t.Skipf("cannot listen on 172.28.0.1:2525: %v", err)
	}
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		w("220 e2e ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					w("250 queued")
					got <- data.String()
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				w("250 e2e")
			case line == "DATA":
				inData = true
				w("354 go")
			case line == "QUIT":
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return got, func() { _ = ln.Close() }
}

// Password reset by e-mail (D-70): forgot -> mail with a link -> reset -> login.
func TestRoadmap_PasswordResetByEmail(t *testing.T) {
	got, stop := fakeSMTP(t)
	defer stop()
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	code, opts := a.do("GET", "/auth/options", nil)
	a.mustOK(code, opts, "options")
	if opts["email_reset"] != true {
		t.Fatalf("email reset should be enabled in dev: %v", opts)
	}
	// a fresh viewer account gets the mail
	code, u := a.do("POST", "/users", map[string]any{"email": "reset-me@example.com", "password": "InitialPassw0rd!", "role": "viewer"})
	if code != 201 && code != 409 {
		t.Fatalf("create user: %d %v", code, u)
	}
	anon := newAPIClient(t)
	code, _ = anon.do("POST", "/auth/forgot", map[string]any{"email": "reset-me@example.com"})
	if code != 202 {
		t.Fatalf("forgot: %d", code)
	}
	var body string
	select {
	case body = <-got:
	case <-time.After(15 * time.Second):
		t.Fatal("no reset e-mail received")
	}
	m := regexp.MustCompile(`reset-password\?token=([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "To: reset-me@example.com") {
		t.Fatalf("mail:\n%s", body)
	}
	code, _ = anon.do("POST", "/auth/reset", map[string]any{"token": m[1], "password": "BrandNewPassw0rd!"})
	if code != 204 {
		t.Fatalf("reset: %d", code)
	}
	code, _ = anon.login("reset-me@example.com", "BrandNewPassw0rd!")
	if code != 200 {
		t.Fatalf("login with new password: %d", code)
	}
	// unknown address: same 202, nothing leaks
	code, _ = anon.do("POST", "/auth/forgot", map[string]any{"email": "nobody@example.com"})
	if code != 202 {
		t.Fatalf("forgot unknown: %d", code)
	}
}
