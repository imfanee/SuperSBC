package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/model"
)

func (h *Handler) mountCarriers(r chi.Router) {
	r.Get("/carriers", h.listCarriers)
	r.With(operators).Post("/carriers", h.createCarrier)
	r.Get("/carriers/{id}", h.getCarrier)
	r.With(operators).Put("/carriers/{id}", h.updateCarrier)
	r.With(operators).Delete("/carriers/{id}", h.deleteCarrier)
	r.Get("/carriers/{id}/account", h.carrierAccount)
	r.With(admins).Post("/carriers/{id}/account/topup", h.carrierTopup)
	r.With(admins).Post("/carriers/{id}/account/adjust", h.carrierAdjust)
	r.Get("/carriers/{id}/account/ledger", h.carrierLedger)
	r.Get("/carriers/{id}/status", h.carrierStatus)
	r.Get("/carriers/status", h.allCarrierStatus)
	r.Get("/carriers/{id}/header-rules", h.listHeaderRules("carrier"))
	r.With(operators).Post("/carriers/{id}/header-rules", h.createHeaderRule("carrier"))
}

type carrierInput struct {
	Name                 string   `json:"name" validate:"required,min=1,max=64,lowercase,excludesall= "`
	Status               string   `json:"status" validate:"omitempty,oneof=active disabled"`
	RateGroupID          string   `json:"rate_group_id" validate:"omitempty,uuid"`
	GatewayHost          string   `json:"gateway_host" validate:"required,hostname|ip"`
	GatewayPort          int      `json:"gateway_port" validate:"omitempty,min=1,max=65535"`
	Transport            string   `json:"transport" validate:"omitempty,oneof=udp tcp tls"`
	DNIPrefix            string   `json:"dni_prefix" validate:"omitempty,max=16"`
	ANIPrefix            string   `json:"ani_prefix" validate:"omitempty,max=16"`
	StripDigits          int      `json:"strip_digits" validate:"min=0,max=15"`
	AuthUsername         string   `json:"auth_username"`
	AuthPassword         *string  `json:"auth_password"`
	FromDomain           string   `json:"from_domain"`
	Register             bool     `json:"register"`
	AllowedCodecs        []string `json:"allowed_codecs"`
	MaxConcurrentCalls   int      `json:"max_concurrent_calls" validate:"min=0"`
	MaxCPS               int      `json:"max_cps" validate:"min=0"`
	FailoverSIPCodes     []int    `json:"failover_sip_codes"`
	SIPOptionsPing       *bool    `json:"sip_options_ping"`
	ChargeFailedAttempts bool     `json:"charge_failed_attempts"`
	IgnoreEarlyMedia     bool     `json:"ignore_early_media"`
	Notes                string   `json:"notes"`
	Currency             string   `json:"currency" validate:"omitempty,len=3"`
	MediaMode            string   `json:"media_mode" validate:"omitempty,oneof=anchor proxy bypass"`
	DTMFMode             string   `json:"dtmf_mode" validate:"omitempty,oneof=rfc2833 info inband"`
	SRTPMode             string   `json:"srtp_mode" validate:"omitempty,oneof=off optional mandatory"`
	PrivacyMode          string   `json:"privacy_mode" validate:"omitempty,oneof=anonymize pass ignore"`
	SignallingSources    []string `json:"signalling_sources" validate:"dive,cidr|ip"`
}

func (in carrierInput) toModel(w http.ResponseWriter) (*model.Carrier, bool) {
	rg, err := uuidPtr(in.RateGroupID)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	status := in.Status
	if status == "" {
		status = "active"
	}
	port := in.GatewayPort
	if port == 0 {
		port = 5060
	}
	tr := in.Transport
	if tr == "" {
		tr = "udp"
	}
	codecs := in.AllowedCodecs
	if len(codecs) == 0 {
		codecs = []string{"PCMA", "PCMU"}
	}
	ping := true
	if in.SIPOptionsPing != nil {
		ping = *in.SIPOptionsPing
	}
	return &model.Carrier{Name: in.Name, Status: status, RateGroupID: rg, GatewayHost: in.GatewayHost, GatewayPort: port, Transport: tr,
		DNIPrefix: in.DNIPrefix, ANIPrefix: in.ANIPrefix, StripDigits: in.StripDigits, AuthUsername: strPtr(in.AuthUsername), AuthPassword: in.AuthPassword,
		FromDomain: strPtr(in.FromDomain), Register: in.Register, AllowedCodecs: codecs, MaxConcurrentCalls: in.MaxConcurrentCalls, MaxCPS: in.MaxCPS,
		FailoverSIPCodes: in.FailoverSIPCodes, SIPOptionsPing: ping, ChargeFailedAttempts: in.ChargeFailedAttempts, IgnoreEarlyMedia: in.IgnoreEarlyMedia, Notes: in.Notes,
		MediaMode: in.MediaMode, DTMFMode: in.DTMFMode, SRTPMode: in.SRTPMode, PrivacyMode: in.PrivacyMode, SignallingSources: in.SignallingSources}, true
}

