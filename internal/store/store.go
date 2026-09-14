// Package store is the Postgres repository layer (D-08). Every query is
// parameterised; money columns are scanned into decimal.Decimal.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on unique violations.
var ErrConflict = errors.New("conflict")

// Store wraps the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool for callers that need transactions.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Querier is satisfied by *pgxpool.Pool, pgx.Tx and *pgxpool.Conn.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Tx is a pgx transaction.
type Tx = pgx.Tx

// WithTx runs f inside a transaction, committing on nil error.
func (s *Store) WithTx(ctx context.Context, f func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// wrapErr maps pgx errors to the package sentinel errors.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.Detail)
		case "23503":
			return fmt.Errorf("%w: %s", ErrConflict, pgErr.Detail)
		}
	}
	return err
}

func one[T any](ctx context.Context, q Querier, sql string, args ...any) (*T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrapErr(err)
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByNameLax[T])
	if err != nil {
		return nil, wrapErr(err)
	}
	return v, nil
}

func many[T any](ctx context.Context, q Querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, wrapErr(err)
	}
	v, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return nil, wrapErr(err)
	}
	return v, nil
}
