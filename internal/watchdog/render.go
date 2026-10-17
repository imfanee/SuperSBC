package watchdog

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// Mail is a rendered message ready for the mailer.
type Mail struct {
	Subject string
	Text    string
	HTML    string
}

const (
	colOK   = "#1a7f37"
	colWarn = "#9a6700"
	colFail = "#b42318"
	colMute = "#57606a"
)

func levelColour(l Level) string {
	switch l {
	case LevelFail:
		return colFail
	case LevelWarn:
		return colWarn
	default:
		return colOK
	}
}

func levelMark(l Level) string {
	switch l {
	case LevelFail:
		return "FAIL"
	case LevelWarn:
		return "WARN"
	default:
		return "OK"
	}
}

// Alert is the short message sent the moment something is wrong: what broke,
// what to do, and nothing else to scroll past on a phone.
func (r Report) Alert(d Decision) Mail {
	problems := r.Problems()
	var t strings.Builder
	fmt.Fprintf(&t, "Super SBC %s (%s)\n%s\n\n", r.IP, r.Node, r.At.Format("Mon 2 Jan 2006 15:04:05 MST"))
	fmt.Fprintf(&t, "%d of %d checks are not OK.\n\n", len(problems), len(r.Checks))
	for _, c := range problems {
		fmt.Fprintf(&t, "[%s] %s: %s\n", levelMark(c.Level), c.Name, c.Status)
		if c.Detail != "" {
			fmt.Fprintf(&t, "       %s\n", c.Detail)
		}
	}
	if len(d.Fixed) > 0 {
		t.WriteString("\nBack to normal since the last alert:\n")
		for _, c := range d.Fixed {
			fmt.Fprintf(&t, "  %s\n", c.Name)
		}
	}
	if d.JustUnmuted {
		t.WriteString("\nThe maintenance window has closed; this is the first alert since it opened.\n")
	}
	fmt.Fprintf(&t, "\nLive calls now: %d. Host up %s.\n", r.Traffic.LiveCalls, r.Uptime)

	var h strings.Builder
	h.WriteString(htmlHead(r, "Something needs attention", colFail))
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;margin:0 0 18px">`)
	for _, c := range problems {
		fmt.Fprintf(&h, `<tr><td style="padding:10px 12px;border-left:4px solid %s;background:#fff;border-bottom:1px solid #e6e8eb">
<div style="font:600 14px/1.4 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#111">%s <span style="color:%s">%s</span></div>
<div style="font:13px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s;margin-top:2px">%s</div></td></tr>`,
			levelColour(c.Level), html.EscapeString(c.Name), levelColour(c.Level), html.EscapeString(c.Status),
			colMute, html.EscapeString(c.Detail))
	}
	h.WriteString(`</table>`)
	if len(d.Fixed) > 0 {
		h.WriteString(`<p style="font:13px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:` + colOK + `">Back to normal: `)
		names := make([]string, 0, len(d.Fixed))
		for _, c := range d.Fixed {
			names = append(names, html.EscapeString(c.Name))
		}
		h.WriteString(strings.Join(names, ", ") + `</p>`)
	}
	fmt.Fprintf(&h, `<p style="font:13px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s">Live calls now %d, host up %s. %d of %d checks are fine.</p>`,
		colMute, r.Traffic.LiveCalls, html.EscapeString(r.Uptime), len(r.Checks)-len(problems), len(r.Checks))
	h.WriteString(htmlFoot(r))
	return Mail{Subject: r.subject("alert"), Text: t.String(), HTML: h.String()}
}

