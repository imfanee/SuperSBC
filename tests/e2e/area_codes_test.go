//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Area codes (D-76): the called number is labelled with its area and operator
// when the CDR is created, the label follows the longest matching prefix, and
// existing CDRs can be relabelled after the table changes.
func TestRoadmap_AreaCodes(t *testing.T) {
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	// the shipped table covers the DRC and India
	code, look := a.do("GET", "/area-codes/lookup?number=243810001234", nil)
	a.mustOK(code, look, "lookup")
	m, _ := look["match"].(map[string]any)
	if m == nil || m["prefix"] != "24381" || m["operator"] != "Vodacom Congo" {
		t.Fatalf("DRC mobile lookup: %v", look["match"])
	}
	code, look = a.do("GET", "/area-codes/lookup?number=912212345678", nil)
	a.mustOK(code, look, "lookup india")
	m, _ = look["match"].(map[string]any)
	if m == nil || m["prefix"] != "9122" || m["description"] != "Mumbai" {
		t.Fatalf("longest prefix should win over +91 9: %v", look["match"])
	}

	// a call to a labelled range carries the label on its CDR
	stamp := fmt.Sprint(time.Now().UnixNano() % 1000)
	code, added := a.do("POST", "/area-codes", map[string]any{"country_code": "44", "area_code": "2071" + stamp[:1],
		"country": "United Kingdom", "description": "London e2e", "operator": "E2E Telecom", "kind": "fixed"})
	a.mustOK(code, added, "add area code")
	id := str(added["id"])
	defer a.do("DELETE", "/area-codes/"+id, nil)
	time.Sleep(500 * time.Millisecond)
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "500"})
	called := "4420 71" + stamp[:1] + "34567"
	called = strings.ReplaceAll(called, " ", "")
	expectFinal(t, placeCall(t, "customer-acme", sc, called, 1), "200 OK")
	cdr := lastCDR(t, "172.28.0.101", called, since)
	expectField(t, cdr, "area_description", "London e2e")
	expectField(t, cdr, "operator_name", "E2E Telecom")

	// a CDR written before the entry existed is labelled by the backfill
	sqlExec(t, fmt.Sprintf(`UPDATE cdrs SET area_prefix = NULL, area_description = NULL, operator_name = NULL WHERE call_uuid = '%s'`, str(cdr["call_uuid"])))
	code, res := a.do("POST", "/area-codes/backfill", nil)
	a.mustOK(code, res, "backfill")
	if res["updated"].(float64) < 1 {
		t.Fatalf("backfill updated nothing: %v", res)
	}
	rows := sql(t, fmt.Sprintf(`SELECT area_description, operator_name FROM cdrs WHERE call_uuid = '%s'`, str(cdr["call_uuid"])))
	if len(rows) != 1 || str(rows[0]["area_description"]) != "London e2e" || str(rows[0]["operator_name"]) != "E2E Telecom" {
		t.Fatalf("backfilled CDR: %v", rows)
	}

	// CSV import replaces an entry and the change takes effect at once
	csv := "country_code,area_code,description,operator,country,kind\n44,2071" + stamp[:1] + ",London imported,Imported Telecom,United Kingdom,fixed\n"
	code, imp := a.upload("/area-codes/import", csv)
	a.mustOK(code, imp, "import")
	if imp["imported"].(float64) != 1 || imp["invalid"].(float64) != 0 {
		t.Fatalf("import: %v", imp)
	}
	time.Sleep(500 * time.Millisecond)
	code, look = a.do("GET", "/area-codes/lookup?number="+called, nil)
	a.mustOK(code, look, "lookup after import")
	m, _ = look["match"].(map[string]any)
	if m == nil || m["operator"] != "Imported Telecom" {
		t.Fatalf("import did not take effect: %v", look["match"])
	}
}

