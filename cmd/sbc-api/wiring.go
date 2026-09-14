package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/config"
	"github.com/opensbc/opensbc/internal/db"
	"github.com/opensbc/opensbc/internal/esl"
)

type slogLogger = slog.Logger
type dbPool = *pgxpool.Pool

// app holds the wired components. Milestones add fields here.
type app struct {
	cfg *config.Config
	log *slog.Logger
	db  *pgxpool.Pool
	rdb *redis.Client
	esl *esl.Supervisor
}

func buildApp(_ context.Context, cfg *config.Config, log *slog.Logger, pool *pgxpool.Pool, rdb *redis.Client, sup *esl.Supervisor) *app {
	return &app{cfg: cfg, log: log, db: pool, rdb: rdb, esl: sup}
}

func (a *app) startWorkers(_ context.Context) {}

func (a *app) mountAdmin(_ chi.Router) {}

func (a *app) mountInternal(_ chi.Router) {}

func withDB(ctx context.Context, cfg *config.Config, f func(dbPool) error) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return f(pool)
}

func runSeed(_ context.Context, _ *config.Config, log *slog.Logger) error {
	log.Info("seed: nothing to do yet (M1)")
	return nil
}

func runReconcile(_ context.Context, _ *config.Config, log *slog.Logger) error {
	log.Info("reconcile: nothing to do yet (M1)")
	return nil
}

// requestLogger logs one JSON line per request with the call_uuid header when
// present so internal API calls can be correlated with Lua and FreeSWITCH.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			attrs := []any{"method", r.Method, "path", r.URL.Path, "status", ww.Status(), "bytes", ww.BytesWritten(), "duration_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr}
			if cu := r.Header.Get("X-Call-UUID"); cu != "" {
				attrs = append(attrs, "call_uuid", cu)
			}
			log.Info("http", attrs...)
		})
	}
}
