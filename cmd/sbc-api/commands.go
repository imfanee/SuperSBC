package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/imfanee/supersbc/internal/config"
	"github.com/imfanee/supersbc/internal/db"
	"github.com/imfanee/supersbc/internal/seed"
	"github.com/imfanee/supersbc/internal/store"
)

func runSeed(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	return withDB(ctx, cfg, func(pool dbPool) error {
		if err := db.Migrate(ctx, pool, log); err != nil {
			return err
		}
		st := store.New(pool)
		if err := seed.Run(ctx, st, log); err != nil {
			return err
		}
		// Ask running API nodes to refresh caches and re-render FreeSWITCH config.
		rdb, err := connectRedis(ctx, cfg)
		if err == nil {
			defer func() { _ = rdb.Close() }()
			publishAll(ctx, rdb)
		}
		return nil
	})
}

// runReconcile prints the invariant check for every account (Section 13)
// and exits non-zero when any account disagrees with its ledger.
func runReconcile(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	return withDB(ctx, cfg, func(pool dbPool) error {
		st := store.New(pool)
		rows, err := st.Reconcile(ctx)
		if err != nil {
			return err
		}
		bad := 0
		for _, r := range rows {
			status := "OK  "
			if !r.OK() {
				status = "FAIL"
				bad++
			}
			fmt.Printf("%s %s\n", status, r.String())
		}
		fmt.Printf("%d accounts checked, %d mismatches\n", len(rows), bad)
		if bad > 0 {
			return errors.New("reconcile: invariants violated")
		}
		log.Info("reconcile ok", "accounts", len(rows))
		return nil
	})
}
