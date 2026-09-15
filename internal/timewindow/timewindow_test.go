package timewindow

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestWindows(t *testing.T) {
	cases := []struct {
		win  string
		when string
		want bool
	}{
		{"", "2026-09-14T12:00:00Z", true},
		{"mon-fri 08:00-18:00", "2026-09-14T12:00:00Z", true},               // Monday noon UTC
		{"mon-fri 08:00-18:00", "2026-09-14T18:00:00Z", false},              // end exclusive
		{"mon-fri 08:00-18:00", "2026-09-13T12:00:00Z", false},              // Sunday
		{"mon-fri 08:00-18:00 Europe/London", "2026-09-14T07:30:00Z", true}, // 08:30 BST
		{"mon-fri 08:00-18:00 Europe/London", "2026-09-14T06:30:00Z", false},
		{"sat,sun 00:00-24:00", "2026-09-13T23:59:00Z", true},
		{"sat,sun 00:00-24:00; mon-fri 18:00-24:00", "2026-09-14T19:00:00Z", true},
		{"mon 22:00-06:00", "2026-09-15T05:00:00Z", true}, // Tuesday 05:00 belongs to Monday night
		{"mon 22:00-06:00", "2026-09-15T07:00:00Z", false},
		{"fri-mon 00:00-24:00", "2026-09-13T12:00:00Z", true}, // wrap of the week: fri sat sun mon
		{"fri-mon 00:00-24:00", "2026-09-16T12:00:00Z", false},
	}
	for _, c := range cases {
		w, err := Parse(c.win)
		if err != nil {
			t.Fatalf("%q: %v", c.win, err)
		}
		if got := w.Contains(at(c.when)); got != c.want {
			t.Errorf("%q at %s: got %v want %v", c.win, c.when, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"mon", "mon 8-18", "xyz 08:00-18:00", "mon 08:00-08:00", "mon 08:00-25:00", "mon 08:00-18:00 Mars/Olympus", "mon 08:00-18:00 x y"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}
