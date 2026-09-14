//go:build e2e

package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestMain(m *testing.M) {
	// The stack must already be up and seeded (make e2e does this); wait for
	// the FreeSWITCH core to report ready before the first INVITE.
	waitFreeSWITCH()
	os.Exit(m.Run())
}

// (a) unknown IP gets 403 IP not authorized
func TestA_UnknownIP(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "403"})
	res := placeCall(t, "customer-unknown", sc, "442071234567", 1)
	expectFinal(t, res, "403 IP not authorized")
	cdr := lastCDR(t, "172.28.0.199", "442071234567", since)
	expectField(t, cdr, "disposition", "rejected_auth")
	expectField(t, cdr, "sip_final_code", "403")
	expectField(t, cdr, "sip_final_reason", "IP not authorized")
	expectField(t, cdr, "billsec", "0")
}

// (b) known IP with zero balance gets 402 Not enough funds
func TestB_ZeroBalance(t *testing.T) {
	since := time.Now()
	before, _ := balance(t, "beta")
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "402"})
	res := placeCall(t, "customer-beta", sc, "442071234567", 1)
	expectFinal(t, res, "402 Not enough funds")
	cdr := lastCDR(t, "172.28.0.102", "442071234567", since)
	expectField(t, cdr, "disposition", "rejected_balance")
	expectField(t, cdr, "sip_final_reason", "Not enough funds")
	after, reserved := balance(t, "beta")
	if !after.Equal(before) || !reserved.IsZero() {
		t.Errorf("balance changed: before %s after %s reserved %s", before, after, reserved)
	}
}

// (c) successful call via primary carrier: CDR correct, balance charged exactly, reservation released
func TestC_AnsweredPrimary(t *testing.T) {
	since := time.Now()
	before, _ := balance(t, "acme")
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "3000"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	if res.ExitCode != 0 {
		t.Errorf("sipp exit %d", res.ExitCode)
	}
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "sip_final_code", "200")
	expectField(t, cdr, "called_number", "442071234567")
	if bs := str(cdr["billsec"]); bs != "3" && bs != "4" {
		t.Errorf("billsec = %s want 3 (or 4)", bs)
	}
	expectField(t, cdr, "sell_billed_seconds", "60")   // 60/60 increments
	expectMoney(t, cdr, "sell_price", "0.010000")      // 0.01/min UK fixed
	expectMoney(t, cdr, "cost", "0.006000")            // carrier-answer buys UK at 0.006
	expectMoney(t, cdr, "reserved_amount", "0.050000") // 5 min at 0.01
	expectMoney(t, cdr, "released_amount", "0.050000")
	expectMoney(t, cdr, "charged_amount", "0.010000")
	expectField(t, cdr, "media_mode", "relay")
	if len(attempts(t, cdr)) != 1 {
		t.Errorf("attempts = %v", cdr["attempts"])
	}
	if cdr["pdd_ms"] == nil {
		t.Errorf("pdd_ms missing")
	}
	after, reserved := balance(t, "acme")
	if want := before.Sub(decimal.RequireFromString("0.01")); !after.Equal(want) {
		t.Errorf("balance: before %s after %s want %s", before, after, want)
	}
	if !reserved.IsZero() {
		t.Errorf("reserved not released: %s", reserved)
	}
	if n := activeCalls(t); n != 0 {
		t.Errorf("active_calls not empty: %d", n)
	}
	reconcileOK(t)
}

// (d) primary returns 503, second carrier answers, attempts shows both
func TestD_Failover(t *testing.T) {
	since := time.Now()
	before, _ := balance(t, "acme")
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "2000"})
	res := placeCall(t, "customer-acme", sc, "447700900123", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "447700900123", since)
	expectField(t, cdr, "disposition", "answered")
	expectMoney(t, cdr, "sell_price", "0.020000") // UK mobile 0.02
	expectMoney(t, cdr, "cost", "0.015000")       // carrier-answer 447 at 0.015
	expectField(t, cdr, "failover_depth", "1")
	at := attempts(t, cdr)
	if len(at) != 2 {
		t.Fatalf("attempts = %d: %v", len(at), cdr["attempts"])
	}
	if str(at[0]["sip_code"]) != "503" || str(at[0]["classification"]) != "carrier_fault" || str(at[0]["carrier_name"]) != "carrier-503" {
		t.Errorf("attempt 1 = %v", at[0])
	}
	if str(at[1]["classification"]) != "answered" || str(at[1]["carrier_name"]) != "carrier-answer" {
		t.Errorf("attempt 2 = %v", at[1])
	}
	after, _ := balance(t, "acme")
	if want := before.Sub(decimal.RequireFromString("0.02")); !after.Equal(want) {
		t.Errorf("balance: before %s after %s want %s", before, after, want)
	}
	reconcileOK(t)
}

