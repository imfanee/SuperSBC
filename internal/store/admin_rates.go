package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/opensbc/opensbc/internal/model"
)

// RateGroupRow is a rate group with its prefix count.
type RateGroupRow struct {
	model.RateGroup
	RateCount     int `json:"rate_count" db:"rate_count"`
	CustomerCount int `json:"customer_count" db:"customer_count"`
	CarrierCount  int `json:"carrier_count" db:"carrier_count"`
}

// ListRateGroups pages through rate groups.
func (s *Store) ListRateGroups(ctx context.Context, search string, p Page) (*Listing[RateGroupRow], error) {
	p = p.Normalize()
	conds := []string{"g.deleted_at IS NULL"}
	var args []any
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("lower(g.name) LIKE $%d", len(args)))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM rate_groups g"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"name": "g.name", "currency": "g.currency", "created_at": "g.created_at"}, "g.name")
	args = append(args, p.PerPage, p.Offset())
	items, err := many[RateGroupRow](ctx, s.pool, `
		SELECT g.id, g.name, g.currency, g.description, g.created_at, g.updated_at,
		  (SELECT count(*) FROM rates r WHERE r.rate_group_id = g.id)::int AS rate_count,
		  (SELECT count(*) FROM customers c WHERE c.rate_group_id = g.id AND c.deleted_at IS NULL)::int AS customer_count,
		  (SELECT count(*) FROM carriers c WHERE c.rate_group_id = g.id AND c.deleted_at IS NULL)::int AS carrier_count
		FROM rate_groups g`+where(conds)+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[RateGroupRow]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// CreateRateGroup inserts a rate group.
func (s *Store) CreateRateGroup(ctx context.Context, name, currency, description string) (*model.RateGroup, error) {
	return one[model.RateGroup](ctx, s.pool, `INSERT INTO rate_groups (name, currency, description) VALUES ($1, $2, $3)
		RETURNING id, name, currency, description, created_at, updated_at`, name, currency, description)
}

// UpdateRateGroup updates name, currency and description.
func (s *Store) UpdateRateGroup(ctx context.Context, id uuid.UUID, name, currency, description string) (*model.RateGroup, error) {
	return one[model.RateGroup](ctx, s.pool, `UPDATE rate_groups SET name = $2, currency = $3, description = $4 WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, name, currency, description, created_at, updated_at`, id, name, currency, description)
}

// DeleteRateGroup soft-deletes a group that is not referenced.
func (s *Store) DeleteRateGroup(ctx context.Context, id uuid.UUID) error {
	var refs int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM customers WHERE rate_group_id = $1 AND deleted_at IS NULL) + (SELECT count(*) FROM carriers WHERE rate_group_id = $1 AND deleted_at IS NULL)`, id).Scan(&refs); err != nil {
		return wrapErr(err)
	}
	if refs > 0 {
		return fmt.Errorf("%w: rate group is in use by %d customers or carriers", ErrConflict, refs)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE rate_groups SET deleted_at = now(), name = name || '~deleted~' || to_char(now(), 'YYYYMMDDHH24MISS') WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRates pages through the rates of a group with an optional prefix or destination search.
func (s *Store) ListRates(ctx context.Context, groupID uuid.UUID, search string, enabledOnly bool, p Page) (*Listing[model.Rate], error) {
	p = p.Normalize()
	conds := []string{"rate_group_id = $1"}
	args := []any{groupID}
	if search != "" {
		args = append(args, search+"%", "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("(prefix LIKE $%d OR lower(destination) LIKE $%d)", len(args)-1, len(args)))
	}
	if enabledOnly {
		conds = append(conds, "enabled")
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM rates"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"prefix": "prefix", "destination": "destination", "rate_per_min": "rate_per_min", "effective_from": "effective_from"}, "prefix")
	args = append(args, p.PerPage, p.Offset())
	items, err := many[model.Rate](ctx, s.pool, `SELECT `+rateCols+` FROM rates`+where(conds)+order+", effective_from DESC"+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[model.Rate]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// AllRates returns every rate of a group (export).
func (s *Store) AllRates(ctx context.Context, groupID uuid.UUID) ([]model.Rate, error) {
	return many[model.Rate](ctx, s.pool, `SELECT `+rateCols+` FROM rates WHERE rate_group_id = $1 ORDER BY prefix, effective_from`, groupID)
}

// UpdateRate updates a rate row.
func (s *Store) UpdateRate(ctx context.Context, r *model.Rate) (*model.Rate, error) {
	return one[model.Rate](ctx, s.pool, `
		UPDATE rates SET prefix = $2, destination = $3, rate_per_min = $4::numeric, connect_fee = $5::numeric, initial_increment = $6, subsequent_increment = $7,
		  min_duration = $8, effective_from = $9, effective_to = $10, enabled = $11
		WHERE id = $1 RETURNING `+rateCols,
		r.ID, r.Prefix, r.Destination, r.RatePerMin, r.ConnectFee, r.InitialIncrement, r.SubsequentIncrement, r.MinDuration, r.EffectiveFrom, r.EffectiveTo, r.Enabled)
}

// DeleteRate removes a rate row (or many).
func (s *Store) DeleteRates(ctx context.Context, groupID uuid.UUID, ids []uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM rates WHERE rate_group_id = $1 AND id = ANY($2)`, groupID, ids)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// ImportRates upserts a batch of rates in one transaction (CSV import).
// With replace=true every existing rate of the group is deleted first.
func (s *Store) ImportRates(ctx context.Context, groupID uuid.UUID, rates []model.Rate, replace bool) (int, error) {
	n := 0
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		if replace {
			if _, err := tx.Exec(ctx, `DELETE FROM rates WHERE rate_group_id = $1`, groupID); err != nil {
				return wrapErr(err)
			}
		}
		for _, r := range rates {
			if _, err := tx.Exec(ctx, `
				INSERT INTO rates (rate_group_id, prefix, destination, rate_per_min, connect_fee, initial_increment, subsequent_increment, min_duration, effective_from, effective_to, enabled)
				VALUES ($1, $2, $3, $4::numeric, $5::numeric, $6, $7, $8, $9, $10, $11)
				ON CONFLICT (rate_group_id, prefix, effective_from) DO UPDATE SET destination = EXCLUDED.destination, rate_per_min = EXCLUDED.rate_per_min,
				  connect_fee = EXCLUDED.connect_fee, initial_increment = EXCLUDED.initial_increment, subsequent_increment = EXCLUDED.subsequent_increment,
				  min_duration = EXCLUDED.min_duration, effective_to = EXCLUDED.effective_to, enabled = EXCLUDED.enabled`,
				groupID, r.Prefix, r.Destination, r.RatePerMin, r.ConnectFee, r.InitialIncrement, r.SubsequentIncrement, r.MinDuration, r.EffectiveFrom, r.EffectiveTo, r.Enabled); err != nil {
				return wrapErr(err)
			}
			n++
		}
		return nil
	})
	return n, err
}

