//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestM4_AuthAndRoles(t *testing.T) {
	a := newAPIClient(t)
	if code, _ := a.login("admin@example.com", "wrong-password"); code != 401 {
		t.Fatalf("wrong password gave %d", code)
	}
	code, body := a.login("admin@example.com", adminPassword(t))
	a.mustOK(code, body, "login")
	if a.csrf == "" {
		t.Fatal("no csrf token")
	}
	// CSRF is required for state changes with cookie sessions
	nocsrf := newAPIClient(t)
	nocsrf.c.Jar = a.c.Jar
	if code, _ := nocsrf.do("POST", "/rate-groups", map[string]string{"name": "x"}); code != 403 {
		t.Fatalf("missing csrf token gave %d", code)
	}
	code, body = a.do("GET", "/auth/me", nil)
	a.mustOK(code, body, "me")
	if body["role"] != "admin" {
		t.Fatalf("role %v", body["role"])
	}
	// viewer cannot write, operator cannot move money
	viewerPW := "viewer-password-1"
	code, v := a.do("POST", "/users", map[string]string{"email": fmt.Sprintf("viewer-%d@example.com", time.Now().UnixNano()), "password": viewerPW, "role": "viewer"})
	a.mustOK(code, v, "create viewer")
	viewer := newAPIClient(t)
	code, vb := viewer.login(v["email"].(string), viewerPW)
	viewer.mustOK(code, vb, "viewer login")
	if code, _ := viewer.do("POST", "/rate-groups", map[string]string{"name": "nope"}); code != 403 {
		t.Fatalf("viewer write gave %d", code)
	}
	if code, _ := viewer.do("GET", "/customers", nil); code != 200 {
		t.Fatalf("viewer read gave %d", code)
	}
	opPW := "operator-password-1"
	code, o := a.do("POST", "/users", map[string]string{"email": fmt.Sprintf("op-%d@example.com", time.Now().UnixNano()), "password": opPW, "role": "operator"})
	a.mustOK(code, o, "create operator")
	op := newAPIClient(t)
	op.login(o["email"].(string), opPW)
	acme := findByName(t, a, "/customers", "acme")
	if code, _ := op.do("POST", "/customers/"+acme+"/account/topup", map[string]string{"amount": "1"}); code != 403 {
		t.Fatalf("operator topup gave %d", code)
	}
	// API keys work without cookies or csrf
	code, k := a.do("POST", "/auth/api-keys", map[string]string{"name": "ci"})
	a.mustOK(code, k, "create api key")
	kc := newAPIClient(t)
	kc.key = k["key"].(string)
	code, kb := kc.do("GET", "/auth/me", nil)
	kc.mustOK(code, kb, "api key me")
	if kb["via_api_key"] != true {
		t.Fatalf("api key principal: %v", kb)
	}
	// login rate limit: 10 failures
	rl := newAPIClient(t)
	victim := v["email"].(string)
	last := 0
	for i := 0; i < 12; i++ {
		last, _ = rl.login(victim, "bad")
	}
	if last != 429 {
		t.Fatalf("expected 429 after repeated failures, got %d", last)
	}
	// audit log has our actions
	code, al := a.do("GET", "/audit-log?entity_type=user", nil)
	a.mustOK(code, al, "audit")
	if len(items(al)) < 2 {
		t.Fatalf("audit entries: %v", al)
	}
	// logout clears the session
	code, _ = a.do("POST", "/auth/logout", nil)
	if code != 204 {
		t.Fatalf("logout %d", code)
	}
	if code, _ := a.do("GET", "/auth/me", nil); code != 401 {
		t.Fatalf("session survived logout: %d", code)
	}
}

