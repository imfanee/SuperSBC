// Package rating is the pure rating engine of Section 4. No I/O, no floats.
package rating

import (
	"github.com/shopspring/decimal"
)

// Increments describes how a rate bills time.
type Increments struct {
	Initial     int // seconds billed at minimum (60, 30, 1 ...)
	Subsequent  int // billing granularity after the initial block
	MinDuration int // calls shorter than this are billed 0 (grace)
}

// Rate is the money part of a rate row.
type Rate struct {
	PerMinute  decimal.Decimal
	ConnectFee decimal.Decimal
	Increments
}

// Scale is the number of decimal places every monetary result carries (NUMERIC(18,6)).
const Scale = 6

var sixty = decimal.NewFromInt(60)

// BilledSeconds applies the increment rules of Section 4 to a raw billsec.
//
//	billsec <= 0            -> 0
//	billsec < min_duration  -> 0
//	billsec <= initial      -> initial
//	else                    -> initial + ceil((billsec-initial)/subsequent) * subsequent
func BilledSeconds(billsec int, inc Increments) int {
	if billsec <= 0 {
		return 0
	}
	if billsec < inc.MinDuration {
		return 0
	}
	initial := inc.Initial
	if initial <= 0 {
		initial = 1
	}
	subsequent := inc.Subsequent
	if subsequent <= 0 {
		subsequent = 1
	}
	if billsec <= initial {
		return initial
	}
	rest := billsec - initial
	blocks := (rest + subsequent - 1) / subsequent // ceil
	return initial + blocks*subsequent
}

// Amount prices billed seconds: connect_fee + rate_per_min * billed / 60,
// rounded half-up to 6 decimal places. Zero billed seconds cost nothing,
// including the connect fee.
func Amount(billedSeconds int, ratePerMin, connectFee decimal.Decimal) decimal.Decimal {
	if billedSeconds <= 0 {
		return decimal.Zero.Round(Scale)
	}
	secs := decimal.NewFromInt(int64(billedSeconds))
	// Use a high internal precision for the division, then round once.
	usage := ratePerMin.Mul(secs).DivRound(sixty, Scale+4)
	return connectFee.Add(usage).Round(Scale)
}

// Price rates a raw billsec with a full rate (increments applied).
func Price(billsec int, r Rate) (billed int, amount decimal.Decimal) {
	billed = BilledSeconds(billsec, r.Increments)
	return billed, Amount(billed, r.PerMinute, r.ConnectFee)
}

// ReserveAmount is the pre-deduction of Section 3 Step 4: reserve_minutes at
// the matched rate, with the increment rules applied (D-18).
func ReserveAmount(reserveMinutes int, r Rate) decimal.Decimal {
	_, amt := Price(reserveMinutes*60, r)
	return amt
}

// MaxCallSeconds is how long the customer may talk with `spendable` money at
// the rate (D-20). A zero rate returns capSeconds. The connect fee is paid
// first; if it alone exceeds the spendable money, zero is returned.
func MaxCallSeconds(spendable decimal.Decimal, r Rate, capSeconds int) int {
	if capSeconds <= 0 {
		return 0
	}
	if spendable.LessThanOrEqual(decimal.Zero) {
		return 0
	}
	afterFee := spendable.Sub(r.ConnectFee)
	if afterFee.LessThanOrEqual(decimal.Zero) {
		return 0
	}
	if r.PerMinute.LessThanOrEqual(decimal.Zero) {
		return capSeconds
	}
	// seconds = floor(afterFee * 60 / rate_per_min)
	secs := afterFee.Mul(sixty).Div(r.PerMinute).Floor()
	if !secs.IsInteger() || secs.GreaterThan(decimal.NewFromInt(int64(capSeconds))) {
		return capSeconds
	}
	n := int(secs.IntPart())
	if n > capSeconds {
		return capSeconds
	}
	return n
}
