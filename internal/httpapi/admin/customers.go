package admin

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/opensbc/opensbc/internal/cache"
	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/store"
)

func (h *Handler) mountCustomers(r chi.Router) {
	r.Get("/customers", h.listCustomers)
	r.With(operators).Post("/customers", h.createCustomer)
	r.Get("/customers/{id}", h.getCustomer)
	r.With(operators).Put("/customers/{id}", h.updateCustomer)
	r.With(operators).Delete("/customers/{id}", h.deleteCustomer)
	r.Get("/customers/{id}/ips", h.listCustomerIPs)
	r.With(operators).Post("/customers/{id}/ips", h.addCustomerIP)
	r.With(operators).Delete("/customers/{id}/ips/{ipId}", h.deleteCustomerIP)
	r.Get("/customers/{id}/account", h.customerAccount)
	r.With(admins).Post("/customers/{id}/account/topup", h.customerTopup)
	r.With(admins).Post("/customers/{id}/account/adjust", h.customerAdjust)
	r.With(admins).Put("/customers/{id}/account/credit", h.customerCredit)
	r.Get("/customers/{id}/account/ledger", h.customerLedger)
	r.Get("/customers/{id}/blocked-prefixes", h.listCustomerBlocks)
	r.With(operators).Post("/customers/{id}/blocked-prefixes", h.addCustomerBlock)
	r.With(operators).Delete("/customers/{id}/blocked-prefixes/{blockId}", h.deleteBlock)
	r.Get("/customers/{id}/header-rules", h.listHeaderRules("customer"))
	r.With(operators).Post("/customers/{id}/header-rules", h.createHeaderRule("customer"))
	r.With(operators).Put("/header-rules/{ruleId}", h.updateHeaderRule)
	r.With(operators).Delete("/header-rules/{ruleId}", h.deleteHeaderRule)
	r.Get("/blocked-prefixes", h.listGlobalBlocks)
	r.With(operators).Post("/blocked-prefixes", h.addGlobalBlock)
	r.With(operators).Delete("/blocked-prefixes/{blockId}", h.deleteBlock)
}

// customerInput is the create/update body.
type customerInput struct {
	Name                   string   `json:"name" validate:"required,min=1,max=100"`
	Status                 string   `json:"status" validate:"omitempty,oneof=active suspended blocked"`
	RateGroupID            string   `json:"rate_group_id" validate:"omitempty,uuid"`
	RouteGroupID           string   `json:"route_group_id" validate:"omitempty,uuid"`
	MaxConcurrentCalls     int      `json:"max_concurrent_calls" validate:"min=0"`
	MaxCPS                 int      `json:"max_cps" validate:"min=0"`
	AllowedCodecs          []string `json:"allowed_codecs"`
	TechPrefix             string   `json:"tech_prefix"`
	DefaultCountryCode     string   `json:"default_country_code" validate:"omitempty,numeric,max=4"`
	IntlPrefix             string   `json:"intl_prefix" validate:"omitempty,numeric,max=4"`
	TrustPAI               bool     `json:"trust_pai"`
	BlockedPrefixesEnabled *bool    `json:"blocked_prefixes_enabled"`
	Notes                  string   `json:"notes"`
	Currency               string   `json:"currency" validate:"omitempty,len=3"`
	MediaMode              string   `json:"media_mode" validate:"omitempty,oneof=anchor proxy bypass"`
	DTMFMode               string   `json:"dtmf_mode" validate:"omitempty,oneof=rfc2833 info inband"`
	SRTPMode               string   `json:"srtp_mode" validate:"omitempty,oneof=off optional mandatory"`
	RequireTLS             bool     `json:"require_tls"`
	STIRMode               string   `json:"stir_mode" validate:"omitempty,oneof=ignore verify require"`
	TLSSubject             string   `json:"tls_subject" validate:"max=500"`
}

