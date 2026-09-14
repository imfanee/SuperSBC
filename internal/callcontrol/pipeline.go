package callcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/opensbc/opensbc/internal/cache"
	"github.com/opensbc/opensbc/internal/config"
	"github.com/opensbc/opensbc/internal/failover"
	"github.com/opensbc/opensbc/internal/logging"
	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/numbering"
	"github.com/opensbc/opensbc/internal/rating"
	"github.com/opensbc/opensbc/internal/store"
	"github.com/opensbc/opensbc/internal/tables"
)

// CarrierHealth answers whether a carrier should be skipped or demoted.
// The gateway ping poller and the circuit breaker feed it.
type CarrierHealth interface {
	GatewayDown(gateway string) bool
	Degraded(carrierID uuid.UUID) bool
}

type noHealth struct{}

func (noHealth) GatewayDown(string) bool { return false }
func (noHealth) Degraded(uuid.UUID) bool { return false }

// Pipeline is the call control service.
type Pipeline struct {
	cfg    *config.Config
	log    *slog.Logger
	st     *store.Store
	rdb    *redis.Client
	adm    *cache.Admission
	ips    *cache.IPCache
	tables *tables.Tables
	rules  *failover.Rules
	health CarrierHealth
	now    func() time.Time
}

// New wires the pipeline.
func New(cfg *config.Config, log *slog.Logger, st *store.Store, rdb *redis.Client, tb *tables.Tables, rules *failover.Rules) *Pipeline {
	return &Pipeline{
		cfg: cfg, log: log, st: st, rdb: rdb,
		adm: cache.NewAdmission(rdb), ips: cache.NewIPCache(rdb),
		tables: tb, rules: rules, health: noHealth{}, now: time.Now,
	}
}

// SetHealth installs the carrier health source.
func (p *Pipeline) SetHealth(h CarrierHealth) { p.health = h }

// Rules exposes the failover rules.
func (p *Pipeline) Rules() *failover.Rules { return p.rules }

// Admission exposes the Redis admission helper (used by billing).
func (p *Pipeline) Admission() *cache.Admission { return p.adm }

// IPCache exposes the source address cache.
func (p *Pipeline) IPCache() *cache.IPCache { return p.ips }

// Tables exposes the prefix tables.
func (p *Pipeline) Tables() *tables.Tables { return p.tables }

// ---- Step 1: authenticate by source IP ----

// AuthResult is the outcome of Authorize.
type AuthResult struct {
	Customer *model.Customer
	Reject   *Reject
	Admitted bool // a concurrent-call slot was taken and must be released
}

