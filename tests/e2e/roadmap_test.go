//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
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
