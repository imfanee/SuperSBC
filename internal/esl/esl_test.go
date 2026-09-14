package esl

import "testing"

func TestParseEvent(t *testing.T) {
	raw := "Event-Name: CHANNEL_HANGUP_COMPLETE\nUnique-ID: abc-123\nvariable_sip_hangup_phrase: Not%20enough%20funds\nHangup-Cause: CALL_REJECTED\n\n"
	ev := parseEvent(raw)
	if ev.Name != "CHANNEL_HANGUP_COMPLETE" {
		t.Fatalf("name = %q", ev.Name)
	}
	if ev.Get("Unique-ID") != "abc-123" {
		t.Fatalf("uuid = %q", ev.Get("Unique-ID"))
	}
	if ev.Get("variable_sip_hangup_phrase") != "Not enough funds" {
		t.Fatalf("phrase not url-decoded: %q", ev.Get("variable_sip_hangup_phrase"))
	}
}