// Recovery says the trouble is over without repeating the whole report.
func (r Report) Recovery(d Decision) Mail {
	var t strings.Builder
	fmt.Fprintf(&t, "Super SBC %s (%s)\n%s\n\nEverything is back to normal. All %d checks pass.\n\n",
		r.IP, r.Node, r.At.Format("Mon 2 Jan 2006 15:04:05 MST"), len(r.Checks))
	for _, c := range d.Fixed {
		fmt.Fprintf(&t, "  recovered: %s\n", c.Name)
	}
	var h strings.Builder
	h.WriteString(htmlHead(r, "Back to normal", colOK))
	fmt.Fprintf(&h, `<p style="font:14px/1.6 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#111">Everything is back to normal. All %d checks pass.</p>`, len(r.Checks))
	if len(d.Fixed) > 0 {
		h.WriteString(`<ul style="font:13px/1.6 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:` + colMute + `">`)
		for _, c := range d.Fixed {
			fmt.Fprintf(&h, `<li>%s</li>`, html.EscapeString(c.Name))
		}
		h.WriteString(`</ul>`)
	}
	h.WriteString(htmlFoot(r))
	return Mail{Subject: r.subject("recovery"), Text: t.String(), HTML: h.String()}
}

// OK is the scheduled all clear: every check, grouped, with what the SBC
// carried over the last day. The maintenance banner, when one is open, says
// that a quiet inbox is the window and not the SBC.
func (r Report) OK(m MuteNotice) Mail {
	var t strings.Builder
	fmt.Fprintf(&t, "Super SBC %s (%s)\n%s\n\n", r.IP, r.Node, r.At.Format("Mon 2 Jan 2006 15:04:05 MST"))
	if m.Muted {
		fmt.Fprintf(&t, "MAINTENANCE MODE: alerts are muted until %s%s.\n\n",
			m.MutedUntil.Format("15:04 on 2 Jan 2006"), reasonSuffix(m.Reason))
	}
	worst := r.Worst()
	if worst == LevelOK {
		fmt.Fprintf(&t, "All %d checks pass.\n", len(r.Checks))
	} else {
		fmt.Fprintf(&t, "%d of %d checks are not OK.\n", len(r.Problems()), len(r.Checks))
	}
	if r.Traffic.Known {
		fmt.Fprintf(&t, "\nLast 24 hours: %d calls, %d answered, ASR %s%%, %s minutes, revenue %s, margin %s.\nLive now: %d calls.\n",
			r.Traffic.Attempts, r.Traffic.Answered, r.Traffic.ASR, r.Traffic.Minutes,
			r.Traffic.money(r.Traffic.Revenue), r.Traffic.money(r.Traffic.Margin), r.Traffic.LiveCalls)
	}
	for _, g := range r.Groups() {
		fmt.Fprintf(&t, "\n%s\n%s\n", g, strings.Repeat("-", len(g)))
		for _, c := range r.In(g) {
			line := fmt.Sprintf("  %-4s %-26s %s", levelMark(c.Level), c.Name, c.Status)
			t.WriteString(strings.TrimRight(line, " ") + "\n")
			if c.Detail != "" {
				fmt.Fprintf(&t, "       %s\n", c.Detail)
			}
		}
	}
	fmt.Fprintf(&t, "\nHost up %s, version %s, checked in %s.\n", r.Uptime, r.Version, r.Duration)

	var h strings.Builder
	title, colour := "All services OK", colOK
	if worst != LevelOK {
		title, colour = fmt.Sprintf("%d checks need attention", len(r.Problems())), levelColour(worst)
	}
	h.WriteString(htmlHead(r, title, colour))
	if m.Muted {
		fmt.Fprintf(&h, `<div style="padding:10px 12px;margin:0 0 16px;background:#fff8e6;border:1px solid #f0d58c;border-radius:6px;font:13px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s">Maintenance mode: alerts are muted until %s%s. Checks keep running and this report is unaffected.</div>`,
			colWarn, html.EscapeString(m.MutedUntil.Format("15:04 on 2 Jan 2006")), html.EscapeString(reasonSuffix(m.Reason)))
	}
	if r.Traffic.Known {
		h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;margin:0 0 20px;background:#fff;border:1px solid #e6e8eb;border-radius:6px"><tr>`)
		for _, kv := range [][2]string{
			{"Calls (24h)", fmt.Sprint(r.Traffic.Attempts)},
			{"Answered", fmt.Sprint(r.Traffic.Answered)},
			{"ASR", r.Traffic.ASR + "%"},
			{"Minutes", r.Traffic.Minutes},
			{"Revenue", r.Traffic.money(r.Traffic.Revenue)},
			{"Margin", r.Traffic.money(r.Traffic.Margin)},
			{"Live now", fmt.Sprint(r.Traffic.LiveCalls)},
		} {
			fmt.Fprintf(&h, `<td style="padding:12px 10px;text-align:center;border-right:1px solid #f0f2f4">
<div style="font:11px/1.2 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s;text-transform:uppercase;letter-spacing:.04em">%s</div>
<div style="font:600 17px/1.4 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#111;margin-top:3px">%s</div></td>`,
				colMute, html.EscapeString(kv[0]), html.EscapeString(kv[1]))
		}
		h.WriteString(`</tr></table>`)
	}
	for _, g := range r.Groups() {
		fmt.Fprintf(&h, `<div style="font:600 12px/1.2 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s;text-transform:uppercase;letter-spacing:.05em;margin:18px 0 6px">%s</div>`, colMute, html.EscapeString(g))
		h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;background:#fff;border:1px solid #e6e8eb;border-radius:6px">`)
		for i, c := range r.In(g) {
			border := "border-top:1px solid #f0f2f4"
			if i == 0 {
				border = ""
			}
			fmt.Fprintf(&h, `<tr><td style="padding:8px 12px;%s;font:13px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#111;width:40%%">%s</td>
<td style="padding:8px 12px;%s;font:13px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s">%s</td>
<td style="padding:8px 12px;%s;text-align:right"><span style="font:600 11px/1.6 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#fff;background:%s;border-radius:10px;padding:2px 8px">%s</span></td></tr>`,
				border, html.EscapeString(c.Name),
				border, colMute, html.EscapeString(c.Status),
				border, levelColour(c.Level), levelMark(c.Level))
			if c.Detail != "" {
				fmt.Fprintf(&h, `<tr><td colspan="3" style="padding:0 12px 8px;font:12px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s">%s</td></tr>`,
					levelColour(c.Level), html.EscapeString(c.Detail))
			}
		}
		h.WriteString(`</table>`)
	}
	h.WriteString(htmlFoot(r))
	return Mail{Subject: r.subject("ok"), Text: t.String(), HTML: h.String()}
}

