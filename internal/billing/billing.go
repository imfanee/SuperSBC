package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/config"
	"github.com/imfanee/supersbc/internal/logging"
	"github.com/imfanee/supersbc/internal/metrics"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/rating"
	"github.com/imfanee/supersbc/internal/store"
)

// FXSource resolves exchange rates (implemented by tables.Tables).
type FXSource interface {
	FX(ctx context.Context, base, quote string) (decimal.Decimal, bool)
}

// Engine bills finished calls.
type Engine struct {
	cfg *config.Config
	log *slog.Logger
	st  *store.Store
	rdb *redis.Client
	adm *cache.Admission
	now func() time.Time
	fx  FXSource
}

// SetFX installs the exchange rate source.
func (e *Engine) SetFX(f FXSource) { e.fx = f }

// New creates the billing engine.
func New(cfg *config.Config, log *slog.Logger, st *store.Store, rdb *redis.Client) *Engine {
	return &Engine{cfg: cfg, log: log, st: st, rdb: rdb, adm: cache.NewAdmission(rdb), now: time.Now}
}

// Outcome summarises what Bill did.
type Outcome struct {
	AlreadyBilled bool
	NoCDR         bool
	Disposition   string
	Billsec       int
	BilledSeconds int
	Price         decimal.Decimal
	Cost          decimal.Decimal
	Released      decimal.Decimal
	CustomerID    *uuid.UUID
	Available     *decimal.Decimal
	CarrierID     *uuid.UUID
	MediaMode     string
	CustomerName  string
	CarrierName   string
}