// Authorize resolves the source address to an active customer and applies
// the concurrency and CPS limits (Section 3 Step 1).
func (p *Pipeline) Authorize(ctx context.Context, srcIP string, srcPort int, transport string) (AuthResult, error) {
	transport = strings.ToLower(transport)
	if transport == "" {
		transport = "udp"
	}
	var customerID *uuid.UUID
	if e := p.ips.Get(ctx, srcIP, srcPort, transport); e != nil {
		if !e.Found {
			return AuthResult{Reject: &Reject{403, "IP not authorized"}}, nil
		}
		if id, err := uuid.Parse(e.CustomerID); err == nil {
			customerID = &id
		}
	}
	var cust *model.Customer
	if customerID != nil {
		c, err := p.st.CustomerByID(ctx, *customerID)
		if err == nil {
			cust = c
		}
	}
	if cust == nil {
		m, err := p.st.CustomerByIP(ctx, srcIP, srcPort, transport)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				p.ips.Set(ctx, srcIP, srcPort, transport, cache.Entry{Found: false})
				return AuthResult{Reject: &Reject{403, "IP not authorized"}}, nil
			}
			return AuthResult{}, fmt.Errorf("lookup customer by ip: %w", err)
		}
		cust = &m.Customer
		p.ips.Set(ctx, srcIP, srcPort, transport, cache.Entry{Found: true, CustomerID: cust.ID.String()})
	}
	if cust.Status != "active" {
		return AuthResult{Customer: cust, Reject: &Reject{403, "Customer suspended"}}, nil
	}
	res, err := p.adm.Admit(ctx, "customer", cust.ID, cust.MaxConcurrentCalls, cust.MaxCPS)
	if err != nil {
		return AuthResult{Customer: cust}, err // fail closed
	}
	if res.ConcurrentExceeded {
		return AuthResult{Customer: cust, Reject: &Reject{480, "Concurrent call limit"}}, nil
	}
	if res.CPSExceeded {
		return AuthResult{Customer: cust, Reject: &Reject{503, "CPS limit"}}, nil
	}
	if p.cfg.Routing.GlobalMaxChannels > 0 || p.cfg.Routing.GlobalMaxCPS > 0 {
		g, err := p.adm.Admit(ctx, "global", uuid.Nil, p.cfg.Routing.GlobalMaxChannels, p.cfg.Routing.GlobalMaxCPS)
		if err != nil {
			p.adm.Leave(ctx, "customer", cust.ID)
			return AuthResult{Customer: cust}, err
		}
		if g.ConcurrentExceeded {
			p.adm.Leave(ctx, "customer", cust.ID)
			return AuthResult{Customer: cust, Reject: &Reject{480, "Concurrent call limit"}}, nil
		}
		if g.CPSExceeded {
			p.adm.Leave(ctx, "customer", cust.ID)
			return AuthResult{Customer: cust, Reject: &Reject{503, "CPS limit"}}, nil
		}
	}
	return AuthResult{Customer: cust, Admitted: true}, nil
}

// LeaveAdmission releases the concurrency slots taken by Authorize.
func (p *Pipeline) LeaveAdmission(ctx context.Context, customerID uuid.UUID) {
	p.adm.Leave(ctx, "customer", customerID)
	if p.cfg.Routing.GlobalMaxChannels > 0 || p.cfg.Routing.GlobalMaxCPS > 0 {
		p.adm.Leave(ctx, "global", uuid.Nil)
	}
}

// ---- Step 2: normalisation ----

func normOptions(c *model.Customer) numbering.Options {
	o := numbering.Options{}
	if c.TechPrefix != nil {
		o.TechPrefix = *c.TechPrefix
	}
	if c.DefaultCountryCode != nil {
		o.DefaultCountryCode = *c.DefaultCountryCode
	}
	return o
}

// ---- Step 3: selling rate ----

// SellRateFor finds the customer's selling rate for a normalised number.
func (p *Pipeline) SellRateFor(ctx context.Context, c *model.Customer, called string) (*model.Rate, error) {
	if c.RateGroupID == nil {
		return nil, nil
	}
	r, ok, err := p.tables.MatchRate(ctx, *c.RateGroupID, called)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return r, nil
}

func toRating(r *model.Rate) rating.Rate {
	return rating.Rate{PerMinute: r.RatePerMin, ConnectFee: r.ConnectFee,
		Increments: rating.Increments{Initial: r.InitialIncrement, Subsequent: r.SubsequentIncrement, MinDuration: r.MinDuration}}
}

// ---- Step 4 and 5: balance check and reservation ----

// ReserveResult is the outcome of Reserve.
type ReserveResult struct {
	Account        *model.Account  // after reservation
	Available      decimal.Decimal // before reservation
	ReservedAmount decimal.Decimal
	MaxCallSeconds int
}

