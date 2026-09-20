// Package quality keeps a rolling quality score per carrier and route
// prefix for quality based routing (D-73).
//
// Recording is one pipelined Redis HINCRBY group per attempt into an hourly
// bucket keyed by carrier and route prefix (bounded key space: routes times
// carriers, not dialled numbers), with a 26 hour TTL. Scores are computed by
// a background refresh every RefreshEvery from the last WindowHours buckets
// with exponential decay (the newest hour weighs most) and kept in memory,
// so the routing hot path reads a map and never touches Redis.
package quality

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Config tunes the tracker.
type Config struct {
	MinSamples   int           // attempts needed on a prefix before its own score is used (default 20)
	WindowHours  int           // hours of history considered (default 24)
	RefreshEvery time.Duration // score refresh period (default 30 s)
}

// Outcome is what an attempt contributes.
type Outcome struct {
	Answered bool
	// Reached is true when the callee side responded normally (answered, busy,
	// no answer, cancelled): counts for NER but not ASR.
	Reached bool
	// Fault is a carrier or network fault.
	Fault   bool
	PDDMs   int // 0 when unknown
	Billsec int
}

// Score is a computed quality with its inputs, for the API and the simulator.
type Score struct {
	Carrier  uuid.UUID `json:"carrier_id"`
	Prefix   string    `json:"prefix"`
	Score    float64   `json:"score"`   // 0..1
	Tier     float64   `json:"tier"`    // score rounded to 0.05, what routing orders by
	Samples  int64     `json:"samples"` // decayed attempts in the window
	ASR      float64   `json:"asr"`
	NER      float64   `json:"ner"`
	PDDMs    float64   `json:"pdd_ms"`
	Faults   int64     `json:"faults"`
	Fallback string    `json:"fallback,omitempty"` // "carrier" or "neutral" when the prefix had too few samples
}

// Tracker records outcomes and serves scores.
type Tracker struct {
	rdb *redis.Client
	cfg Config
	log *slog.Logger
	now func() time.Time

	mu     sync.RWMutex
	scores map[string]Score // "carrier|prefix" and "carrier|" (carrier wide)
}

// New creates a tracker.
func New(rdb *redis.Client, cfg Config, log *slog.Logger) *Tracker {
	if cfg.MinSamples <= 0 {
		cfg.MinSamples = 20
	}
	if cfg.WindowHours <= 0 {
		cfg.WindowHours = 24
	}
	if cfg.RefreshEvery <= 0 {
		cfg.RefreshEvery = 30 * time.Second
	}
	return &Tracker{rdb: rdb, cfg: cfg, log: log, now: time.Now, scores: map[string]Score{}}
}

const (
	pairsKey  = "q:pairs"
	neutral   = 0.5
	tierWidth = 0.05
)

func key(carrier uuid.UUID, prefix string, hour int64) string {
	return fmt.Sprintf("q:%s:%s:%d", carrier, prefix, hour)
}

// Record adds one attempt outcome for a carrier on a route prefix (and to
// the carrier wide aggregate, prefix ""), fire and forget.
func (t *Tracker) Record(ctx context.Context, carrier uuid.UUID, prefix string, o Outcome) {
	if t == nil || t.rdb == nil {
		return
	}
	hour := t.now().Unix() / 3600
	pipe := t.rdb.Pipeline()
	for _, p := range []string{prefix, ""} {
		k := key(carrier, p, hour)
		pipe.HIncrBy(ctx, k, "att", 1)
		if o.Answered {
			pipe.HIncrBy(ctx, k, "ans", 1)
			pipe.HIncrBy(ctx, k, "billsec", int64(o.Billsec))
		}
		if o.Reached {
			pipe.HIncrBy(ctx, k, "reached", 1)
		}
		if o.Fault {
			pipe.HIncrBy(ctx, k, "fault", 1)
		}
		if o.PDDMs > 0 {
			pipe.HIncrBy(ctx, k, "pdd_sum", int64(o.PDDMs))
			pipe.HIncrBy(ctx, k, "pdd_n", 1)
		}
		pipe.Expire(ctx, k, time.Duration(t.cfg.WindowHours+2)*time.Hour)
		pipe.SAdd(ctx, pairsKey, carrier.String()+"|"+p)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.log.Warn("quality record", "error", err)
	}
}

