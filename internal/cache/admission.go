package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ErrAdmissionUnavailable is returned when Redis cannot answer; callers fail
// closed (D-31, ARCHITECTURE section 5).
var ErrAdmissionUnavailable = errors.New("admission control unavailable")

// Admission implements per-customer concurrent-call and CPS limits and the
// reservation TTL keys.
type Admission struct {
	rdb *redis.Client
}

// NewAdmission creates the admission helper.
func NewAdmission(rdb *redis.Client) *Admission { return &Admission{rdb: rdb} }

func ccKey(scope string, id uuid.UUID) string { return fmt.Sprintf("cc:%s:%s", scope, id) }
func cpsKey(scope string, id uuid.UUID, sec int64) string {
	return fmt.Sprintf("cps:%s:%s:%d", scope, id, sec)
}

// Result of an admission check.
type Result struct {
	ConcurrentExceeded bool
	CPSExceeded        bool
	Concurrent         int64
	CPS                int64
}

// Admit increments the concurrent-call counter and the current-second CPS
// counter for the customer and reports whether either limit is exceeded.
// When a limit is exceeded the concurrent counter is decremented again so
// rejected calls do not occupy a slot. Limits of 0 mean unlimited.
func (a *Admission) Admit(ctx context.Context, scope string, id uuid.UUID, maxConcurrent, maxCPS int) (Result, error) {
	now := time.Now().Unix()
	pipe := a.rdb.TxPipeline()
	cc := pipe.Incr(ctx, ccKey(scope, id))
	pipe.Expire(ctx, ccKey(scope, id), 6*time.Hour)
	cps := pipe.Incr(ctx, cpsKey(scope, id, now))
	pipe.Expire(ctx, cpsKey(scope, id, now), 3*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrAdmissionUnavailable, err)
	}
	r := Result{Concurrent: cc.Val(), CPS: cps.Val()}
	if maxConcurrent > 0 && r.Concurrent > int64(maxConcurrent) {
		r.ConcurrentExceeded = true
	}
	if maxCPS > 0 && r.CPS > int64(maxCPS) {
		r.CPSExceeded = true
	}
	if r.ConcurrentExceeded || r.CPSExceeded {
		_ = a.rdb.Decr(ctx, ccKey(scope, id)).Err()
	}
	return r, nil
}

// Leave decrements the concurrent-call counter, never below zero.
func (a *Admission) Leave(ctx context.Context, scope string, id uuid.UUID) {
	key := ccKey(scope, id)
	n, err := a.rdb.Decr(ctx, key).Result()
	if err == nil && n < 0 {
		_ = a.rdb.Set(ctx, key, 0, 6*time.Hour).Err()
	}
}

// SetConcurrent overwrites a counter (reconciliation from active_calls).
func (a *Admission) SetConcurrent(ctx context.Context, scope string, id uuid.UUID, n int) error {
	return a.rdb.Set(ctx, ccKey(scope, id), n, 6*time.Hour).Err()
}

// Concurrent reads a counter.
func (a *Admission) Concurrent(ctx context.Context, scope string, id uuid.UUID) (int64, error) {
	n, err := a.rdb.Get(ctx, ccKey(scope, id)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// AllConcurrent returns every counter for a scope (for reconciliation).
func (a *Admission) AllConcurrent(ctx context.Context, scope string) (map[uuid.UUID]int64, error) {
	out := map[uuid.UUID]int64{}
	iter := a.rdb.Scan(ctx, 0, "cc:"+scope+":*", 500).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		id, err := uuid.Parse(key[len("cc:"+scope+":"):])
		if err != nil {
			continue
		}
		n, err := a.rdb.Get(ctx, key).Int64()
		if err != nil {
			continue
		}
		out[id] = n
	}
	return out, iter.Err()
}

// Reserve records the reservation key with the max call duration TTL.
func (a *Admission) Reserve(ctx context.Context, callUUID uuid.UUID, ttl time.Duration) error {
	return a.rdb.Set(ctx, "resv:"+callUUID.String(), time.Now().Unix(), ttl).Err()
}

// Unreserve deletes the reservation key.
func (a *Admission) Unreserve(ctx context.Context, callUUID uuid.UUID) {
	_ = a.rdb.Del(ctx, "resv:"+callUUID.String()).Err()
}

// Publish sends a message on a channel (cache invalidation, live UI).
func Publish(ctx context.Context, rdb *redis.Client, channel, payload string) {
	_ = rdb.Publish(ctx, channel, payload).Err()
}

// Channel names.
const (
	ChanRateDeckChanged = "ratedeck:changed"
	ChanRoutesChanged   = "routes:changed"
	ChanCarriersChanged = "carriers:changed"
	ChanCustomerIPs     = "customer_ips:changed"
	ChanCDRCompleted    = "cdr.completed"
	ChanCallStarted     = "call.started"
)
