//go:build integration

package quality

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Integration style test against the compose Redis when reachable
// (SBC_TEST_REDIS_URL, as the integration suite uses); otherwise the pure
// parts only.
func TestIntegrationQualityTracker(t *testing.T) {
	url := os.Getenv("SBC_TEST_REDIS_URL")
	if url == "" {
		t.Skip("SBC_TEST_REDIS_URL not set")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	ctx := context.Background()
	good, bad, fresh := uuid.New(), uuid.New(), uuid.New()
	tr := New(rdb, Config{MinSamples: 5, WindowHours: 3, RefreshEvery: time.Hour}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	for i := 0; i < 10; i++ {
		tr.Record(ctx, good, "4477", Outcome{Answered: true, Reached: true, PDDMs: 500, Billsec: 60})
		tr.Record(ctx, bad, "4477", Outcome{Fault: true, PDDMs: 6000})
	}
	// a carrier with few prefix samples but a carrier wide history
	for i := 0; i < 10; i++ {
		tr.Record(ctx, fresh, "1", Outcome{Answered: true, Reached: true, PDDMs: 1000})
	}
	tr.Record(ctx, fresh, "4477", Outcome{Answered: true, Reached: true})
	if err := tr.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	g, ok := tr.Lookup(good, "4477")
	b, ok2 := tr.Lookup(bad, "4477")
	if !ok || !ok2 || g.Score <= b.Score || g.ASR != 1 || b.ASR != 0 || g.Tier <= b.Tier {
		t.Fatalf("good %+v bad %+v", g, b)
	}
	if g.Samples != 10 || g.PDDMs != 500 {
		t.Fatalf("good samples/pdd: %+v", g)
	}
	fr, ok := tr.Lookup(fresh, "4477")
	if !ok || fr.Fallback != "carrier" || fr.Samples != 11 {
		t.Fatalf("fresh should fall back to the carrier wide score: %+v", fr)
	}
	if u, ok := tr.Lookup(uuid.New(), "4477"); ok || u.Fallback != "neutral" || u.Score != 0.5 {
		t.Fatalf("unknown carrier: %+v", u)
	}
	if len(tr.Scores(good)) != 1 || len(tr.Scores(fresh)) != 2 {
		t.Fatalf("scores per carrier: %v %v", tr.Scores(good), tr.Scores(fresh))
	}
	// a nil tracker is safe
	var none *Tracker
	none.Record(ctx, good, "x", Outcome{})
	if s, ok := none.Lookup(good, "x"); ok || s.Score != 0.5 {
		t.Fatal("nil tracker")
	}
}
