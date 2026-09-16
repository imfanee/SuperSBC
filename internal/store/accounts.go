package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/model"
)

// ErrInsufficientFunds is returned when a reservation cannot be afforded.
var ErrInsufficientFunds = errors.New("insufficient funds")

const accountCols = `id, owner_type::text AS owner_type, owner_id, currency, balance, allowed_credit, reserved, created_at, updated_at`

// AccountByOwner loads the account of a customer or carrier.
func (s *Store) AccountByOwner(ctx context.Context, ownerType string, ownerID uuid.UUID) (*model.Account, error) {
	return AccountByOwnerQ(ctx, s.pool, ownerType, ownerID)
}

// AccountByOwnerQ loads the account through q.
func AccountByOwnerQ(ctx context.Context, q Querier, ownerType string, ownerID uuid.UUID) (*model.Account, error) {
	return one[model.Account](ctx, q, `SELECT `+accountCols+` FROM accounts WHERE owner_type = $1::owner_kind AND owner_id = $2`, ownerType, ownerID)
}

// AccountByID loads an account.
func (s *Store) AccountByID(ctx context.Context, id uuid.UUID) (*model.Account, error) {
	return one[model.Account](ctx, s.pool, `SELECT `+accountCols+` FROM accounts WHERE id = $1`, id)
}

// EnsureAccount creates the account of an owner if missing.
func (s *Store) EnsureAccount(ctx context.Context, ownerType string, ownerID uuid.UUID, currency string) (*model.Account, error) {
	return one[model.Account](ctx, s.pool, `
		INSERT INTO accounts (owner_type, owner_id, currency) VALUES ($1::owner_kind, $2, $3)
		ON CONFLICT (owner_type, owner_id) DO UPDATE SET currency = accounts.currency
		RETURNING `+accountCols, ownerType, ownerID, currency)
}

// SetAllowedCredit updates the credit limit.
func (s *Store) SetAllowedCredit(ctx context.Context, accountID uuid.UUID, credit decimal.Decimal) error {
	_, err := s.pool.Exec(ctx, `UPDATE accounts SET allowed_credit = $2::numeric WHERE id = $1`, accountID, credit)
	return wrapErr(err)
}

// Reserve atomically adds amount to accounts.reserved when the account can
// afford it (Section 3 Step 5). The WHERE clause re-checks affordability
// under the row lock taken by UPDATE, which is what serialises two
// concurrent calls from one customer. A ledger "reserve" entry is written.
func Reserve(ctx context.Context, tx pgx.Tx, accountID, callUUID uuid.UUID, amount decimal.Decimal, description string) (*model.Account, error) {
	rows, err := tx.Query(ctx, `
		UPDATE accounts SET reserved = reserved + $2::numeric
		WHERE id = $1 AND (balance + allowed_credit - reserved) > 0 AND (balance + allowed_credit - reserved) >= $2::numeric
		RETURNING `+accountCols, accountID, amount)
	if err != nil {
		return nil, wrapErr(err)
	}
	acc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[model.Account])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInsufficientFunds
		}
		return nil, wrapErr(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (account_id, call_uuid, type, amount, balance_after, description)
		VALUES ($1, $2, 'reserve', $3::numeric, $4::numeric, $5)`, accountID, callUUID, amount.Neg(), acc.Balance, description); err != nil {
		return nil, wrapErr(err)
	}
	return acc, nil
}

// LockAccount loads an account FOR UPDATE inside tx.
func LockAccount(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (*model.Account, error) {
	rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = $1 FOR UPDATE`, accountID)
	if err != nil {
		return nil, wrapErr(err)
	}
	acc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[model.Account])
	if err != nil {
		return nil, wrapErr(err)
	}
	return acc, nil
}