func TestM4_RateDeckImportAndTest(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	name := fmt.Sprintf("import-%d", time.Now().UnixNano())
	code, rg := a.do("POST", "/rate-groups", map[string]string{"name": name, "currency": "USD", "description": "csv import test"})
	a.mustOK(code, rg, "create rate group")
	rgID := idOf(t, rg)
	csv := "prefix,destination,rate_per_min,connect_fee,initial_increment,subsequent_increment\n" +
		"44,UK Fixed,0.0100,0,60,60\n" +
		"447,UK Mobile,0.0200,0.001,30,6\n" +
		"4477,UK Mobile O2,0.0190,0,1,1\n" +
		"abc,Bad prefix,0.01,0,60,60\n" +
		"49,Germany,notanumber,0,60,60\n"
	code, prev := a.upload("/rate-groups/"+rgID+"/rates/import?dry_run=true", csv)
	a.mustOK(code, prev, "dry run")
	if prev["valid"].(float64) != 3 || prev["invalid"].(float64) != 2 || prev["imported"].(float64) != 0 {
		t.Fatalf("preview: %v", prev)
	}
	// a full import refuses invalid rows unless partial=true
	code, res := a.upload("/rate-groups/"+rgID+"/rates/import", csv)
	if code != 422 {
		t.Fatalf("import with bad rows should be 422, got %d %v", code, res)
	}
	code, res = a.upload("/rate-groups/"+rgID+"/rates/import?partial=true", csv)
	a.mustOK(code, res, "partial import")
	if res["imported"].(float64) != 3 {
		t.Fatalf("imported: %v", res)
	}
	code, tst := a.do("GET", "/rate-groups/"+rgID+"/rates/test?number=447700900123", nil)
	a.mustOK(code, tst, "test number")
	rate := tst["rate"].(map[string]any)
	if rate["prefix"] != "4477" || tst["sql_agrees"] != true || tst["reserve_amount"] != "0.095000" {
		t.Fatalf("test: %v", tst)
	}
	// export round trip
	code, exp := a.do("GET", "/rate-groups/"+rgID+"/rates/export", nil)
	if code != 200 || !strings.Contains(exp["raw"].(string), "4477,UK Mobile O2,0.019000") {
		t.Fatalf("export: %d %v", code, exp)
	}
	// replace mode wipes the old rows
	code, res = a.upload("/rate-groups/"+rgID+"/rates/import?replace=true", "prefix,rate\n1,0.005\n")
	a.mustOK(code, res, "replace import")
	code, lst := a.do("GET", "/rate-groups/"+rgID+"/rates", nil)
	a.mustOK(code, lst, "list rates")
	if lst["total"].(float64) != 1 {
		t.Fatalf("after replace: %v", lst["total"])
	}
	a.ok(a.do("DELETE", "/rate-groups/"+rgID, nil))
}

func TestM4_RoutesAndSimulator(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	answer := findByName(t, a, "/carriers", "carrier-answer")
	c503 := findByName(t, a, "/carriers", "carrier-503")
	code, sim := a.do("GET", "/routing/simulate?customer_id="+acme+"&number=00447700900123", nil)
	a.mustOK(code, sim, "simulate")
	if !strings.HasPrefix(sim["expected_sip"].(string), "200") || sim["called"] != "447700900123" {
		t.Fatalf("simulate: %v", sim)
	}
	carriers := sim["carriers"].([]any)
	if len(carriers) != 2 || carriers[0].(map[string]any)["name"] != "carrier-503" {
		t.Fatalf("simulated carriers: %v", carriers)
	}
	// reorder: carrier-answer first
	rg := findByName(t, a, "/route-groups", "default")
	code, routes := a.do("GET", "/route-groups/"+rg+"/routes?search=4477", nil)
	a.mustOK(code, routes, "routes")
	var routeID string
	for _, r := range items(routes) {
		if r["prefix"] == "4477" {
			routeID = r["id"].(string)
		}
	}
	if routeID == "" {
		t.Fatal("route 4477 missing")
	}
	code, upd := a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
		{"carrier_id": answer, "priority": 1, "weight": 100, "enabled": true},
		{"carrier_id": c503, "priority": 2, "weight": 100, "enabled": true},
	}})
	a.mustOK(code, upd, "reorder")
	code, sim = a.do("GET", "/routing/simulate?customer_id="+acme+"&number=447700900123", nil)
	a.mustOK(code, sim, "simulate 2")
	if sim["carriers"].([]any)[0].(map[string]any)["name"] != "carrier-answer" {
		t.Fatalf("reorder not applied: %v", sim["carriers"])
	}
	// restore the seed order (other tests depend on it)
	code, upd = a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
		{"carrier_id": c503, "priority": 1, "weight": 100, "enabled": true},
		{"carrier_id": answer, "priority": 2, "weight": 100, "enabled": true},
	}})
	a.mustOK(code, upd, "restore order")
	// preview shows buy rates and margin
	code, pv := a.do("GET", "/routes/"+routeID+"/preview?number=447700900123&sell_rate_group_id="+findByName(t, a, "/rate-groups", "retail-usd"), nil)
	a.mustOK(code, pv, "preview")
	pcs := pv["carriers"].([]any)
	if len(pcs) != 2 || pcs[1].(map[string]any)["margin_per_min"] != "0.005000" {
		t.Fatalf("preview: %v", pv)
	}
	// LCR mode reorders by buy rate: carrier-503 (0.014) before carrier-answer (0.015) already; flip to test it applies
	code, _ = a.do("PUT", "/route-groups/"+rg, map[string]any{"name": "default", "description": "Demo route group", "lcr_mode": true})
	a.mustOK(code, nil, "lcr on")
	code, sim = a.do("GET", "/routing/simulate?customer_id="+acme+"&number=447700900123", nil)
	a.mustOK(code, sim, "simulate lcr")
	if sim["carriers"].([]any)[0].(map[string]any)["name"] != "carrier-503" {
		t.Fatalf("lcr order: %v", sim["carriers"])
	}
	a.ok(a.do("PUT", "/route-groups/"+rg, map[string]any{"name": "default", "description": "Demo route group", "lcr_mode": false}))
}

