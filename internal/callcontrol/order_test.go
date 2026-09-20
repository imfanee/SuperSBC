package callcontrol

import (
	"math"
	"math/rand"
	"testing"

	"github.com/imfanee/supersbc/internal/model"
)

func TestOrderPriorityThenWeight(t *testing.T) {
	c := []orderable{
		{priority: 2, weight: 100},
		{priority: 1, weight: 100},
		{priority: 3, weight: 100},
	}
	got := orderCandidates(c, model.RouteModes{}, nil)
	if got[0] != 1 || got[1] != 0 || got[2] != 2 {
		t.Fatalf("priority order wrong: %v", got)
	}
}

func TestOrderDegradedLast(t *testing.T) {
	c := []orderable{
		{priority: 1, weight: 100, degraded: true},
		{priority: 2, weight: 100},
	}
	got := orderCandidates(c, model.RouteModes{}, nil)
	if got[0] != 1 || got[1] != 0 {
		t.Fatalf("degraded should be last: %v", got)
	}
}

func TestOrderWeightedDistribution(t *testing.T) {
	c := []orderable{
		{priority: 1, weight: 300},
		{priority: 1, weight: 100},
	}
	rnd := rand.New(rand.NewSource(42))
	first := 0
	const n = 20000
	for i := 0; i < n; i++ {
		if orderCandidates(c, model.RouteModes{}, rnd)[0] == 0 {
			first++
		}
	}
	share := float64(first) / n
	if share < 0.72 || share > 0.78 {
		t.Fatalf("weight 300 vs 100 should be first ~75%% of the time, got %.3f", share)
	}
}

func TestOrderLCR(t *testing.T) {
	c := []orderable{
		{priority: 1, weight: 100, buyRate: 0.02},
		{priority: 2, weight: 100, buyRate: 0.01},
		{priority: 3, weight: 100, buyRate: 0.015},
	}
	got := orderCandidates(c, model.RouteModes{LCR: true}, nil)
	if got[0] != 1 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("lcr order wrong: %v", got)
	}
}

// D-73: quality first, then LCR inside a tier, then priority; and every
// combination of the three flags keeps a defined order.
func TestOrderQualityAndLCR(t *testing.T) {
	c := []orderable{
		{priority: 1, weight: 100, buyRate: 0.010, quality: 0.60}, // cheap but mediocre
		{priority: 2, weight: 100, buyRate: 0.015, quality: 0.95}, // best quality, pricier
		{priority: 3, weight: 100, buyRate: 0.012, quality: 0.95}, // best quality, cheaper than 1
	}
	if got := orderCandidates(c, model.RouteModes{Quality: true}, nil); got[0] != 1 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("quality only: %v", got) // same tier: priority decides
	}
	if got := orderCandidates(c, model.RouteModes{Quality: true, LCR: true}, nil); got[0] != 2 || got[1] != 1 || got[2] != 0 {
		t.Fatalf("quality+lcr: %v", got) // inside the 0.95 tier, cheapest first
	}
	if got := orderCandidates(c, model.RouteModes{LCR: true}, nil); got[0] != 0 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("lcr only: %v", got)
	}
	if got := orderCandidates(c, model.RouteModes{Lossless: true}, nil); got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("lossless alone changes nothing in the order: %v", got)
	}
	// degraded still last even with the best quality
	c[1].degraded = true
	if got := orderCandidates(c, model.RouteModes{Quality: true, LCR: true}, nil); got[2] != 1 {
		t.Fatalf("degraded last: %v", got)
	}
	if qualityTier(0.874) != 0.85 || qualityTier(0.876) != 0.9 {
		t.Fatal("tier rounding")
	}
}

// Percent mode: the first carrier follows the shares (within 1 percent over
// 100k draws) and the rest form a failover chain by descending share.
func TestOrderPercent(t *testing.T) {
	c := []orderable{{priority: 1, weight: 20}, {priority: 2, weight: 50}, {priority: 3, weight: 30}}
	rnd := rand.New(rand.NewSource(7))
	n := 100000
	first := map[int]int{}
	for i := 0; i < n; i++ {
		got := orderCandidates(c, model.RouteModes{Percent: true}, rnd)
		first[got[0]]++
		if len(got) != 3 {
			t.Fatal("all carriers must remain for failover")
		}
		// failover chain is the others by descending share
		rest := got[1:]
		if got[0] == 1 && (rest[0] != 2 || rest[1] != 0) {
			t.Fatalf("failover chain after carrier 1: %v", got)
		}
	}
	for i, want := range []float64{0.20, 0.50, 0.30} {
		if p := float64(first[i]) / float64(n); math.Abs(p-want) > 0.01 {
			t.Errorf("carrier %d share %.3f want %.2f", i, p, want)
		}
	}
	// zero shares: deterministic by priority
	z := []orderable{{priority: 2}, {priority: 1}}
	if got := orderCandidates(z, model.RouteModes{Percent: true}, rnd); got[0] != 1 {
		t.Fatalf("zero shares: %v", got)
	}
	if !(model.RouteModes{LCR: true, Lossless: true, Quality: true}).Valid() || (model.RouteModes{Percent: true, LCR: true}).Valid() {
		t.Fatal("mode validity")
	}
}
