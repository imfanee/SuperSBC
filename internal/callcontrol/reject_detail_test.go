package callcontrol

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/model"
)

func TestNoRouteDetail(t *testing.T) {
	gid := uuid.New()
	cust := &model.Customer{RouteGroupID: &gid}
	route := &model.Route{Prefix: "24389", Destination: "DRC ORANGE"}

	t.Run("no route group", func(t *testing.T) {
		if d := noRouteDetail(&model.Customer{}, "24389", &RouteResult{}); !strings.Contains(d, "no route group") {
			t.Errorf("got %q", d)
		}
	})
	t.Run("no prefix matches", func(t *testing.T) {
		if d := noRouteDetail(cust, "24389", &RouteResult{}); !strings.Contains(d, "24389") {
			t.Errorf("got %q", d)
		}
	})
	t.Run("route without carriers", func(t *testing.T) {
		d := noRouteDetail(cust, "24389", &RouteResult{Route: route})
		if !strings.Contains(d, "no carrier attached") {
			t.Errorf("got %q", d)
		}
	})
	t.Run("every carrier skipped names the reason", func(t *testing.T) {
		d := noRouteDetail(cust, "24389", &RouteResult{Route: route, Skipped: []SkippedCarrier{
			{Name: "drc_avo", Reason: "lossless_skipped", Detail: "buy 0.220000 on 24389 is above sell 0.200000 on 24389"},
			{Name: "drc_africell", Reason: "gateway_down", Detail: "all 2 endpoints are down or parked"},
		}})
		for _, want := range []string{"24389", "DRC ORANGE", "drc_avo lossless_skipped", "buy 0.220000", "sell 0.200000", "drc_africell gateway_down"} {
			if !strings.Contains(d, want) {
				t.Errorf("detail %q is missing %q", d, want)
			}
		}
	})
}

func TestMarginDetail(t *testing.T) {
	buy := &model.Rate{Prefix: "24389", RatePerMin: decimal.RequireFromString("0.22")}
	sell := &model.Rate{Prefix: "2438", RatePerMin: decimal.RequireFromString("0.20")}
	d := marginDetail(buy, sell)
	if !strings.Contains(d, "buy 0.220000 on 24389") || !strings.Contains(d, "sell 0.200000 on 2438") {
		t.Errorf("got %q", d)
	}
	if marginDetail(nil, sell) != "" {
		t.Error("a missing rate should give no detail")
	}
}

func TestCalls(t *testing.T) {
	for n, want := range map[int64]string{0: "0 calls", 1: "1 call", 15: "15 calls"} {
		if got := calls(n); got != want {
			t.Errorf("calls(%d) = %q want %q", n, got, want)
		}
	}
}

func TestUnknownAddress(t *testing.T) {
	d := unknownAddress("203.0.113.9", 5060, "udp")
	for _, want := range []string{"203.0.113.9:5060", "udp", "allowed IPs"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail %q is missing %q", d, want)
		}
	}
}
