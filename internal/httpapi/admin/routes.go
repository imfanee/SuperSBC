package admin

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/callcontrol"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/numbering"
	"github.com/imfanee/supersbc/internal/quality"
	"github.com/imfanee/supersbc/internal/rating"
	"github.com/imfanee/supersbc/internal/store"
	"github.com/imfanee/supersbc/internal/timewindow"
)

func (h *Handler) mountRoutes(r chi.Router) {
	r.Get("/route-groups", h.listRouteGroups)
	r.With(operators).Post("/route-groups", h.createRouteGroup)
	r.Get("/route-groups/{id}", h.getRouteGroup)
	r.With(operators).Put("/route-groups/{id}", h.updateRouteGroup)
	r.With(operators).Delete("/route-groups/{id}", h.deleteRouteGroup)
	r.Get("/route-groups/{id}/routes", h.listRoutes)
	r.With(operators).Post("/route-groups/{id}/routes", h.createRoute)
	r.Get("/routes/{id}", h.getRoute)
	r.With(operators).Put("/routes/{id}", h.updateRoute)
	r.With(operators).Delete("/routes/{id}", h.deleteRoute)
	r.With(operators).Put("/routes/{id}/carriers", h.setRouteCarriers)
	r.Get("/routes/{id}/preview", h.previewRoute)
	r.Get("/routing/simulate", h.simulate)
}

type routeGroupInput struct {
	Name         string `json:"name" validate:"required,min=1,max=100"`
	Description  string `json:"description" validate:"max=500"`
	LCRMode      bool   `json:"lcr_mode"`
	LosslessMode bool   `json:"lossless_mode"`
	QualityMode  bool   `json:"quality_mode"`
	PercentMode  bool   `json:"percent_mode"`
}

