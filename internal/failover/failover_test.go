package failover

import "testing"

func TestClassify(t *testing.T) {
	r := Default()
	cases := []struct {
		name string
		a    Attempt
		want Classification
	}{
		{"answered", Attempt{Answered: true}, Answered},
		{"404 stops", Attempt{SIPCode: 404, HangupCause: "UNALLOCATED_NUMBER"}, NumberFault},
		{"486 stops", Attempt{SIPCode: 486, HangupCause: "USER_BUSY"}, NumberFault},
		{"603 stops", Attempt{SIPCode: 603, HangupCause: "CALL_REJECTED"}, NumberFault},
		{"503 continues", Attempt{SIPCode: 503, HangupCause: "NORMAL_TEMPORARY_FAILURE"}, CarrierFault},
		{"403 from carrier continues", Attempt{SIPCode: 403, HangupCause: "CALL_REJECTED"}, CarrierFault},
		{"408 continues", Attempt{SIPCode: 408, HangupCause: "RECOVERY_ON_TIMER_EXPIRE"}, CarrierFault},
		{"488 codec continues", Attempt{SIPCode: 488, HangupCause: "INCOMPATIBLE_DESTINATION"}, CarrierFault},
		{"unknown 5xx continues", Attempt{SIPCode: 599}, CarrierFault},
		{"unknown 4xx stops", Attempt{SIPCode: 433}, NumberFault},
		{"customer cancelled", Attempt{SIPCode: 487, HangupCause: "ORIGINATOR_CANCEL"}, Cancelled},
		{"a-leg gone", Attempt{ALegGone: true}, Cancelled},
		{"timeout no code", Attempt{HangupCause: "RECOVERY_ON_TIMER_EXPIRE"}, CarrierFault},
		{"gateway down", Attempt{HangupCause: "GATEWAY_DOWN"}, CarrierFault},
		{"progress timeout", Attempt{HangupCause: "PROGRESS_TIMEOUT"}, CarrierFault},
		{"no answer after real ring", Attempt{HangupCause: "NO_ANSWER", RingSeconds: 25}, NumberFault},
		{"no answer too early is carrier fault", Attempt{HangupCause: "NO_ANSWER", RingSeconds: 3}, CarrierFault},
		{"empty cause", Attempt{}, CarrierFault},
		{"carrier override makes 404 a carrier fault", Attempt{SIPCode: 404, CarrierOverride: []int{404, 503}}, CarrierFault},
		{"carrier override makes 503 a number fault", Attempt{SIPCode: 503, CarrierOverride: []int{404}}, NumberFault},
	}
	for _, c := range cases {
		if got := r.Classify(c.a); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
	if !Continue(CarrierFault) || Continue(NumberFault) || Continue(Cancelled) {
		t.Error("Continue wrong")
	}
}

func TestSIPCodeForCause(t *testing.T) {
	if c, _ := SIPCodeForCause("USER_BUSY"); c != 486 {
		t.Errorf("USER_BUSY -> %d", c)
	}
	if c, _ := SIPCodeForCause("NORMAL_TEMPORARY_FAILURE"); c != 503 {
		t.Errorf("NTF -> %d", c)
	}
	if c, _ := SIPCodeForCause("something_new"); c != 503 {
		t.Errorf("unknown -> %d", c)
	}
	if ReasonPhrase(402) != "Payment Required" {
		t.Error("phrase")
	}
}

func TestParseFile(t *testing.T) {
	r, err := Load("../../failover.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if r.Classify(Attempt{SIPCode: 503}) != CarrierFault {
		t.Error("file rules differ from defaults for 503")
	}
	if r.MinRingSecondsForNoAnswer != 10 {
		t.Error("min ring")
	}
}
