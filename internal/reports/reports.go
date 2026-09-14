// Package reports implements the CDR reports of Section 10 as SQL over cdrs
// (and, for the dashboard, over the hourly roll-up table).
package reports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service runs report queries.
type Service struct {
	pool *pgxpool.Pool
}

// New creates the service.
func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Range is a half-open time window with optional scoping.
type Range struct {
	From       time.Time
	To         time.Time
	CustomerID *uuid.UUID
	CarrierID  *uuid.UUID
}

func (r Range) conds() ([]string, []any) {
	conds := []string{"d.disposition <> 'pending'", "d.start_time >= $1", "d.start_time < $2"}
	args := []any{r.From, r.To}
	if r.CustomerID != nil {
		args = append(args, *r.CustomerID)
		conds = append(conds, fmt.Sprintf("d.customer_id = $%d", len(args)))
	}
	if r.CarrierID != nil {
		args = append(args, *r.CarrierID)
		conds = append(conds, fmt.Sprintf("d.carrier_id = $%d", len(args)))
	}
	return conds, args
}

// Row is one aggregated line: the group key plus the standard KPIs.
type Row struct {
	Key        string  `json:"key" db:"key"`
	Label      string  `json:"label" db:"label"`
	Attempts   int64   `json:"attempts" db:"attempts"`
	Answered   int64   `json:"answered" db:"answered"`
	Rejected   int64   `json:"rejected" db:"rejected"`
	Failed     int64   `json:"failed" db:"failed"`
	ASR        float64 `json:"asr" db:"asr"`
	NER        float64 `json:"ner" db:"ner"`
	ACD        float64 `json:"acd" db:"acd"`
	Billsec    int64   `json:"billsec" db:"billsec"`
	Minutes    float64 `json:"minutes" db:"minutes"`
	Revenue    string  `json:"revenue" db:"revenue"`
	Cost       string  `json:"cost" db:"cost"`
	Margin     string  `json:"margin" db:"margin"`
	MarginPct  float64 `json:"margin_pct" db:"margin_pct"`
	PDDAvg     float64 `json:"pdd_avg_ms" db:"pdd_avg_ms"`
	PDDP95     float64 `json:"pdd_p95_ms" db:"pdd_p95_ms"`
	ShortCalls int64   `json:"short_calls" db:"short_calls"`
}

// kpiSelect is the shared KPI projection. NER counts answered plus callee
// side outcomes (busy, no answer, cancelled) as "network delivered".
const kpiSelect = `
	count(*) AS attempts,
	count(*) FILTER (WHERE d.disposition = 'answered') AS answered,
	count(*) FILTER (WHERE d.disposition IN ('rejected_auth','rejected_balance','rejected_route')) AS rejected,
	count(*) FILTER (WHERE d.disposition = 'failed') AS failed,
	COALESCE(count(*) FILTER (WHERE d.disposition = 'answered')::float / NULLIF(count(*) FILTER (WHERE d.disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 0) AS asr,
	COALESCE(count(*) FILTER (WHERE d.disposition IN ('answered','busy','no_answer','cancelled'))::float / NULLIF(count(*) FILTER (WHERE d.disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 0) AS ner,
	COALESCE(avg(d.billsec) FILTER (WHERE d.disposition = 'answered'), 0)::float AS acd,
	COALESCE(sum(d.billsec), 0)::bigint AS billsec,
	COALESCE(sum(d.billsec), 0)::float / 60 AS minutes,
	COALESCE(sum(d.sell_price), 0)::text AS revenue,
	COALESCE(sum(d.cost), 0)::text AS cost,
	COALESCE(sum(d.sell_price - d.cost), 0)::text AS margin,
	COALESCE(sum(d.sell_price - d.cost)::float / NULLIF(sum(d.sell_price)::float, 0), 0) AS margin_pct,
	COALESCE(avg(d.pdd_ms), 0)::float AS pdd_avg_ms,
	COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY d.pdd_ms), 0)::float AS pdd_p95_ms,
	count(*) FILTER (WHERE d.disposition = 'answered' AND d.billsec < 6) AS short_calls`

const fromCDR = ` FROM cdrs d LEFT JOIN customers c ON c.id = d.customer_id LEFT JOIN carriers k ON k.id = d.carrier_id`