// Lookup returns the score used for routing a carrier on a prefix: the
// prefix score when it has enough samples, else the carrier wide score,
// else neutral. ok is false when nothing is known at all.
func (t *Tracker) Lookup(carrier uuid.UUID, prefix string) (Score, bool) {
	if t == nil {
		return Score{Score: neutral, Tier: neutral}, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.scores[carrier.String()+"|"+prefix]; ok && s.Samples >= int64(t.cfg.MinSamples) {
		return s, true
	}
	if s, ok := t.scores[carrier.String()+"|"]; ok && s.Samples >= int64(t.cfg.MinSamples) {
		s.Prefix, s.Fallback = prefix, "carrier"
		return s, true
	}
	return Score{Carrier: carrier, Prefix: prefix, Score: neutral, Tier: neutral, Fallback: "neutral"}, false
}

// Scores returns every known score of a carrier (all prefixes), best first.
func (t *Tracker) Scores(carrier uuid.UUID) []Score {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []Score
	for k, s := range t.scores {
		if len(k) > 37 && k[:36] == carrier.String() && k[37:] != "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if out == nil {
		out = []Score{}
	}
	return out
}

// Run refreshes the scores until ctx ends.
func (t *Tracker) Run(ctx context.Context) {
	if t == nil || t.rdb == nil {
		return
	}
	tk := time.NewTicker(t.cfg.RefreshEvery)
	defer tk.Stop()
	for {
		if err := t.Refresh(ctx); err != nil && ctx.Err() == nil {
			t.log.Warn("quality refresh", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// Refresh recomputes every score from Redis in one pipelined round trip.
func (t *Tracker) Refresh(ctx context.Context) error {
	pairs, err := t.rdb.SMembers(ctx, pairsKey).Result()
	if err != nil {
		return err
	}
	hour := t.now().Unix() / 3600
	pipe := t.rdb.Pipeline()
	type cell struct {
		pair string
		age  int
		cmd  *redis.MapStringStringCmd
	}
	cells := make([]cell, 0, len(pairs)*t.cfg.WindowHours)
	for _, pr := range pairs {
		carrierS, prefix := cut(pr)
		carrier, err := uuid.Parse(carrierS)
		if err != nil {
			continue
		}
		for age := 0; age < t.cfg.WindowHours; age++ {
			cells = append(cells, cell{pair: pr, age: age, cmd: pipe.HGetAll(ctx, key(carrier, prefix, hour-int64(age)))})
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return err
	}
	type acc struct {
		att, ans, reached, fault, pddSum, pddN float64
		rawAtt                                 int64
	}
	accs := map[string]*acc{}
	for _, c := range cells {
		m := c.cmd.Val()
		if len(m) == 0 {
			continue
		}
		w := math.Pow(0.85, float64(c.age)) // newest hour weighs most
		a := accs[c.pair]
		if a == nil {
			a = &acc{}
			accs[c.pair] = a
		}
		att := f(m["att"])
		a.rawAtt += int64(att)
		a.att += w * att
		a.ans += w * f(m["ans"])
		a.reached += w * f(m["reached"])
		a.fault += w * f(m["fault"])
		a.pddSum += w * f(m["pdd_sum"])
		a.pddN += w * f(m["pdd_n"])
	}
	next := make(map[string]Score, len(accs))
	var stale []any
	for _, pr := range pairs {
		a := accs[pr]
		if a == nil || a.rawAtt == 0 {
			stale = append(stale, pr)
			continue
		}
		carrierS, prefix := cut(pr)
		carrier, _ := uuid.Parse(carrierS)
		asr := a.ans / a.att
		ner := a.reached / a.att
		pdd := 0.0
		if a.pddN > 0 {
			pdd = a.pddSum / a.pddN
		}
		// PDD term: 0 ms is perfect, 8 s (the progress timeout) and above is worst.
		pddTerm := 1 - math.Min(pdd, 8000)/8000
		score := 0.5*asr + 0.3*ner + 0.2*pddTerm
		// consecutive style penalty: a fault heavy window loses up to 0.2
		if a.att > 0 {
			score -= 0.2 * (a.fault / a.att)
		}
		score = math.Max(0, math.Min(1, score))
		next[pr] = Score{Carrier: carrier, Prefix: prefix, Score: round(score, 4), Tier: math.Round(math.Round(score/tierWidth)*tierWidth*100) / 100,
			Samples: a.rawAtt, ASR: round(asr, 4), NER: round(ner, 4), PDDMs: round(pdd, 0), Faults: int64(a.fault + 0.5)}
	}
	if len(stale) > 0 {
		_ = t.rdb.SRem(ctx, pairsKey, stale...).Err()
	}
	t.mu.Lock()
	t.scores = next
	t.mu.Unlock()
	return nil
}

func cut(pair string) (string, string) {
	for i := 0; i < len(pair); i++ {
		if pair[i] == '|' {
			return pair[:i], pair[i+1:]
		}
	}
	return pair, ""
}

func f(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
