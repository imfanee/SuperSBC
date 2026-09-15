package fsconfig

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
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

func TestBanExport(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	in10 := now.Add(10 * time.Minute)
	past := now.Add(-time.Minute)
	out := BanExport([]model.BannedIP{
		{IP: "203.0.113.5", ExpiresAt: &in10},
		{IP: "203.0.113.6"},
		{IP: "203.0.113.7", ExpiresAt: &past},
	}, now)
	if !strings.Contains(out, "203.0.113.5 601\n") || !strings.Contains(out, "203.0.113.6 0\n") || strings.Contains(out, "203.0.113.7") {
		t.Fatalf("export:\n%s", out)
	}
}
