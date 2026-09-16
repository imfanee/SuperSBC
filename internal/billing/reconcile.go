package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/imfanee/supersbc/internal/esl"
	"github.com/imfanee/supersbc/internal/model"
)

// Reconciler releases reservations of calls whose hangup was never billed
// (ARCHITECTURE section 7). It runs every minute.
type Reconciler struct {
	eng *Engine
	esl *esl.Supervisor
}

// NewReconciler creates the worker.
func NewReconciler(eng *Engine, sup *esl.Supervisor) *Reconciler {
	return &Reconciler{eng: eng, esl: sup}
}

// Run blocks until ctx is done.
func (r *Reconciler) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := r.Once(ctx); err != nil {
				r.eng.log.Warn("reconcile pass failed", "error", err)
			} else if n > 0 {
				r.eng.log.Info("reconcile released orphaned reservations", "count", n)
			}
			r.syncCounters(ctx)
		}
	}
}

// Once performs one pass. With FreeSWITCH reachable, every reservation older
// than orphan_timeout whose channel no longer exists is released (the
// channel list is authoritative, so an API restart that lost hangup events
// is repaired within minutes). Without FreeSWITCH only reservations past
// expiry + orphan_timeout are released, as the specification requires.
func (r *Reconciler) Once(ctx context.Context) (int, error) {
	connected := r.esl != nil && r.esl.Connected()
	var expired []model.ActiveCall
	var err error
	if connected {
		expired, err = r.eng.st.StaleActiveCalls(ctx, r.eng.cfg.NodeName, r.eng.cfg.Billing.OrphanTimeout, 500)
	} else {
		expired, err = r.eng.st.ExpiredActiveCalls(ctx, r.eng.cfg.Billing.OrphanTimeout, 200)
	}
	if err != nil {
		return 0, err
	}
	if len(expired) == 0 {
		return 0, nil
	}
	live := map[uuid.UUID]bool{}
	if connected {
		out, err := r.esl.API(ctx, "show channels as delim |")
		if err != nil {
			return 0, fmt.Errorf("show channels: %w", err)
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.SplitN(line, "|", 2)
			if id, err := uuid.Parse(strings.TrimSpace(f[0])); err == nil {
				live[id] = true
			}
		}
	} else {
		// Without ESL we cannot tell; FreeSWITCH itself enforces sched_hangup at
		// max_call_duration, so past expiry + orphan_timeout the channel is gone.
		r.eng.log.Warn("reconcile without esl: assuming expired channels are gone")
	}
	n := 0
	for _, ac := range expired {
		if live[ac.CallUUID] {
			continue
		}
		h := HangupInfo{CallUUID: ac.CallUUID, StartTime: ac.StartedAt, EndTime: r.eng.now(), HangupCause: "ORPHANED", SIPCode: 0, Source: "reconcile", RejectReason: "orphaned reservation"}
		if _, err := r.eng.Bill(ctx, h); err != nil {
			r.eng.log.Warn("reconcile bill failed", "call_uuid", ac.CallUUID, "error", err)
			continue
		}
		n++
	}
	return n, nil
}

// syncCounters makes the Redis concurrent-call counters equal the number of
// open reservations (D-31), correcting drift from crashes.
func (r *Reconciler) syncCounters(ctx context.Context) {
	open, err := r.eng.st.CountActiveCallsByCustomer(ctx)
	if err != nil {
		return
	}
	current, err := r.eng.adm.AllConcurrent(ctx, "customer")
	if err != nil {
		return
	}
	for id, n := range current {
		if open[id] != int(n) {
			_ = r.eng.adm.SetConcurrent(ctx, "customer", id, open[id])
		}
	}
	for id, n := range open {
		if _, ok := current[id]; !ok && n > 0 {
			_ = r.eng.adm.SetConcurrent(ctx, "customer", id, n)
		}
	}
}