// Reserve pre-deducts reserve_minutes at the selling rate in one transaction
// together with the pending CDR row and the active_calls row (Step 5). The
// affordability check is re-run atomically inside the UPDATE.
func (p *Pipeline) Reserve(ctx context.Context, cust *model.Customer, rate *model.Rate, cdr *model.CDR) (*ReserveResult, error) {
	acc, err := p.st.AccountByOwner(ctx, "customer", cust.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrInsufficientFunds
		}
		return nil, err
	}
	rr := toRating(rate)
	reserve := rating.ReserveAmount(p.cfg.Billing.ReserveMinutes, rr)
	available := acc.Available()
	// Step 4: explicit pre-check gives the fast path and the D-19 semantics.
	if !available.GreaterThan(decimal.Zero) || available.LessThan(reserve) {
		return &ReserveResult{Account: acc, Available: available, ReservedAmount: reserve}, store.ErrInsufficientFunds
	}
	capSecs := int(p.cfg.Billing.MaxCallDuration.Seconds())
	maxSecs := rating.MaxCallSeconds(available, rr, capSecs)
	res := &ReserveResult{Available: available, ReservedAmount: reserve, MaxCallSeconds: maxSecs}
	err = p.st.WithTx(ctx, func(tx pgx.Tx) error {
		locked, err := store.Reserve(ctx, tx, acc.ID, cdr.CallUUID, reserve, fmt.Sprintf("reserve %d min for %s", p.cfg.Billing.ReserveMinutes, cdr.CalledNumber))
		if err != nil {
			return err
		}
		res.Account = locked
		cdr.ReservedAmount = reserve
		cdr.Disposition = model.DispositionPending
		if err := store.InsertCDRSetup(ctx, tx, cdr); err != nil {
			return err
		}
		return store.InsertActiveCall(ctx, tx, &model.ActiveCall{
			CallUUID: cdr.CallUUID, CustomerID: cust.ID, AccountID: acc.ID, CalledNumber: cdr.CalledNumber,
			ReservedAmount: reserve, MaxCallSeconds: maxSecs, StartedAt: cdr.StartTime,
			ExpiresAt: cdr.StartTime.Add(p.cfg.Billing.MaxCallDuration),
		})
	})
	if err != nil {
		return res, err
	}
	if err := p.adm.Reserve(ctx, cdr.CallUUID, p.cfg.Billing.MaxCallDuration+p.cfg.Billing.OrphanTimeout); err != nil {
		p.log.Warn("redis reservation key failed", "call_uuid", cdr.CallUUID, "error", err)
	}
	return res, nil
}

// ---- Step 6 and 7: route and buying rates ----

// RouteResult is the outcome of Route.
type RouteResult struct {
	Route    *model.Route
	Carriers []CarrierChoice
	Skipped  []SkippedCarrier
}

