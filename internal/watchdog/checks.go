// Package watchdog checks that every part of a live SBC is running and
// reports by e-mail: a formatted all clear on a schedule, and a short alert
// the moment anything is wrong (D-79).
package watchdog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Level is how bad a result is. The zero value is OK so a check that forgets
// to set it never raises a false alarm.
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelFail
)

func (l Level) String() string {
	switch l {
	case LevelFail:
		return "FAIL"
	case LevelWarn:
		return "WARN"
	default:
		return "OK"
	}
}

// Check is one thing that was looked at.
type Check struct {
	Group  string `json:"group"`  // Services, Carriers, Resources, Security
	Name   string `json:"name"`   // what was checked
	Level  Level  `json:"level"`  // how it came out
	Status string `json:"status"` // short state, for example "running" or "92% full"
	Detail string `json:"detail"` // what an operator needs to act, empty when fine
}

// Key identifies a check across runs, so a state change can be recognised.
func (c Check) Key() string { return c.Group + "/" + c.Name }

// Report is the outcome of one pass.
type Report struct {
	Node     string    `json:"node"`
	IP       string    `json:"ip"`
	Version  string    `json:"version"`
	At       time.Time `json:"at"`
	Checks   []Check   `json:"checks"`
	Traffic  Traffic   `json:"traffic"`
	Uptime   string    `json:"uptime"`
	Reboot   time.Time `json:"reboot"`
	Duration string    `json:"duration"`
}

// Traffic summarises the last 24 hours for the all clear report.
type Traffic struct {
	Known     bool   `json:"known"`
	Attempts  int64  `json:"attempts"`
	Answered  int64  `json:"answered"`
	Minutes   string `json:"minutes"`
	ASR       string `json:"asr"`
	Revenue   string `json:"revenue"`
	Margin    string `json:"margin"`
	Currency  string `json:"currency"`
	LiveCalls int64  `json:"live_calls"`
}

// Worst is the highest level in the report.
func (r Report) Worst() Level {
	worst := LevelOK
	for _, c := range r.Checks {
		if c.Level > worst {
			worst = c.Level
		}
	}
	return worst
}

// Problems are the checks that are not OK, worst first.
func (r Report) Problems() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Level != LevelOK {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level > out[j].Level })
	return out
}

// Groups returns the checks grouped in display order.
func (r Report) Groups() []string {
	order := []string{"Services", "Containers", "Carriers", "Capture", "Resources", "Security"}
	seen := map[string]bool{}
	var out []string
	for _, g := range order {
		for _, c := range r.Checks {
			if c.Group == g && !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	for _, c := range r.Checks {
		if !seen[c.Group] {
			seen[c.Group] = true
			out = append(out, c.Group)
		}
	}
	return out
}

// In returns the checks of one group.
func (r Report) In(group string) []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Group == group {
			out = append(out, c)
		}
	}
	return out
}

// Options configure one pass.
type Options struct {
	Project      string   // docker compose project, for example supersbc-live
	Services     []string // systemd units that must be active
	Containers   []string // compose services that must be running
	ReadyURL     string   // unauthenticated /readyz
	MetricsURL   string   // unauthenticated /metrics
	NFTable      string   // nftables table name, empty to skip
	NFSets       []string // sets that must exist and hold at least one element
	DiskPath     string   // filesystem to watch
	DiskWarnPct  int      // warn above this used percentage
	DiskFailPct  int      // fail above this used percentage
	TLSFiles     []string // certificates to check for expiry
	TLSWarnDays  int
	PGContainer  string // container holding the SBC database, empty to skip traffic and capture checks
	PGUser       string
	PGDatabase   string
	Currency     string // shown beside the money figures in the report
	HEPContainer string // container holding the capture database, empty to skip
	HEPUser      string
	HEPDatabase  string
}

// Run makes one pass over everything in opts.
func Run(ctx context.Context, opts Options) Report {
	start := time.Now()
	r := Report{At: start, Checks: []Check{}}
	r.Node, _ = os.Hostname()
	r.Uptime, r.Reboot = uptime()

	for _, unit := range opts.Services {
		r.add(systemdCheck(ctx, unit))
	}
	r.addAll(containerChecks(ctx, opts))
	r.addAll(readyChecks(ctx, opts.ReadyURL, &r))
	r.addAll(metricChecks(ctx, opts.MetricsURL, &r))
	r.addAll(firewallChecks(ctx, opts))
	r.add(diskCheck(opts))
	r.add(loadCheck())
	r.addAll(tlsChecks(opts))
	r.addAll(captureChecks(ctx, opts))
	r.Traffic = traffic(ctx, opts)
	r.Duration = time.Since(start).Round(time.Millisecond).String()
	return r
}

func (r *Report) add(c Check) {
	if c.Name != "" {
		r.Checks = append(r.Checks, c)
	}
}

func (r *Report) addAll(cs []Check) {
	for _, c := range cs {
		r.add(c)
	}
}