func (in customerInput) toModel(w http.ResponseWriter) (*model.Customer, bool) {
	rg, err := uuidPtr(in.RateGroupID)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	rt, err := uuidPtr(in.RouteGroupID)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	status := in.Status
	if status == "" {
		status = "active"
	}
	codecs := in.AllowedCodecs
	if len(codecs) == 0 {
		codecs = []string{"PCMA", "PCMU", "OPUS", "G722"}
	}
	blocks := true
	if in.BlockedPrefixesEnabled != nil {
		blocks = *in.BlockedPrefixesEnabled
	}
	return &model.Customer{Name: in.Name, Status: status, RateGroupID: rg, RouteGroupID: rt, MaxConcurrentCalls: in.MaxConcurrentCalls, MaxCPS: in.MaxCPS,
		AllowedCodecs: codecs, TechPrefix: strPtr(in.TechPrefix), DefaultCountryCode: strPtr(in.DefaultCountryCode), IntlPrefix: in.IntlPrefix,
		TrustPAI: in.TrustPAI, BlockedPrefixesEnabled: blocks, Notes: in.Notes,
		MediaMode: in.MediaMode, DTMFMode: in.DTMFMode, SRTPMode: in.SRTPMode, RequireTLS: in.RequireTLS, STIRMode: in.STIRMode, TLSSubject: strings.TrimSpace(in.TLSSubject)}, true
}

// listCustomers godoc
// @Summary List customers with account summary
// @Tags customers
// @Produce json
// @Param search query string false "name or IP substring"
// @Param status query string false "active|suspended|blocked"
// @Param page query int false "page"
// @Param per_page query int false "per page"
// @Param sort query string false "name|status|balance|created_at (prefix - for descending)"
// @Success 200 {object} map[string]any
// @Router /customers [get]
func (h *Handler) listCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.Store.ListCustomers(r.Context(), store.CustomerFilter{Search: q.Get("search"), Status: q.Get("status")}, pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createCustomer godoc
// @Summary Create a customer (and its account)
// @Tags customers
// @Accept json
// @Produce json
// @Param body body customerInput true "customer"
// @Success 201 {object} model.Customer
// @Router /customers [post]
func (h *Handler) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in customerInput
	if !h.decode(w, r, &in) {
		return
	}
	m, ok := in.toModel(w)
	if !ok {
		return
	}
	if !h.checkRateGroupCurrency(w, r, m.RateGroupID, in.Currency) {
		return
	}
	cur := in.Currency
	if cur == "" {
		cur = h.Cfg.Billing.Currency
	}
	out, err := h.Store.CreateCustomer(r.Context(), m, cur)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "customer.create", "customer", out.ID.String(), nil, out)
	writeJSON(w, http.StatusCreated, out)
}

// checkRateGroupCurrency enforces D-24: without an fx rate, the deck currency
// must match the account currency.
func (h *Handler) checkRateGroupCurrency(w http.ResponseWriter, r *http.Request, rateGroupID *uuidT, accountCurrency string) bool {
	if rateGroupID == nil {
		return true
	}
	rg, err := h.Store.RateGroupByID(r.Context(), *rateGroupID)
	if err != nil {
		fail(w, http.StatusBadRequest, "rate group not found")
		return false
	}
	if accountCurrency == "" {
		accountCurrency = h.Cfg.Billing.Currency
	}
	if _, ok := h.Tables.FX(r.Context(), rg.Currency, accountCurrency); !ok {
		fail(w, http.StatusUnprocessableEntity, "rate group currency "+rg.Currency+" differs from account currency "+accountCurrency+" and no exchange rate exists")
		return false
	}
	return true
}

// getCustomer godoc
// @Summary Get a customer with account, IPs and group names
// @Tags customers
// @Produce json
// @Param id path string true "customer id"
// @Success 200 {object} map[string]any
// @Router /customers/{id} [get]
func (h *Handler) getCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	c, err := h.Store.CustomerByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	acc, _ := h.Store.AccountByOwner(r.Context(), "customer", id)
	ips, _ := h.Store.CustomerIPs(r.Context(), id)
	var rg, rt any
	if c.RateGroupID != nil {
		rg, _ = h.Store.RateGroupByID(r.Context(), *c.RateGroupID)
	}
	if c.RouteGroupID != nil {
		rt, _ = h.Store.RouteGroupByID(r.Context(), *c.RouteGroupID)
	}
	var available *string
	if acc != nil {
		a := acc.Available().StringFixed(6)
		available = &a
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer": c, "account": acc, "available": available, "ips": ips, "rate_group": rg, "route_group": rt})
}

