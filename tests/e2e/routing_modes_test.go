//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Routing modes (D-73): lossless, percentage and quality based routing,
// exercised through a dedicated route group that the acme customer is
// pointed at for the duration of the test.
func TestRoadmap_RoutingModes(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	answer := findByName(t, a, "/carriers", "carrier-answer")
	c503 := findByName(t, a, "/carriers", "carrier-503")
	code, cust := a.do("GET", "/customers/"+acme, nil)
	a.mustOK(code, cust, "acme")
	custBody := cust["customer"].(map[string]any)
	origGroup := custBody["route_group_id"]
	setGroup := func(id any) {
		custBody["route_group_id"] = id
		code, res := a.do("PUT", "/customers/"+acme, custBody)
		a.mustOK(code, res, "customer route group")
	}
	defer setGroup(origGroup)

	// an expensive carrier (same UAS as carrier-answer) for the lossless case
	stamp := fmt.Sprint(time.Now().UnixNano() % 1e6)
	code, rg := a.do("POST", "/rate-groups", map[string]string{"name": "expensive-" + stamp, "currency": "USD"})
	a.mustOK(code, rg, "rate group")
	code, imp := a.upload("/rate-groups/"+idOf(t, rg)+"/rates/import", "prefix,destination,rate_per_min\n44,UK expensive,0.050000\n")
	a.mustOK(code, imp, "import")
	code, exp := a.do("POST", "/carriers", map[string]any{"name": "carrier-expensive-" + stamp, "status": "active", "rate_group_id": idOf(t, rg),
		"gateway_host": "172.28.0.61", "gateway_port": 5060, "transport": "udp", "allowed_codecs": []string{"PCMA", "PCMU"}, "currency": "USD"})
	a.mustOK(code, exp, "carrier")
	expensive := idOf(t, exp)
	defer a.do("DELETE", "/carriers/"+expensive, nil)

	code, grp := a.do("POST", "/route-groups", map[string]any{"name": "modes-" + stamp, "lossless_mode": true})
	a.mustOK(code, grp, "group")
	gid := idOf(t, grp)
	defer a.do("DELETE", "/route-groups/"+gid, nil)
	setModes := func(m map[string]any) {
		body := map[string]any{"name": "modes-" + stamp, "description": ""}
		for k, v := range m {
			body[k] = v
		}
		code, res := a.do("PUT", "/route-groups/"+gid, body)
		a.mustOK(code, res, "modes")
	}
	// percent excludes the other modes
	if code, _ := a.do("PUT", "/route-groups/"+gid, map[string]any{"name": "modes-" + stamp, "percent_mode": true, "lcr_mode": true}); code != 400 {
		t.Fatalf("percent with lcr accepted: %d", code)
	}
	code, rt := a.do("POST", "/route-groups/"+gid+"/routes", map[string]any{"prefix": "4420", "destination": "London", "carriers": []map[string]any{
		{"carrier_id": expensive, "priority": 1, "weight": 100, "enabled": true},
		{"carrier_id": answer, "priority": 2, "weight": 100, "enabled": true},
	}})
	a.mustOK(code, rt, "route")
	routeID := idOf(t, rt)
	setGroup(gid)
	time.Sleep(400 * time.Millisecond)
	sim := func(number string) (first string, skipped []any, m map[string]any) {
		code, s := a.do("GET", "/routing/simulate?customer_id="+acme+"&number="+number, nil)
		a.mustOK(code, s, "simulate")
		if cs, _ := s["carriers"].([]any); len(cs) > 0 {
			first = cs[0].(map[string]any)["name"].(string)
		}
		for _, st := range s["steps"].([]any) {
			if x := st.(map[string]any); x["step"] == "route" {
				skipped, _ = x["detail"].(map[string]any)["skipped"].([]any)
			}
		}
		return first, skipped, s
	}

	// 1. Lossless: the expensive carrier (0.05 > sell 0.01) is skipped, the call goes to carrier-answer
	first, skipped, _ := sim("442071234567")
	if first != "carrier-answer" || len(skipped) != 1 || skipped[0].(map[string]any)["reason"] != "lossless_skipped" {
		t.Fatalf("lossless: first %s skipped %v", first, skipped)
	}
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "500"})
	billable := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1500"})
	expectFinal(t, placeCall(t, "customer-acme", billable, "442071234567", 1), "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	expectMoney(t, cdr, "cost", "0.006000")
	// lossless off: the expensive carrier is first again (priority 1)
	setModes(map[string]any{})
	time.Sleep(400 * time.Millisecond)
	if first, _, _ = sim("442071234567"); !strings.HasPrefix(first, "carrier-expensive") {
		t.Fatalf("priority mode: first %s", first)
	}

	// 2. Percent: 50/50 over 300 simulations lands within 10 points; a 100/0 share
	//    with carrier-503 first still fails over to carrier-answer on a real call
	setModes(map[string]any{"percent_mode": true})
	a.ok(a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
		{"carrier_id": c503, "priority": 1, "weight": 50, "enabled": true},
		{"carrier_id": answer, "priority": 2, "weight": 50, "enabled": true},
	}}))
	time.Sleep(400 * time.Millisecond)
	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		f, _, m := sim("442071234567")
		counts[f]++
		if i == 0 {
			if modes, _ := m["modes"].(map[string]any); modes["percent"] != true {
				t.Fatalf("simulate should report percent mode: %v", m["modes"])
			}
			if cs := m["carriers"].([]any); len(cs) != 2 || cs[0].(map[string]any)["share"].(float64) != 50 {
				t.Fatalf("share missing: %v", cs)
			}
		}
	}
	if p := float64(counts["carrier-503"]) / 300; p < 0.40 || p > 0.60 {
		t.Fatalf("percent distribution: %v", counts)
	}
	a.ok(a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
		{"carrier_id": c503, "priority": 1, "weight": 100, "enabled": true},
		{"carrier_id": answer, "priority": 2, "weight": 0, "enabled": true},
	}}))
	time.Sleep(400 * time.Millisecond)
	since = time.Now()
	expectFinal(t, placeCall(t, "customer-acme", sc, "442071234567", 1), "200 OK")
	cdr = lastCDR(t, "172.28.0.101", "442071234567", since)
	expectField(t, cdr, "failover_depth", "1")
	if at := attempts(t, cdr); len(at) != 2 || str(at[0]["carrier_name"]) != "carrier-503" || str(at[1]["carrier_name"]) != "carrier-answer" {
		t.Fatalf("percent failover attempts: %v", cdr["attempts"])
	}

	// 3. Quality: after enough attempts on prefix 4420 (carrier-503 always faults,
	//    carrier-answer always answers) quality mode puts carrier-answer first
	//    despite its lower priority.
	setModes(map[string]any{"quality_mode": true})
	a.ok(a.do("PUT", "/routes/"+routeID+"/carriers", map[string]any{"carriers": []map[string]any{
		{"carrier_id": c503, "priority": 1, "weight": 100, "enabled": true},
		{"carrier_id": answer, "priority": 2, "weight": 100, "enabled": true},
	}}))
	time.Sleep(400 * time.Millisecond)
	// 24 calls: each gives carrier-503 a fault and carrier-answer an answer on prefix 4420
	res := placeCallArgs(t, "customer-acme", sc, "442071234567", 24, "-r", "6", "-rp", "1000", "-timeout", "120s")
	if n := strings.Count(strings.Join(res.FinalLines, "\n"), "200 OK"); n < 24 {
		t.Fatalf("quality warm-up: %d answered of 24", n)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		first, _, m := sim("442071234567")
		cs := m["carriers"].([]any)
		if first == "carrier-answer" && len(cs) == 2 && cs[0].(map[string]any)["quality"] != nil {
			q0 := cs[0].(map[string]any)["quality"].(float64)
			q1 := cs[1].(map[string]any)["quality"].(float64)
			if q0 <= q1 {
				t.Fatalf("quality scores: answer %.2f vs 503 %.2f", q0, q1)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("quality mode did not reorder: first %s carriers %v", first, cs)
		}
		time.Sleep(time.Second)
	}
	code, qs := a.do("GET", "/carriers/"+c503+"/quality", nil)
	a.mustOK(code, qs, "carrier quality")
	found := false
	for _, x := range items(qs) {
		if x["prefix"] == "4420" && x["asr"].(float64) == 0 && x["samples"].(float64) >= 20 {
			found = true
		}
	}
	if !found {
		t.Fatalf("carrier-503 quality per prefix: %v", qs)
	}
	reconcileOK(t)
}
