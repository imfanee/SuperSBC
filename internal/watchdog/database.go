package watchdog

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// psql runs one query inside a database container and returns the raw row.
// The watchdog is a host process, so it reaches both databases the same way
// an operator does, without needing a published port or a password on the
// command line.
func psql(ctx context.Context, container, user, db, query string) (string, error) {
	out, err := run(ctx, "docker", "exec", "-i", container, "psql", "-U", user, "-d", db, "-Atc", query)
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, firstLine(out))
	}
	return strings.TrimSpace(out), nil
}

// captureChecks confirm SIP capture is still being written. heplify can sit
// there receiving packets and storing none (D-78), which only shows up as an
// empty trace days later, so compare the newest captured message with the
// calls the SBC actually handled.
func captureChecks(ctx context.Context, opts Options) []Check {
	if opts.HEPContainer == "" {
		return nil
	}
	c := Check{Group: "Capture", Name: "SIP capture"}
	row, err := psql(ctx, opts.HEPContainer, opts.HEPUser, opts.HEPDatabase,
		`SELECT COALESCE(EXTRACT(EPOCH FROM now() - max(create_date))::bigint, -1) FROM hep_proto_1_call WHERE create_date > now() - interval '24 hours'`)
	if err != nil {
		c.Level, c.Status, c.Detail = LevelWarn, "unreadable", "cannot query the capture database: "+err.Error()
		return []Check{c}
	}
	age, _ := strconv.ParseInt(row, 10, 64)
	calls := recentCalls(ctx, opts)
	switch {
	case age < 0 && calls > 0:
		c.Level, c.Status = LevelFail, "nothing captured"
		c.Detail = fmt.Sprintf("%d calls in the last hour and no SIP captured in 24 hours; restart heplify", calls)
	case age < 0:
		c.Status = "idle, no calls"
	case calls > 0 && age > 3600:
		c.Level, c.Status = LevelFail, "stale"
		c.Detail = fmt.Sprintf("%d calls in the last hour but the newest captured message is %s old; restart heplify", calls, ago(time.Duration(age)*time.Second))
	default:
		c.Status = "last message " + ago(time.Duration(age)*time.Second)
	}
	return []Check{c}
}

// recentCalls counts the calls of the last hour, which says whether a quiet
// capture table means trouble or simply no traffic.
func recentCalls(ctx context.Context, opts Options) int64 {
	if opts.PGContainer == "" {
		return 0
	}
	row, err := psql(ctx, opts.PGContainer, opts.PGUser, opts.PGDatabase,
		`SELECT count(*) FROM cdrs WHERE start_time > now() - interval '1 hour'`)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(row, 10, 64)
	return n
}

// traffic summarises the last 24 hours for the all clear report, so the
// e-mail says what the SBC did rather than only that it is alive.
func traffic(ctx context.Context, opts Options) Traffic {
	t := Traffic{}
	if opts.PGContainer == "" {
		return t
	}
	row, err := psql(ctx, opts.PGContainer, opts.PGUser, opts.PGDatabase, `
		SELECT count(*)
		  || '|' || count(*) FILTER (WHERE disposition = 'answered')
		  || '|' || round(COALESCE(sum(billsec), 0) / 60.0, 1)
		  || '|' || COALESCE(round(100.0 * count(*) FILTER (WHERE disposition = 'answered')
		       / NULLIF(count(*) FILTER (WHERE disposition NOT IN ('rejected_auth','rejected_balance','rejected_route')), 0), 1), 0)
		  || '|' || COALESCE(sum(sell_price), 0)::numeric(18,2)
		  || '|' || COALESCE(sum(sell_price - cost), 0)::numeric(18,2)
		FROM cdrs WHERE start_time > now() - interval '24 hours'`)
	if err != nil {
		return t
	}
	f := strings.Split(row, "|")
	if len(f) != 6 {
		return t
	}
	t.Known = true
	t.Attempts, _ = strconv.ParseInt(f[0], 10, 64)
	t.Answered, _ = strconv.ParseInt(f[1], 10, 64)
	t.Minutes, t.ASR, t.Revenue, t.Margin = f[2], f[3], f[4], f[5]
	t.Currency = opts.Currency
	return t
}
