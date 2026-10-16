//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// Carrier endpoints (D-75): several addresses per carrier, load balanced with
// failover inside the carrier, and an address that stops answering is parked
// and later tried again.
func TestRoadmap_CarrierEndpoints(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	stamp := fmt.Sprint(time.Now().UnixNano() % 1e6)
	// a carrier whose first address is dead (nothing listens on .250) and
	// whose second is the answering sipp UAS
	code, rg := a.do("POST", "/rate-groups", map[string]string{"name": "ep-" + stamp, "currency": "USD"})
	a.mustOK(code, rg, "rate group")
	code, imp := a.upload("/rate-groups/"+idOf(t, rg)+"/rates/import", "prefix,destination,rate_per_min\n44,UK,0.004000\n")
	a.mustOK(code, imp, "import")
	code, car := a.do("POST", "/carriers", map[string]any{"name": "ep-carrier-" + stamp, "status": "active", "rate_group_id": idOf(t, rg),
		"gateway_host": "172.28.0.250", "gateway_port": 5060, "transport": "udp", "allowed_codecs": []string{"PCMA", "PCMU"},
		"currency": "USD", "endpoint_fail_threshold": 1, "endpoint_retry_seconds": 60,
		// no OPTIONS ping: deadness has to be learned from the calls themselves
		"sip_options_ping": false, "endpoint_park_on_failure": true})
	a.mustOK(code, car, "carrier")
	cid := idOf(t, car)
	defer a.do("DELETE", "/carriers/"+cid, nil)
	// one endpoint to start with, mirrored from the gateway columns
	code, eps := a.do("GET", "/carriers/"+cid+"/endpoints", nil)
	a.mustOK(code, eps, "endpoints")
	if items := items(eps); len(items) != 1 || items[0]["host"] != "172.28.0.250" || items[0]["gateway"] != "ep-carrier-"+stamp {
		t.Fatalf("primary endpoint: %v", eps)
	}
	// add the answering address behind it
	code, eps = a.do("PUT", "/carriers/"+cid+"/endpoints", map[string]any{"endpoints": []map[string]any{
		{"host": "172.28.0.250", "port": 5060, "transport": "udp", "priority": 1},
		{"host": "172.28.0.61", "port": 5060, "transport": "udp", "priority": 2},
	}})
	a.mustOK(code, eps, "set endpoints")
	rows := items(eps)
	if len(rows) != 2 || rows[0]["gateway"] != "ep-carrier-"+stamp+"-ep1" || rows[1]["gateway"] != "ep-carrier-"+stamp+"-ep2" {
		t.Fatalf("endpoint gateways: %v", rows)
	}
	// route a prefix of its own to this carrier
	rgrp := findByName(t, a, "/route-groups", "default")
	code, rt := a.do("POST", "/route-groups/"+rgrp+"/routes", map[string]any{"prefix": "4428", "destination": "endpoint test",
		"carriers": []map[string]any{{"carrier_id": cid, "priority": 1, "weight": 100, "enabled": true}}})
	a.mustOK(code, rt, "route")
	defer a.do("DELETE", "/routes/"+idOf(t, rt), nil)
	time.Sleep(time.Second) // gateway render plus rescan

	// the call rolls over from the dead address to the working one, inside one carrier
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1000"})
	res := placeCallArgs(t, "customer-acme", sc, "442871234567", 1, "-timeout", "60s")
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442871234567", since)
	expectField(t, cdr, "disposition", "answered")
	at := attempts(t, cdr)
	if len(at) != 2 || str(at[0]["endpoint"]) != "172.28.0.250:5060" || str(at[1]["endpoint"]) != "172.28.0.61:5060" {
		t.Fatalf("endpoint failover attempts: %v", cdr["attempts"])
	}
	if str(at[0]["carrier_name"]) != str(at[1]["carrier_name"]) {
		t.Fatalf("both attempts must belong to the same carrier: %v", cdr["attempts"])
	}
	// the dead address is parked (threshold 1) and dropped from the next call
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, eps = a.do("GET", "/carriers/"+cid+"/endpoints", nil)
		a.mustOK(code, eps, "endpoints after call")
		if r := items(eps); r[0]["parked"] == true && r[0]["parked_for_seconds"].(float64) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dead endpoint was not parked: %v", eps)
		}
		time.Sleep(time.Second)
	}
	code, sim := a.do("GET", "/routing/simulate?customer_id="+acme+"&number=442871234567", nil)
	a.mustOK(code, sim, "simulate")
	cs, _ := sim["carriers"].([]any)
	if len(cs) != 1 || cs[0].(map[string]any)["endpoint"] != "172.28.0.61:5060" {
		t.Fatalf("parked endpoint still offered, or none left: %v", sim)
	}
	since = time.Now()
	res = placeCallArgs(t, "customer-acme", sc, "442871234567", 1, "-timeout", "60s")
	expectFinal(t, res, "200 OK")
	cdr = lastCDR(t, "172.28.0.101", "442871234567", since)
	if at = attempts(t, cdr); len(at) != 1 || str(at[0]["endpoint"]) != "172.28.0.61:5060" {
		t.Fatalf("second call should skip the parked address: %v", cdr["attempts"])
	}
	// an operator can put it back at once
	epID := str(items(eps)[0]["id"])
	code, _ = a.do("POST", "/carriers/"+cid+"/endpoints/"+epID+"/revive", nil)
	if code != 204 {
		t.Fatalf("revive: %d", code)
	}
	code, eps = a.do("GET", "/carriers/"+cid+"/endpoints", nil)
	a.mustOK(code, eps, "endpoints after revive")
	if items(eps)[0]["parked"] == true {
		t.Fatalf("endpoint should be back in rotation: %v", eps)
	}
	reconcileOK(t)
}
