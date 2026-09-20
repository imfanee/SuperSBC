// Package callcontrol implements the ingress pipeline of Section 3: it turns
// an INVITE description into a decision that Lua executes. Every step is a
// separate method so the individual internal endpoints and the collapsed
// /call/setup endpoint (D-16) share one implementation.
package callcontrol

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// SetupRequest is what Lua posts for every ingress INVITE.
type SetupRequest struct {
	CallUUID  string `json:"call_uuid"`
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	Transport string `json:"transport"`
	Caller    string `json:"caller"`
	Called    string `json:"called"`
	SIPCallID string `json:"sip_call_id"`
	// Codecs offered by the customer (from FreeSWITCH), informational.
	OfferedCodecs string `json:"offered_codecs"`
	Node          string `json:"node"`
	// SRTPOffered is true when the customer's SDP carried crypto attributes.
	SRTPOffered bool `json:"srtp_offered"`
	// Privacy is true when the INVITE asked for caller privacy (Privacy header or anonymous From).
	Privacy bool `json:"privacy"`
	// PAINumber is the user part of P-Asserted-Identity when present.
	PAINumber string `json:"pai_number"`
	// Identity is the STIR/SHAKEN Identity header of the INVITE, if any (D-64).
	Identity string `json:"identity"`
}

// App is a dialplan application Lua executes on the a-leg before dialling.
type App struct {
	App  string `json:"app"`
	Data string `json:"data"`
}

// Reject describes the SIP response Lua must send.
type Reject struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// SellRate is the matched selling rate as returned to Lua and the simulator.
type SellRate struct {
	RateID              uuid.UUID       `json:"rate_id"`
	Prefix              string          `json:"prefix"`
	Destination         string          `json:"destination"`
	RatePerMin          decimal.Decimal `json:"rate_per_min"`
	ConnectFee          decimal.Decimal `json:"connect_fee"`
	InitialIncrement    int             `json:"initial_increment"`
	SubsequentIncrement int             `json:"subsequent_increment"`
	MinDuration         int             `json:"min_duration"`
}

// CarrierChoice is one entry of the ordered dial list.
type CarrierChoice struct {
	Seq            int              `json:"seq"`
	CarrierID      uuid.UUID        `json:"carrier_id"`
	Name           string           `json:"name"`
	Gateway        string           `json:"gateway"`
	Priority       int              `json:"priority"`
	Weight         int              `json:"weight"`
	DialNumber     string           `json:"dial_number"`
	CallerID       string           `json:"caller_id"`
	DialString     string           `json:"dial_string"`
	BuyRateID      *uuid.UUID       `json:"buy_rate_id"`
	BuyRatePerMin  *decimal.Decimal `json:"buy_rate_per_min"`
	BuyDestination string           `json:"buy_destination"`
	NegativeMargin bool             `json:"negative_margin"`
	// Quality is the score used in quality mode (0..1), QualitySamples its evidence; Share the percent in percent mode (D-73).
	Quality          *float64 `json:"quality,omitempty"`
	QualitySamples   int64    `json:"quality_samples,omitempty"`
	Share            int      `json:"share,omitempty"`
	FailoverSIPCodes []int    `json:"failover_sip_codes"`
	Codecs           string   `json:"codecs"`
	IgnoreEarlyMedia bool     `json:"ignore_early_media"`
	Degraded         bool     `json:"degraded"`
	MediaMode        string   `json:"media_mode"` // anchor | proxy | bypass for this pair
	// Passthrough lists a-leg headers Lua copies onto this carrier's INVITE (sip_h_<name>).
	Passthrough []string `json:"passthrough_headers"`
}

// SkippedCarrier explains why a route carrier was not dialled.
type SkippedCarrier struct {
	CarrierID uuid.UUID `json:"carrier_id"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason"`
}

// SetupResponse is the full decision.
type SetupResponse struct {
	Action             string           `json:"action"` // "dial" or "reject"
	Reject             *Reject          `json:"reject,omitempty"`
	RejectStep         string           `json:"reject_step,omitempty"`
	CallUUID           string           `json:"call_uuid"`
	CustomerID         *uuid.UUID       `json:"customer_id,omitempty"`
	CustomerName       string           `json:"customer_name,omitempty"`
	Caller             string           `json:"caller"`
	Called             string           `json:"called"`
	Sell               *SellRate        `json:"sell,omitempty"`
	ReservedAmount     decimal.Decimal  `json:"reserved_amount"`
	Available          decimal.Decimal  `json:"available_before"`
	MaxCallSeconds     int              `json:"max_call_seconds"`
	RouteID            *uuid.UUID       `json:"route_id,omitempty"`
	RoutePrefix        string           `json:"route_prefix,omitempty"`
	Carriers           []CarrierChoice  `json:"carriers"`
	Skipped            []SkippedCarrier `json:"skipped,omitempty"`
	OriginateTimeout   int              `json:"originate_timeout"`
	ProgressTimeout    int              `json:"progress_timeout"`
	CustomerCodecs     string           `json:"customer_codecs"`
	MediaTimeoutMs     int              `json:"media_timeout_ms"`
	MediaHoldTimeoutMs int              `json:"media_hold_timeout_ms"`
	// Vars are channel variables Lua sets on the a-leg so they land in the CDR.
	Vars map[string]string `json:"vars"`
	// Apps are dialplan applications Lua runs on the a-leg before dialling (DTMF detection).
	Apps []App `json:"apps"`
}

// AttemptRequest is what Lua posts after each bridge attempt.
type AttemptRequest struct {
	CallUUID      string     `json:"call_uuid"`
	Seq           int        `json:"seq"`
	CarrierID     uuid.UUID  `json:"carrier_id"`
	SIPCode       int        `json:"sip_code"`
	Reason        string     `json:"reason"`
	HangupCause   string     `json:"hangup_cause"`
	Answered      bool       `json:"answered"`
	ALegGone      bool       `json:"aleg_gone"`
	RingSeconds   int        `json:"ring_seconds"`
	PDDMs         *int       `json:"pdd_ms"`
	StartedAt     *time.Time `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at"`
	BuyRateID     *uuid.UUID `json:"buy_rate_id"`
	BuyRatePerMin *string    `json:"buy_rate_per_min"`
	// RoutePrefix is the routing table prefix the call matched (sbc_route_prefix), for quality tracking (D-73).
	RoutePrefix string `json:"route_prefix"`
}

// AttemptResponse tells Lua whether to continue.
type AttemptResponse struct {
	Classification string `json:"classification"`
	Continue       bool   `json:"continue"`
	// RelayCode and RelayReason are what Lua should send to the customer when
	// the loop stops here (number fault) or the list is exhausted.
	RelayCode   int    `json:"relay_code"`
	RelayReason string `json:"relay_reason"`
}

// ReleaseRequest asks the API to release a reservation for a call that never
// dialled (e.g. Lua hit an internal error after setup).
type ReleaseRequest struct {
	CallUUID string `json:"call_uuid"`
	Reason   string `json:"reason"`
	SIPCode  int    `json:"sip_code"`
}
