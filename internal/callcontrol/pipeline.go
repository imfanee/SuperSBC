package callcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/config"
	"github.com/imfanee/supersbc/internal/failover"
	"github.com/imfanee/supersbc/internal/logging"
	"github.com/imfanee/supersbc/internal/metrics"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/numbering"
	"github.com/imfanee/supersbc/internal/quality"
	"github.com/imfanee/supersbc/internal/rating"
	"github.com/imfanee/supersbc/internal/stir"
	"github.com/imfanee/supersbc/internal/store"
	"github.com/imfanee/supersbc/internal/tables"
	"github.com/imfanee/supersbc/internal/timewindow"
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
	stir   *stir.Verifier
	// quality serves the per carrier and prefix scores for quality based routing (D-73); nil disables.
	quality *quality.Tracker
	// epHealth parks carrier endpoints that stop replying (D-75).
	epHealth *EndpointHealth
	now      func() time.Time

	breaker *Breaker
	rndMu   sync.Mutex
	rnd     *rand.Rand
}

// New wires the pipeline.
func New(cfg *config.Config, log *slog.Logger, st *store.Store, rdb *redis.Client, tb *tables.Tables, rules *failover.Rules) *Pipeline {
	return &Pipeline{
		cfg: cfg, log: log, st: st, rdb: rdb,
		adm: cache.NewAdmission(rdb), ips: cache.NewIPCache(rdb),
		tables: tb, rules: rules, health: noHealth{}, now: time.Now,
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec // weighted distribution, not security
	}
}

// SetBreaker installs the circuit breaker.
func (p *Pipeline) SetBreaker(b *Breaker) { p.breaker = b }

// Breaker exposes the circuit breaker (may be nil).
func (p *Pipeline) Breaker() *Breaker { return p.breaker }

// EndpointHealth returns the endpoint health tracker (D-75); nil when disabled.
func (p *Pipeline) EndpointHealth() *EndpointHealth { return p.epHealth }

// SetEndpointHealth installs the endpoint health tracker (D-75).
func (p *Pipeline) SetEndpointHealth(h *EndpointHealth) { p.epHealth = h }

// SetQuality installs the quality tracker (D-73).
func (p *Pipeline) SetQuality(q *quality.Tracker) { p.quality = q }

