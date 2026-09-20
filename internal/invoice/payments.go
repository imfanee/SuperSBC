package invoice

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// Payment is money received (customer) or paid (carrier) against invoices (D-72).
type Payment struct {
	ID            uuid.UUID    `json:"id" db:"id"`
	AccountID     uuid.UUID    `json:"account_id" db:"account_id"`
	Amount        string       `json:"amount" db:"amount"`
	Currency      string       `json:"currency" db:"currency"`
	ReceivedAt    time.Time    `json:"received_at" db:"received_at"`
	Reference     string       `json:"reference" db:"reference"`
	Method        string       `json:"method" db:"method"`
	Notes         string       `json:"notes" db:"notes"`
	LedgerEntryID *int64       `json:"ledger_entry_id" db:"ledger_entry_id"`
	CreatedBy     *string      `json:"created_by" db:"created_by"`
	CreatedAt     time.Time    `json:"created_at" db:"created_at"`
	Allocations   []Allocation `json:"allocations" db:"-"`
	// Unallocated is the part of the payment not assigned to any invoice (credit on account).
	Unallocated string `json:"unallocated" db:"-"`
}

// Allocation is the part of a payment assigned to one invoice.
type Allocation struct {
	PaymentID     uuid.UUID `json:"payment_id" db:"payment_id"`
	InvoiceID     uuid.UUID `json:"invoice_id" db:"invoice_id"`
	InvoiceNumber string    `json:"invoice_number" db:"invoice_number"`
	Amount        string    `json:"amount" db:"amount"`
	ReceivedAt    time.Time `json:"received_at" db:"received_at"`
	Reference     string    `json:"reference" db:"reference"`
}

// AllocationInput is a requested allocation.
type AllocationInput struct {
	InvoiceID uuid.UUID
	Amount    decimal.Decimal
}

// InsertPayment records a payment and its allocations inside tx. With
// autoAllocate the unallocated remainder is spread over the account's open
// invoices, oldest first. Allocations may not exceed the payment or what an
// invoice still owes. The ledger entry id (a top-up posted by the caller in
// the same transaction) is stored for cross reference.
func InsertPayment(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, amount decimal.Decimal, currency string, receivedAt time.Time,
	reference, method, notes string, allocs []AllocationInput, autoAllocate bool, ledgerEntryID *int64, by string) (*Payment, error) {
	if !amount.IsPositive() {
		return nil, errors.New("payment amount must be positive")
	}
	p := &Payment{AccountID: accountID, Amount: amount.StringFixed(6), Currency: currency, ReceivedAt: receivedAt.UTC(), Reference: reference, Method: method, Notes: notes, LedgerEntryID: ledgerEntryID, Allocations: []Allocation{}}
	err := tx.QueryRow(ctx, `INSERT INTO invoice_payments (account_id, amount, currency, received_at, reference, method, notes, ledger_entry_id, created_by)
		VALUES ($1, $2::numeric, $3, $4, $5, $6, $7, $8, $9) RETURNING id, created_at, created_by`,
		accountID, amount, currency, receivedAt.UTC(), reference, method, notes, ledgerEntryID, by).Scan(&p.ID, &p.CreatedAt, &p.CreatedBy)
	if err != nil {
		return nil, err
	}
	// Open invoices of the account with what they still owe, locked.
	type open struct {
		id     uuid.UUID
		number string
		start  time.Time
		due    decimal.Decimal
	}
	rows, err := tx.Query(ctx, `SELECT i.id, i.number, i.period_start,
			i.amount - COALESCE((SELECT sum(al.amount) FROM invoice_allocations al WHERE al.invoice_id = i.id), 0)
		FROM invoices i WHERE i.account_id = $1 ORDER BY i.period_start FOR UPDATE OF i`, accountID)
	if err != nil {
		return nil, err
	}
	var opens []open
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.number, &o.start, &o.due); err != nil {
			rows.Close()
			return nil, err
		}
		byID[o.id] = len(opens)
		opens = append(opens, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	remaining := amount
	planned := map[uuid.UUID]decimal.Decimal{}
	for _, a := range allocs {
		i, ok := byID[a.InvoiceID]
		if !ok {
			return nil, fmt.Errorf("invoice %s does not belong to this account", a.InvoiceID)
		}
		if !a.Amount.IsPositive() {
			return nil, fmt.Errorf("allocation to %s must be positive", opens[i].number)
		}
		if a.Amount.GreaterThan(opens[i].due.Sub(planned[a.InvoiceID])) {
			return nil, fmt.Errorf("allocation to %s exceeds what it owes (%s)", opens[i].number, opens[i].due.Sub(planned[a.InvoiceID]).StringFixed(6))
		}
		if a.Amount.GreaterThan(remaining) {
			return nil, fmt.Errorf("allocations exceed the payment amount")
		}
		planned[a.InvoiceID] = planned[a.InvoiceID].Add(a.Amount)
		remaining = remaining.Sub(a.Amount)
	}
	if autoAllocate {
		for _, o := range opens {
			if !remaining.IsPositive() {
				break
			}
			due := o.due.Sub(planned[o.id])
			if !due.IsPositive() {
				continue
			}
			take := decimal.Min(due, remaining)
			planned[o.id] = planned[o.id].Add(take)
			remaining = remaining.Sub(take)
		}
	}
	ids := make([]uuid.UUID, 0, len(planned))
	for id := range planned {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return opens[byID[ids[a]]].start.Before(opens[byID[ids[b]]].start) })
	for _, id := range ids {
		amt := planned[id]
		if _, err := tx.Exec(ctx, `INSERT INTO invoice_allocations (payment_id, invoice_id, amount) VALUES ($1, $2, $3::numeric)`, p.ID, id, amt); err != nil {
			return nil, err
		}
		p.Allocations = append(p.Allocations, Allocation{PaymentID: p.ID, InvoiceID: id, InvoiceNumber: opens[byID[id]].number, Amount: amt.StringFixed(6), ReceivedAt: p.ReceivedAt, Reference: reference})
	}
	p.Unallocated = remaining.StringFixed(6)
	return p, nil
}

