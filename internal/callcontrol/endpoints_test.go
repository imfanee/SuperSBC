package callcontrol

import (
	"math"
	"math/rand"
	"testing"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/failover"
	"github.com/imfanee/supersbc/internal/model"
)

func eps(n int) []model.CarrierEndpoint {
	out := make([]model.CarrierEndpoint, n)
	for i := range out {
		out[i] = model.CarrierEndpoint{ID: uuid.New(), Seq: i + 1, Host: "10.0.0." + string(rune('1'+i)), Port: 5060, Priority: i + 1, Weight: 100, Enabled: true}
	}
	return out
}

// D-75: endpoints are ordered by the carrier's load balancing mode, and
// disabled, parked or DOWN addresses never appear.
func TestUsableEndpoints(t *testing.T) {
	c := model.Carrier{Name: "acme", EndpointLB: "failover"}
	list := eps(3)
	rnd := rand.New(rand.NewSource(1))
	got := UsableEndpoints(c, list, nil, nil, 0, rnd)
	if len(got) != 3 || got[0].Seq != 1 || got[2].Seq != 3 {
		t.Fatalf("failover order: %+v", got)
	}
	// a parked endpoint and a disabled one drop out, the rest stay in order
	list[0].Enabled = false
	dead := map[uuid.UUID]bool{list[1].ID: true}
	got = UsableEndpoints(c, list, nil, dead, 0, rnd)
	if len(got) != 1 || got[0].Seq != 3 {
		t.Fatalf("filtered: %+v", got)
	}
	// a DOWN gateway drops out too; with several endpoints the gateway name is suffixed
	list = eps(2)
	down := func(gw string) bool { return gw == "acme-ep1" }
	got = UsableEndpoints(c, list, down, nil, 0, rnd)
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("gateway down: %+v", got)
	}
	// a single endpoint keeps the plain carrier name
	one := eps(1)
	if one[0].GatewayName("acme", true) != "acme" || list[0].GatewayName("acme", false) != "acme-ep1" {
		t.Fatal("gateway naming")
	}
	// every endpoint is returned so a failed call rolls over, capped by max
	if got = UsableEndpoints(c, eps(5), nil, nil, 3, rnd); len(got) != 3 {
		t.Fatalf("cap: %d", len(got))
	}
	// nothing usable
	if got = UsableEndpoints(c, nil, nil, nil, 0, rnd); got != nil {
		t.Fatalf("empty: %+v", got)
	}
}

// Weighted and round robin spread the first attempt over the endpoints while
// keeping the others behind it as failover.
func TestUsableEndpointsBalancing(t *testing.T) {
	list := eps(2)
	list[0].Weight, list[1].Weight = 80, 20
	rnd := rand.New(rand.NewSource(3))
	first := map[int]int{}
	for i := 0; i < 20000; i++ {
		got := UsableEndpoints(model.Carrier{Name: "acme", EndpointLB: "weighted"}, list, nil, nil, 0, rnd)
		if len(got) != 2 {
			t.Fatalf("all endpoints must remain for failover: %+v", got)
		}
		first[got[0].Seq]++
	}
	if p := float64(first[1]) / 20000; math.Abs(p-0.8) > 0.02 {
		t.Errorf("weighted share %.3f want 0.80", p)
	}
	first = map[int]int{}
	for i := 0; i < 20000; i++ {
		first[UsableEndpoints(model.Carrier{Name: "acme", EndpointLB: "round_robin"}, list, nil, nil, 0, rnd)[0].Seq]++
	}
	if p := float64(first[1]) / 20000; math.Abs(p-0.5) > 0.02 {
		t.Errorf("round robin share %.3f want 0.50 (weights ignored)", p)
	}
}

// D-75: an answered call or a far end rejection means the address worked, a
// caller who gave up tells us nothing, and anything else counts against it.
func TestEndpointOutcome(t *testing.T) {
	if rec, ok := EndpointOutcome(failover.Answered, true); !rec || !ok {
		t.Error("an answered attempt is proof of life")
	}
	if rec, ok := EndpointOutcome(failover.NumberFault, false); !rec || !ok {
		t.Error("the far end rejecting the number proves it answered")
	}
	if rec, _ := EndpointOutcome(failover.Cancelled, false); rec {
		t.Error("a caller who gave up tells us nothing about the address")
	}
	if rec, ok := EndpointOutcome(failover.CarrierFault, false); !rec || ok {
		t.Error("a call that never reached the far end counts against the address")
	}
}
