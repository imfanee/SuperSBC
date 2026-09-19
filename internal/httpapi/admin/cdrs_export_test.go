package admin

import (
	"strings"
	"testing"
)

func TestCDRViews(t *testing.T) {
	full, ok := cdrColumnsFor("")
	if !ok || len(full) != len(cdrColumns) {
		t.Fatal("full view")
	}
	if _, ok := cdrColumnsFor("bogus"); ok {
		t.Fatal("bogus view accepted")
	}
	names := func(view string) string {
		cols, ok := cdrColumnsFor(view)
		if !ok {
			t.Fatalf("view %s", view)
		}
		var n []string
		for _, c := range cols {
			if c.get == nil {
				t.Fatalf("view %s references an unknown column", view)
			}
			n = append(n, c.name)
		}
		return " " + strings.Join(n, " ") + " "
	}
	cust := names("customer")
	for _, forbidden := range []string{" carrier ", " buy_rate_per_min ", " cost ", " margin ", " failover_depth ", " attempts ", " codec_out ", " transport_out ", " sbc_node "} {
		if strings.Contains(cust, forbidden) {
			t.Errorf("customer view leaks %q", forbidden)
		}
	}
	for _, required := range []string{" customer ", " sell_price ", " caller ", " called ", " billsec "} {
		if !strings.Contains(cust, required) {
			t.Errorf("customer view misses %q", required)
		}
	}
	car := names("carrier")
	for _, forbidden := range []string{" customer ", " src_ip ", " sell_rate_per_min ", " sell_price ", " margin ", " failover_depth ", " attempts ", " caller_raw ", " codec_in ", " sbc_node "} {
		if strings.Contains(car, forbidden) {
			t.Errorf("carrier view leaks %q", forbidden)
		}
	}
	for _, required := range []string{" carrier ", " cost ", " buy_rate_per_min ", " called ", " billsec "} {
		if !strings.Contains(car, required) {
			t.Errorf("carrier view misses %q", required)
		}
	}
}
