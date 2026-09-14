package callcontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/failover"
)

// Breaker is the per-carrier circuit breaker of Section 6, kept in Redis so
// every API node agrees (D-32). A carrier is degraded for DegradedFor when
// it produced ConsecutiveFaults carrier faults in a row, or when its ASR
// over the last five minutes is below ASRThresholdPercent with at least
// MinSamples attempts.
type Breaker struct {
	rdb                 *redis.Client
	ConsecutiveFaults   int
	ASRThresholdPercent int
	MinSamples          int
	DegradedFor         time.Duration
}

// NewBreaker creates a breaker.
func NewBreaker(rdb *redis.Client, consecutive, asrPercent, minSamples, degradedSeconds int) *Breaker {
	return &Breaker{rdb: rdb, ConsecutiveFaults: consecutive, ASRThresholdPercent: asrPercent, MinSamples: minSamples, DegradedFor: time.Duration(degradedSeconds) * time.Second}
}

func (b *Breaker) key(id uuid.UUID, suffix string) string { return "cb:" + id.String() + ":" + suffix }

// Record updates the counters after an attempt and trips the breaker when a
// threshold is crossed. Returns true when the carrier is (now) degraded.
func (b *Breaker) Record(ctx context.Context, carrierID uuid.UUID, class failover.Classification) bool {
	if b == nil || b.rdb == nil {
		return false
	}
	minute := time.Now().Unix() / 60
	pipe := b.rdb.TxPipeline()
	var consec *redis.IntCmd
	switch class {
	case failover.CarrierFault:
		consec = pipe.Incr(ctx, b.key(carrierID, "consec"))
		pipe.Expire(ctx, b.key(carrierID, "consec"), 10*time.Minute)
		pipe.Incr(ctx, b.key(carrierID, fmt.Sprintf("att:%d", minute)))
		pipe.Expire(ctx, b.key(carrierID, fmt.Sprintf("att:%d", minute)), 6*time.Minute)
	case failover.Answered:
		pipe.Set(ctx, b.key(carrierID, "consec"), 0, 10*time.Minute)
		pipe.Incr(ctx, b.key(carrierID, fmt.Sprintf("att:%d", minute)))
		pipe.Expire(ctx, b.key(carrierID, fmt.Sprintf("att:%d", minute)), 6*time.Minute)
		pipe.Incr(ctx, b.key(carrierID, fmt.Sprintf("ans:%d", minute)))
		pipe.Expire(ctx, b.key(carrierID, fmt.Sprintf("ans:%d", minute)), 6*time.Minute)
	case failover.NumberFault:
		// the number was bad, not the carrier: counts as an attempt only
		pipe.Set(ctx, b.key(carrierID, "consec"), 0, 10*time.Minute)
		pipe.Incr(ctx, b.key(carrierID, fmt.Sprintf("att:%d", minute)))
		pipe.Expire(ctx, b.key(carrierID, fmt.Sprintf("att:%d", minute)), 6*time.Minute)
	default:
		return b.Degraded(ctx, carrierID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false
	}
	if consec != nil && b.ConsecutiveFaults > 0 && consec.Val() >= int64(b.ConsecutiveFaults) {
		b.trip(ctx, carrierID, "consecutive_faults")
		return true
	}
	if b.ASRThresholdPercent > 0 && b.MinSamples > 0 {
		att, ans := b.window(ctx, carrierID, minute)
		if att >= int64(b.MinSamples) && ans*100 < att*int64(b.ASRThresholdPercent) {
			b.trip(ctx, carrierID, "low_asr")
			return true
		}
	}
	return b.Degraded(ctx, carrierID)
}

func (b *Breaker) window(ctx context.Context, id uuid.UUID, minute int64) (attempts, answered int64) {
	keysA := make([]string, 0, 5)
	keysB := make([]string, 0, 5)
	for i := int64(0); i < 5; i++ {
		keysA = append(keysA, b.key(id, fmt.Sprintf("att:%d", minute-i)))
		keysB = append(keysB, b.key(id, fmt.Sprintf("ans:%d", minute-i)))
	}
	sum := func(keys []string) int64 {
		vals, err := b.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			return 0
		}
		var n int64
		for _, v := range vals {
			if s, ok := v.(string); ok {
				var x int64
				_, _ = fmt.Sscan(s, &x)
				n += x
			}
		}
		return n
	}
	return sum(keysA), sum(keysB)
}

func (b *Breaker) trip(ctx context.Context, id uuid.UUID, reason string) {
	_ = b.rdb.Set(ctx, b.key(id, "degraded"), reason, b.DegradedFor).Err()
	_ = b.rdb.Set(ctx, b.key(id, "consec"), 0, 10*time.Minute).Err()
}

// Degraded reports whether the carrier is currently degraded.
func (b *Breaker) Degraded(ctx context.Context, carrierID uuid.UUID) bool {
	if b == nil || b.rdb == nil {
		return false
	}
	n, err := b.rdb.Exists(ctx, b.key(carrierID, "degraded")).Result()
	return err == nil && n > 0
}

// Reason returns why a carrier is degraded ("" when it is not).
func (b *Breaker) Reason(ctx context.Context, carrierID uuid.UUID) string {
	if b == nil || b.rdb == nil {
		return ""
	}
	s, err := b.rdb.Get(ctx, b.key(carrierID, "degraded")).Result()
	if err != nil {
		return ""
	}
	return s
}

// Stats returns the five minute window counters.
func (b *Breaker) Stats(ctx context.Context, carrierID uuid.UUID) (attempts, answered int64, consecutiveFaults int64) {
	if b == nil || b.rdb == nil {
		return 0, 0, 0
	}
	minute := time.Now().Unix() / 60
	attempts, answered = b.window(ctx, carrierID, minute)
	consecutiveFaults, _ = b.rdb.Get(ctx, b.key(carrierID, "consec")).Int64()
	return
}