func htmlHead(r Report, title, colour string) string {
	return fmt.Sprintf(`<!doctype html><html><body style="margin:0;padding:0;background:#f6f8fa">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:#f6f8fa"><tr><td align="center" style="padding:24px 12px">
<table role="presentation" width="640" cellpadding="0" cellspacing="0" style="max-width:640px;width:100%%">
<tr><td style="padding:0 0 16px">
<div style="font:600 18px/1.3 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#111">Super SBC <span style="color:%s">%s</span></div>
<div style="font:600 15px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s;margin-top:4px">%s</div>
<div style="font:12px/1.5 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s;margin-top:2px">%s on %s</div>
</td></tr><tr><td>`,
		colMute, html.EscapeString(r.IP), colour, html.EscapeString(title), colMute,
		html.EscapeString(r.At.Format("Mon 2 Jan 2006 15:04:05 MST")), html.EscapeString(r.Node))
}

func htmlFoot(r Report) string {
	return fmt.Sprintf(`</td></tr><tr><td style="padding:18px 0 0;border-top:1px solid #e6e8eb;margin-top:18px">
<div style="font:11px/1.6 -apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:%s">Super SBC watchdog on %s, version %s. Host up %s, checks took %s.</div>
</td></tr></table></td></tr></table></body></html>`,
		colMute, html.EscapeString(r.Node), html.EscapeString(r.Version), html.EscapeString(r.Uptime), html.EscapeString(r.Duration))
}

// money puts the billing currency beside an amount.
func (t Traffic) money(v string) string {
	if t.Currency == "" {
		return v
	}
	return v + " " + t.Currency
}

// MuteNotice tells the report whether a maintenance window is open.
type MuteNotice struct {
	Muted      bool
	MutedUntil time.Time
	Reason     string
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}
