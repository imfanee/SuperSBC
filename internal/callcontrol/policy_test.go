package callcontrol

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/model"
)

func TestMediaModeFor(t *testing.T) {
	cases := []struct{ c, k, want string }{
		{"anchor", "bypass", "anchor"}, {"bypass", "anchor", "anchor"}, {"bypass", "bypass", "bypass"},
		{"bypass", "proxy", "proxy"}, {"proxy", "bypass", "proxy"}, {"", "bypass", "anchor"},
	}
	for _, x := range cases {
		if got := MediaModeFor(x.c, x.k); got != x.want {
			t.Errorf("%s+%s = %s want %s", x.c, x.k, got, x.want)
		}
	}
}

func TestDTMFAndSRTP(t *testing.T) {
	if v := dtmfVars("inband"); len(v) != 3 || v[0] != "dtmf_type=none" {
		t.Errorf("inband vars %v", v)
	}
	if v := dtmfVars("info"); v[0] != "dtmf_type=info" {
		t.Errorf("info vars %v", v)
	}
	if srtpVar("mandatory") != "mandatory" || srtpVar("off") != "false" || srtpVar("") != "false" {
		t.Error("srtp mapping")
	}
	vars, apps := ALegDTMF("inband")
	if vars["dtmf_type"] != "none" || len(apps) != 2 {
		t.Error("aleg inband")
	}
}

func TestPrivacyVars(t *testing.T) {
	cid, vars := PrivacyVars("anonymize", "15551234567", "1.2.3.4")
	if cid != "anonymous" || len(vars) != 2 {
		t.Errorf("anonymize: %s %v", cid, vars)
	}
	cid, vars = PrivacyVars("pass", "15551234567", "1.2.3.4")
	if cid != "15551234567" || !strings.Contains(vars[1], "sip:15551234567@1.2.3.4") {
		t.Errorf("pass: %s %v", cid, vars)
	}
	cid, vars = PrivacyVars("ignore", "15551234567", "1.2.3.4")
	if cid != "15551234567" || vars != nil {
		t.Errorf("ignore: %s %v", cid, vars)
	}
}

func TestApplyHeaderRules(t *testing.T) {
	cust := uuid.New()
	car := uuid.New()
	customerRules := []model.HeaderRule{
		{OwnerType: "customer", OwnerID: cust, Direction: "egress", Action: "add", Header: "X-Account", Value: "acct-{customer}", Priority: 10, Enabled: true},
		{OwnerType: "customer", OwnerID: cust, Direction: "egress", Action: "passthrough", Header: "X-Trace", Priority: 20, Enabled: true},
		{OwnerType: "customer", OwnerID: cust, Direction: "egress", Action: "add", Header: "X-Disabled", Value: "no", Priority: 30, Enabled: false},
		{OwnerType: "customer", OwnerID: cust, Direction: "response", Action: "add", Header: "X-SBC", Value: "{node_ip}", Priority: 5, Enabled: true},
	}
	carrierRules := []model.HeaderRule{
		{OwnerType: "carrier", OwnerID: car, Direction: "egress", Action: "remove", Header: "x-trace", Priority: 1, Enabled: true},
		{OwnerType: "carrier", OwnerID: car, Direction: "egress", Action: "add", Header: "X-Carrier-Key", Value: "k1,k2", Priority: 2, Enabled: true},
	}
	plan := ApplyHeaderRules(customerRules, carrierRules, HeaderContext{Customer: "acme", NodeIP: "10.0.0.1"})
	if len(plan.EgressVars) != 2 || plan.EgressVars[0] != "sip_h_X-Account=acct-acme" || plan.EgressVars[1] != `sip_h_X-Carrier-Key=k1\,k2` {
		t.Errorf("egress vars: %v", plan.EgressVars)
	}
	if len(plan.Passthrough) != 0 {
		t.Errorf("passthrough should be removed by the carrier rule: %v", plan.Passthrough)
	}
	if plan.ResponseVars["sip_rh_X-Sbc"] != "10.0.0.1" {
		t.Errorf("response vars: %v", plan.ResponseVars)
	}
	if canonicalHeader("p-asserted-identity") != "P-Asserted-Identity" {
		t.Error("canonical header")
	}
}