// updateCustomer godoc
// @Summary Update a customer
// @Tags customers
// @Accept json
// @Produce json
// @Param id path string true "customer id"
// @Param body body customerInput true "customer"
// @Success 200 {object} model.Customer
// @Router /customers/{id} [put]
func (h *Handler) updateCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.CustomerByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	var in customerInput
	if !h.decode(w, r, &in) {
		return
	}
	m, ok := in.toModel(w)
	if !ok {
		return
	}
	m.ID = id
	acc, _ := h.Store.AccountByOwner(r.Context(), "customer", id)
	cur := ""
	if acc != nil {
		cur = acc.Currency
	}
	if !h.checkRateGroupCurrency(w, r, m.RateGroupID, cur) {
		return
	}
	out, err := h.Store.UpdateCustomer(r.Context(), m)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "customer.update", "customer", id.String(), before, out)
	h.Pipe.IPCache().Flush(r.Context()) //nolint:errcheck // best effort cache flush
	h.publish(r.Context(), cache.ChanCustomerIPs, id.String())
	writeJSON(w, http.StatusOK, out)
}

// deleteCustomer godoc
// @Summary Delete a customer (soft delete, IPs removed, history kept)
// @Tags customers
// @Param id path string true "customer id"
// @Success 204
// @Router /customers/{id} [delete]
func (h *Handler) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.CustomerByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.DeleteCustomer(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "customer.delete", "customer", id.String(), before, nil)
	h.publish(r.Context(), cache.ChanCustomerIPs, id.String())
	w.WriteHeader(http.StatusNoContent)
}

// listCustomerIPs godoc
// @Summary List a customer's authorised addresses
// @Tags customers
// @Produce json
// @Param id path string true "customer id"
// @Success 200 {array} model.CustomerIP
// @Router /customers/{id}/ips [get]
func (h *Handler) listCustomerIPs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	ips, err := h.Store.CustomerIPs(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ips)
}

type ipInput struct {
	IPCIDR    string `json:"ip_cidr" validate:"required,cidr|ip"`
	Port      *int   `json:"port" validate:"omitempty,min=1,max=65535"`
	Transport string `json:"transport" validate:"omitempty,oneof=udp tcp tls any"`
}

// addCustomerIP godoc
// @Summary Authorise an address (single IP or CIDR) for a customer
// @Tags customers
// @Accept json
// @Produce json
// @Param id path string true "customer id"
// @Param body body ipInput true "address"
// @Success 201 {object} model.CustomerIP
// @Router /customers/{id}/ips [post]
func (h *Handler) addCustomerIP(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in ipInput
	if !h.decode(w, r, &in) {
		return
	}
	if in.Transport == "" {
		in.Transport = "any"
	}
	ip, err := h.Store.AddCustomerIP(r.Context(), id, in.IPCIDR, in.Port, in.Transport)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "customer.ip.add", "customer", id.String(), nil, ip)
	h.Pipe.IPCache().Flush(r.Context()) //nolint:errcheck // best effort cache flush
	h.publish(r.Context(), cache.ChanCustomerIPs, id.String())
	writeJSON(w, http.StatusCreated, ip)
}