// SetSTIR installs the STIR/SHAKEN verifier (D-64); nil disables verification.
func (p *Pipeline) SetSTIR(v *stir.Verifier) { p.stir = v }

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
	// Detail explains this exact rejection to an operator (D-77): which limit
	// was hit, with the counter and the limit, or which address matched no
	// customer. The caller only ever sees Reject.
	Detail string
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
			return AuthResult{Reject: &Reject{403, "IP not authorized"}, Detail: unknownAddress(srcIP, srcPort, transport)}, nil
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
				return AuthResult{Reject: &Reject{403, "IP not authorized"}, Detail: unknownAddress(srcIP, srcPort, transport)}, nil
			}
			return AuthResult{}, fmt.Errorf("lookup customer by ip: %w", err)
		}
		cust = &m.Customer
		p.ips.Set(ctx, srcIP, srcPort, transport, cache.Entry{Found: true, CustomerID: cust.ID.String()})
	}
	if cust.Status != "active" {
		return AuthResult{Customer: cust, Reject: &Reject{403, "Customer suspended"},
			Detail: fmt.Sprintf("customer %s is %s", cust.Name, cust.Status)}, nil
	}
	res, err := p.adm.Admit(ctx, "customer", cust.ID, cust.MaxConcurrentCalls, cust.MaxCPS)
	if err != nil {
		return AuthResult{Customer: cust}, err // fail closed
	}
	if res.ConcurrentExceeded {
		return AuthResult{Customer: cust, Reject: &Reject{480, "Concurrent call limit"},
			Detail: fmt.Sprintf("customer %s already had %s up, its max_concurrent_calls is %d (max_cps is a separate limit)",
				cust.Name, calls(res.Concurrent-1), cust.MaxConcurrentCalls)}, nil
	}
	if res.CPSExceeded {
		return AuthResult{Customer: cust, Reject: &Reject{503, "CPS limit"},
			Detail: fmt.Sprintf("customer %s started %s in this second, its max_cps is %d",
				cust.Name, calls(res.CPS), cust.MaxCPS)}, nil
	}
	if p.cfg.Routing.GlobalMaxChannels > 0 || p.cfg.Routing.GlobalMaxCPS > 0 {
		g, err := p.adm.Admit(ctx, "global", uuid.Nil, p.cfg.Routing.GlobalMaxChannels, p.cfg.Routing.GlobalMaxCPS)
		if err != nil {
			p.adm.Leave(ctx, "customer", cust.ID)
			return AuthResult{Customer: cust}, err
		}
		if g.ConcurrentExceeded {
			p.adm.Leave(ctx, "customer", cust.ID)
			return AuthResult{Customer: cust, Reject: &Reject{480, "Concurrent call limit"},
				Detail: fmt.Sprintf("the node already had %s up across all customers, SBC_ROUTING_GLOBAL_MAX_CHANNELS is %d (customer %s was within its own limit)",
					calls(g.Concurrent-1), p.cfg.Routing.GlobalMaxChannels, cust.Name)}, nil
		}
		if g.CPSExceeded {
			p.adm.Leave(ctx, "customer", cust.ID)
			return AuthResult{Customer: cust, Reject: &Reject{503, "CPS limit"},
				Detail: fmt.Sprintf("the node took %s in this second across all customers, SBC_ROUTING_GLOBAL_MAX_CPS is %d (customer %s was within its own limit)",
					calls(g.CPS), p.cfg.Routing.GlobalMaxCPS, cust.Name)}, nil
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

// ConvertRate returns a copy of r with money converted from the rate deck
// currency into the account currency (D-45). ok is false when no exchange
// rate exists, which callers treat as "no rate for destination".
func (p *Pipeline) ConvertRate(ctx context.Context, r *model.Rate, accountCurrency string) (*model.Rate, decimal.Decimal, bool) {
	deck, err := p.st.RateGroupByID(ctx, r.RateGroupID)
	if err != nil {
		return r, decimal.NewFromInt(1), true
	}
	fx, ok := p.tables.FX(ctx, deck.Currency, accountCurrency)
	if !ok {
		return r, decimal.Zero, false
	}
	if fx.Equal(decimal.NewFromInt(1)) {
		return r, fx, true
	}
	c := *r
	c.RatePerMin = r.RatePerMin.Mul(fx).Round(rating.Scale)
	c.ConnectFee = r.ConnectFee.Mul(fx).Round(rating.Scale)
	return &c, fx, true
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
		// Per-call rows first; the account row lock (taken by the UPDATE in
		// Reserve) is held only for the ledger insert that follows it.
		cdr.ReservedAmount = reserve
		cdr.Disposition = model.DispositionPending
		if err := store.InsertCDRSetup(ctx, tx, cdr); err != nil {
			return err
		}
		if err := store.InsertActiveCall(ctx, tx, &model.ActiveCall{
			CallUUID: cdr.CallUUID, CustomerID: cust.ID, AccountID: acc.ID, CalledNumber: cdr.CalledNumber,
			ReservedAmount: reserve, MaxCallSeconds: maxSecs, StartedAt: cdr.StartTime,
			ExpiresAt: cdr.StartTime.Add(p.cfg.Billing.MaxCallDuration), Node: p.cfg.NodeName,
		}); err != nil {
			return err
		}
		locked, err := store.Reserve(ctx, tx, acc.ID, cdr.CallUUID, reserve, fmt.Sprintf("reserve %d min for %s", p.cfg.Billing.ReserveMinutes, cdr.CalledNumber))
		if err != nil {
			return err
		}
		res.Account = locked
		return nil
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
	Modes    model.RouteModes
	Carriers []CarrierChoice
	Skipped  []SkippedCarrier
}

// Route finds the route and builds the ordered carrier list with buy rates
// (Steps 6 and 7). Carriers are ordered by priority, ties by weight
// descending then random; degraded carriers go last; DOWN gateways and
// carriers without a buy rate are skipped.
func (p *Pipeline) Route(ctx context.Context, cust *model.Customer, called, caller string, sell *model.Rate, callUUID string) (*RouteResult, error) {
	return p.RouteWith(ctx, cust, called, caller, sell, callUUID, RouteOpts{})
}

// RouteOpts are per call flags that shape the carrier legs.
type RouteOpts struct {
	Privacy         bool   // the caller asked for privacy (D-54)
	ForwardIdentity bool   // pass the verified STIR Identity header to the carrier (D-64)
	OfferedCodecs   string // the a-leg ep_codec_string, so the carrier leg can relay instead of transcode (D-83)
}

// RouteWith is Route with the per call options.
func (p *Pipeline) RouteWith(ctx context.Context, cust *model.Customer, called, caller string, sell *model.Rate, callUUID string, ro RouteOpts) (*RouteResult, error) {
	privacy := ro.Privacy
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
	// Endpoints of every candidate carrier (D-75); a carrier without rows
	// falls back to its gateway columns as a single endpoint.
	allEndpoints, err := p.st.AllEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	res := &RouteResult{Route: route, Modes: route.Modes()}
	modes := route.Modes()
	type cand struct {
		choice CarrierChoice
		rc     model.RouteCarrier
		buy    *model.Rate
		eps    []model.CarrierEndpoint // dial order of this carrier's endpoints (D-75)
		only   bool                    // the carrier has exactly one endpoint: plain gateway name
		extra  []string                // per carrier dial string variables
	}
	var cands []cand
	for _, rc := range route.Carriers {
		c, ok := carriers[rc.CarrierID]
		if !ok || c.Status != "active" {
			name := c.Name
			if !ok {
				name = rc.CarrierID.String()
			}
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: rc.CarrierID, Name: name, Reason: "disabled",
				Detail: "the carrier is not active"})
			continue
		}
		eps := allEndpoints[c.ID]
		if len(eps) == 0 {
			eps = []model.CarrierEndpoint{{Seq: 1, Host: c.GatewayHost, Port: c.GatewayPort, Transport: c.Transport, Priority: 1, Weight: 100, Enabled: true}}
		}
		p.rndMu.Lock()
		usable := UsableEndpoints(c, eps, p.health.GatewayDown, p.epHealth.Dead(ctx, eps), p.cfg.Routing.MaxEndpointsPerCarrier, p.rnd)
		p.rndMu.Unlock()
		if len(usable) == 0 {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "gateway_down",
				Detail: fmt.Sprintf("all %d endpoints are down or parked", len(eps))})
			continue
		}
		// Time-of-day routing window of this route carrier (D-61).
		if win, err := timewindow.Parse(rc.Window); err != nil || !win.Contains(p.now()) {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "outside_window",
				Detail: "outside the routing window " + rc.Window})
			continue
		}
		// Carrier capacity (Section 7): skip a carrier already at its channel limit.
		if c.MaxConcurrentCalls > 0 {
			if n, err := p.adm.Concurrent(ctx, "carrier", c.ID); err == nil && n >= int64(c.MaxConcurrentCalls) {
				res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "carrier_capacity",
					Detail: fmt.Sprintf("%d calls in progress, limit %d", n, c.MaxConcurrentCalls)})
				continue
			}
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
			detail := "the carrier has no rate group"
			if c.RateGroupID != nil {
				detail = "no rate matches " + called + " in the carrier rate deck"
			}
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "skipped_no_rate", Detail: detail})
			continue
		}
		negative := sell != nil && buy.RatePerMin.GreaterThan(sell.RatePerMin)
		if negative && p.cfg.Routing.BlockNegativeMargin {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "negative_margin_blocked",
				Detail: marginDetail(buy, sell)})
			continue
		}
		// Lossless routing (D-73): the group refuses carriers that would lose money on this call.
		if negative && modes.Lossless {
			res.Skipped = append(res.Skipped, SkippedCarrier{CarrierID: c.ID, Name: c.Name, Reason: "lossless_skipped",
				Detail: marginDetail(buy, sell)})
			continue
		}
		dial := buildDialNumber(called, c)
		ani := c.ANIPrefix + caller
		rate := buy.RatePerMin
		rid := buy.ID
		ch := CarrierChoice{
			CarrierID: c.ID, Name: c.Name, Gateway: c.GatewayName(), Priority: rc.Priority, Weight: rc.Weight,
			DialNumber: dial, CallerID: ani, BuyRateID: &rid, BuyRatePerMin: &rate, BuyDestination: buy.Destination,
			NegativeMargin: negative, FailoverSIPCodes: c.FailoverSIPCodes, Codecs: codecStringFor(cust.AllowedCodecs, c.AllowedCodecs, ro.OfferedCodecs),
			IgnoreEarlyMedia: c.IgnoreEarlyMedia, Degraded: p.health.Degraded(c.ID),
			MediaMode: ResolveMediaMode(MediaModeFor(cust.MediaMode, c.MediaMode), ro.OfferedCodecs, cust.AllowedCodecs, c.AllowedCodecs),
		}
		var extra []string
		if privacy {
			ch.CallerID, extra = PrivacyVars(c.PrivacyMode, ani, p.cfg.FSNodeIP)
		}
		extra = append(extra, dtmfVars(c.DTMFMode)...)
		extra = append(extra, "rtp_secure_media="+srtpVar(c.SRTPMode))
		custRules, _ := p.tables.HeaderRules(ctx, "customer", cust.ID)
		carRules, _ := p.tables.HeaderRules(ctx, "carrier", c.ID)
		plan := ApplyHeaderRules(custRules, carRules, HeaderContext{Caller: caller, Called: called, Customer: cust.Name, Carrier: c.Name, CallUUID: callUUID, NodeIP: p.cfg.FSNodeIP})
		extra = append(extra, plan.EgressVars...)
		ch.Passthrough = plan.Passthrough
		if ro.ForwardIdentity && !slices.Contains(ch.Passthrough, "Identity") {
			ch.Passthrough = append(ch.Passthrough, "Identity")
		}
		if modes.Quality {
			q, _ := p.quality.Lookup(c.ID, route.Prefix)
			score := q.Score
			ch.Quality = &score
			ch.QualitySamples = q.Samples
		}
		if modes.Percent {
			ch.Share = rc.Weight
		}
		cands = append(cands, cand{choice: ch, rc: rc, buy: buy, eps: usable, only: len(eps) == 1, extra: extra})
	}
	// Order by the group's routing modes (D-73): priority and weighted
	// random by default, LCR by buy rate, quality tier first, or percent
	// distribution; degraded carriers always last.
	ord := make([]orderable, len(cands))
	for i, c := range cands {
		rate, _ := c.buy.RatePerMin.Float64()
		q := 0.5
		if c.choice.Quality != nil {
			q = qualityTier(*c.choice.Quality)
		}
		ord[i] = orderable{priority: c.rc.Priority, weight: c.rc.Weight, degraded: c.choice.Degraded, buyRate: rate, quality: q}
	}
	p.rndMu.Lock()
	order := orderCandidates(ord, modes, p.rnd)
	p.rndMu.Unlock()
	// Expand every carrier into its endpoints, keeping them together: the
	// bridge loop in Lua then fails over inside a carrier before moving to
	// the next one (D-75).
	seq := 0
	for _, i := range order {
		cd := cands[i]
		for _, e := range cd.eps {
			ch := cd.choice
			seq++
			ch.Seq = seq
			ch.Gateway = e.GatewayName(cd.choice.Name, cd.only)
			id := e.ID
			ch.EndpointID = &id
			ch.Endpoint = net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
			ch.DialString = p.dialString(ch, callUUID, cd.extra)
			res.Carriers = append(res.Carriers, ch)
		}
	}
	return res, nil
}

