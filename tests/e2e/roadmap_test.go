//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/stir"
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

// STIR/SHAKEN (D-64): iota-stir requires a verified Identity header. The
// test signs PASSporTs with a throwaway ES256 key and serves the certificate
// from the host (172.28.0.1) where the API fetches it.
func TestRoadmap_STIR(t *testing.T) {
	key, certPEM, err := stir.NewTestCert(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "172.28.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on the docker bridge address: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(certPEM) }), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	x5u := "http://" + ln.Addr().String() + "/sti.pem"
	sign := func(orig, dest string, iat time.Time) string {
		s, err := stir.Sign(key, x5u, "A", orig, dest, "e2e", iat)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// (a) no Identity: 428 Use Identity Header
	since := time.Now()
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "428"})
	res := placeCall(t, "customer-iota", sc, "442071234567", 1)
	expectFinal(t, res, "428 Use Identity Header")
	cdr := lastCDR(t, "172.28.0.111", "442071234567", since)
	expectField(t, cdr, "disposition", "rejected_auth")
	expectField(t, cdr, "stir_status", "none")
	// (b) stale token: 438
	since = time.Now()
	sc = scenario(t, "uac_call_stir.xml.tmpl", map[string]string{"__TALK__": "500", "__IDENTITY__": sign("15550001111", "442071234567", time.Now().Add(-10*time.Minute))})
	res = placeCall(t, "customer-iota", sc, "442071234567", 1)
	expectFinal(t, res, "438 Invalid Identity Header")
	cdr = lastCDR(t, "172.28.0.111", "442071234567", since)
	expectField(t, cdr, "stir_status", "stale")
	// (c) valid token: answered, attest recorded, Identity forwarded to the carrier
	since = time.Now()
	sc = scenario(t, "uac_call_stir.xml.tmpl", map[string]string{"__TALK__": "1000", "__IDENTITY__": sign("15550001111", "442071234567", time.Now())})
	res = placeCall(t, "customer-iota", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr = lastCDR(t, "172.28.0.111", "442071234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "stir_status", "verified")
	expectField(t, cdr, "stir_attest", "A")
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "Identity: eyJ") {
		t.Errorf("carrier INVITE should carry the verified Identity header:\n%s", inv)
	}
	reconcileOK(t)
}

// HEP capture (D-65): the dev stack mirrors SIP as HEPv3 to 172.28.0.1:9060;
// the test listens there and expects the INVITE and the 200 OK of one call.
func TestRoadmap_HEPCapture(t *testing.T) {
	pc, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", "172.28.0.1:9060")
	if err != nil {
		t.Skipf("cannot listen on 172.28.0.1:9060 (homer profile running?): %v", err)
	}
	defer func() { _ = pc.Close() }()
	var mu sync.Mutex
	var seen []string
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n > 6 && string(buf[:4]) == "HEP3" {
				mu.Lock()
				seen = append(seen, string(buf[:n]))
				mu.Unlock()
			}
		}
	}()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "500"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		all := strings.Join(seen, "\n")
		n := len(seen)
		mu.Unlock()
		if strings.Contains(all, "INVITE sip:442071234567@") && strings.Contains(all, "SIP/2.0 200 OK") && strings.Contains(all, "BYE sip:") {
			t.Logf("%d HEP packets captured", n)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("HEP capture incomplete after %d packets", n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TLS client certificate verification (D-67): the dev ingress profile runs
// with tls-verify-policy subjects_in, so a client certificate must chain to
// cafile.pem and carry a subject listed on a customer. The M5 TLS test covers
// the accepted case; here a certificate from an unknown CA and one from the
// trusted CA with an unlisted subject are both refused at the handshake.
func TestRoadmap_TLSClientCert(t *testing.T) {
	copyTLSFiles(t)
	out := filepath.Join(repoRoot(t), "tests", "e2e", "out")
	tls := filepath.Join(repoRoot(t), "freeswitch", "tls")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "openssl", args...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("openssl %v: %v\n%s", args, err, b)
		}
	}
	// unknown CA, listed subject
	run("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2", "-keyout", filepath.Join(out, "rogue-key.pem"), "-out", filepath.Join(out, "rogue-cert.pem"),
		"-subj", "/CN=172.28.0.10/O=Rogue", "-addext", "subjectAltName=IP:172.28.0.10")
	// trusted CA, unlisted subject
	run("req", "-newkey", "rsa:2048", "-nodes", "-keyout", filepath.Join(out, "other-key.pem"), "-out", filepath.Join(out, "other.csr"), "-subj", "/CN=other.example/O=SuperSBC")
	run("x509", "-req", "-in", filepath.Join(out, "other.csr"), "-CA", filepath.Join(tls, "cert.pem"), "-CAkey", filepath.Join(tls, "key.pem"), "-CAcreateserial",
		"-out", filepath.Join(out, "other-cert.pem"), "-days", "2")
	// The subject list is rendered when customers change and applies at the
	// next profile start (D-67): restart the ingress profile through the API.
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	var code int
	var rs map[string]any
	for i := 0; i < 20; i++ { // a preceding test may have restarted the API; wait for its ESL link
		if code, rs = a.do("POST", "/system/profiles/external-ingress/restart", nil); code == 200 {
			break
		}
		time.Sleep(time.Second)
	}
	a.mustOK(code, rs, "restart profile")
	deadline := time.Now().Add(30 * time.Second)
	for {
		out := compose(t, "exec", "-T", "api", "wget", "-qO-", "http://127.0.0.1:8080/readyz")
		if b, err := out.Output(); err == nil && strings.Contains(string(b), `"ready":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ingress profile did not come back")
		}
		time.Sleep(time.Second)
	}
	time.Sleep(2 * time.Second)
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "500"})
	for _, c := range []struct{ name, cert, key string }{{"unknown CA", "rogue-cert.pem", "rogue-key.pem"}, {"unlisted subject", "other-cert.pem", "other-key.pem"}} {
		res := placeTLSCallWithCert(t, "customer-zeta", sc, "442071234567", c.cert, c.key)
		if len(res.FinalLines) != 0 {
			t.Errorf("%s: call should have failed at the TLS handshake, got %v", c.name, res.FinalLines)
		}
	}
	// the right certificate still works
	res := placeTLSCall(t, "customer-zeta", sc, "442071234567")
	expectFinal(t, res, "200 OK")
}
