package mail

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
)

// A tiny SMTP server that records the message.
func fakeSMTP(t *testing.T, got chan<- string) string {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
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
			case strings.HasPrefix(line, "EHLO"):
				w("250-fake")
				w("250 AUTH PLAIN")
			case strings.HasPrefix(line, "AUTH"):
				w("235 ok")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				w("250 ok")
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
	return ln.Addr().String()
}

func TestSend(t *testing.T) {
	got := make(chan string, 1)
	addr := fakeSMTP(t, got)
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	m := New(Config{Host: host, Port: p, TLS: "none", Username: "u", Password: "p", From: "SuperSBC <noreply@example.com>"})
	if !m.Enabled() {
		t.Fatal("should be enabled")
	}
	if err := m.Send(context.Background(), "ops@example.com", "Reset your password", "link here"); err != nil {
		t.Fatal(err)
	}
	msg := <-got
	for _, want := range []string{"To: ops@example.com", "Subject: Reset your password", "link here"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if New(Config{}).Enabled() {
		t.Fatal("empty config must be disabled")
	}
	if envelope("Name <a@b.c>") != "a@b.c" || envelope("a@b.c") != "a@b.c" {
		t.Fatal("envelope")
	}
}
