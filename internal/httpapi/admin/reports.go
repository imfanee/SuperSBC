package admin

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/imfanee/supersbc/internal/reports"
)

// ReportsHandler serves /reports/* (Section 10).
type ReportsHandler struct {
	svc        *reports.Service
	lowBalance string
	h          *Handler
}

// NewReports creates the reports handler.
func NewReports(svc *reports.Service, lowBalance string) *ReportsHandler {
	return &ReportsHandler{svc: svc, lowBalance: lowBalance}
}

// Mount registers the routes (called from Handler.Mount).
func (rh *ReportsHandler) Mount(r chi.Router) {
	r.Get("/reports/summary", rh.summary)
	r.Get("/reports/traffic", rh.traffic)
	r.Get("/reports/quality", rh.quality)
	r.Get("/reports/statement", rh.statement)
	r.Get("/reports/peaks", rh.peaks)
}

func rangeParams(r *http.Request) reports.Range {
	now := time.Now().UTC()
	from := now.Truncate(24 * time.Hour)
	to := now.Add(time.Minute)
	if t := queryTime(r, "from"); t != nil {
		from = *t
	}
	if t := queryTime(r, "to"); t != nil {
		to = *t
	}
	return reports.Range{From: from, To: to, CustomerID: queryUUID(r, "customer_id"), CarrierID: queryUUID(r, "carrier_id")}
}

// summary godoc
// @Summary Dashboard: today's KPIs, hourly profile, top destinations, rejections, live calls, low balance customers
// @Tags reports
// @Produce json
// @Param from query string false "start (default today 00:00 UTC)"
// @Param to query string false "end"
// @Success 200 {object} reports.Summary
// @Router /reports/summary [get]
func (rh *ReportsHandler) summary(w http.ResponseWriter, r *http.Request) {
	out, err := rh.svc.Summary(r.Context(), rangeParams(r), rh.lowBalance)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// traffic godoc
// @Summary Traffic report: attempts, ASR, ACD, NER, minutes, revenue, cost, margin, PDD grouped by any dimension; ?format=csv exports
// @Tags reports
// @Produce json
// @Param from query string false "start"
// @Param to query string false "end"
// @Param group_by query string false "total|customer|carrier|destination|prefix:N|hour|day|month|hour_of_day|sip_code|hangup_cause|disposition|src_ip|failover_depth"
// @Param order_by query string false "attempts|key|minutes|revenue|cost|margin|asr"
// @Param limit query int false "max rows"
// @Param customer_id query string false "scope to a customer"
// @Param carrier_id query string false "scope to a carrier"
// @Param format query string false "csv"
// @Success 200 {array} reports.Row
// @Router /reports/traffic [get]
func (rh *ReportsHandler) traffic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	rows, err := rh.svc.Traffic(r.Context(), rangeParams(r), q.Get("group_by"), q.Get("order_by"), limit)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if rows == nil {
		rows = []reports.Row{}
	}
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "traffic-"+q.Get("group_by")+".csv"))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"key", "label", "attempts", "answered", "rejected", "failed", "asr", "ner", "acd", "minutes", "revenue", "cost", "margin", "margin_pct", "pdd_avg_ms", "pdd_p95_ms", "short_calls"})
		for _, x := range rows {
			_ = cw.Write([]string{x.Key, x.Label, strconv.FormatInt(x.Attempts, 10), strconv.FormatInt(x.Answered, 10), strconv.FormatInt(x.Rejected, 10), strconv.FormatInt(x.Failed, 10),
				fmt.Sprintf("%.4f", x.ASR), fmt.Sprintf("%.4f", x.NER), fmt.Sprintf("%.1f", x.ACD), fmt.Sprintf("%.2f", x.Minutes), x.Revenue, x.Cost, x.Margin, fmt.Sprintf("%.4f", x.MarginPct),
				fmt.Sprintf("%.0f", x.PDDAvg), fmt.Sprintf("%.0f", x.PDDP95), strconv.FormatInt(x.ShortCalls, 10)})
		}
		cw.Flush()
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// quality godoc
// @Summary Quality report per group and day: ASR, ACD, PDD, MOS, loss, jitter, short call ratio, false answer indicator
// @Tags reports
// @Produce json
// @Param from query string false "start"
// @Param to query string false "end"
// @Param group_by query string false "carrier|customer|destination|total"
// @Success 200 {array} reports.QualityRow
// @Router /reports/quality [get]
func (rh *ReportsHandler) quality(w http.ResponseWriter, r *http.Request) {
	gb := r.URL.Query().Get("group_by")
	if gb == "" {
		gb = "carrier"
	}
	rows, err := rh.svc.Quality(r.Context(), rangeParams(r), gb)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if rows == nil {
		rows = []reports.QualityRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// statement godoc
// @Summary Balance statement of an account for a period (opening, movements, closing)
// @Tags reports
// @Produce json
// @Param owner_type query string true "customer|carrier"
// @Param owner_id query string true "owner id"
// @Param from query string false "start"
// @Param to query string false "end"
// @Param lines query bool false "include ledger lines"
// @Success 200 {object} reports.Statement
// @Router /reports/statement [get]
func (rh *ReportsHandler) statement(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	owner := queryUUID(r, "owner_id")
	if owner == nil || (q.Get("owner_type") != "customer" && q.Get("owner_type") != "carrier") {
		fail(w, http.StatusBadRequest, "owner_type and owner_id required")
		return
	}
	acc, err := rh.h.Store.AccountByOwner(r.Context(), q.Get("owner_type"), *owner)
	if err != nil {
		failErr(w, err)
		return
	}
	rg := rangeParams(r)
	st, err := rh.svc.Statement(r.Context(), acc.ID, rg.From, rg.To, q.Get("lines") != "false")
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// peaks godoc
// @Summary Peak concurrent calls and peak CPS per customer, carrier or system
// @Tags reports
// @Produce json
// @Param from query string false "start"
// @Param to query string false "end"
// @Param group_by query string false "customer|carrier|system"
// @Success 200 {array} reports.PeakRow
// @Router /reports/peaks [get]
func (rh *ReportsHandler) peaks(w http.ResponseWriter, r *http.Request) {
	rows, err := rh.svc.Peaks(r.Context(), rangeParams(r), r.URL.Query().Get("group_by"))
	if err != nil {
		failErr(w, err)
		return
	}
	if rows == nil {
		rows = []reports.PeakRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}