// BeginAttempt takes a concurrency and CPS slot on the carrier before Lua
// dials it. When the carrier is at capacity the attempt is refused and Lua
// moves to the next carrier without dialling.
func (p *Pipeline) BeginAttempt(ctx context.Context, carrierID uuid.UUID) (bool, string, error) {
	c, err := p.st.CarrierByID(ctx, carrierID)
	if err != nil {
		return false, "unknown_carrier", nil
	}
	res, err := p.adm.Admit(ctx, "carrier", c.ID, c.MaxConcurrentCalls, c.MaxCPS)
	if err != nil {
		return false, "admission_unavailable", err
	}
	if res.ConcurrentExceeded {
		return false, "carrier_capacity", nil
	}
	if res.CPSExceeded {
		return false, "carrier_cps", nil
	}
	return true, "", nil
}

func buildDialNumber(called string, c model.Carrier) string {
	n := called
	if c.StripDigits > 0 && c.StripDigits < len(n) {
		n = n[c.StripDigits:]
	}
	return c.DNIPrefix + n
}

// dialString builds the FreeSWITCH originate string for one carrier
// (Section 3 Step 8). Variables that belong to the a-leg (continue_on_fail,
// hangup_after_bridge) are set by Lua, not here.
func (p *Pipeline) dialString(c CarrierChoice, callUUID string, extra []string) string {
	vars := []string{
		"sip_h_X-SBC-Call=" + callUUID,
		"origination_caller_id_number=" + c.CallerID,
		"origination_caller_id_name=" + c.CallerID,
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
	// Codec policy belongs to the leg FreeSWITCH negotiates itself. When the
	// media is proxied or bypassed the SDP passes through untouched and the
	// endpoints agree between themselves, so pinning a list here would be a
	// lie (D-84).
	if c.MediaMode != "proxy" && c.MediaMode != "bypass" {
		vars = append(vars, "absolute_codec_string=^^:"+strings.ReplaceAll(c.Codecs, ",", ":"))
	}
	vars = append(vars, extra...)
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
	if req.SIPCallID != "" {
		cid := req.SIPCallID
		cdr.SIPCallID = &cid
	}

	reject := func(step string, r Reject, disposition, detail string) *SetupResponse {
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
		cdr.RejectStep = &step
		if detail != "" {
			cdr.RejectDetail = &detail
			resp.Vars["sbc_reject_detail"] = detail
		}
		cdr.SIPFinalCode = &code
		cdr.SIPFinalReason = &reason
		cdr.BilledAt = &end
		cdr.BilledBy = &node
		if err := store.InsertCDRSetup(ctx, p.st.Pool(), cdr); err != nil {
			log.Error("write rejected cdr", "error", err)
		}
		metrics.Setups.WithLabelValues(resp.CustomerName, "reject_"+step).Inc()
		metrics.Rejections.WithLabelValues(step, fmt.Sprint(r.Code)).Inc()
		log.Info("call rejected", "step", step, "code", r.Code, "reason", r.Reason, "detail", detail, "src_ip", req.SrcIP, "called", req.Called)
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
		resp.CustomerCodecs = strings.Join(auth.Customer.AllowedCodecs, ",")
	}
	if auth.Reject != nil {
		if auth.Reject.Reason == "IP not authorized" {
			p.countScanner(ctx, req.SrcIP)
		}
		return reject("authorize", *auth.Reject, model.DispositionRejectedAuth, auth.Detail), nil
	}
	cust := auth.Customer
	transport := strings.ToLower(req.Transport)
	if transport == "" {
		transport = "udp"
	}
	cdr.TransportIn = &transport
	cdr.SRTPIn = req.SRTPOffered
	cdr.Privacy = req.Privacy
	// From here on a concurrency slot is held; release it on every rejection.
	rejectAdmitted := func(step string, r Reject, disposition, detail string) *SetupResponse {
		p.LeaveAdmission(ctx, cust.ID)
		return reject(step, r, disposition, detail)
	}

	// Customer transport and media policies (Section 7 [M5]).
	if cust.RequireTLS && transport != "tls" {
		return rejectAdmitted("policy", Reject{403, "TLS required"}, model.DispositionRejectedAuth,
			"the customer requires TLS and the call arrived over "+transport), nil
	}
	if cust.SRTPMode == "mandatory" && !req.SRTPOffered {
		return rejectAdmitted("policy", Reject{488, "SRTP required"}, model.DispositionRejectedAuth,
			"the customer requires SRTP and the offer carried none"), nil
	}
	resp.Vars["rtp_secure_media"] = srtpVar(cust.SRTPMode)
	dtmfV, dtmfApps := ALegDTMF(cust.DTMFMode)
	for k, v := range dtmfV {
		resp.Vars[k] = v
	}
	resp.Apps = dtmfApps
	if req.Privacy {
		resp.Vars["sbc_privacy"] = "true"
	}

	// Step 2
	opts := normOptions(cust)
	called, err := numbering.Normalize(req.Called, opts)
	if err != nil {
		return rejectAdmitted("normalize", Reject{484, "Address Incomplete"}, model.DispositionRejectedRoute,
			fmt.Sprintf("cannot normalise %q: %v (country code %q, international prefix %q)", req.Called, err, opts.DefaultCountryCode, opts.IntlPrefix)), nil
	}
	caller := numbering.NormalizeCaller(req.Caller, opts)
	if cust.TrustPAI && req.PAINumber != "" {
		caller = numbering.NormalizeCaller(req.PAINumber, opts)
	}
	cdr.CalledNumber = called
	cdr.CallerNumber = caller
	resp.Called = called
	resp.Caller = caller
	// Area and operator of the called number (D-76); a number outside every
	// known range simply has none.
	if area, err := p.tables.Area(ctx, called); err == nil && area != nil {
		pfx, desc, op := area.Prefix, area.Description, area.Operator
		cdr.AreaPrefix, cdr.AreaDescription, cdr.OperatorName = &pfx, &desc, &op
		resp.Vars["sbc_area"] = desc
		if op != "" {
			resp.Vars["sbc_operator"] = op
		}
	}

	// STIR/SHAKEN (D-64): verify the Identity header when the customer asks for it.
	forwardIdentity := false
	if p.stir != nil && cust.STIRMode != "" && cust.STIRMode != "ignore" {
		res := p.stir.Verify(ctx, req.Identity, caller, called)
		status := res.Status
		cdr.STIRStatus = &status
		if res.Attest != "" {
			attest := res.Attest
			cdr.STIRAttest = &attest
		}
		resp.Vars["sbc_stir_status"] = status
		if res.Status != stir.StatusVerified {
			log.Info("stir verification", "status", res.Status, "attest", res.Attest, "error", res.Error, "mode", cust.STIRMode)
		}
		metrics.STIR.WithLabelValues(res.Status).Inc()
		if cust.STIRMode == "require" && res.Status != stir.StatusVerified {
			code, reason := res.RejectCode()
			return rejectAdmitted("stir", Reject{code, reason}, model.DispositionRejectedAuth,
				fmt.Sprintf("the customer requires STIR and verification returned %s", res.Status)), nil
		}
		forwardIdentity = res.Status == stir.StatusVerified && p.cfg.STIR.Forward
	}

	// Step 2b: block lists (Section 7, routing and policy). D-44.
	if cust.BlockedPrefixesEnabled {
		if blk, err := p.tables.Blocked(ctx, cust.ID, called); err == nil && blk != nil {
			log.Info("destination blocked", "prefix", blk.Prefix, "reason", blk.Reason, "global", blk.CustomerID == nil)
			scope := "this customer"
			if blk.CustomerID == nil {
				scope = "every customer"
			}
			return rejectAdmitted("blocklist", Reject{403, "Destination blocked"}, model.DispositionRejectedRoute,
				fmt.Sprintf("prefix %s is blocked for %s: %s", blk.Prefix, scope, blk.Reason)), nil
		}
	}

	// Step 3
	sell, err := p.SellRateFor(ctx, cust, called)
	if err != nil {
		return nil, err
	}
	if sell == nil {
		detail := "no rate matches " + called + " in the customer rate deck"
		if cust.RateGroupID == nil {
			detail = "the customer has no rate group"
		}
		return rejectAdmitted("rate", Reject{404, "No rate for destination"}, model.DispositionRejectedRoute, detail), nil
	}
	// Multi-currency (D-45): rate in the customer's account currency.
	acctCurrency := p.cfg.Billing.Currency
	if acc, err := p.st.AccountByOwner(ctx, "customer", cust.ID); err == nil {
		acctCurrency = acc.Currency
	}
	sellConv, sellFX, fxOK := p.ConvertRate(ctx, sell, acctCurrency)
	if !fxOK {
		log.Error("no exchange rate for sell deck", "rate_group_id", sell.RateGroupID, "account_currency", acctCurrency)
		return rejectAdmitted("rate", Reject{404, "No rate for destination"}, model.DispositionRejectedRoute,
			"no exchange rate from the sell deck currency to the account currency "+acctCurrency), nil
	}
	sell = sellConv
	cdr.SellCurrency = &acctCurrency
	cdr.SellFX = sellFX
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
			metrics.ReservationFailures.WithLabelValues(cust.Name).Inc()
			detail := "the account has no funds for the reservation"
			if rr != nil {
				detail = fmt.Sprintf("available %s %s is below the %s needed to reserve %d minutes at %s per minute",
					rr.Available.StringFixed(6), acctCurrency, rr.ReservedAmount.StringFixed(6),
					p.cfg.Billing.ReserveMinutes, sell.RatePerMin.StringFixed(6))
			}
			return rejectAdmitted("balance", Reject{402, "Not enough funds"}, model.DispositionRejectedBalance, detail), nil
		}
		return nil, err
	}
	resp.ReservedAmount = rr.ReservedAmount
	resp.Available = rr.Available
	resp.MaxCallSeconds = rr.MaxCallSeconds
	resp.Vars["sbc_reserved"] = rr.ReservedAmount.StringFixed(6)
	resp.Vars["sbc_max_call_seconds"] = fmt.Sprint(rr.MaxCallSeconds)

	// A rejection after the reservation must release it.
	rejectReserved := func(step string, r Reject, disposition, detail string) *SetupResponse {
		if err := p.ReleaseReservation(ctx, callUUID, r.Code, r.Reason, disposition, step, detail); err != nil {
			log.Error("release reservation after reject", "error", err)
		}
		p.LeaveAdmission(ctx, cust.ID)
		resp.Reject = &r
		resp.RejectStep = step
		resp.Vars["sbc_reject_reason"] = r.Reason
		resp.Vars["sbc_reject_step"] = step
		if detail != "" {
			resp.Vars["sbc_reject_detail"] = detail
		}
		metrics.Setups.WithLabelValues(cust.Name, "reject_"+step).Inc()
		metrics.Rejections.WithLabelValues(step, fmt.Sprint(r.Code)).Inc()
		log.Info("call rejected", "step", step, "code", r.Code, "reason", r.Reason, "detail", detail, "called", called)
		return resp
	}

	// Steps 6 and 7
	route, err := p.RouteWith(ctx, cust, called, caller, sell, req.CallUUID, RouteOpts{Privacy: req.Privacy, ForwardIdentity: forwardIdentity, OfferedCodecs: req.OfferedCodecs})
	if err != nil {
		return nil, err
	}
	resp.Skipped = route.Skipped
	// Response header rules (customer side) apply to what the customer receives.
	if custRules, err := p.tables.HeaderRules(ctx, "customer", cust.ID); err == nil {
		plan := ApplyHeaderRules(custRules, nil, HeaderContext{Caller: caller, Called: called, Customer: cust.Name, CallUUID: req.CallUUID, NodeIP: p.cfg.FSNodeIP})
		for k, v := range plan.ResponseVars {
			resp.Vars[k] = v
		}
	}
	if route.Route == nil || len(route.Carriers) == 0 {
		return rejectReserved("route", Reject{503, "No route"}, model.DispositionRejectedRoute,
			noRouteDetail(cust, called, route)), nil
	}
	rid := route.Route.ID
	resp.RouteID = &rid
	resp.RoutePrefix = route.Route.Prefix
	resp.Carriers = route.Carriers
	resp.Action = "dial"
	resp.Vars["sbc_route_id"] = rid.String()
	resp.Vars["sbc_route_prefix"] = route.Route.Prefix
	if p.rdb != nil {
		cache.Publish(ctx, p.rdb, cache.ChanCallStarted, req.CallUUID)
	}
	metrics.Setups.WithLabelValues(cust.Name, "dial").Inc()
	log.Info("call setup", "customer", cust.Name, "called", called, "sell_rate", sell.RatePerMin.StringFixed(6),
		"reserved", rr.ReservedAmount.StringFixed(6), "max_call_seconds", rr.MaxCallSeconds, "carriers", len(route.Carriers))
	return resp, nil
}

