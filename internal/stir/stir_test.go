package stir

import (
	"context"
	"crypto/ecdsa"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestCert(t *testing.T, notBefore time.Time) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, pemData, err := NewTestCert(notBefore)
	if err != nil {
		t.Fatal(err)
	}
	return key, pemData
}

func TestVerify(t *testing.T) {
	now := time.Now()
	key, certPEM := newTestCert(t, now.Add(-time.Hour))
	otherKey, _ := newTestCert(t, now.Add(-time.Hour))
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/missing.pem" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(certPEM)
	}))
	defer srv.Close()
	v, err := New(Config{AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return now }
	x5u := srv.URL + "/cert.pem"
	sign := func(k *ecdsa.PrivateKey, url, attest, orig, dest string, iat time.Time) string {
		s, err := Sign(k, url, attest, orig, dest, "uuid-1", iat)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	cases := []struct {
		name     string
		identity string
		caller   string
		called   string
		status   string
	}{
		{"none", "", "15551112222", "442071234567", StatusNone},
		{"verified", sign(key, x5u, "A", "+15551112222", "+442071234567", now), "15551112222", "442071234567", StatusVerified},
		{"verified with 00 prefix on the call", sign(key, x5u, "B", "15551112222", "442071234567", now.Add(-30*time.Second)), "15551112222", "442071234567", StatusVerified},
		{"wrong key", sign(otherKey, x5u, "A", "15551112222", "442071234567", now), "15551112222", "442071234567", StatusInvalid},
		{"stale", sign(key, x5u, "A", "15551112222", "442071234567", now.Add(-5*time.Minute)), "15551112222", "442071234567", StatusStale},
		{"orig mismatch", sign(key, x5u, "A", "15559999999", "442071234567", now), "15551112222", "442071234567", StatusMismatch},
		{"dest mismatch", sign(key, x5u, "A", "15551112222", "442079999999", now), "15551112222", "442071234567", StatusMismatch},
		{"missing cert", sign(key, srv.URL+"/missing.pem", "A", "15551112222", "442071234567", now), "15551112222", "442071234567", StatusNoCert},
		{"garbage", "not.a.jws;info=<https://x>;alg=ES256", "15551112222", "442071234567", StatusInvalid},
		{"https only", sign(key, "ftp://example.com/c.pem", "A", "15551112222", "442071234567", now), "15551112222", "442071234567", StatusNoCert},
	}
	for _, c := range cases {
		res := v.Verify(context.Background(), c.identity, c.caller, c.called)
		if res.Status != c.status {
			t.Errorf("%s: got %s (%s) want %s", c.name, res.Status, res.Error, c.status)
		}
		if c.status == StatusVerified && (res.Attest == "" || res.OrigID != "uuid-1") {
			t.Errorf("%s: attest/origid missing: %+v", c.name, res)
		}
	}
	if hits != 2 { // cert.pem once (cached), missing.pem once
		t.Errorf("x5u fetched %d times, want 2", hits)
	}
	// require mode codes
	if code, _ := (Result{Status: StatusNone}).RejectCode(); code != 428 {
		t.Errorf("none -> %d", code)
	}
	if code, _ := (Result{Status: StatusInvalid}).RejectCode(); code != 438 {
		t.Errorf("invalid -> %d", code)
	}
	// chain validation against a CA bundle rejects the self-signed cert of another key
	_, otherPEM := newTestCert(t, now.Add(-time.Hour))
	dir := t.TempDir()
	ca := dir + "/ca.pem"
	if err := os.WriteFile(ca, otherPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	vc, err := New(Config{AllowHTTP: true, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	vc.now = v.now
	if res := vc.Verify(context.Background(), sign(key, x5u, "A", "15551112222", "442071234567", now), "15551112222", "442071234567"); res.Status != StatusInvalid || !strings.Contains(res.Error, "chain") {
		t.Errorf("chain: %+v", res)
	}
	if err := os.WriteFile(ca, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	vc2, _ := New(Config{AllowHTTP: true, CAFile: ca})
	vc2.now = v.now
	if res := vc2.Verify(context.Background(), sign(key, x5u, "A", "15551112222", "442071234567", now), "15551112222", "442071234567"); res.Status != StatusVerified {
		t.Errorf("trusted chain: %+v", res)
	}
}
