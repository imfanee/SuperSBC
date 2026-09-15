//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// placeTLSCall is placeCall over SIP TLS to the ingress TLS port.
func placeTLSCall(t *testing.T, customer, scenarioPath, called string) callResult {
	return placeTLSCallWithCert(t, customer, scenarioPath, called, "cert.pem", "key.pem")
}

// placeTLSCallWithCert is placeTLSCall presenting the given client certificate (files in tests/e2e/out).
func placeTLSCallWithCert(t *testing.T, customer, scenarioPath, called, cert, key string) callResult {
	t.Helper()
	ip := customerIP[customer]
	logName := fmt.Sprintf("%s_%d.msg", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	args := []string{"--profile", "e2e-clients", "run", "--rm", "-T", customer,
		"-sf", scenarioPath, "-t", "l1", "-tls_cert", "/out/" + cert, "-tls_key", "/out/" + key,
		"-i", ip, "-p", "5061", "-mi", ip, "-mp", "7000", "-s", called, "-m", "1", "-l", "1", "-r", "1",
		"-nostdin", "-trace_msg", "-message_file", "/out/" + logName, "-trace_err", "-error_file", "/dev/null",
		"-cid_str", fmt.Sprintf("tls-%%u-%%p-%d@%%s", time.Now().UnixNano()), "-timeout", "60s", "172.28.0.10:5061"}
	cmd := compose(t, args...)
	_ = cmd.Run()
	return readCallResult(t, logName, customer, called)
}

// SIP over TLS: the CDR records the transport and require_tls is enforced.
func TestM5_TLS(t *testing.T) {
	copyTLSFiles(t)
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeTLSCall(t, "customer-acme", sc, "442071234567")
	expectFinal(t, res, "200 OK")
	if !strings.Contains(res.Messages, "SIP/2.0/TLS") {
		t.Fatal("call was not carried over TLS")
	}
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	expectField(t, cdr, "disposition", "answered")
	waitCDRField(t, str(cdr["call_uuid"]), "transport_in", "tls")

	// zeta-tls requires TLS: UDP is refused, TLS is accepted
	fail := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "403"})
	res = placeCall(t, "customer-zeta", fail, "442071234567", 1)
	expectFinal(t, res, "403 TLS required")
	res = placeTLSCall(t, "customer-zeta", sc, "442071234567")
	expectFinal(t, res, "200 OK")
}

// SRTP: a mandatory customer refuses plain offers and accepts crypto offers.
func TestM5_SRTP(t *testing.T) {
	since := time.Now()
	fail := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "488"})
	res := placeCall(t, "customer-eta", fail, "442071234567", 1)
	expectFinal(t, res, "488 SRTP required")
	sc := scenario(t, "uac_call_srtp.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res = placeCall(t, "customer-eta", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	ok200 := ""
	for _, block := range strings.Split(res.Messages, "----------------------------------------------- ") {
		if strings.Contains(block, "SIP/2.0 200 OK") && strings.Contains(block, "CSeq: 1 INVITE") {
			ok200 = block
		}
	}
	if !strings.Contains(ok200, "a=crypto:") || !strings.Contains(ok200, "RTP/SAVP") {
		t.Errorf("200 OK to the customer should carry an SRTP answer:\n%s", ok200)
	}
	cdr := lastCDR(t, "172.28.0.108", "442071234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "srtp_in", "true")
	// the carrier (srtp off) still receives plain RTP
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if strings.Contains(inv, "RTP/SAVP") || strings.Contains(inv, "a=crypto") {
		t.Errorf("carrier leg should be plain RTP:\n%s", inv)
	}
	// acme (srtp optional) accepts the crypto offer too
	res = placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
}

// Privacy: anonymised towards a default carrier, passed with PAI to a "pass" carrier.
func TestM5_Privacy(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call_privacy.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	expectField(t, cdr, "privacy", "true")
	expectField(t, cdr, "caller_number", "15550001111") // the CDR keeps the real number
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "From: \"Anonymous\" <sip:anonymous@") && !strings.Contains(inv, "<sip:anonymous@") {
		t.Errorf("carrier should see an anonymous From:\n%s", inv)
	}
	if !strings.Contains(inv, "Privacy: id") || strings.Contains(inv, "15550001111") {
		t.Errorf("carrier should get Privacy: id and never the real number:\n%s", inv)
	}
	// carrier-privacy-pass gets the real identity plus Privacy: id
	res = placeCall(t, "customer-acme", sc, "442471234567", 1)
	expectFinal(t, res, "200 OK")
	cdr = lastCDR(t, "172.28.0.101", "442471234567", since)
	inv = inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "Privacy: id") || !strings.Contains(inv, "P-Asserted-Identity: <sip:15550001111@172.28.0.10>") || !strings.Contains(inv, "sip:15550001111@172.28.0.10>;tag") {
		t.Errorf("pass mode should send real From, PAI and Privacy: id:\n%s", inv)
	}
}

