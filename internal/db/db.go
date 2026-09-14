// Package db owns the Postgres connection pool and the embedded goose
// migrations (D-09).
package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Connect opens a pgx pool and verifies connectivity.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(gooseLogger{log})
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()
	return goose.UpContext(ctx, sqlDB, "migrations")
}

// MigrateDown rolls back the most recent migration (used by make rollback).
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(gooseLogger{log})
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()
	return goose.DownContext(ctx, sqlDB, "migrations")
}

type gooseLogger struct{ l *slog.Logger }

func (g gooseLogger) Fatalf(format string, v ...any) { g.l.Error(fmt.Sprintf(format, v...)) }
func (g gooseLogger) Printf(format string, v ...any) { g.l.Info(fmt.Sprintf(format, v...)) }
