package admin

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/sipcapture"
	"github.com/imfanee/supersbc/internal/store"
)

func (h *Handler) mountCDRs(r chi.Router) {
	r.Get("/cdrs", h.listCDRs)
	r.Get("/cdrs/export", h.exportCDRs)
	r.Get("/cdrs/{id}", h.getCDR)
	r.Get("/cdrs/{id}/sip", h.getCDRSIP)
	r.Get("/cdrs/{id}/sip.pcap", h.getCDRSIPPcap)
	r.Get("/calls/active", h.activeCalls)
	r.With(operators).Delete("/calls/active/{id}", h.hangupCall)
}

func cdrFilter(r *http.Request) store.CDRFilter {
	q := r.URL.Query()
	f := store.CDRFilter{From: queryTime(r, "from"), To: queryTime(r, "to"), CustomerID: queryUUID(r, "customer_id"), CarrierID: queryUUID(r, "carrier_id"),
		Prefix: q.Get("prefix"), Caller: q.Get("caller"), Disposition: q.Get("disposition"), SIPCode: queryInt(r, "sip_code"),
		MinBillsec: queryInt(r, "min_billsec"), MaxBillsec: queryInt(r, "max_billsec"), SrcIP: q.Get("src_ip"), CallUUID: queryUUID(r, "call_uuid"), SIPCallID: q.Get("sip_call_id"),
		NegativeOnly: q.Get("negative_margin") == "true"}
	return f
}

