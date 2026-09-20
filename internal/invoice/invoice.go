// Package invoice builds monthly invoices (prepaid usage statements) per
// account from the ledger and the CDRs, stores them, and renders them as PDF
// (D-63). Generation is idempotent: one invoice per account and period.
package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Line is the usage of one destination in the period.
type Line struct {
	Destination   string `json:"destination"`
	Calls         int64  `json:"calls"`
	BilledSeconds int64  `json:"billed_seconds"`
	Amount        string `json:"amount"`
}

// Invoice is one stored invoice.
type Invoice struct {
	ID            uuid.UUID `json:"id" db:"id"`
	Number        string    `json:"number" db:"number"`
	AccountID     uuid.UUID `json:"account_id" db:"account_id"`
	OwnerType     string    `json:"owner_type" db:"owner_type"`
	OwnerID       uuid.UUID `json:"owner_id" db:"owner_id"`
	OwnerName     string    `json:"owner_name" db:"owner_name"`
	PeriodStart   time.Time `json:"period_start" db:"period_start"`
	PeriodEnd     time.Time `json:"period_end" db:"period_end"`
	Currency      string    `json:"currency" db:"currency"`
	Opening       string    `json:"opening" db:"opening"`
	Topups        string    `json:"topups" db:"topups"`
	Charges       string    `json:"charges" db:"charges"`
	Costs         string    `json:"costs" db:"costs"`
	Adjustments   string    `json:"adjustments" db:"adjustments"`
	Refunds       string    `json:"refunds" db:"refunds"`
	Closing       string    `json:"closing" db:"closing"`
	Calls         int64     `json:"calls" db:"calls"`
	BilledSeconds int64     `json:"billed_seconds" db:"billed_seconds"`
	Lines         []Line    `json:"lines" db:"lines"`
	// Kind is "monthly" (worker or a YYYY-MM request) or "custom" (from/to dates).
	Kind string `json:"kind" db:"kind"`
	// Amount is the invoiced usage: charges for a customer, costs for a carrier (positive).
	Amount string `json:"amount" db:"amount"`
	// Paid is the sum of payment allocations; Status derives from Amount and Paid.
	Paid      string    `json:"paid" db:"paid"`
	Status    string    `json:"status" db:"status"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Operator is the issuer printed on the PDF.
type Operator struct {
	Name    string
	Address string // lines separated by "\n" or "|"
	Footer  string
}

// Service generates and stores invoices.
type Service struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	Op   Operator
}

// New creates the service.
func New(pool *pgxpool.Pool, log *slog.Logger, op Operator) *Service {
	return &Service{pool: pool, log: log, Op: op}
}

// ErrExists is returned by Generate when the invoice already exists.
var ErrExists = errors.New("invoice already exists for this period")

// ErrOverlap is returned when another invoice of the account covers part of the period.
var ErrOverlap = errors.New("another invoice overlaps this period")

// Period returns the calendar month [start, end) that contains t, in UTC.
func Period(t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// Generate builds the monthly invoice of one account for the month containing `at`.
// It returns ErrExists when one already exists (the existing one is returned
// too), so callers and the monthly worker can call it freely.
func (s *Service) Generate(ctx context.Context, accountID uuid.UUID, at time.Time) (*Invoice, error) {
	start, end := Period(at)
	return s.GeneratePeriod(ctx, accountID, start, end, "monthly")
}

// GeneratePeriod builds an invoice for [start, end). Periods of one account
// may not overlap (ErrOverlap): every call is invoiced once.
func (s *Service) GeneratePeriod(ctx context.Context, accountID uuid.UUID, start, end time.Time, kind string) (*Invoice, error) {
	start, end = start.UTC(), end.UTC()
	if !end.After(start) {
		return nil, errors.New("period end must be after its start")
	}
	if existing, err := s.ByPeriod(ctx, accountID, start, end); err == nil {
		return existing, ErrExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var overlap string
	err := s.pool.QueryRow(ctx, `SELECT number FROM invoices WHERE account_id = $1 AND period_start < $3 AND period_end > $2 ORDER BY period_start LIMIT 1`, accountID, start, end).Scan(&overlap)
	if err == nil {
		return nil, fmt.Errorf("%w (%s)", ErrOverlap, overlap)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	inv := &Invoice{AccountID: accountID, PeriodStart: start, PeriodEnd: end, Kind: kind, Lines: []Line{}}
	err = s.pool.QueryRow(ctx, `
		SELECT a.owner_type, a.owner_id, a.currency,
		       COALESCE((SELECT name FROM customers WHERE id = a.owner_id AND a.owner_type = 'customer'),
		                (SELECT name FROM carriers WHERE id = a.owner_id AND a.owner_type = 'carrier'), '')
		FROM accounts a WHERE a.id = $1`, accountID).Scan(&inv.OwnerType, &inv.OwnerID, &inv.Currency, &inv.OwnerName)
	if err != nil {
		return nil, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT sum(amount) FROM ledger_entries WHERE account_id = $1 AND type NOT IN ('reserve','release') AND created_at < $2), 0)::text,
		       COALESCE((SELECT sum(amount) FROM ledger_entries WHERE account_id = $1 AND type NOT IN ('reserve','release') AND created_at < $3), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'topup'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'charge'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'cost'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'adjustment'), 0)::text,
		       COALESCE(sum(amount) FILTER (WHERE type = 'refund'), 0)::text
		FROM ledger_entries WHERE account_id = $1 AND created_at >= $2 AND created_at < $3`, accountID, start, end).
		Scan(&inv.Opening, &inv.Closing, &inv.Topups, &inv.Charges, &inv.Costs, &inv.Adjustments, &inv.Refunds)
	if err != nil {
		return nil, err
	}
	// Usage per destination from the CDRs of the period (customer: sold;
	// carrier: bought).
	var rows pgx.Rows
	if inv.OwnerType == "customer" {
		rows, err = s.pool.Query(ctx, `
			SELECT COALESCE(sell_destination, ''), count(*), COALESCE(sum(sell_billed_seconds), 0), COALESCE(sum(sell_price), 0)::text
			FROM cdrs WHERE customer_id = $1 AND disposition = 'answered' AND start_time >= $2 AND start_time < $3
			GROUP BY 1 ORDER BY sum(sell_price) DESC, 1`, inv.OwnerID, start, end)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT COALESCE(sell_destination, ''), count(*), COALESCE(sum(buy_billed_seconds), 0), COALESCE(sum(cost), 0)::text
			FROM cdrs WHERE carrier_id = $1 AND disposition = 'answered' AND start_time >= $2 AND start_time < $3
			GROUP BY 1 ORDER BY sum(cost) DESC, 1`, inv.OwnerID, start, end)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.Destination, &l.Calls, &l.BilledSeconds, &l.Amount); err != nil {
			return nil, err
		}
		inv.Calls += l.Calls
		inv.BilledSeconds += l.BilledSeconds
		inv.Lines = append(inv.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lines, _ := json.Marshal(inv.Lines)
	// The invoiced amount is the usage of the period: what the customer was
	// charged, or what the carrier cost, as a positive number.
	usage := inv.Charges
	if inv.OwnerType == "carrier" {
		usage = inv.Costs
	}
	amount := negate(usage)
	err = s.pool.QueryRow(ctx, `
		INSERT INTO invoices (number, account_id, owner_type, owner_id, owner_name, period_start, period_end, currency,
		  opening, topups, charges, costs, adjustments, refunds, closing, calls, billed_seconds, lines, kind, amount)
		VALUES ('INV-' || to_char($5::timestamptz, 'YYYYMM') || '-' || lpad(nextval('invoice_seq')::text, 6, '0'),
		  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19::numeric)
		RETURNING id, number, created_at`,
		accountID, inv.OwnerType, inv.OwnerID, inv.OwnerName, start, end, inv.Currency,
		inv.Opening, inv.Topups, inv.Charges, inv.Costs, inv.Adjustments, inv.Refunds, inv.Closing, inv.Calls, inv.BilledSeconds, lines, kind, amount).
		Scan(&inv.ID, &inv.Number, &inv.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		existing, err2 := s.ByPeriod(ctx, accountID, start, end)
		if err2 != nil {
			return nil, err2
		}
		return existing, ErrExists
	}
	if err != nil {
		return nil, err
	}
	inv.Amount, inv.Paid, inv.Status = amount, "0.000000", statusOf(amount, "0")
	return inv, nil
}

// statusOf derives the invoice status from its amount and what was allocated to it.
func statusOf(amount, paid string) string {
	a, _ := decimal.NewFromString(amount)
	p, _ := decimal.NewFromString(paid)
	switch {
	case a.IsZero() && p.IsZero():
		return "nothing_due"
	case p.IsZero():
		return "open"
	case p.LessThan(a):
		return "partial"
	case p.Equal(a):
		return "paid"
	default:
		return "overpaid"
	}
}

const cols = `i.id, i.number, i.account_id, i.owner_type, i.owner_id, i.owner_name, i.period_start, i.period_end, i.currency::text AS currency,
	i.opening::text AS opening, i.topups::text AS topups, i.charges::text AS charges, i.costs::text AS costs, i.adjustments::text AS adjustments,
	i.refunds::text AS refunds, i.closing::text AS closing, i.calls, i.billed_seconds, i.lines, i.kind, i.amount::text AS amount,
	COALESCE((SELECT sum(al.amount) FROM invoice_allocations al WHERE al.invoice_id = i.id), 0)::text AS paid, i.created_at`

// fill derives the status of loaded invoices.
func fill(invs []Invoice) []Invoice {
	for k := range invs {
		invs[k].Status = statusOf(invs[k].Amount, invs[k].Paid)
	}
	return invs
}

// ByPeriod loads the invoice of an account for exactly [start, end).
func (s *Service) ByPeriod(ctx context.Context, accountID uuid.UUID, start, end time.Time) (*Invoice, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+cols+` FROM invoices i WHERE i.account_id = $1 AND i.period_start = $2 AND i.period_end = $3`, accountID, start, end)
	inv, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByNameLax[Invoice])
	if err != nil {
		return nil, err
	}
	inv.Status = statusOf(inv.Amount, inv.Paid)
	return inv, nil
}

// ByID loads one invoice.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	rows, _ := s.pool.Query(ctx, `SELECT `+cols+` FROM invoices i WHERE i.id = $1`, id)
	inv, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByNameLax[Invoice])
	if err != nil {
		return nil, err
	}
	inv.Status = statusOf(inv.Amount, inv.Paid)
	return inv, nil
}

// List returns the invoices of an owner, newest first, or all when ownerID is nil.
func (s *Service) List(ctx context.Context, ownerType string, ownerID *uuid.UUID, limit int) ([]Invoice, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows pgx.Rows
	if ownerID != nil {
		rows, _ = s.pool.Query(ctx, `SELECT `+cols+` FROM invoices i WHERE i.owner_type = $1 AND i.owner_id = $2 ORDER BY i.period_start DESC LIMIT $3`, ownerType, *ownerID, limit)
	} else {
		rows, _ = s.pool.Query(ctx, `SELECT `+cols+` FROM invoices i ORDER BY i.period_start DESC, i.owner_name LIMIT $1`, limit)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Invoice])
	if out == nil {
		out = []Invoice{}
	}
	return fill(out), err
}

// GenerateMonth generates last month's invoice for every account that had a
// ledger movement or a non-zero balance. It is safe to call repeatedly.
func (s *Service) GenerateMonth(ctx context.Context, at time.Time) (int, error) {
	start, end := Period(at)
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM accounts a
		WHERE a.balance <> 0 OR EXISTS (SELECT 1 FROM ledger_entries l WHERE l.account_id = a.id AND l.created_at >= $1 AND l.created_at < $2)`, start, end)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if _, err := s.Generate(ctx, id, start); err == nil {
			n++
		} else if errors.Is(err, ErrOverlap) {
			s.log.Debug("monthly invoice skipped, a custom invoice covers the month", "account_id", id, "err", err)
		} else if !errors.Is(err, ErrExists) {
			s.log.Error("invoice generation failed", "account_id", id, "err", err)
		}
	}
	return n, nil
}

// Run is the monthly worker: every hour it makes sure last month's invoices
// exist. Running on every node is harmless (unique constraint).
func (s *Service) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		lastMonth := time.Now().UTC().AddDate(0, 0, -time.Now().UTC().Day()) // any instant in the previous month
		if n, err := s.GenerateMonth(ctx, lastMonth); err != nil {
			s.log.Error("monthly invoices", "err", err)
		} else if n > 0 {
			s.log.Info("monthly invoices generated", "count", n, "period", fmt.Sprint(Period(lastMonth)))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
