//go:build e2e

// Package e2e drives sipp mock customers against the running compose stack
// and asserts CDR, balance and SIP outcomes (Section 12 M1 scenarios).
//
// Run with: make e2e   (or: go test -tags e2e ./tests/e2e/ with the stack up)
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

const sbcAddr = "172.28.0.10:5060"

var customerIP = map[string]string{
	"customer-acme":    "172.28.0.101",
	"customer-beta":    "172.28.0.102",
	"customer-gamma":   "172.28.0.103",
	"customer-delta":   "172.28.0.104",
	"customer-unknown": "172.28.0.199",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func compose(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = repoRoot(t)
	return cmd
}

// sql runs a query through psql inside the postgres container and decodes
// the JSON array result.
func sql(t *testing.T, query string) []map[string]any {
	t.Helper()
	q := fmt.Sprintf("SELECT COALESCE(json_agg(t), '[]'::json) FROM (%s) t", query)
	cmd := compose(t, "exec", "-T", "postgres", "psql", "-U", "opensbc", "-d", "opensbc", "-tA", "-c", q)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("psql: %v: %s", err, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &rows); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	return rows
}

func str(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%v", x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func dec(t *testing.T, v any) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(str(v))
	if err != nil {
		t.Fatalf("decimal %v: %v", v, err)
	}
	return d
}

func balance(t *testing.T, customer string) (bal, reserved decimal.Decimal) {
	t.Helper()
	rows := sql(t, fmt.Sprintf(`SELECT a.balance::text AS balance, a.reserved::text AS reserved FROM accounts a JOIN customers c ON c.id = a.owner_id AND a.owner_type = 'customer' WHERE c.name = '%s'`, customer))
	if len(rows) != 1 {
		t.Fatalf("no account for %s", customer)
	}
	return dec(t, rows[0]["balance"]), dec(t, rows[0]["reserved"])
}

// scenario renders a template into tests/e2e/out and returns its container path.
func scenario(t *testing.T, tmpl string, repl map[string]string) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "tests", "e2e", "scenarios", tmpl)
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	name := strings.TrimSuffix(tmpl, ".xml.tmpl")
	for k, v := range repl {
		s = strings.ReplaceAll(s, k, v)
		name += "_" + v
	}
	name = regexp.MustCompile(`[^a-zA-Z0-9_]+`).ReplaceAllString(name, "_") + ".xml"
	dst := filepath.Join(repoRoot(t), "tests", "e2e", "out", name)
	if err := os.WriteFile(dst, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return "/out/" + name
}

// callResult is what one sipp run produced.
type callResult struct {
	ExitCode int
	Messages string
	// FinalLines are the final (>= 200) response status lines received, in order.
	FinalLines []string
}

// placeCall runs sipp from a mock customer container.
func placeCall(t *testing.T, customer, scenarioPath, called string, calls int) callResult {
	t.Helper()
	ip, ok := customerIP[customer]
	if !ok {
		t.Fatalf("unknown customer %s", customer)
	}
	logName := fmt.Sprintf("%s_%d.msg", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())
	args := []string{"--profile", "e2e-clients", "run", "--rm", "-T", customer,
		"-sf", scenarioPath, "-i", ip, "-p", "5060", "-mi", ip, "-mp", "7000",
		"-s", called, "-m", fmt.Sprint(calls), "-l", fmt.Sprint(calls), "-r", fmt.Sprint(calls), "-rp", "1000",
		"-nostdin", "-trace_msg", "-message_file", "/out/" + logName, "-trace_err", "-error_file", "/dev/null",
		// sipp's default Call-ID (%u-%p@%s) repeats across fresh containers (pid is always 1);
		// a repeated Call-ID would be matched to the previous dialog by Sofia.
		"-cid_str", fmt.Sprintf("%%u-%%p-%d@%%s", time.Now().UnixNano()),
		"-timeout", "90s", "-timeout_error"}
	args = append(args, sbcAddr)
	cmd := compose(t, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	res := callResult{}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			t.Fatalf("run sipp: %v\n%s", err, out.String())
		}
	}
	b, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests", "e2e", "out", logName))
	res.Messages = string(b)
	re := regexp.MustCompile(`(?m)^SIP/2\.0 ([2-6]\d\d [^\r\n]*)`)
	for _, m := range re.FindAllStringSubmatch(res.Messages, -1) {
		res.FinalLines = append(res.FinalLines, strings.TrimSpace(m[1]))
	}
	t.Logf("sipp %s -> %s exit=%d finals=%v", customer, called, res.ExitCode, res.FinalLines)
	return res
}

