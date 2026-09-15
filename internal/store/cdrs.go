package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/opensbc/opensbc/internal/model"
)

const cdrCols = `call_uuid, customer_id, carrier_id, src_ip::text AS src_ip, src_port, caller_number_raw, caller_number, called_number_raw, called_number,
	start_time, progress_time, answer_time, end_time, pdd_ms, ring_seconds, billsec, duration, sip_final_code, sip_final_reason, hangup_cause,
	disposition::text AS disposition, reject_reason, sell_rate_id, sell_rate_per_min, sell_billed_seconds, sell_price, sell_destination, sell_currency, buy_currency, sell_fx, buy_fx,
	buy_rate_id, buy_rate_per_min, buy_billed_seconds, cost, margin, negative_margin, reserved_amount, charged_amount, released_amount,
	attempts, failover_depth, codec_in, codec_out, media_mode, COALESCE(rtp_stats, 'null'::jsonb) AS rtp_stats, transport_in, transport_out, srtp_in, srtp_out, privacy, stir_status, stir_attest, sbc_node, billed_at, billed_by, created_at, updated_at`

// InsertCDRSetup creates the CDR row at call setup (D-36) or, for rejected
// calls, the final row. Idempotent on call_uuid.
func InsertCDRSetup(ctx context.Context, q Querier, c *model.CDR) error {
	attempts, _ := json.Marshal(c.Attempts)
	if c.Attempts == nil {
		attempts = []byte("[]")
	}
	_, err := q.Exec(ctx, `
		INSERT INTO cdrs (call_uuid, customer_id, carrier_id, src_ip, src_port, caller_number_raw, caller_number, called_number_raw, called_number,
		  start_time, end_time, disposition, reject_reason, sip_final_code, sip_final_reason, hangup_cause,
		  sell_rate_id, sell_rate_per_min, sell_destination, reserved_amount, attempts, sbc_node, billed_at, billed_by, sell_currency, sell_fx, transport_in, srtp_in, privacy, stir_status, stir_attest)
		VALUES ($1, $2, $3, NULLIF($4, '')::inet, $5, $6, $7, $8, $9, $10, $11, $12::disposition_kind, $13, $14, $15, $16, $17, $18::numeric, $19, $20::numeric, $21::jsonb, $22, $23, $24, $25, COALESCE(NULLIF($26, '')::numeric, 1), $27, $28, $29, $30, $31)
		ON CONFLICT (call_uuid) DO NOTHING`,
		c.CallUUID, c.CustomerID, c.CarrierID, deref(c.SrcIP), c.SrcPort, c.CallerNumberRaw, c.CallerNumber, c.CalledNumberRaw, c.CalledNumber,
		c.StartTime, c.EndTime, c.Disposition, c.RejectReason, c.SIPFinalCode, c.SIPFinalReason, c.HangupCause,
		c.SellRateID, c.SellRatePerMin, c.SellDestination, c.ReservedAmount, string(attempts), c.SBCNode, c.BilledAt, c.BilledBy, c.SellCurrency, fxString(c.SellFX),
		c.TransportIn, c.SRTPIn, c.Privacy, c.STIRStatus, c.STIRAttest)
	return wrapErr(err)
}

func fxString(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CDRByUUID loads a CDR.
func (s *Store) CDRByUUID(ctx context.Context, id uuid.UUID) (*model.CDR, error) {
	return one[model.CDR](ctx, s.pool, `SELECT `+cdrCols+` FROM cdrs WHERE call_uuid = $1`, id)
}

// LockCDR loads a CDR FOR UPDATE inside tx (billing idempotency guard, D-17).
func LockCDR(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*model.CDR, error) {
	rows, err := tx.Query(ctx, `SELECT `+cdrCols+` FROM cdrs WHERE call_uuid = $1 FOR UPDATE`, id)
	if err != nil {
		return nil, wrapErr(err)
	}
	c, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[model.CDR])
	if err != nil {
		return nil, wrapErr(err)
	}
	return c, nil
}

