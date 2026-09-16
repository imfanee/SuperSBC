package admin

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/metrics"
	"github.com/imfanee/supersbc/internal/model"
)

type headerRuleInput struct {
	Direction string `json:"direction" validate:"required,oneof=egress response"`
	Action    string `json:"action" validate:"required,oneof=add passthrough remove"`
	Header    string `json:"header" validate:"required,max=64"`
	Value     string `json:"value" validate:"max=1000"`
	Priority  int    `json:"priority"`
	Enabled   *bool  `json:"enabled"`
}

// listHeaderRules godoc
// @Summary Header manipulation rules of a customer or carrier
// @Tags routing
// @Produce json
// @Param id path string true "owner id"
// @Success 200 {array} model.HeaderRule
// @Router /customers/{id}/header-rules [get]
func (h *Handler) listHeaderRules(ownerType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		out, err := h.Store.HeaderRules(r.Context(), ownerType, id)
		if err != nil {
			failErr(w, err)
			return
		}
		if out == nil {
			out = []model.HeaderRule{}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// createHeaderRule godoc
// @Summary Add a header rule (egress: INVITE to the carrier; response: responses to the customer). Values may use {caller} {called} {customer} {carrier} {call_uuid} {node_ip}
// @Tags routing
// @Accept json
// @Produce json
// @Param id path string true "owner id"
// @Param body body headerRuleInput true "rule"
// @Success 201 {object} model.HeaderRule
// @Router /customers/{id}/header-rules [post]
func (h *Handler) createHeaderRule(ownerType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var in headerRuleInput
		if !h.decode(w, r, &in) {
			return
		}
		enabled := true
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
		if in.Priority == 0 {
			in.Priority = 100
		}
		out, err := h.Store.CreateHeaderRule(r.Context(), &model.HeaderRule{OwnerType: ownerType, OwnerID: id, Direction: in.Direction, Action: in.Action, Header: in.Header, Value: in.Value, Priority: in.Priority, Enabled: enabled})
		if err != nil {
			failErr(w, err)
			return
		}
		h.audit(r, "header_rule.create", ownerType, id.String(), nil, out)
		h.Tables.InvalidateHeaderRules()
		h.publish(r.Context(), cache.ChanHeadersChanged, out.ID.String())
		writeJSON(w, http.StatusCreated, out)
	}
}

// updateHeaderRule godoc
// @Summary Update a header rule
// @Tags routing
// @Accept json
// @Produce json
// @Param ruleId path string true "rule id"
// @Param body body headerRuleInput true "rule"
// @Success 200 {object} model.HeaderRule
// @Router /header-rules/{ruleId} [put]
func (h *Handler) updateHeaderRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "ruleId")
	if !ok {
		return
	}
	var in headerRuleInput
	if !h.decode(w, r, &in) {
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	out, err := h.Store.UpdateHeaderRule(r.Context(), &model.HeaderRule{ID: id, Direction: in.Direction, Action: in.Action, Header: in.Header, Value: in.Value, Priority: in.Priority, Enabled: enabled})
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "header_rule.update", "header_rule", id.String(), nil, out)
	h.Tables.InvalidateHeaderRules()
	h.publish(r.Context(), cache.ChanHeadersChanged, id.String())
	writeJSON(w, http.StatusOK, out)
}

// deleteHeaderRule godoc
// @Summary Delete a header rule
// @Tags routing
// @Param ruleId path string true "rule id"
// @Success 204
// @Router /header-rules/{ruleId} [delete]
func (h *Handler) deleteHeaderRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "ruleId")
	if !ok {
		return
	}
	if err := h.Store.DeleteHeaderRule(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "header_rule.delete", "header_rule", id.String(), nil, nil)
	h.Tables.InvalidateHeaderRules()
	h.publish(r.Context(), cache.ChanHeadersChanged, id.String())
	w.WriteHeader(http.StatusNoContent)
}

// ---- banned ips ----

func (h *Handler) mountBans(r chi.Router) {
	r.Get("/system/banned-ips", h.listBans)
	r.With(operators).Post("/system/banned-ips", h.createBan)
	r.With(operators).Delete("/system/banned-ips/{ip}", h.deleteBan)
}

type banInput struct {
	IP      string `json:"ip" validate:"required,ip"`
	Reason  string `json:"reason" validate:"max=200"`
	Minutes int    `json:"minutes" validate:"min=0"` // 0 = permanent
}

// listBans godoc
// @Summary Active source address bans (automatic scanner bans and manual ones)
// @Tags system
// @Produce json
// @Success 200 {array} model.BannedIP
// @Router /system/banned-ips [get]
func (h *Handler) listBans(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.BannedIPs(r.Context())
	if err != nil {
		failErr(w, err)
		return
	}
	if out == nil {
		out = []model.BannedIP{}
	}
	writeJSON(w, http.StatusOK, out)
}

// createBan godoc
// @Summary Ban a source address (FreeSWITCH ACL deny, reloaded immediately)
// @Tags system
// @Accept json
// @Produce json
// @Param body body banInput true "address"
// @Success 201 {object} model.BannedIP
// @Router /system/banned-ips [post]
func (h *Handler) createBan(w http.ResponseWriter, r *http.Request) {
	var in banInput
	if !h.decode(w, r, &in) {
		return
	}
	var exp *time.Time
	if in.Minutes > 0 {
		t := time.Now().Add(time.Duration(in.Minutes) * time.Minute)
		exp = &t
	}
	out, err := h.Store.Ban(r.Context(), in.IP, in.Reason, 0, true, exp)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "ban.create", "banned_ip", in.IP, nil, out)
	metrics.Bans.WithLabelValues("manual").Inc()
	if h.Renderer != nil {
		_ = h.Renderer.RenderACLs(r.Context())
	}
	h.publish(r.Context(), cache.ChanBansChanged, in.IP)
	writeJSON(w, http.StatusCreated, out)
}

// deleteBan godoc
// @Summary Lift a ban
// @Tags system
// @Param ip path string true "address"
// @Success 204
// @Router /system/banned-ips/{ip} [delete]
func (h *Handler) deleteBan(w http.ResponseWriter, r *http.Request) {
	ip := chi.URLParam(r, "ip")
	if err := h.Store.Unban(r.Context(), ip); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "ban.delete", "banned_ip", ip, nil, nil)
	if h.Renderer != nil {
		_ = h.Renderer.RenderACLs(r.Context())
	}
	h.publish(r.Context(), cache.ChanBansChanged, ip)
	w.WriteHeader(http.StatusNoContent)
}
