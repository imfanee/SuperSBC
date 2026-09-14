package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

const rateCols = `id, rate_group_id, prefix, destination, rate_per_min, connect_fee, initial_increment, subsequent_increment,
	min_duration, effective_from, effective_to, enabled, created_at, updated_at`

// RateGroupByID loads a rate group.
func (s *Store) RateGroupByID(ctx context.Context, id uuid.UUID) (*model.RateGroup, error) {
	return RateGroupByIDQ(ctx, s.pool, id)
}

// RateGroupByIDQ loads a rate group through q.
func RateGroupByIDQ(ctx context.Context, q Querier, id uuid.UUID) (*model.RateGroup, error) {
	return one[model.RateGroup](ctx, q, `SELECT id, name, currency, description, created_at, updated_at FROM rate_groups WHERE id = $1 AND deleted_at IS NULL`, id)
}

// RateGroupByName loads a rate group by name.
func (s *Store) RateGroupByName(ctx context.Context, name string) (*model.RateGroup, error) {
	return one[model.RateGroup](ctx, s.pool, `SELECT id, name, currency, description, created_at, updated_at FROM rate_groups WHERE name = $1 AND deleted_at IS NULL`, name)
}

// UpsertRateGroup inserts or updates a rate group by name.
func (s *Store) UpsertRateGroup(ctx context.Context, name, currency, description string) (*model.RateGroup, error) {
	return one[model.RateGroup](ctx, s.pool, `
		INSERT INTO rate_groups (name, currency, description) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE SET currency = EXCLUDED.currency, description = EXCLUDED.description, deleted_at = NULL
		RETURNING id, name, currency, description, created_at, updated_at`, name, currency, description)
}

// EffectiveRates returns the enabled, currently effective rates of a group,
// the input of the in-memory trie (Section 2.3 implementation b).
func (s *Store) EffectiveRates(ctx context.Context, groupID uuid.UUID, at time.Time) ([]model.Rate, error) {
	return many[model.Rate](ctx, s.pool, `
		SELECT DISTINCT ON (prefix) `+rateCols+`
		FROM rates
		WHERE rate_group_id = $1 AND enabled AND effective_from <= $2 AND (effective_to IS NULL OR effective_to > $2)
		ORDER BY prefix, effective_from DESC`, groupID, at)
}

// LongestPrefixSQL is the reference longest-prefix-match implementation
// (Section 2.3 implementation a). Used by tests and the admin "test number" tool.
func (s *Store) LongestPrefixSQL(ctx context.Context, groupID uuid.UUID, number string, at time.Time) (*model.Rate, error) {
	return one[model.Rate](ctx, s.pool, `
		SELECT `+rateCols+`
		FROM rates
		WHERE rate_group_id = $1 AND enabled AND effective_from <= $3 AND (effective_to IS NULL OR effective_to > $3)
		  AND $2 LIKE prefix || '%'
		ORDER BY length(prefix) DESC, effective_from DESC
		LIMIT 1`, groupID, number, at)
}

// UpsertRate inserts or updates a rate row keyed by (group, prefix, effective_from).
func (s *Store) UpsertRate(ctx context.Context, r *model.Rate) (*model.Rate, error) {
	return one[model.Rate](ctx, s.pool, `
		INSERT INTO rates (rate_group_id, prefix, destination, rate_per_min, connect_fee, initial_increment, subsequent_increment, min_duration, effective_from, effective_to, enabled)
		VALUES ($1, $2, $3, $4::numeric, $5::numeric, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (rate_group_id, prefix, effective_from) DO UPDATE SET destination = EXCLUDED.destination, rate_per_min = EXCLUDED.rate_per_min,
		  connect_fee = EXCLUDED.connect_fee, initial_increment = EXCLUDED.initial_increment, subsequent_increment = EXCLUDED.subsequent_increment,
		  min_duration = EXCLUDED.min_duration, effective_to = EXCLUDED.effective_to, enabled = EXCLUDED.enabled
		RETURNING `+rateCols,
		r.RateGroupID, r.Prefix, r.Destination, r.RatePerMin, r.ConnectFee, r.InitialIncrement, r.SubsequentIncrement, r.MinDuration, r.EffectiveFrom, r.EffectiveTo, r.Enabled)
}

// RateByID loads one rate row.
func (s *Store) RateByID(ctx context.Context, id uuid.UUID) (*model.Rate, error) {
	return RateByIDQ(ctx, s.pool, id)
}

// RateByIDQ loads one rate row through q (use the transaction inside a
// transaction: taking a second pool connection while one is held deadlocks
// the pool under load).
func RateByIDQ(ctx context.Context, q Querier, id uuid.UUID) (*model.Rate, error) {
	return one[model.Rate](ctx, q, `SELECT `+rateCols+` FROM rates WHERE id = $1`, id)
}
