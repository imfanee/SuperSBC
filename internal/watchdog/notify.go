package watchdog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// State is what the watchdog remembers between runs, so a problem alerts once
// rather than every five minutes, and a recovery is noticed.
type State struct {
	Failing      map[string]string `json:"failing"`       // check key to the level it was last alerted at
	LastAlert    time.Time         `json:"last_alert"`    // when the last alert went out
	LastReport   time.Time         `json:"last_report"`   // when the last all clear went out
	LastBoot     time.Time         `json:"last_boot"`     // boot time seen on the previous run
	LastRun      time.Time         `json:"last_run"`      // when the previous pass ran
	Consecutive  int               `json:"consecutive"`   // how many passes in a row were not clean
	AlertedClear bool              `json:"alerted_clear"` // a recovery notice is owed
}

// LoadState reads the state file, returning an empty state when it is missing
// or unreadable: a watchdog that cannot read its history still has to run.
func LoadState(path string) State {
	s := State{Failing: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{Failing: map[string]string{}}
	}
	if s.Failing == nil {
		s.Failing = map[string]string{}
	}
	return s
}

// Save writes the state file.
func (s State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o640)
}

// Decision is what a pass concluded should be sent.
type Decision struct {
	SendAlert    bool
	SendRecovery bool
	SendReport   bool
	Reason       string
	New          []Check // problems not alerted before
	Fixed        []Check // problems that have gone away
}

// Policy configures when to speak up.
type Policy struct {
	RepeatAfter time.Duration // re-alert while the same problem lasts
	ReportHour  int           // local hour for the daily all clear, -1 to never
	Force       bool          // send the all clear now whatever the schedule says
	AlertOnWarn bool          // treat a warning as worth an alert
}

// Decide compares this pass with the remembered state and updates it.
func Decide(r Report, s *State, p Policy, now time.Time) Decision {
	var d Decision
	current := map[string]Check{}
	for _, c := range r.Problems() {
		if c.Level == LevelWarn && !p.AlertOnWarn {
			continue
		}
		current[c.Key()] = c
	}
	for key, c := range current {
		if was, ok := s.Failing[key]; !ok || was != c.Level.String() {
			d.New = append(d.New, c)
		}
	}
	for key := range s.Failing {
		if _, ok := current[key]; !ok {
			group, name, found := strings.Cut(key, "/")
			if !found {
				group, name = "", key
			}
			d.Fixed = append(d.Fixed, Check{Group: group, Name: name, Status: "back to normal"})
		}
	}
	sort.SliceStable(d.New, func(i, j int) bool { return d.New[i].Level > d.New[j].Level })
	sort.SliceStable(d.Fixed, func(i, j int) bool { return d.Fixed[i].Name < d.Fixed[j].Name })

	switch {
	case len(d.New) > 0:
		d.SendAlert, d.Reason = true, "a new problem appeared"
	case len(current) > 0 && p.RepeatAfter > 0 && now.Sub(s.LastAlert) >= p.RepeatAfter:
		d.SendAlert, d.Reason = true, "the problem is still there"
	}
	if len(current) == 0 && len(s.Failing) > 0 {
		d.SendRecovery, d.Reason = true, "everything recovered"
	}
	// A reboot between two passes is worth saying out loud even when every
	// service came back by itself.
	if !r.Reboot.IsZero() && !s.LastBoot.IsZero() && r.Reboot.Sub(s.LastBoot) > time.Minute {
		d.SendAlert = true
		d.New = append(d.New, Check{Group: "Resources", Name: "host reboot", Level: LevelWarn,
			Status: "rebooted " + ago(now.Sub(r.Reboot)),
			Detail: "the machine restarted at " + r.Reboot.Format("15:04 on 2 Jan 2006") + "; every service above came back"})
		if d.Reason == "" {
			d.Reason = "the host rebooted"
		}
	}
	if p.Force {
		d.SendReport = true
	} else if p.ReportHour >= 0 && now.Hour() == p.ReportHour && now.Sub(s.LastReport) > 23*time.Hour {
		d.SendReport = true
	}

	s.Failing = map[string]string{}
	for key, c := range current {
		s.Failing[key] = c.Level.String()
	}
	s.LastRun = now
	s.LastBoot = r.Reboot
	if len(current) > 0 {
		s.Consecutive++
	} else {
		s.Consecutive = 0
	}
	if d.SendAlert || d.SendRecovery {
		s.LastAlert = now
	}
	if d.SendReport {
		s.LastReport = now
	}
	return d
}

// Subject lines always carry the name and the address, so a phone shows what
// is wrong and where before the mail is opened.
func (r Report) subject(kind string) string {
	switch kind {
	case "alert":
		p := r.Problems()
		head := "something is wrong"
		if len(p) > 0 {
			head = p[0].Name + " " + p[0].Status
		}
		if len(p) > 1 {
			head += fmt.Sprintf(" (+%d more)", len(p)-1)
		}
		return fmt.Sprintf("[ALERT] Super SBC %s: %s", r.IP, head)
	case "recovery":
		return fmt.Sprintf("[RECOVERED] Super SBC %s: all services back to normal", r.IP)
	default:
		if p := r.Problems(); len(p) > 0 {
			return fmt.Sprintf("Super SBC %s: %d checks need attention, %s", r.IP, len(p), r.At.Format("2 Jan 2006 15:04 MST"))
		}
		return fmt.Sprintf("Super SBC %s: all services OK, %s", r.IP, r.At.Format("2 Jan 2006 15:04 MST"))
	}
}