// run executes a command and returns its trimmed output.
func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func systemdCheck(ctx context.Context, unit string) Check {
	c := Check{Group: "Services", Name: unit}
	state, _ := run(ctx, "systemctl", "is-active", unit)
	c.Status = state
	if state != "active" {
		c.Level = LevelFail
		detail, _ := run(ctx, "systemctl", "is-failed", unit)
		c.Detail = fmt.Sprintf("systemd reports %s (%s); start it with systemctl start %s", state, detail, unit)
	}
	return c
}

type composePS struct {
	Service string `json:"Service"`
	Name    string `json:"Name"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

func containerChecks(ctx context.Context, opts Options) []Check {
	if opts.Project == "" {
		return nil
	}
	out, err := run(ctx, "docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+opts.Project,
		"--format", `{{json .}}`)
	if err != nil {
		return []Check{{Group: "Containers", Name: "docker", Level: LevelFail, Status: "unreachable",
			Detail: "cannot talk to the Docker daemon: " + firstLine(out)}}
	}
	found := map[string]composePS{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row struct {
			Names  string `json:"Names"`
			State  string `json:"State"`
			Status string `json:"Status"`
			Labels string `json:"Labels"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		svc := labelValue(row.Labels, "com.docker.compose.service")
		health := ""
		if i := strings.Index(row.Status, "(health"); i >= 0 {
			health = strings.Trim(row.Status[i:], "()")
		} else if strings.Contains(row.Status, "(healthy)") {
			health = "healthy"
		}
		found[svc] = composePS{Service: svc, Name: row.Names, State: row.State, Health: health}
	}
	var out2 []Check
	for _, svc := range opts.Containers {
		c := Check{Group: "Containers", Name: svc}
		ps, ok := found[svc]
		switch {
		case !ok:
			c.Level, c.Status = LevelFail, "missing"
			c.Detail = "no container for this service; bring the stack up with make live-up"
		case ps.State != "running":
			c.Level, c.Status = LevelFail, ps.State
			c.Detail = fmt.Sprintf("container %s is %s; check docker logs %s", ps.Name, ps.State, ps.Name)
		case strings.Contains(ps.Health, "unhealthy"):
			c.Level, c.Status = LevelFail, "unhealthy"
			c.Detail = fmt.Sprintf("container %s reports unhealthy; check docker logs %s", ps.Name, ps.Name)
		case strings.Contains(ps.Health, "starting"):
			c.Level, c.Status = LevelWarn, "starting"
			c.Detail = "container is still starting its health check"
		default:
			c.Status = "running"
		}
		out2 = append(out2, c)
	}
	return out2
}

func labelValue(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func uptime() (string, time.Time) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown", time.Time{}
	}
	secs, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		return "unknown", time.Time{}
	}
	d := time.Duration(secs) * time.Second
	boot := time.Now().Add(-d)
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, h, m), boot
	}
	return fmt.Sprintf("%dh %dm", h, m), boot
}

func diskCheck(opts Options) Check {
	path := opts.DiskPath
	if path == "" {
		path = "/"
	}
	c := Check{Group: "Resources", Name: "disk " + path}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		c.Level, c.Status, c.Detail = LevelWarn, "unknown", err.Error()
		return c
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	used := total - free
	pct := 0
	if total > 0 {
		pct = int(used * 100 / total)
	}
	c.Status = fmt.Sprintf("%d%% used, %s free", pct, humanBytes(free))
	switch {
	case opts.DiskFailPct > 0 && pct >= opts.DiskFailPct:
		c.Level = LevelFail
		c.Detail = fmt.Sprintf("only %s left; clear the Docker build cache (docker builder prune -af) and the journal", humanBytes(free))
	case opts.DiskWarnPct > 0 && pct >= opts.DiskWarnPct:
		c.Level = LevelWarn
		c.Detail = fmt.Sprintf("%s left; a build or a capture burst can fill this", humanBytes(free))
	}
	return c
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}

func loadCheck() Check {
	c := Check{Group: "Resources", Name: "load and memory"}
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		c.Level, c.Status = LevelWarn, "unknown"
		return c
	}
	f := strings.Fields(string(b))
	cores := float64(runtimeNumCPU())
	one, _ := strconv.ParseFloat(f[0], 64)
	memFree, memTotal := meminfo()
	c.Status = fmt.Sprintf("load %s %s %s on %d cores, %s of %s memory free", f[0], f[1], f[2], int(cores), humanBytes(memFree), humanBytes(memTotal))
	if cores > 0 && one > cores*2 {
		c.Level = LevelWarn
		c.Detail = fmt.Sprintf("one minute load %.2f is more than twice the %d cores", one, int(cores))
	}
	if memTotal > 0 && memFree*10 < memTotal {
		c.Level = LevelWarn
		c.Detail = fmt.Sprintf("only %s of %s memory available", humanBytes(memFree), humanBytes(memTotal))
	}
	return c
}

func meminfo() (available, total uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
	}
	return available, total
}
