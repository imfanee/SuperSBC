// Package health serves /healthz (process alive) and /readyz (dependencies).
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/opensbc/opensbc/internal/esl"
)

// Deps are the dependencies /readyz verifies.
type Deps struct {
	DB       *pgxpool.Pool
	Redis    *redis.Client
	ESL      *esl.Supervisor
	Profiles []string // sofia profiles that must be RUNNING
	Version  string
	Node     string
}

// Status is the JSON body of /readyz.
type Status struct {
	Ready   bool              `json:"ready"`
	Version string            `json:"version"`
	Node    string            `json:"node"`
	Checks  map[string]string `json:"checks"`
}

// Healthz always returns 200 while the process is running.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// Readyz verifies Postgres, Redis, the ESL connection and the Sofia profiles.
func (d Deps) Readyz(w http.ResponseWriter, r *http.Request) {
	st := d.Check(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if !st.Ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(st)
}

// Check runs the readiness checks.
func (d Deps) Check(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	st := Status{Ready: true, Version: d.Version, Node: d.Node, Checks: map[string]string{}}
	fail := func(name string, err error) {
		st.Ready = false
		st.Checks[name] = "fail: " + err.Error()
	}
	if d.DB != nil {
		if err := d.DB.Ping(ctx); err != nil {
			fail("postgres", err)
		} else {
			st.Checks["postgres"] = "ok"
		}
	}
	if d.Redis != nil {
		if err := d.Redis.Ping(ctx).Err(); err != nil {
			fail("redis", err)
		} else {
			st.Checks["redis"] = "ok"
		}
	}
	if d.ESL != nil {
		if !d.ESL.Connected() {
			st.Ready = false
			st.Checks["esl"] = "fail: not connected"
		} else {
			st.Checks["esl"] = "ok"
			out, err := d.ESL.API(ctx, "sofia status")
			if err != nil {
				fail("sofia", err)
			} else {
				for _, p := range d.Profiles {
					if profileRunning(out, p) {
						st.Checks["sofia."+p] = "ok"
					} else {
						st.Ready = false
						st.Checks["sofia."+p] = "fail: not running"
					}
				}
			}
		}
	}
	return st
}

// profileRunning parses the "sofia status" table for a RUNNING profile row.
func profileRunning(out, profile string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == profile && f[1] == "profile" && strings.Contains(line, "RUNNING") {
			return true
		}
	}
	return false
}