// (e) primary returns 404, no failover, customer receives 404
func TestE_NumberFaultNoFailover(t *testing.T) {
	since := time.Now()
	before, _ := balance(t, "acme")
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "404"})
	res := placeCall(t, "customer-acme", sc, "447900900123", 1)
	expectFinal(t, res, "404 Not Found")
	cdr := lastCDR(t, "172.28.0.101", "447900900123", since)
	expectField(t, cdr, "disposition", "failed")
	expectField(t, cdr, "sip_final_code", "404")
	at := attempts(t, cdr)
	if len(at) != 1 || str(at[0]["classification"]) != "number_fault" {
		t.Errorf("attempts = %v", cdr["attempts"])
	}
	expectMoney(t, cdr, "sell_price", "0")
	expectMoney(t, cdr, "released_amount", "0.100000")
	after, reserved := balance(t, "acme")
	if !after.Equal(before) || !reserved.IsZero() {
		t.Errorf("balance changed: before %s after %s reserved %s", before, after, reserved)
	}
}

// (f) all carriers 503, customer receives 503, balance unchanged
func TestF_AllCarriersFail(t *testing.T) {
	since := time.Now()
	before, _ := balance(t, "acme")
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "503"})
	res := placeCall(t, "customer-acme", sc, "447800900123", 1)
	expectFinal(t, res, "503 Service Unavailable")
	cdr := lastCDR(t, "172.28.0.101", "447800900123", since)
	expectField(t, cdr, "disposition", "failed")
	expectField(t, cdr, "sip_final_code", "503")
	at := attempts(t, cdr)
	if len(at) != 2 {
		t.Fatalf("attempts = %v", cdr["attempts"])
	}
	for _, a := range at {
		if str(a["sip_code"]) != "503" || str(a["classification"]) != "carrier_fault" {
			t.Errorf("attempt = %v", a)
		}
	}
	after, reserved := balance(t, "acme")
	if !after.Equal(before) || !reserved.IsZero() {
		t.Errorf("balance changed: before %s after %s reserved %s", before, after, reserved)
	}
	reconcileOK(t)
}

// (g) two concurrent calls from one customer with balance for only one: the second gets 402
func TestG_ConcurrentReservation(t *testing.T) {
	since := time.Now()
	before, _ := balance(t, "gamma") // 0.08: one reservation of 0.05 fits, two do not
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "4000"})
	res := placeCall(t, "customer-gamma", sc, "442071234567", 2)
	expectFinal(t, res, "200 OK")
	expectFinal(t, res, "402 Not enough funds")
	rows := cdrsSince(t, "172.28.0.103", since)
	if len(rows) != 2 {
		t.Fatalf("expected 2 CDRs, got %d: %v", len(rows), rows)
	}
	var answered, rejected int
	for _, r := range rows {
		switch str(r["disposition"]) {
		case "answered":
			answered++
		case "rejected_balance":
			rejected++
		}
	}
	if answered != 1 || rejected != 1 {
		t.Errorf("dispositions: %v", rows)
	}
	after, reserved := balance(t, "gamma")
	if want := before.Sub(decimal.RequireFromString("0.01")); !after.Equal(want) {
		t.Errorf("balance: before %s after %s want %s", before, after, want)
	}
	if !reserved.IsZero() {
		t.Errorf("reserved not released: %s", reserved)
	}
	reconcileOK(t)
}

// Extra: no route and no rate rejections use the exact reason phrases.
func TestH_NoRouteNoRate(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "503"})
	res := placeCall(t, "customer-acme", sc, "33123456789", 1) // rate exists, route has no carriers
	expectFinal(t, res, "503 No route")
	cdr := lastCDR(t, "172.28.0.101", "33123456789", since)
	expectField(t, cdr, "disposition", "rejected_route")
	expectMoney(t, cdr, "released_amount", "0.075000")

	sc404 := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "404"})
	res = placeCall(t, "customer-acme", sc404, "99123456789", 1) // no selling rate
	expectFinal(t, res, "404 No rate for destination")
	cdr = lastCDR(t, "172.28.0.101", "99123456789", since)
	expectField(t, cdr, "disposition", "rejected_route")

	sc484 := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "484"})
	res = placeCall(t, "customer-acme", sc484, "12", 1) // malformed
	expectFinal(t, res, "484 Address Incomplete")
	reconcileOK(t)
}
