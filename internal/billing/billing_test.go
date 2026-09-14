package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/model"
)

func TestFromVariables(t *testing.T) {
	id := uuid.New()
	v := map[string]string{
		"uuid": id.String(), "start_uepoch": "1700000000000000", "progress_uepoch": "1700000001500000",
		"answer_uepoch": "1700000003000000", "end_uepoch": "1700000010000000", "billsec": "7", "duration": "10",
		"hangup_cause": "NORMAL_CLEARING", "sbc_carrier_id": uuid.New().String(), "sbc_answered": "true",
		"read_codec": "PCMA", "write_codec": "PCMU", "rtp_audio_in_packet_count": "350", "rtp_audio_in_mos": "4.5",
	}
	h, ok := FromVariables(v, "esl")
	if !ok {
		t.Fatal("not ok")
	}
	if h.Billsec != 7 || h.Duration != 10 || h.AnswerTime == nil || h.ProgressTime == nil {
		t.Fatalf("timing wrong: %+v", h)
	}
	if h.SIPCode != 200 || h.AnsweredCarrierID == nil || h.CodecIn != "PCMA" || h.CodecOut != "" {
		t.Fatalf("fields wrong: %+v", h)
	}
	if h.RTPStats["rtp_audio_in_mos"] != 4.5 {
		t.Fatalf("rtp stats: %v", h.RTPStats)
	}

	// rejected call: no answer, code from Lua
	v2 := map[string]string{"uuid": id.String(), "start_uepoch": "1700000000000000", "end_uepoch": "1700000000200000", "hangup_cause": "CALL_REJECTED", "sbc_final_sip_code": "402", "sbc_final_reason": "Not enough funds", "billsec": "0"}
	h2, _ := FromVariables(v2, "json_cdr")
	if h2.SIPCode != 402 || h2.SIPReason != "Not enough funds" || h2.AnswerTime != nil || h2.Billsec != 0 {
		t.Fatalf("reject parse wrong: %+v", h2)
	}
	// proto specific fallback
	v3 := map[string]string{"uuid": id.String(), "proto_specific_hangup_cause": "sip:503", "hangup_cause": "NORMAL_TEMPORARY_FAILURE"}
	h3, _ := FromVariables(v3, "esl")
	if h3.SIPCode != 503 {
		t.Fatalf("psc fallback: %d", h3.SIPCode)
	}
	if _, ok := FromVariables(map[string]string{}, "esl"); ok {
		t.Fatal("missing uuid accepted")
	}
}

func TestDisposition(t *testing.T) {
	cases := []struct {
		answered bool
		cause    string
		code     int
		want     string
	}{
		{true, "NORMAL_CLEARING", 200, model.DispositionAnswered},
		{false, "ORIGINATOR_CANCEL", 487, model.DispositionCancelled},
		{false, "USER_BUSY", 486, model.DispositionBusy},
		{false, "NO_ANSWER", 480, model.DispositionNoAnswer},
		{false, "NORMAL_TEMPORARY_FAILURE", 503, model.DispositionFailed},
		{false, "UNALLOCATED_NUMBER", 404, model.DispositionFailed},
	}
	for _, c := range cases {
		if got := Disposition(c.answered, c.cause, c.code); got != c.want {
			t.Errorf("%s/%d: got %s want %s", c.cause, c.code, got, c.want)
		}
	}
}

func TestAttemptPDD(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := t0.Add(2 * time.Second)
	e2 := t0.Add(6 * time.Second)
	attempts := []model.AttemptRecord{
		{Seq: 1, StartedAt: t0, EndedAt: &e1, Classification: "carrier_fault"},
		{Seq: 2, StartedAt: e1, EndedAt: &e2, Classification: "answered"},
	}
	progress := e1.Add(1200 * time.Millisecond)
	answer := e1.Add(3 * time.Second)
	fillAttemptPDD(attempts, &progress, &answer)
	if attempts[0].PDDMs != nil {
		t.Errorf("attempt 1 should have no PDD")
	}
	if attempts[1].PDDMs == nil || *attempts[1].PDDMs != 1200 {
		t.Errorf("attempt 2 PDD = %v", attempts[1].PDDMs)
	}
	if pdd := callPDD(attempts, t0, &progress, &answer); pdd == nil || *pdd != 1200 {
		t.Errorf("call PDD = %v", pdd)
	}
	if pdd := callPDD(nil, t0, &progress, nil); pdd == nil || *pdd != 3200 {
		t.Errorf("fallback PDD = %v", pdd)
	}
}
