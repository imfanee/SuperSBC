package watchdog

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func runtimeNumCPU() int { return runtime.NumCPU() }

// client ignores the self signed certificate the live UI serves: the watchdog
// talks to its own node over the loopback interface.
var client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
	TLSClientConfig:       &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // loopback only
	ResponseHeaderTimeout: 10 * time.Second,
}}

func get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b, resp.StatusCode, err
}

type readyBody struct {
	Ready   bool              `json:"ready"`
	Version string            `json:"version"`
	Node    string            `json:"node"`
	Checks  map[string]string `json:"checks"`
}

// readyChecks turns /readyz into one check per dependency, so an alert names
// the broken one instead of saying the API is unwell.
func readyChecks(ctx context.Context, url string, r *Report) []Check {
	if url == "" {
		return nil
	}
	body, code, err := get(ctx, url)
	if err != nil {
		return []Check{{Group: "Services", Name: "admin API", Level: LevelFail, Status: "unreachable",
			Detail: "cannot reach " + url + ": " + err.Error()}}
	}
	var rb readyBody
	if err := json.Unmarshal(body, &rb); err != nil {
		return []Check{{Group: "Services", Name: "admin API", Level: LevelFail, Status: "bad response",
			Detail: fmt.Sprintf("%s answered HTTP %d with an unreadable body", url, code)}}
	}
	if rb.Version != "" {
		r.Version = rb.Version
	}
	out := []Check{{Group: "Services", Name: "admin API", Status: fmt.Sprintf("ready, version %s", rb.Version)}}
	if !rb.Ready || code != http.StatusOK {
		out[0].Level, out[0].Status = LevelFail, "not ready"
		out[0].Detail = fmt.Sprintf("%s answered HTTP %d", url, code)
	}
	names := make([]string, 0, len(rb.Checks))
	for k := range rb.Checks {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		c := Check{Group: "Services", Name: name, Status: rb.Checks[name]}
		if c.Status != "ok" {
			c.Level = LevelFail
			c.Detail = strings.TrimPrefix(c.Status, "fail: ")
			c.Status = "failing"
		}
		out = append(out, c)
	}
	return out
}

// metricChecks reads the gateway gauges, the roll-up freshness and the call
// count from the Prometheus endpoint, which needs no credentials.
func metricChecks(ctx context.Context, url string, r *Report) []Check {
	if url == "" {
		return nil
	}
	body, code, err := get(ctx, url)
	if err != nil || code != http.StatusOK {
		return []Check{{Group: "Carriers", Name: "metrics", Level: LevelWarn, Status: "unreachable",
			Detail: fmt.Sprintf("cannot read %s (HTTP %d)", url, code)}}
	}
	var down, up []string
	var rollup float64
	for _, line := range strings.Split(string(body), "\n") {
		switch {
		case strings.HasPrefix(line, "sbc_gateway_up{"):
			name, value, ok := sample(line)
			if !ok {
				continue
			}
			if value >= 1 {
				up = append(up, name)
			} else {
				down = append(down, name)
			}
		case strings.HasPrefix(line, "sbc_reports_rollup_last_success_timestamp_seconds "):
			rollup, _ = strconv.ParseFloat(strings.Fields(line)[1], 64)
		case strings.HasPrefix(line, "sbc_calls_in_progress "):
			v, _ := strconv.ParseFloat(strings.Fields(line)[1], 64)
			r.Traffic.LiveCalls = int64(v)
		}
	}
	sort.Strings(down)
	out := []Check{{Group: "Carriers", Name: "carrier gateways",
		Status: fmt.Sprintf("%d of %d up", len(up), len(up)+len(down))}}
	if len(down) > 0 {
		out[0].Level = LevelFail
		if len(up) > 0 {
			out[0].Level = LevelWarn
		}
		out[0].Detail = "down: " + strings.Join(down, ", ")
	}
	if len(up)+len(down) == 0 {
		out[0].Level, out[0].Status = LevelWarn, "no gateways"
		out[0].Detail = "FreeSWITCH reports no gateways at all; check the carrier configuration"
	}
	c := Check{Group: "Services", Name: "reports roll-up"}
	switch rollup {
	case 0:
		c.Level, c.Status = LevelWarn, "never ran"
		c.Detail = "the hourly roll-up has not completed since the API started"
	default:
		age := time.Since(time.Unix(int64(rollup), 0))
		c.Status = "last ran " + ago(age)
		if age > 15*time.Minute {
			c.Level = LevelFail
			c.Detail = "the roll-up has not completed for " + ago(age) + "; the dashboard chart is frozen (docker logs supersbc-live-api-1 | grep rollup)"
		}
	}
	out = append(out, c)
	return out
}

