package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/billing"
	"github.com/opensbc/opensbc/internal/callcontrol"
	"github.com/opensbc/opensbc/internal/config"
	"github.com/opensbc/opensbc/internal/db"
	"github.com/opensbc/opensbc/internal/esl"
	"github.com/opensbc/opensbc/internal/failover"
	"github.com/opensbc/opensbc/internal/fsconfig"
	"github.com/opensbc/opensbc/internal/gateways"
	"github.com/opensbc/opensbc/internal/httpapi/internalapi"
	"github.com/opensbc/opensbc/internal/logging"
	"github.com/opensbc/opensbc/internal/store"
	"github.com/opensbc/opensbc/internal/tables"
)

type slogLogger = slog.Logger
type dbPool = *pgxpool.Pool

// app holds the wired components. Milestones add fields here.
type app struct {
	cfg      *config.Config
	log      *slog.Logger
	db       *pgxpool.Pool
	rdb      *redis.Client
	esl      *esl.Supervisor
	st       *store.Store
	tables   *tables.Tables
	pipe     *callcontrol.Pipeline
	bill     *billing.Engine
	renderer *fsconfig.Renderer
	internal *internalapi.Handler
	gateways *gateways.Poller
}

func buildApp(ctx context.Context, cfg *config.Config, log *slog.Logger, pool *pgxpool.Pool, rdb *redis.Client, sup *esl.Supervisor) *app {
	st := store.New(pool)
	rules, err := failover.Load(cfg.Failover.RulesFile)
	if err != nil {
		log.Warn("failover rules file not loaded, using built-in defaults", "path", cfg.Failover.RulesFile, "error", err)
		rules = failover.Default()
	}
	rules.MinRingSecondsForNoAnswer = cfg.Failover.MinRingSecondsForNoAnswer
	tb := tables.New(st, log)
	pipe := callcontrol.New(cfg, log, st, rdb, tb, rules)
	bill := billing.New(cfg, log, st, rdb)
	renderer := fsconfig.New(cfg.FSConfigDir, cfg.ACLMode, cfg.FSNodeIP, st, sup, log)
	gw := gateways.New(sup, "external-egress", time.Duration(cfg.Failover.GatewayPingIntervalSeconds)*time.Second, log)
	pipe.SetHealth(gw)
	a := &app{cfg: cfg, log: log, db: pool, rdb: rdb, esl: sup, st: st, tables: tb, pipe: pipe, bill: bill, renderer: renderer, gateways: gw}
	a.internal = internalapi.New(cfg.InternalSecret, log, pipe, bill, st)

	// Render gateways and ACLs before FreeSWITCH starts (compose depends_on)
	// and again after every ESL (re)connect so FreeSWITCH restarts pick up
	// the current database state.
	if err := renderer.RenderAll(ctx); err != nil {
		log.Error("initial freeswitch config render failed", "error", err)
	}
	sup.OnConnect(func(c *esl.Client) {
		if err := renderer.RenderAll(ctx); err != nil {
			log.Error("freeswitch config render on connect failed", "error", err)
		}
	})
	sup.Events(a.onESLEvent, "CHANNEL_HANGUP_COMPLETE")
	return a
}

// onESLEvent is billing trigger 1 of 2 (mod_json_cdr is 2 of 2).
func (a *app) onESLEvent(ev esl.Event) {
	if ev.Name != "CHANNEL_HANGUP_COMPLETE" {
		return
	}
	if ev.Get("Call-Direction") != "inbound" || ev.Get("variable_sofia_profile_name") != "external-ingress" {
		return
	}
	vars := make(map[string]string, len(ev.Headers))
	for k, v := range ev.Headers {
		if strings.HasPrefix(k, "variable_") {
			vars[strings.TrimPrefix(k, "variable_")] = v
		}
	}
	vars["uuid"] = ev.Get("Unique-ID")
	if vars["hangup_cause"] == "" {
		vars["hangup_cause"] = ev.Get("Hangup-Cause")
	}
	info, ok := billing.FromVariables(vars, "esl")
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ctx = logging.WithCallUUID(ctx, a.log, info.CallUUID.String())
		if _, err := a.bill.Bill(ctx, info); err != nil {
			a.log.Error("bill from esl failed", "call_uuid", info.CallUUID, "error", err)
		}
	}()
}

func (a *app) startWorkers(ctx context.Context) {
	go a.tables.Listen(ctx, a.rdb)
	go a.listenConfigChanges(ctx)
	go a.gateways.Run(ctx)
	go billing.NewReconciler(a.bill, a.esl).Run(ctx, time.Minute)
}

func (a *app) mountAdmin(_ chi.Router) {}

func (a *app) mountInternal(r chi.Router) { a.internal.Mount(r) }

func withDB(ctx context.Context, cfg *config.Config, f func(dbPool) error) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return f(pool)
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
