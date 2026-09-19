package sipcapture

import "testing"

func TestFirstLine(t *testing.T) {
	l, m := firstLine("INVITE sip:442071234567@172.28.0.10:5060 SIP/2.0\r\nVia: x\r\n")
	if l != "INVITE sip:442071234567@172.28.0.10:5060 SIP/2.0" || m != "INVITE" {
		t.Fatal(l, m)
	}
	l, m = firstLine("SIP/2.0 183 Session Progress\r\n")
	if m != "183" || l != "SIP/2.0 183 Session Progress" {
		t.Fatal(l, m)
	}
	if !sameHost("172.28.0.101", "172.28.0.101") || sameHost("", "x") || !sameHost("10.0.0.1", "10.0.0.1:5060") {
		t.Fatal("sameHost")
	}
	if New("").Enabled() {
		t.Fatal("empty url must be disabled")
	}
}
