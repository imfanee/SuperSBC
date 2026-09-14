// Package failover classifies a failed carrier attempt as a number fault
// (stop, relay to customer) or a carrier fault (try the next carrier), per
// Section 6. Pure functions plus a small rules loader.
package failover

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Classification is the outcome of classifying an attempt.
type Classification string

const (
	Answered     Classification = "answered"
	NumberFault  Classification = "number_fault"
	CarrierFault Classification = "carrier_fault"
	Cancelled    Classification = "cancelled" // the A-leg hung up during the attempt
)

// Rules is the parsed failover.yaml.
type Rules struct {
	NumberFault struct {
		SIPCodes []int    `yaml:"sip_codes"`
		Causes   []string `yaml:"causes"`
	} `yaml:"number_fault"`
	CarrierFault struct {
		SIPCodes []int    `yaml:"sip_codes"`
		Causes   []string `yaml:"causes"`
	} `yaml:"carrier_fault"`
	MinRingSecondsForNoAnswer int    `yaml:"min_ring_seconds_for_no_answer"`
	Default5xx                string `yaml:"default_5xx"`
	DefaultOther              string `yaml:"default_other"`

	numberCodes  map[int]bool
	carrierCodes map[int]bool
	numberCauses map[string]bool
	carrierCause map[string]bool
}

// Load reads and indexes a rules file.
func Load(path string) (*Rules, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read failover rules: %w", err)
	}
	return Parse(b)
}

// Parse parses YAML rules.
func Parse(b []byte) (*Rules, error) {
	var r Rules
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse failover rules: %w", err)
	}
	if r.MinRingSecondsForNoAnswer <= 0 {
		r.MinRingSecondsForNoAnswer = 10
	}
	if r.Default5xx == "" {
		r.Default5xx = string(CarrierFault)
	}
	if r.DefaultOther == "" {
		r.DefaultOther = string(NumberFault)
	}
	r.index()
	return &r, nil
}

// Default returns the built-in rules identical to failover.yaml, used when
// the file is missing so the SBC never runs without a classification table.
func Default() *Rules {
	r, _ := Parse([]byte(defaultYAML))
	return r
}

func (r *Rules) index() {
	r.numberCodes = map[int]bool{}
	r.carrierCodes = map[int]bool{}
	r.numberCauses = map[string]bool{}
	r.carrierCause = map[string]bool{}
	for _, c := range r.NumberFault.SIPCodes {
		r.numberCodes[c] = true
	}
	for _, c := range r.CarrierFault.SIPCodes {
		r.carrierCodes[c] = true
	}
	for _, c := range r.NumberFault.Causes {
		r.numberCauses[strings.ToUpper(c)] = true
	}
	for _, c := range r.CarrierFault.Causes {
		r.carrierCause[strings.ToUpper(c)] = true
	}
}

// Attempt is what Lua reports about one bridge attempt.
type Attempt struct {
	SIPCode     int    // 0 when no SIP response was received
	HangupCause string // FreeSWITCH cause, e.g. NORMAL_TEMPORARY_FAILURE
	Answered    bool
	ALegGone    bool // the customer hung up (ORIGINATOR_CANCEL or a-leg not ready)
	RingSeconds int  // seconds of ringing before the failure (for NO_ANSWER)
	// CarrierOverride is carriers.failover_sip_codes: when non-empty it
	// replaces the carrier_fault SIP code list for this carrier.
	CarrierOverride []int
}

// Classify applies the rules to an attempt.
func (r *Rules) Classify(a Attempt) Classification {
	if a.Answered {
		return Answered
	}
	cause := strings.ToUpper(strings.TrimSpace(a.HangupCause))
	if a.ALegGone || cause == "ORIGINATOR_CANCEL" {
		return Cancelled
	}
	// Carrier override: an explicit list of codes that mean "try the next".
	if len(a.CarrierOverride) > 0 && a.SIPCode > 0 {
		for _, c := range a.CarrierOverride {
			if c == a.SIPCode {
				return CarrierFault
			}
		}
		if a.SIPCode >= 300 {
			return NumberFault
		}
	}
	if a.SIPCode > 0 {
		if r.numberCodes[a.SIPCode] {
			// 487 is a number fault only when the customer cancelled, handled above.
			return NumberFault
		}
		if r.carrierCodes[a.SIPCode] {
			return CarrierFault
		}
		if a.SIPCode >= 500 && a.SIPCode <= 599 {
			return Classification(r.Default5xx)
		}
		return Classification(r.DefaultOther)
	}
	// No SIP code: classify on the FreeSWITCH cause.
	switch {
	case cause == "NO_ANSWER" || cause == "NO_USER_RESPONSE":
		if a.RingSeconds >= r.MinRingSecondsForNoAnswer {
			return NumberFault
		}
		return CarrierFault
	case r.numberCauses[cause]:
		return NumberFault
	case r.carrierCause[cause]:
		return CarrierFault
	case cause == "":
		return CarrierFault
	}
	return Classification(r.DefaultOther)
}

// Continue reports whether the next carrier should be tried.
func Continue(c Classification) bool { return c == CarrierFault }

