package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

const headerRuleCols = `id, owner_type::text AS owner_type, owner_id, direction, action, header, value, priority, enabled, created_at, updated_at`

// HeaderRules lists the rules of one owner, by priority.
func (s *Store) HeaderRules(ctx context.Context, ownerType string, ownerID uuid.UUID) ([]model.HeaderRule, error) {
	return many[model.HeaderRule](ctx, s.pool, `SELECT `+headerRuleCols+` FROM header_rules WHERE owner_type = $1::owner_kind AND owner_id = $2 ORDER BY priority, header`, ownerType, ownerID)
}

// AllHeaderRules lists every enabled rule (for the in-memory table).
func (s *Store) AllHeaderRules(ctx context.Context) ([]model.HeaderRule, error) {
	return many[model.HeaderRule](ctx, s.pool, `SELECT `+headerRuleCols+` FROM header_rules WHERE enabled ORDER BY owner_type, owner_id, priority`)
}

// CreateHeaderRule inserts a rule.
func (s *Store) CreateHeaderRule(ctx context.Context, r *model.HeaderRule) (*model.HeaderRule, error) {
	return one[model.HeaderRule](ctx, s.pool, `INSERT INTO header_rules (owner_type, owner_id, direction, action, header, value, priority, enabled)
		VALUES ($1::owner_kind, $2, $3, $4, $5, $6, $7, $8) RETURNING `+headerRuleCols, r.OwnerType, r.OwnerID, r.Direction, r.Action, r.Header, r.Value, r.Priority, r.Enabled)
}

// UpdateHeaderRule updates a rule.
func (s *Store) UpdateHeaderRule(ctx context.Context, r *model.HeaderRule) (*model.HeaderRule, error) {
	return one[model.HeaderRule](ctx, s.pool, `UPDATE header_rules SET direction = $2, action = $3, header = $4, value = $5, priority = $6, enabled = $7, updated_at = now()
		WHERE id = $1 RETURNING `+headerRuleCols, r.ID, r.Direction, r.Action, r.Header, r.Value, r.Priority, r.Enabled)
}

// DeleteHeaderRule removes a rule.
func (s *Store) DeleteHeaderRule(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM header_rules WHERE id = $1`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- banned ips ----

const bannedCols = `host(ip) AS ip, reason, hits, manual, banned_at, expires_at`

// BannedIPs lists active bans.
func (s *Store) BannedIPs(ctx context.Context) ([]model.BannedIP, error) {
	return many[model.BannedIP](ctx, s.pool, `SELECT `+bannedCols+` FROM banned_ips WHERE expires_at IS NULL OR expires_at > now() ORDER BY banned_at DESC`)
}

// Ban inserts or extends a ban.
func (s *Store) Ban(ctx context.Context, ip, reason string, hits int, manual bool, expires *time.Time) (*model.BannedIP, error) {
	return one[model.BannedIP](ctx, s.pool, `INSERT INTO banned_ips (ip, reason, hits, manual, expires_at) VALUES ($1::inet, $2, $3, $4, $5)
		ON CONFLICT (ip) DO UPDATE SET reason = EXCLUDED.reason, hits = EXCLUDED.hits, manual = banned_ips.manual OR EXCLUDED.manual, banned_at = now(), expires_at = EXCLUDED.expires_at
		RETURNING `+bannedCols, ip, reason, hits, manual, expires)
}

// Unban removes a ban.
func (s *Store) Unban(ctx context.Context, ip string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM banned_ips WHERE ip = $1::inet`, ip)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpireBans deletes expired bans and reports how many.
func (s *Store) ExpireBans(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM banned_ips WHERE expires_at IS NOT NULL AND expires_at <= now()`)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// IsBanned reports whether an address is currently banned.
func (s *Store) IsBanned(ctx context.Context, ip string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM banned_ips WHERE ip = $1::inet AND (expires_at IS NULL OR expires_at > now())`, ip).Scan(&n)
	return n > 0, wrapErr(err)
}