// ---- route groups and routes ----

// RouteGroupRow is a route group with counts.
type RouteGroupRow struct {
	model.RouteGroup
	RouteCount    int `json:"route_count" db:"route_count"`
	CustomerCount int `json:"customer_count" db:"customer_count"`
}

// ListRouteGroups pages through route groups.
func (s *Store) ListRouteGroups(ctx context.Context, search string, p Page) (*Listing[RouteGroupRow], error) {
	p = p.Normalize()
	conds := []string{"g.deleted_at IS NULL"}
	var args []any
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("lower(g.name) LIKE $%d", len(args)))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM route_groups g"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"name": "g.name", "created_at": "g.created_at"}, "g.name")
	args = append(args, p.PerPage, p.Offset())
	items, err := many[RouteGroupRow](ctx, s.pool, `
		SELECT g.id, g.name, g.description, g.lcr_mode, g.created_at, g.updated_at,
		  (SELECT count(*) FROM routes r WHERE r.route_group_id = g.id)::int AS route_count,
		  (SELECT count(*) FROM customers c WHERE c.route_group_id = g.id AND c.deleted_at IS NULL)::int AS customer_count
		FROM route_groups g`+where(conds)+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[RouteGroupRow]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// CreateRouteGroup inserts a group.
func (s *Store) CreateRouteGroup(ctx context.Context, name, description string, lcr bool) (*model.RouteGroup, error) {
	return one[model.RouteGroup](ctx, s.pool, `INSERT INTO route_groups (name, description, lcr_mode) VALUES ($1, $2, $3)
		RETURNING id, name, description, lcr_mode, created_at, updated_at`, name, description, lcr)
}

// UpdateRouteGroup updates a group.
func (s *Store) UpdateRouteGroup(ctx context.Context, id uuid.UUID, name, description string, lcr bool) (*model.RouteGroup, error) {
	return one[model.RouteGroup](ctx, s.pool, `UPDATE route_groups SET name = $2, description = $3, lcr_mode = $4 WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, name, description, lcr_mode, created_at, updated_at`, id, name, description, lcr)
}

// DeleteRouteGroup soft-deletes an unreferenced group.
func (s *Store) DeleteRouteGroup(ctx context.Context, id uuid.UUID) error {
	var refs int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE route_group_id = $1 AND deleted_at IS NULL`, id).Scan(&refs); err != nil {
		return wrapErr(err)
	}
	if refs > 0 {
		return fmt.Errorf("%w: route group is in use by %d customers", ErrConflict, refs)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE route_groups SET deleted_at = now(), name = name || '~deleted~' || to_char(now(), 'YYYYMMDDHH24MISS') WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RouteCarrierRow is a route carrier with the carrier name.
type RouteCarrierRow struct {
	model.RouteCarrier
	CarrierName   string `json:"carrier_name" db:"carrier_name"`
	CarrierStatus string `json:"carrier_status" db:"carrier_status"`
}

// RouteRow is a route with its carriers for the admin API.
type RouteRow struct {
	model.Route
	CarrierRows []RouteCarrierRow `json:"carriers"`
}

// ListRoutes returns all routes of a group with their ordered carriers.
func (s *Store) ListRoutes(ctx context.Context, groupID uuid.UUID, search string) ([]RouteRow, error) {
	conds := []string{"r.route_group_id = $1"}
	args := []any{groupID}
	if search != "" {
		args = append(args, search+"%", "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("(r.prefix LIKE $%d OR lower(r.destination) LIKE $%d)", len(args)-1, len(args)))
	}
	routes, err := many[model.Route](ctx, s.pool, `SELECT r.id, r.route_group_id, r.prefix, r.destination, r.enabled, g.lcr_mode, r.created_at, r.updated_at
		FROM routes r JOIN route_groups g ON g.id = r.route_group_id`+where(conds)+` ORDER BY r.prefix`, args...)
	if err != nil {
		return nil, err
	}
	rcs, err := many[RouteCarrierRow](ctx, s.pool, `
		SELECT rc.id, rc.route_id, rc.carrier_id, rc.priority, rc.weight, rc.enabled, rc."window", c.name AS carrier_name, c.status::text AS carrier_status
		FROM route_carriers rc JOIN routes r ON r.id = rc.route_id JOIN carriers c ON c.id = rc.carrier_id
		WHERE r.route_group_id = $1 ORDER BY rc.route_id, rc.priority, rc.weight DESC`, groupID)
	if err != nil {
		return nil, err
	}
	byRoute := map[uuid.UUID][]RouteCarrierRow{}
	for _, rc := range rcs {
		byRoute[rc.RouteID] = append(byRoute[rc.RouteID], rc)
	}
	out := make([]RouteRow, 0, len(routes))
	for _, r := range routes {
		out = append(out, RouteRow{Route: r, CarrierRows: byRoute[r.ID]})
	}
	return out, nil
}

// RouteByID loads a single route with carriers.
func (s *Store) RouteByID(ctx context.Context, id uuid.UUID) (*RouteRow, error) {
	r, err := one[model.Route](ctx, s.pool, `SELECT r.id, r.route_group_id, r.prefix, r.destination, r.enabled, g.lcr_mode, r.created_at, r.updated_at
		FROM routes r JOIN route_groups g ON g.id = r.route_group_id WHERE r.id = $1`, id)
	if err != nil {
		return nil, err
	}
	rcs, err := many[RouteCarrierRow](ctx, s.pool, `
		SELECT rc.id, rc.route_id, rc.carrier_id, rc.priority, rc.weight, rc.enabled, rc."window", c.name AS carrier_name, c.status::text AS carrier_status
		FROM route_carriers rc JOIN carriers c ON c.id = rc.carrier_id WHERE rc.route_id = $1 ORDER BY rc.priority, rc.weight DESC`, id)
	if err != nil {
		return nil, err
	}
	return &RouteRow{Route: *r, CarrierRows: rcs}, nil
}

// CreateRoute inserts a route.
func (s *Store) CreateRoute(ctx context.Context, groupID uuid.UUID, prefix, destination string, enabled bool) (*model.Route, error) {
	return one[model.Route](ctx, s.pool, `INSERT INTO routes (route_group_id, prefix, destination, enabled) VALUES ($1, $2, $3, $4)
		RETURNING id, route_group_id, prefix, destination, enabled, created_at, updated_at`, groupID, prefix, destination, enabled)
}

// UpdateRoute updates a route.
func (s *Store) UpdateRoute(ctx context.Context, id uuid.UUID, prefix, destination string, enabled bool) (*model.Route, error) {
	return one[model.Route](ctx, s.pool, `UPDATE routes SET prefix = $2, destination = $3, enabled = $4 WHERE id = $1
		RETURNING id, route_group_id, prefix, destination, enabled, created_at, updated_at`, id, prefix, destination, enabled)
}

// DeleteRoute removes a route and its carriers.
func (s *Store) DeleteRoute(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RouteCarrierSpec is one entry of a replace-all carrier list.
type RouteCarrierSpec struct {
	CarrierID uuid.UUID `json:"carrier_id"`
	Priority  int       `json:"priority"`
	Weight    int       `json:"weight"`
	Enabled   bool      `json:"enabled"`
	Window    string    `json:"window"`
}

// ReplaceRouteCarriers replaces the ordered carrier list of a route.
func (s *Store) ReplaceRouteCarriers(ctx context.Context, routeID uuid.UUID, specs []RouteCarrierSpec) error {
	return s.WithTx(ctx, func(tx Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM route_carriers WHERE route_id = $1`, routeID); err != nil {
			return wrapErr(err)
		}
		for i, sp := range specs {
			prio := sp.Priority
			if prio <= 0 {
				prio = i + 1
			}
			w := sp.Weight
			if w <= 0 {
				w = 100
			}
			if _, err := tx.Exec(ctx, `INSERT INTO route_carriers (route_id, carrier_id, priority, weight, enabled, "window") VALUES ($1, $2, $3, $4, $5, $6)`, routeID, sp.CarrierID, prio, w, sp.Enabled, strings.TrimSpace(sp.Window)); err != nil {
				return wrapErr(err)
			}
		}
		return nil
	})
}

// ---- fx rates ----

// FXRates lists the latest rate per pair.
func (s *Store) FXRates(ctx context.Context) ([]model.FXRate, error) {
	return many[model.FXRate](ctx, s.pool, `SELECT DISTINCT ON (base, quote) id, base, quote, rate, effective_from, created_by, created_at
		FROM fx_rates WHERE effective_from <= now() ORDER BY base, quote, effective_from DESC`)
}

// AddFXRate inserts a rate effective from the given time.
func (s *Store) AddFXRate(ctx context.Context, base, quote string, rate string, from time.Time, by *string) (*model.FXRate, error) {
	return one[model.FXRate](ctx, s.pool, `INSERT INTO fx_rates (base, quote, rate, effective_from, created_by) VALUES ($1, $2, $3::numeric, $4, $5)
		ON CONFLICT (base, quote, effective_from) DO UPDATE SET rate = EXCLUDED.rate, created_by = EXCLUDED.created_by
		RETURNING id, base, quote, rate, effective_from, created_by, created_at`, strings.ToUpper(base), strings.ToUpper(quote), rate, from, by)
}