// SIPCodeForCause maps a FreeSWITCH hangup cause to the SIP code FreeSWITCH
// itself would send (mod_sofia's cause table), used when a b-leg failed
// without a SIP response.
func SIPCodeForCause(cause string) (int, string) {
	switch strings.ToUpper(cause) {
	case "UNALLOCATED_NUMBER", "NO_ROUTE_TRANSIT_NET", "NO_ROUTE_DESTINATION":
		return 404, "Not Found"
	case "USER_BUSY":
		return 486, "Busy Here"
	case "NO_USER_RESPONSE", "NO_ANSWER", "SUBSCRIBER_ABSENT":
		return 480, "Temporarily Unavailable"
	case "CALL_REJECTED":
		return 603, "Decline"
	case "NUMBER_CHANGED", "REDIRECTION_TO_NEW_DESTINATION":
		return 410, "Gone"
	case "INVALID_NUMBER_FORMAT":
		return 484, "Address Incomplete"
	case "NORMAL_CIRCUIT_CONGESTION", "SWITCH_CONGESTION", "NETWORK_OUT_OF_ORDER", "NORMAL_TEMPORARY_FAILURE",
		"REQUESTED_CHAN_UNAVAIL", "GATEWAY_DOWN", "SERVICE_UNAVAILABLE", "DESTINATION_OUT_OF_ORDER", "INVALID_GATEWAY":
		return 503, "Service Unavailable"
	case "RECOVERY_ON_TIMER_EXPIRE", "PROGRESS_TIMEOUT", "ALLOTTED_TIMEOUT":
		return 504, "Server Time-out"
	case "INCOMPATIBLE_DESTINATION", "BEARERCAPABILITY_NOTAUTH", "BEARERCAPABILITY_NOTAVAIL", "BEARERCAPABILITY_NOTIMPL":
		return 488, "Not Acceptable Here"
	case "MANDATORY_IE_MISSING", "INVALID_IE_CONTENTS", "PROTOCOL_ERROR", "INTERWORKING":
		return 500, "Server Internal Error"
	case "FACILITY_REJECTED", "FACILITY_NOT_IMPLEMENTED", "SERVICE_NOT_IMPLEMENTED":
		return 501, "Not Implemented"
	case "EXCHANGE_ROUTING_ERROR":
		return 483, "Too Many Hops"
	case "ORIGINATOR_CANCEL":
		return 487, "Request Terminated"
	case "NORMAL_CLEARING":
		return 480, "Temporarily Unavailable"
	}
	return 503, "Service Unavailable"
}

// ReasonPhrase returns the standard reason phrase for a SIP status code.
func ReasonPhrase(code int) string {
	if p, ok := phrases[code]; ok {
		return p
	}
	switch {
	case code >= 500 && code < 600:
		return "Server Error"
	case code >= 600:
		return "Global Failure"
	case code >= 400:
		return "Client Error"
	}
	return "Unknown"
}

var phrases = map[int]string{
	400: "Bad Request", 401: "Unauthorized", 402: "Payment Required", 403: "Forbidden", 404: "Not Found",
	405: "Method Not Allowed", 406: "Not Acceptable", 407: "Proxy Authentication Required", 408: "Request Timeout",
	410: "Gone", 413: "Request Entity Too Large", 415: "Unsupported Media Type", 416: "Unsupported URI Scheme",
	420: "Bad Extension", 421: "Extension Required", 423: "Interval Too Brief", 480: "Temporarily Unavailable",
	481: "Call/Transaction Does Not Exist", 482: "Loop Detected", 483: "Too Many Hops", 484: "Address Incomplete",
	485: "Ambiguous", 486: "Busy Here", 487: "Request Terminated", 488: "Not Acceptable Here", 491: "Request Pending",
	493: "Undecipherable", 500: "Server Internal Error", 501: "Not Implemented", 502: "Bad Gateway",
	503: "Service Unavailable", 504: "Server Time-out", 505: "Version Not Supported", 513: "Message Too Large",
	580: "Precondition Failure", 600: "Busy Everywhere", 603: "Decline", 604: "Does Not Exist Anywhere", 606: "Not Acceptable",
}

const defaultYAML = `
number_fault:
  sip_codes: [404, 410, 484, 486, 600, 603, 604]
  causes: [USER_BUSY, NO_ANSWER, CALL_REJECTED, UNALLOCATED_NUMBER, NORMAL_CLEARING, NO_USER_RESPONSE, ORIGINATOR_CANCEL]
carrier_fault:
  sip_codes: [401, 402, 403, 407, 408, 480, 488, 500, 501, 502, 503, 504, 505, 513, 580]
  causes: [RECOVERY_ON_TIMER_EXPIRE, NETWORK_OUT_OF_ORDER, NORMAL_TEMPORARY_FAILURE, NORMAL_CIRCUIT_CONGESTION, GATEWAY_DOWN, PROGRESS_TIMEOUT, SERVICE_UNAVAILABLE, INCOMPATIBLE_DESTINATION, BEARERCAPABILITY_NOTAUTH, MANDATORY_IE_MISSING, SWITCH_CONGESTION, REQUESTED_CHAN_UNAVAIL, EXCHANGE_ROUTING_ERROR, DESTINATION_OUT_OF_ORDER, NO_ROUTE_DESTINATION, INVALID_GATEWAY, ALLOTTED_TIMEOUT]
min_ring_seconds_for_no_answer: 10
default_5xx: carrier_fault
default_other: number_fault
`