// countScanner counts unauthorised INVITEs per source address and bans the
// address when the threshold is crossed (Section 7, scanner protection).
func (p *Pipeline) countScanner(ctx context.Context, ip string) {
	if p.cfg.Ban.Threshold <= 0 || p.rdb == nil || ip == "" {
		return
	}
	key := "scan:" + ip
	n, err := p.rdb.Incr(ctx, key).Result()
	if err != nil {
		return
	}
	if n == 1 {
		_ = p.rdb.Expire(ctx, key, p.cfg.Ban.Window).Err()
	}
	if n < int64(p.cfg.Ban.Threshold) {
		return
	}
	exp := time.Now().Add(p.cfg.Ban.Duration)
	if _, err := p.st.Ban(ctx, ip, fmt.Sprintf("%d unauthorised INVITEs in %s", n, p.cfg.Ban.Window), int(n), false, &exp); err != nil {
		p.log.Warn("ban failed", "ip", ip, "error", err)
		return
	}
	_ = p.rdb.Del(ctx, key).Err()
	p.log.Warn("source address banned", "ip", ip, "hits", n, "until", exp)
	metrics.Bans.WithLabelValues("auto").Inc()
	cache.Publish(ctx, p.rdb, cache.ChanBansChanged, ip)
}

// ReleaseReservation gives the money back for a call that will not be
// dialled or that ended without a billable event, and finalises the CDR.
func (p *Pipeline) ReleaseReservation(ctx context.Context, callUUID uuid.UUID, sipCode int, reason, disposition, step, detail string) error {
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
		if step != "" {
			cdr.RejectStep = &step
		}
		if detail != "" {
			cdr.RejectDetail = &detail
		}
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
		BuyRateID: req.BuyRateID, BuyRatePerMin: req.BuyRatePerMin, Endpoint: req.Endpoint}
	if err := p.st.AppendAttempt(ctx, callUUID, rec); err != nil {
		return nil, err
	}
	// The carrier slot taken by BeginAttempt is released here for failed
	// attempts; answered calls release it when billed.
	if !req.Answered {
		p.adm.Leave(ctx, "carrier", req.CarrierID)
	}
	// Endpoint health (D-75): with call based parking enabled, attempts that
	// never reached the far end count against the address and enough of them
	// in a row park it for the carrier's retry period.
	if p.epHealth != nil && req.EndpointID != nil {
		if record, usable := EndpointOutcome(class, req.Answered); record {
			if c, err := p.st.CarrierByID(ctx, req.CarrierID); err == nil && c.EndpointParkOnFailure && !c.SIPOptionsPing {
				p.epHealth.RecordAttempt(ctx, *req.EndpointID, usable, c.EndpointFailThreshold, c.EndpointRetrySeconds)
			}
		}
	}
	degraded := false
	if p.quality != nil {
		pdd := 0
		if req.PDDMs != nil {
			pdd = *req.PDDMs
		}
		p.quality.Record(ctx, req.CarrierID, req.RoutePrefix, quality.Outcome{
			Answered: class == failover.Answered,
			// NER: the network did its job when the callee side decided (answered, busy/number faults, caller gave up)
			Reached: class == failover.Answered || class == failover.NumberFault || class == failover.Cancelled,
			Fault:   class == failover.CarrierFault,
			PDDMs:   pdd,
		})
	}
	if p.breaker != nil {
		degraded = p.breaker.Record(ctx, req.CarrierID, class)
		if degraded {
			metrics.CarrierDegraded.WithLabelValues(carrierName).Set(1)
		}
	}
	metrics.Attempts.WithLabelValues(carrierName, string(class)).Inc()
	if req.SIPCode > 0 {
		metrics.AttemptSIPCodes.WithLabelValues(carrierName, fmt.Sprint(req.SIPCode)).Inc()
	}
	if req.PDDMs != nil {
		metrics.PDD.WithLabelValues(carrierName).Observe(float64(*req.PDDMs) / 1000)
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
		"cause", req.HangupCause, "classification", class, "continue", resp.Continue, "pdd_ms", req.PDDMs, "carrier_degraded", degraded)
	return resp, nil
}

