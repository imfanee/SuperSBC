// Package model holds the domain types shared by store, call control,
// billing and the HTTP layers. Money is decimal.Decimal, never float.
package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Customer is a client identified by source IP.
type Customer struct {
	ID                 uuid.UUID  `json:"id" db:"id"`
	Name               string     `json:"name" db:"name"`
	Status             string     `json:"status" db:"status"`
	RateGroupID        *uuid.UUID `json:"rate_group_id" db:"rate_group_id"`
	RouteGroupID       *uuid.UUID `json:"route_group_id" db:"route_group_id"`
	MaxConcurrentCalls int        `json:"max_concurrent_calls" db:"max_concurrent_calls"`
	MaxCPS             int        `json:"max_cps" db:"max_cps"`
	AllowedCodecs      []string   `json:"allowed_codecs" db:"allowed_codecs"`
	TechPrefix         *string    `json:"tech_prefix" db:"tech_prefix"`
	DefaultCountryCode *string    `json:"default_country_code" db:"default_country_code"`
	IntlPrefix         string     `json:"intl_prefix" db:"intl_prefix"`
	MediaMode          string     `json:"media_mode" db:"media_mode"`
	DTMFMode           string     `json:"dtmf_mode" db:"dtmf_mode"`
	SRTPMode           string     `json:"srtp_mode" db:"srtp_mode"`
	RequireTLS         bool       `json:"require_tls" db:"require_tls"`
	// STIRMode: ignore (default), verify (record the result) or require (reject unverified calls). D-64.
	STIRMode string `json:"stir_mode" db:"stir_mode"`
	// TLSSubject is the certificate subject (CN or SAN, comma separated for several)
	// the customer presents over TLS; empty when client certificates are not verified. D-67.
	TLSSubject             string    `json:"tls_subject" db:"tls_subject"`
	TrustPAI               bool      `json:"trust_pai" db:"trust_pai"`
	BlockedPrefixesEnabled bool      `json:"blocked_prefixes_enabled" db:"blocked_prefixes_enabled"`
	Notes                  string    `json:"notes" db:"notes"`
	CreatedAt              time.Time `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time `json:"updated_at" db:"updated_at"`
}

// CustomerIP is one authorised source address of a customer.
type CustomerIP struct {
	ID         uuid.UUID `json:"id" db:"id"`
	CustomerID uuid.UUID `json:"customer_id" db:"customer_id"`
	IPCIDR     string    `json:"ip_cidr" db:"ip_cidr"`
	Port       *int      `json:"port" db:"port"`
	Transport  string    `json:"transport" db:"transport"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

// Carrier is a supplier reached through a FreeSWITCH gateway.
type Carrier struct {
	ID                   uuid.UUID  `json:"id" db:"id"`
	Name                 string     `json:"name" db:"name"`
	Status               string     `json:"status" db:"status"`
	RateGroupID          *uuid.UUID `json:"rate_group_id" db:"rate_group_id"`
	GatewayHost          string     `json:"gateway_host" db:"gateway_host"`
	GatewayPort          int        `json:"gateway_port" db:"gateway_port"`
	Transport            string     `json:"transport" db:"transport"`
	DNIPrefix            string     `json:"dni_prefix" db:"dni_prefix"`
	ANIPrefix            string     `json:"ani_prefix" db:"ani_prefix"`
	StripDigits          int        `json:"strip_digits" db:"strip_digits"`
	AuthUsername         *string    `json:"auth_username" db:"auth_username"`
	AuthPassword         *string    `json:"auth_password,omitempty" db:"auth_password"`
	FromDomain           *string    `json:"from_domain" db:"from_domain"`
	Register             bool       `json:"register" db:"register"`
	AllowedCodecs        []string   `json:"allowed_codecs" db:"allowed_codecs"`
	MaxConcurrentCalls   int        `json:"max_concurrent_calls" db:"max_concurrent_calls"`
	MaxCPS               int        `json:"max_cps" db:"max_cps"`
	FailoverSIPCodes     []int      `json:"failover_sip_codes" db:"failover_sip_codes"`
	SIPOptionsPing       bool       `json:"sip_options_ping" db:"sip_options_ping"`
	ChargeFailedAttempts bool       `json:"charge_failed_attempts" db:"charge_failed_attempts"`
	IgnoreEarlyMedia     bool       `json:"ignore_early_media" db:"ignore_early_media"`
	MediaMode            string     `json:"media_mode" db:"media_mode"`
	DTMFMode             string     `json:"dtmf_mode" db:"dtmf_mode"`
	SRTPMode             string     `json:"srtp_mode" db:"srtp_mode"`
	PrivacyMode          string     `json:"privacy_mode" db:"privacy_mode"`
	// SignallingSources are extra source addresses (IP or CIDR) of the carrier
	// allow-listed in the host firewall besides the gateway host (D-69).
	SignallingSources []string  `json:"signalling_sources" db:"signalling_sources"`
	Notes             string    `json:"notes" db:"notes"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

// GatewayName is the FreeSWITCH gateway name for the carrier.
func (c Carrier) GatewayName() string { return c.Name }

// RateGroup is a rate deck (selling or buying).
type RateGroup struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Currency    string    `json:"currency" db:"currency"`
	Description string    `json:"description" db:"description"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Rate is one prefix row of a rate deck.
type Rate struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	RateGroupID         uuid.UUID       `json:"rate_group_id" db:"rate_group_id"`
	Prefix              string          `json:"prefix" db:"prefix"`
	Destination         string          `json:"destination" db:"destination"`
	RatePerMin          decimal.Decimal `json:"rate_per_min" db:"rate_per_min"`
	ConnectFee          decimal.Decimal `json:"connect_fee" db:"connect_fee"`
	InitialIncrement    int             `json:"initial_increment" db:"initial_increment"`
	SubsequentIncrement int             `json:"subsequent_increment" db:"subsequent_increment"`
	MinDuration         int             `json:"min_duration" db:"min_duration"`
	EffectiveFrom       time.Time       `json:"effective_from" db:"effective_from"`
	EffectiveTo         *time.Time      `json:"effective_to" db:"effective_to"`
	Enabled             bool            `json:"enabled" db:"enabled"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`
}

// RouteGroup is a routing table.
type RouteGroup struct {
	ID          uuid.UUID `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	LCRMode     bool      `json:"lcr_mode" db:"lcr_mode"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Route is one prefix row with its ordered carriers.
type Route struct {
	ID           uuid.UUID      `json:"id" db:"id"`
	RouteGroupID uuid.UUID      `json:"route_group_id" db:"route_group_id"`
	Prefix       string         `json:"prefix" db:"prefix"`
	Destination  string         `json:"destination" db:"destination"`
	Enabled      bool           `json:"enabled" db:"enabled"`
	LCRMode      bool           `json:"lcr_mode" db:"lcr_mode"`
	Carriers     []RouteCarrier `json:"carriers" db:"carriers"`
	CreatedAt    time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at" db:"updated_at"`
}

// RouteCarrier is one entry of the ordered carrier list of a route.
type RouteCarrier struct {
	ID        uuid.UUID `json:"id" db:"id"`
	RouteID   uuid.UUID `json:"route_id" db:"route_id"`
	CarrierID uuid.UUID `json:"carrier_id" db:"carrier_id"`
	Priority  int       `json:"priority" db:"priority"`
	Weight    int       `json:"weight" db:"weight"`
	Enabled   bool      `json:"enabled" db:"enabled"`
	// Window restricts the carrier to a time-of-day and day-of-week window
	// (internal/timewindow syntax); empty means always.
	Window string `json:"window" db:"window"`
}

// Account is the money account of a customer or carrier.
type Account struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	OwnerType     string          `json:"owner_type" db:"owner_type"`
	OwnerID       uuid.UUID       `json:"owner_id" db:"owner_id"`
	Currency      string          `json:"currency" db:"currency"`
	Balance       decimal.Decimal `json:"balance" db:"balance"`
	AllowedCredit decimal.Decimal `json:"allowed_credit" db:"allowed_credit"`
	Reserved      decimal.Decimal `json:"reserved" db:"reserved"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at" db:"updated_at"`
}

// Available is balance + allowed_credit - reserved (Section 2.1).
func (a Account) Available() decimal.Decimal {
	return a.Balance.Add(a.AllowedCredit).Sub(a.Reserved)
}

// LedgerEntry is one append-only money movement.
type LedgerEntry struct {
	ID           int64           `json:"id" db:"id"`
	AccountID    uuid.UUID       `json:"account_id" db:"account_id"`
	CallUUID     *uuid.UUID      `json:"call_uuid" db:"call_uuid"`
	Type         string          `json:"type" db:"type"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	BalanceAfter decimal.Decimal `json:"balance_after" db:"balance_after"`
	Description  string          `json:"description" db:"description"`
	CreatedBy    *string         `json:"created_by" db:"created_by"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
}

// AttemptRecord is one element of cdrs.attempts.
type AttemptRecord struct {
	Seq            int        `json:"seq" db:"seq"`
	CarrierID      uuid.UUID  `json:"carrier_id" db:"carrier_id"`
	CarrierName    string     `json:"carrier_name" db:"carrier_name"`
	Gateway        string     `json:"gateway" db:"gateway"`
	SIPCode        int        `json:"sip_code" db:"sip_code"`
	Reason         string     `json:"reason" db:"reason"`
	HangupCause    string     `json:"hangup_cause" db:"hangup_cause"`
	StartedAt      time.Time  `json:"started_at" db:"started_at"`
	EndedAt        *time.Time `json:"ended_at" db:"ended_at"`
	PDDMs          *int       `json:"pdd_ms" db:"pdd_ms"`
	Classification string     `json:"classification" db:"classification"`
	BuyRateID      *uuid.UUID `json:"buy_rate_id,omitempty" db:"buy_rate_id"`
	BuyRatePerMin  *string    `json:"buy_rate_per_min,omitempty" db:"buy_rate_per_min"`
	// Cost is the connect fee charged for a failed attempt when the carrier
	// has charge_failed_attempts (D-62); unset otherwise.
	Cost *string `json:"cost,omitempty" db:"cost"`
}

// CDR is one call detail record; every attempted call has exactly one.
type CDR struct {
	CallUUID          uuid.UUID        `json:"call_uuid" db:"call_uuid"`
	CustomerID        *uuid.UUID       `json:"customer_id" db:"customer_id"`
	CarrierID         *uuid.UUID       `json:"carrier_id" db:"carrier_id"`
	SrcIP             *string          `json:"src_ip" db:"src_ip"`
	SrcPort           *int             `json:"src_port" db:"src_port"`
	CallerNumberRaw   string           `json:"caller_number_raw" db:"caller_number_raw"`
	CallerNumber      string           `json:"caller_number" db:"caller_number"`
	CalledNumberRaw   string           `json:"called_number_raw" db:"called_number_raw"`
	CalledNumber      string           `json:"called_number" db:"called_number"`
	StartTime         time.Time        `json:"start_time" db:"start_time"`
	ProgressTime      *time.Time       `json:"progress_time" db:"progress_time"`
	AnswerTime        *time.Time       `json:"answer_time" db:"answer_time"`
	EndTime           *time.Time       `json:"end_time" db:"end_time"`
	PDDMs             *int             `json:"pdd_ms" db:"pdd_ms"`
	RingSeconds       int              `json:"ring_seconds" db:"ring_seconds"`
	Billsec           int              `json:"billsec" db:"billsec"`
	Duration          int              `json:"duration" db:"duration"`
	SIPFinalCode      *int             `json:"sip_final_code" db:"sip_final_code"`
	SIPFinalReason    *string          `json:"sip_final_reason" db:"sip_final_reason"`
	HangupCause       *string          `json:"hangup_cause" db:"hangup_cause"`
	Disposition       string           `json:"disposition" db:"disposition"`
	RejectReason      *string          `json:"reject_reason" db:"reject_reason"`
	SellRateID        *uuid.UUID       `json:"sell_rate_id" db:"sell_rate_id"`
	SellRatePerMin    *decimal.Decimal `json:"sell_rate_per_min" db:"sell_rate_per_min"`
	SellBilledSeconds int              `json:"sell_billed_seconds" db:"sell_billed_seconds"`
	SellPrice         decimal.Decimal  `json:"sell_price" db:"sell_price"`
	SellDestination   *string          `json:"sell_destination" db:"sell_destination"`
	SellCurrency      *string          `json:"sell_currency" db:"sell_currency"`
	BuyCurrency       *string          `json:"buy_currency" db:"buy_currency"`
	SellFX            decimal.Decimal  `json:"sell_fx" db:"sell_fx"`
	BuyFX             decimal.Decimal  `json:"buy_fx" db:"buy_fx"`
	BuyRateID         *uuid.UUID       `json:"buy_rate_id" db:"buy_rate_id"`
	BuyRatePerMin     *decimal.Decimal `json:"buy_rate_per_min" db:"buy_rate_per_min"`
	BuyBilledSeconds  int              `json:"buy_billed_seconds" db:"buy_billed_seconds"`
	Cost              decimal.Decimal  `json:"cost" db:"cost"`
	Margin            decimal.Decimal  `json:"margin" db:"margin"`
	NegativeMargin    bool             `json:"negative_margin" db:"negative_margin"`
	ReservedAmount    decimal.Decimal  `json:"reserved_amount" db:"reserved_amount"`
	ChargedAmount     decimal.Decimal  `json:"charged_amount" db:"charged_amount"`
	ReleasedAmount    decimal.Decimal  `json:"released_amount" db:"released_amount"`
	Attempts          []AttemptRecord  `json:"attempts" db:"attempts"`
	FailoverDepth     int              `json:"failover_depth" db:"failover_depth"`
	CodecIn           *string          `json:"codec_in" db:"codec_in"`
	CodecOut          *string          `json:"codec_out" db:"codec_out"`
	MediaMode         *string          `json:"media_mode" db:"media_mode"`
	RTPStats          map[string]any   `json:"rtp_stats" db:"rtp_stats"`
	TransportIn       *string          `json:"transport_in" db:"transport_in"`
	TransportOut      *string          `json:"transport_out" db:"transport_out"`
	SRTPIn            bool             `json:"srtp_in" db:"srtp_in"`
	SRTPOut           bool             `json:"srtp_out" db:"srtp_out"`
	Privacy           bool             `json:"privacy" db:"privacy"`
	// SIPCallID is the customer leg Call-ID (D-71).
	SIPCallID  *string    `json:"sip_call_id" db:"sip_call_id"`
	STIRStatus *string    `json:"stir_status" db:"stir_status"`
	STIRAttest *string    `json:"stir_attest" db:"stir_attest"`
	SBCNode    *string    `json:"sbc_node" db:"sbc_node"`
	BilledAt   *time.Time `json:"billed_at" db:"billed_at"`
	BilledBy   *string    `json:"billed_by" db:"billed_by"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at" db:"updated_at"`
}

// ActiveCall is a call with an open reservation.
type ActiveCall struct {
	CallUUID       uuid.UUID       `json:"call_uuid" db:"call_uuid"`
	CustomerID     uuid.UUID       `json:"customer_id" db:"customer_id"`
	AccountID      uuid.UUID       `json:"account_id" db:"account_id"`
	CalledNumber   string          `json:"called_number" db:"called_number"`
	ReservedAmount decimal.Decimal `json:"reserved_amount" db:"reserved_amount"`
	MaxCallSeconds int             `json:"max_call_seconds" db:"max_call_seconds"`
	StartedAt      time.Time       `json:"started_at" db:"started_at"`
	ExpiresAt      time.Time       `json:"expires_at" db:"expires_at"`
	// Node is the SBC node that made the reservation (D-68).
	Node string `json:"node" db:"node"`
}

// Disposition values (cdrs.disposition).
const (
	DispositionPending         = "pending"
	DispositionAnswered        = "answered"
	DispositionNoAnswer        = "no_answer"
	DispositionBusy            = "busy"
	DispositionFailed          = "failed"
	DispositionRejectedAuth    = "rejected_auth"
	DispositionRejectedBalance = "rejected_balance"
	DispositionRejectedRoute   = "rejected_route"
	DispositionCancelled       = "cancelled"
)

// Ledger entry types.
const (
	LedgerReserve    = "reserve"
	LedgerRelease    = "release"
	LedgerCharge     = "charge"
	LedgerCost       = "cost"
	LedgerTopup      = "topup"
	LedgerAdjustment = "adjustment"
	LedgerRefund     = "refund"
)
