package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/model"
)

// CarrierRow is a carrier with account summary.
type CarrierRow struct {
	model.Carrier
	Balance   *string `json:"balance" db:"balance"`
	Currency  *string `json:"currency" db:"currency"`
	RateGroup *string `json:"rate_group_name" db:"rate_group_name"`
}

// ListCarriers pages through carriers.
func (s *Store) ListCarriers(ctx context.Context, search, status string, p Page) (*Listing[CarrierRow], error) {
	p = p.Normalize()
	conds := []string{"c.deleted_at IS NULL"}
	var args []any
	if search != "" {
		args = append(args, "%"+strings.ToLower(search)+"%")
		conds = append(conds, fmt.Sprintf("(lower(c.name) LIKE $%d OR lower(c.gateway_host) LIKE $%d)", len(args), len(args)))
	}
	if status != "" {
		args = append(args, status)
		conds = append(conds, fmt.Sprintf("c.status::text = $%d", len(args)))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM carriers c"+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"name": "c.name", "status": "c.status", "gateway_host": "c.gateway_host", "created_at": "c.created_at"}, "c.name")
	args = append(args, p.PerPage, p.Offset())
	items, err := many[CarrierRow](ctx, s.pool, `
		SELECT c.id, c.name, c.status::text AS status, c.rate_group_id, c.gateway_host, c.gateway_port, c.transport::text AS transport,
		  c.dni_prefix, c.ani_prefix, c.strip_digits, c.auth_username, NULL::text AS auth_password, c.from_domain, c.register, c.allowed_codecs,
		  c.max_concurrent_calls, c.max_cps, c.failover_sip_codes, c.sip_options_ping, c.charge_failed_attempts, c.ignore_early_media,
		  c.media_mode::text AS media_mode, c.dtmf_mode::text AS dtmf_mode, c.srtp_mode::text AS srtp_mode, c.privacy_mode::text AS privacy_mode, c.signalling_sources, c.notes, c.created_at, c.updated_at,
		  a.balance::text AS balance, a.currency, g.name AS rate_group_name
		FROM carriers c LEFT JOIN accounts a ON a.owner_type = 'carrier' AND a.owner_id = c.id LEFT JOIN rate_groups g ON g.id = c.rate_group_id`+
		where(conds)+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[CarrierRow]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// CreateCarrier inserts a carrier and its account.
func (s *Store) CreateCarrier(ctx context.Context, c *model.Carrier, currency string) (*model.Carrier, error) {
	out, err := one[model.Carrier](ctx, s.pool, `
		INSERT INTO carriers (name, status, rate_group_id, gateway_host, gateway_port, transport, dni_prefix, ani_prefix, strip_digits,
		  auth_username, auth_password, from_domain, register, allowed_codecs, max_concurrent_calls, max_cps, failover_sip_codes,
		  sip_options_ping, charge_failed_attempts, ignore_early_media, notes, media_mode, dtmf_mode, srtp_mode, privacy_mode, signalling_sources)
		VALUES ($1, $2::carrier_status, $3, $4, $5, $6::transport_kind, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21,
		  COALESCE(NULLIF($22, ''), 'anchor')::media_mode_kind, COALESCE(NULLIF($23, ''), 'rfc2833')::dtmf_kind, COALESCE(NULLIF($24, ''), 'off')::srtp_kind, COALESCE(NULLIF($25, ''), 'anonymize')::privacy_kind, COALESCE($26::text[], '{}'::text[]))
		RETURNING `+carrierCols,
		c.Name, c.Status, c.RateGroupID, c.GatewayHost, c.GatewayPort, c.Transport, c.DNIPrefix, c.ANIPrefix, c.StripDigits,
		c.AuthUsername, c.AuthPassword, c.FromDomain, c.Register, c.AllowedCodecs, c.MaxConcurrentCalls, c.MaxCPS, c.FailoverSIPCodes,
		c.SIPOptionsPing, c.ChargeFailedAttempts, c.IgnoreEarlyMedia, c.Notes, c.MediaMode, c.DTMFMode, c.SRTPMode, c.PrivacyMode, c.SignallingSources)
	if err != nil {
		return nil, err
	}
	if _, err := s.EnsureAccount(ctx, "carrier", out.ID, currency); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateCarrier updates every mutable column (password only when non-nil).
func (s *Store) UpdateCarrier(ctx context.Context, c *model.Carrier) (*model.Carrier, error) {
	return one[model.Carrier](ctx, s.pool, `
		UPDATE carriers SET name = $2, status = $3::carrier_status, rate_group_id = $4, gateway_host = $5, gateway_port = $6, transport = $7::transport_kind,
		  dni_prefix = $8, ani_prefix = $9, strip_digits = $10, auth_username = $11, auth_password = COALESCE($12, auth_password), from_domain = $13, register = $14,
		  allowed_codecs = $15, max_concurrent_calls = $16, max_cps = $17, failover_sip_codes = $18, sip_options_ping = $19, charge_failed_attempts = $20,
		  ignore_early_media = $21, notes = $22, media_mode = COALESCE(NULLIF($23, ''), 'anchor')::media_mode_kind, dtmf_mode = COALESCE(NULLIF($24, ''), 'rfc2833')::dtmf_kind,
		  srtp_mode = COALESCE(NULLIF($25, ''), 'off')::srtp_kind, privacy_mode = COALESCE(NULLIF($26, ''), 'anonymize')::privacy_kind, signalling_sources = COALESCE($27::text[], '{}'::text[])
		WHERE id = $1 AND deleted_at IS NULL RETURNING `+carrierCols,
		c.ID, c.Name, c.Status, c.RateGroupID, c.GatewayHost, c.GatewayPort, c.Transport, c.DNIPrefix, c.ANIPrefix, c.StripDigits,
		c.AuthUsername, c.AuthPassword, c.FromDomain, c.Register, c.AllowedCodecs, c.MaxConcurrentCalls, c.MaxCPS, c.FailoverSIPCodes,
		c.SIPOptionsPing, c.ChargeFailedAttempts, c.IgnoreEarlyMedia, c.Notes, c.MediaMode, c.DTMFMode, c.SRTPMode, c.PrivacyMode, c.SignallingSources)
}

// DeleteCarrier soft-deletes a carrier and detaches it from routes.
func (s *Store) DeleteCarrier(ctx context.Context, id uuid.UUID) error {
	return s.WithTx(ctx, func(tx Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE carriers SET deleted_at = now(), status = 'disabled', name = name || '-deleted-' || to_char(now(), 'YYYYMMDDHH24MISS') WHERE id = $1 AND deleted_at IS NULL`, id)
		if err != nil {
			return wrapErr(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM route_carriers WHERE carrier_id = $1`, id)
		return wrapErr(err)
	})
}
