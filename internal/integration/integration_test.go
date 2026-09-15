//go:build integration

// Package integration holds tests that need a real Postgres and Redis
// (D-38). They run the migrations on the dedicated database given by
// SBC_TEST_DATABASE_URL (opensbc_test in compose), truncate it, and use the
// Redis database of SBC_TEST_REDIS_URL.
package integration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/opensbc/opensbc/internal/billing"
	"github.com/opensbc/opensbc/internal/cache"
	"github.com/opensbc/opensbc/internal/callcontrol"
	"github.com/opensbc/opensbc/internal/config"
	"github.com/opensbc/opensbc/internal/db"
	"github.com/opensbc/opensbc/internal/failover"
	"github.com/opensbc/opensbc/internal/logging"
	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/seed"
	"github.com/opensbc/opensbc/internal/store"
	"github.com/opensbc/opensbc/internal/tables"
)

type env struct {
	st   *store.Store
	pipe *callcontrol.Pipeline
	bill *billing.Engine
	cfg  *config.Config
}

func setup(t *testing.T) *env {
	t.Helper()
	dbURL := os.Getenv("SBC_TEST_DATABASE_URL")
	rdURL := os.Getenv("SBC_TEST_REDIS_URL")
	if dbURL == "" || rdURL == "" {
		t.Skip("SBC_TEST_DATABASE_URL / SBC_TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	log := logging.New("warn")
	pool, err := db.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}
	// clean slate: keep schema, drop data
	_, err = pool.Exec(ctx, `TRUNCATE cdrs, active_calls, ledger_entries, accounts, customer_ips, customers, route_carriers, routes, route_groups, rates, rate_groups, carriers, notifications, audit_log CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	rdb, err := cache.Connect(ctx, rdURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	if err := seed.Run(ctx, st, log); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{NodeName: "test-node", Billing: config.Billing{ReserveMinutes: 5, MaxCallDuration: 4 * time.Hour, OrphanTimeout: 10 * time.Minute, LowBalanceThreshold: "1.00"},
		Routing: config.Routing{OriginateTimeout: 60, ProgressTimeout: 8}}
	tb := tables.New(st, log)
	pipe := callcontrol.New(cfg, log, st, rdb, tb, failover.Default())
	bill := billing.New(cfg, log, st, rdb)
	return &env{st: st, pipe: pipe, bill: bill, cfg: cfg}
}

func TestIntegrationCustomerByIP(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	m, err := e.st.CustomerByIP(ctx, "172.28.0.101", 5060, "udp")
	if err != nil || m.Customer.Name != "acme" {
		t.Fatalf("acme lookup: %v %v", err, m)
	}
	if _, err := e.st.CustomerByIP(ctx, "172.28.0.199", 5060, "udp"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown ip: %v", err)
	}
	// CIDR range and most specific match
	c, _ := e.st.CustomerByName(ctx, "beta")
	if err := e.st.UpsertCustomerIP(ctx, c.ID, "10.9.0.0/16", nil, "any"); err != nil {
		t.Fatal(err)
	}
	a, _ := e.st.CustomerByName(ctx, "acme")
	if err := e.st.UpsertCustomerIP(ctx, a.ID, "10.9.1.0/24", nil, "any"); err != nil {
		t.Fatal(err)
	}
	m, err = e.st.CustomerByIP(ctx, "10.9.1.7", 5060, "tcp")
	if err != nil || m.Customer.Name != "acme" {
		t.Fatalf("most specific cidr should win: %v %v", err, m)
	}
	m, err = e.st.CustomerByIP(ctx, "10.9.2.7", 5060, "tcp")
	if err != nil || m.Customer.Name != "beta" {
		t.Fatalf("range match: %v %v", err, m)
	}
}

// The SQL and trie longest-prefix implementations must agree (Section 2.3).
func TestIntegrationLongestPrefixAgreement(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rg, err := e.st.RateGroupByName(ctx, "retail-usd")
	if err != nil {
		t.Fatal(err)
	}
	numbers := []string{"442071234567", "447700900123", "447500900123", "12125551234", "33123456789", "4912345678", "93700000000", "99123456789", "441", "4"}
	for _, n := range numbers {
		sqlRate, sqlErr := e.st.LongestPrefixSQL(ctx, rg.ID, n, time.Now())
		trieRate, ok, err := e.pipe.Tables().MatchRate(ctx, rg.ID, n)
		if err != nil {
			t.Fatal(err)
		}
		if errors.Is(sqlErr, store.ErrNotFound) {
			if ok {
				t.Errorf("%s: sql no match, trie matched %s", n, trieRate.Prefix)
			}
			continue
		}
		if sqlErr != nil {
			t.Fatal(sqlErr)
		}
		if !ok || trieRate.ID != sqlRate.ID {
			t.Errorf("%s: sql=%s trie=%v", n, sqlRate.Prefix, trieRate)
		}
	}
}

// Two concurrent reservations against funds for one: exactly one wins.
func TestIntegrationConcurrentReserve(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cust, _ := e.st.CustomerByName(ctx, "gamma") // 0.08 balance
	acc, _ := e.st.AccountByOwner(ctx, "customer", cust.ID)
	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := e.st.WithTx(ctx, func(tx pgx.Tx) error {
				_, err := store.Reserve(ctx, tx, acc.ID, uuid.New(), decimal.RequireFromString("0.05"), "test")
				return err
			})
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, store.ErrInsufficientFunds) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("wins = %d, want 1", wins)
	}
	after, _ := e.st.AccountByID(ctx, acc.ID)
	if !after.Reserved.Equal(decimal.RequireFromString("0.05")) {
		t.Fatalf("reserved = %s", after.Reserved)
	}
}

// Full in-process call: setup, attempts, billing, idempotent re-billing, reconcile.
func TestIntegrationSetupAndBill(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	callID := uuid.New()
	resp, err := e.pipe.Setup(ctx, callcontrol.SetupRequest{CallUUID: callID.String(), SrcIP: "172.28.0.101", SrcPort: 5060, Transport: "udp", Caller: "15550001111", Called: "00447700900123"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Action != "dial" || resp.Called != "447700900123" || len(resp.Carriers) != 2 {
		t.Fatalf("setup: %+v", resp)
	}
	if !resp.ReservedAmount.Equal(decimal.RequireFromString("0.10")) {
		t.Fatalf("reserved = %s", resp.ReservedAmount)
	}
	if resp.Carriers[0].Name != "carrier-503" || resp.Carriers[1].Name != "carrier-answer" {
		t.Fatalf("carrier order: %s, %s", resp.Carriers[0].Name, resp.Carriers[1].Name)
	}
	if resp.MaxCallSeconds != 14400 { // 10 USD at 0.02/min = 30000 s, capped at 4 h
		t.Fatalf("max_call_seconds = %d", resp.MaxCallSeconds)
	}
	acc, _ := e.st.AccountByOwner(ctx, "customer", *resp.CustomerID)
	if !acc.Reserved.Equal(decimal.RequireFromString("0.10")) {
		t.Fatalf("account reserved = %s", acc.Reserved)
	}
	// attempts
	now := time.Now()
	a1, err := e.pipe.RecordAttempt(ctx, callcontrol.AttemptRequest{CallUUID: callID.String(), Seq: 1, CarrierID: resp.Carriers[0].CarrierID, SIPCode: 503, HangupCause: "NORMAL_TEMPORARY_FAILURE", StartedAt: &now, EndedAt: &now, BuyRateID: resp.Carriers[0].BuyRateID})
	if err != nil || !a1.Continue {
		t.Fatalf("attempt 1: %v %+v", err, a1)
	}
	rate := resp.Carriers[1].BuyRatePerMin.StringFixed(6)
	a2, err := e.pipe.RecordAttempt(ctx, callcontrol.AttemptRequest{CallUUID: callID.String(), Seq: 2, CarrierID: resp.Carriers[1].CarrierID, SIPCode: 200, Answered: true, StartedAt: &now, EndedAt: &now, BuyRateID: resp.Carriers[1].BuyRateID, BuyRatePerMin: &rate})
	if err != nil || a2.Continue || a2.Classification != "answered" {
		t.Fatalf("attempt 2: %v %+v", err, a2)
	}
	// hangup after 61 s talk: 60/60 -> 120 s billed at 0.02 = 0.04; cost 120 s at 0.015 = 0.03
	answer := now.Add(2 * time.Second)
	end := answer.Add(61 * time.Second)
	cid := resp.Carriers[1].CarrierID
	h := billing.HangupInfo{CallUUID: callID, StartTime: now, AnswerTime: &answer, EndTime: end, Billsec: 61, Duration: 63, HangupCause: "NORMAL_CLEARING", SIPCode: 200, AnsweredCarrierID: &cid, Source: "test"}
	out, err := e.bill.Bill(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if out.BilledSeconds != 120 || !out.Price.Equal(decimal.RequireFromString("0.04")) || !out.Cost.Equal(decimal.RequireFromString("0.03")) {
		t.Fatalf("billing: %+v", out)
	}
	acc, _ = e.st.AccountByOwner(ctx, "customer", *resp.CustomerID)
	if !acc.Reserved.IsZero() || !acc.Balance.Equal(decimal.RequireFromString("9.96")) {
		t.Fatalf("account after: balance %s reserved %s", acc.Balance, acc.Reserved)
	}
	cacc, _ := e.st.AccountByOwner(ctx, "carrier", cid)
	if !cacc.Balance.Equal(decimal.RequireFromString("-0.03")) {
		t.Fatalf("carrier account: %s", cacc.Balance)
	}
	// second delivery of the same hangup (json_cdr after ESL) is a no-op
	out2, err := e.bill.Bill(ctx, h)
	if err != nil || !out2.AlreadyBilled {
		t.Fatalf("rebill: %v %+v", err, out2)
	}
	acc2, _ := e.st.AccountByOwner(ctx, "customer", *resp.CustomerID)
	if !acc2.Balance.Equal(acc.Balance) {
		t.Fatal("double billing")
	}
	cdr, err := e.st.CDRByUUID(ctx, callID)
	if err != nil {
		t.Fatal(err)
	}
	if cdr.Disposition != model.DispositionAnswered || cdr.FailoverDepth != 1 || cdr.CarrierID == nil || *cdr.CarrierID != cid || !cdr.Margin.Equal(decimal.RequireFromString("0.01")) {
		t.Fatalf("cdr: %+v", cdr)
	}
	rows, err := e.st.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !r.OK() {
			t.Errorf("reconcile: %s", r)
		}
	}
	// rejections write CDRs too
	r2, err := e.pipe.Setup(ctx, callcontrol.SetupRequest{CallUUID: uuid.New().String(), SrcIP: "172.28.0.102", SrcPort: 5060, Caller: "1", Called: "442071234567"})
	if err != nil || r2.Action != "reject" || r2.Reject.Code != 402 || r2.Reject.Reason != "Not enough funds" {
		t.Fatalf("beta reject: %v %+v", err, r2)
	}
	r3, err := e.pipe.Setup(ctx, callcontrol.SetupRequest{CallUUID: uuid.New().String(), SrcIP: "1.2.3.4", SrcPort: 5060, Caller: "1", Called: "442071234567"})
	if err != nil || r3.Reject.Code != 403 || r3.Reject.Reason != "IP not authorized" {
		t.Fatalf("unknown reject: %v %+v", err, r3)
	}
	var n int
	if err := e.st.Pool().QueryRow(ctx, `SELECT count(*) FROM cdrs WHERE disposition IN ('rejected_balance','rejected_auth') AND billed_at IS NOT NULL`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rejected cdrs = %d (%v)", n, err)
	}
}

// Orphaned reservations are released by the reconciler.
func TestIntegrationReconcileOrphan(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.cfg.Billing.MaxCallDuration = time.Second
	e.cfg.Billing.OrphanTimeout = time.Second
	callID := uuid.New()
	resp, err := e.pipe.Setup(ctx, callcontrol.SetupRequest{CallUUID: callID.String(), SrcIP: "172.28.0.101", SrcPort: 5060, Caller: "1", Called: "442071234567"})
	if err != nil || resp.Action != "dial" {
		t.Fatalf("setup: %v %+v", err, resp)
	}
	time.Sleep(2500 * time.Millisecond)
	rec := billing.NewReconciler(e.bill, nil)
	n, err := rec.Once(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reconcile once: %d %v", n, err)
	}
	acc, _ := e.st.AccountByOwner(ctx, "customer", *resp.CustomerID)
	if !acc.Reserved.IsZero() || !acc.Balance.Equal(decimal.RequireFromString("10")) {
		t.Fatalf("after orphan release: %+v", acc)
	}
	cdr, _ := e.st.CDRByUUID(ctx, callID)
	if cdr.Disposition != model.DispositionFailed || cdr.BilledAt == nil {
		t.Fatalf("orphan cdr: %+v", cdr)
	}
	if cdr.RejectReason == nil || *cdr.RejectReason != "orphaned reservation" {
		t.Fatalf("orphan reason: %v", cdr.RejectReason)
	}
}

// The circuit breaker degrades a carrier after consecutive faults and the
// route then tries it last.
func TestIntegrationBreakerReorders(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rdb, _ := cache.Connect(ctx, os.Getenv("SBC_TEST_REDIS_URL"))
	defer func() { _ = rdb.Close() }()
	br := callcontrol.NewBreaker(rdb, 3, 10, 50, 60)
	e.pipe.SetBreaker(br)
	e.pipe.SetHealth(breakerHealth{br})
	c503, _ := e.st.CarrierByName(ctx, "carrier-503")
	cust, _ := e.st.CustomerByName(ctx, "acme")
	first := func() string {
		r, err := e.pipe.Route(ctx, cust, "447700900123", "1", nil, uuid.New().String())
		if err != nil || len(r.Carriers) != 2 {
			t.Fatalf("route: %v %+v", err, r)
		}
		return r.Carriers[0].Name
	}
	if first() != "carrier-503" {
		t.Fatal("carrier-503 should be primary before the breaker trips")
	}
	for i := 0; i < 3; i++ {
		br.Record(ctx, c503.ID, failover.CarrierFault)
	}
	if !br.Degraded(ctx, c503.ID) || br.Reason(ctx, c503.ID) != "consecutive_faults" {
		t.Fatalf("breaker did not trip: %v", br.Reason(ctx, c503.ID))
	}
	if first() != "carrier-answer" {
		t.Fatal("degraded carrier should be tried last")
	}
	att, ans, consec := br.Stats(ctx, c503.ID)
	if att != 3 || ans != 0 || consec != 0 {
		t.Fatalf("stats: att=%d ans=%d consec=%d", att, ans, consec)
	}
	// an answered call resets the consecutive counter (the degraded TTL still runs)
	br.Record(ctx, c503.ID, failover.Answered)
	_, ans, _ = br.Stats(ctx, c503.ID)
	if ans != 1 {
		t.Fatalf("answered not counted")
	}
}

type breakerHealth struct{ b *callcontrol.Breaker }

func (breakerHealth) GatewayDown(string) bool { return false }
func (h breakerHealth) Degraded(id uuid.UUID) bool {
	return h.b.Degraded(context.Background(), id)
}

// Carrier capacity: BeginAttempt refuses the second slot on a one-channel carrier.
func TestIntegrationCarrierCapacity(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cap1, _ := e.st.CarrierByName(ctx, "carrier-cap1")
	ok, _, err := e.pipe.BeginAttempt(ctx, cap1.ID)
	if err != nil || !ok {
		t.Fatalf("first slot: %v %v", ok, err)
	}
	ok, reason, _ := e.pipe.BeginAttempt(ctx, cap1.ID)
	if ok || reason != "carrier_capacity" {
		t.Fatalf("second slot should be refused: %v %s", ok, reason)
	}
	cust, _ := e.st.CustomerByName(ctx, "acme")
	r, err := e.pipe.Route(ctx, cust, "442271234567", "1", nil, uuid.New().String())
	if err != nil || len(r.Carriers) != 1 || r.Carriers[0].Name != "carrier-answer" || len(r.Skipped) != 1 || r.Skipped[0].Reason != "carrier_capacity" {
		t.Fatalf("route should skip the full carrier: %+v", r)
	}
}

// Soft deletes must satisfy the name check constraints (a "~" suffix once broke carrier deletion).
func TestIntegrationSoftDeleteNames(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, _ := e.st.CarrierByName(ctx, "carrier-404")
	if err := e.st.DeleteCarrier(ctx, c.ID); err != nil {
		t.Fatalf("delete carrier: %v", err)
	}
	if _, err := e.st.CarrierByName(ctx, "carrier-404"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("carrier still visible: %v", err)
	}
	cu, _ := e.st.CustomerByName(ctx, "beta")
	if err := e.st.DeleteCustomer(ctx, cu.ID); err != nil {
		t.Fatalf("delete customer: %v", err)
	}
}

// Multi node (D-68): the node scoped sweep only considers its own
// reservations; a stale reservation made by another node is left alone.
func TestIntegrationStaleActiveCallsNodeScoped(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.cfg.NodeName = "node-a"
	callID := uuid.New()
	resp, err := e.pipe.Setup(ctx, callcontrol.SetupRequest{CallUUID: callID.String(), SrcIP: "172.28.0.101", SrcPort: 5060, Caller: "1", Called: "442071234567"})
	if err != nil || resp.Action != "dial" {
		t.Fatalf("setup: %v %+v", err, resp)
	}
	if _, err := e.st.Pool().Exec(ctx, `UPDATE active_calls SET started_at = now() - interval '1 hour' WHERE call_uuid = $1`, callID); err != nil {
		t.Fatal(err)
	}
	mine, err := e.st.StaleActiveCalls(ctx, "node-a", time.Minute, 10)
	if err != nil || len(mine) != 1 || mine[0].Node != "node-a" {
		t.Fatalf("own node: %v %+v", err, mine)
	}
	other, err := e.st.StaleActiveCalls(ctx, "node-b", time.Minute, 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("other node must not see it: %v %+v", err, other)
	}
	// reservations from before the upgrade (no node) are swept by any node
	if _, err := e.st.Pool().Exec(ctx, `UPDATE active_calls SET node = '' WHERE call_uuid = $1`, callID); err != nil {
		t.Fatal(err)
	}
	legacy, err := e.st.StaleActiveCalls(ctx, "node-b", time.Minute, 10)
	if err != nil || len(legacy) != 1 {
		t.Fatalf("legacy rows: %v %+v", err, legacy)
	}
}
