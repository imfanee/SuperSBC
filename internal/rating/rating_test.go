package rating

import (
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestBilledSecondsTable(t *testing.T) {
	type incs struct{ initial, subsequent int }
	increments := []incs{{1, 1}, {60, 60}, {30, 6}, {6, 6}}
	billsecs := []int{0, 1, 29, 30, 31, 59, 60, 61, 3599, 3600}
	minDurations := []int{0, 3}

	// expected[inc][min][billsec]
	expected := map[string]int{}
	set := func(i incs, m, b, want int) {
		expected[fmt.Sprintf("%d/%d m%d b%d", i.initial, i.subsequent, m, b)] = want
	}

	// 1/1: identity, except grace
	for _, m := range minDurations {
		for _, b := range billsecs {
			want := b
			if b <= 0 || b < m {
				want = 0
			}
			set(incs{1, 1}, m, b, want)
		}
	}
	// 60/60
	w6060 := map[int]int{0: 0, 1: 60, 29: 60, 30: 60, 31: 60, 59: 60, 60: 60, 61: 120, 3599: 3600, 3600: 3600}
	// 30/6
	w306 := map[int]int{0: 0, 1: 30, 29: 30, 30: 30, 31: 36, 59: 60, 60: 60, 61: 66, 3599: 3600, 3600: 3600}
	// 6/6
	w66 := map[int]int{0: 0, 1: 6, 29: 30, 30: 30, 31: 36, 59: 60, 60: 60, 61: 66, 3599: 3600, 3600: 3600}
	for _, m := range minDurations {
		for _, b := range billsecs {
			for i, table := range map[incs]map[int]int{{60, 60}: w6060, {30, 6}: w306, {6, 6}: w66} {
				want := table[b]
				if b < m {
					want = 0
				}
				set(i, m, b, want)
			}
		}
	}

	for _, i := range increments {
		for _, m := range minDurations {
			for _, b := range billsecs {
				key := fmt.Sprintf("%d/%d m%d b%d", i.initial, i.subsequent, m, b)
				got := BilledSeconds(b, Increments{Initial: i.initial, Subsequent: i.subsequent, MinDuration: m})
				if got != expected[key] {
					t.Errorf("%s: got %d want %d", key, got, expected[key])
				}
			}
		}
	}
}

func TestAmount(t *testing.T) {
	cases := []struct {
		billed   int
		rate     string
		fee      string
		expected string
	}{
		{0, "0.02", "0", "0.000000"},
		{0, "0.02", "0.01", "0.000000"}, // no connect fee on unbilled call
		{60, "0.02", "0", "0.020000"},
		{60, "0.02", "0.01", "0.030000"},
		{1, "0.02", "0", "0.000333"}, // 0.02/60 = 0.000333.. rounds to 0.000333
		{30, "0.05", "0", "0.025000"},
		{66, "0.0123", "0", "0.013530"},
		{3600, "0.02", "0.01", "1.210000"},
		{1, "0.00001", "0", "0.000000"}, // 0.000000166 rounds down
		{1, "0.00003", "0", "0.000001"}, // 0.0000005 rounds half-up to 0.000001
	}
	for _, c := range cases {
		got := Amount(c.billed, d(c.rate), d(c.fee))
		if got.StringFixed(6) != c.expected {
			t.Errorf("Amount(%d, %s, %s) = %s want %s", c.billed, c.rate, c.fee, got.StringFixed(6), c.expected)
		}
	}
}

func TestReserveAmount(t *testing.T) {
	r := Rate{PerMinute: d("0.02"), ConnectFee: d("0"), Increments: Increments{Initial: 60, Subsequent: 60}}
	if got := ReserveAmount(5, r); got.StringFixed(6) != "0.100000" {
		t.Fatalf("reserve = %s", got)
	}
	r.ConnectFee = d("0.01")
	if got := ReserveAmount(5, r); got.StringFixed(6) != "0.110000" {
		t.Fatalf("reserve with fee = %s", got)
	}
}

func TestMaxCallSeconds(t *testing.T) {
	r := Rate{PerMinute: d("0.02"), Increments: Increments{Initial: 60, Subsequent: 60}}
	if got := MaxCallSeconds(d("10"), r, 14400); got != 14400 {
		t.Fatalf("10 USD at 0.02 should hit the cap, got %d", got)
	}
	if got := MaxCallSeconds(d("0.10"), r, 14400); got != 300 {
		t.Fatalf("0.10 at 0.02/min = 300 s, got %d", got)
	}
	if got := MaxCallSeconds(d("0.10"), Rate{PerMinute: d("0.02"), ConnectFee: d("0.02")}, 14400); got != 240 {
		t.Fatalf("connect fee reduces talk time, got %d", got)
	}
	if got := MaxCallSeconds(d("0"), r, 14400); got != 0 {
		t.Fatalf("no money no time, got %d", got)
	}
	if got := MaxCallSeconds(d("5"), Rate{}, 14400); got != 14400 {
		t.Fatalf("free rate should return cap, got %d", got)
	}
	if got := MaxCallSeconds(d("0.001"), r, 14400); got != 3 {
		t.Fatalf("0.001 at 0.02/min = 3 s, got %d", got)
	}
}
