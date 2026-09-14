// Package numbering normalises dialled and calling numbers to E.164 digits
// without "+" (Section 3 Step 2, D-26). Pure functions only.
package numbering

import (
	"errors"
	"strings"
)

// ErrMalformed is returned when a number cannot be turned into E.164.
var ErrMalformed = errors.New("malformed number")

// Options are the per-customer normalisation settings.
type Options struct {
	TechPrefix         string // stripped when the raw number starts with it
	DefaultCountryCode string // prepended to national numbers (leading 0 removed)
	IntlPrefix         string // international dial prefix, default "00"
}

const (
	minE164 = 7
	maxE164 = 15
)

// Normalize converts a raw dialled string to E.164 digits.
//
// Order: strip tech prefix; keep digits and a leading "+"; "+CC" -> "CC";
// "00CC" (or the customer's intl prefix) -> "CC"; "0national" with a default
// country code -> "CCnational"; length 7 to 15 digits, otherwise ErrMalformed.
func Normalize(raw string, o Options) (string, error) {
	s := strings.TrimSpace(raw)
	if o.TechPrefix != "" && strings.HasPrefix(s, o.TechPrefix) {
		s = s[len(o.TechPrefix):]
	}
	// SIP user parts may carry ";" parameters or user=phone suffixes.
	if i := strings.IndexAny(s, ";@"); i >= 0 {
		s = s[:i]
	}
	plus := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return "", ErrMalformed
	}
	intl := o.IntlPrefix
	if intl == "" {
		intl = "00"
	}
	switch {
	case plus:
		// already international
	case strings.HasPrefix(digits, intl) && len(digits) > len(intl):
		digits = digits[len(intl):]
	case strings.HasPrefix(digits, "0") && o.DefaultCountryCode != "":
		digits = o.DefaultCountryCode + strings.TrimLeft(digits, "0")
	}
	if digits == "" || strings.HasPrefix(digits, "0") {
		return "", ErrMalformed
	}
	if len(digits) < minE164 || len(digits) > maxE164 {
		return "", ErrMalformed
	}
	return digits, nil
}

// NormalizeCaller is lenient: caller IDs may be short, anonymous or
// alphanumeric. It strips "+" and non-digits, keeps whatever is left, and
// returns the raw value when nothing usable remains (e.g. "anonymous").
func NormalizeCaller(raw string, o Options) string {
	s := strings.TrimSpace(raw)
	if i := strings.IndexAny(s, ";@"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return ""
	}
	plus := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if digits == "" {
		return s
	}
	intl := o.IntlPrefix
	if intl == "" {
		intl = "00"
	}
	switch {
	case plus:
	case strings.HasPrefix(digits, intl) && len(digits) > len(intl):
		digits = digits[len(intl):]
	case strings.HasPrefix(digits, "0") && o.DefaultCountryCode != "" && len(digits) > 1:
		digits = o.DefaultCountryCode + strings.TrimLeft(digits, "0")
	}
	return digits
}
