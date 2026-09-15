//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// Time-of-day routing window (D-61): a carrier outside its window is
// skipped in the simulator and never dialled.
func TestRoadmap_RoutingWindow(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	answer := findByName(t, a, "/carriers", "carrier-answer")
	c503 := findByName(t, a, "/carriers", "carrier-503")
	rg := findByName(t, a, "/route-groups", "default")
	code, routes := a.do("GET", "/route-groups/"+rg+"/routes?search=4477", nil)
	a.mustOK(code, routes, "routes")
	var routeID string
	for _, r := range items(routes) {
		if r["prefix"] == "4477" {
			routeID = r["id"].(string)
		}
	}
	// A window that is closed right now: one minute a week that is not this minute.
	now := time.Now().UTC()
	day := strings.ToLower(now.Weekday().String()[:3])
	closed := day + " " + now.Add(3*time.Hour).Format("15:04") + "-" + now.Add(3*time.Hour+time.Minute).Format("15:04")
	set := func(win string) {
		code, upd := a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
			{"carrier_id": c503, "priority": 1, "weight": 100, "enabled": true, "window": win},
			{"carrier_id": answer, "priority": 2, "weight": 100, "enabled": true},
		}})
		a.mustOK(code, upd, "set window")
	}
	defer set("")
	// invalid syntax is rejected
	code, _ = a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
		{"carrier_id": c503, "priority": 1, "weight": 100, "enabled": true, "window": "someday 08:00-18:00"}}})
	if code != 400 {
		t.Fatalf("bad window accepted: %d", code)
	}
	set(closed)
	time.Sleep(300 * time.Millisecond)
	code, sim := a.do("GET", "/routing/simulate?customer_id="+acme+"&number=447700900123", nil)
	a.mustOK(code, sim, "simulate")
	if cs := sim["carriers"].([]any); len(cs) != 1 || cs[0].(map[string]any)["name"] != "carrier-answer" {
		t.Fatalf("carriers with closed window: %v", sim["carriers"])
	}
	var skipped []any
	for _, st := range sim["steps"].([]any) {
		if m := st.(map[string]any); m["step"] == "route" {
			skipped, _ = m["detail"].(map[string]any)["skipped"].([]any)
		}
	}
	if len(skipped) != 1 || skipped[0].(map[string]any)["reason"] != "outside_window" {
		t.Fatalf("skipped: %v", sim["steps"])
	}
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1000"})
	res := placeCall(t, "customer-acme", sc, "447700900123", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "447700900123", since)
	expectField(t, cdr, "failover_depth", "0")
	if at := attempts(t, cdr); len(at) != 1 || str(at[0]["carrier_name"]) != "carrier-answer" {
		t.Fatalf("attempts: %v", cdr["attempts"])
	}
	// an open window (whole week) keeps carrier-503 first
	set("mon-sun 00:00-24:00 Europe/London")
	time.Sleep(300 * time.Millisecond)
	code, sim = a.do("GET", "/routing/simulate?customer_id="+acme+"&number=447700900123", nil)
	a.mustOK(code, sim, "simulate open")
	if cs := sim["carriers"].([]any); len(cs) != 2 || cs[0].(map[string]any)["name"] != "carrier-503" {
		t.Fatalf("carriers with open window: %v", sim["carriers"])
	}
}

// Failed attempt charging (D-62): a carrier with charge_failed_attempts
// costs its connect fee for every unanswered attempt.
func TestRoadmap_FailedAttemptCharging(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	c503 := findByName(t, a, "/carriers", "carrier-503")
	code, c := a.do("GET", "/carriers/"+c503, nil)
	a.mustOK(code, c, "carrier")
	body := c["carrier"].(map[string]any)
	body["charge_failed_attempts"] = true
	code, _ = a.do("PUT", "/carriers/"+c503, body)
	a.mustOK(code, nil, "enable charging")
	defer func() {
		body["charge_failed_attempts"] = false
		a.do("PUT", "/carriers/"+c503, body)
	}()
	rows := sql(t, `SELECT a.balance::text AS balance FROM accounts a JOIN carriers c ON c.id = a.owner_id AND a.owner_type = 'carrier' WHERE c.name = 'carrier-503'`)
	before := dec(t, rows[0]["balance"])
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1000"})
	res := placeCall(t, "customer-acme", sc, "447700900123", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "447700900123", since)
	expectField(t, cdr, "failover_depth", "1")
	expectMoney(t, cdr, "cost", "0.017000") // 0.015 answered on carrier-answer plus 0.002 connect fee of the failed attempt
	at := attempts(t, cdr)
	if len(at) != 2 || str(at[0]["cost"]) != "0.002000" || at[1]["cost"] != nil {
		t.Fatalf("attempt costs: %v", cdr["attempts"])
	}
	rows = sql(t, `SELECT a.balance::text AS balance FROM accounts a JOIN carriers c ON c.id = a.owner_id AND a.owner_type = 'carrier' WHERE c.name = 'carrier-503'`)
	if after := dec(t, rows[0]["balance"]); !after.Equal(before.Sub(decimal.RequireFromString("0.002"))) {
		t.Errorf("carrier-503 balance: before %s after %s", before, after)
	}
	rows = sql(t, fmt.Sprintf(`SELECT description FROM ledger_entries WHERE call_uuid = '%s' AND type = 'cost' ORDER BY created_at`, str(cdr["call_uuid"])))
	if len(rows) != 2 || !strings.Contains(str(rows[0]["description"])+str(rows[1]["description"]), "connect fee of failed attempt 1") {
		t.Errorf("ledger: %v", rows)
	}
	reconcileOK(t)
}

// Invoices (D-63): generated once per account and month, listed per owner,
// rendered as PDF.
func TestRoadmap_Invoices(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	period := time.Now().UTC().Format("2006-01")
	code, inv := a.do("POST", "/invoices/generate", map[string]any{"owner_type": "customer", "owner_id": acme, "period": period})
	if code != 200 && code != 201 {
		t.Fatalf("generate: %d %v", code, inv)
	}
	if !strings.HasPrefix(str(inv["number"]), "INV-"+strings.ReplaceAll(period, "-", "")+"-") || inv["currency"] != "USD" {
		t.Fatalf("invoice: %v", inv)
	}
	// idempotent: same invoice again
	code2, again := a.do("POST", "/invoices/generate", map[string]any{"owner_type": "customer", "owner_id": acme, "period": period})
	if code2 != 200 || again["id"] != inv["id"] {
		t.Fatalf("second generate: %d %v", code2, again)
	}
	code, _ = a.do("POST", "/invoices/generate", map[string]any{"owner_type": "customer", "owner_id": acme, "period": "2999-01"})
	if code != 400 {
		t.Fatalf("future period accepted: %d", code)
	}
	code, list := a.do("GET", "/invoices?owner_type=customer&owner_id="+acme, nil)
	a.mustOK(code, list, "list")
	if n := len(items(list)); n < 1 {
		t.Fatalf("list: %v", list)
	}
	code, pdf := a.do("GET", "/invoices/"+str(inv["id"])+".pdf", nil)
	if code != 200 || !strings.HasPrefix(str(pdf["raw"]), "%PDF-") {
		t.Fatalf("pdf: %d %.40q", code, str(pdf["raw"]))
	}
}
