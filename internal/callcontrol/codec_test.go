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
			name:     "a G.729 only caller is offered G.729 first so neither leg transcodes",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "G729@8000h@20i@8000b",
			want:     "G729,PCMA,PCMU",
		},
		{
			name:     "a PCMA caller keeps the carrier order",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "PCMA@8000h@20i@64000b,telephone-event@8000h",
			want:     "PCMA,PCMU,G729",
		},
		{
			name:     "the caller order is kept when it offers several we allow",
			customer: []string{"PCMA", "PCMU", "G729"},
			carrier:  []string{"PCMA", "PCMU", "G729"},
			offered:  "G729@8000h,PCMU@8000h",
			want:     "G729,PCMU,PCMA",
		},
		{
			name:     "an offer the carrier does not allow does not reach the carrier leg",
			customer: []string{"PCMA", "PCMU", "OPUS"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "OPUS@48000h,PCMU@8000h",
			want:     "PCMU,PCMA",
		},
		{
			name:     "no offer behaves as before",
			customer: []string{"PCMA", "G729"},
			carrier:  []string{"G729", "PCMA"},
			offered:  "",
			want:     "G729,PCMA",
		},
		{
			name:     "an offer nobody allows leaves the intersection alone",
			customer: []string{"PCMA", "PCMU"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "G722@16000h",
			want:     "PCMA,PCMU",
		},
		{
			name:     "no intersection still falls back to the carrier list",
			customer: []string{"OPUS"},
			carrier:  []string{"PCMA", "PCMU"},
			offered:  "OPUS@48000h",
			want:     "PCMA,PCMU",
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