// lastCDR waits for the most recent billed CDR of a customer for a number.
func lastCDR(t *testing.T, srcIP, called string, since time.Time) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rows := sql(t, fmt.Sprintf(`SELECT call_uuid, disposition, sip_final_code, sip_final_reason, billsec, duration, sell_billed_seconds,
			sell_price::text AS sell_price, cost::text AS cost, reserved_amount::text AS reserved_amount, charged_amount::text AS charged_amount,
			released_amount::text AS released_amount, hangup_cause, attempts, failover_depth, pdd_ms, carrier_id, called_number, called_number_raw,
			src_ip::text AS src_ip, billed_at, reject_reason, media_mode, codec_in
			FROM cdrs WHERE src_ip = '%s' AND called_number_raw = '%s' AND start_time >= '%s' AND billed_at IS NOT NULL
			ORDER BY start_time DESC LIMIT 1`, srcIP, called, since.UTC().Format(time.RFC3339)))
		if len(rows) == 1 {
			return rows[0]
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no billed CDR for %s -> %s", srcIP, called)
	return nil
}

func cdrsSince(t *testing.T, srcIP string, since time.Time) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rows := sql(t, fmt.Sprintf(`SELECT disposition, sip_final_code, sip_final_reason, billsec, sell_price::text AS sell_price, billed_at
			FROM cdrs WHERE src_ip = '%s' AND start_time >= '%s' ORDER BY start_time`, srcIP, since.UTC().Format(time.RFC3339)))
		allBilled := len(rows) > 0
		for _, r := range rows {
			if r["billed_at"] == nil {
				allBilled = false
			}
		}
		if allBilled {
			return rows
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func attempts(t *testing.T, cdr map[string]any) []map[string]any {
	t.Helper()
	var a []map[string]any
	switch v := cdr["attempts"].(type) {
	case []any:
		for _, x := range v {
			a = append(a, x.(map[string]any))
		}
	case string:
		_ = json.Unmarshal([]byte(v), &a)
	}
	return a
}

func expectFinal(t *testing.T, res callResult, want string) {
	t.Helper()
	for _, l := range res.FinalLines {
		if l == want {
			return
		}
	}
	t.Errorf("customer did not receive %q, got finals %v", want, res.FinalLines)
}

func expectField(t *testing.T, cdr map[string]any, field, want string) {
	t.Helper()
	if got := str(cdr[field]); got != want {
		t.Errorf("cdr.%s = %q want %q", field, got, want)
	}
}

func expectMoney(t *testing.T, cdr map[string]any, field, want string) {
	t.Helper()
	if got := dec(t, cdr[field]); !got.Equal(decimal.RequireFromString(want)) {
		t.Errorf("cdr.%s = %s want %s", field, got.StringFixed(6), want)
	}
}

func reconcileOK(t *testing.T) {
	t.Helper()
	cmd := compose(t, "exec", "-T", "api", "/app/sbc-api", "reconcile")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "0 mismatches") {
		t.Errorf("reconcile failed: %v\n%s", err, out)
	}
}

func activeCalls(t *testing.T) int {
	t.Helper()
	rows := sql(t, `SELECT count(*) AS n FROM active_calls`)
	return int(rows[0]["n"].(float64))
}
