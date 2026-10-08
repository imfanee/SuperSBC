package fsconfig

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/model"
)

func TestGatewayXML(t *testing.T) {
	u := "user1"
	p := "secret"
	c := model.Carrier{ID: uuid.New(), Name: "carrier-a", GatewayHost: "10.0.0.5", GatewayPort: 5080, Transport: "tcp", AuthUsername: &u, AuthPassword: &p, SIPOptionsPing: true, AllowedCodecs: []string{"PCMA", "PCMU"}}
	x := GatewayXML(c, "10.0.0.1")
	for _, want := range []string{`<gateway name="carrier-a">`, `value="10.0.0.5:5080;transport=tcp"`, `name="username" value="user1"`, `name="ping" value="30"`, `sbc_carrier_codecs" value="PCMA,PCMU"`} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %s in\n%s", want, x)
		}
	}
	if !strings.Contains(x, `name="from-domain" value="10.0.0.1"`) {
		t.Errorf("from-domain should be the node ip:\n%s", x)
	}
	if hash(x) != hash(GatewayXML(c, "10.0.0.1")) {
		t.Error("hash must ignore the timestamp line")
	}
}

func TestACLs(t *testing.T) {
	ips := []model.CustomerIP{{IPCIDR: "203.0.113.5/32"}, {IPCIDR: "198.51.100.0/24"}, {IPCIDR: "203.0.113.5/32"}}
	strict := CustomersACL(ips, []model.BannedIP{{IP: "198.51.100.7"}}, "strict")
	if !strings.Contains(strict, `default="deny"`) || strings.Count(strict, "<node") != 3 || !strings.Contains(strict, `type="deny" cidr="198.51.100.7/32"`) {
		t.Errorf("strict acl wrong:\n%s", strict)
	}
	lax := CustomersACL(ips, nil, "dialplan")
	if !strings.Contains(lax, `default="allow"`) {
		t.Errorf("dialplan acl wrong:\n%s", lax)
	}
	car := CarriersACL([]model.Carrier{{GatewayHost: "10.1.1.1"}, {GatewayHost: "sip.example.com"}})
	if strings.Count(car, "<node") != 1 || !strings.Contains(car, "10.1.1.1/32") {
		t.Errorf("carrier acl wrong:\n%s", car)
	}
}

func TestTLSSubjects(t *testing.T) {
	out := TLSSubjects([]model.Customer{
		{Status: "active", TLSSubject: "sip.acme.example, 203.0.113.9"},
		{Status: "active", TLSSubject: "trunk.beta.example"},
		{Status: "suspended", TLSSubject: "old.example"},
		{Status: "active", TLSSubject: " sip.acme.example "},
	})
	if !strings.Contains(out, `value="203.0.113.9,sip.acme.example,trunk.beta.example"`) || strings.Contains(out, "old.example") {
		t.Fatalf("subjects:\n%s", out)
	}
}

// A killgw that could not be delivered (ESL down) stays pending so the next
// render retries it; a name that came back needs no kill. Regression: a
// carrier renamed while ESL was briefly down stayed loaded in Sofia.
func TestPendingGatewayKills(t *testing.T) {
	r := New("", "dialplan", "10.0.0.1", nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.pendingKills["old-name"] = true
	if r.applyGatewayChanges(context.Background(), map[string]string{"new-name": "x"}) {
		t.Fatal("without ESL nothing can be applied")
	}
	if !r.pendingKills["old-name"] {
		t.Fatal("an undelivered kill must stay pending")
	}
	// the name reappearing (a rename undone) clears the pending kill
	r.esl = nil
	r.pendingKills = map[string]bool{"back-again": true}
	_ = r.applyGatewayChanges(context.Background(), map[string]string{"back-again": "x"})
	if r.pendingKills["back-again"] {
		t.Fatal("a gateway that exists again must not be killed")
	}
}