// groupExpr returns the key and label expressions for a group_by value.
// prefix:N groups by the first N digits of the called number.
func groupExpr(groupBy string) (key, label string, err error) {
	switch {
	case groupBy == "" || groupBy == "total":
		return "'total'", "'Total'", nil
	case groupBy == "customer":
		return "COALESCE(d.customer_id::text, '')", "COALESCE(c.name, '(unknown)')", nil
	case groupBy == "carrier":
		return "COALESCE(d.carrier_id::text, '')", "COALESCE(k.name, '(none)')", nil
	case groupBy == "destination":
		return "COALESCE(d.sell_destination, '')", "COALESCE(d.sell_destination, '(no rate)')", nil
	case groupBy == "hour":
		return "to_char(date_trunc('hour', d.start_time), 'YYYY-MM-DD\"T\"HH24:00:00Z')", "to_char(date_trunc('hour', d.start_time), 'YYYY-MM-DD HH24:00')", nil
	case groupBy == "day":
		return "to_char(date_trunc('day', d.start_time), 'YYYY-MM-DD')", "to_char(date_trunc('day', d.start_time), 'YYYY-MM-DD')", nil
	case groupBy == "month":
		return "to_char(date_trunc('month', d.start_time), 'YYYY-MM')", "to_char(date_trunc('month', d.start_time), 'YYYY-MM')", nil
	case groupBy == "hour_of_day":
		return "to_char(d.start_time, 'HH24')", "to_char(d.start_time, 'HH24') || ':00'", nil
	case groupBy == "sip_code":
		return "COALESCE(d.sip_final_code::text, '')", "COALESCE(d.sip_final_code::text || ' ' || COALESCE(d.sip_final_reason, ''), '(none)')", nil
	case groupBy == "hangup_cause":
		return "COALESCE(d.hangup_cause, '')", "COALESCE(d.hangup_cause, '(none)')", nil
	case groupBy == "disposition":
		return "d.disposition::text", "d.disposition::text", nil
	case groupBy == "src_ip":
		return "COALESCE(d.src_ip::text, '')", "COALESCE(d.src_ip::text, '(none)')", nil
	case groupBy == "failover_depth":
		return "d.failover_depth::text", "'carrier #' || (d.failover_depth + 1)::text", nil
	case strings.HasPrefix(groupBy, "prefix:"):
		var n int
		if _, err := fmt.Sscanf(groupBy, "prefix:%d", &n); err != nil || n < 1 || n > 15 {
			return "", "", fmt.Errorf("bad prefix length")
		}
		e := fmt.Sprintf("left(d.called_number, %d)", n)
		return e, e, nil
	}
	return "", "", fmt.Errorf("unknown group_by %q", groupBy)
}

// Traffic is the general purpose aggregation: any group_by with all KPIs.
func (s *Service) Traffic(ctx context.Context, r Range, groupBy, orderBy string, limit int) ([]Row, error) {
	key, label, err := groupExpr(groupBy)
	if err != nil {
		return nil, err
	}
	conds, args := r.conds()
	order := "attempts DESC"
	switch orderBy {
	case "key":
		order = "key"
	case "minutes":
		order = "billsec DESC"
	case "revenue":
		order = "sum(d.sell_price) DESC"
	case "cost":
		order = "sum(d.cost) DESC"
	case "margin":
		order = "sum(d.sell_price - d.cost) DESC"
	case "asr":
		order = "asr DESC"
	}
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	q := fmt.Sprintf("SELECT %s AS key, %s AS label, %s%s WHERE %s GROUP BY 1, 2 ORDER BY %s LIMIT %d", key, label, kpiSelect, fromCDR, strings.Join(conds, " AND "), order, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return collectRows(rows)
}

func collectRows(rows pgx.Rows) ([]Row, error) {
	out, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Row])
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Row{}
	}
	return out, nil
}

// Summary is the dashboard payload.
type Summary struct {
	Total          Row             `json:"total"`
	LiveCalls      int64           `json:"live_calls"`
	CPSNow         int64           `json:"cps_now"`
	Hourly         []Row           `json:"hourly"`
	TopDestination []Row           `json:"top_destinations"`
	Rejections     []Row           `json:"rejections"`
	LowBalance     []LowBalanceRow `json:"low_balance"`
}

// LowBalanceRow is a customer under the low balance threshold.
type LowBalanceRow struct {
	CustomerID uuid.UUID `json:"customer_id" db:"customer_id"`
	Name       string    `json:"name" db:"name"`
	Available  string    `json:"available" db:"available"`
	Currency   string    `json:"currency" db:"currency"`
}

