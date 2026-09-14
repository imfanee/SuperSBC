//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// Codec policy: a PCMA customer to a PCMU-only carrier is transcoded and the
// CDR records both codecs, the media mode and RTP statistics.
func TestM3_Transcoding(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "2000"})
	res := placeCall(t, "customer-acme", sc, "442171234567", 1)
	expectFinal(t, res, "200 OK")
	cdr := lastCDR(t, "172.28.0.101", "442171234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "codec_in", "PCMA")
	// the carrier side codec arrives with the b-leg hangup, independently of billing
	waitCDRField(t, str(cdr["call_uuid"]), "media_mode", "transcode")
	waitCDRField(t, str(cdr["call_uuid"]), "codec_out", "PCMU")
	inv := inviteReceivedByCarrier(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "m=audio") || strings.Contains(inv, "PCMA/8000") {
		t.Errorf("carrier offer should only contain PCMU:\n%s", inv)
	}
	rows := sql(t, "SELECT rtp_stats IS NOT NULL AS has_stats, rtp_stats ? 'rtp_audio_in_packet_count' AS has_pkts FROM cdrs WHERE call_uuid = '"+str(cdr["call_uuid"])+"'")
	if len(rows) != 1 || rows[0]["has_stats"] != true || rows[0]["has_pkts"] != true {
		t.Errorf("rtp stats missing: %v", rows)
	}
	// and a plain PCMA to PCMA call is relayed
	res = placeCall(t, "customer-acme", sc, "442071234567", 1)
	expectFinal(t, res, "200 OK")
	cdr = lastCDR(t, "172.28.0.101", "442071234567", since)
	waitCDRField(t, str(cdr["call_uuid"]), "media_mode", "relay")
}

// Carrier capacity: carrier-cap1 takes one call, the second concurrent call
// is routed to the next carrier before dialling.
func TestM3_CarrierCapacity(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call.xml.tmpl", map[string]string{"__TALK__": "4000"})
	res := placeCall(t, "customer-acme", sc, "442271234567", 2)
	expectFinal(t, res, "200 OK")
	rows := sql(t, `SELECT c.name AS carrier, d.disposition FROM cdrs d LEFT JOIN carriers c ON c.id = d.carrier_id
		WHERE d.src_ip = '172.28.0.101' AND d.called_number_raw = '442271234567' AND d.start_time >= '`+since.UTC().Format(time.RFC3339)+`' AND d.billed_at IS NOT NULL`)
	deadline := time.Now().Add(20 * time.Second)
	for len(rows) < 2 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		rows = sql(t, `SELECT c.name AS carrier, d.disposition FROM cdrs d LEFT JOIN carriers c ON c.id = d.carrier_id
			WHERE d.src_ip = '172.28.0.101' AND d.called_number_raw = '442271234567' AND d.start_time >= '`+since.UTC().Format(time.RFC3339)+`' AND d.billed_at IS NOT NULL`)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 billed CDRs, got %v", rows)
	}
	carriers := map[string]int{}
	for _, r := range rows {
		if str(r["disposition"]) != "answered" {
			t.Errorf("not answered: %v", r)
		}
		carriers[str(r["carrier"])]++
	}
	if carriers["carrier-cap1"] != 1 || carriers["carrier-answer"] != 1 {
		t.Errorf("expected one call per carrier, got %v", carriers)
	}
	// the slot is released after hangup
	res = placeCall(t, "customer-acme", sc, "442271234567", 1)
	expectFinal(t, res, "200 OK")
	reconcileOK(t)
}

// Prometheus metrics are exposed on the admin listener.
func TestM3_Metrics(t *testing.T) {
	cmd := compose(t, "exec", "-T", "api", "wget", "-qO-", "http://127.0.0.1:8080/metrics")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	body := string(out)
	for _, want := range []string{"sbc_calls_billed_total", "sbc_carrier_attempts_total", "sbc_call_setups_total", "sbc_internal_api_duration_seconds_bucket", "sbc_calls_in_progress", "sbc_gateway_up", "sbc_media_mode_total"} {
		if !strings.Contains(body, want) {
			t.Errorf("metric %s missing", want)
		}
	}
}
