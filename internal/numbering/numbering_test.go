package numbering

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		raw  string
		opts Options
		want string
		err  bool
	}{
		{"447700900123", Options{}, "447700900123", false},
		{"+447700900123", Options{}, "447700900123", false},
		{"00447700900123", Options{}, "447700900123", false},
		{"011447700900123", Options{IntlPrefix: "011"}, "447700900123", false},
		{"07700900123", Options{DefaultCountryCode: "44"}, "447700900123", false},
		{"07700900123", Options{}, "", true}, // national without country code
		{"#77447700900123", Options{TechPrefix: "#77"}, "447700900123", false},
		{"9999447700900123", Options{TechPrefix: "9999"}, "447700900123", false},
		{"+44 (0) 7700-900123", Options{}, "4407700900123", false}, // digits kept as dialled after +
		{"447700900123;npdi=yes", Options{}, "447700900123", false},
		{"abc", Options{}, "", true},
		{"", Options{}, "", true},
		{"123456", Options{}, "", true},           // too short
		{"1234567890123456", Options{}, "", true}, // 16 digits, too long
		{"00", Options{}, "", true},
		{"1", Options{}, "", true},
		{"12125551234", Options{DefaultCountryCode: "1"}, "12125551234", false},
	}
	for _, c := range cases {
		got, err := Normalize(c.raw, c.opts)
		if c.err {
			if err == nil {
				t.Errorf("Normalize(%q) expected error, got %q", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q) unexpected error %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q want %q", c.raw, got, c.want)
		}
	}
}

func TestNormalizeCaller(t *testing.T) {
	if got := NormalizeCaller("+15551234567", Options{}); got != "15551234567" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeCaller("anonymous", Options{}); got != "anonymous" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeCaller("0201234567", Options{DefaultCountryCode: "44"}); got != "44201234567" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeCaller("", Options{}); got != "" {
		t.Errorf("got %q", got)
	}
}
