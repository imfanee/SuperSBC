package callcontrol

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/imfanee/supersbc/internal/failover"
	"github.com/imfanee/supersbc/internal/model"
)

// EndpointHealth tracks which endpoints of a carrier are usable (D-75).
//
// With OPTIONS ping on, FreeSWITCH decides and the gateway poller reports it.
// With ping off (carriers that do not answer OPTIONS) deadness is learned
// from calls: attempts that get no reply at all count up per endpoint and at
// the carrier's threshold the endpoint is parked in Redis for
// endpoint_retry_seconds, after which it is tried again. Any answered or
// rejected-by-the-far-end attempt clears the counter, because both prove the
// endpoint is alive.
type EndpointHealth struct {
	rdb *redis.Client
}

// NewEndpointHealth creates the tracker; a nil client disables it.
func NewEndpointHealth(rdb *redis.Client) *EndpointHealth { return &EndpointHealth{rdb: rdb} }

func epKey(id uuid.UUID, kind string) string { return fmt.Sprintf("ep:%s:%s", id, kind) }

// Dead reports the endpoints parked right now.
func (h *EndpointHealth) Dead(ctx context.Context, eps []model.CarrierEndpoint) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	if h == nil || h.rdb == nil || len(eps) == 0 {
		return out
	}
	pipe := h.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(eps))
	for i, e := range eps {
		cmds[i] = pipe.Exists(ctx, epKey(e.ID, "dead"))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return out // fail open: a Redis problem must not black-hole every endpoint
	}
	for i, c := range cmds {
		if c.Val() == 1 {
			out[eps[i].ID] = true
		}
	}
	return out
}

// RecordAttempt feeds one attempt result for an endpoint. reachable means the
// far end answered something (a SIP response of any kind); when it is false
// the attempt timed out or the transport failed.
func (h *EndpointHealth) RecordAttempt(ctx context.Context, endpointID uuid.UUID, reachable bool, threshold, retrySeconds int) {
	if h == nil || h.rdb == nil || endpointID == uuid.Nil {
		return
	}
	if reachable {
		h.rdb.Del(ctx, epKey(endpointID, "fail"))
		return
	}
	if threshold <= 0 {
		threshold = 2
	}
	if retrySeconds <= 0 {
		retrySeconds = 300
	}
	n, err := h.rdb.Incr(ctx, epKey(endpointID, "fail")).Result()
	if err != nil {
		return
	}
	h.rdb.Expire(ctx, epKey(endpointID, "fail"), time.Duration(retrySeconds)*time.Second)
	if n >= int64(threshold) {
		h.rdb.Set(ctx, epKey(endpointID, "dead"), n, time.Duration(retrySeconds)*time.Second)
		h.rdb.Del(ctx, epKey(endpointID, "fail"))
	}
}

// Revive takes an endpoint out of the parked set (an operator action).
func (h *EndpointHealth) Revive(ctx context.Context, endpointID uuid.UUID) {
	if h == nil || h.rdb == nil {
		return
	}
	h.rdb.Del(ctx, epKey(endpointID, "dead"), epKey(endpointID, "fail"))
}

// TTL reports how long an endpoint stays parked; zero when it is not parked.
func (h *EndpointHealth) TTL(ctx context.Context, endpointID uuid.UUID) time.Duration {
	if h == nil || h.rdb == nil {
		return 0
	}
	d, err := h.rdb.TTL(ctx, epKey(endpointID, "dead")).Result()
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// EndpointOutcome reads one attempt as evidence about the address it dialled
// (D-75). record says whether the attempt counts; usable says whether the
// call got through to the far end.
//
// It deliberately does not try to tell "the carrier answered 503" from "the
// address answered nothing": FreeSWITCH reports both as a 503 with cause
// NORMAL_TEMPORARY_FAILURE and protocol cause sip:503, so no SIP signal
// separates them. Parking is therefore opt in per carrier
// (endpoint_park_on_failure) and counts every attempt that did not reach the
// far end, which is what an address worth skipping looks like either way.
func EndpointOutcome(class failover.Classification, answered bool) (record, usable bool) {
	switch {
	case answered || class == failover.Answered || class == failover.NumberFault:
		return true, true
	case class == failover.Cancelled:
		return false, false
	default:
		return true, false
	}
}

// UsableEndpoints returns the endpoints of a carrier in dial order (D-75):
// disabled, gateway-down and parked ones are dropped, then "failover" orders
// by priority (weighted random inside a priority), while "round_robin" and
// "weighted" draw the first endpoint by weight and keep the rest behind it so
// a failed call still rolls over to the others.
func UsableEndpoints(c model.Carrier, eps []model.CarrierEndpoint, gatewayDown func(string) bool, dead map[uuid.UUID]bool, max int, rnd *rand.Rand) []model.CarrierEndpoint {
	only := len(eps) == 1
	usable := make([]model.CarrierEndpoint, 0, len(eps))
	for _, e := range eps {
		if !e.Enabled || dead[e.ID] {
			continue
		}
		if gatewayDown != nil && gatewayDown(e.GatewayName(c.Name, only)) {
			continue
		}
		usable = append(usable, e)
	}
	if len(usable) == 0 {
		return nil
	}
	ord := make([]orderable, len(usable))
	for i, e := range usable {
		ord[i] = orderable{priority: e.Priority, weight: e.Weight}
	}
	modes := model.RouteModes{}
	if c.EndpointLB == "round_robin" || c.EndpointLB == "weighted" {
		modes.Percent = true // draw the first by share, the rest follow as failover
		if c.EndpointLB == "round_robin" {
			for i := range ord {
				ord[i].weight = 100 // equal shares
			}
		}
	}
	order := orderCandidates(ord, modes, rnd)
	out := make([]model.CarrierEndpoint, 0, len(order))
	for _, i := range order {
		out = append(out, usable[i])
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}
