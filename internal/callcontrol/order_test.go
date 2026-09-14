package callcontrol

import (
	"math/rand"
	"testing"
)

func TestOrderPriorityThenWeight(t *testing.T) {
	c := []orderable{
		{priority: 2, weight: 100},
		{priority: 1, weight: 100},
		{priority: 3, weight: 100},
	}
	got := orderCandidates(c, false, nil)
	if got[0] != 1 || got[1] != 0 || got[2] != 2 {
		t.Fatalf("priority order wrong: %v", got)
	}
}

func TestOrderDegradedLast(t *testing.T) {
	c := []orderable{
		{priority: 1, weight: 100, degraded: true},
		{priority: 2, weight: 100},
	}
	got := orderCandidates(c, false, nil)
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
		if orderCandidates(c, false, rnd)[0] == 0 {
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
	got := orderCandidates(c, true, nil)
	if got[0] != 1 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("lcr order wrong: %v", got)
	}
}
