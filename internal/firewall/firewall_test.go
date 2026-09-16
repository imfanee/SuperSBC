package firewall

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imfanee/supersbc/internal/model"
)

func TestBuildAndScript(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	in10 := now.Add(10 * time.Minute)
	past := now.Add(-time.Minute)
	ex := Build(
		[]model.CustomerIP{{IPCIDR: "198.51.100.10/32"}, {IPCIDR: "203.0.113.0/24"}, {IPCIDR: "198.51.100.10/32"}},
		[]model.Carrier{
			{Status: "active", GatewayHost: "41.243.7.250", SignallingSources: []string{"41.243.7.0/28"}},
			{Status: "active", GatewayHost: "sip.carrier.example"},
			{Status: "disabled", GatewayHost: "192.0.2.1"},
		},
		[]model.BannedIP{{IP: "203.0.113.77", ExpiresAt: &in10}, {IP: "203.0.113.78"}, {IP: "203.0.113.79", ExpiresAt: &past}},
		now)
	if len(ex.Customers) != 2 || len(ex.Carriers) != 3 || len(ex.Banned) != 2 {
		t.Fatalf("export: %+v", ex)
	}
	back, err := Parse(ex.JSON())
	if err != nil || len(back.Carriers) != 3 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	resolve := func(h string) ([]string, error) {
		if h == "sip.carrier.example" {
			return []string{"192.0.2.50", "2001:db8::1"}, nil
		}
		return nil, errors.New("nx")
	}
	script, problems := Script(back, "inet sbc", resolve)
	for _, want := range []string{
		"flush set inet sbc customers\n",
		"add element inet sbc customers { 198.51.100.10/32, 203.0.113.0/24 }\n",
		"add element inet sbc carriers { 192.0.2.50, 41.243.7.0/28, 41.243.7.250 }\n",
		"add element inet sbc banned { 203.0.113.77 timeout 601s, 203.0.113.78 }\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	if len(problems) != 0 {
		t.Errorf("problems: %v", problems)
	}
	// unresolvable host is reported, not fatal; empty sets are flushed only
	back.Carriers = []string{"nx.example"}
	back.Banned = nil
	script, problems = Script(back, "inet sbc", resolve)
	if len(problems) != 1 || strings.Contains(script, "add element inet sbc carriers") || !strings.Contains(script, "flush set inet sbc banned\n") {
		t.Errorf("unresolved: %v\n%s", problems, script)
	}
}
