package callcontrol

import (
	"math/rand"
	"sort"
)

// orderable is what the ordering needs from a candidate.
type orderable struct {
	priority int
	weight   int
	degraded bool
	buyRate  float64 // only used in LCR mode
}

// orderCandidates returns the dial order (indexes into cands) per Section 7:
// lowest priority first; within one priority, weighted random (a carrier
// with weight 200 is picked before one with weight 100 twice as often);
// degraded carriers move to the back keeping their relative order. In LCR
// mode the primary key is the buy rate instead of the priority.
func orderCandidates(cands []orderable, lcr bool, rnd *rand.Rand) []int {
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	key := func(i int) (float64, int) {
		if lcr {
			return cands[i].buyRate, 0
		}
		return 0, cands[i].priority
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ra, pa := key(idx[a])
		rb, pb := key(idx[b])
		if ra != rb {
			return ra < rb
		}
		return pa < pb
	})
	// weighted shuffle inside each equal-key group
	out := make([]int, 0, len(idx))
	for start := 0; start < len(idx); {
		end := start + 1
		r0, p0 := key(idx[start])
		for end < len(idx) {
			r, p := key(idx[end])
			if r != r0 || p != p0 {
				break
			}
			end++
		}
		group := append([]int(nil), idx[start:end]...)
		for len(group) > 0 {
			total := 0
			for _, i := range group {
				total += max(cands[i].weight, 1)
			}
			pick := 0
			if rnd != nil && total > 0 {
				pick = rnd.Intn(total)
			}
			chosen := 0
			for j, i := range group {
				pick -= max(cands[i].weight, 1)
				if pick < 0 {
					chosen = j
					break
				}
			}
			out = append(out, group[chosen])
			group = append(group[:chosen], group[chosen+1:]...)
		}
		start = end
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