// Block lists reject with 403 Destination blocked (D-44) and are removable.
func TestM4_BlockList(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	code, blk := a.do("POST", "/customers/"+acme+"/blocked-prefixes", map[string]string{"prefix": "4420", "reason": "test block"})
	a.mustOK(code, blk, "block")
	since := time.Now()
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "403"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "403 Destination blocked")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	expectField(t, cdr, "disposition", "rejected_route")
	expectField(t, cdr, "reject_reason", "Destination blocked")
	a.ok(a.do("DELETE", "/blocked-prefixes/"+idOf(t, blk), nil))
	// global blacklist applies to everyone
	code, g := a.do("POST", "/blocked-prefixes", map[string]string{"prefix": "4420", "reason": "global test"})
	a.mustOK(code, g, "global block")
	res = placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "403 Destination blocked")
	a.ok(a.do("DELETE", "/blocked-prefixes/"+idOf(t, g), nil))
	ok := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1000"})
	res = placeCall(t, "customer-acme", ok, "442071234567", 1)
	expectFinal(t, res, "200 OK")
}

// Multi-currency: a EUR customer is billed the USD deck converted at the fx rate.
func TestM4_MultiCurrency(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	a.ok(a.do("POST", "/fx-rates", map[string]string{"base": "USD", "quote": "EUR", "rate": "0.5"}))
	retail := findByName(t, a, "/rate-groups", "retail-usd")
	rg := findByName(t, a, "/route-groups", "default")
	name := "euro-e2e"
	code, list := a.do("GET", "/customers?search="+name, nil)
	a.mustOK(code, list, "list")
	var cid string
	for _, it := range items(list) {
		if it["name"] == name {
			cid = it["id"].(string)
		}
	}
	if cid == "" {
		code, c := a.do("POST", "/customers", map[string]any{"name": name, "currency": "EUR", "rate_group_id": retail, "route_group_id": rg})
		a.mustOK(code, c, "create eur customer")
		cid = idOf(t, c)
		a.ok(a.do("POST", "/customers/"+cid+"/ips", map[string]string{"ip_cidr": "172.28.0.106/32"}))
		a.ok(a.do("POST", "/customers/"+cid+"/account/topup", map[string]string{"amount": "2.00", "description": "e2e"}))
	}
	customerIP["customer-euro"] = "172.28.0.106"
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-euro", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.106", "442071234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectMoney(t, cdr, "sell_price", "0.005000") // 0.01 USD/min at 0.5 = 0.005 EUR
	rows := sql(t, "SELECT sell_currency, sell_fx::text AS sell_fx, sell_rate_per_min::text AS r FROM cdrs WHERE call_uuid = '"+str(cdr["call_uuid"])+"'")
	if len(rows) != 1 || str(rows[0]["sell_currency"]) != "EUR" || !strings.HasPrefix(str(rows[0]["sell_fx"]), "0.5") || !strings.HasPrefix(str(rows[0]["r"]), "0.005") {
		t.Fatalf("currency columns: %v", rows)
	}
	reconcileOK(t)
}

func TestM4_CDRsAndActiveCalls(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	code, l := a.do("GET", "/cdrs?disposition=answered&per_page=5&sort=-start_time", nil)
	a.mustOK(code, l, "cdrs")
	if len(items(l)) == 0 {
		t.Fatal("no answered cdrs")
	}
	first := items(l)[0]
	code, one := a.do("GET", "/cdrs/"+first["call_uuid"].(string), nil)
	a.mustOK(code, one, "cdr")
	if one["customer_name"] == nil || one["attempts"] == nil {
		t.Fatalf("cdr detail: %v", one)
	}
	code, exp := a.do("GET", "/cdrs/export?disposition=answered", nil)
	if code != 200 || !strings.HasPrefix(exp["raw"].(string), "call_uuid,start_time") {
		t.Fatalf("export: %d", code)
	}
	// active calls during a long call, then hang it up through the API
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "30000"})
	done := make(chan callResult, 1)
	go func() { done <- placeCallArgs(t, "customer-acme", sc, "442071234567", 1, "-timeout", "60s") }()
	var active map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		code, act := a.do("GET", "/calls/active", nil)
		a.mustOK(code, act, "active")
		for _, it := range items(act) {
			// wait for the answered state so the API hangup produces a BYE, not a CANCEL
			if it["called_number"] == "442071234567" && it["freeswitch_seen"] == true && (it["state"] == "ACTIVE" || it["state"] == "answered") {
				active = it
			}
		}
		if active != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if active == nil {
		t.Fatal("call never appeared in /calls/active")
	}
	code, _ = a.do("DELETE", "/calls/active/"+active["call_uuid"].(string), nil)
	if code != 204 {
		t.Fatalf("hangup gave %d", code)
	}
	res := <-done
	if !strings.Contains(res.Messages, "BYE sip:") {
		t.Error("customer did not receive BYE after API hangup")
	}
	cdr := lastCDR(t, "172.28.0.101", "442071234567", time.Now().Add(-40*time.Second))
	if bs := int(cdr["billsec"].(float64)); bs > 20 {
		t.Errorf("call was not hung up early: billsec %d", bs)
	}
}