// listRouteGroups godoc
// @Summary List route groups
// @Tags routing
// @Produce json
// @Success 200 {object} map[string]any
// @Router /route-groups [get]
func (h *Handler) listRouteGroups(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.ListRouteGroups(r.Context(), r.URL.Query().Get("search"), pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createRouteGroup godoc
// @Summary Create a route group
// @Tags routing
// @Accept json
// @Produce json
// @Param body body routeGroupInput true "route group"
// @Success 201 {object} model.RouteGroup
// @Router /route-groups [post]
func (h *Handler) createRouteGroup(w http.ResponseWriter, r *http.Request) {
	var in routeGroupInput
	if !h.decode(w, r, &in) {
		return
	}
	modes := model.RouteModes{LCR: in.LCRMode, Lossless: in.LosslessMode, Quality: in.QualityMode, Percent: in.PercentMode}
	if !modes.Valid() {
		fail(w, http.StatusBadRequest, "percentage based routing cannot be combined with least cost, lossless or quality routing")
		return
	}
	out, err := h.Store.CreateRouteGroup(r.Context(), in.Name, in.Description, modes)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route_group.create", "route_group", out.ID.String(), nil, out)
	writeJSON(w, http.StatusCreated, out)
}

// getRouteGroup godoc
// @Summary Get a route group
// @Tags routing
// @Produce json
// @Param id path string true "route group id"
// @Success 200 {object} model.RouteGroup
// @Router /route-groups/{id} [get]
func (h *Handler) getRouteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.RouteGroupByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// updateRouteGroup godoc
// @Summary Update a route group (name, description, LCR mode)
// @Tags routing
// @Accept json
// @Produce json
// @Param id path string true "route group id"
// @Param body body routeGroupInput true "route group"
// @Success 200 {object} model.RouteGroup
// @Router /route-groups/{id} [put]
func (h *Handler) updateRouteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.RouteGroupByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	var in routeGroupInput
	if !h.decode(w, r, &in) {
		return
	}
	modes := model.RouteModes{LCR: in.LCRMode, Lossless: in.LosslessMode, Quality: in.QualityMode, Percent: in.PercentMode}
	if !modes.Valid() {
		fail(w, http.StatusBadRequest, "percentage based routing cannot be combined with least cost, lossless or quality routing")
		return
	}
	out, err := h.Store.UpdateRouteGroup(r.Context(), id, in.Name, in.Description, modes)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route_group.update", "route_group", id.String(), before, out)
	h.routesChanged(r, id)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) routesChanged(r *http.Request, groupID uuid.UUID) {
	h.Tables.InvalidateRoutes(groupID)
	h.publish(r.Context(), cache.ChanRoutesChanged, groupID.String())
}

// deleteRouteGroup godoc
// @Summary Delete an unused route group
// @Tags routing
// @Param id path string true "route group id"
// @Success 204
// @Router /route-groups/{id} [delete]
func (h *Handler) deleteRouteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.Store.DeleteRouteGroup(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route_group.delete", "route_group", id.String(), nil, nil)
	h.routesChanged(r, id)
	w.WriteHeader(http.StatusNoContent)
}

// listRoutes godoc
// @Summary Routes of a group with their ordered carriers
// @Tags routing
// @Produce json
// @Param id path string true "route group id"
// @Param search query string false "prefix or destination"
// @Success 200 {array} store.RouteRow
// @Router /route-groups/{id}/routes [get]
func (h *Handler) listRoutes(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.ListRoutes(r.Context(), id, r.URL.Query().Get("search"))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type routeInput struct {
	Prefix      string                   `json:"prefix" validate:"omitempty,numeric,max=15"`
	Destination string                   `json:"destination" validate:"max=200"`
	Enabled     *bool                    `json:"enabled"`
	Carriers    []store.RouteCarrierSpec `json:"carriers"`
}

// createRoute godoc
// @Summary Create a route (optionally with its carriers)
// @Tags routing
// @Accept json
// @Produce json
// @Param id path string true "route group id"
// @Param body body routeInput true "route"
// @Success 201 {object} store.RouteRow
// @Router /route-groups/{id}/routes [post]
func (h *Handler) createRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in routeInput
	if !h.decode(w, r, &in) {
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	route, err := h.Store.CreateRoute(r.Context(), id, in.Prefix, in.Destination, enabled)
	if err != nil {
		failErr(w, err)
		return
	}
	if len(in.Carriers) > 0 {
		if !validateSpecs(w, in.Carriers) {
			return
		}
		if err := h.Store.ReplaceRouteCarriers(r.Context(), route.ID, normaliseSpecs(in.Carriers)); err != nil {
			failErr(w, err)
			return
		}
	}
	out, err := h.Store.RouteByID(r.Context(), route.ID)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route.create", "route", route.ID.String(), nil, out)
	h.routesChanged(r, id)
	writeJSON(w, http.StatusCreated, out)
}

// validateSpecs parses every routing window so a typo is a 400, not a
// carrier silently skipped at call time.
func validateSpecs(w http.ResponseWriter, specs []store.RouteCarrierSpec) bool {
	for _, s := range specs {
		if _, err := timewindow.Parse(s.Window); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return false
		}
	}
	return true
}

func normaliseSpecs(specs []store.RouteCarrierSpec) []store.RouteCarrierSpec {
	out := make([]store.RouteCarrierSpec, 0, len(specs))
	for i, s := range specs {
		s.Window = strings.TrimSpace(s.Window)
		if s.Priority <= 0 {
			s.Priority = i + 1
		}
		if s.Weight < 0 { // 0 is a valid share in percent mode
			s.Weight = 100
		}
		out = append(out, s)
	}
	return out
}

// getRoute godoc
// @Summary Get a route with its carriers
// @Tags routing
// @Produce json
// @Param id path string true "route id"
// @Success 200 {object} store.RouteRow
// @Router /routes/{id} [get]
func (h *Handler) getRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// updateRoute godoc
// @Summary Update a route (and replace its carriers when given)
// @Tags routing
// @Accept json
// @Produce json
// @Param id path string true "route id"
// @Param body body routeInput true "route"
// @Success 200 {object} store.RouteRow
// @Router /routes/{id} [put]
func (h *Handler) updateRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	var in routeInput
	if !h.decode(w, r, &in) {
		return
	}
	enabled := before.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	prefix := in.Prefix
	if prefix == "" && before.Prefix != "" {
		prefix = before.Prefix
	}
	if _, err := h.Store.UpdateRoute(r.Context(), id, prefix, in.Destination, enabled); err != nil {
		failErr(w, err)
		return
	}
	if in.Carriers != nil {
		if err := h.Store.ReplaceRouteCarriers(r.Context(), id, normaliseSpecs(in.Carriers)); err != nil {
			failErr(w, err)
			return
		}
	}
	out, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route.update", "route", id.String(), before, out)
	h.routesChanged(r, before.RouteGroupID)
	writeJSON(w, http.StatusOK, out)
}

// deleteRoute godoc
// @Summary Delete a route
// @Tags routing
// @Param id path string true "route id"
// @Success 204
// @Router /routes/{id} [delete]
func (h *Handler) deleteRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	if err := h.Store.DeleteRoute(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route.delete", "route", id.String(), before, nil)
	h.routesChanged(r, before.RouteGroupID)
	w.WriteHeader(http.StatusNoContent)
}

