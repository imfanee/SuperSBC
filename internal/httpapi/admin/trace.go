package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/opensbc/opensbc/internal/trace"
)

func (h *Handler) mountTrace(r chi.Router) {
	r.Get("/cdrs/{id}/trace", h.callTrace)
	r.Get("/system/siptrace", h.sipTraceState)
	r.With(operators).Post("/system/siptrace", h.sipTraceEnable)
	r.With(operators).Delete("/system/siptrace", h.sipTraceDisable)
	r.Get("/system/siptrace/messages", h.sipTraceMessages)
}

// callTrace godoc
// @Summary Per-call trace: CDR, ledger entries, sbc-api log lines and FreeSWITCH log lines (Lua and Sofia) for one call uuid
// @Tags cdrs
// @Produce json
// @Param id path string true "call uuid"
// @Success 200 {object} map[string]any
// @Router /cdrs/{id}/trace [get]
func (h *Handler) callTrace(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	cdr, err := h.Store.CDRRowByUUID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	var ledger []any
	rows, _ := h.Store.Pool().Query(r.Context(), `SELECT l.id, l.type::text, l.amount::text, l.balance_after::text, l.description, l.created_at, a.owner_type::text, a.owner_id
		FROM ledger_entries l JOIN accounts a ON a.id = l.account_id WHERE l.call_uuid = $1 ORDER BY l.id`, id)
	if rows != nil {
		for rows.Next() {
			var lid int64
			var typ, amount, after, desc, ownerType string
			var at time.Time
			var ownerID string
			if err := rows.Scan(&lid, &typ, &amount, &after, &desc, &at, &ownerType, &ownerID); err == nil {
				ledger = append(ledger, map[string]any{"id": lid, "type": typ, "amount": amount, "balance_after": after, "description": desc, "created_at": at, "owner_type": ownerType, "owner_id": ownerID})
			}
		}
		rows.Close()
	}
	if ledger == nil {
		ledger = []any{}
	}
	var apiLines, fsLines []string
	if h.Trace != nil {
		apiLines = h.Trace.APILines(r.Context(), id.String())
		fsLines = h.Trace.FreeSWITCHLines(id.String(), 400)
	}
	if apiLines == nil {
		apiLines = []string{}
	}
	if fsLines == nil {
		fsLines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"cdr": cdr, "ledger": ledger, "api_lines": apiLines, "freeswitch_lines": fsLines})
}

// sipTraceState godoc
// @Summary SIP trace toggle state
// @Tags system
// @Produce json
// @Success 200 {object} trace.SIPTraceState
// @Router /system/siptrace [get]
func (h *Handler) sipTraceState(w http.ResponseWriter, _ *http.Request) {
	if h.Trace == nil {
		writeJSON(w, http.StatusOK, trace.SIPTraceState{})
		return
	}
	writeJSON(w, http.StatusOK, h.Trace.State())
}

type sipTraceInput struct {
	Scope   string `json:"scope" validate:"omitempty,oneof=global external-ingress external-egress"`
	Minutes int    `json:"minutes" validate:"min=1,max=120"`
}

// sipTraceEnable godoc
// @Summary Enable Sofia SIP tracing for N minutes (global or one profile); messages land in the FreeSWITCH log
// @Tags system
// @Accept json
// @Param body body sipTraceInput true "scope and minutes"
// @Success 200 {object} trace.SIPTraceState
// @Router /system/siptrace [post]
func (h *Handler) sipTraceEnable(w http.ResponseWriter, r *http.Request) {
	var in sipTraceInput
	if !h.decode(w, r, &in) {
		return
	}
	if in.Scope == "" {
		in.Scope = "external-ingress"
	}
	if h.Trace == nil || h.ESL == nil || !h.ESL.Connected() {
		fail(w, http.StatusServiceUnavailable, "freeswitch not connected")
		return
	}
	if err := h.Trace.EnableSIPTrace(r.Context(), in.Scope, time.Duration(in.Minutes)*time.Minute); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "siptrace.enable", "system", in.Scope, nil, in)
	writeJSON(w, http.StatusOK, h.Trace.State())
}

// sipTraceDisable godoc
// @Summary Disable SIP tracing
// @Tags system
// @Success 204
// @Router /system/siptrace [delete]
func (h *Handler) sipTraceDisable(w http.ResponseWriter, r *http.Request) {
	if h.Trace != nil {
		_ = h.Trace.DisableSIPTrace(r.Context())
	}
	h.audit(r, "siptrace.disable", "system", "", nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// sipTraceMessages godoc
// @Summary Traced SIP messages involving one address (customer or carrier IP) from the FreeSWITCH log
// @Tags system
// @Produce json
// @Param ip query string true "address"
// @Param limit query int false "max messages (default 200)"
// @Success 200 {object} map[string]any
// @Router /system/siptrace/messages [get]
func (h *Handler) sipTraceMessages(w http.ResponseWriter, r *http.Request) {
	ip := r.URL.Query().Get("ip")
	if ip == "" {
		fail(w, http.StatusBadRequest, "ip required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	var msgs []string
	if h.Trace != nil {
		msgs = h.Trace.SIPMessages(ip, limit)
	}
	if msgs == nil {
		msgs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": ip, "messages": msgs, "state": h.Trace.State()})
}
