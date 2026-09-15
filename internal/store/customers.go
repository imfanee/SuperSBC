package store

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

const customerCols = `id, name, status::text AS status, rate_group_id, route_group_id, max_concurrent_calls, max_cps,
	allowed_codecs, tech_prefix, default_country_code, intl_prefix, trust_pai, blocked_prefixes_enabled,
	media_mode::text AS media_mode, dtmf_mode::text AS dtmf_mode, srtp_mode::text AS srtp_mode, require_tls, notes, created_at, updated_at`

// CustomerByID loads one customer (soft-deleted excluded).
func (s *Store) CustomerByID(ctx context.Context, id uuid.UUID) (*model.Customer, error) {
	return one[model.Customer](ctx, s.pool, `SELECT `+customerCols+` FROM customers WHERE id = $1 AND deleted_at IS NULL`, id)
}

// CustomerByName loads one customer by unique name.
func (s *Store) CustomerByName(ctx context.Context, name string) (*model.Customer, error) {
	return one[model.Customer](ctx, s.pool, `SELECT `+customerCols+` FROM customers WHERE name = $1 AND deleted_at IS NULL`, name)
}

// IPMatch is the result of a source address lookup.
type IPMatch struct {
	Customer model.Customer
	IP       model.CustomerIP
}

// CustomerByIP resolves a source address to a customer (Section 3 Step 1).
// The most specific matching CIDR wins; a row with a port matches only that
// port; transport 'any' matches every transport.
func (s *Store) CustomerByIP(ctx context.Context, ip string, port int, transport string) (*IPMatch, error) {
	transport = strings.ToLower(transport)
	if transport == "" {
		transport = "udp"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.status::text AS status, c.rate_group_id, c.route_group_id, c.max_concurrent_calls, c.max_cps,
		       c.allowed_codecs, c.tech_prefix, c.default_country_code, c.intl_prefix, c.trust_pai, c.blocked_prefixes_enabled,
		       c.media_mode::text, c.dtmf_mode::text, c.srtp_mode::text, c.require_tls, c.notes, c.created_at, c.updated_at,
		       i.id AS ip_id, i.ip_cidr::text AS ip_cidr, i.port, i.transport::text AS transport, i.created_at AS ip_created_at
		FROM customer_ips i
		JOIN customers c ON c.id = i.customer_id AND c.deleted_at IS NULL
		WHERE i.ip_cidr >>= $1::inet
		  AND (i.port IS NULL OR i.port = $2)
		  AND (i.transport = 'any' OR i.transport::text = $3)
		ORDER BY masklen(i.ip_cidr) DESC, (i.port IS NOT NULL) DESC, (i.transport <> 'any') DESC
		LIMIT 1`, ip, port, transport)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	var m IPMatch
	c := &m.Customer
	i := &m.IP
	if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.RateGroupID, &c.RouteGroupID, &c.MaxConcurrentCalls, &c.MaxCPS,
		&c.AllowedCodecs, &c.TechPrefix, &c.DefaultCountryCode, &c.IntlPrefix, &c.TrustPAI, &c.BlockedPrefixesEnabled,
		&c.MediaMode, &c.DTMFMode, &c.SRTPMode, &c.RequireTLS, &c.Notes, &c.CreatedAt, &c.UpdatedAt,
		&i.ID, &i.IPCIDR, &i.Port, &i.Transport, &i.CreatedAt); err != nil {
		return nil, wrapErr(err)
	}
	i.CustomerID = c.ID
	return &m, nil
}

// CustomerIPs lists the addresses of a customer.
func (s *Store) CustomerIPs(ctx context.Context, customerID uuid.UUID) ([]model.CustomerIP, error) {
	return many[model.CustomerIP](ctx, s.pool, `SELECT id, customer_id, ip_cidr::text AS ip_cidr, port, transport::text AS transport, created_at
		FROM customer_ips WHERE customer_id = $1 ORDER BY ip_cidr`, customerID)
}

// AllCustomerIPs lists every authorised address (for ACL rendering).
func (s *Store) AllCustomerIPs(ctx context.Context) ([]model.CustomerIP, error) {
	return many[model.CustomerIP](ctx, s.pool, `SELECT i.id, i.customer_id, i.ip_cidr::text AS ip_cidr, i.port, i.transport::text AS transport, i.created_at
		FROM customer_ips i JOIN customers c ON c.id = i.customer_id AND c.deleted_at IS NULL ORDER BY i.ip_cidr`)
}

// UpsertCustomer inserts or updates a customer by name (used by seed).
func (s *Store) UpsertCustomer(ctx context.Context, c *model.Customer) (*model.Customer, error) {
	return one[model.Customer](ctx, s.pool, `
		INSERT INTO customers (name, status, rate_group_id, route_group_id, max_concurrent_calls, max_cps, allowed_codecs, tech_prefix, default_country_code, intl_prefix, trust_pai, blocked_prefixes_enabled, notes,
		  media_mode, dtmf_mode, srtp_mode, require_tls)
		VALUES ($1, $2::customer_status, $3, $4, $5, $6, $7, $8, $9, COALESCE(NULLIF($10, ''), '00'), $11, $12, $13,
		  COALESCE(NULLIF($14, ''), 'anchor')::media_mode_kind, COALESCE(NULLIF($15, ''), 'rfc2833')::dtmf_kind, COALESCE(NULLIF($16, ''), 'optional')::srtp_kind, $17)
		ON CONFLICT (name) DO UPDATE SET status = EXCLUDED.status, rate_group_id = EXCLUDED.rate_group_id, route_group_id = EXCLUDED.route_group_id,
		  max_concurrent_calls = EXCLUDED.max_concurrent_calls, max_cps = EXCLUDED.max_cps, allowed_codecs = EXCLUDED.allowed_codecs,
		  tech_prefix = EXCLUDED.tech_prefix, default_country_code = EXCLUDED.default_country_code, intl_prefix = EXCLUDED.intl_prefix,
		  trust_pai = EXCLUDED.trust_pai, blocked_prefixes_enabled = EXCLUDED.blocked_prefixes_enabled, notes = EXCLUDED.notes,
		  media_mode = EXCLUDED.media_mode, dtmf_mode = EXCLUDED.dtmf_mode, srtp_mode = EXCLUDED.srtp_mode, require_tls = EXCLUDED.require_tls, deleted_at = NULL
		RETURNING `+customerCols,
		c.Name, c.Status, c.RateGroupID, c.RouteGroupID, c.MaxConcurrentCalls, c.MaxCPS, c.AllowedCodecs, c.TechPrefix, c.DefaultCountryCode, c.IntlPrefix, c.TrustPAI, c.BlockedPrefixesEnabled, c.Notes,
		c.MediaMode, c.DTMFMode, c.SRTPMode, c.RequireTLS)
}

// UpsertCustomerIP adds an address to a customer if not present.
func (s *Store) UpsertCustomerIP(ctx context.Context, customerID uuid.UUID, cidr string, port *int, transport string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO customer_ips (customer_id, ip_cidr, port, transport) VALUES ($1, $2::cidr, $3, $4::transport_kind)
		ON CONFLICT (ip_cidr, port, transport) DO UPDATE SET customer_id = EXCLUDED.customer_id`, customerID, cidr, port, transport)
	return wrapErr(err)
}