// listCarriers godoc
// @Summary List carriers with account and gateway state
// @Tags carriers
// @Produce json
// @Param search query string false "name or host substring"
// @Param status query string false "active|disabled"
// @Param page query int false "page"
// @Param per_page query int false "per page"
// @Success 200 {object} map[string]any
// @Router /carriers [get]
func (h *Handler) listCarriers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.Store.ListCarriers(r.Context(), q.Get("search"), q.Get("status"), pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	type row struct {
		*model.Carrier
		Balance      *string `json:"balance"`
		Currency     *string `json:"currency"`
		RateGroup    *string `json:"rate_group_name"`
		GatewayState string  `json:"gateway_state"`
		Degraded     bool    `json:"degraded"`
	}
	items := make([]row, 0, len(out.Items))
	for i := range out.Items {
		c := out.Items[i]
		state := "UNKNOWN"
		if h.Gateways != nil {
			state = h.Gateways.State(c.GatewayName()).Status
		}
		if !c.SIPOptionsPing && state == "UNKNOWN" {
			state = "NOPING"
		}
		degraded := h.Pipe.Breaker() != nil && h.Pipe.Breaker().Degraded(r.Context(), c.ID)
		cc := c.Carrier
		items = append(items, row{Carrier: &cc, Balance: c.Balance, Currency: c.Currency, RateGroup: c.RateGroup, GatewayState: state, Degraded: degraded})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": out.Total, "page": out.Page, "per_page": out.PerPage})
}

// createCarrier godoc
// @Summary Create a carrier (renders its FreeSWITCH gateway)
// @Tags carriers
// @Accept json
// @Produce json
// @Param body body carrierInput true "carrier"
// @Success 201 {object} model.Carrier
// @Router /carriers [post]
func (h *Handler) createCarrier(w http.ResponseWriter, r *http.Request) {
	var in carrierInput
	if !h.decode(w, r, &in) {
		return
	}
	m, ok := in.toModel(w)
	if !ok {
		return
	}
	cur := in.Currency
	if cur == "" {
		cur = h.Cfg.Billing.Currency
	}
	out, err := h.Store.CreateCarrier(r.Context(), m, cur)
	if err != nil {
		failErr(w, err)
		return
	}
	out.AuthPassword = nil
	h.audit(r, "carrier.create", "carrier", out.ID.String(), nil, out)
	h.afterCarrierChange(r, out.ID)
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) afterCarrierChange(r *http.Request, id uuidT) {
	h.Tables.InvalidateRoutes(uuidNil)
	if h.Renderer != nil {
		if err := h.Renderer.RenderAll(r.Context()); err != nil {
			h.Log.Error("render after carrier change", "error", err)
		}
	}
	h.publish(r.Context(), cache.ChanCarriersChanged, id.String())
}

// getCarrier godoc
// @Summary Get a carrier with account, gateway state and breaker statistics
// @Tags carriers
// @Produce json
// @Param id path string true "carrier id"
// @Success 200 {object} map[string]any
// @Router /carriers/{id} [get]
func (h *Handler) getCarrier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	c, err := h.Store.CarrierByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	c.AuthPassword = nil
	acc, _ := h.Store.AccountByOwner(r.Context(), "carrier", id)
	var rg any
	if c.RateGroupID != nil {
		rg, _ = h.Store.RateGroupByID(r.Context(), *c.RateGroupID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"carrier": c, "account": acc, "rate_group": rg, "status": h.carrierState(r, c)})
}

func (h *Handler) carrierState(r *http.Request, c *model.Carrier) map[string]any {
	st := map[string]any{"gateway": c.GatewayName(), "state": "UNKNOWN", "ping_enabled": c.SIPOptionsPing}
	if h.Gateways != nil {
		g := h.Gateways.State(c.GatewayName())
		st["state"] = g.Status
		if !g.Since.IsZero() {
			st["since"] = g.Since
			st["checked_at"] = g.CheckedAt
		}
	}
	if !c.SIPOptionsPing && st["state"] == "UNKNOWN" {
		st["state"] = "NOPING"
	}
	if b := h.Pipe.Breaker(); b != nil {
		att, ans, consec := b.Stats(r.Context(), c.ID)
		st["degraded"] = b.Degraded(r.Context(), c.ID)
		st["degraded_reason"] = b.Reason(r.Context(), c.ID)
		st["attempts_5m"] = att
		st["answered_5m"] = ans
		st["consecutive_faults"] = consec
	}
	if n, err := h.Pipe.Admission().Concurrent(r.Context(), "carrier", c.ID); err == nil {
		st["calls_in_progress"] = n
	}
	return st
}

