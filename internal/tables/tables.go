// Package tables holds the in-memory longest-prefix tables for rate decks
// and route groups (Section 2.3 implementation b, ARCHITECTURE section 6).
// Tables are loaded lazily per group, invalidated by Redis pub/sub and
// refreshed periodically.
package tables

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/cache"
	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/prefix"
	"github.com/opensbc/opensbc/internal/store"
)

// Tables is the cache of rate and route tries.
type Tables struct {
	st  *store.Store
	log *slog.Logger

	mu     sync.RWMutex
	rates  map[uuid.UUID]*entry[model.Rate]
	routes map[uuid.UUID]*entry[model.Route]

	// MaxAge forces a reload of a table older than this.
	MaxAge time.Duration
}

type entry[T any] struct {
	trie     *prefix.Trie[T]
	loadedAt time.Time
}

// New creates the table cache.
func New(st *store.Store, log *slog.Logger) *Tables {
	return &Tables{st: st, log: log, rates: map[uuid.UUID]*entry[model.Rate]{}, routes: map[uuid.UUID]*entry[model.Route]{}, MaxAge: 5 * time.Minute}
}

// MatchRate finds the longest-prefix rate for number in a rate group.
func (t *Tables) MatchRate(ctx context.Context, groupID uuid.UUID, number string) (*model.Rate, bool, error) {
	tr, err := t.rateTrie(ctx, groupID)
	if err != nil {
		return nil, false, err
	}
	r, _, ok := tr.Match(number)
	if !ok {
		return nil, false, nil
	}
	return &r, true, nil
}

// MatchRoute finds the longest-prefix route for number in a route group.
func (t *Tables) MatchRoute(ctx context.Context, groupID uuid.UUID, number string) (*model.Route, bool, error) {
	tr, err := t.routeTrie(ctx, groupID)
	if err != nil {
		return nil, false, err
	}
	r, _, ok := tr.Match(number)
	if !ok {
		return nil, false, nil
	}
	return &r, true, nil
}

func (t *Tables) rateTrie(ctx context.Context, id uuid.UUID) (*prefix.Trie[model.Rate], error) {
	t.mu.RLock()
	e := t.rates[id]
	t.mu.RUnlock()
	if e != nil && time.Since(e.loadedAt) < t.MaxAge {
		return e.trie, nil
	}
	rates, err := t.st.EffectiveRates(ctx, id, time.Now())
	if err != nil {
		if e != nil { // serve stale on DB error
			return e.trie, nil
		}
		return nil, err
	}
	tr := prefix.New[model.Rate]()
	for _, r := range rates {
		tr.Insert(r.Prefix, r)
	}
	t.mu.Lock()
	t.rates[id] = &entry[model.Rate]{trie: tr, loadedAt: time.Now()}
	t.mu.Unlock()
	t.log.Debug("rate table loaded", "rate_group_id", id, "prefixes", tr.Len())
	return tr, nil
}

func (t *Tables) routeTrie(ctx context.Context, id uuid.UUID) (*prefix.Trie[model.Route], error) {
	t.mu.RLock()
	e := t.routes[id]
	t.mu.RUnlock()
	if e != nil && time.Since(e.loadedAt) < t.MaxAge {
		return e.trie, nil
	}
	routes, err := t.st.RoutesForGroup(ctx, id)
	if err != nil {
		if e != nil {
			return e.trie, nil
		}
		return nil, err
	}
	tr := prefix.New[model.Route]()
	for _, r := range routes {
		tr.Insert(r.Prefix, r)
	}
	t.mu.Lock()
	t.routes[id] = &entry[model.Route]{trie: tr, loadedAt: time.Now()}
	t.mu.Unlock()
	t.log.Debug("route table loaded", "route_group_id", id, "prefixes", tr.Len())
	return tr, nil
}

// InvalidateRates drops a rate group's table (or all when id is Nil).
func (t *Tables) InvalidateRates(id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id == uuid.Nil {
		t.rates = map[uuid.UUID]*entry[model.Rate]{}
		return
	}
	delete(t.rates, id)
}

// InvalidateRoutes drops a route group's table (or all when id is Nil).
// Carrier changes invalidate every route table because carriers are
// embedded in route entries.
func (t *Tables) InvalidateRoutes(id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id == uuid.Nil {
		t.routes = map[uuid.UUID]*entry[model.Route]{}
		return
	}
	delete(t.routes, id)
}

// Listen subscribes to the invalidation channels until ctx ends.
func (t *Tables) Listen(ctx context.Context, rdb *redis.Client) {
	sub := rdb.Subscribe(ctx, cache.ChanRateDeckChanged, cache.ChanRoutesChanged, cache.ChanCarriersChanged)
	defer func() { _ = sub.Close() }()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			id, _ := uuid.Parse(m.Payload)
			switch m.Channel {
			case cache.ChanRateDeckChanged:
				t.InvalidateRates(id)
			case cache.ChanRoutesChanged:
				t.InvalidateRoutes(id)
			case cache.ChanCarriersChanged:
				t.InvalidateRoutes(uuid.Nil)
			}
			t.log.Debug("table invalidated", "channel", m.Channel, "id", m.Payload)
		}
	}
}
