package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

// CustomerFilter filters the customer list.
type CustomerFilter struct {
	Search string
	Status string
}

// CustomerRow is a customer with its account summary for list views.
type CustomerRow struct {
	model.Customer
	Balance       *string `json:"balance" db:"balance"`
	AllowedCredit *string `json:"allowed_credit" db:"allowed_credit"`
	Reserved      *string `json:"reserved" db:"reserved"`
	Available     *string `json:"available" db:"available"`
	Currency      *string `json:"currency" db:"currency"`
	IPCount       int     `json:"ip_count" db:"ip_count"`
}

// ListCustomers pages through customers with their account summary.
func (s *Store) ListCustomers(ctx context.Context, f CustomerFilter, p Page) (*Listing[CustomerRow], error) {
	p = p.Normalize()
	conds := []string{"c.deleted_at IS NULL"}
	var args []any
	if f.Search != "" {
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		conds = append(conds, fmt.Sprintf("(lower(c.name) LIKE $%d OR EXISTS (SELECT 1 FROM customer_ips i WHERE i.customer_id = c.id AND host(i.ip_cidr) LIKE $%d))", len(args), len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("c.status::text = $%d", len(args)))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM customers c"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"name": "c.name", "status": "c.status", "balance": "a.balance", "created_at": "c.created_at"}, "c.name")
	args = append(args, p.PerPage, p.Offset())
	items, err := many[CustomerRow](ctx, s.pool, `
		SELECT c.id, c.name, c.status::text AS status, c.rate_group_id, c.route_group_id, c.max_concurrent_calls, c.max_cps,
		       c.allowed_codecs, c.tech_prefix, c.default_country_code, c.intl_prefix, c.trust_pai, c.blocked_prefixes_enabled, c.notes, c.created_at, c.updated_at,
		       a.balance::text AS balance, a.allowed_credit::text AS allowed_credit, a.reserved::text AS reserved, account_available(a)::text AS available, a.currency,
		       (SELECT count(*) FROM customer_ips i WHERE i.customer_id = c.id)::int AS ip_count
		FROM customers c LEFT JOIN accounts a ON a.owner_type = 'customer' AND a.owner_id = c.id`+where(conds)+order+
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[CustomerRow]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// CreateCustomer inserts a customer and its account.
func (s *Store) CreateCustomer(ctx context.Context, c *model.Customer, currency string) (*model.Customer, error) {
	out, err := one[model.Customer](ctx, s.pool, `
		INSERT INTO customers (name, status, rate_group_id, route_group_id, max_concurrent_calls, max_cps, allowed_codecs, tech_prefix, default_country_code, intl_prefix, trust_pai, blocked_prefixes_enabled, notes)
		VALUES ($1, $2::customer_status, $3, $4, $5, $6, $7, $8, $9, COALESCE(NULLIF($10, ''), '00'), $11, $12, $13)
		RETURNING `+customerCols,
		c.Name, c.Status, c.RateGroupID, c.RouteGroupID, c.MaxConcurrentCalls, c.MaxCPS, c.AllowedCodecs, c.TechPrefix, c.DefaultCountryCode, c.IntlPrefix, c.TrustPAI, c.BlockedPrefixesEnabled, c.Notes)
	if err != nil {
		return nil, err
	}
	if _, err := s.EnsureAccount(ctx, "customer", out.ID, currency); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateCustomer updates every mutable column.
func (s *Store) UpdateCustomer(ctx context.Context, c *model.Customer) (*model.Customer, error) {
	return one[model.Customer](ctx, s.pool, `
		UPDATE customers SET name = $2, status = $3::customer_status, rate_group_id = $4, route_group_id = $5, max_concurrent_calls = $6, max_cps = $7,
		  allowed_codecs = $8, tech_prefix = $9, default_country_code = $10, intl_prefix = COALESCE(NULLIF($11, ''), '00'), trust_pai = $12, blocked_prefixes_enabled = $13, notes = $14
		WHERE id = $1 AND deleted_at IS NULL RETURNING `+customerCols,
		c.ID, c.Name, c.Status, c.RateGroupID, c.RouteGroupID, c.MaxConcurrentCalls, c.MaxCPS, c.AllowedCodecs, c.TechPrefix, c.DefaultCountryCode, c.IntlPrefix, c.TrustPAI, c.BlockedPrefixesEnabled, c.Notes)
}

// DeleteCustomer soft-deletes a customer and removes its addresses so the
// IPs can be reused. CDRs and the ledger are kept.
func (s *Store) DeleteCustomer(ctx context.Context, id uuid.UUID) error {
	return s.WithTx(ctx, func(tx Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE customers SET deleted_at = now(), name = name || '~deleted~' || to_char(now(), 'YYYYMMDDHH24MISS') WHERE id = $1 AND deleted_at IS NULL`, id)
		if err != nil {
			return wrapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM customer_ips WHERE customer_id = $1`, id)
		return wrapErr(err)
	})
}

// AddCustomerIP inserts an address.
func (s *Store) AddCustomerIP(ctx context.Context, customerID uuid.UUID, cidr string, port *int, transport string) (*model.CustomerIP, error) {
	return one[model.CustomerIP](ctx, s.pool, `INSERT INTO customer_ips (customer_id, ip_cidr, port, transport) VALUES ($1, $2::cidr, $3, $4::transport_kind)
		RETURNING id, customer_id, ip_cidr::text AS ip_cidr, port, transport::text AS transport, created_at`, customerID, cidr, port, transport)
}

// DeleteCustomerIP removes an address.
func (s *Store) DeleteCustomerIP(ctx context.Context, customerID, ipID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM customer_ips WHERE id = $1 AND customer_id = $2`, ipID, customerID)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- blocked prefixes ----

// BlockedPrefixes lists global (customerID nil) or per-customer blocks.
func (s *Store) BlockedPrefixes(ctx context.Context, customerID *uuid.UUID) ([]model.BlockedPrefix, error) {
	if customerID == nil {
		return many[model.BlockedPrefix](ctx, s.pool, `SELECT id, customer_id, prefix, reason, enabled, created_at FROM blocked_prefixes WHERE customer_id IS NULL ORDER BY prefix`)
	}
	return many[model.BlockedPrefix](ctx, s.pool, `SELECT id, customer_id, prefix, reason, enabled, created_at FROM blocked_prefixes WHERE customer_id = $1 ORDER BY prefix`, *customerID)
}

// AllEnabledBlockedPrefixes returns every enabled block for the routing table.
func (s *Store) AllEnabledBlockedPrefixes(ctx context.Context) ([]model.BlockedPrefix, error) {
	return many[model.BlockedPrefix](ctx, s.pool, `SELECT id, customer_id, prefix, reason, enabled, created_at FROM blocked_prefixes WHERE enabled ORDER BY prefix`)
}

// AddBlockedPrefix inserts a block.
func (s *Store) AddBlockedPrefix(ctx context.Context, customerID *uuid.UUID, prefix, reason string) (*model.BlockedPrefix, error) {
	return one[model.BlockedPrefix](ctx, s.pool, `INSERT INTO blocked_prefixes (customer_id, prefix, reason) VALUES ($1, $2, $3)
		RETURNING id, customer_id, prefix, reason, enabled, created_at`, customerID, prefix, reason)
}

// DeleteBlockedPrefix removes a block.
func (s *Store) DeleteBlockedPrefix(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM blocked_prefixes WHERE id = $1`, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