// updateCarrier godoc
// @Summary Update a carrier (re-renders its gateway)
// @Tags carriers
// @Accept json
// @Produce json
// @Param id path string true "carrier id"
// @Param body body carrierInput true "carrier"
// @Success 200 {object} model.Carrier
// @Router /carriers/{id} [put]
func (h *Handler) updateCarrier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.CarrierByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	before.AuthPassword = nil
	var in carrierInput
	if !h.decode(w, r, &in) {
		return
	}
	m, ok := in.toModel(w)
	if !ok {
		return
	}
	m.ID = id
	out, err := h.Store.UpdateCarrier(r.Context(), m)
	if err != nil {
		failErr(w, err)
		return
	}
	out.AuthPassword = nil
	h.audit(r, "carrier.update", "carrier", id.String(), before, out)
	h.afterCarrierChange(r, id)
	writeJSON(w, http.StatusOK, out)
}

// deleteCarrier godoc
// @Summary Delete a carrier (soft delete, removed from routes)
// @Tags carriers
// @Param id path string true "carrier id"
// @Success 204
// @Router /carriers/{id} [delete]
func (h *Handler) deleteCarrier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.CarrierByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	before.AuthPassword = nil
	if err := h.Store.DeleteCarrier(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "carrier.delete", "carrier", id.String(), before, nil)
	h.afterCarrierChange(r, id)
	w.WriteHeader(http.StatusNoContent)
}

// carrierAccount godoc
// @Summary Account of a carrier
// @Tags carriers
// @Produce json
// @Param id path string true "carrier id"
// @Success 200 {object} map[string]any
// @Router /carriers/{id}/account [get]
func (h *Handler) carrierAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.accountOf(w, r, "carrier", id)
}

// carrierTopup godoc
// @Summary Record a payment to the carrier (ledger type topup)
// @Tags carriers
// @Accept json
// @Produce json
// @Param id path string true "carrier id"
// @Param body body moneyInput true "amount"
// @Success 200 {object} map[string]any
// @Router /carriers/{id}/account/topup [post]
func (h *Handler) carrierTopup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.postMoney(w, r, "carrier", id, model.LedgerTopup, false)
}

// carrierAdjust godoc
// @Summary Adjust a carrier balance (signed)
// @Tags carriers
// @Accept json
// @Produce json
// @Param id path string true "carrier id"
// @Param body body moneyInput true "signed amount"
// @Success 200 {object} map[string]any
// @Router /carriers/{id}/account/adjust [post]
func (h *Handler) carrierAdjust(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.postMoney(w, r, "carrier", id, model.LedgerAdjustment, true)
}

// carrierLedger godoc
// @Summary Ledger entries of a carrier account
// @Tags carriers
// @Produce json
// @Param id path string true "carrier id"
// @Success 200 {object} map[string]any
// @Router /carriers/{id}/account/ledger [get]
func (h *Handler) carrierLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.ledgerOf(w, r, "carrier", id)
}

// carrierStatus godoc
// @Summary Gateway ping state, circuit breaker and live channel count of a carrier
// @Tags carriers
// @Produce json
// @Param id path string true "carrier id"
// @Success 200 {object} map[string]any
// @Router /carriers/{id}/status [get]
func (h *Handler) carrierStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	c, err := h.Store.CarrierByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.carrierState(r, c))
}

// allCarrierStatus godoc
// @Summary Gateway state of every carrier (dashboard health cards)
// @Tags carriers
// @Produce json
// @Success 200 {array} map[string]any
// @Router /carriers/status [get]
func (h *Handler) allCarrierStatus(w http.ResponseWriter, r *http.Request) {
	carriers, err := h.Store.Carriers(r.Context())
	if err != nil {
		failErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(carriers))
	for i := range carriers {
		st := h.carrierState(r, &carriers[i])
		st["carrier_id"] = carriers[i].ID
		st["name"] = carriers[i].Name
		st["status"] = carriers[i].Status
		out = append(out, st)
	}
	writeJSON(w, http.StatusOK, out)
}
