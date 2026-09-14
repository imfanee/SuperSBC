package reports

import (
	"context"
	"log/slog"
	"time"
)

// Rollup maintains cdr_hourly_stats.
type Rollup struct {
	svc *Service
	log *slog.Logger
}

// NewRollup creates the worker.
func NewRollup(svc *Service, log *slog.Logger) *Rollup { return &Rollup{svc: svc, log: log} }

// Run recomputes every minute until ctx ends.
func (r *Rollup) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	if err := r.Once(ctx); err != nil {
		r.log.Warn("rollup", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Once(ctx); err != nil {
				r.log.Warn("rollup", "error", err)
			}
		}
	}
}

// Once recomputes the hours that need it: the current and previous hour
// always, plus any hour containing a CDR updated since its roll-up.
func (r *Rollup) Once(ctx context.Context) error {
	_, err := r.svc.pool.Exec(ctx, `
		WITH stale AS (
			SELECT DISTINCT date_trunc('hour', d.start_time) AS hour
			FROM cdrs d LEFT JOIN cdr_hourly_stats s ON s.hour = date_trunc('hour', d.start_time)
			WHERE d.disposition <> 'pending' AND (s.hour IS NULL OR d.updated_at > s.computed_at)
			UNION SELECT date_trunc('hour', now()) UNION SELECT date_trunc('hour', now() - interval '1 hour')
		),
		del AS (DELETE FROM cdr_hourly_stats WHERE hour IN (SELECT hour FROM stale))
		INSERT INTO cdr_hourly_stats (hour, customer_id, carrier_id, disposition, calls, billsec, sell_price, cost, pdd_ms_sum, pdd_count, short_calls, computed_at)
		SELECT date_trunc('hour', d.start_time), d.customer_id, d.carrier_id, d.disposition, count(*), COALESCE(sum(d.billsec), 0), COALESCE(sum(d.sell_price), 0), COALESCE(sum(d.cost), 0),
		       COALESCE(sum(d.pdd_ms), 0), count(d.pdd_ms), count(*) FILTER (WHERE d.disposition = 'answered' AND d.billsec < 6), now()
		FROM cdrs d WHERE d.disposition <> 'pending' AND date_trunc('hour', d.start_time) IN (SELECT hour FROM stale)
		GROUP BY 1, 2, 3, 4`)
	return err
}

// HourlyFromRollup serves the dashboard hourly series from the roll-up table.
func (s *Service) HourlyFromRollup(ctx context.Context, r Range) ([]Row, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(hour, 'YYYY-MM-DD"T"HH24:00:00Z') AS key, to_char(hour, 'YYYY-MM-DD HH24:00') AS label,
		  sum(calls) AS attempts, sum(calls) FILTER (WHERE disposition = 'answered') AS answered,
		  sum(calls) FILTER (WHERE disposition IN ('rejected_auth','rejected_balance','rejected_route')) AS rejected,
		  sum(calls) FILTER (WHERE disposition = 'failed') AS failed,
		  COALESCE(sum(calls) FILTER (WHERE disposition = 'answered')::float / NULLIF(sum(calls) FILTER (WHERE disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 0) AS asr,
		  COALESCE(sum(calls) FILTER (WHERE disposition IN ('answered','busy','no_answer','cancelled'))::float / NULLIF(sum(calls) FILTER (WHERE disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 0) AS ner,
		  COALESCE(sum(billsec)::float / NULLIF(sum(calls) FILTER (WHERE disposition = 'answered'), 0), 0) AS acd,
		  sum(billsec) AS billsec, sum(billsec)::float / 60 AS minutes, sum(sell_price)::text AS revenue, sum(cost)::text AS cost, sum(sell_price - cost)::text AS margin,
		  COALESCE(sum(sell_price - cost)::float / NULLIF(sum(sell_price)::float, 0), 0) AS margin_pct,
		  COALESCE(sum(pdd_ms_sum)::float / NULLIF(sum(pdd_count), 0), 0) AS pdd_avg_ms, 0::float AS pdd_p95_ms, sum(short_calls) AS short_calls
		FROM cdr_hourly_stats WHERE hour >= date_trunc('hour', $1::timestamptz) AND hour < $2
		  AND ($3::uuid IS NULL OR customer_id = $3) AND ($4::uuid IS NULL OR carrier_id = $4)
		GROUP BY hour ORDER BY hour`, r.From, r.To, r.CustomerID, r.CarrierID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows)
}
