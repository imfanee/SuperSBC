package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imfanee/supersbc/internal/config"
)

// A ban is dropped by the firewall before the rules that let a customer's SIP
// in, so the addresses that must never reach the ban list matter as much as
// the ones that should (D-81).
func TestBannableIP(t *testing.T) {
	for _, ip := range []string{"45.76.1.1", "203.0.113.9", "2001:db8::1"} {
		if !bannableIP(ip) {
			t.Errorf("%s is a public address and should be bannable", ip)
		}
	}
	for _, ip := range []string{
		"127.0.0.1",      // the operator's own loopback
		"::1",            // the same over IPv6
		"172.29.0.5",     // the live docker bridge
		"10.0.0.7",       // a private network
		"192.168.1.10",   // a private network
		"169.254.1.1",    // link local
		"0.0.0.0",        // unspecified
		"",               // nothing at all
		"not-an-address", // a header someone made up
	} {
		if bannableIP(ip) {
			t.Errorf("%q must never be banned automatically", ip)
		}
	}
}

// Who a request is attributed to decides who gets throttled and who gets
// banned, so a client supplied header must never decide it (D-81).
func TestRemoteIPTrustsOnlyOurOwnProxies(t *testing.T) {
	req := func(peer, xff string) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = peer + ":40000"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct {
		name, peer, xff, want string
	}{
		{"direct caller, no header", "203.0.113.9", "", "203.0.113.9"},
		{"a caller forging the header is ignored", "203.0.113.9", "1.2.3.4", "203.0.113.9"},
		{"our nginx reports the real client", "172.29.0.3", "198.51.100.77", "198.51.100.77"},
		{"a forged chain through our nginx keeps the rightmost", "172.29.0.3", "1.2.3.4, 198.51.100.77", "198.51.100.77"},
		{"several of our own hops are skipped", "172.29.0.3", "198.51.100.77, 172.29.0.9", "198.51.100.77"},
		{"a malformed chain falls back to the peer", "172.29.0.3", "not-an-ip", "172.29.0.3"},
		{"loopback with no header", "127.0.0.1", "", "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := remoteIP(req(c.peer, c.xff)); got != c.want {
				t.Errorf("remoteIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSetTrustedProxies(t *testing.T) {
	original := trustedProxyNets
	defer func() { trustedProxyNets = original }()
	SetTrustedProxies("192.0.2.1")
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:40000"
	r.Header.Set("X-Forwarded-For", "198.51.100.77")
	if got := remoteIP(r); got != "198.51.100.77" {
		t.Errorf("a configured proxy should be believed: %q", got)
	}
	r.RemoteAddr = "172.29.0.3:40000" // no longer in the list
	if got := remoteIP(r); got != "172.29.0.3" {
		t.Errorf("an address outside the configured list must not be believed: %q", got)
	}
}

func TestOwnAddressIsNeverBanned(t *testing.T) {
	h := &Handler{Deps: Deps{Cfg: &config.Config{FSNodeIP: "51.38.218.42"}}}
	if !h.ownAddress("51.38.218.42") {
		t.Error("the advertised signalling address is this node")
	}
	if h.ownAddress("198.51.100.4") {
		t.Error("an address that is not ours should not be mistaken for it")
	}
	if h.ownAddress("") {
		t.Error("an empty address is not this node")
	}
}