// marginDetail states the two rates behind a margin skip, which is what an
// operator has to change to let the call through (D-77).
func marginDetail(buy, sell *model.Rate) string {
	if buy == nil || sell == nil {
		return ""
	}
	return fmt.Sprintf("buy %s on %s is above sell %s on %s", buy.RatePerMin.StringFixed(6), buy.Prefix,
		sell.RatePerMin.StringFixed(6), sell.Prefix)
}

// noRouteDetail says why a call found no carrier: either no route prefix
// matched, or every carrier on the matching route was skipped and for what
// reason, so an operator can see what to change (D-77).
func noRouteDetail(cust *model.Customer, called string, route *RouteResult) string {
	if route == nil || route.Route == nil {
		if cust.RouteGroupID == nil {
			return "the customer has no route group"
		}
		return "no route prefix matches " + called + " in the customer route group"
	}
	r := route.Route
	if len(route.Skipped) == 0 {
		return fmt.Sprintf("route %s (%s) has no carrier attached", r.Prefix, r.Destination)
	}
	parts := make([]string, 0, len(route.Skipped))
	for _, s := range route.Skipped {
		if s.Detail != "" {
			parts = append(parts, fmt.Sprintf("%s %s: %s", s.Name, s.Reason, s.Detail))
			continue
		}
		parts = append(parts, s.Name+" "+s.Reason)
	}
	return fmt.Sprintf("every carrier on route %s (%s) was skipped: %s", r.Prefix, r.Destination, strings.Join(parts, "; "))
}

// unknownAddress is the rejection detail when a source address matches no
// customer: the exact three values that have to be in an allowed IP (D-77).
func unknownAddress(srcIP string, srcPort int, transport string) string {
	return fmt.Sprintf("no active customer has %s:%d over %s in its allowed IPs", srcIP, srcPort, transport)
}

// calls counts calls in a rejection detail, singular or plural.
func calls(n int64) string {
	if n == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", n)
}
