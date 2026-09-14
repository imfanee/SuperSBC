package store

import (
	"context"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

const carrierCols = `id, name, status::text AS status, rate_group_id, gateway_host, gateway_port, transport::text AS transport,
	dni_prefix, ani_prefix, strip_digits, auth_username, auth_password, from_domain, register, allowed_codecs,
	max_concurrent_calls, max_cps, failover_sip_codes, sip_options_ping, charge_failed_attempts, ignore_early_media, notes, created_at, updated_at`

// CarrierByID loads one carrier.
func (s *Store) CarrierByID(ctx context.Context, id uuid.UUID) (*model.Carrier, error) {
	return one[model.Carrier](ctx, s.pool, `SELECT `+carrierCols+` FROM carriers WHERE id = $1 AND deleted_at IS NULL`, id)
}

// CarrierByName loads one carrier by name.
func (s *Store) CarrierByName(ctx context.Context, name string) (*model.Carrier, error) {
	return one[model.Carrier](ctx, s.pool, `SELECT `+carrierCols+` FROM carriers WHERE name = $1 AND deleted_at IS NULL`, name)
}

// Carriers lists all non-deleted carriers.
func (s *Store) Carriers(ctx context.Context) ([]model.Carrier, error) {
	return many[model.Carrier](ctx, s.pool, `SELECT `+carrierCols+` FROM carriers WHERE deleted_at IS NULL ORDER BY name`)
}

// CarriersByIDs loads several carriers keyed by id.
func (s *Store) CarriersByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]model.Carrier, error) {
	list, err := many[model.Carrier](ctx, s.pool, `SELECT `+carrierCols+` FROM carriers WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]model.Carrier, len(list))
	for _, c := range list {
		m[c.ID] = c
	}
	return m, nil
}

// UpsertCarrier inserts or updates a carrier by name (seed).
func (s *Store) UpsertCarrier(ctx context.Context, c *model.Carrier) (*model.Carrier, error) {
	return one[model.Carrier](ctx, s.pool, `
		INSERT INTO carriers (name, status, rate_group_id, gateway_host, gateway_port, transport, dni_prefix, ani_prefix, strip_digits,
		  auth_username, auth_password, from_domain, register, allowed_codecs, max_concurrent_calls, max_cps, failover_sip_codes,
		  sip_options_ping, charge_failed_attempts, ignore_early_media, notes)
		VALUES ($1, $2::carrier_status, $3, $4, $5, $6::transport_kind, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		ON CONFLICT (name) DO UPDATE SET status = EXCLUDED.status, rate_group_id = EXCLUDED.rate_group_id, gateway_host = EXCLUDED.gateway_host,
		  gateway_port = EXCLUDED.gateway_port, transport = EXCLUDED.transport, dni_prefix = EXCLUDED.dni_prefix, ani_prefix = EXCLUDED.ani_prefix,
		  strip_digits = EXCLUDED.strip_digits, auth_username = EXCLUDED.auth_username, auth_password = EXCLUDED.auth_password,
		  from_domain = EXCLUDED.from_domain, register = EXCLUDED.register, allowed_codecs = EXCLUDED.allowed_codecs,
		  max_concurrent_calls = EXCLUDED.max_concurrent_calls, max_cps = EXCLUDED.max_cps, failover_sip_codes = EXCLUDED.failover_sip_codes,
		  sip_options_ping = EXCLUDED.sip_options_ping, charge_failed_attempts = EXCLUDED.charge_failed_attempts,
		  ignore_early_media = EXCLUDED.ignore_early_media, notes = EXCLUDED.notes, deleted_at = NULL
		RETURNING `+carrierCols,
		c.Name, c.Status, c.RateGroupID, c.GatewayHost, c.GatewayPort, c.Transport, c.DNIPrefix, c.ANIPrefix, c.StripDigits,
		c.AuthUsername, c.AuthPassword, c.FromDomain, c.Register, c.AllowedCodecs, c.MaxConcurrentCalls, c.MaxCPS, c.FailoverSIPCodes,
		c.SIPOptionsPing, c.ChargeFailedAttempts, c.IgnoreEarlyMedia, c.Notes)
}