type routeCarriersInput struct {
	Carriers []store.RouteCarrierSpec `json:"carriers" validate:"required,dive"`
}

// setRouteCarriers godoc
// @Summary Replace the ordered carrier list of a route (reorder endpoint)
// @Tags routing
// @Accept json
// @Produce json
// @Param id path string true "route id"
// @Param body body routeCarriersInput true "ordered carriers"
// @Success 200 {object} store.RouteRow
// @Router /routes/{id}/carriers [put]
func (h *Handler) setRouteCarriers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	var in routeCarriersInput
	if !h.decode(w, r, &in) || !validateSpecs(w, in.Carriers) {
		return
	}
	if err := h.Store.ReplaceRouteCarriers(r.Context(), id, normaliseSpecs(in.Carriers)); err != nil {
		failErr(w, err)
		return
	}
	out, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "route.carriers", "route", id.String(), before.CarrierRows, out.CarrierRows)
	h.routesChanged(r, before.RouteGroupID)
	writeJSON(w, http.StatusOK, out)
}

// previewRoute godoc
// @Summary Buy rate and margin per carrier of a route for a sample number
// @Tags routing
// @Produce json
// @Param id path string true "route id"
// @Param number query string true "sample number (defaults to the route prefix padded)"
// @Param sell_rate_group_id query string false "selling deck to compute margin against"
// @Success 200 {object} map[string]any
// @Router /routes/{id}/preview [get]
func (h *Handler) previewRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	route, err := h.Store.RouteByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	number := r.URL.Query().Get("number")
	if number == "" {
		number = route.Prefix + strings.Repeat("0", 12-len(route.Prefix))
	}
	var sell *decimal.Decimal
	if sg := queryUUID(r, "sell_rate_group_id"); sg != nil {
		if rt, ok, _ := h.Tables.MatchRate(r.Context(), *sg, number); ok {
			sell = &rt.RatePerMin
		}
	}
	type row struct {
		store.RouteCarrierRow
		BuyRate     *string        `json:"buy_rate_per_min"`
		Destination string         `json:"buy_destination"`
		Margin      *string        `json:"margin_per_min"`
		State       string         `json:"gateway_state"`
		Quality     *quality.Score `json:"quality,omitempty"`
	}
	out := make([]row, 0, len(route.CarrierRows))
	for _, rc := range route.CarrierRows {
		x := row{RouteCarrierRow: rc, State: "UNKNOWN"}
		if h.Quality != nil {
			q, _ := h.Quality.Lookup(rc.CarrierID, route.Prefix)
			x.Quality = &q
		}
		if c, err := h.Store.CarrierByID(r.Context(), rc.CarrierID); err == nil {
			if h.Gateways != nil {
				x.State = h.Gateways.State(c.GatewayName()).Status
			}
			if c.RateGroupID != nil {
				if rt, ok, _ := h.Tables.MatchRate(r.Context(), *c.RateGroupID, number); ok {
					b := rt.RatePerMin.StringFixed(6)
					x.BuyRate = &b
					x.Destination = rt.Destination
					if sell != nil {
						m := sell.Sub(rt.RatePerMin).StringFixed(6)
						x.Margin = &m
					}
				}
			}
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"route": route, "number": number, "sell_rate_per_min": sell, "carriers": out})
}

