package callcontrol

import (
	"math"
	"math/rand"
	"sort"

	"github.com/imfanee/supersbc/internal/model"
)

// orderable is what the ordering needs from a candidate.
type orderable struct {
	priority int
	weight   int // failover weight, or the percentage share in percent mode
	degraded bool
	buyRate  float64 // LCR key
	quality  float64 // quality tier (0..1, higher is better); neutral 0.5 when unknown
}

// orderCandidates returns the dial order (indexes into cands) for the
// routing modes of the route group (D-73).
//
// Priority mode (no flags): lowest priority first; within one priority,
// weighted random (a carrier with weight 200 is picked before one with
// weight 100 twice as often).
//
// LCR and quality build the sort key: quality tier first (best first) when
// quality is on, then buy rate (cheapest first) when LCR is on, then
// priority; the weighted shuffle applies inside equal keys. Lossless is a
// filter applied by the caller before ordering.
//
// Percent mode: the first carrier is drawn at random with probability equal
// to its share of the total weight, the rest follow by descending share as
// failover; over many calls the distribution converges to the shares.
//
// Degraded carriers (circuit breaker) move to the back in every mode,
// keeping their relative order.
func orderCandidates(cands []orderable, modes model.RouteModes, rnd *rand.Rand) []int {
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	var out []int
	if modes.Percent {
		out = percentOrder(cands, idx, rnd)
	} else {
		out = keyedOrder(cands, idx, modes, rnd)
	}
	// degraded last, stable
	healthy := make([]int, 0, len(out))
	degraded := make([]int, 0)
	for _, i := range out {
		if cands[i].degraded {
			degraded = append(degraded, i)
		} else {
			healthy = append(healthy, i)
		}
	}
	return append(healthy, degraded...)
}

type sortKey struct {
	quality float64 // negated tier so that ascending sort puts the best first
	rate    float64
	prio    int
}

func keyOf(c orderable, modes model.RouteModes) sortKey {
	k := sortKey{prio: c.priority}
	if modes.Quality {
		k.quality = -c.quality
	}
	if modes.LCR {
		k.rate = c.buyRate
	}
	return k
}

func keyedOrder(cands []orderable, idx []int, modes model.RouteModes, rnd *rand.Rand) []int {
	sort.SliceStable(idx, func(a, b int) bool {
		ka, kb := keyOf(cands[idx[a]], modes), keyOf(cands[idx[b]], modes)
		if ka.quality != kb.quality {
			return ka.quality < kb.quality
		}
		if ka.rate != kb.rate {
			return ka.rate < kb.rate
		}
		return ka.prio < kb.prio
	})
	out := make([]int, 0, len(idx))
	for start := 0; start < len(idx); {
		end := start + 1
		k0 := keyOf(cands[idx[start]], modes)
		for end < len(idx) && keyOf(cands[idx[end]], modes) == k0 {
			end++
		}
		out = append(out, weightedShuffle(cands, idx[start:end], rnd)...)
		start = end
	}
	return out
}

// weightedShuffle orders a group by repeated weighted random draws.
func weightedShuffle(cands []orderable, group []int, rnd *rand.Rand) []int {
	g := append([]int(nil), group...)
	out := make([]int, 0, len(g))
	for len(g) > 0 {
		total := 0
		for _, i := range g {
			total += max(cands[i].weight, 1)
		}
		pick := 0
		if rnd != nil && total > 0 {
			pick = rnd.Intn(total)
		}
		chosen := 0
		for j, i := range g {
			pick -= max(cands[i].weight, 1)
			if pick < 0 {
				chosen = j
				break
			}
		}
		out = append(out, g[chosen])
		g = append(g[:chosen], g[chosen+1:]...)
	}
	return out
}

// percentOrder draws the first carrier by share, then lists the others by
// descending share (priority breaks ties) as the failover chain.
func percentOrder(cands []orderable, idx []int, rnd *rand.Rand) []int {
	if len(idx) == 0 {
		return nil
	}
	rest := append([]int(nil), idx...)
	sort.SliceStable(rest, func(a, b int) bool {
		wa, wb := max(cands[rest[a]].weight, 0), max(cands[rest[b]].weight, 0)
		if wa != wb {
			return wa > wb
		}
		return cands[rest[a]].priority < cands[rest[b]].priority
	})
	total := 0
	for _, i := range rest {
		total += max(cands[i].weight, 0)
	}
	first := 0
	if total > 0 && rnd != nil {
		pick := rnd.Intn(total)
		for j, i := range rest {
			pick -= max(cands[i].weight, 0)
			if pick < 0 {
				first = j
				break
			}
		}
	}
	out := []int{rest[first]}
	return append(out, append(rest[:first:first], rest[first+1:]...)...)
}

// qualityTier rounds a score to the routing tier so small noise does not
// reorder carriers on every call.
func qualityTier(score float64) float64 { return math.Round(math.Round(score/0.05)*0.05*100) / 100 }