// deleteCustomerIP godoc
// @Summary Remove an authorised address
// @Tags customers
// @Param id path string true "customer id"
// @Param ipId path string true "address id"
// @Success 204
// @Router /customers/{id}/ips/{ipId} [delete]
func (h *Handler) deleteCustomerIP(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	ipID, ok := pathUUID(w, r, "ipId")
	if !ok {
		return
	}
	if err := h.Store.DeleteCustomerIP(r.Context(), id, ipID); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "customer.ip.delete", "customer", id.String(), map[string]string{"ip_id": ipID.String()}, nil)
	h.Pipe.IPCache().Flush(r.Context()) //nolint:errcheck // best effort cache flush
	h.publish(r.Context(), cache.ChanCustomerIPs, id.String())
	w.WriteHeader(http.StatusNoContent)
}

// customerAccount godoc
// @Summary Account of a customer
// @Tags customers
// @Produce json
// @Param id path string true "customer id"
// @Success 200 {object} map[string]any
// @Router /customers/{id}/account [get]
func (h *Handler) customerAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.accountOf(w, r, "customer", id)
}

func (h *Handler) accountOf(w http.ResponseWriter, r *http.Request, ownerType string, id uuidT) {
	acc, err := h.Store.AccountByOwner(r.Context(), ownerType, id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": acc, "available": acc.Available().StringFixed(6)})
}

type moneyInput struct {
	Amount      string `json:"amount" validate:"required"`
	Description string `json:"description" validate:"max=500"`
}

func (h *Handler) postMoney(w http.ResponseWriter, r *http.Request, ownerType string, id uuidT, kind string, allowNegative bool) {
	var in moneyInput
	if !h.decode(w, r, &in) {
		return
	}
	amt, err := decimal.NewFromString(in.Amount)
	if err != nil {
		fail(w, http.StatusBadRequest, "amount must be a decimal string")
		return
	}
	if amt.IsZero() || (!allowNegative && amt.IsNegative()) {
		fail(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	acc, err := h.Store.AccountByOwner(r.Context(), ownerType, id)
	if err != nil {
		failErr(w, err)
		return
	}
	p := principal(r.Context())
	by := p.Email
	var out *model.Account
	err = h.Store.WithTx(r.Context(), func(tx pgx.Tx) error {
		if _, err := store.LockAccount(r.Context(), tx, acc.ID); err != nil {
			return err
		}
		out, err = store.Post(r.Context(), tx, acc.ID, nil, kind, amt.Round(6), in.Description, &by)
		return err
	})
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "account."+kind, ownerType, id.String(), map[string]string{"balance": acc.Balance.String()}, map[string]string{"balance": out.Balance.String(), "amount": amt.String()})
	writeJSON(w, http.StatusOK, map[string]any{"account": out, "available": out.Available().StringFixed(6)})
}

// customerTopup godoc
// @Summary Add money to a customer account (ledger type topup)
// @Tags customers
// @Accept json
// @Produce json
// @Param id path string true "customer id"
// @Param body body moneyInput true "amount (positive decimal string)"
// @Success 200 {object} map[string]any
// @Router /customers/{id}/account/topup [post]
func (h *Handler) customerTopup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.postMoney(w, r, "customer", id, model.LedgerTopup, false)
}

// customerAdjust godoc
// @Summary Adjust a customer balance (signed decimal, ledger type adjustment)
// @Tags customers
// @Accept json
// @Produce json
// @Param id path string true "customer id"
// @Param body body moneyInput true "signed amount"
// @Success 200 {object} map[string]any
// @Router /customers/{id}/account/adjust [post]
func (h *Handler) customerAdjust(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.postMoney(w, r, "customer", id, model.LedgerAdjustment, true)
}

type creditInput struct {
	AllowedCredit string `json:"allowed_credit" validate:"required"`
}

// customerCredit godoc
// @Summary Set the credit limit of a customer
// @Tags customers
// @Accept json
// @Produce json
// @Param id path string true "customer id"
// @Param body body creditInput true "credit"
// @Success 200 {object} map[string]any
// @Router /customers/{id}/account/credit [put]
func (h *Handler) customerCredit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in creditInput
	if !h.decode(w, r, &in) {
		return
	}
	credit, err := decimal.NewFromString(in.AllowedCredit)
	if err != nil || credit.IsNegative() {
		fail(w, http.StatusBadRequest, "allowed_credit must be a non-negative decimal string")
		return
	}
	acc, err := h.Store.AccountByOwner(r.Context(), "customer", id)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.SetAllowedCredit(r.Context(), acc.ID, credit); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "account.credit", "customer", id.String(), map[string]string{"allowed_credit": acc.AllowedCredit.String()}, map[string]string{"allowed_credit": credit.String()})
	h.accountOf(w, r, "customer", id)
}