// listCDRs godoc
// @Summary Search CDRs
// @Tags cdrs
// @Produce json
// @Param from query string false "RFC3339 or date"
// @Param to query string false "RFC3339 or date"
// @Param customer_id query string false "uuid"
// @Param carrier_id query string false "uuid"
// @Param prefix query string false "called number prefix"
// @Param caller query string false "caller prefix"
// @Param disposition query string false "answered|no_answer|busy|failed|rejected_auth|rejected_balance|rejected_route|cancelled"
// @Param sip_code query int false "final SIP code"
// @Param min_billsec query int false "min billsec"
// @Param max_billsec query int false "max billsec"
// @Param src_ip query string false "source ip"
// @Param negative_margin query bool false "only negative margin"
// @Param page query int false "page"
// @Param per_page query int false "per page"
// @Param sort query string false "start_time|billsec|sell_price|cost|margin|pdd_ms (prefix - for descending)"
// @Success 200 {object} map[string]any
// @Router /cdrs [get]
func (h *Handler) listCDRs(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.ListCDRs(r.Context(), cdrFilter(r), pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getCDR godoc
// @Summary One CDR with attempts and RTP statistics
// @Tags cdrs
// @Produce json
// @Param id path string true "call uuid"
// @Success 200 {object} store.CDRRow
// @Router /cdrs/{id} [get]
func (h *Handler) getCDR(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.CDRRowByUUID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// cdrColumn is one CSV column of the CDR export.
type cdrColumn struct {
	name string
	get  func(c store.CDRRow) string
}

// cdrColumns are every exportable column, in the order of the full export.
var cdrColumns = []cdrColumn{
	{"call_uuid", func(c store.CDRRow) string { return c.CallUUID.String() }},
	{"sip_call_id", func(c store.CDRRow) string { return deref(c.SIPCallID) }},
	{"start_time", func(c store.CDRRow) string { return ts(&c.StartTime) }},
	{"answer_time", func(c store.CDRRow) string { return ts(c.AnswerTime) }},
	{"end_time", func(c store.CDRRow) string { return ts(c.EndTime) }},
	{"customer", func(c store.CDRRow) string { return deref(c.CustomerName) }},
	{"carrier", func(c store.CDRRow) string { return deref(c.CarrierName) }},
	{"src_ip", func(c store.CDRRow) string { return deref(c.SrcIP) }},
	{"caller_raw", func(c store.CDRRow) string { return c.CallerNumberRaw }},
	{"called_raw", func(c store.CDRRow) string { return c.CalledNumberRaw }},
	{"caller", func(c store.CDRRow) string { return c.CallerNumber }},
	{"called", func(c store.CDRRow) string { return c.CalledNumber }},
	{"destination", func(c store.CDRRow) string { return deref(c.SellDestination) }},
	{"disposition", func(c store.CDRRow) string { return c.Disposition }},
	{"reject_reason", func(c store.CDRRow) string { return deref(c.RejectReason) }},
	{"sip_code", func(c store.CDRRow) string { return intStr(c.SIPFinalCode) }},
	{"sip_reason", func(c store.CDRRow) string { return deref(c.SIPFinalReason) }},
	{"hangup_cause", func(c store.CDRRow) string { return deref(c.HangupCause) }},
	{"pdd_ms", func(c store.CDRRow) string { return intStr(c.PDDMs) }},
	{"billsec", func(c store.CDRRow) string { return strconv.Itoa(c.Billsec) }},
	{"duration", func(c store.CDRRow) string { return strconv.Itoa(c.Duration) }},
	{"sell_rate_per_min", func(c store.CDRRow) string { return decStr(c.SellRatePerMin) }},
	{"sell_billed_seconds", func(c store.CDRRow) string { return strconv.Itoa(c.SellBilledSeconds) }},
	{"sell_price", func(c store.CDRRow) string { return c.SellPrice.StringFixed(6) }},
	{"sell_currency", func(c store.CDRRow) string { return deref(c.SellCurrency) }},
	{"buy_rate_per_min", func(c store.CDRRow) string { return decStr(c.BuyRatePerMin) }},
	{"buy_billed_seconds", func(c store.CDRRow) string { return strconv.Itoa(c.BuyBilledSeconds) }},
	{"cost", func(c store.CDRRow) string { return c.Cost.StringFixed(6) }},
	{"buy_currency", func(c store.CDRRow) string { return deref(c.BuyCurrency) }},
	{"margin", func(c store.CDRRow) string { return c.Margin.StringFixed(6) }},
	{"failover_depth", func(c store.CDRRow) string { return strconv.Itoa(c.FailoverDepth) }},
	{"attempts", func(c store.CDRRow) string { return strconv.Itoa(len(c.Attempts)) }},
	{"codec_in", func(c store.CDRRow) string { return deref(c.CodecIn) }},
	{"codec_out", func(c store.CDRRow) string { return deref(c.CodecOut) }},
	{"media_mode", func(c store.CDRRow) string { return deref(c.MediaMode) }},
	{"transport_in", func(c store.CDRRow) string { return deref(c.TransportIn) }},
	{"transport_out", func(c store.CDRRow) string { return deref(c.TransportOut) }},
	{"srtp_in", func(c store.CDRRow) string { return strconv.FormatBool(c.SRTPIn) }},
	{"srtp_out", func(c store.CDRRow) string { return strconv.FormatBool(c.SRTPOut) }},
	{"privacy", func(c store.CDRRow) string { return strconv.FormatBool(c.Privacy) }},
	{"stir_status", func(c store.CDRRow) string { return deref(c.STIRStatus) }},
	{"stir_attest", func(c store.CDRRow) string { return deref(c.STIRAttest) }},
	{"sbc_node", func(c store.CDRRow) string { return deref(c.SBCNode) }},
}

// cdrViews are the column subsets of the export: what a customer may see
// (no carrier, routing or cost data), what a carrier may see (no customer,
// routing or selling data), and everything.
var cdrViews = map[string][]string{
	"customer": {"call_uuid", "sip_call_id", "start_time", "answer_time", "end_time", "customer", "src_ip", "caller_raw", "called_raw", "caller", "called", "destination",
		"disposition", "reject_reason", "sip_code", "sip_reason", "hangup_cause", "pdd_ms", "billsec", "duration",
		"sell_rate_per_min", "sell_billed_seconds", "sell_price", "sell_currency", "codec_in", "transport_in", "srtp_in", "privacy", "stir_status", "stir_attest"},
	"carrier": {"call_uuid", "start_time", "answer_time", "end_time", "carrier", "caller", "called", "destination",
		"disposition", "sip_code", "sip_reason", "hangup_cause", "pdd_ms", "billsec", "duration",
		"buy_rate_per_min", "buy_billed_seconds", "cost", "buy_currency", "codec_out", "transport_out", "srtp_out"},
}

// cdrColumnsFor returns the columns of a view ("customer", "carrier", "full" or empty).
func cdrColumnsFor(view string) ([]cdrColumn, bool) {
	names, ok := cdrViews[view]
	if !ok {
		if view != "" && view != "full" {
			return nil, false
		}
		return cdrColumns, true
	}
	byName := map[string]cdrColumn{}
	for _, c := range cdrColumns {
		byName[c.name] = c
	}
	out := make([]cdrColumn, 0, len(names))
	for _, n := range names {
		out = append(out, byName[n])
	}
	return out, true
}

// exportCDRs godoc
// @Summary Stream CDRs as CSV with the same filters as the list; view=customer (no carrier, routing or cost columns), carrier (no customer, routing or selling columns) or full
// @Tags cdrs
// @Produce text/csv
// @Param view query string false "customer|carrier|full (default full)"
// @Success 200 {string} string "csv"
// @Router /cdrs/export [get]
func (h *Handler) exportCDRs(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	cols, ok := cdrColumnsFor(view)
	if !ok {
		fail(w, http.StatusBadRequest, "view must be customer, carrier or full")
		return
	}
	if view == "" {
		view = "full"
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "cdrs-"+view+"-"+time.Now().UTC().Format("20060102-150405")+".csv"))
	cw := csv.NewWriter(w)
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.name
	}
	_ = cw.Write(header)
	n := 0
	rec := make([]string, len(cols))
	err := h.Store.StreamCDRs(r.Context(), cdrFilter(r), func(c store.CDRRow) error {
		for i, col := range cols {
			rec[i] = col.get(c)
		}
		n++
		if n%500 == 0 {
			cw.Flush()
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		return cw.Write(rec)
	})
	cw.Flush()
	if err != nil {
		h.Log.Error("cdr export", "error", err)
	}
}

func ts(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func intStr(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

func decStr(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.StringFixed(6)
}

// activeCalls godoc
// @Summary Calls in progress (from active_calls, enriched from FreeSWITCH when connected)
// @Tags calls
// @Produce json
// @Success 200 {object} map[string]any
// @Router /calls/active [get]
func (h *Handler) activeCalls(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Store.ActiveCallRows(r.Context())
	if err != nil {
		failErr(w, err)
		return
	}
	live := map[string]map[string]any{}
	if h.ESL != nil && h.ESL.Connected() {
		if out, err := h.ESL.API(r.Context(), "show channels as json"); err == nil {
			var doc struct {
				Rows []map[string]any `json:"rows"`
			}
			if json.Unmarshal([]byte(out), &doc) == nil {
				for _, ch := range doc.Rows {
					if id, ok := ch["uuid"].(string); ok {
						live[id] = ch
					}
				}
			}
		}
	}
	type row struct {
		store.ActiveCallRow
		Elapsed int            `json:"elapsed_seconds"`
		Channel map[string]any `json:"channel,omitempty"`
		FSSeen  bool           `json:"freeswitch_seen"`
		State   string         `json:"state"`
	}
	out := make([]row, 0, len(rows))
	for _, a := range rows {
		x := row{ActiveCallRow: a, Elapsed: int(time.Since(a.StartedAt).Seconds()), State: "setup"}
		if a.AnswerTime != nil {
			x.State = "answered"
		}
		if ch, ok := live[a.CallUUID.String()]; ok {
			x.FSSeen = true
			x.Channel = map[string]any{"callstate": ch["callstate"], "read_codec": ch["read_codec"], "write_codec": ch["write_codec"], "dest": ch["dest"], "created": ch["created"]}
			if cs, ok := ch["callstate"].(string); ok && cs != "" {
				x.State = cs
			}
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out), "freeswitch_connected": h.ESL != nil && h.ESL.Connected()})
}

// hangupCall godoc
// @Summary Hang up a call in progress (uuid_kill)
// @Tags calls
// @Param id path string true "call uuid"
// @Success 204
// @Router /calls/active/{id} [delete]
func (h *Handler) hangupCall(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if h.ESL == nil || !h.ESL.Connected() {
		fail(w, http.StatusServiceUnavailable, "freeswitch not connected")
		return
	}
	if _, err := h.ESL.API(r.Context(), "uuid_kill "+id.String()+" MANAGER_REQUEST"); err != nil {
		fail(w, http.StatusNotFound, "channel not found: "+err.Error())
		return
	}
	h.audit(r, "call.hangup", "call", id.String(), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

// getCDRSIP godoc
// @Summary Every captured SIP message of a call (customer and carrier legs, headers and bodies) from the HOMER capture store (D-71)
// @Tags cdrs
// @Produce json
// @Param id path string true "call uuid"
// @Success 200 {object} sipcapture.Trace
// @Failure 503 {object} errorBody "capture store not configured or unreachable"
// @Router /cdrs/{id}/sip [get]
func (h *Handler) getCDRSIP(w http.ResponseWriter, r *http.Request) {
	tr, _, ok := h.loadSIPTrace(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// getCDRSIPPcap godoc
// @Summary Download the captured SIP of a call as a pcap file (leg=customer|carrier|all)
// @Tags cdrs
// @Produce application/vnd.tcpdump.pcap
// @Param id path string true "call uuid"
// @Param leg query string false "customer, carrier or all (default)"
// @Success 200 {file} binary
// @Failure 503 {object} errorBody "capture store not configured or unreachable"
// @Router /cdrs/{id}/sip.pcap [get]
func (h *Handler) getCDRSIPPcap(w http.ResponseWriter, r *http.Request) {
	tr, id, ok := h.loadSIPTrace(w, r)
	if !ok {
		return
	}
	leg := r.URL.Query().Get("leg")
	if leg == "all" || leg == "" {
		leg = ""
	} else if leg != "customer" && leg != "carrier" {
		fail(w, http.StatusBadRequest, "leg must be customer, carrier or all")
		return
	}
	name := "sip-" + id + ".pcap"
	if leg != "" {
		name = "sip-" + id + "-" + leg + ".pcap"
	}
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	if err := sipcapture.WritePCAP(w, tr.Messages, leg); err != nil {
		h.Log.Warn("pcap write", "call_uuid", id, "error", err)
	}
}

// loadSIPTrace loads the CDR and its captured messages; on failure it has
// already written the error response.
func (h *Handler) loadSIPTrace(w http.ResponseWriter, r *http.Request) (*sipcapture.Trace, string, bool) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return nil, "", false
	}
	if h.Capture == nil || !h.Capture.Enabled() {
		fail(w, http.StatusServiceUnavailable, "SIP capture store is not configured (SBC_HEP_DATABASE_URL); enable HEP capture and HOMER")
		return nil, "", false
	}
	c, err := h.Store.CDRRowByUUID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return nil, "", false
	}
	from := c.StartTime.Add(-30 * time.Second)
	to := c.StartTime.Add(h.Cfg.Billing.MaxCallDuration + time.Minute)
	if c.EndTime != nil {
		to = c.EndTime.Add(2 * time.Minute)
	}
	q := sipcapture.Query{CallUUID: id.String(), CustomerIP: deref(c.SrcIP), From: from, To: to}
	if c.SIPCallID != nil {
		q.CustomerCallID = *c.SIPCallID
	}
	tr, err := h.Capture.Trace(r.Context(), q)
	if err != nil {
		h.Log.Warn("sip capture lookup failed", "call_uuid", id, "error", err)
		fail(w, http.StatusServiceUnavailable, "SIP capture store unreachable: "+err.Error())
		return nil, "", false
	}
	return tr, id.String(), true
}