// SetLedgerEntry links the top-up posted for a payment.
func SetLedgerEntry(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID, ledgerEntryID int64) error {
	_, err := tx.Exec(ctx, `UPDATE invoice_payments SET ledger_entry_id = $2 WHERE id = $1`, paymentID, ledgerEntryID)
	return err
}

// Payments lists the payments of an account with their allocations, newest first.
func (s *Service) Payments(ctx context.Context, accountID uuid.UUID) ([]Payment, error) {
	rows, _ := s.pool.Query(ctx, `SELECT id, account_id, amount::text AS amount, currency::text AS currency, received_at, reference, method, notes, ledger_entry_id, created_by, created_at
		FROM invoice_payments WHERE account_id = $1 ORDER BY received_at DESC, created_at DESC LIMIT 1000`, accountID)
	pays, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Payment])
	if err != nil {
		return nil, err
	}
	if len(pays) == 0 {
		return []Payment{}, nil
	}
	rows, _ = s.pool.Query(ctx, `SELECT al.payment_id, al.invoice_id, i.number AS invoice_number, al.amount::text AS amount, p.received_at, p.reference
		FROM invoice_allocations al JOIN invoices i ON i.id = al.invoice_id JOIN invoice_payments p ON p.id = al.payment_id
		WHERE p.account_id = $1 ORDER BY i.period_start`, accountID)
	allocs, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Allocation])
	if err != nil {
		return nil, err
	}
	byPayment := map[uuid.UUID][]Allocation{}
	for _, a := range allocs {
		byPayment[a.PaymentID] = append(byPayment[a.PaymentID], a)
	}
	for k := range pays {
		pays[k].Allocations = byPayment[pays[k].ID]
		if pays[k].Allocations == nil {
			pays[k].Allocations = []Allocation{}
		}
		total, _ := decimal.NewFromString(pays[k].Amount)
		for _, a := range pays[k].Allocations {
			amt, _ := decimal.NewFromString(a.Amount)
			total = total.Sub(amt)
		}
		pays[k].Unallocated = total.StringFixed(6)
	}
	return pays, nil
}

