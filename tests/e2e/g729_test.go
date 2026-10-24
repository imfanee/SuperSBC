//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freeswitchLog returns the FreeSWITCH container log since a point in time.
func freeswitchLog(t *testing.T, since time.Time) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "docker", "logs", "supersbc-freeswitch-1",
		"--since", since.UTC().Format(time.RFC3339)).CombinedOutput()
	if err != nil {
		t.Fatalf("freeswitch log: %v", err)
	}
	return string(out)
}

// expectNoCodecErrors fails if FreeSWITCH complained about a codec. The
// pass-through mod_g729 logged "This codec is only usable in passthrough
// mode!" and then "Codec G.729 encoder error!" for exactly these calls.
func expectNoCodecErrors(t *testing.T, since time.Time) {
	t.Helper()
	log := freeswitchLog(t, since)
	for _, bad := range []string{"only usable in passthrough mode", "encoder error", "decoder error", "Codec Activation Error", "CODEC_NEGOTIATION"} {
		if strings.Contains(log, bad) {
			t.Errorf("freeswitch logged %q:\n%s", bad, tail(log, 2000))
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// mod_bcg729 replaces the pass-through stub, so the module that is loaded must
// be the real codec and the stub must be gone (D-86).
func TestG729_ModuleIsTheRealCodec(t *testing.T) {
	out, err := exec.CommandContext(context.Background(), "docker", "exec", "supersbc-freeswitch-1",
		"sh", "-c", "ls /usr/lib/freeswitch/mod/ | grep -i 729").CombinedOutput()
	if err != nil {
		t.Fatalf("list modules: %v %s", err, out)
	}
	mods := string(out)
	if !strings.Contains(mods, "mod_bcg729.so") {
		t.Errorf("mod_bcg729.so is not installed: %s", mods)
	}
	if strings.Contains(mods, "mod_g729.so") {
		t.Errorf("the pass-through mod_g729.so is still installed: %s", mods)
	}
	// The codec has to be registered with the 12 implementations the module adds.
	logs, err := exec.CommandContext(context.Background(), "docker", "logs", "supersbc-freeswitch-1").CombinedOutput()
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(string(logs), "Successfully Loaded [mod_bcg729]") {
		t.Error("freeswitch never loaded mod_bcg729")
	}
	if n := strings.Count(string(logs), "Adding Codec G729 18 G.729"); n < 12 {
		t.Errorf("G.729 registered %d implementations, want at least 12", n)
	}
}

// A PCMA customer calling a G.729 only carrier has to be transcoded, which is
// what the old pass-through module could not do. Audio really flows: the
// customer plays PCMA frames from a pcap and the carrier echoes the G.729 we
// send, so the encoder and the decoder both run on real frames (D-86).
func TestG729_TranscodeBothDirections(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call_audio.xml.tmpl", map[string]string{"__TALK__": "3000"})
	res := placeCall(t, "customer-acme", sc, "442571234567", 1)
	expectFinal(t, res, "200 OK")

	cdr := lastCDR(t, "172.28.0.101", "442571234567", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "codec_in", "PCMA")
	waitCDRField(t, str(cdr["call_uuid"]), "codec_out", "G729")
	waitCDRField(t, str(cdr["call_uuid"]), "media_mode", "transcode")
	if b := dec(t, cdr["billsec"]); b.IsZero() {
		t.Error("a transcoded call should have talk time")
	}

	// The carrier leg must have been offered G.729 and nothing else: the
	// customer offered PCMA, which this carrier does not allow, so the egress
	// INVITE falls back to the carrier's own list (D-85).
	inv := inviteReceivedByCarrier2(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "RTP/AVP 18") {
		t.Errorf("carrier leg was not offered G.729:\n%s", inv)
	}

	// Frames actually went through the codec in both directions.
	rows := sql(t, "SELECT (rtp_stats->>'rtp_audio_in_packet_count')::int AS in_pkts, (rtp_stats->>'rtp_audio_out_packet_count')::int AS out_pkts FROM cdrs WHERE call_uuid = '"+str(cdr["call_uuid"])+"'")
	if len(rows) != 1 {
		t.Fatal("no rtp stats row")
	}
	if dec(t, rows[0]["in_pkts"]).IsZero() || dec(t, rows[0]["out_pkts"]).IsZero() {
		t.Errorf("no RTP counted, so nothing was transcoded: %+v", rows[0])
	}
	expectNoCodecErrors(t, since)
}

// A G.729 only customer (the ASTER TELECOM shape) reaching a carrier that
// allows G.729 must relay, not transcode: the egress INVITE carries G.729
// alone (D-85) and no codec work happens at all.
func TestG729_RelayWhenBothSidesHaveIt(t *testing.T) {
	since := time.Now()
	sc := scenario(t, "uac_call_g729.xml.tmpl", map[string]string{"__TALK__": "1500"})
	res := placeCall(t, "customer-acme", sc, "442571234568", 1)
	expectFinal(t, res, "200 OK")

	cdr := lastCDR(t, "172.28.0.101", "442571234568", since)
	expectField(t, cdr, "disposition", "answered")
	expectField(t, cdr, "codec_in", "G729")
	waitCDRField(t, str(cdr["call_uuid"]), "codec_out", "G729")
	waitCDRField(t, str(cdr["call_uuid"]), "media_mode", "relay")

	inv := inviteReceivedByCarrier2(t, str(cdr["call_uuid"]))
	if !strings.Contains(inv, "RTP/AVP 18") {
		t.Errorf("carrier leg was not offered G.729:\n%s", inv)
	}
	for _, other := range []string{"RTP/AVP 8 ", "PCMA/8000", "PCMU/8000"} {
		if strings.Contains(inv, other) {
			t.Errorf("the egress INVITE offered %s as well as G.729:\n%s", other, inv)
		}
	}
	expectNoCodecErrors(t, since)
}

// Ten transcoded calls in a row: every one answers, FreeSWITCH stays up and
// nothing in the log suggests the codec misbehaved.
func TestG729_StableUnderLoad(t *testing.T) {
	// Ten reservations need funds; a long soak leaves the demo customer empty
	// and the burst would then measure prepaid rejections, not the codec.
	topUp(t, "acme", "5.00")
	since := time.Now()
	startedAt := containerStartedAt(t)
	sc := scenario(t, "uac_call_audio.xml.tmpl", map[string]string{"__TALK__": "1200"})
	res := placeCall(t, "customer-acme", sc, "442571234569", 10)
	finals := strings.Join(res.FinalLines, ",")
	if n := strings.Count(finals, "200 OK"); n < 10 {
		t.Errorf("only %d of 10 calls answered: %s", n, finals)
	}
	rows := sql(t, "SELECT count(*) AS n FROM cdrs WHERE src_ip = '172.28.0.101' AND called_number_raw = '442571234569' AND disposition = 'answered' AND start_time >= '"+since.UTC().Format(time.RFC3339)+"'")
	if dec(t, rows[0]["n"]).IntPart() < 10 {
		t.Errorf("only %v answered CDRs", rows[0]["n"])
	}
	if now := containerStartedAt(t); now != startedAt {
		t.Errorf("freeswitch restarted during the test: %s -> %s", startedAt, now)
	}
	expectNoCodecErrors(t, since)
}

// containerStartedAt is the FreeSWITCH container's start time; a crash and
// restart changes it.
func containerStartedAt(t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "docker", "inspect", "-f", "{{.State.StartedAt}}{{.State.Restarting}}{{.State.OOMKilled}}", "supersbc-freeswitch-1").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect freeswitch: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// inviteReceivedByCarrier2 is inviteReceivedByCarrier for the G.729 carrier's
// own message log.
func inviteReceivedByCarrier2(t *testing.T, callUUID string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(filepath.Join(repoRoot(t), "tests", "e2e", "out", "carrier-g729.msg"))
		msgs := string(b)
		for _, block := range strings.Split(msgs, "----------------------------------------------- ") {
			if strings.Contains(block, "INVITE sip:") && strings.Contains(block, "X-SBC-Call: "+callUUID) {
				return block
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("carrier-g729 never logged an INVITE for %s", callUUID)
	return ""
}

// topUp credits a demo customer so a burst is not cut short by its balance.
func topUp(t *testing.T, customer, amount string) {
	t.Helper()
	a := newAPIClient(t)
	a.ok(a.login("admin@example.com", adminPassword(t)))
	id := findByName(t, a, "/customers", customer)
	a.ok(a.do("POST", "/customers/"+id+"/account/topup", map[string]string{"amount": amount, "description": "g729 soak"}))
}