// Rejection detail (D-77): every rejected call records where setup stopped and
// what an operator has to change, not only what the caller was told.
func TestRoadmap_RejectDetail(t *testing.T) {
	since := time.Now()
	// a destination whose route has no usable carrier
	sc := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "503"})
	expectFinal(t, placeCall(t, "customer-acme", sc, "33123456789", 1), "503 No route")
	cdr := lastCDR(t, "172.28.0.101", "33123456789", since)
	expectField(t, cdr, "reject_step", "route")
	detail := str(cdr["reject_detail"])
	if detail == "" || !strings.Contains(detail, "33") {
		t.Errorf("no route detail should name the route: %q", detail)
	}

	// a destination with no selling rate
	sc404 := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "404"})
	expectFinal(t, placeCall(t, "customer-acme", sc404, "99123456789", 1), "404 No rate for destination")
	cdr = lastCDR(t, "172.28.0.101", "99123456789", since)
	expectField(t, cdr, "reject_step", "rate")
	if d := str(cdr["reject_detail"]); !strings.Contains(d, "99123456789") {
		t.Errorf("rate detail should name the number: %q", d)
	}

	// a number that cannot be normalised
	sc484 := scenario(t, "uac_expect_fail.xml.tmpl", map[string]string{"__CODE__": "484"})
	expectFinal(t, placeCall(t, "customer-acme", sc484, "12", 1), "484 Address Incomplete")
	cdr = lastCDR(t, "172.28.0.101", "12", since)
	expectField(t, cdr, "reject_step", "normalize")
	if d := str(cdr["reject_detail"]); d == "" {
		t.Error("normalise rejection should explain itself")
	}
	reconcileOK(t)
}

// Web brute force protection (D-81): an unknown address that keeps guessing
// lands on the ban list the firewall drops, a customer address that does the
// same is only flagged, because a ban would take their calls down with it.
func TestRoadmap_AuthAbuseBan(t *testing.T) {
	// this test deliberately fails logins, so it clears the attempt history on
	// the way in and out: the per address throttle would otherwise lock the
	// next test out of the admin account
	sqlExec(t, `DELETE FROM login_attempts`)
	defer sqlExec(t, `DELETE FROM login_attempts`)
	defer sqlExec(t, `DELETE FROM banned_ips WHERE reason LIKE 'web %'`)
	// fresh addresses each run, so a counter left in Redis by an earlier run
	// cannot decide the outcome, and an address nobody uses for the guesses
	// so the admin account stays usable
	stamp := time.Now().UnixNano()
	attacker := fmt.Sprintf("198.51.100.%d", 2+stamp%250)
	customerIP := fmt.Sprintf("203.0.113.%d", 2+(stamp/251)%250)
	guessed := fmt.Sprintf("nobody-%d@example.com", stamp%10000)

	admin := newAPIClient(t)
	admin.ok(admin.login("admin@example.com", adminPassword(t)))
	code, list := admin.do("GET", "/customers?search=acme", nil)
	admin.mustOK(code, list, "customers")
	custID := str(list["items"].([]any)[0].(map[string]any)["id"])
	code, added := admin.do("POST", "/customers/"+custID+"/ips", map[string]any{"ip_cidr": customerIP + "/32", "transport": "udp"})
	admin.mustOK(code, added, "add customer ip")
	defer admin.do("DELETE", "/customers/"+custID+"/ips/"+str(added["id"]), nil)

	flood := func(ip string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			c := newAPIClient(t)
			c.header("X-Forwarded-For", ip)
			code, _ := c.do("POST", "/auth/login", map[string]any{"email": guessed, "password": "nope"})
			if code != 401 && code != 429 {
				t.Fatalf("a wrong password should be refused, got %d", code)
			}
		}
	}

	flood(attacker, 45)
	rows := sql(t, `SELECT reason, hits FROM banned_ips WHERE ip = '`+attacker+`'::inet`)
	if len(rows) != 1 || !strings.Contains(str(rows[0]["reason"]), "sign in") {
		t.Fatalf("an unknown address that guesses 45 times should be banned: %v", rows)
	}

	flood(customerIP, 45)
	if rows := sql(t, `SELECT ip FROM banned_ips WHERE ip = '`+customerIP+`'::inet`); len(rows) != 0 {
		t.Errorf("a customer address must not be banned, its SIP would go down with it: %v", rows)
	}
	notes := sql(t, `SELECT payload->>'ip' AS ip FROM notifications WHERE kind = 'auth_abuse_known_source' ORDER BY id DESC LIMIT 1`)
	if len(notes) != 1 || str(notes[0]["ip"]) != customerIP {
		t.Errorf("abuse from a customer address should be raised for an operator instead: %v", notes)
	}
}
