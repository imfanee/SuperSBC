// Package seed loads the demo data set (D-40). It is idempotent: every
// object is keyed by name and re-running only updates attributes; balances
// are only topped up when the account has no ledger history.
package seed

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/store"
)

type rateRow struct {
	prefix, dest, rate string
	fee                string
	initial, subseq    int
}

type carrierDef struct {
	name, host string
	port       int
	rates      []rateRow
}

type customerDef struct {
	name, ip, balance string
	credit            string
	maxCC, maxCPS     int
}

// Demo data. Carrier hosts are the sipp UAS containers of docker compose.
var (
	sellRates = []rateRow{
		{"1", "USA", "0.008000", "0", 6, 6},
		{"44", "UK Fixed", "0.010000", "0", 60, 60},
		{"447", "UK Mobile", "0.020000", "0", 60, 60},
		{"4477", "UK Mobile O2", "0.020000", "0", 60, 60},
		{"33", "France", "0.015000", "0", 60, 60},
		{"49", "Germany", "0.012000", "0", 30, 6},
		{"93", "Afghanistan", "0.250000", "0.010000", 60, 60},
	}
	carriers = []carrierDef{
		{"carrier-answer", "172.28.0.61", 5060, []rateRow{
			{"1", "USA", "0.005000", "0", 6, 6}, {"44", "UK Fixed", "0.006000", "0", 60, 60}, {"447", "UK Mobile", "0.015000", "0", 60, 60},
			{"33", "France", "0.010000", "0", 60, 60}, {"49", "Germany", "0.009000", "0", 1, 1}, {"93", "Afghanistan", "0.300000", "0", 60, 60}}},
		{"carrier-503", "172.28.0.62", 5060, []rateRow{
			{"44", "UK Fixed", "0.005500", "0", 60, 60}, {"447", "UK Mobile", "0.014000", "0", 60, 60}}},
		{"carrier-503b", "172.28.0.62", 5060, []rateRow{
			{"44", "UK Fixed", "0.005800", "0", 60, 60}, {"447", "UK Mobile", "0.014500", "0", 60, 60}}},
		{"carrier-404", "172.28.0.63", 5060, []rateRow{
			{"44", "UK Fixed", "0.005000", "0", 60, 60}, {"447", "UK Mobile", "0.013000", "0", 60, 60}}},
		// Nothing listens here: OPTIONS ping marks the gateway DOWN and routing skips it.
		{"carrier-down", "172.28.0.250", 5060, []rateRow{
			{"44", "UK Fixed", "0.004000", "0", 60, 60}, {"447", "UK Mobile", "0.012000", "0", 60, 60}}},
	}
	// prefix -> ordered carriers
	routes = []struct {
		prefix, dest string
		carriers     []string
	}{
		{"1", "USA", []string{"carrier-answer"}},
		{"44", "UK Fixed", []string{"carrier-answer"}},
		{"4420", "UK London", []string{"carrier-answer"}},
		{"447", "UK Mobile", []string{"carrier-answer"}},
		{"4477", "UK Mobile failover", []string{"carrier-503", "carrier-answer"}},
		{"4478", "UK Mobile all fail", []string{"carrier-503", "carrier-503b"}},
		{"4479", "UK Mobile number fault", []string{"carrier-404", "carrier-answer"}},
		{"4476", "UK Mobile gateway down", []string{"carrier-down", "carrier-answer"}},
		{"33", "France (no carriers)", []string{}},
	}
	customers = []customerDef{
		{"acme", "172.28.0.101/32", "10.000000", "0", 0, 0},
		{"beta", "172.28.0.102/32", "0.000000", "0", 0, 0},
		{"gamma", "172.28.0.103/32", "0.080000", "0", 0, 0},
		{"delta-limited", "172.28.0.104/32", "100.000000", "0", 1, 0},
		{"epsilon-cps", "172.28.0.105/32", "100.000000", "0", 0, 1},
	}
)

