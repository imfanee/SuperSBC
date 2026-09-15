// Command sbc-api is the OpenSBC control plane: internal call control API,
// admin REST API, billing engine, ESL consumer and background workers.
//
//	@title			OpenSBC Admin API
//	@version		1.0
//	@description	Session Border Controller control plane: customers, carriers, rate decks, routing, CDRs, reports.
//	@BasePath		/api/v1
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in				header
//	@name			Authorization
//	@description	"Bearer sbc_..." API key, or the sbc_access cookie set by POST /auth/login (with X-CSRF-Token for state changes).
//
// Usage:
//
//	sbc-api [serve]        run everything (default)
//	sbc-api migrate        apply pending migrations and exit
//	sbc-api migrate-down   roll back the latest migration and exit
//	sbc-api seed           load the demo data set (idempotent) and exit
//	sbc-api reconcile      prove ledger and account invariants and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/opensbc/opensbc/internal/config"
	"github.com/opensbc/opensbc/internal/db"
	"github.com/opensbc/opensbc/internal/esl"
	"github.com/opensbc/opensbc/internal/httpapi/health"
	"github.com/opensbc/opensbc/internal/logging"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	log := logging.New(cfg.LogLevel).With("service", "sbc-api", "node", cfg.NodeName, "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	var runErr error
	switch cmd {
	case "serve":
		runErr = serve(ctx, cfg, log)
	case "migrate":
		runErr = withDB(ctx, cfg, func(pool dbPool) error { return db.Migrate(ctx, pool, log) })
	case "migrate-down":
		runErr = withDB(ctx, cfg, func(pool dbPool) error { return db.MigrateDown(ctx, pool, log) })
	case "seed":
		runErr = runSeed(ctx, cfg, log)
	case "reconcile":
		runErr = runReconcile(ctx, cfg, log)
	default:
		runErr = fmt.Errorf("unknown command %q", cmd)
	}
	stop()
	if runErr != nil {
		log.Error("exit", "error", runErr)
		os.Exit(1)
	}
}

func serve(ctx context.Context, cfg *config.Config, log *slogLogger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := db.Migrate(ctx, pool, log); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	rdb, err := connectRedis(ctx, cfg)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	sup := esl.NewSupervisor(cfg.ESLHost, cfg.ESLPort, cfg.ESLPassword, log)
	app := buildApp(ctx, cfg, log, pool, rdb, sup)
	go sup.Run(ctx)
	app.startWorkers(ctx)

	hd := health.Deps{DB: pool, Redis: rdb, ESL: sup, Profiles: []string{"external-ingress", "external-egress"}, Version: version, Node: cfg.NodeName}

	admin := chi.NewRouter()
	admin.Use(middleware.Recoverer, requestLogger(log))
	admin.Get("/healthz", health.Healthz)
	admin.Get("/readyz", hd.Readyz)
	app.mountAdmin(admin)

	internal := chi.NewRouter()
	internal.Use(middleware.Recoverer, requestLogger(log))
	internal.Get("/healthz", health.Healthz)
	internal.Get("/readyz", hd.Readyz)
	app.mountInternal(internal)

	servers := []*http.Server{
		{Addr: cfg.AdminListen, Handler: admin, ReadHeaderTimeout: 10 * time.Second},
		{Addr: cfg.InternalListen, Handler: internal, ReadHeaderTimeout: 10 * time.Second},
	}
	errCh := make(chan error, len(servers))
	for _, s := range servers {
		go func(s *http.Server) {
			log.Info("listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("listen %s: %w", s.Addr, err)
			}
		}(s)
	}
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		return err
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shCtx)
	}
	return nil
}