// customerLedger godoc
// @Summary Ledger entries of a customer account
// @Tags customers
// @Produce json
// @Param id path string true "customer id"
// @Param page query int false "page"
// @Param per_page query int false "per page"
// @Success 200 {array} model.LedgerEntry
// @Router /customers/{id}/account/ledger [get]
func (h *Handler) customerLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.ledgerOf(w, r, "customer", id)
}

func (h *Handler) ledgerOf(w http.ResponseWriter, r *http.Request, ownerType string, id uuidT) {
	acc, err := h.Store.AccountByOwner(r.Context(), ownerType, id)
	if err != nil {
		failErr(w, err)
		return
	}
	p := pageParams(r)
	entries, err := h.Store.Ledger(r.Context(), acc.ID, p.PerPage, p.Offset())
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries, "page": p.Page, "per_page": p.PerPage})
}

type blockInput struct {
	Prefix string `json:"prefix" validate:"required,numeric,max=15"`
	Reason string `json:"reason" validate:"max=200"`
}

// listCustomerBlocks godoc
// @Summary Blocked prefixes of a customer
// @Tags customers
// @Produce json
// @Param id path string true "customer id"
// @Success 200 {array} model.BlockedPrefix
// @Router /customers/{id}/blocked-prefixes [get]
func (h *Handler) listCustomerBlocks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.BlockedPrefixes(r.Context(), &id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addCustomerBlock godoc
// @Summary Block a prefix for one customer
// @Tags customers
// @Accept json
// @Produce json
// @Param id path string true "customer id"
// @Param body body blockInput true "prefix"
// @Success 201 {object} model.BlockedPrefix
// @Router /customers/{id}/blocked-prefixes [post]
func (h *Handler) addCustomerBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	h.addBlock(w, r, &id)
}

// listGlobalBlocks godoc
// @Summary Global blacklist of prefixes
// @Tags routing
// @Produce json
// @Success 200 {array} model.BlockedPrefix
// @Router /blocked-prefixes [get]
func (h *Handler) listGlobalBlocks(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.BlockedPrefixes(r.Context(), nil)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addGlobalBlock godoc
// @Summary Block a prefix for every customer
// @Tags routing
// @Accept json
// @Produce json
// @Param body body blockInput true "prefix"
// @Success 201 {object} model.BlockedPrefix
// @Router /blocked-prefixes [post]
func (h *Handler) addGlobalBlock(w http.ResponseWriter, r *http.Request) { h.addBlock(w, r, nil) }

func (h *Handler) addBlock(w http.ResponseWriter, r *http.Request, customerID *uuidT) {
	var in blockInput
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.Store.AddBlockedPrefix(r.Context(), customerID, in.Prefix, in.Reason)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "blocklist.add", "blocked_prefix", out.ID.String(), nil, out)
	h.Tables.InvalidateBlocks()
	h.publish(r.Context(), cache.ChanBlocklistChanged, out.ID.String())
	writeJSON(w, http.StatusCreated, out)
}

// deleteBlock godoc
// @Summary Remove a blocked prefix
// @Tags routing
// @Param blockId path string true "block id"
// @Success 204
// @Router /blocked-prefixes/{blockId} [delete]
func (h *Handler) deleteBlock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "blockId")
	if !ok {
		return
	}
	if err := h.Store.DeleteBlockedPrefix(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "blocklist.delete", "blocked_prefix", id.String(), nil, nil)
	h.Tables.InvalidateBlocks()
	h.publish(r.Context(), cache.ChanBlocklistChanged, id.String())
	w.WriteHeader(http.StatusNoContent)
}
