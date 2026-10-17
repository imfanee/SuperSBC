package reports

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/imfanee/supersbc/internal/metrics"
)

// Rollup maintains cdr_hourly_stats.
type Rollup struct {
	svc *Service
	log *slog.Logger

	mu          sync.Mutex
	lastSuccess time.Time
	lastErr     string
}

// Health reports the last successful pass and the last error, so a roll-up
// that stops writing shows up on the status page instead of only in a log
// line nobody reads (D-78).
func (r *Rollup) Health() (lastSuccess time.Time, lastErr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSuccess, r.lastErr
}

func (r *Rollup) record(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.lastErr = err.Error()
		return
	}
	r.lastErr = ""
	r.lastSuccess = time.Now()
	metrics.RollupLastSuccess.SetToCurrentTime()
}

// NewRollup creates the worker.
func NewRollup(svc *Service, log *slog.Logger) *Rollup { return &Rollup{svc: svc, log: log} }

// Run recomputes every minute until ctx ends.
func (r *Rollup) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	r.run(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.run(ctx)
		}
	}
}

// run makes one pass and records its outcome.
func (r *Rollup) run(ctx context.Context) {
	err := r.Once(ctx)
	r.record(err)
	if err != nil {
		r.log.Error("rollup", "error", err)
	}
}

// Once recomputes the hours that need it: the current and previous hour
// always, plus any hour containing a CDR updated since its roll-up.
//
// The hours are resolved first and the delete and the insert then run in
// order inside one transaction. Expressing this as a single statement with a
// data modifying CTE does not work: Postgres gives no ordering between a
// DELETE in a WITH clause and the INSERT that follows it, so the insert
// collided with the rows the delete was removing and every run failed on the
// unique key, silently freezing the table the dashboard reads.
func (r *Rollup) Once(ctx context.Context) error {
	hours, err := r.staleHours(ctx)
	if err != nil {
		return err
	}
	if len(hours) == 0 {
		return nil
	}
	tx, err := r.svc.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM cdr_hourly_stats WHERE hour = ANY($1)`, hours); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cdr_hourly_stats (hour, customer_id, carrier_id, disposition, calls, billsec, sell_price, cost, pdd_ms_sum, pdd_count, short_calls, computed_at)
		SELECT date_trunc('hour', d.start_time), d.customer_id, d.carrier_id, d.disposition, count(*), COALESCE(sum(d.billsec), 0), COALESCE(sum(d.sell_price), 0), COALESCE(sum(d.cost), 0),
		       COALESCE(sum(d.pdd_ms), 0), count(d.pdd_ms), count(*) FILTER (WHERE d.disposition = 'answered' AND d.billsec < 6), now()
		FROM cdrs d WHERE d.disposition <> 'pending' AND date_trunc('hour', d.start_time) = ANY($1)
		GROUP BY 1, 2, 3, 4`, hours); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// staleHours lists the hours whose roll-up is missing or older than a CDR in
// it, always including the current and the previous hour.
func (r *Rollup) staleHours(ctx context.Context) ([]time.Time, error) {
	rows, err := r.svc.pool.Query(ctx, `
		SELECT DISTINCT date_trunc('hour', d.start_time) AS hour
		FROM cdrs d LEFT JOIN cdr_hourly_stats s ON s.hour = date_trunc('hour', d.start_time)
		WHERE d.disposition <> 'pending' AND (s.hour IS NULL OR d.updated_at > s.computed_at)
		UNION SELECT date_trunc('hour', now()) UNION SELECT date_trunc('hour', now() - interval '1 hour')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var h time.Time
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HourlyFromRollup serves the dashboard hourly series from the roll-up table.
func (s *Service) HourlyFromRollup(ctx context.Context, r Range) ([]Row, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(hour, 'YYYY-MM-DD"T"HH24:00:00Z') AS key, to_char(hour, 'YYYY-MM-DD HH24:00') AS label,
		  COALESCE(sum(calls), 0) AS attempts, COALESCE(sum(calls) FILTER (WHERE disposition = 'answered'), 0) AS answered,
		  COALESCE(sum(calls) FILTER (WHERE disposition IN ('rejected_auth','rejected_balance','rejected_route')), 0) AS rejected,
		  COALESCE(sum(calls) FILTER (WHERE disposition = 'failed'), 0) AS failed,
		  COALESCE(sum(calls) FILTER (WHERE disposition = 'answered')::float / NULLIF(sum(calls) FILTER (WHERE disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 0) AS asr,
		  COALESCE(sum(calls) FILTER (WHERE disposition IN ('answered','busy','no_answer','cancelled'))::float / NULLIF(sum(calls) FILTER (WHERE disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 0) AS ner,
		  COALESCE(sum(billsec)::float / NULLIF(sum(calls) FILTER (WHERE disposition = 'answered'), 0), 0) AS acd,
		  COALESCE(sum(billsec), 0) AS billsec, COALESCE(sum(billsec), 0)::float / 60 AS minutes,
		  COALESCE(sum(sell_price), 0)::text AS revenue, COALESCE(sum(cost), 0)::text AS cost, COALESCE(sum(sell_price - cost), 0)::text AS margin,
		  COALESCE(sum(sell_price - cost)::float / NULLIF(sum(sell_price)::float, 0), 0) AS margin_pct,
		  COALESCE(sum(pdd_ms_sum)::float / NULLIF(sum(pdd_count), 0), 0) AS pdd_avg_ms, 0::float AS pdd_p95_ms, COALESCE(sum(short_calls), 0) AS short_calls
		FROM cdr_hourly_stats WHERE hour >= date_trunc('hour', $1::timestamptz) AND hour < $2
		  AND ($3::uuid IS NULL OR customer_id = $3) AND ($4::uuid IS NULL OR carrier_id = $4)
		GROUP BY hour ORDER BY hour`, r.From, r.To, r.CustomerID, r.CarrierID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows)
}