// Route finds the route and builds the ordered carrier list with buy rates
// (Steps 6 and 7). Carriers are ordered by priority, ties by weight
// descending then random; degraded carriers go last; DOWN gateways and
// carriers without a buy rate are skipped.
func (p *Pipeline) Route(ctx context.Context, cust *model.Customer, called, caller string, sell *model.Rate, callUUID string) (*RouteResult, error) {
	if cust.RouteGroupID == nil {
		return &RouteResult{}, nil
	}
	route, ok, err := p.tables.MatchRoute(ctx, *cust.RouteGroupID, called)
	if err != nil {
		return nil, err
	}
	if !ok || len(route.Carriers) == 0 {
		return &RouteResult{Route: route}, nil
	}
	ids := make([]uuid.UUID, 0, len(route.Carriers))
	for _, rc := range route.Carriers {
		ids = append(ids, rc.CarrierID)
	}
	carriers, err := p.st.CarriersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	res := &RouteResult{Route: route}
	type cand struct {
		choice CarrierChoice
		rc     model.RouteCarrier
		buy    *model.Rate
	}
	var cands []cand
	for _, rc := range route.Carriers {
		c, ok := carriers[rc.CarrierID]
		if !ok || c.Status != "active" {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: rc.CarrierID, Name: c.Name, Reason: "disabled"})
			continue
		}
		if p.health.GatewayDown(c.GatewayName()) {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "gateway_down"})
			continue
		}
		var buy *model.Rate
		if c.RateGroupID != nil {
			r, ok, err := p.tables.MatchRate(ctx, *c.RateGroupID, called)
			if err != nil {
				return nil, err
			}
			if ok {
				buy = r
			}
		}
		if buy == nil {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "skipped_no_rate"})
			continue
		}
		negative := sell != nil && buy.RatePerMin.GreaterThan(sell.RatePerMin)
		if negative && p.cfg.Routing.BlockNegativeMargin {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "negative_margin_blocked"})
			continue
		}
		dial := buildDialNumber(called, c)
		ani := c.ANIPrefix + caller
		rate := buy.RatePerMin
		rid := buy.ID
		ch := CarrierChoice{
			CarrierID: c.ID, Name: c.Name, Gateway: c.GatewayName(), Priority: rc.Priority, Weight: rc.Weight,
			DialNumber: dial, CallerID: ani, BuyRateID: &rid, BuyRatePerMin: &rate, BuyDestination: buy.Destination,
			NegativeMargin: negative, FailoverSIPCodes: c.FailoverSIPCodes, Codecs: codecString(cust.AllowedCodecs, c.AllowedCodecs),
			IgnoreEarlyMedia: c.IgnoreEarlyMedia, Degraded: p.health.Degraded(c.ID),
		}
		ch.DialString = p.dialString(ch, callUUID)
		cands = append(cands, cand{choice: ch, rc: rc, buy: buy})
	}
	// LCR mode (M4): order by buy rate ascending instead of priority.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.choice.Degraded != b.choice.Degraded {
			return !a.choice.Degraded
		}
		if a.rc.Priority != b.rc.Priority {
			return a.rc.Priority < b.rc.Priority
		}
		return a.rc.Weight > b.rc.Weight
	})
	for i := range cands {
		cands[i].choice.Seq = i + 1
		res.Carriers = append(res.Carriers, cands[i].choice)
	}
	return res, nil
}

func buildDialNumber(called string, c model.Carrier) string {
	n := called
	if c.StripDigits > 0 && c.StripDigits < len(n) {
		n = n[c.StripDigits:]
	}
	return c.DNIPrefix + n
}

// codecString intersects customer and carrier codec lists, keeping the
// carrier order; falls back to the carrier list when nothing intersects.
func codecString(customer, carrier []string) string {
	if len(carrier) == 0 {
		return strings.Join(customer, ",")
	}
	if len(customer) == 0 {
		return strings.Join(carrier, ",")
	}
	allowed := map[string]bool{}
	for _, c := range customer {
		allowed[strings.ToUpper(c)] = true
	}
	var out []string
	for _, c := range carrier {
		if allowed[strings.ToUpper(c)] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return strings.Join(carrier, ",")
	}
	return strings.Join(out, ",")
}

// dialString builds the FreeSWITCH originate string for one carrier
// (Section 3 Step 8). Variables that belong to the a-leg (continue_on_fail,
// hangup_after_bridge) are set by Lua, not here.
func (p *Pipeline) dialString(c CarrierChoice, callUUID string) string {
	vars := []string{
		"sip_h_X-SBC-Call=" + callUUID,
		"origination_caller_id_number=" + c.CallerID,
		"origination_caller_id_name=" + c.CallerID,
		"absolute_codec_string=^^:" + strings.ReplaceAll(c.Codecs, ",", ":"),
		fmt.Sprintf("ignore_early_media=%t", c.IgnoreEarlyMedia),
		fmt.Sprintf("originate_timeout=%d", p.cfg.Routing.OriginateTimeout),
		fmt.Sprintf("progress_timeout=%d", p.cfg.Routing.ProgressTimeout),
		// Topology hiding and header sanitisation on egress (Section 7):
		// no customer X-* headers, no P-Asserted-Identity / Remote-Party-ID.
		"sip_copy_custom_headers=false",
		"sip_cid_type=none",
		fmt.Sprintf("media_timeout=%d", p.cfg.Routing.MediaTimeoutSec*1000),
		fmt.Sprintf("media_hold_timeout=%d", p.cfg.Routing.MediaHoldTimeoutSec*1000),
		"sbc_carrier_id=" + c.CarrierID.String(),
		"sbc_attempt_seq=" + fmt.Sprint(c.Seq),
	}
	return "{" + strings.Join(vars, ",") + "}sofia/gateway/" + c.Gateway + "/" + c.DialNumber
}