// sample pulls the label value and the number out of a Prometheus line.
func sample(line string) (string, float64, bool) {
	i, j := strings.Index(line, `="`), strings.Index(line, `"}`)
	if i < 0 || j < i {
		return "", 0, false
	}
	name := line[i+2 : j]
	fields := strings.Fields(line)
	v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	return name, v, err == nil
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours())/24)
}

// firewallChecks verify the ruleset is loaded and the sets are not empty,
// which is what an unordered boot left behind once before (D-69).
func firewallChecks(ctx context.Context, opts Options) []Check {
	if opts.NFTable == "" {
		return nil
	}
	out, err := run(ctx, "nft", "-j", "list", "table", "inet", opts.NFTable)
	if err != nil {
		return []Check{{Group: "Security", Name: "firewall", Level: LevelFail, Status: "not loaded",
			Detail: fmt.Sprintf("nftables has no table inet %s; load it with make live-firewall", opts.NFTable)}}
	}
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return []Check{{Group: "Security", Name: "firewall", Level: LevelWarn, Status: "unreadable",
			Detail: "cannot parse the nftables output"}}
	}
	counts := map[string]int{}
	for _, node := range doc.Nftables {
		raw, ok := node["set"]
		if !ok {
			continue
		}
		var s struct {
			Name string            `json:"name"`
			Elem []json.RawMessage `json:"elem"`
		}
		if err := json.Unmarshal(raw, &s); err == nil {
			counts[s.Name] = len(s.Elem)
		}
	}
	checks := []Check{{Group: "Security", Name: "firewall", Status: "loaded"}}
	for _, set := range opts.NFSets {
		c := Check{Group: "Security", Name: "firewall set " + set}
		n, ok := counts[set]
		switch {
		case !ok:
			c.Level, c.Status = LevelFail, "missing"
			c.Detail = "the set does not exist; the ruleset loaded without it"
		case n == 0:
			c.Level, c.Status = LevelWarn, "empty"
			c.Detail = "no addresses are allowed in; sbc-fwsync may not have run since boot"
		default:
			c.Status = fmt.Sprintf("%d entries", n)
		}
		checks = append(checks, c)
	}
	return checks
}

// tlsChecks warn before a certificate expires, which is silent until a call
// or a browser fails.
func tlsChecks(opts Options) []Check {
	var out []Check
	for _, path := range opts.TLSFiles {
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		c := Check{Group: "Security", Name: "certificate " + name}
		b, err := os.ReadFile(path)
		if err != nil {
			c.Level, c.Status, c.Detail = LevelWarn, "unreadable", err.Error()
			out = append(out, c)
			continue
		}
		var cert *x509.Certificate
		for rest := b; len(rest) > 0; {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if block.Type == "CERTIFICATE" {
				if parsed, err := x509.ParseCertificate(block.Bytes); err == nil {
					cert = parsed
					break
				}
			}
		}
		if cert == nil {
			c.Level, c.Status, c.Detail = LevelWarn, "no certificate", "the file holds no PEM certificate"
			out = append(out, c)
			continue
		}
		days := int(time.Until(cert.NotAfter).Hours() / 24)
		c.Status = fmt.Sprintf("valid for %d days", days)
		switch {
		case days < 0:
			c.Level, c.Status = LevelFail, "expired"
			c.Detail = "expired on " + cert.NotAfter.Format("2 Jan 2006")
		case opts.TLSWarnDays > 0 && days <= opts.TLSWarnDays:
			c.Level = LevelWarn
			c.Detail = fmt.Sprintf("expires on %s; renew it", cert.NotAfter.Format("2 Jan 2006"))
		}
		out = append(out, c)
	}
	return out
}