// InvoiceAllocations lists the payments applied to one invoice.
func (s *Service) InvoiceAllocations(ctx context.Context, invoiceID uuid.UUID) ([]Allocation, error) {
	rows, _ := s.pool.Query(ctx, `SELECT al.payment_id, al.invoice_id, i.number AS invoice_number, al.amount::text AS amount, p.received_at, p.reference
		FROM invoice_allocations al JOIN invoices i ON i.id = al.invoice_id JOIN invoice_payments p ON p.id = al.payment_id
		WHERE al.invoice_id = $1 ORDER BY p.received_at`, invoiceID)
	out, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[Allocation])
	if out == nil {
		out = []Allocation{}
	}
	return out, err
}

// LedgerRow is one line of the invoice ledger: an invoice or a payment.
type LedgerRow struct {
	Kind        string       `json:"kind"` // invoice | payment
	At          time.Time    `json:"at"`
	Invoice     *Invoice     `json:"invoice,omitempty"`
	Payment     *Payment     `json:"payment,omitempty"`
	Allocations []Allocation `json:"allocations"`
}

// Ledger is the accounts receivable view of one account.
type Ledger struct {
	AccountID   uuid.UUID   `json:"account_id"`
	Currency    string      `json:"currency"`
	Invoiced    string      `json:"invoiced"`
	Received    string      `json:"received"`
	Allocated   string      `json:"allocated"`
	Unallocated string      `json:"unallocated"`
	Outstanding string      `json:"outstanding"` // invoiced minus allocated
	Rows        []LedgerRow `json:"rows"`
}

// InvoiceLedger returns invoices and payments of an account merged by date, newest first.
func (s *Service) InvoiceLedger(ctx context.Context, ownerType string, ownerID uuid.UUID) (*Ledger, error) {
	invs, err := s.List(ctx, ownerType, &ownerID, 500)
	if err != nil {
		return nil, err
	}
	var accountID uuid.UUID
	currency := ""
	if err := s.pool.QueryRow(ctx, `SELECT id, currency FROM accounts WHERE owner_type = $1 AND owner_id = $2`, ownerType, ownerID).Scan(&accountID, &currency); err != nil {
		return nil, err
	}
	pays, err := s.Payments(ctx, accountID)
	if err != nil {
		return nil, err
	}
	led := &Ledger{AccountID: accountID, Currency: currency, Rows: []LedgerRow{}}
	invoiced, received, allocated := decimal.Zero, decimal.Zero, decimal.Zero
	allocByInvoice := map[uuid.UUID][]Allocation{}
	for _, p := range pays {
		for _, a := range p.Allocations {
			allocByInvoice[a.InvoiceID] = append(allocByInvoice[a.InvoiceID], a)
		}
	}
	for k := range invs {
		inv := invs[k]
		a, _ := decimal.NewFromString(inv.Amount)
		pd, _ := decimal.NewFromString(inv.Paid)
		invoiced, allocated = invoiced.Add(a), allocated.Add(pd)
		al := allocByInvoice[inv.ID]
		if al == nil {
			al = []Allocation{}
		}
		led.Rows = append(led.Rows, LedgerRow{Kind: "invoice", At: inv.CreatedAt, Invoice: &invs[k], Allocations: al})
	}
	for k := range pays {
		a, _ := decimal.NewFromString(pays[k].Amount)
		received = received.Add(a)
		led.Rows = append(led.Rows, LedgerRow{Kind: "payment", At: pays[k].ReceivedAt, Payment: &pays[k], Allocations: pays[k].Allocations})
	}
	sort.SliceStable(led.Rows, func(i, j int) bool { return led.Rows[i].At.After(led.Rows[j].At) })
	led.Invoiced = invoiced.StringFixed(6)
	led.Received = received.StringFixed(6)
	led.Allocated = allocated.StringFixed(6)
	led.Unallocated = received.Sub(allocated).StringFixed(6)
	led.Outstanding = invoiced.Sub(allocated).StringFixed(6)
	return led, nil
}
