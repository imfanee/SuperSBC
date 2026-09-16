//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// Per-customer concurrent call limit: delta-limited allows one call.
func TestM2_ConcurrentCallLimit(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "4000"})
	res := placeCall(t, "customer-delta", sc, "442071234567", 2)
	expectFinal(t, res, "200 OK")
	expectFinal(t, res, "480 Concurrent call limit")
	rows := cdrsSince(t, "172.28.0.104", since)
	var answered, limited int
	for _, r := range rows {
		switch str(r["disposition"]) {
		case "answered":
			answered++
		case "rejected_auth":
			if str(r["sip_final_reason"]) == "Concurrent call limit" {
				limited++
			}
		}
	}
	if answered != 1 || limited != 1 {
		t.Errorf("dispositions: %v", rows)
	}
	// the slot is free again afterwards
	res = placeCall(t, "customer-delta", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	reconcileOK(t)
}

// Per-customer CPS limit: epsilon-cps allows one call per second.
func TestM2_CPSLimit(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1000"})
	res := placeCall(t, "customer-epsilon", sc, "442071234567", 3)
	expectFinal(t, res, "200 OK")
	expectFinal(t, res, "503 CPS limit")
	rows := cdrsSince(t, "172.28.0.105", since)
	var answered, limited int
	for _, r := range rows {
		switch {
		case str(r["disposition"]) == "answered":
			answered++
		case str(r["sip_final_reason"]) == "CPS limit":
			limited++
		}
	}
	if answered < 1 || limited < 1 || answered+limited != 3 {
		t.Errorf("answered=%d limited=%d rows=%v", answered, limited, rows)
	}
}

// A carrier whose gateway does not respond is failed over (progress timeout
// or GATEWAY_DOWN) or skipped once FreeSWITCH marks it DOWN.
func TestM2_GatewayDownFailover(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-acme", sc, "447600900123", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "447600900123", since)
	expectField(t, cdr, "disposition", "answered")
	at := attempts(t, cdr)
	switch len(at) {
	case 1:
		if str(at[0]["carrier_name"]) != "carrier-answer" {
			t.Errorf("single attempt not via carrier-answer: %v", at)
		}
	case 2:
		if str(at[0]["carrier_name"]) != "carrier-down" || str(at[0]["classification"]) != "carrier_fault" {
			t.Errorf("attempt 1 = %v", at[0])
		}
		cause := str(at[0]["hangup_cause"])
		// ICMP unreachable becomes a synthetic 503 (NORMAL_TEMPORARY_FAILURE); a silent host times out.
		if cause != "PROGRESS_TIMEOUT" && cause != "GATEWAY_DOWN" && cause != "RECOVERY_ON_TIMER_EXPIRE" && cause != "NORMAL_TEMPORARY_FAILURE" {
			t.Errorf("unexpected cause for unreachable gateway: %s", cause)
		}
		if str(at[1]["carrier_name"]) != "carrier-answer" {
			t.Errorf("attempt 2 = %v", at[1])
		}
	default:
		t.Errorf("attempts = %v", at)
	}
}

// Topology hiding and header sanitisation on both legs.
func TestM2_HeaderSanitisation(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call_headers.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	for _, forbidden := range []string{"X-Customer-Secret", "X-Account-Code", "P-Asserted-Identity", "Remote-Party-ID", "CustomerPBX", "172.28.0.101"} {
		if strings.Contains(inv, forbidden) {
			t.Errorf("carrier received %q:\n%s", forbidden, inv)
		}
	}
	if !strings.Contains(inv, "User-Agent: SuperSBC") {
		t.Errorf("carrier did not see our User-Agent:\n%s", inv)
	}
	if !strings.Contains(inv, "From: \"15550001111\" <sip:15550001111@172.28.0.10>") && !strings.Contains(inv, "<sip:15550001111@172.28.0.10>") {
		t.Errorf("From should carry the ANI at the SBC address:\n%s", inv)
	}
	// responses to the customer carry no Remote-Party-ID / PAI and only the SBC address
	ok200 := ""
	for _, block := range strings.Split(res.Messages, "----------------------------------------------- ") {
		if strings.Contains(block, "SIP/2.0 200 OK") && strings.Contains(block, "CSeq: 1 INVITE") {
			ok200 = block
		}
	}
	if ok200 == "" {
		t.Fatal("no 200 OK to INVITE in customer log")
	}
	for _, forbidden := range []string{"Remote-Party-ID", "P-Asserted-Identity", "172.28.0.61"} {
		if strings.Contains(ok200, forbidden) {
			t.Errorf("customer received %q in 200 OK:\n%s", forbidden, ok200)
		}
	}
}

// The max call duration cap hangs the call up with ALLOTTED_TIMEOUT.
func TestM2_MaxCallDuration(t *testing.T) {
	restartAPI(t, map[string]string{"SBC_BILLING_MAX_CALL_DURATION": "12s"})
	defer restartAPI(t, nil)
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "40000"})
	res := placeCallArgs(t, "customer-acme", sc, "442071234567", 1, "-timeout", "60s")
	expectFinal(t, res, "200 OK")
	if !strings.Contains(res.Messages, "BYE sip:") {
		t.Errorf("customer never received a BYE from the SBC")
	}
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "hangup_cause", "ALLOTTED_TIMEOUT")
	bs := int(cdr["billsec"].(float64))
	if bs < 11 || bs > 14 {
		t.Errorf("billsec = %d, want about 12", bs)
	}
	expectMoney(t, cdr, "sell_price", "0.010000")
	reconcileOK(t)
}

// Strict ACL mode rejects unknown addresses in Sofia before the dialplan:
// a bare "403 Forbidden", no Lua, no API call, no CDR.
func TestM2_StrictACL(t *testing.T) {
	restartAPI(t, map[string]string{"SBC_ACL_MODE": "strict"})
	defer restartAPI(t, nil)
	since := time.Now()
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "403"})
	res := placeCallArgs(t, "customer-unknown", sc, "442071234567", 1, "-timeout", "5s")
	expectFinal(t, res, "403 Forbidden")
	for _, l := range res.FinalLines {
		if l == "403 IP not authorized" {
			t.Errorf("strict mode must not reach the Lua pipeline")
		}
	}
	if rows := sql(t, "SELECT call_uuid FROM cdrs WHERE src_ip = '172.28.0.199' AND start_time >= '"+since.UTC().Format(time.RFC3339)+"'"); len(rows) != 0 {
		t.Errorf("strict mode wrote a CDR for a dropped INVITE")
	}
	// known customers still work
	ok := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1000"})
	res = placeCall(t, "customer-acme", ok, "442071234567", 1)
	expectFinal(t, res, "200 OK")
}
