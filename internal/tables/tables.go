// Package tables holds the in-memory longest-prefix tables for rate decks
// and route groups (Section 2.3 implementation b, ARCHITECTURE section 6).
// Tables are loaded lazily per group, invalidated by Redis pub/sub and
// refreshed periodically.
package tables

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

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
	blocks *blockEntry
	fx     *fxEntry

	// MaxAge forces a reload of a table older than this.
	MaxAge time.Duration
}

type entry[T any] struct {
	trie     *prefix.Trie[T]
	loadedAt time.Time
}

type blockEntry struct {
	global   *prefix.Trie[model.BlockedPrefix]
	customer map[uuid.UUID]*prefix.Trie[model.BlockedPrefix]
	loadedAt time.Time
}

type fxEntry struct {
	rates    map[string]decimal.Decimal // "BASE/QUOTE"
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

// Blocked returns the matching block for number: the customer's own list
// first, then the global blacklist (Section 7, blocking rules).
func (t *Tables) Blocked(ctx context.Context, customerID uuid.UUID, number string) (*model.BlockedPrefix, error) {
	t.mu.RLock()
	b := t.blocks
	t.mu.RUnlock()
	if b == nil || time.Since(b.loadedAt) >= t.MaxAge {
		rows, err := t.st.AllEnabledBlockedPrefixes(ctx)
		if err != nil {
			if b == nil {
				return nil, err
			}
		} else {
			nb := &blockEntry{global: prefix.New[model.BlockedPrefix](), customer: map[uuid.UUID]*prefix.Trie[model.BlockedPrefix]{}, loadedAt: time.Now()}
			for _, r := range rows {
				if r.CustomerID == nil {
					nb.global.Insert(r.Prefix, r)
					continue
				}
				tr := nb.customer[*r.CustomerID]
				if tr == nil {
					tr = prefix.New[model.BlockedPrefix]()
					nb.customer[*r.CustomerID] = tr
				}
				tr.Insert(r.Prefix, r)
			}
			t.mu.Lock()
			t.blocks = nb
			t.mu.Unlock()
			b = nb
		}
	}
	if tr := b.customer[customerID]; tr != nil {
		if m, _, ok := tr.Match(number); ok {
			return &m, nil
		}
	}
	if m, _, ok := b.global.Match(number); ok {
		return &m, nil
	}
	return nil, nil
}

// InvalidateBlocks drops the block list tables.
func (t *Tables) InvalidateBlocks() {
	t.mu.Lock()
	t.blocks = nil
	t.mu.Unlock()
}

// FX returns the multiplier converting an amount in base to quote (D-45).
// Same currency is 1. When no rate exists ok is false.
func (t *Tables) FX(ctx context.Context, base, quote string) (decimal.Decimal, bool) {
	if base == "" || quote == "" || strings.EqualFold(base, quote) {
		return decimal.NewFromInt(1), true
	}
	t.mu.RLock()
	f := t.fx
	t.mu.RUnlock()
	if f == nil || time.Since(f.loadedAt) >= time.Minute {
		rows, err := t.st.FXRates(ctx)
		if err == nil {
			nf := &fxEntry{rates: map[string]decimal.Decimal{}, loadedAt: time.Now()}
			for _, r := range rows {
				nf.rates[strings.ToUpper(r.Base)+"/"+strings.ToUpper(r.Quote)] = r.Rate
			}
			t.mu.Lock()
			t.fx = nf
			t.mu.Unlock()
			f = nf
		} else if f == nil {
			return decimal.Zero, false
		}
	}
	if r, ok := f.rates[strings.ToUpper(base)+"/"+strings.ToUpper(quote)]; ok {
		return r, true
	}
	if r, ok := f.rates[strings.ToUpper(quote)+"/"+strings.ToUpper(base)]; ok && !r.IsZero() {
		return decimal.NewFromInt(1).DivRound(r, 8), true
	}
	return decimal.Zero, false
}

// InvalidateFX drops the exchange rate cache.
func (t *Tables) InvalidateFX() {
	t.mu.Lock()
	t.fx = nil
	t.mu.Unlock()
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
	sub := rdb.Subscribe(ctx, cache.ChanRateDeckChanged, cache.ChanRoutesChanged, cache.ChanCarriersChanged, cache.ChanBlocklistChanged, cache.ChanFXChanged)
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
			case cache.ChanBlocklistChanged:
				t.InvalidateBlocks()
			case cache.ChanFXChanged:
				t.InvalidateFX()
			}
			t.log.Debug("table invalidated", "channel", m.Channel, "id", m.Payload)
		}
	}
}
