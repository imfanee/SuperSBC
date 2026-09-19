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

var cdrCSVHeader = []string{"call_uuid", "start_time", "answer_time", "end_time", "customer", "carrier", "src_ip", "caller", "called", "disposition", "sip_code", "sip_reason",
	"hangup_cause", "pdd_ms", "billsec", "duration", "sell_rate_per_min", "sell_billed_seconds", "sell_price", "sell_currency", "buy_rate_per_min", "buy_billed_seconds", "cost", "buy_currency", "margin",
	"failover_depth", "codec_in", "codec_out", "media_mode", "sbc_node"}

// exportCDRs godoc
// @Summary Stream CDRs as CSV with the same filters as the list
// @Tags cdrs
// @Produce text/csv
// @Success 200 {string} string "csv"
// @Router /cdrs/export [get]
func (h *Handler) exportCDRs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "cdrs-"+time.Now().UTC().Format("20060102-150405")+".csv"))
	cw := csv.NewWriter(w)
	_ = cw.Write(cdrCSVHeader)
	n := 0
	err := h.Store.StreamCDRs(r.Context(), cdrFilter(r), func(c store.CDRRow) error {
		rec := []string{c.CallUUID.String(), ts(&c.StartTime), ts(c.AnswerTime), ts(c.EndTime), deref(c.CustomerName), deref(c.CarrierName), deref(c.SrcIP), c.CallerNumber, c.CalledNumber,
			c.Disposition, intStr(c.SIPFinalCode), deref(c.SIPFinalReason), deref(c.HangupCause), intStr(c.PDDMs), strconv.Itoa(c.Billsec), strconv.Itoa(c.Duration),
			decStr(c.SellRatePerMin), strconv.Itoa(c.SellBilledSeconds), c.SellPrice.StringFixed(6), deref(c.SellCurrency), decStr(c.BuyRatePerMin), strconv.Itoa(c.BuyBilledSeconds), c.Cost.StringFixed(6), deref(c.BuyCurrency),
			c.Margin.StringFixed(6), strconv.Itoa(c.FailoverDepth), deref(c.CodecIn), deref(c.CodecOut), deref(c.MediaMode), deref(c.SBCNode)}
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
