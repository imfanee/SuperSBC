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
