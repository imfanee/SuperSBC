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
	for _, offered := range []string{"G729@8000h", "PCMA@8000h", "G729@8000h,PCMA@8000h", "G722@16000h", ""} {
		narrowed := codecStringFor(cust, carr, offered) != "PCMA,PCMU,G729"
		proxied := ResolveMediaMode("smart", offered, cust, carr) == "proxy"
		if narrowed != proxied {
			t.Errorf("offer %q: narrowed=%v proxied=%v", offered, narrowed, proxied)
		}
	}
}

func TestOfferedCodecs(t *testing.T) {
	got := offeredCodecs("G729@8000h@20i@8000b,telephone-event@8000h, PCMU@8000h")
	want := []string{"G729", "PCMU"}
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
