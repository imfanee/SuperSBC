// Package billing is the only writer of money at call end (Section 5) and the
// reconciliation worker for orphaned reservations.
package billing

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HangupInfo is everything billing needs about a finished call. It is built
// from FreeSWITCH channel variables, whether they arrive by ESL event or by
// mod_json_cdr, so both sources produce identical billing.
type HangupInfo struct {
	CallUUID     uuid.UUID
	StartTime    time.Time
	ProgressTime *time.Time
	AnswerTime   *time.Time
	EndTime      time.Time
	Billsec      int
	Duration     int
	HangupCause  string
	SIPCode      int
	SIPReason    string
	// AnsweredCarrierID is set by Lua (sbc_carrier_id) when a bridge succeeded.
	AnsweredCarrierID *uuid.UUID
	CodecIn           string
	CodecOut          string
	RTPStats          map[string]any
	RejectReason      string
	Source            string // "esl", "json_cdr", "reconcile"
}

// FromVariables builds HangupInfo from a flat map of FreeSWITCH channel
// variables (keys without the "variable_" prefix).
func FromVariables(v map[string]string, source string) (HangupInfo, bool) {
	id, err := uuid.Parse(v["uuid"])
	if err != nil {
		return HangupInfo{}, false
	}
	h := HangupInfo{CallUUID: id, Source: source}
	h.StartTime = uepoch(v["start_uepoch"], v["start_epoch"])
	h.EndTime = uepoch(v["end_uepoch"], v["end_epoch"])
	if h.EndTime.IsZero() {
		h.EndTime = time.Now()
	}
	if t := uepoch(v["progress_uepoch"], v["progress_epoch"]); !t.IsZero() {
		h.ProgressTime = &t
	}
	if t := uepoch(v["answer_uepoch"], v["answer_epoch"]); !t.IsZero() {
		h.AnswerTime = &t
	}
	h.Billsec = atoi(v["billsec"])
	h.Duration = atoi(v["duration"])
	if h.AnswerTime != nil && h.Billsec == 0 {
		h.Billsec = int(h.EndTime.Sub(*h.AnswerTime).Seconds())
	}
	if h.AnswerTime == nil {
		h.Billsec = 0
	}
	h.HangupCause = v["hangup_cause"]
	if h.HangupCause == "" {
		h.HangupCause = v["Hangup-Cause"]
	}
	// SIP code seen by the customer: what Lua decided, else what sofia recorded.
	if c := atoi(v["sbc_final_sip_code"]); c > 0 {
		h.SIPCode = c
		h.SIPReason = v["sbc_final_reason"]
	} else if psc := v["proto_specific_hangup_cause"]; strings.HasPrefix(psc, "sip:") {
		h.SIPCode = atoi(strings.TrimPrefix(psc, "sip:"))
	} else if c := atoi(v["sip_term_status"]); c > 0 {
		h.SIPCode = c
	}
	if h.AnswerTime != nil && h.SIPCode == 0 {
		h.SIPCode = 200
		h.SIPReason = "OK"
	}
	if cid, err := uuid.Parse(v["sbc_carrier_id"]); err == nil && v["sbc_answered"] == "true" {
		h.AnsweredCarrierID = &cid
	}
	// read_codec is what this leg receives; the carrier side codec comes from
	// the b-leg hangup (Engine.RecordBLeg), never from the a-leg's write_codec.
	h.CodecIn = v["read_codec"]
	h.RejectReason = v["sbc_reject_reason"]
	stats := map[string]any{}
	for k, val := range v {
		if strings.HasPrefix(k, "rtp_audio_") {
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				stats[k] = f
			} else {
				stats[k] = val
			}
		}
	}
	if len(stats) > 0 {
		h.RTPStats = stats
	}
	return h, true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// uepoch parses microsecond or second epoch strings; zero when both empty.
func uepoch(micro, sec string) time.Time {
	if u, err := strconv.ParseInt(strings.TrimSpace(micro), 10, 64); err == nil && u > 0 {
		return time.UnixMicro(u).UTC()
	}
	if s, err := strconv.ParseInt(strings.TrimSpace(sec), 10, 64); err == nil && s > 0 {
		return time.Unix(s, 0).UTC()
	}
	return time.Time{}
}