// Summary computes the dashboard numbers for the range.
func (s *Service) Summary(ctx context.Context, r Range, lowBalance string) (*Summary, error) {
	total, err := s.Traffic(ctx, r, "total", "", 1)
	if err != nil {
		return nil, err
	}
	out := &Summary{}
	if len(total) > 0 {
		out.Total = total[0]
	}
	// The hourly profile comes from the roll-up table (fast on large CDR
	// tables); the worker refreshes the current hour every minute.
	if out.Hourly, err = s.HourlyFromRollup(ctx, r); err != nil {
		return nil, err
	}
	if out.TopDestination, err = s.Traffic(ctx, r, "destination", "minutes", 10); err != nil {
		return nil, err
	}
	if out.Rejections, err = s.Traffic(ctx, r, "disposition", "", 20); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM active_calls`).Scan(&out.LiveCalls); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM cdrs WHERE start_time >= now() - interval '1 second'`).Scan(&out.CPSNow); err != nil {
		return nil, err
	}
	if lowBalance == "" {
		lowBalance = "0"
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id AS customer_id, c.name, account_available(a)::text AS available, a.currency
		FROM accounts a JOIN customers c ON c.id = a.owner_id AND a.owner_type = 'customer' AND c.deleted_at IS NULL AND c.status = 'active'
		WHERE account_available(a) < $1::numeric ORDER BY account_available(a) LIMIT 20`, lowBalance)
	if err != nil {
		return nil, err
	}
	if out.LowBalance, err = pgx.CollectRows(rows, pgx.RowToStructByNameLax[LowBalanceRow]); err != nil {
		return nil, err
	}
	if out.LowBalance == nil {
		out.LowBalance = []LowBalanceRow{}
	}
	return out, nil
}

// QualityRow is one quality bucket (per carrier per day).
type QualityRow struct {
	Row
	MOSAvg      float64 `json:"mos_avg" db:"mos_avg"`
	LossAvg     float64 `json:"loss_avg" db:"loss_avg"`
	JitterAvg   float64 `json:"jitter_avg" db:"jitter_avg"`
	ShortRatio  float64 `json:"short_ratio" db:"short_ratio"`
	FalseAnswer int64   `json:"false_answer" db:"false_answer"`
}

// Quality returns ASR/ACD/PDD plus RTP statistics per group and day.
// False answer supervision indicator: answered calls shorter than 3 s with no RTP received.
func (s *Service) Quality(ctx context.Context, r Range, groupBy string) ([]QualityRow, error) {
	key, label, err := groupExpr(groupBy)
	if err != nil {
		return nil, err
	}
	conds, args := r.conds()
	q := fmt.Sprintf(`SELECT %s || ' ' || to_char(date_trunc('day', d.start_time), 'YYYY-MM-DD') AS key, %s || ' ' || to_char(date_trunc('day', d.start_time), 'YYYY-MM-DD') AS label, %s,
		COALESCE(avg((d.rtp_stats->>'rtp_audio_in_mos')::float), 0) AS mos_avg,
		COALESCE(avg(NULLIF(d.rtp_stats->>'rtp_audio_in_skip_packet_count','')::float / NULLIF((d.rtp_stats->>'rtp_audio_in_packet_count')::float, 0)), 0) AS loss_avg,
		COALESCE(avg((d.rtp_stats->>'rtp_audio_in_jitter_max_variance')::float), 0) AS jitter_avg,
		COALESCE(count(*) FILTER (WHERE d.disposition = 'answered' AND d.billsec < 6)::float / NULLIF(count(*) FILTER (WHERE d.disposition = 'answered'), 0), 0) AS short_ratio,
		count(*) FILTER (WHERE d.disposition = 'answered' AND d.billsec < 3 AND COALESCE((d.rtp_stats->>'rtp_audio_in_packet_count')::float, 0) = 0) AS false_answer
		%s WHERE %s GROUP BY 1, 2 ORDER BY 1`, key, label, kpiSelect, fromCDR, strings.Join(conds, " AND "))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[QualityRow])
}

// StatementLine is one ledger movement in a statement.
type StatementLine struct {
	ID           int64      `json:"id" db:"id"`
	Type         string     `json:"type" db:"type"`
	Amount       string     `json:"amount" db:"amount"`
	BalanceAfter string     `json:"balance_after" db:"balance_after"`
	Description  string     `json:"description" db:"description"`
	CallUUID     *uuid.UUID `json:"call_uuid" db:"call_uuid"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// Statement is the balance statement of an account for a period.
type Statement struct {
	AccountID   uuid.UUID       `json:"account_id"`
	Currency    string          `json:"currency"`
	Opening     string          `json:"opening_balance"`
	Closing     string          `json:"closing_balance"`
	Topups      string          `json:"topups"`
	Charges     string          `json:"charges"`
	Costs       string          `json:"costs"`
	Adjustments string          `json:"adjustments"`
	Refunds     string          `json:"refunds"`
	Calls       int64           `json:"calls"`
	Lines       []StatementLine `json:"lines"`
}

// Statement computes opening balance, movements and closing balance.
func (s *Service) Statement(ctx context.Context, accountID uuid.UUID, from, to time.Time, withLines bool) (*Statement, error) {
	st := &Statement{AccountID: accountID}
	if err := s.pool.QueryRow(ctx, `SELECT currency FROM accounts WHERE id = $1`, accountID).Scan(&st.Currency); err != nil {
		return nil, err
	}
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT sum(amount) FROM ledger_entries WHERE account_id = $1 AND type NOT IN ('reserve','release') AND created_at < $2), 0)::text,
		       COALESCE((SELECT sum(amount) FROM ledger_entries WHERE account_id = $1 AND type NOT IN ('reserve','release') AND created_at < $3), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'topup'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'charge'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'cost'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'adjustment'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'refund'), 0)::text,
		       count(*) FILTER (WHERE type IN ('charge','cost'))
		FROM ledger_entries WHERE account_id = $1 AND created_at >= $2 AND created_at < $3`, accountID, from, to).
		Scan(&st.Opening, &st.Closing, &st.Topups, &st.Charges, &st.Costs, &st.Adjustments, &st.Refunds, &st.Calls)
	if err != nil {
		return nil, err
	}
	st.Lines = []StatementLine{}
	if withLines {
		rows, err := s.pool.Query(ctx, `SELECT id, type::text AS type, amount::text AS amount, balance_after::text AS balance_after, description, call_uuid, created_at
			FROM ledger_entries WHERE account_id = $1 AND type NOT IN ('reserve','release') AND created_at >= $2 AND created_at < $3 ORDER BY id LIMIT 10000`, accountID, from, to)
		if err != nil {
			return nil, err
		}
		if st.Lines, err = pgx.CollectRows(rows, pgx.RowToStructByNameLax[StatementLine]); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// PeakRow is the concurrency and CPS peak of one scope.
type PeakRow struct {
	Key            string    `json:"key" db:"key"`
	Label          string    `json:"label" db:"label"`
	PeakConcurrent int64     `json:"peak_concurrent" db:"peak_concurrent"`
	PeakAt         time.Time `json:"peak_at" db:"peak_at"`
	PeakCPS        int64     `json:"peak_cps" db:"peak_cps"`
	PeakCPSAt      time.Time `json:"peak_cps_at" db:"peak_cps_at"`
}

// Peaks computes peak concurrent calls (sweep over start and end events) and
// peak CPS (max calls started in one second) per customer, carrier or system.
func (s *Service) Peaks(ctx context.Context, r Range, groupBy string) ([]PeakRow, error) {
	var key, label string
	switch groupBy {
	case "customer":
		key, label = "COALESCE(d.customer_id::text, '')", "COALESCE(c.name, '(unknown)')"
	case "carrier":
		key, label = "COALESCE(d.carrier_id::text, '')", "COALESCE(k.name, '(none)')"
	default:
		key, label = "'system'", "'System'"
	}
	conds, args := r.conds()
	q := fmt.Sprintf(`
		WITH scoped AS (SELECT %s AS key, %s AS label, d.start_time, COALESCE(d.end_time, d.start_time) AS end_time, d.disposition %s WHERE %s),
		events AS (
			SELECT key, label, start_time AS at, 1 AS delta FROM scoped WHERE disposition = 'answered'
			UNION ALL SELECT key, label, end_time AS at, -1 FROM scoped WHERE disposition = 'answered'),
		running AS (SELECT key, label, at, sum(delta) OVER (PARTITION BY key ORDER BY at, delta ROWS UNBOUNDED PRECEDING) AS n FROM events),
		conc AS (SELECT DISTINCT ON (key) key, label, n AS peak_concurrent, at AS peak_at FROM running ORDER BY key, n DESC, at),
		cps AS (SELECT key, label, count(*) AS n, date_trunc('second', start_time) AS sec FROM scoped GROUP BY 1, 2, 4),
		cpsmax AS (SELECT DISTINCT ON (key) key, label, n AS peak_cps, sec AS peak_cps_at FROM cps ORDER BY key, n DESC, sec)
		SELECT COALESCE(conc.key, cpsmax.key) AS key, COALESCE(conc.label, cpsmax.label) AS label,
		       COALESCE(conc.peak_concurrent, 0)::bigint AS peak_concurrent, COALESCE(conc.peak_at, cpsmax.peak_cps_at, now()) AS peak_at,
		       COALESCE(cpsmax.peak_cps, 0)::bigint AS peak_cps, COALESCE(cpsmax.peak_cps_at, now()) AS peak_cps_at
		FROM cpsmax FULL OUTER JOIN conc ON conc.key = cpsmax.key ORDER BY peak_concurrent DESC`, key, label, fromCDR, strings.Join(conds, " AND "))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByNameLax[PeakRow])
}
