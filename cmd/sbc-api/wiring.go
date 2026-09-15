package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/auth"
	"github.com/opensbc/opensbc/internal/billing"
	"github.com/opensbc/opensbc/internal/callcontrol"
	"github.com/opensbc/opensbc/internal/config"
	"github.com/opensbc/opensbc/internal/db"
	"github.com/opensbc/opensbc/internal/esl"
	"github.com/opensbc/opensbc/internal/failover"
	"github.com/opensbc/opensbc/internal/fsconfig"
	"github.com/opensbc/opensbc/internal/gateways"
	"github.com/opensbc/opensbc/internal/httpapi/admin"
	"github.com/opensbc/opensbc/internal/httpapi/health"
	"github.com/opensbc/opensbc/internal/httpapi/internalapi"
	"github.com/opensbc/opensbc/internal/invoice"
	"github.com/opensbc/opensbc/internal/logging"
	"github.com/opensbc/opensbc/internal/metrics"
	"github.com/opensbc/opensbc/internal/reports"
	"github.com/opensbc/opensbc/internal/stir"
	"github.com/opensbc/opensbc/internal/store"
	"github.com/opensbc/opensbc/internal/tables"
	"github.com/opensbc/opensbc/internal/trace"
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
	admin    *admin.Handler
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
	bill.SetFX(tb)
	renderer := fsconfig.New(cfg.FSConfigDir, cfg.ACLMode, cfg.FSNodeIP, st, sup, log)
	renderer.SetBanExportFile(cfg.Ban.ExportFile)
	gw := gateways.New(sup, "external-egress", time.Duration(cfg.Failover.GatewayPingIntervalSeconds)*time.Second, log)
	breaker := callcontrol.NewBreaker(rdb, cfg.Failover.BreakerConsecutiveFaults, cfg.Failover.BreakerASRThresholdPercent, cfg.Failover.BreakerASRMinSamples, cfg.Failover.BreakerDegradedSeconds)
	pipe.SetBreaker(breaker)
	gw.SetDegrader(breaker)
	pipe.SetHealth(gw)
	if stirV, err := stir.New(stir.Config{MaxAge: cfg.STIR.MaxAge, CAFile: cfg.STIR.CAFile, AllowHTTP: cfg.STIR.AllowHTTP}); err != nil {
		log.Error("stir verifier disabled", "err", err)
	} else {
		pipe.SetSTIR(stirV)
	}
	a := &app{cfg: cfg, log: log, db: pool, rdb: rdb, esl: sup, st: st, tables: tb, pipe: pipe, bill: bill, renderer: renderer, gateways: gw}
	a.internal = internalapi.New(cfg.InternalSecret, log, pipe, bill, st)
	a.admin = admin.New(admin.Deps{Cfg: cfg, Log: log, Store: st, Redis: rdb, Pipe: pipe, Bill: bill, Tables: tb, ESL: sup, Gateways: gw, Renderer: renderer, Version: version,
		Reports:  admin.NewReports(reports.New(pool), cfg.Billing.LowBalanceThreshold),
		Trace:    trace.New(rdb, cfg.FSLogFile, sup),
		Invoices: invoice.New(pool, log, invoice.Operator{Name: cfg.Invoice.OperatorName, Address: cfg.Invoice.OperatorAddress, Footer: cfg.Invoice.Footer}),
		Ready: func(ctx context.Context) any {
			return health.Deps{DB: pool, Redis: rdb, ESL: sup, Profiles: []string{"external-ingress", "external-egress"}, Version: version, Node: cfg.NodeName}.Check(ctx)
		}})
	a.bootstrapAdmin(ctx)

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
	if ev.Get("Call-Direction") == "outbound" && ev.Get("variable_sofia_profile_name") == "external-egress" {
		a.onBLegHangup(ev)
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

// onBLegHangup enriches the a-leg CDR with the carrier side codec and RTP
// statistics (Section 7: media_mode, per-call RTP stats).
func (a *app) onBLegHangup(ev esl.Event) {
	id, err := uuid.Parse(ev.Get("variable_sip_h_X-SBC-Call"))
	if err != nil {
		if id, err = uuid.Parse(ev.Get("Other-Leg-Unique-ID")); err != nil {
			return
		}
	}
	codec := ev.Get("variable_read_codec")
	stats := map[string]any{}
	for k, v := range ev.Headers {
		if strings.HasPrefix(k, "variable_rtp_audio_") {
			stats[strings.TrimPrefix(k, "variable_")] = v
		}
	}
	if len(stats) == 0 {
		stats = nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.bill.RecordBLeg(ctx, id, codec, stats, ev.Get("variable_rtp_secure_media_confirmed_audio") == "true"); err != nil {
			a.log.Warn("record b-leg failed", "call_uuid", id, "error", err)
		}
	}()
}

func (a *app) startWorkers(ctx context.Context) {
	go a.tables.Listen(ctx, a.rdb)
	go a.listenConfigChanges(ctx)
	go a.gateways.Run(ctx)
	go billing.NewReconciler(a.bill, a.esl).Run(ctx, time.Minute)
	go a.gaugeLoop(ctx)
	go a.expireBans(ctx)
	go reports.NewRollup(reports.New(a.db), a.log).Run(ctx, time.Minute)
	go a.admin.Invoices.Run(ctx, time.Hour)
}

// gaugeLoop refreshes the gauge metrics every few seconds.
func (a *app) gaugeLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var n int
			if err := a.db.QueryRow(ctx, `SELECT count(*) FROM active_calls`).Scan(&n); err == nil {
				metrics.CallsInProgress.Set(float64(n))
			}
			for name, st := range a.gateways.States() {
				v := 0.0
				if st.Status == "UP" {
					v = 1
				}
				metrics.GatewayUp.WithLabelValues(name).Set(v)
			}
			if carriers, err := a.st.Carriers(ctx); err == nil {
				for _, c := range carriers {
					v := 0.0
					if a.pipe.Breaker().Degraded(ctx, c.ID) {
						v = 1
					}
					metrics.CarrierDegraded.WithLabelValues(c.Name).Set(v)
				}
			}
		}
	}
}

func (a *app) mountAdmin(r chi.Router) {
	r.Handle("/metrics", promhttp.Handler())
	a.admin.Mount(r)
}

// bootstrapAdmin creates the first admin user from the environment when the
// users table is empty, so a fresh install can log in.
func (a *app) bootstrapAdmin(ctx context.Context) {
	n, err := a.st.CountUsers(ctx)
	if err != nil || n > 0 {
		return
	}
	pw := a.cfg.Auth.BootstrapAdminPassword
	if pw == "" {
		a.log.Warn("no users exist and SBC_BOOTSTRAP_ADMIN_PASSWORD is empty: nobody can log in")
		return
	}
	if err := auth.CheckPasswordPolicy(pw); err != nil {
		a.log.Error("bootstrap admin password rejected", "error", err)
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		a.log.Error("bootstrap admin", "error", err)
		return
	}
	u, err := a.st.CreateUser(ctx, a.cfg.Auth.BootstrapAdminEmail, hash, "admin")
	if err != nil {
		a.log.Error("bootstrap admin", "error", err)
		return
	}
	a.log.Info("bootstrap admin user created", "email", u.Email)
}

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