// Header manipulation rules: add, passthrough, and response headers.
func TestM5_HeaderRules(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	answer := findByName(t, a, "/carriers", "carrier-answer")
	var ruleIDs []string
	mk := func(path string, body map[string]any) {
		code, r := a.do("POST", path, body)
		a.mustOK(code, r, "create rule")
		ruleIDs = append(ruleIDs, idOf(t, r))
	}
	mk("/customers/"+acme+"/header-rules", map[string]any{"direction": "egress", "action": "add", "header": "X-Account", "value": "acct-{customer}-{called}", "priority": 10})
	mk("/customers/"+acme+"/header-rules", map[string]any{"direction": "egress", "action": "passthrough", "header": "X-Trace", "priority": 20})
	mk("/customers/"+acme+"/header-rules", map[string]any{"direction": "response", "action": "add", "header": "X-Sbc", "value": "{node_ip}", "priority": 5})
	mk("/carriers/"+answer+"/header-rules", map[string]any{"direction": "egress", "action": "add", "header": "X-Carrier-Key", "value": "k1,k2", "priority": 5})
	defer func() {
		for _, id := range ruleIDs {
			a.do("DELETE", "/header-rules/"+id, nil)
		}
	}()
	time.Sleep(500 * time.Millisecond)
	since := time.Now()
	sc := scenario(t, "uac_call_xtrace.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442071234567", since)
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	for _, want := range []string{"X-Account: acct-acme-442071234567", "X-Trace: t-123", "X-Carrier-Key: k1,k2"} {
		if !strings.Contains(inv, want) {
			t.Errorf("carrier INVITE missing %q:\n%s", want, inv)
		}
	}
	if !strings.Contains(res.Messages, "X-Sbc: 172.28.0.10") {
		t.Errorf("customer responses should carry X-Sbc")
	}
	// remove rule on the carrier cancels the customer's passthrough
	code, r := a.do("POST", "/carriers/"+answer+"/header-rules", map[string]any{"direction": "egress", "action": "remove", "header": "X-Trace", "priority": 1})
	a.mustOK(code, r, "remove rule")
	ruleIDs = append(ruleIDs, idOf(t, r))
	time.Sleep(500 * time.Millisecond)
	res = placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr = lastCDR(t, "172.28.0.101", "442071234567", since)
	inv = inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if strings.Contains(inv, "X-Trace") {
		t.Errorf("remove rule did not cancel the passthrough:\n%s", inv)
	}
}

// Media bypass: the carrier receives the customer's media address and the CDR says bypass.
func TestM5_MediaBypass(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-theta", sc, "442371234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.109", "442371234567", since)
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "c=IN IP4 172.28.0.109") {
		t.Errorf("bypass should offer the customer's media address to the carrier:\n%s", inv)
	}
	waitCDRField(t, str(cdr["call_uuid"]), "media_mode", "bypass")
	// the same customer to an anchoring carrier stays anchored
	res = placeCall(t, "customer-theta", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr = lastCDR(t, "172.28.0.109", "442071234567", since)
	inv = inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "c=IN IP4 172.28.0.10\r") && !strings.Contains(inv, "c=IN IP4 172.28.0.10\n") {
		t.Errorf("anchored call should offer the SBC address:\n%s", inv)
	}
}

// DTMF interworking: the carrier's mode shapes the dial string (checked through the simulator).
func TestM5_DTMF(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	acme := findByName(t, a, "/customers", "acme")
	code, sim := a.do("GET", "/routing/simulate?customer_id="+acme+"&number=442071234567", nil)
	a.mustOK(code, sim, "simulate")
	dial := sim["carriers"].([]any)[0].(map[string]any)["dial_string"].(string)
	if !strings.Contains(dial, "dtmf_type=rfc2833") || !strings.Contains(dial, "rtp_secure_media=false") {
		t.Errorf("default dial string: %s", dial)
	}
	cid := findByName(t, a, "/carriers", "carrier-answer")
	code, c := a.do("GET", "/carriers/"+cid, nil)
	a.mustOK(code, c, "carrier")
	body := c["carrier"].(map[string]any)
	body["dtmf_mode"] = "inband"
	body["srtp_mode"] = "optional"
	code, _ = a.do("PUT", "/carriers/"+cid, body)
	a.mustOK(code, nil, "update carrier")
	defer func() {
		body["dtmf_mode"] = "rfc2833"
		body["srtp_mode"] = "off"
		a.do("PUT", "/carriers/"+cid, body)
	}()
	time.Sleep(300 * time.Millisecond)
	code, sim = a.do("GET", "/routing/simulate?customer_id="+acme+"&number=442071234567", nil)
	a.mustOK(code, sim, "simulate 2")
	dial = sim["carriers"].([]any)[0].(map[string]any)["dial_string"].(string)
	for _, want := range []string{"dtmf_type=none", "execute_on_answer_1=start_dtmf", "execute_on_answer_2=start_dtmf_generate", "rtp_secure_media=optional"} {
		if !strings.Contains(dial, want) {
			t.Errorf("inband dial string missing %s: %s", want, dial)
		}
	}
}

// Scanner protection: repeated unauthorised INVITEs ban the source in the FreeSWITCH ACL.
func TestM5_ScannerBan(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	a.do("DELETE", "/system/banned-ips/172.28.0.199", nil)
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "403"})
	res := placeCall(t, "customer-unknown", sc, "442071234567", 25) // threshold is 20 in 5 minutes
	expectFinal(t, res, "403 IP not authorized")
	deadline := time.Now().Add(10 * time.Second)
	banned := false
	for time.Now().Before(deadline) && !banned {
		code, l := a.do("GET", "/system/banned-ips", nil)
		a.mustOK(code, l, "bans")
		for _, b := range items(l) {
			if b["ip"] == "172.28.0.199" {
				banned = true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !banned {
		t.Fatal("source was not banned")
	}
	time.Sleep(1500 * time.Millisecond) // reloadacl
	res = placeCall(t, "customer-unknown", sc, "442071234567", 1)
	expectFinal(t, res, "403 Forbidden") // Sofia ACL, before the dialplan
	for _, l := range res.FinalLines {
		if l == "403 IP not authorized" {
			t.Errorf("banned source still reached the pipeline")
		}
	}
	code, _ := a.do("DELETE", "/system/banned-ips/172.28.0.199", nil)
	if code != 204 {
		t.Fatalf("unban gave %d", code)
	}
	time.Sleep(1500 * time.Millisecond)
	res = placeCall(t, "customer-unknown", sc, "442071234567", 1)
	expectFinal(t, res, "403 IP not authorized")
}