// ---- Setup: the whole pipeline in one call (D-16) ----

// Setup runs Steps 1 to 7 and returns the decision. Every rejection writes
// a CDR (Section 2.5: every attempted call produces exactly one CDR).
func (p *Pipeline) Setup(ctx context.Context, req SetupRequest) (*SetupResponse, error) {
	log := logging.FromContext(ctx, p.log)
	callUUID, err := uuid.Parse(req.CallUUID)
	if err != nil {
		return nil, fmt.Errorf("bad call_uuid: %w", err)
	}
	now := p.now()
	resp := &SetupResponse{Action: "reject", CallUUID: req.CallUUID, Caller: req.Caller, Called: req.Called,
		OriginateTimeout: p.cfg.Routing.OriginateTimeout, ProgressTimeout: p.cfg.Routing.ProgressTimeout,
		MediaTimeoutMs: p.cfg.Routing.MediaTimeoutSec * 1000, MediaHoldTimeoutMs: p.cfg.Routing.MediaHoldTimeoutSec * 1000, Vars: map[string]string{}}
	node := p.cfg.NodeName
	srcIP := req.SrcIP
	cdr := &model.CDR{CallUUID: callUUID, SrcIP: &srcIP, SrcPort: &req.SrcPort, CallerNumberRaw: req.Caller, CalledNumberRaw: req.Called,
		CallerNumber: req.Caller, CalledNumber: req.Called, StartTime: now, SBCNode: &node}

	reject := func(step string, r Reject, disposition string) *SetupResponse {
		resp.Reject = &r
		resp.RejectStep = step
		resp.Vars["sbc_reject_reason"] = r.Reason
		resp.Vars["sbc_reject_step"] = step
		end := now
		code := r.Code
		reason := r.Reason
		cdr.EndTime = &end
		cdr.Disposition = disposition
		cdr.RejectReason = &reason
		cdr.SIPFinalCode = &code
		cdr.SIPFinalReason = &reason
		cdr.BilledAt = &end
		cdr.BilledBy = &node
		if err := store.InsertCDRSetup(ctx, p.st.Pool(), cdr); err != nil {
			log.Error("write rejected cdr", "error", err)
		}
		log.Info("call rejected", "step", step, "code", r.Code, "reason", r.Reason, "src_ip", req.SrcIP, "called", req.Called)
		return resp
	}

	// Step 1
	auth, err := p.Authorize(ctx, req.SrcIP, req.SrcPort, req.Transport)
	if err != nil {
		return nil, err
	}
	if auth.Customer != nil {
		id := auth.Customer.ID
		cdr.CustomerID = &id
		resp.CustomerID = &id
		resp.CustomerName = auth.Customer.Name
		resp.Vars["sbc_customer_id"] = id.String()
		resp.Vars["sbc_customer_name"] = auth.Customer.Name
	}
	if auth.Reject != nil {
		return reject("authorize", *auth.Reject, model.DispositionRejectedAuth), nil
	}
	cust := auth.Customer
	// From here on a concurrency slot is held; release it on every rejection.
	rejectAdmitted := func(step string, r Reject, disposition string) *SetupResponse {
		p.LeaveAdmission(ctx, cust.ID)
		return reject(step, r, disposition)
	}

	// Step 2
	opts := normOptions(cust)
	called, err := numbering.Normalize(req.Called, opts)
	if err != nil {
		return rejectAdmitted("normalize", Reject{484, "Address Incomplete"}, model.DispositionRejectedRoute), nil
	}
	caller := numbering.NormalizeCaller(req.Caller, opts)
	cdr.CalledNumber = called
	cdr.CallerNumber = caller
	resp.Called = called
	resp.Caller = caller

	// Step 3
	sell, err := p.SellRateFor(ctx, cust, called)
	if err != nil {
		return nil, err
	}
	if sell == nil {
		return rejectAdmitted("rate", Reject{404, "No rate for destination"}, model.DispositionRejectedRoute), nil
	}
	sellID := sell.ID
	sellRate := sell.RatePerMin
	sellDest := sell.Destination
	cdr.SellRateID = &sellID
	cdr.SellRatePerMin = &sellRate
	cdr.SellDestination = &sellDest
	resp.Sell = &SellRate{RateID: sell.ID, Prefix: sell.Prefix, Destination: sell.Destination, RatePerMin: sell.RatePerMin, ConnectFee: sell.ConnectFee,
		InitialIncrement: sell.InitialIncrement, SubsequentIncrement: sell.SubsequentIncrement, MinDuration: sell.MinDuration}
	resp.Vars["sbc_sell_rate_id"] = sell.ID.String()
	resp.Vars["sbc_sell_rate"] = sell.RatePerMin.StringFixed(6)
	resp.Vars["sbc_sell_destination"] = sell.Destination

	// Steps 4 and 5
	rr, err := p.Reserve(ctx, cust, sell, cdr)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientFunds) {
			if rr != nil {
				resp.ReservedAmount = rr.ReservedAmount
				resp.Available = rr.Available
			}
			return rejectAdmitted("balance", Reject{402, "Not enough funds"}, model.DispositionRejectedBalance), nil
		}
		return nil, err
	}
	resp.ReservedAmount = rr.ReservedAmount
	resp.Available = rr.Available
	resp.MaxCallSeconds = rr.MaxCallSeconds
	resp.Vars["sbc_reserved"] = rr.ReservedAmount.StringFixed(6)
	resp.Vars["sbc_max_call_seconds"] = fmt.Sprint(rr.MaxCallSeconds)

	// A rejection after the reservation must release it.
	rejectReserved := func(step string, r Reject, disposition string) *SetupResponse {
		if err := p.ReleaseReservation(ctx, callUUID, r.Code, r.Reason, disposition); err != nil {
			log.Error("release reservation after reject", "error", err)
		}
		p.LeaveAdmission(ctx, cust.ID)
		resp.Reject = &r
		resp.RejectStep = step
		resp.Vars["sbc_reject_reason"] = r.Reason
		resp.Vars["sbc_reject_step"] = step
		log.Info("call rejected", "step", step, "code", r.Code, "reason", r.Reason, "called", called)
		return resp
	}

	// Steps 6 and 7
	route, err := p.Route(ctx, cust, called, caller, sell, req.CallUUID)
	if err != nil {
		return nil, err
	}
	resp.Skipped = route.Skipped
	if route.Route == nil || len(route.Carriers) == 0 {
		return rejectReserved("route", Reject{503, "No route"}, model.DispositionRejectedRoute), nil
	}
	rid := route.Route.ID
	resp.RouteID = &rid
	resp.RoutePrefix = route.Route.Prefix
	resp.Carriers = route.Carriers
	resp.Action = "dial"
	resp.Vars["sbc_route_id"] = rid.String()
	if p.rdb != nil {
		cache.Publish(ctx, p.rdb, cache.ChanCallStarted, req.CallUUID)
	}
	log.Info("call setup", "customer", cust.Name, "called", called, "sell_rate", sell.RatePerMin.StringFixed(6),
		"reserved", rr.ReservedAmount.StringFixed(6), "max_call_seconds", rr.MaxCallSeconds, "carriers", len(route.Carriers))
	return resp, nil
}

