package watchdog

import (
	"strings"
	"testing"
	"time"
)

func report(checks ...Check) Report {
	return Report{IP: "203.0.113.9", Node: "sbc-1", Version: "live", At: time.Now(), Checks: checks, Uptime: "3d 2h"}
}

func okCheck(name string) Check { return Check{Group: "Services", Name: name, Status: "active"} }
func failCheck(name string) Check {
	return Check{Group: "Containers", Name: name, Level: LevelFail, Status: "missing", Detail: "bring the stack up"}
}

func TestDecideAlertsOnceThenRepeats(t *testing.T) {
	st := State{Failing: map[string]string{}}
	p := Policy{RepeatAfter: 30 * time.Minute, ReportHour: -1}
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	r := report(okCheck("docker.service"), failCheck("freeswitch"))

	d := Decide(r, &st, p, now)
	if !d.SendAlert || len(d.New) != 1 || d.New[0].Name != "freeswitch" {
		t.Fatalf("first pass should alert once: %+v", d)
	}
	// five minutes later the same problem must stay quiet
	d = Decide(r, &st, p, now.Add(5*time.Minute))
	if d.SendAlert {
		t.Errorf("an unchanged problem must not alert again after 5 minutes")
	}
	// after the repeat window it speaks up again
	d = Decide(r, &st, p, now.Add(31*time.Minute))
	if !d.SendAlert || d.Reason != "the problem is still there" {
		t.Errorf("a lasting problem should be repeated: %+v", d)
	}
	// and the recovery is noticed
	d = Decide(report(okCheck("docker.service"), okCheck("freeswitch")), &st, p, now.Add(40*time.Minute))
	if !d.SendRecovery || len(d.Fixed) != 1 || d.Fixed[0].Name != "freeswitch" {
		t.Fatalf("recovery should be reported: %+v", d)
	}
	if len(st.Failing) != 0 {
		t.Errorf("state should be clean after recovery: %v", st.Failing)
	}
}

func TestDecideWarnPolicy(t *testing.T) {
	warn := report(Check{Group: "Resources", Name: "disk /", Level: LevelWarn, Status: "85% used"})
	st := State{Failing: map[string]string{}}
	if d := Decide(warn, &st, Policy{ReportHour: -1}, time.Now()); d.SendAlert {
		t.Error("a warning must stay quiet when AlertOnWarn is off")
	}
	st = State{Failing: map[string]string{}}
	if d := Decide(warn, &st, Policy{ReportHour: -1, AlertOnWarn: true}, time.Now()); !d.SendAlert {
		t.Error("a warning should alert when AlertOnWarn is on")
	}
}

func TestDecideReportsReboot(t *testing.T) {
	boot := time.Date(2026, 10, 17, 4, 31, 0, 0, time.UTC)
	st := State{Failing: map[string]string{}, LastBoot: boot.Add(-72 * time.Hour)}
	r := report(okCheck("docker.service"))
	r.Reboot = boot
	d := Decide(r, &st, Policy{ReportHour: -1}, boot.Add(10*time.Minute))
	if !d.SendAlert {
		t.Fatal("an unplanned reboot should raise an alert even when every service came back")
	}
	if len(d.New) != 1 || !strings.Contains(d.New[0].Detail, "restarted") {
		t.Errorf("the alert should explain the reboot: %+v", d.New)
	}
}

func TestDecideDailyReport(t *testing.T) {
	st := State{Failing: map[string]string{}}
	p := Policy{ReportHour: 8}
	r := report(okCheck("docker.service"))
	if d := Decide(r, &st, p, time.Date(2026, 10, 17, 7, 55, 0, 0, time.Local)); d.SendReport {
		t.Error("no report before the hour")
	}
	d := Decide(r, &st, p, time.Date(2026, 10, 17, 8, 5, 0, 0, time.Local))
	if !d.SendReport {
		t.Fatal("the report should go out in the configured hour")
	}
	if d2 := Decide(r, &st, p, time.Date(2026, 10, 17, 8, 40, 0, 0, time.Local)); d2.SendReport {
		t.Error("only one report a day")
	}
}

func TestSubjectsCarryNameAndAddress(t *testing.T) {
	clean := report(okCheck("docker.service"))
	broken := report(failCheck("freeswitch"), failCheck("api"))
	for name, got := range map[string]string{
		"ok":       clean.subject("ok"),
		"alert":    broken.subject("alert"),
		"recovery": clean.subject("recovery"),
	} {
		if !strings.Contains(got, "Super SBC") || !strings.Contains(got, "203.0.113.9") {
			t.Errorf("%s subject must carry the name and the address: %q", name, got)
		}
	}
	if s := broken.subject("alert"); !strings.Contains(s, "+1 more") || !strings.HasPrefix(s, "[ALERT]") {
		t.Errorf("alert subject: %q", s)
	}
	// an "all clear" that is not clear must not claim it is
	if s := broken.subject("ok"); strings.Contains(s, "all services OK") {
		t.Errorf("a report with problems must not say all OK: %q", s)
	}
}

func TestRenderCoversEveryCheck(t *testing.T) {
	r := report(okCheck("docker.service"), okCheck("redis"), failCheck("freeswitch"))
	r.Traffic = Traffic{Known: true, Attempts: 120, Answered: 90, ASR: "75.0", Minutes: "310.5", Revenue: "41.20", Margin: "6.80", LiveCalls: 3}
	m := r.OK()
	for _, want := range []string{"docker.service", "redis", "freeswitch", "120", "75.0", "41.20", "Services", "Containers"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text report is missing %q", want)
		}
		if !strings.Contains(m.HTML, want) {
			t.Errorf("html report is missing %q", want)
		}
	}
	if !strings.Contains(m.HTML, "<!doctype html>") || strings.Contains(m.HTML, "<script") {
		t.Error("the html body should be a plain document with no script")
	}
	a := r.Alert(Decision{})
	if strings.Contains(a.Text, "docker.service") {
		t.Error("an alert should carry only the problems, not every passing check")
	}
	if !strings.Contains(a.Text, "bring the stack up") {
		t.Error("an alert should carry what to do")
	}
}

func TestLevelsAndHelpers(t *testing.T) {
	if LevelOK.String() != "OK" || LevelWarn.String() != "WARN" || LevelFail.String() != "FAIL" {
		t.Error("level names")
	}
	if humanBytes(1536) != "1.5 KB" || humanBytes(5*1024*1024*1024) != "5.0 GB" {
		t.Errorf("humanBytes: %s %s", humanBytes(1536), humanBytes(5*1024*1024*1024))
	}
	if ago(30*time.Second) != "just now" || ago(90*time.Minute) != "1 h ago" {
		t.Errorf("ago: %s %s", ago(30*time.Second), ago(90*time.Minute))
	}
	name, v, ok := sample(`sbc_gateway_up{gateway="drc_avo"} 1`)
	if !ok || name != "drc_avo" || v != 1 {
		t.Errorf("sample: %q %v %v", name, v, ok)
	}
}