// AppendAttempt appends one attempt record to cdrs.attempts.
func (s *Store) AppendAttempt(ctx context.Context, id uuid.UUID, a model.AttemptRecord) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE cdrs SET attempts = attempts || $2::jsonb, failover_depth = jsonb_array_length(attempts || $2::jsonb) - 1 WHERE call_uuid = $1`, id, string(b))
	return wrapErr(err)
}

// FinalizeCDR writes the billing outcome. Called inside the billing tx.
func FinalizeCDR(ctx context.Context, tx pgx.Tx, c *model.CDR) error {
	attempts, _ := json.Marshal(c.Attempts)
	if c.Attempts == nil {
		attempts = []byte("[]")
	}
	var rtp any
	if c.RTPStats != nil {
		b, _ := json.Marshal(c.RTPStats)
		rtp = string(b)
	}
	_, err := tx.Exec(ctx, `
		UPDATE cdrs SET
		  carrier_id = $2, progress_time = $3, answer_time = $4, end_time = $5, pdd_ms = $6, ring_seconds = $7, billsec = $8, duration = $9,
		  sip_final_code = $10, sip_final_reason = $11, hangup_cause = $12, disposition = $13::disposition_kind, reject_reason = COALESCE($14, reject_reason),
		  sell_billed_seconds = $15, sell_price = $16::numeric, buy_rate_id = $17, buy_rate_per_min = $18::numeric, buy_billed_seconds = $19, cost = $20::numeric,
		  negative_margin = $21, charged_amount = $22::numeric, released_amount = $23::numeric, attempts = $24::jsonb,
		  failover_depth = GREATEST(jsonb_array_length($24::jsonb) - 1, 0),
		  codec_in = $25, codec_out = COALESCE(codec_out, $26), media_mode = COALESCE($27, media_mode), rtp_stats = COALESCE($28::jsonb, rtp_stats), sbc_node = $29, billed_at = $30, billed_by = $31,
		  buy_currency = $32, buy_fx = COALESCE(NULLIF($33, '')::numeric, 1),
		  transport_in = COALESCE($34, transport_in), transport_out = COALESCE($35, transport_out), srtp_in = $36, srtp_out = $37 OR srtp_out, privacy = $38 OR privacy
		WHERE call_uuid = $1`,
		c.CallUUID, c.CarrierID, c.ProgressTime, c.AnswerTime, c.EndTime, c.PDDMs, c.RingSeconds, c.Billsec, c.Duration,
		c.SIPFinalCode, c.SIPFinalReason, c.HangupCause, c.Disposition, c.RejectReason,
		c.SellBilledSeconds, c.SellPrice, c.BuyRateID, c.BuyRatePerMin, c.BuyBilledSeconds, c.Cost,
		c.NegativeMargin, c.ChargedAmount, c.ReleasedAmount, string(attempts),
		c.CodecIn, c.CodecOut, c.MediaMode, rtp, c.SBCNode, c.BilledAt, c.BilledBy, c.BuyCurrency, fxString(c.BuyFX),
		c.TransportIn, c.TransportOut, c.SRTPIn, c.SRTPOut, c.Privacy)
	return wrapErr(err)
}

// InsertActiveCall records an open reservation.
func InsertActiveCall(ctx context.Context, q Querier, a *model.ActiveCall) error {
	_, err := q.Exec(ctx, `INSERT INTO active_calls (call_uuid, customer_id, account_id, called_number, reserved_amount, max_call_seconds, started_at, expires_at, node)
		VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9) ON CONFLICT (call_uuid) DO NOTHING`,
		a.CallUUID, a.CustomerID, a.AccountID, a.CalledNumber, a.ReservedAmount, a.MaxCallSeconds, a.StartedAt, a.ExpiresAt, a.Node)
	return wrapErr(err)
}

// LockActiveCall loads and locks the active call row; ErrNotFound when the
// call was already billed or never reserved.
func LockActiveCall(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*model.ActiveCall, error) {
	rows, err := tx.Query(ctx, `SELECT call_uuid, customer_id, account_id, called_number, reserved_amount, max_call_seconds, started_at, expires_at
		FROM active_calls WHERE call_uuid = $1 FOR UPDATE`, id)
	if err != nil {
		return nil, wrapErr(err)
	}
	a, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[model.ActiveCall])
	if err != nil {
		return nil, wrapErr(err)
	}
	return a, nil
}

// DeleteActiveCall removes the open reservation row.
func DeleteActiveCall(ctx context.Context, q Querier, id uuid.UUID) error {
	_, err := q.Exec(ctx, `DELETE FROM active_calls WHERE call_uuid = $1`, id)
	return wrapErr(err)
}

// ActiveCalls lists open reservations.
func (s *Store) ActiveCalls(ctx context.Context) ([]model.ActiveCall, error) {
	return many[model.ActiveCall](ctx, s.pool, `SELECT call_uuid, customer_id, account_id, called_number, reserved_amount, max_call_seconds, started_at, expires_at
		FROM active_calls ORDER BY started_at`)
}

// ExpiredActiveCalls lists reservations older than their expiry plus grace.
func (s *Store) ExpiredActiveCalls(ctx context.Context, grace time.Duration, limit int) ([]model.ActiveCall, error) {
	return many[model.ActiveCall](ctx, s.pool, `SELECT call_uuid, customer_id, account_id, called_number, reserved_amount, max_call_seconds, started_at, expires_at, node
		FROM active_calls WHERE expires_at + $1::interval < now() ORDER BY expires_at LIMIT $2`, grace, limit)
}

// StaleActiveCalls lists reservations of this node started more than age ago
// (candidates for the reconciliation sweep when this node's FreeSWITCH can be
// asked about them). Reservations made before the node column existed
// (empty node) are included so an upgrade does not leave them behind.
func (s *Store) StaleActiveCalls(ctx context.Context, node string, age time.Duration, limit int) ([]model.ActiveCall, error) {
	return many[model.ActiveCall](ctx, s.pool, `SELECT call_uuid, customer_id, account_id, called_number, reserved_amount, max_call_seconds, started_at, expires_at, node
		FROM active_calls WHERE (node = $3 OR node = '') AND started_at + $1::interval < now() ORDER BY started_at LIMIT $2`, age, limit, node)
}

// CountActiveCallsByCustomer returns the number of open reservations per customer.
func (s *Store) CountActiveCallsByCustomer(ctx context.Context) (map[uuid.UUID]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT customer_id, count(*) FROM active_calls GROUP BY customer_id`)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	m := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		m[id] = n
	}
	return m, rows.Err()
}