// Bill applies Section 5 in one transaction. It is idempotent on call_uuid:
// the CDR row is locked and billed_at is the guard (D-17).
func (e *Engine) Bill(ctx context.Context, h HangupInfo) (*Outcome, error) {
	log := logging.FromContext(ctx, e.log).With("call_uuid", h.CallUUID, "source", h.Source)
	out := &Outcome{}
	now := e.now()
	node := e.cfg.NodeName
	var lowBalance *model.Account
	err := e.st.WithTx(ctx, func(tx pgx.Tx) error {
		cdr, err := store.LockCDR(ctx, tx, h.CallUUID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				out.NoCDR = true
				return nil
			}
			return err
		}
		if cdr.BilledAt != nil {
			out.AlreadyBilled = true
			out.Disposition = cdr.Disposition
			out.CustomerID = cdr.CustomerID
			return nil
		}
		out.CustomerID = cdr.CustomerID
		ac, err := store.LockActiveCall(ctx, tx, h.CallUUID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}

		// Timing
		cdr.EndTime = &h.EndTime
		if h.ProgressTime != nil {
			cdr.ProgressTime = h.ProgressTime
		}
		if h.AnswerTime != nil {
			cdr.AnswerTime = h.AnswerTime
		}
		cdr.Billsec = h.Billsec
		if cdr.Billsec < 0 {
			cdr.Billsec = 0
		}
		cdr.Duration = h.Duration
		if cdr.Duration == 0 {
			cdr.Duration = int(h.EndTime.Sub(cdr.StartTime).Seconds())
		}
		if h.AnswerTime != nil {
			ringStart := cdr.StartTime
			if cdr.ProgressTime != nil {
				ringStart = *cdr.ProgressTime
			}
			cdr.RingSeconds = int(h.AnswerTime.Sub(ringStart).Seconds())
		} else if cdr.ProgressTime != nil {
			cdr.RingSeconds = int(h.EndTime.Sub(*cdr.ProgressTime).Seconds())
		}
		fillAttemptPDD(cdr.Attempts, cdr.ProgressTime, cdr.AnswerTime)
		if cdr.PDDMs == nil {
			cdr.PDDMs = callPDD(cdr.Attempts, cdr.StartTime, cdr.ProgressTime, cdr.AnswerTime)
		}
		cause := h.HangupCause
		cdr.HangupCause = &cause
		if h.SIPCode > 0 {
			code := h.SIPCode
			reason := h.SIPReason
			cdr.SIPFinalCode = &code
			cdr.SIPFinalReason = &reason
		}
		if h.RejectReason != "" && cdr.RejectReason == nil {
			r := h.RejectReason
			cdr.RejectReason = &r
		}
		if h.CodecIn != "" {
			ci := h.CodecIn
			cdr.CodecIn = &ci
		}
		if h.RTPStats != nil {
			cdr.RTPStats = h.RTPStats
		}
		if cdr.CodecIn != nil && cdr.CodecOut != nil {
			mm := "relay"
			if !strings.EqualFold(*cdr.CodecIn, *cdr.CodecOut) {
				mm = "transcode"
			}
			cdr.MediaMode = &mm
		}
		if h.MediaMode != "" {
			mm := h.MediaMode
			cdr.MediaMode = &mm
		}
		if h.SRTP {
			cdr.SRTPIn = true
		}
		cdr.Disposition = Disposition(h.AnswerTime != nil, cause, h.SIPCode)
		cdr.SBCNode = &node
		cdr.BilledAt = &now
		cdr.BilledBy = &node

		// Money
		var price, cost decimal.Decimal
		if h.AnswerTime != nil && cdr.SellRateID != nil {
			sell, err := store.RateByIDQ(ctx, tx, *cdr.SellRateID)
			if err != nil {
				return fmt.Errorf("load sell rate: %w", err)
			}
			billed, amt := rating.Price(cdr.Billsec, toRatingFX(sell, cdr.SellFX))
			cdr.SellBilledSeconds = billed
			cdr.SellPrice = amt
			price = amt
			// Carrier cost: the answering carrier's buy rate from the attempts.
			if h.AnsweredCarrierID != nil {
				cid := *h.AnsweredCarrierID
				cdr.CarrierID = &cid
				if buyID := buyRateFor(cdr.Attempts, cid); buyID != nil {
					buy, err := store.RateByIDQ(ctx, tx, *buyID)
					if err == nil {
						buyFX, buyCurrency := e.buyFX(ctx, tx, buy, cid)
						cdr.BuyFX = buyFX
						cdr.BuyCurrency = &buyCurrency
						bb, bamt := rating.Price(cdr.Billsec, toRatingFX(buy, buyFX))
						cdr.BuyRateID = buyID
						rate := buy.RatePerMin.Mul(buyFX).Round(rating.Scale)
						cdr.BuyRatePerMin = &rate
						cdr.BuyBilledSeconds = bb
						cdr.Cost = bamt
						cost = bamt
						cdr.NegativeMargin = bamt.GreaterThan(amt)
					}
				}
			}
		} else if h.AnsweredCarrierID != nil {
			cid := *h.AnsweredCarrierID
			cdr.CarrierID = &cid
		}
		if cdr.CarrierID != nil {
			if c, err := e.st.CarrierByID(ctx, *cdr.CarrierID); err == nil {
				tr := c.Transport
				cdr.TransportOut = &tr
			}
		}
		// Failed attempt charging (D-62): carriers that bill a connect fee for
		// every attempt, answered or not.
		attemptFees := e.failedAttemptFees(ctx, tx, cdr)
		for _, f := range attemptFees {
			cdr.Cost = cdr.Cost.Add(f.amount)
		}
		out.Billsec = cdr.Billsec
		out.BilledSeconds = cdr.SellBilledSeconds
		out.Price = price
		out.Cost = cost
		out.Disposition = cdr.Disposition
		out.CarrierID = cdr.CarrierID
		if cdr.MediaMode != nil {
			out.MediaMode = *cdr.MediaMode
		}

		// Per-call rows first, then the hot account rows last so their locks
		// are held for the shortest possible time before commit.
		if ac != nil {
			cdr.ReleasedAmount = ac.ReservedAmount
			out.Released = ac.ReservedAmount
			if price.GreaterThan(decimal.Zero) {
				cdr.ChargedAmount = price
			}
			if err := store.DeleteActiveCall(ctx, tx, h.CallUUID); err != nil {
				return err
			}
		}
		if err := store.FinalizeCDR(ctx, tx, cdr); err != nil {
			return err
		}
		if cost.GreaterThan(decimal.Zero) && cdr.CarrierID != nil {
			cacc, err := store.AccountByOwnerQ(ctx, tx, "carrier", *cdr.CarrierID)
			if err == nil {
				desc := fmt.Sprintf("cost of call to %s, %ds billed as %ds", cdr.CalledNumber, cdr.Billsec, cdr.BuyBilledSeconds)
				// Post is an atomic UPDATE ... RETURNING; no separate lock needed.
				if _, err := store.Post(ctx, tx, cacc.ID, &h.CallUUID, model.LedgerCost, cost.Neg(), desc, nil); err != nil {
					return err
				}
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		for _, f := range attemptFees {
			cacc, err := store.AccountByOwnerQ(ctx, tx, "carrier", f.carrierID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			desc := fmt.Sprintf("connect fee of failed attempt %d to %s (%d, %s)", f.seq, cdr.CalledNumber, f.sipCode, f.reason)
			if _, err := store.Post(ctx, tx, cacc.ID, &h.CallUUID, model.LedgerCost, f.amount.Neg(), desc, nil); err != nil {
				return err
			}
		}
		if ac != nil {
			acc, err := store.LockAccount(ctx, tx, ac.AccountID)
			if err != nil {
				return err
			}
			if _, err := store.Release(ctx, tx, acc.ID, h.CallUUID, ac.ReservedAmount, "release reservation"); err != nil {
				return err
			}
			if price.GreaterThan(decimal.Zero) {
				desc := fmt.Sprintf("call to %s, %ds billed as %ds", cdr.CalledNumber, cdr.Billsec, cdr.SellBilledSeconds)
				acc, err = store.Post(ctx, tx, acc.ID, &h.CallUUID, model.LedgerCharge, price.Neg(), desc, nil)
				if err != nil {
					return err
				}
			}
			av := acc.Available()
			out.Available = &av
			if threshold, err := decimal.NewFromString(e.cfg.Billing.LowBalanceThreshold); err == nil && threshold.GreaterThan(decimal.Zero) && av.LessThan(threshold) {
				lowBalance = acc
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.NoCDR {
		log.Warn("hangup for unknown call, no cdr row")
		return out, nil
	}
	if out.AlreadyBilled {
		log.Debug("already billed")
		return out, nil
	}
	e.adm.Unreserve(ctx, h.CallUUID)
	if out.CustomerID != nil {
		e.adm.Leave(ctx, "customer", *out.CustomerID)
		if e.cfg.Routing.GlobalMaxChannels > 0 || e.cfg.Routing.GlobalMaxCPS > 0 {
			e.adm.Leave(ctx, "global", uuid.Nil)
		}
	}
	if h.AnsweredCarrierID != nil {
		e.adm.Leave(ctx, "carrier", *h.AnsweredCarrierID)
	}
	e.observe(ctx, out)
	if lowBalance != nil {
		_, _ = e.st.Pool().Exec(ctx, `INSERT INTO notifications (kind, owner_type, owner_id, payload) VALUES ('low_balance', 'customer', $1, jsonb_build_object('available', $2::text, 'threshold', $3::text))`,
			lowBalance.OwnerID, lowBalance.Available().StringFixed(6), e.cfg.Billing.LowBalanceThreshold)
	}
	cache.Publish(ctx, e.rdb, cache.ChanCDRCompleted, h.CallUUID.String())
	log.Info("billed", "disposition", out.Disposition, "billsec", out.Billsec, "billed_seconds", out.BilledSeconds,
		"price", out.Price.StringFixed(6), "cost", out.Cost.StringFixed(6), "released", out.Released.StringFixed(6), "cause", h.HangupCause, "sip_code", h.SIPCode)
	return out, nil
}

// RecordBLeg stores what the carrier leg negotiated (codec, RTP statistics)
// on the a-leg CDR, keyed by the X-SBC-Call header we sent. It runs
// independently of Bill: whichever comes second completes media_mode.
func (e *Engine) RecordBLeg(ctx context.Context, aLegUUID uuid.UUID, codec string, stats map[string]any, srtp bool) error {
	var statsJSON any
	if stats != nil {
		b, _ := json.Marshal(stats)
		statsJSON = string(b)
	}
	_, err := e.st.Pool().Exec(ctx, `
		UPDATE cdrs SET
		  codec_out = NULLIF($2, ''),
		  media_mode = CASE WHEN media_mode IN ('bypass', 'proxy') THEN media_mode WHEN codec_in IS NULL OR $2 = '' THEN media_mode WHEN upper(codec_in) = upper($2) THEN 'relay' ELSE 'transcode' END,
		  rtp_stats = CASE WHEN $3::jsonb IS NULL THEN rtp_stats ELSE COALESCE(rtp_stats, '{}'::jsonb) || jsonb_build_object('carrier', $3::jsonb) END,
		  srtp_out = srtp_out OR $4
		WHERE call_uuid = $1`, aLegUUID, codec, statsJSON, srtp)
	return err
}

// observe records the Prometheus counters for a billed call.
func (e *Engine) observe(ctx context.Context, out *Outcome) {
	if out.CustomerID != nil {
		if c, err := e.st.CustomerByID(ctx, *out.CustomerID); err == nil {
			out.CustomerName = c.Name
		}
	}
	if out.CarrierID != nil {
		if c, err := e.st.CarrierByID(ctx, *out.CarrierID); err == nil {
			out.CarrierName = c.Name
		}
	}
	metrics.Billed.WithLabelValues(out.CustomerName, out.CarrierName, out.Disposition).Inc()
	if out.Billsec > 0 {
		metrics.Billsec.WithLabelValues(out.CustomerName, out.CarrierName).Add(float64(out.Billsec))
	}
	if p, _ := out.Price.Float64(); p > 0 {
		metrics.Revenue.WithLabelValues(out.CustomerName).Add(p)
	}
	if c, _ := out.Cost.Float64(); c > 0 {
		metrics.Cost.WithLabelValues(out.CarrierName).Add(c)
	}
	if out.MediaMode != "" {
		metrics.MediaMode.WithLabelValues(out.MediaMode).Inc()
	}
}

// Disposition derives the CDR disposition from the outcome.
func Disposition(answered bool, cause string, sipCode int) string {
	if answered {
		return model.DispositionAnswered
	}
	c := strings.ToUpper(cause)
	switch {
	case c == "ORIGINATOR_CANCEL" || sipCode == 487:
		return model.DispositionCancelled
	case c == "USER_BUSY" || sipCode == 486 || sipCode == 600:
		return model.DispositionBusy
	case c == "NO_ANSWER" || c == "NO_USER_RESPONSE" || sipCode == 480 || sipCode == 408:
		return model.DispositionNoAnswer
	}
	return model.DispositionFailed
}

// toRatingFX converts the deck money into the account currency (D-45).
func toRatingFX(r *model.Rate, fx decimal.Decimal) rating.Rate {
	if fx.IsZero() {
		fx = decimal.NewFromInt(1)
	}
	return rating.Rate{PerMinute: r.RatePerMin.Mul(fx).Round(rating.Scale), ConnectFee: r.ConnectFee.Mul(fx).Round(rating.Scale),
		Increments: rating.Increments{Initial: r.InitialIncrement, Subsequent: r.SubsequentIncrement, MinDuration: r.MinDuration}}
}

// buyFX resolves the exchange rate between the carrier's rate deck and its
// account currency at billing time (a small drift from setup is accepted).
func (e *Engine) buyFX(ctx context.Context, tx pgx.Tx, buy *model.Rate, carrierID uuid.UUID) (decimal.Decimal, string) {
	one := decimal.NewFromInt(1)
	deck, err := store.RateGroupByIDQ(ctx, tx, buy.RateGroupID)
	if err != nil {
		return one, e.cfg.Billing.Currency
	}
	acc, err := store.AccountByOwnerQ(ctx, tx, "carrier", carrierID)
	if err != nil {
		return one, deck.Currency
	}
	if e.fx == nil {
		return one, acc.Currency
	}
	fx, ok := e.fx.FX(ctx, deck.Currency, acc.Currency)
	if !ok {
		e.log.Error("no exchange rate for buy deck, costing at 1:1", "rate_group_id", buy.RateGroupID, "carrier_id", carrierID)
		return one, acc.Currency
	}
	return fx, acc.Currency
}

type attemptFee struct {
	seq       int
	carrierID uuid.UUID
	sipCode   int
	reason    string
	amount    decimal.Decimal
}

// failedAttemptFees returns, for every attempt that did not answer and whose
// carrier has charge_failed_attempts, the connect fee of the buy rate the
// attempt was routed with (in the carrier account currency). The fee is
// recorded on the attempt so the CDR shows where the cost came from.
func (e *Engine) failedAttemptFees(ctx context.Context, tx pgx.Tx, cdr *model.CDR) []attemptFee {
	var fees []attemptFee
	charge := map[uuid.UUID]bool{}
	for i := range cdr.Attempts {
		at := &cdr.Attempts[i]
		if at.Classification == "answered" || at.BuyRateID == nil {
			continue
		}
		on, seen := charge[at.CarrierID]
		if !seen {
			c, err := store.CarrierByIDQ(ctx, tx, at.CarrierID)
			on = err == nil && c.ChargeFailedAttempts
			charge[at.CarrierID] = on
		}
		if !on {
			continue
		}
		buy, err := store.RateByIDQ(ctx, tx, *at.BuyRateID)
		if err != nil || !buy.ConnectFee.IsPositive() {
			continue
		}
		fx, _ := e.buyFX(ctx, tx, buy, at.CarrierID)
		amt := buy.ConnectFee.Mul(fx).Round(rating.Scale)
		s := amt.StringFixed(6)
		at.Cost = &s
		fees = append(fees, attemptFee{seq: at.Seq, carrierID: at.CarrierID, sipCode: at.SIPCode, reason: at.HangupCause, amount: amt})
	}
	return fees
}

func buyRateFor(attempts []model.AttemptRecord, carrierID uuid.UUID) *uuid.UUID {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].CarrierID == carrierID && attempts[i].BuyRateID != nil {
			return attempts[i].BuyRateID
		}
	}
	return nil
}

// fillAttemptPDD completes per-attempt PDD for attempts Lua could not time
// (the answered attempt: the channel is gone when bridge returns, D-29).
// The a-leg's first progress or answer timestamp is attributed to the attempt
// whose window contains it.
func fillAttemptPDD(attempts []model.AttemptRecord, progress, answer *time.Time) {
	for i := range attempts {
		a := &attempts[i]
		if a.PDDMs != nil || a.EndedAt == nil {
			continue
		}
		for _, t := range []*time.Time{progress, answer} {
			if t == nil {
				continue
			}
			if !t.Before(a.StartedAt) && !t.After(a.EndedAt.Add(time.Second)) {
				ms := int(t.Sub(a.StartedAt).Milliseconds())
				if ms < 0 {
					ms = 0
				}
				a.PDDMs = &ms
				break
			}
		}
	}
}

// callPDD is the CDR-level PDD: the answering attempt's PDD when answered,
// otherwise the last attempt that produced one, otherwise the customer
// perceived value (INVITE to first provisional response).
func callPDD(attempts []model.AttemptRecord, start time.Time, progress, answer *time.Time) *int {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Classification == "answered" && attempts[i].PDDMs != nil {
			return attempts[i].PDDMs
		}
	}
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].PDDMs != nil {
			return attempts[i].PDDMs
		}
	}
	for _, t := range []*time.Time{progress, answer} {
		if t != nil && t.After(start) {
			ms := int(t.Sub(start).Milliseconds())
			return &ms
		}
	}
	return nil
}
