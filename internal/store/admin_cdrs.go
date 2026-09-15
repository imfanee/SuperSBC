package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/opensbc/opensbc/internal/model"
)

// CDRFilter is the rich filter of the CDR list (Section 8).
type CDRFilter struct {
	From         *time.Time
	To           *time.Time
	CustomerID   *uuid.UUID
	CarrierID    *uuid.UUID
	Prefix       string
	Caller       string
	Disposition  string
	SIPCode      *int
	MinBillsec   *int
	MaxBillsec   *int
	SrcIP        string
	CallUUID     *uuid.UUID
	NegativeOnly bool
}

func (f CDRFilter) conds() ([]string, []any) {
	conds := []string{"d.disposition <> 'pending'"}
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.From != nil {
		add("d.start_time >= $%d", *f.From)
	}
	if f.To != nil {
		add("d.start_time < $%d", *f.To)
	}
	if f.CustomerID != nil {
		add("d.customer_id = $%d", *f.CustomerID)
	}
	if f.CarrierID != nil {
		add("d.carrier_id = $%d", *f.CarrierID)
	}
	if f.Prefix != "" {
		add("d.called_number LIKE $%d", f.Prefix+"%")
	}
	if f.Caller != "" {
		add("d.caller_number LIKE $%d", f.Caller+"%")
	}
	if f.Disposition != "" {
		add("d.disposition::text = $%d", f.Disposition)
	}
	if f.SIPCode != nil {
		add("d.sip_final_code = $%d", *f.SIPCode)
	}
	if f.MinBillsec != nil {
		add("d.billsec >= $%d", *f.MinBillsec)
	}
	if f.MaxBillsec != nil {
		add("d.billsec <= $%d", *f.MaxBillsec)
	}
	if f.SrcIP != "" {
		add("d.src_ip = $%d::inet", f.SrcIP)
	}
	if f.CallUUID != nil {
		add("d.call_uuid = $%d", *f.CallUUID)
	}
	if f.NegativeOnly {
		conds = append(conds, "d.margin < 0")
	}
	return conds, args
}

// CDRRow is a CDR with customer and carrier names.
type CDRRow struct {
	model.CDR
	CustomerName *string `json:"customer_name" db:"customer_name"`
	CarrierName  *string `json:"carrier_name" db:"carrier_name"`
}

const cdrRowCols = `d.call_uuid, d.customer_id, d.carrier_id, d.src_ip::text AS src_ip, d.src_port, d.caller_number_raw, d.caller_number, d.called_number_raw, d.called_number,
	d.start_time, d.progress_time, d.answer_time, d.end_time, d.pdd_ms, d.ring_seconds, d.billsec, d.duration, d.sip_final_code, d.sip_final_reason, d.hangup_cause,
	d.disposition::text AS disposition, d.reject_reason, d.sell_rate_id, d.sell_rate_per_min, d.sell_billed_seconds, d.sell_price, d.sell_destination, d.sell_currency, d.buy_currency,
	d.buy_rate_id, d.buy_rate_per_min, d.buy_billed_seconds, d.cost, d.margin, d.negative_margin, d.reserved_amount, d.charged_amount, d.released_amount,
	d.attempts, d.failover_depth, d.codec_in, d.codec_out, d.media_mode, COALESCE(d.rtp_stats, 'null'::jsonb) AS rtp_stats, d.transport_in, d.transport_out, d.srtp_in, d.srtp_out, d.privacy, d.stir_status, d.stir_attest, d.sbc_node, d.billed_at, d.billed_by, d.created_at, d.updated_at,
	c.name AS customer_name, k.name AS carrier_name`

const cdrRowFrom = ` FROM cdrs d LEFT JOIN customers c ON c.id = d.customer_id LEFT JOIN carriers k ON k.id = d.carrier_id`

// ListCDRs pages through CDRs.
func (s *Store) ListCDRs(ctx context.Context, f CDRFilter, p Page) (*Listing[CDRRow], error) {
	p = p.Normalize()
	conds, args := f.conds()
	var total int64
	if err := s.pool.QueryRow(ctx, "SELECT count(*)"+cdrRowFrom+where(conds), args...).Scan(&total); err != nil {
		return nil, wrapErr(err)
	}
	order := p.orderBy(map[string]string{"start_time": "d.start_time", "billsec": "d.billsec", "sell_price": "d.sell_price", "cost": "d.cost", "margin": "d.margin", "pdd_ms": "d.pdd_ms"}, "d.start_time")
	if p.Sort == "" {
		order = " ORDER BY d.start_time DESC"
	}
	args = append(args, p.PerPage, p.Offset())
	items, err := many[CDRRow](ctx, s.pool, "SELECT "+cdrRowCols+cdrRowFrom+where(conds)+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, err
	}
	return &Listing[CDRRow]{Items: items, Total: total, Page: p.Page, PerPage: p.PerPage}, nil
}

// StreamCDRs calls fn for every matching CDR (CSV export), oldest first.
func (s *Store) StreamCDRs(ctx context.Context, f CDRFilter, fn func(CDRRow) error) error {
	conds, args := f.conds()
	rows, err := s.pool.Query(ctx, "SELECT "+cdrRowCols+cdrRowFrom+where(conds)+" ORDER BY d.start_time", args...)
	if err != nil {
		return wrapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := pgx.RowToStructByNameLax[CDRRow](rows)
		if err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CDRRowByUUID loads one CDR with names.
func (s *Store) CDRRowByUUID(ctx context.Context, id uuid.UUID) (*CDRRow, error) {
	return one[CDRRow](ctx, s.pool, "SELECT "+cdrRowCols+cdrRowFrom+" WHERE d.call_uuid = $1", id)
}

// ActiveCallRow is an active call with names for the live view.
type ActiveCallRow struct {
	model.ActiveCall
	CustomerName string     `json:"customer_name" db:"customer_name"`
	CallerNumber string     `json:"caller_number" db:"caller_number"`
	SrcIP        *string    `json:"src_ip" db:"src_ip"`
	CarrierName  *string    `json:"carrier_name" db:"carrier_name"`
	Attempts     int        `json:"attempts" db:"attempts"`
	AnswerTime   *time.Time `json:"answer_time" db:"answer_time"`
}

// ActiveCallRows lists calls with open reservations, joined with the CDR in progress.
func (s *Store) ActiveCallRows(ctx context.Context) ([]ActiveCallRow, error) {
	return many[ActiveCallRow](ctx, s.pool, `
		SELECT a.call_uuid, a.customer_id, a.account_id, a.called_number, a.reserved_amount, a.max_call_seconds, a.started_at, a.expires_at,
		  c.name AS customer_name, d.caller_number, d.src_ip::text AS src_ip, k.name AS carrier_name, jsonb_array_length(d.attempts) AS attempts, d.answer_time
		FROM active_calls a JOIN customers c ON c.id = a.customer_id JOIN cdrs d ON d.call_uuid = a.call_uuid
		LEFT JOIN carriers k ON k.id = d.carrier_id ORDER BY a.started_at`)
}
