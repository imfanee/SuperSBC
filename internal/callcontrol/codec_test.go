package callcontrol

import "testing"

func TestCodecStringFor(t *testing.T) {
	cases := []struct {
		name     string
		customer []string
		carrier  []string
		offered  string
		want     string
	}{
		{
			name:     "a G.729 only caller is offered G.729 only, so the carrier cannot answer PCMA",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "G729@8000h@20i@8000b,telephone-event@8000h",
			want:     "G729",
		},
		{
			name:     "a PCMA only caller is offered PCMA only",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "PCMA@8000h@20i@64000b,telephone-event@8000h",
			want:     "PCMA",
		},
		{
			name:     "several matching codecs keep the caller's order",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "G729@8000h,PCMU@8000h",
			want:     "G729,PCMU",
		},
		{
			name:     "only the matching ones are offered, the rest are dropped",
			customer: []string{"PCMA", "PCMU", "OPUS"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "OPUS@48000h,PCMU@8000h",
			want:     "PCMU",
		},
		{
			name:     "no match means the carrier's codecs and a transcode",
			customer: []string{"PCMA", "PCMU", "G722"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "G722@16000h",
			want:     "PCMA,PCMU",
		},
		{
			name:     "a codec the customer is not allowed to use is not a match",
			customer: []string{"PCMA"},
			carrier:  []string{"PCMA", "G729"},
			offered:  "G729@8000h",
			want:     "PCMA",
		},
		{
			name:     "telephone-event alone is not a match",
			customer: []string{"PCMA", "PCMU"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "telephone-event@8000h",
			want:     "PCMA,PCMU",
		},
		{
			name:     "the module prefix FreeSWITCH puts on each codec is stripped",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "CORE_PCM_MODULE.PCMU@8000h@20i@64000b,CORE_PCM_MODULE.telephone-event@8000h",
			want:     "PCMU",
		},
		{
			name:     "a prefixed G.729 offer narrows too",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "mod_bcg729.G729@8000h@20i@8000b",
			want:     "G729",
		},
		{
			name:     "no offer at all behaves as before: the intersection in carrier order",
			customer: []string{"PCMA", "G729"},
			carrier:  []string{"G729", "PCMA"},
			offered:  "",
			want:     "G729,PCMA",
		},
		{
			name:     "the carrier's spelling is used, not the offer's",
			customer: []string{"g729"},
			carrier:  []string{"G729"},
			offered:  "g729@8000h",
			want:     "G729",
		},
		{
			name:     "lists with nothing in common fall back to the carrier",
			customer: []string{"OPUS"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "OPUS@48000h",
			want:     "PCMA,PCMU",
		},
		{
			name:     "a carrier without a list leaves the customer's",
			customer: []string{"PCMA"},
			carrier:  nil,
			offered:  "PCMA@8000h",
			want:     "PCMA",
		},
		{
			name:     "a customer without a list leaves the carrier's",
			customer: nil,
			carrier:  []string{"PCMA", "G729"},
			offered:  "G729@8000h",
			want:     "PCMA,G729",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := codecStringFor(c.customer, c.carrier, c.offered); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// The egress list and the media decision must always agree: a narrowed list
// means one codec on both legs, which is exactly when proxying is safe.
func TestCodecNarrowingAgreesWithMediaMode(t *testing.T) {
	cust := []string{"PCMA", "PCMU", "G729"}
	carr := []string{"PCMA", "PCMU", "G729"}
	for _, offered := range []string{"mod_bcg729.G729@8000h@20i@8000b", "CORE_PCM_MODULE.PCMA@8000h@20i@64000b",
		"mod_bcg729.G729@8000h,CORE_PCM_MODULE.PCMA@8000h", "CORE_PCM_MODULE.G722@16000h", ""} {
		narrowed := codecStringFor(cust, carr, offered) != "PCMA,PCMU,G729"
		proxied := ResolveMediaMode("smart", offered, cust, carr) == "proxy"
		if narrowed != proxied {
			t.Errorf("offer %q: narrowed=%v proxied=%v", offered, narrowed, proxied)
		}
	}
}

func TestOfferedCodecs(t *testing.T) {
	// The real thing, copied off a live channel: the module name prefixes
	// every codec.
	got := offeredCodecs("CORE_PCM_MODULE.PCMU@8000h@20i@64000b,mod_bcg729.G729@8000h@20i@8000b,CORE_PCM_MODULE.telephone-event@8000h")
	want := []string{"PCMU", "G729"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	got = offeredCodecs("G729@8000h@20i@8000b,telephone-event@8000h, PCMU@8000h")
	want = []string{"G729", "PCMU"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if n := len(offeredCodecs("")); n != 0 {
		t.Errorf("empty offer parsed to %d codecs", n)
	}
}

// The real INVITE bodies: what the codec selection actually has to read.
func TestSDPAudioCodecs(t *testing.T) {
	// ASTER TELECOM, copied from the capture of 2026-10-22.
	aster := "v=0\r\no=- 1 1 IN IP4 199.33.126.170\r\ns=-\r\nc=IN IP4 199.33.126.170\r\nt=0 0\r\n" +
		"m=audio 8974 RTP/AVP 18 101\r\na=rtpmap:18 G729/8000/1\r\na=rtpmap:101 telephone-event/8000\r\n" +
		"a=fmtp:101 0-15\r\na=sendrecv\r\n"
	cases := []struct {
		name string
		sdp  string
		want []string
	}{
		{"a G.729 only offer", aster, []string{"G729"}},
		{
			name: "static payloads need no rtpmap",
			sdp:  "m=audio 7000 RTP/AVP 8 0 101\r\na=rtpmap:101 telephone-event/8000\r\n",
			want: []string{"PCMA", "PCMU"},
		},
		{
			name: "the m= line order is the caller's preference",
			sdp:  "m=audio 7000 RTP/AVP 0 8 9\r\na=rtpmap:0 PCMU/8000\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:9 G722/8000\r\n",
			want: []string{"PCMU", "PCMA", "G722"},
		},
		{
			name: "dynamic payloads come from the rtpmap",
			sdp:  "m=audio 7000 RTP/AVP 111 0\r\na=rtpmap:111 opus/48000/2\r\n",
			want: []string{"opus", "PCMU"},
		},
		{
			name: "comfort noise is not a codec to bridge",
			sdp:  "m=audio 7000 RTP/AVP 8 13 101\r\na=rtpmap:13 CN/8000\r\na=rtpmap:101 telephone-event/8000\r\n",
			want: []string{"PCMA"},
		},
		{
			name: "only the first audio section counts",
			sdp:  "m=audio 7000 RTP/AVP 8\r\na=rtpmap:8 PCMA/8000\r\nm=video 7002 RTP/AVP 99\r\na=rtpmap:99 H264/90000\r\n",
			want: []string{"PCMA"},
		},
		{
			name: "video only offers nothing",
			sdp:  "m=video 7002 RTP/AVP 99\r\na=rtpmap:99 H264/90000\r\n",
			want: nil,
		},
		{
			name: "an unknown payload with no rtpmap is skipped",
			sdp:  "m=audio 7000 RTP/AVP 97 8\r\n",
			want: []string{"PCMA"},
		},
		{"no sdp at all", "", nil},
		{"a duplicate payload is listed once", "m=audio 7000 RTP/AVP 8 8\r\n", []string{"PCMA"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sdpAudioCodecs(c.sdp)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

// The SDP wins over FreeSWITCH's parse, because ep_codec_string is empty for
// exactly the calls this rule exists for (D-87).
func TestOfferedFrom(t *testing.T) {
	sdp := "m=audio 8974 RTP/AVP 18 101\r\na=rtpmap:18 G729/8000/1\r\n"
	if got := offeredFrom(sdp, ""); got != "G729" {
		t.Errorf("a G.729 only INVITE with no ep_codec_string: got %q", got)
	}
	if got := offeredFrom(sdp, "CORE_PCM_MODULE.PCMU@8000h@20i@64000b"); got != "G729" {
		t.Errorf("the SDP has to win: got %q", got)
	}
	if got := offeredFrom("", "CORE_PCM_MODULE.PCMU@8000h@20i@64000b"); got != "CORE_PCM_MODULE.PCMU@8000h@20i@64000b" {
		t.Errorf("without an SDP, fall back to the parsed string: got %q", got)
	}
	if got := offeredFrom("", ""); got != "" {
		t.Errorf("nothing at all: got %q", got)
	}
	// End to end: the ASTER shape must narrow the egress list to G.729.
	if got := codecStringFor([]string{"PCMA", "PCMU", "G729"}, []string{"PCMA", "PCMU", "G729"}, offeredFrom(sdp, "")); got != "G729" {
		t.Errorf("egress list for a G.729 only caller: got %q want G729", got)
	}
}