// Run loads the data set.
func Run(ctx context.Context, st *store.Store, log *slog.Logger) error {
	now := time.Now().Add(-time.Minute)
	sell, err := st.UpsertRateGroup(ctx, "retail-usd", "USD", "Demo selling deck")
	if err != nil {
		return err
	}
	for _, r := range sellRates {
		if err := upsertRate(ctx, st, sell.ID, r, now); err != nil {
			return err
		}
	}
	carrierIDs := map[string]uuid.UUID{}
	for _, c := range carriers {
		rg, err := st.UpsertRateGroup(ctx, c.name+"-buy", "USD", "Demo buying deck for "+c.name)
		if err != nil {
			return err
		}
		for _, r := range c.rates {
			if err := upsertRate(ctx, st, rg.ID, r, now); err != nil {
				return err
			}
		}
		rgID := rg.ID
		car, err := st.UpsertCarrier(ctx, &model.Carrier{
			Name: c.name, Status: "active", RateGroupID: &rgID, GatewayHost: c.host, GatewayPort: c.port, Transport: "udp",
			AllowedCodecs: []string{"PCMA", "PCMU"}, SIPOptionsPing: c.name == "carrier-down",
			Notes: "Demo carrier backed by a sipp UAS container (OPTIONS ping off: sipp UAS does not answer OPTIONS)",
		})
		if err != nil {
			return err
		}
		carrierIDs[c.name] = car.ID
		if _, err := st.EnsureAccount(ctx, "carrier", car.ID, "USD"); err != nil {
			return err
		}
	}
	rgrp, err := st.UpsertRouteGroup(ctx, "default", "Demo route group")
	if err != nil {
		return err
	}
	for _, r := range routes {
		route, err := st.UpsertRoute(ctx, rgrp.ID, r.prefix, r.dest)
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for _, n := range r.carriers {
			ids = append(ids, carrierIDs[n])
		}
		if err := st.SetRouteCarriers(ctx, route.ID, ids); err != nil {
			return err
		}
	}
	sellID := sell.ID
	routeID := rgrp.ID
	for _, c := range customers {
		cust, err := st.UpsertCustomer(ctx, &model.Customer{
			Name: c.name, Status: "active", RateGroupID: &sellID, RouteGroupID: &routeID,
			MaxConcurrentCalls: c.maxCC, MaxCPS: c.maxCPS, AllowedCodecs: []string{"PCMA", "PCMU", "OPUS", "G722"},
			Notes: "Demo customer",
		})
		if err != nil {
			return err
		}
		if err := st.UpsertCustomerIP(ctx, cust.ID, c.ip, nil, "any"); err != nil {
			return err
		}
		acc, err := st.EnsureAccount(ctx, "customer", cust.ID, "USD")
		if err != nil {
			return err
		}
		if err := st.SetAllowedCredit(ctx, acc.ID, decimal.RequireFromString(c.credit)); err != nil {
			return err
		}
		if err := topupIfFresh(ctx, st, acc, decimal.RequireFromString(c.balance)); err != nil {
			return err
		}
	}
	log.Info("seed complete", "customers", len(customers), "carriers", len(carriers), "routes", len(routes), "sell_rates", len(sellRates))
	return nil
}

func upsertRate(ctx context.Context, st *store.Store, groupID uuid.UUID, r rateRow, from time.Time) error {
	_, err := st.UpsertRate(ctx, &model.Rate{
		RateGroupID: groupID, Prefix: r.prefix, Destination: r.dest,
		RatePerMin: decimal.RequireFromString(r.rate), ConnectFee: decimal.RequireFromString(r.fee),
		InitialIncrement: r.initial, SubsequentIncrement: r.subseq, EffectiveFrom: from.Truncate(time.Second), Enabled: true,
	})
	return err
}

// topupIfFresh posts the opening balance only when the account has no ledger
// history, so the invariant balance == sum(ledger) always holds.
func topupIfFresh(ctx context.Context, st *store.Store, acc *model.Account, amount decimal.Decimal) error {
	entries, err := st.Ledger(ctx, acc.ID, 1, 0)
	if err != nil {
		return err
	}
	if len(entries) > 0 || amount.IsZero() {
		return nil
	}
	by := "seed"
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockAccount(ctx, tx, acc.ID); err != nil {
			return err
		}
		_, err := store.Post(ctx, tx, acc.ID, nil, model.LedgerTopup, amount, "opening balance (seed)", &by)
		return err
	})
}

// Summary returns a human readable description of the data set.
func Summary() string {
	return fmt.Sprintf("%d customers, %d carriers, %d routes", len(customers), len(carriers), len(routes))
}
