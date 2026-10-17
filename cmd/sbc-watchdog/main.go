// Command sbc-watchdog checks every part of a live SBC and reports by
// e-mail: a formatted all clear once a day, and a short alert the moment
// anything is wrong (D-79). It runs on the host from a systemd timer, reads
// its SMTP credentials from the environment file, and never prints them.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/imfanee/supersbc/internal/mail"
	"github.com/imfanee/supersbc/internal/watchdog"
)

func main() { os.Exit(run()) }

// run does one pass and returns the exit code, so every deferred cleanup
// happens before the process leaves.
func run() int {
	var (
		envFile  = flag.String("env", "/root/supersbc/.env.live", "environment file with the SMTP settings")
		state    = flag.String("state", "/var/lib/supersbc/watchdog.json", "where to remember what was already alerted")
		report   = flag.Bool("report", false, "send the all clear report now, whatever the schedule says")
		dry      = flag.Bool("dry-run", false, "print what would be sent and send nothing")
		mute     = flag.Duration("mute", 0, "hold alerts back for this long, for planned maintenance (for example 2h)")
		muteWhy  = flag.String("mute-reason", "", "what the maintenance is, shown in the reports")
		unmute   = flag.Bool("unmute", false, "end the maintenance window now")
		printOut = flag.String("print", "", "write the rendered message to this file as well (ok, alert or recovery picked automatically)")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	env := readEnv(*envFile)

	// Opening or closing a maintenance window only touches the state file:
	// it is a flag for the passes that follow, not a pass of its own.
	if *mute > 0 || *unmute {
		st := watchdog.LoadState(*state)
		switch {
		case *unmute:
			st.MutedUntil, st.MuteReason = time.Time{}, ""
			fmt.Println("maintenance window closed, alerts are live again")
		default:
			st.MutedUntil, st.MuteReason, st.MutedAlerts = time.Now().Add(*mute), *muteWhy, 0
			fmt.Printf("alerts muted until %s%s\n", st.MutedUntil.Format("15:04 on 2 Jan 2006"), reasonSuffix(*muteWhy))
		}
		if err := st.Save(*state); err != nil {
			log.Error("save state", "error", err)
			return 1
		}
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts := watchdog.Options{
		Project:    env.get("SBC_WATCHDOG_PROJECT", "supersbc-live"),
		Services:   env.list("SBC_WATCHDOG_UNITS", "docker.service,nftables.service,supersbc-fwsync.service"),
		Containers: env.list("SBC_WATCHDOG_CONTAINERS", "api,web,postgres,redis,freeswitch,hepdb,heplify"),
		ReadyURL:   env.get("SBC_WATCHDOG_READY_URL", "http://127.0.0.1:"+env.get("SBC_ADMIN_PORT", "28080")+"/readyz"),
		MetricsURL: env.get("SBC_WATCHDOG_METRICS_URL", "http://127.0.0.1:"+env.get("SBC_ADMIN_PORT", "28080")+"/metrics"),
		NFTable:    env.get("SBC_WATCHDOG_NFT_TABLE", "sbc"),
		NFSets:     env.list("SBC_WATCHDOG_NFT_SETS", "customers,carriers"),
		DiskPath:   env.get("SBC_WATCHDOG_DISK", "/"),

		DiskWarnPct: env.num("SBC_WATCHDOG_DISK_WARN_PCT", 80),
		DiskFailPct: env.num("SBC_WATCHDOG_DISK_FAIL_PCT", 92),
		TLSFiles:    env.list("SBC_WATCHDOG_TLS_FILES", ""),
		TLSWarnDays: env.num("SBC_WATCHDOG_TLS_WARN_DAYS", 21),

		PGContainer:  env.get("SBC_WATCHDOG_PG_CONTAINER", "supersbc-live-postgres-1"),
		PGUser:       env.get("POSTGRES_USER", "supersbc"),
		PGDatabase:   env.get("POSTGRES_DB", "supersbc"),
		Currency:     env.get("SBC_BILLING_CURRENCY", "USD"),
		HEPContainer: env.get("SBC_WATCHDOG_HEP_CONTAINER", "supersbc-live-hepdb-1"),
		HEPUser:      env.get("SBC_WATCHDOG_HEP_USER", "homer"),
		HEPDatabase:  env.get("SBC_WATCHDOG_HEP_DB", "homer_data"),
	}
	r := watchdog.Run(ctx, opts)
	r.IP = env.get("SBC_WATCHDOG_IP", env.get("SBC_FS_NODE_IP", env.get("SBC_PUBLIC_IP", "")))
	if r.IP == "" {
		r.IP = r.Node
	}

	st := watchdog.LoadState(*state)
	policy := watchdog.Policy{
		RepeatAfter: time.Duration(env.num("SBC_WATCHDOG_REPEAT_MINUTES", 30)) * time.Minute,
		ReportHour:  env.num("SBC_WATCHDOG_REPORT_HOUR", 8),
		Force:       *report,
		AlertOnWarn: env.get("SBC_WATCHDOG_ALERT_ON_WARN", "true") != "false",
	}
	now := time.Now()
	d := watchdog.Decide(r, &st, policy, now)

	to := env.list("SBC_WATCHDOG_TO", env.get("SBC_BOOTSTRAP_ADMIN_EMAIL", ""))
	from := env.get("SBC_WATCHDOG_FROM", "Super SBC <"+env.get("SBC_SMTP_USERNAME", "")+">")
	mailer := mail.New(mail.Config{
		Host: env.get("SBC_SMTP_HOST", ""), Port: env.num("SBC_SMTP_PORT", 587),
		Username: env.get("SBC_SMTP_USERNAME", ""), Password: env.get("SBC_SMTP_PASSWORD", ""),
		From: from, TLS: env.get("SBC_SMTP_TLS", ""),
	})

	var outgoing []watchdog.Mail
	switch {
	case d.SendAlert:
		outgoing = append(outgoing, r.Alert(d))
	case d.SendRecovery:
		outgoing = append(outgoing, r.Recovery(d))
	}
	if d.SendReport {
		outgoing = append(outgoing, r.OK(watchdog.MuteNotice{Muted: d.Muted, MutedUntil: d.MutedUntil, Reason: st.MuteReason}))
	}

	log.Info("watchdog pass", "worst", r.Worst().String(), "checks", len(r.Checks), "problems", len(r.Problems()),
		"sending", len(outgoing), "muted", d.Muted, "reason", d.Reason)
	for _, c := range r.Problems() {
		log.Warn("check not ok", "name", c.Name, "level", c.Level.String(), "status", c.Status, "detail", c.Detail)
	}

	if *printOut != "" && len(outgoing) > 0 {
		_ = os.WriteFile(*printOut, []byte(outgoing[0].HTML), 0o600)
		_ = os.WriteFile(*printOut+".txt", []byte(outgoing[0].Text), 0o600)
	}
	if *dry {
		for _, m := range outgoing {
			fmt.Println("Subject:", m.Subject)
			fmt.Println(m.Text)
		}
		return exitCode(r)
	}
	for _, m := range outgoing {
		if !mailer.Enabled() {
			log.Error("cannot send", "subject", m.Subject, "error", "SBC_SMTP_HOST is empty in "+*envFile)
			continue
		}
		if len(to) == 0 {
			log.Error("cannot send", "subject", m.Subject, "error", "no recipient: set SBC_WATCHDOG_TO")
			continue
		}
		if err := mailer.SendMessage(ctx, mail.Message{To: to, From: from, Subject: m.Subject, Text: m.Text, HTML: m.HTML}); err != nil {
			// The error can quote the server greeting; it never carries the password.
			log.Error("send failed", "subject", m.Subject, "error", err)
			continue
		}
		log.Info("sent", "subject", m.Subject, "to", strings.Join(to, ","))
	}
	if err := st.Save(*state); err != nil {
		log.Error("save state", "error", err)
	}
	return exitCode(r)
}

// exitCode lets systemd and a human see the outcome without reading the mail.
func exitCode(r watchdog.Report) int {
	switch r.Worst() {
	case watchdog.LevelFail:
		return 2
	case watchdog.LevelWarn:
		return 1
	}
	return 0
}

// envMap is the parsed environment file. It is never printed.
type envMap map[string]string

func readEnv(path string) envMap {
	m := envMap{}
	f, err := os.Open(path) //nolint:gosec // operator supplied path
	if err != nil {
		return m
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.TrimSuffix(strings.TrimPrefix(v, `"`), `"`)
		m[strings.TrimSpace(k)] = v
	}
	return m
}

func (m envMap) get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}

func (m envMap) num(key string, def int) int {
	if n, err := strconv.Atoi(m.get(key, "")); err == nil {
		return n
	}
	return def
}

func (m envMap) list(key, def string) []string {
	var out []string
	for _, p := range strings.Split(m.get(key, def), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// reasonSuffix matches the one the reports use, for the console line.
func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}
