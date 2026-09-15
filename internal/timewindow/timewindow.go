// Package timewindow parses routing windows such as
// "mon-fri 08:00-18:00 Europe/London; sat,sun 00:00-24:00" and tells
// whether an instant falls inside one. Day names are the English three
// letter abbreviations, ranges are inclusive, times are hh:mm from 00:00 to
// 24:00 and the optional location defaults to UTC. An empty window is always
// open. A clause whose end is before its start wraps past midnight
// ("mon 22:00-06:00" is Monday 22:00 to Tuesday 06:00).
package timewindow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var days = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

type clause struct {
	days       [7]bool
	start, end int // minutes since midnight; end 1440 means midnight
	loc        *time.Location
}

// Window is a parsed set of clauses; the zero value is always open.
type Window struct {
	clauses []clause
	src     string
}

// String returns the source text.
func (w Window) String() string { return w.src }

// Always reports whether the window has no restriction.
func (w Window) Always() bool { return len(w.clauses) == 0 }

// Parse parses the text. An empty or blank text is the always-open window.
func Parse(text string) (Window, error) {
	w := Window{src: strings.TrimSpace(text)}
	if w.src == "" {
		return w, nil
	}
	for _, part := range strings.Split(w.src, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		c, err := parseClause(part)
		if err != nil {
			return Window{}, fmt.Errorf("window %q: %w", part, err)
		}
		w.clauses = append(w.clauses, c)
	}
	return w, nil
}

func parseClause(text string) (clause, error) {
	fields := strings.Fields(text)
	if len(fields) < 2 || len(fields) > 3 {
		return clause{}, fmt.Errorf("expected \"days hh:mm-hh:mm [zone]\"")
	}
	c := clause{loc: time.UTC}
	for _, d := range strings.Split(strings.ToLower(fields[0]), ",") {
		if d == "" {
			return clause{}, fmt.Errorf("empty day")
		}
		if from, to, ok := strings.Cut(d, "-"); ok {
			a, okA := days[from]
			b, okB := days[to]
			if !okA || !okB {
				return clause{}, fmt.Errorf("unknown day in %q", d)
			}
			for i := a; ; i = (i + 1) % 7 {
				c.days[i] = true
				if i == b {
					break
				}
			}
			continue
		}
		wd, ok := days[d]
		if !ok {
			return clause{}, fmt.Errorf("unknown day %q", d)
		}
		c.days[wd] = true
	}
	from, to, ok := strings.Cut(fields[1], "-")
	if !ok {
		return clause{}, fmt.Errorf("time range %q must be hh:mm-hh:mm", fields[1])
	}
	var err error
	if c.start, err = parseHM(from); err != nil {
		return clause{}, err
	}
	if c.end, err = parseHM(to); err != nil {
		return clause{}, err
	}
	if c.start == c.end {
		return clause{}, fmt.Errorf("empty time range")
	}
	if len(fields) == 3 {
		if c.loc, err = time.LoadLocation(fields[2]); err != nil {
			return clause{}, fmt.Errorf("unknown time zone %q", fields[2])
		}
	}
	return c, nil
}

func parseHM(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, fmt.Errorf("time %q must be hh:mm", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("time %q must be hh:mm between 00:00 and 24:00", s)
	}
	return hh*60 + mm, nil
}

// Contains reports whether t falls inside the window.
func (w Window) Contains(t time.Time) bool {
	if len(w.clauses) == 0 {
		return true
	}
	for _, c := range w.clauses {
		if c.contains(t) {
			return true
		}
	}
	return false
}

func (c clause) contains(t time.Time) bool {
	lt := t.In(c.loc)
	now := lt.Hour()*60 + lt.Minute()
	if c.start < c.end {
		return c.days[lt.Weekday()] && now >= c.start && now < c.end
	}
	// Wraps midnight: the part after start belongs to the listed day, the
	// part before end to the following day.
	if c.days[lt.Weekday()] && now >= c.start {
		return true
	}
	prev := (lt.Weekday() + 6) % 7
	return c.days[prev] && now < c.end
}