// ReleaseReservation gives the money back for a call that will not be
// dialled or that ended without a billable event, and finalises the CDR.
func (p *Pipeline) ReleaseReservation(ctx context.Context, callUUID uuid.UUID, sipCode int, reason, disposition string) error {
	now := p.now()
	node := p.cfg.NodeName
	err := p.st.WithTx(ctx, func(tx pgx.Tx) error {
		cdr, err := store.LockCDR(ctx, tx, callUUID)
		if err != nil {
			return err
		}
		if cdr.BilledAt != nil {
			return nil
		}
		ac, err := store.LockActiveCall(ctx, tx, callUUID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if ac != nil {
			if _, err := store.LockAccount(ctx, tx, ac.AccountID); err != nil {
				return err
			}
			if _, err := store.Release(ctx, tx, ac.AccountID, callUUID, ac.ReservedAmount, "release: "+reason); err != nil {
				return err
			}
			cdr.ReleasedAmount = ac.ReservedAmount
			if err := store.DeleteActiveCall(ctx, tx, callUUID); err != nil {
				return err
			}
		}
		cdr.EndTime = &now
		cdr.Duration = int(now.Sub(cdr.StartTime).Seconds())
		cdr.Disposition = disposition
		cdr.RejectReason = &reason
		cdr.SIPFinalCode = &sipCode
		cdr.SIPFinalReason = &reason
		cdr.BilledAt = &now
		cdr.BilledBy = &node
		return store.FinalizeCDR(ctx, tx, cdr)
	})
	if err != nil {
		return err
	}
	p.adm.Unreserve(ctx, callUUID)
	return nil
}

// ---- Step 8 support: attempt classification ----

// RecordAttempt stores the attempt in the CDR and classifies it.
func (p *Pipeline) RecordAttempt(ctx context.Context, req AttemptRequest) (*AttemptResponse, error) {
	callUUID, err := uuid.Parse(req.CallUUID)
	if err != nil {
		return nil, fmt.Errorf("bad call_uuid: %w", err)
	}
	var override []int
	var carrierName, gateway string
	if c, err := p.st.CarrierByID(ctx, req.CarrierID); err == nil {
		override = c.FailoverSIPCodes
		carrierName = c.Name
		gateway = c.GatewayName()
	}
	class := p.rules.Classify(failover.Attempt{
		SIPCode: req.SIPCode, HangupCause: req.HangupCause, Answered: req.Answered, ALegGone: req.ALegGone,
		RingSeconds: req.RingSeconds, CarrierOverride: override,
	})
	started := p.now()
	if req.StartedAt != nil {
		started = *req.StartedAt
	}
	rec := model.AttemptRecord{Seq: req.Seq, CarrierID: req.CarrierID, CarrierName: carrierName, Gateway: gateway, SIPCode: req.SIPCode,
		Reason: req.Reason, HangupCause: req.HangupCause, StartedAt: started, EndedAt: req.EndedAt, PDDMs: req.PDDMs, Classification: string(class),
		BuyRateID: req.BuyRateID, BuyRatePerMin: req.BuyRatePerMin}
	if err := p.st.AppendAttempt(ctx, callUUID, rec); err != nil {
		return nil, err
	}
	resp := &AttemptResponse{Classification: string(class), Continue: failover.Continue(class)}
	code := req.SIPCode
	reason := req.Reason
	if code == 0 {
		code, reason = failover.SIPCodeForCause(req.HangupCause)
	}
	if reason == "" {
		reason = failover.ReasonPhrase(code)
	}
	resp.RelayCode = code
	resp.RelayReason = reason
	logging.FromContext(ctx, p.log).Info("attempt", "seq", req.Seq, "carrier", carrierName, "sip_code", req.SIPCode,
		"cause", req.HangupCause, "classification", class, "continue", resp.Continue, "pdd_ms", req.PDDMs)
	return resp, nil
}
