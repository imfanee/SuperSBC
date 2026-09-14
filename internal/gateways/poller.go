// Package gateways tracks the Sofia gateway state of every carrier by
// polling FreeSWITCH (OPTIONS ping results) and implements the carrier
// health source used by routing: DOWN gateways are skipped (Section 6).
package gateways

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/esl"
)

// State of one gateway.
type State struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"` // UP, DOWN, UNKNOWN
	Since     time.Time `json:"since"`
	CheckedAt time.Time `json:"checked_at"`
}

// Degrader reports circuit-breaker state per carrier (installed in M3).
type Degrader interface {
	Degraded(ctx context.Context, carrierID uuid.UUID) bool
}

// Poller polls "sofia profile external-egress gwlist up|down".
type Poller struct {
	esl      *esl.Supervisor
	log      *slog.Logger
	profile  string
	interval time.Duration

	mu     sync.RWMutex
	states map[string]State
	deg    Degrader
}

// New creates a poller for the given egress profile.
func New(sup *esl.Supervisor, profile string, interval time.Duration, log *slog.Logger) *Poller {
	return &Poller{esl: sup, log: log, profile: profile, interval: interval, states: map[string]State{}}
}

// SetDegrader installs the circuit breaker source.
func (p *Poller) SetDegrader(d Degrader) { p.deg = d }

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	p.Once(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Once(ctx)
		}
	}
}

// Once performs one poll.
func (p *Poller) Once(ctx context.Context) {
	if p.esl == nil || !p.esl.Connected() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	up, err := p.esl.API(ctx, "sofia profile "+p.profile+" gwlist up")
	if err != nil {
		p.log.Warn("gwlist up failed", "error", err)
		return
	}
	down, err := p.esl.API(ctx, "sofia profile "+p.profile+" gwlist down")
	if err != nil {
		p.log.Warn("gwlist down failed", "error", err)
		return
	}
	now := time.Now()
	seen := map[string]string{}
	for _, n := range strings.Fields(up) {
		seen[n] = "UP"
	}
	for _, n := range strings.Fields(down) {
		seen[n] = "DOWN"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, status := range seen {
		prev, ok := p.states[name]
		st := State{Name: name, Status: status, Since: prev.Since, CheckedAt: now}
		if !ok || prev.Status != status {
			st.Since = now
			if ok {
				p.log.Warn("gateway state changed", "gateway", name, "from", prev.Status, "to", status)
			} else {
				p.log.Info("gateway state", "gateway", name, "status", status)
			}
		}
		p.states[name] = st
	}
	for name := range p.states {
		if _, ok := seen[name]; !ok {
			delete(p.states, name)
		}
	}
}

// GatewayDown implements callcontrol.CarrierHealth.
func (p *Poller) GatewayDown(gateway string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.states[gateway].Status == "DOWN"
}

// Degraded implements callcontrol.CarrierHealth.
func (p *Poller) Degraded(carrierID uuid.UUID) bool {
	if p.deg == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return p.deg.Degraded(ctx, carrierID)
}

// States returns a snapshot of all gateway states.
func (p *Poller) States() map[string]State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]State, len(p.states))
	for k, v := range p.states {
		out[k] = v
	}
	return out
}

// State returns one gateway's state (UNKNOWN when never seen).
func (p *Poller) State(gateway string) State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if s, ok := p.states[gateway]; ok {
		return s
	}
	return State{Name: gateway, Status: "UNKNOWN"}
}