// simulate godoc
// @Summary Routing simulator: the whole setup decision for a customer and number without dialling
// @Tags routing
// @Produce json
// @Param customer_id query string true "customer id"
// @Param number query string true "dialled number as the customer would send it"
// @Param caller query string false "caller id"
// @Success 200 {object} map[string]any
// @Router /routing/simulate [get]
func (h *Handler) simulate(w http.ResponseWriter, r *http.Request) {
	cid := queryUUID(r, "customer_id")
	number := strings.TrimSpace(r.URL.Query().Get("number"))
	if cid == nil || number == "" {
		fail(w, http.StatusBadRequest, "customer_id and number required")
		return
	}
	cust, err := h.Store.CustomerByID(r.Context(), *cid)
	if err != nil {
		failErr(w, err)
		return
	}
	caller := r.URL.Query().Get("caller")
	if caller == "" {
		caller = "15550000000"
	}
	steps := []map[string]any{}
	step := func(name string, ok bool, detail any) {
		steps = append(steps, map[string]any{"step": name, "ok": ok, "detail": detail})
	}
	res := map[string]any{"customer": cust, "input_number": number, "steps": nil, "expected_sip": "200 OK on answer"}
	finish := func(code int, reason string) {
		res["expected_sip"] = strings.TrimSpace(strings.Join([]string{itoa(code), reason}, " "))
		res["steps"] = steps
		writeJSON(w, http.StatusOK, res)
	}
	ips, _ := h.Store.CustomerIPs(r.Context(), cust.ID)
	step("authorize", cust.Status == "active", map[string]any{"status": cust.Status, "ip_count": len(ips), "max_concurrent_calls": cust.MaxConcurrentCalls, "max_cps": cust.MaxCPS})
	if cust.Status != "active" {
		finish(403, "Customer suspended")
		return
	}
	opts := numbering.Options{IntlPrefix: cust.IntlPrefix}
	if cust.TechPrefix != nil {
		opts.TechPrefix = *cust.TechPrefix
	}
	if cust.DefaultCountryCode != nil {
		opts.DefaultCountryCode = *cust.DefaultCountryCode
	}
	called, err := numbering.Normalize(number, opts)
	step("normalize", err == nil, map[string]any{"called": called, "error": errString(err)})
	if err != nil {
		finish(484, "Address Incomplete")
		return
	}
	res["called"] = called
	if cust.BlockedPrefixesEnabled {
		if blk, _ := h.Tables.Blocked(r.Context(), cust.ID, called); blk != nil {
			step("blocklist", false, blk)
			finish(403, "Destination blocked")
			return
		}
		step("blocklist", true, nil)
	}
	sell, err := h.Pipe.SellRateFor(r.Context(), cust, called)
	if err != nil {
		failErr(w, err)
		return
	}
	if sell == nil {
		step("rate", false, nil)
		finish(404, "No rate for destination")
		return
	}
	acc, _ := h.Store.AccountByOwner(r.Context(), "customer", cust.ID)
	currency := h.Cfg.Billing.Currency
	if acc != nil {
		currency = acc.Currency
	}
	sellConv, fx, fxOK := h.Pipe.ConvertRate(r.Context(), sell, currency)
	if !fxOK {
		step("rate", false, map[string]any{"rate": sell, "error": "no exchange rate into " + currency})
		finish(404, "No rate for destination")
		return
	}
	rr := rating.Rate{PerMinute: sellConv.RatePerMin, ConnectFee: sellConv.ConnectFee, Increments: rating.Increments{Initial: sell.InitialIncrement, Subsequent: sell.SubsequentIncrement, MinDuration: sell.MinDuration}}
	reserve := rating.ReserveAmount(h.Cfg.Billing.ReserveMinutes, rr)
	step("rate", true, map[string]any{"rate": sellConv, "fx": fx.String(), "currency": currency})
	var available decimal.Decimal
	if acc != nil {
		available = acc.Available()
	}
	affordable := available.GreaterThan(decimal.Zero) && available.GreaterThanOrEqual(reserve)
	maxSecs := rating.MaxCallSeconds(available, rr, int(h.Cfg.Billing.MaxCallDuration.Seconds()))
	step("balance", affordable, map[string]any{"available": available.StringFixed(6), "reserve_amount": reserve.StringFixed(6), "max_call_seconds": maxSecs})
	if !affordable {
		finish(402, "Not enough funds")
		return
	}
	route, err := h.Pipe.Route(r.Context(), cust, called, caller, sellConv, uuid.New().String())
	if err != nil {
		failErr(w, err)
		return
	}
	if route.Route == nil || len(route.Carriers) == 0 {
		step("route", false, map[string]any{"route": route.Route, "skipped": route.Skipped})
		finish(503, "No route")
		return
	}
	type choice struct {
		callcontrol.CarrierChoice
		MarginPerMin *string `json:"margin_per_min"`
	}
	choices := make([]choice, 0, len(route.Carriers))
	for _, c := range route.Carriers {
		ch := choice{CarrierChoice: c}
		if c.BuyRatePerMin != nil {
			m := sellConv.RatePerMin.Sub(*c.BuyRatePerMin).StringFixed(6)
			ch.MarginPerMin = &m
		}
		choices = append(choices, ch)
	}
	step("route", true, map[string]any{"route": route.Route, "modes": route.Modes, "carriers": choices, "skipped": route.Skipped})
	res["carriers"] = choices
	res["modes"] = route.Modes
	finish(200, "OK (dial "+route.Carriers[0].Name+" first)")
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