// Release removes a reservation (reserved -= amount) and writes a "release"
// ledger entry. The account row must already be locked by the caller.
func Release(ctx context.Context, tx pgx.Tx, accountID, callUUID uuid.UUID, amount decimal.Decimal, description string) (*model.Account, error) {
	if amount.IsZero() {
		return LockAccount(ctx, tx, accountID)
	}
	rows, err := tx.Query(ctx, `
		UPDATE accounts SET reserved = GREATEST(reserved - $2::numeric, 0) WHERE id = $1 RETURNING `+accountCols, accountID, amount)
	if err != nil {
		return nil, wrapErr(err)
	}
	acc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[model.Account])
	if err != nil {
		return nil, wrapErr(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (account_id, call_uuid, type, amount, balance_after, description)
		VALUES ($1, $2, 'release', $3::numeric, $4::numeric, $5)`, accountID, callUUID, amount, acc.Balance, description); err != nil {
		return nil, wrapErr(err)
	}
	return acc, nil
}

// Post moves money on the balance: balance += amount (amount is signed) and
// writes a ledger entry of the given type. Used for charge (negative), cost
// (negative), topup, adjustment and refund.
func Post(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, callUUID *uuid.UUID, kind string, amount decimal.Decimal, description string, createdBy *string) (*model.Account, error) {
	rows, err := tx.Query(ctx, `UPDATE accounts SET balance = balance + $2::numeric WHERE id = $1 RETURNING `+accountCols, accountID, amount)
	if err != nil {
		return nil, wrapErr(err)
	}
	acc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[model.Account])
	if err != nil {
		return nil, wrapErr(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (account_id, call_uuid, type, amount, balance_after, description, created_by)
		VALUES ($1, $2, $3::ledger_kind, $4::numeric, $5::numeric, $6, $7)`, accountID, callUUID, kind, amount, acc.Balance, description, createdBy); err != nil {
		return nil, wrapErr(err)
	}
	return acc, nil
}

// Ledger lists the entries of an account, newest first.
func (s *Store) Ledger(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]model.LedgerEntry, error) {
	return many[model.LedgerEntry](ctx, s.pool, `SELECT id, account_id, call_uuid, type::text AS type, amount, balance_after, description, created_by, created_at
		FROM ledger_entries WHERE account_id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3`, accountID, limit, offset)
}

// ReconcileRow is one account's invariant check (Section 13).
type ReconcileRow struct {
	AccountID      uuid.UUID       `db:"account_id"`
	OwnerType      string          `db:"owner_type"`
	OwnerID        uuid.UUID       `db:"owner_id"`
	Balance        decimal.Decimal `db:"balance"`
	LedgerBalance  decimal.Decimal `db:"ledger_balance"`
	Reserved       decimal.Decimal `db:"reserved"`
	OpenReserved   decimal.Decimal `db:"open_reserved"`
	LedgerReserved decimal.Decimal `db:"ledger_reserved"`
}

// OK reports whether the invariants hold for this account.
func (r ReconcileRow) OK() bool {
	return r.Balance.Equal(r.LedgerBalance) && r.Reserved.Equal(r.OpenReserved) && r.Reserved.Equal(r.LedgerReserved)
}

// String formats the row for the reconcile report.
func (r ReconcileRow) String() string {
	return fmt.Sprintf("%s %s balance=%s ledger=%s reserved=%s open_calls=%s ledger_open_reserves=%s",
		r.OwnerType, r.OwnerID, r.Balance.StringFixed(6), r.LedgerBalance.StringFixed(6), r.Reserved.StringFixed(6), r.OpenReserved.StringFixed(6), r.LedgerReserved.StringFixed(6))
}

// Reconcile computes, per account: balance vs sum of non-reserve ledger
// amounts, reserved vs sum of active_calls reservations, and reserved vs
// (sum of reserve entries + sum of release entries), which must all agree.
func (s *Store) Reconcile(ctx context.Context) ([]ReconcileRow, error) {
	return many[ReconcileRow](ctx, s.pool, `
		SELECT a.id AS account_id, a.owner_type::text AS owner_type, a.owner_id, a.balance, a.reserved,
		  COALESCE((SELECT SUM(amount) FROM ledger_entries l WHERE l.account_id = a.id AND l.type IN ('charge','cost','topup','adjustment','refund')), 0) AS ledger_balance,
		  COALESCE((SELECT SUM(reserved_amount) FROM active_calls c WHERE c.account_id = a.id), 0) AS open_reserved,
		  COALESCE(-(SELECT SUM(amount) FROM ledger_entries l WHERE l.account_id = a.id AND l.type IN ('reserve','release')), 0) AS ledger_reserved
		FROM accounts a ORDER BY a.owner_type, a.owner_id`)
}
