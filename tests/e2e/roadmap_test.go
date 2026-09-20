//go:build e2e

package e2e

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
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

// Invoices (D-63) and the invoice ledger (D-72): monthly and custom periods
// that never overlap, payments allocated to one or several invoices (full or
// partial), optional top-up of the prepaid balance, PDF.
func TestRoadmap_Invoices(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	// a fresh customer so the ledger starts empty
	name := fmt.Sprintf("inv-%d", time.Now().UnixNano()%1e6)
	code, cu := a.do("POST", "/customers", map[string]any{"name": name, "status": "active", "allowed_codecs": []string{"PCMA"}, "intl_prefix": "00", "currency": "USD"})
	a.mustOK(code, cu, "create customer")
	cid := idOf(t, cu)
	defer a.do("DELETE", "/customers/"+cid, nil)
	gen := func(body map[string]any) (int, map[string]any) {
		body["owner_type"], body["owner_id"] = "customer", cid
		return a.do("POST", "/invoices/generate", body)
	}
	// custom period in a past month, then a monthly one for the same month must be refused (overlap)
	code, inv := gen(map[string]any{"from": "2026-07-05", "to": "2026-07-20"})
	if code != 201 || inv["kind"] != "custom" || inv["status"] != "nothing_due" {
		t.Fatalf("custom invoice: %d %v", code, inv)
	}
	if code, again := gen(map[string]any{"from": "2026-07-05", "to": "2026-07-20"}); code != 200 || again["id"] != inv["id"] {
		t.Fatalf("idempotent custom: %d", code)
	}
	if code, _ = gen(map[string]any{"period": "2026-07"}); code != 409 {
		t.Fatalf("overlap should be 409: %d", code)
	}
	if code, _ = gen(map[string]any{"from": "2026-07-20", "to": "2026-07-10"}); code != 400 {
		t.Fatalf("reversed range: %d", code)
	}
	code, inv2 := gen(map[string]any{"period": "2026-06"})
	if code != 201 || inv2["kind"] != "monthly" || !strings.HasPrefix(str(inv2["number"]), "INV-202606-") {
		t.Fatalf("monthly invoice: %d %v", code, inv2)
	}
	code, inv3 := gen(map[string]any{"period": "2026-05"})
	a.mustOK(code, inv3, "third invoice")
	// give the invoices an amount to pay (the customer has no calls): set it directly
	sqlExec(t, fmt.Sprintf(`UPDATE invoices SET amount = 30 WHERE id = '%s'`, str(inv["id"])))
	sqlExec(t, fmt.Sprintf(`UPDATE invoices SET amount = 20 WHERE id = '%s'`, str(inv2["id"])))
	sqlExec(t, fmt.Sprintf(`UPDATE invoices SET amount = 10 WHERE id = '%s'`, str(inv3["id"])))
	before, _ := balance(t, name)
	// partial payment by hand against the newest invoice, with top-up of the prepaid balance
	code, p1 := a.do("POST", "/invoice-payments", map[string]any{"owner_type": "customer", "owner_id": cid, "amount": "12.5", "reference": "TRX-1", "method": "bank",
		"allocations": []map[string]any{{"invoice_id": inv["id"], "amount": "12.5"}}, "topup": true})
	if code != 201 || p1["unallocated"] != "0.000000" || p1["ledger_entry_id"] == nil {
		t.Fatalf("partial payment: %d %v", code, p1)
	}
	after, _ := balance(t, name)
	if !after.Equal(before.Add(decimal.RequireFromString("12.5"))) {
		t.Fatalf("top-up not posted: %s -> %s", before, after)
	}
	// over-allocation is refused
	if code, _ = a.do("POST", "/invoice-payments", map[string]any{"owner_type": "customer", "owner_id": cid, "amount": "100", "allocations": []map[string]any{{"invoice_id": inv["id"], "amount": "100"}}}); code != 400 {
		t.Fatalf("over-allocation accepted: %d", code)
	}
	// auto allocation spreads over open invoices oldest first: 10 (May) + 20 (June) + 17.5 (July) = 47.5, 2.5 left unallocated
	code, p2 := a.do("POST", "/invoice-payments", map[string]any{"owner_type": "customer", "owner_id": cid, "amount": "50", "reference": "TRX-2", "auto_allocate": true, "topup": false})
	if code != 201 || p2["unallocated"] != "2.500000" || len(p2["allocations"].([]any)) != 3 {
		t.Fatalf("auto payment: %d %v", code, p2)
	}
	if after2, _ := balance(t, name); !after2.Equal(after) {
		t.Fatalf("payment without top-up changed the balance: %s -> %s", after, after2)
	}
	code, led := a.do("GET", "/invoice-ledger?owner_type=customer&owner_id="+cid, nil)
	a.mustOK(code, led, "ledger")
	if led["invoiced"] != "60.000000" || led["received"] != "62.500000" || led["outstanding"] != "0.000000" || led["unallocated"] != "2.500000" || len(led["rows"].([]any)) != 5 {
		t.Fatalf("ledger totals: %v", led)
	}
	code, one := a.do("GET", "/invoices/"+str(inv["id"]), nil)
	a.mustOK(code, one, "invoice")
	if one["status"] != "paid" || one["paid"] != "30.000000" {
		t.Fatalf("invoice status: %v", one)
	}
	code, pdf := a.do("GET", "/invoices/"+str(inv["id"])+".pdf", nil)
	if code != 200 || !strings.HasPrefix(str(pdf["raw"]), "%PDF-") {
		t.Fatalf("pdf: %d", code)
	}
	code, list := a.do("GET", "/invoices?owner_type=customer&owner_id="+cid, nil)
	a.mustOK(code, list, "list")
	if len(items(list)) != 3 {
		t.Fatalf("list: %d", len(items(list)))
	}
	if code, _ = gen(map[string]any{"period": "2999-01"}); code != 400 {
		t.Fatalf("future period accepted: %d", code)
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

// SIP capture (D-65, D-71): FreeSWITCH mirrors SIP as HEPv3 to heplify-server
// (make e2e starts the homer profile) and the CDR "SIP trace" endpoint returns
// both legs of a call, headers and bodies, from the capture store.
func TestRoadmap_SIPTrace(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "500"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	id := str(cdr["call_uuid"])
	var tr map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, body := a.do("GET", "/cdrs/"+id+"/sip", nil)
		if code == 503 {
			t.Skipf("capture store not available: %v", body["error"])
		}
		a.mustOK(code, body, "sip trace")
		tr = body
		if msgs, _ := body["messages"].([]any); len(msgs) >= 10 && strings.Contains(fmt.Sprint(msgs), "BYE sip:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("captured messages: %v", body)
		}
		time.Sleep(500 * time.Millisecond)
	}
	msgs := tr["messages"].([]any)
	legs := map[string]int{}
	var invites, sdp, bye int
	for _, x := range msgs {
		m := x.(map[string]any)
		legs[str(m["leg"])]++
		if m["method"] == "INVITE" {
			invites++
			if strings.Contains(str(m["raw"]), "m=audio") {
				sdp++
			}
		}
		if m["method"] == "BYE" {
			bye++
		}
	}
	if legs["customer"] == 0 || legs["carrier"] == 0 || invites < 2 || sdp < 2 || bye == 0 {
		t.Fatalf("legs %v invites %d with sdp %d bye %d", legs, invites, sdp, bye)
	}
	if str(tr["customer_call_id"]) == "" || len(tr["carrier_call_ids"].([]any)) != 1 {
		t.Fatalf("call ids: %v %v", tr["customer_call_id"], tr["carrier_call_ids"])
	}
	// pcap export: a valid libpcap header and one frame per message of the chosen legs
	for _, c := range []struct {
		leg  string
		want int
	}{{"all", len(msgs)}, {"customer", legs["customer"]}, {"carrier", legs["carrier"]}} {
		code, raw := a.do("GET", "/cdrs/"+id+"/sip.pcap?leg="+c.leg, nil)
		b := []byte(str(raw["raw"]))
		if code != 200 || len(b) < 24 || binary.LittleEndian.Uint32(b) != 0xa1b2c3d4 {
			t.Fatalf("pcap %s: %d %d bytes", c.leg, code, len(b))
		}
		n := 0
		for off := 24; off+16 <= len(b); n++ {
			off += 16 + int(binary.LittleEndian.Uint32(b[off+8:]))
		}
		if n != c.want {
			t.Fatalf("pcap %s: %d frames, want %d", c.leg, n, c.want)
		}
	}
	// the customer leg Call-ID is searchable on the CDR list
	code, list := a.do("GET", "/cdrs?sip_call_id="+str(tr["customer_call_id"]), nil)
	a.mustOK(code, list, "cdrs by call id")
	if n := len(items(list)); n != 1 {
		t.Fatalf("cdrs by sip_call_id: %d", n)
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
