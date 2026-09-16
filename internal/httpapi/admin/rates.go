package admin

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/rating"
)

func (h *Handler) mountRates(r chi.Router) {
	r.Get("/rate-groups", h.listRateGroups)
	r.With(operators).Post("/rate-groups", h.createRateGroup)
	r.Get("/rate-groups/{id}", h.getRateGroup)
	r.With(operators).Put("/rate-groups/{id}", h.updateRateGroup)
	r.With(operators).Delete("/rate-groups/{id}", h.deleteRateGroup)
	r.Get("/rate-groups/{id}/rates", h.listRates)
	r.With(operators).Post("/rate-groups/{id}/rates", h.createRate)
	r.With(operators).Put("/rate-groups/{id}/rates/{rateId}", h.updateRate)
	r.With(operators).Delete("/rate-groups/{id}/rates/{rateId}", h.deleteRate)
	r.With(operators).Post("/rate-groups/{id}/rates/bulk-delete", h.bulkDeleteRates)
	r.With(operators).Post("/rate-groups/{id}/rates/import", h.importRates)
	r.Get("/rate-groups/{id}/rates/export", h.exportRates)
	r.Get("/rate-groups/{id}/rates/test", h.testRate)
	r.Get("/fx-rates", h.listFX)
	r.With(admins).Post("/fx-rates", h.addFX)
}

type rateGroupInput struct {
	Name        string `json:"name" validate:"required,min=1,max=100"`
	Currency    string `json:"currency" validate:"omitempty,len=3"`
	Description string `json:"description" validate:"max=500"`
}

// listRateGroups godoc
// @Summary List rate groups (rate decks)
// @Tags rates
// @Produce json
// @Param search query string false "name substring"
// @Success 200 {object} map[string]any
// @Router /rate-groups [get]
func (h *Handler) listRateGroups(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.ListRateGroups(r.Context(), r.URL.Query().Get("search"), pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// createRateGroup godoc
// @Summary Create a rate group
// @Tags rates
// @Accept json
// @Produce json
// @Param body body rateGroupInput true "rate group"
// @Success 201 {object} model.RateGroup
// @Router /rate-groups [post]
func (h *Handler) createRateGroup(w http.ResponseWriter, r *http.Request) {
	var in rateGroupInput
	if !h.decode(w, r, &in) {
		return
	}
	cur := strings.ToUpper(in.Currency)
	if cur == "" {
		cur = h.Cfg.Billing.Currency
	}
	out, err := h.Store.CreateRateGroup(r.Context(), in.Name, cur, in.Description)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "rate_group.create", "rate_group", out.ID.String(), nil, out)
	writeJSON(w, http.StatusCreated, out)
}

// getRateGroup godoc
// @Summary Get a rate group
// @Tags rates
// @Produce json
// @Param id path string true "rate group id"
// @Success 200 {object} model.RateGroup
// @Router /rate-groups/{id} [get]
func (h *Handler) getRateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.RateGroupByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// updateRateGroup godoc
// @Summary Update a rate group
// @Tags rates
// @Accept json
// @Produce json
// @Param id path string true "rate group id"
// @Param body body rateGroupInput true "rate group"
// @Success 200 {object} model.RateGroup
// @Router /rate-groups/{id} [put]
func (h *Handler) updateRateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	before, err := h.Store.RateGroupByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	var in rateGroupInput
	if !h.decode(w, r, &in) {
		return
	}
	cur := strings.ToUpper(in.Currency)
	if cur == "" {
		cur = before.Currency
	}
	out, err := h.Store.UpdateRateGroup(r.Context(), id, in.Name, cur, in.Description)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "rate_group.update", "rate_group", id.String(), before, out)
	h.ratesChanged(r, id)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) ratesChanged(r *http.Request, groupID uuid.UUID) {
	h.Tables.InvalidateRates(groupID)
	h.publish(r.Context(), cache.ChanRateDeckChanged, groupID.String())
}

// deleteRateGroup godoc
// @Summary Delete an unused rate group
// @Tags rates
// @Param id path string true "rate group id"
// @Success 204
// @Router /rate-groups/{id} [delete]
func (h *Handler) deleteRateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.Store.DeleteRateGroup(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "rate_group.delete", "rate_group", id.String(), nil, nil)
	h.ratesChanged(r, id)
	w.WriteHeader(http.StatusNoContent)
}

// listRates godoc
// @Summary List rates of a group
// @Tags rates
// @Produce json
// @Param id path string true "rate group id"
// @Param search query string false "prefix or destination"
// @Param enabled query bool false "only enabled"
// @Success 200 {object} map[string]any
// @Router /rate-groups/{id}/rates [get]
func (h *Handler) listRates(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Store.ListRates(r.Context(), id, r.URL.Query().Get("search"), r.URL.Query().Get("enabled") == "true", pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type rateInput struct {
	Prefix              string  `json:"prefix" validate:"required,numeric,max=15"`
	Destination         string  `json:"destination" validate:"max=200"`
	RatePerMin          string  `json:"rate_per_min" validate:"required"`
	ConnectFee          string  `json:"connect_fee"`
	InitialIncrement    int     `json:"initial_increment" validate:"omitempty,min=1"`
	SubsequentIncrement int     `json:"subsequent_increment" validate:"omitempty,min=1"`
	MinDuration         int     `json:"min_duration" validate:"min=0"`
	EffectiveFrom       *string `json:"effective_from"`
	EffectiveTo         *string `json:"effective_to"`
	Enabled             *bool   `json:"enabled"`
}

func (in rateInput) toModel(groupID uuid.UUID) (*model.Rate, error) {
	rate, err := decimal.NewFromString(in.RatePerMin)
	if err != nil || rate.IsNegative() {
		return nil, fmt.Errorf("rate_per_min must be a non-negative decimal string")
	}
	fee := decimal.Zero
	if in.ConnectFee != "" {
		fee, err = decimal.NewFromString(in.ConnectFee)
		if err != nil || fee.IsNegative() {
			return nil, fmt.Errorf("connect_fee must be a non-negative decimal string")
		}
	}
	r := &model.Rate{RateGroupID: groupID, Prefix: in.Prefix, Destination: in.Destination, RatePerMin: rate, ConnectFee: fee,
		InitialIncrement: in.InitialIncrement, SubsequentIncrement: in.SubsequentIncrement, MinDuration: in.MinDuration, EffectiveFrom: time.Now().Truncate(time.Second), Enabled: true}
	if r.InitialIncrement == 0 {
		r.InitialIncrement = 60
	}
	if r.SubsequentIncrement == 0 {
		r.SubsequentIncrement = 60
	}
	if in.EffectiveFrom != nil && *in.EffectiveFrom != "" {
		t, err := parseTime(*in.EffectiveFrom)
		if err != nil {
			return nil, fmt.Errorf("effective_from: %w", err)
		}
		r.EffectiveFrom = t
	}
	if in.EffectiveTo != nil && *in.EffectiveTo != "" {
		t, err := parseTime(*in.EffectiveTo)
		if err != nil {
			return nil, fmt.Errorf("effective_to: %w", err)
		}
		r.EffectiveTo = &t
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	return r, nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", s)
}

// createRate godoc
// @Summary Add a rate row
// @Tags rates
// @Accept json
// @Produce json
// @Param id path string true "rate group id"
// @Param body body rateInput true "rate"
// @Success 201 {object} model.Rate
// @Router /rate-groups/{id}/rates [post]
func (h *Handler) createRate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in rateInput
	if !h.decode(w, r, &in) {
		return
	}
	m, err := in.toModel(id)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := h.Store.UpsertRate(r.Context(), m)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "rate.create", "rate", out.ID.String(), nil, out)
	h.ratesChanged(r, id)
	writeJSON(w, http.StatusCreated, out)
}

// updateRate godoc
// @Summary Update a rate row
// @Tags rates
// @Accept json
// @Produce json
// @Param id path string true "rate group id"
// @Param rateId path string true "rate id"
// @Param body body rateInput true "rate"
// @Success 200 {object} model.Rate
// @Router /rate-groups/{id}/rates/{rateId} [put]
func (h *Handler) updateRate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rateID, ok := pathUUID(w, r, "rateId")
	if !ok {
		return
	}
	before, err := h.Store.RateByID(r.Context(), rateID)
	if err != nil || before.RateGroupID != id {
		fail(w, http.StatusNotFound, "rate not found")
		return
	}
	var in rateInput
	if !h.decode(w, r, &in) {
		return
	}
	m, err := in.toModel(id)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID = rateID
	if in.EffectiveFrom == nil {
		m.EffectiveFrom = before.EffectiveFrom
	}
	out, err := h.Store.UpdateRate(r.Context(), m)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "rate.update", "rate", rateID.String(), before, out)
	h.ratesChanged(r, id)
	writeJSON(w, http.StatusOK, out)
}

// deleteRate godoc
// @Summary Delete a rate row
// @Tags rates
// @Param id path string true "rate group id"
// @Param rateId path string true "rate id"
// @Success 204
// @Router /rate-groups/{id}/rates/{rateId} [delete]
func (h *Handler) deleteRate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rateID, ok := pathUUID(w, r, "rateId")
	if !ok {
		return
	}
	n, err := h.Store.DeleteRates(r.Context(), id, []uuid.UUID{rateID})
	if err != nil {
		failErr(w, err)
		return
	}
	if n == 0 {
		fail(w, http.StatusNotFound, "rate not found")
		return
	}
	h.audit(r, "rate.delete", "rate", rateID.String(), nil, nil)
	h.ratesChanged(r, id)
	w.WriteHeader(http.StatusNoContent)
}

type bulkDeleteInput struct {
	IDs []string `json:"ids" validate:"required,min=1,dive,uuid"`
}

// bulkDeleteRates godoc
// @Summary Delete several rate rows
// @Tags rates
// @Accept json
// @Produce json
// @Param id path string true "rate group id"
// @Param body body bulkDeleteInput true "ids"
// @Success 200 {object} map[string]int64
// @Router /rate-groups/{id}/rates/bulk-delete [post]
func (h *Handler) bulkDeleteRates(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in bulkDeleteInput
	if !h.decode(w, r, &in) {
		return
	}
	ids := make([]uuid.UUID, 0, len(in.IDs))
	for _, s := range in.IDs {
		u, _ := uuid.Parse(s)
		ids = append(ids, u)
	}
	n, err := h.Store.DeleteRates(r.Context(), id, ids)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "rate.bulk_delete", "rate_group", id.String(), nil, map[string]any{"deleted": n})
	h.ratesChanged(r, id)
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}

// ---- CSV import / export ----

// csvHeader is the canonical column order. Import accepts any order by name;
// unknown columns are ignored; missing optional columns take defaults.
var csvHeader = []string{"prefix", "destination", "rate_per_min", "connect_fee", "initial_increment", "subsequent_increment", "min_duration", "effective_from", "effective_to", "enabled"}

// ImportRow is the validation result of one CSV line.
type ImportRow struct {
	Line  int         `json:"line"`
	Rate  *model.Rate `json:"rate,omitempty"`
	Error string      `json:"error,omitempty"`
}

// ImportPreview is the response of a dry run or an import.
type ImportPreview struct {
	Total    int         `json:"total"`
	Valid    int         `json:"valid"`
	Invalid  int         `json:"invalid"`
	Imported int         `json:"imported"`
	DryRun   bool        `json:"dry_run"`
	Replace  bool        `json:"replace"`
	Errors   []ImportRow `json:"errors"`
	Sample   []ImportRow `json:"sample"`
}

// parseRatesCSV validates CSV content into rates.
func parseRatesCSV(groupID uuid.UUID, rd io.Reader, defaultFrom time.Time) ([]model.Rate, []ImportRow, int) {
	cr := csv.NewReader(rd)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, []ImportRow{{Line: 1, Error: "cannot read header: " + err.Error()}}, 0
	}
	col := map[string]int{}
	for i, hname := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(hname, "\ufeff")))] = i
	}
	if _, ok := col["prefix"]; !ok {
		return nil, []ImportRow{{Line: 1, Error: "header must contain a prefix column"}}, 0
	}
	rateCol, ok := col["rate_per_min"]
	if !ok {
		if rateCol, ok = col["rate"]; !ok {
			return nil, []ImportRow{{Line: 1, Error: "header must contain rate_per_min (or rate)"}}, 0
		}
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var rates []model.Rate
	var bad []ImportRow
	line := 1
	total := 0
	seen := map[string]bool{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			bad = append(bad, ImportRow{Line: line, Error: err.Error()})
			total++
			continue
		}
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}
		total++
		in := rateInput{Prefix: get(rec, "prefix"), Destination: get(rec, "destination"), RatePerMin: strings.TrimSpace(rec[rateCol]), ConnectFee: get(rec, "connect_fee")}
		in.InitialIncrement, _ = strconv.Atoi(get(rec, "initial_increment"))
		in.SubsequentIncrement, _ = strconv.Atoi(get(rec, "subsequent_increment"))
		in.MinDuration, _ = strconv.Atoi(get(rec, "min_duration"))
		if v := get(rec, "effective_from"); v != "" {
			in.EffectiveFrom = &v
		}
		if v := get(rec, "effective_to"); v != "" {
			in.EffectiveTo = &v
		}
		if v := strings.ToLower(get(rec, "enabled")); v != "" {
			b := v == "true" || v == "1" || v == "yes" || v == "y"
			in.Enabled = &b
		}
		if in.Prefix == "" || strings.Trim(in.Prefix, "0123456789") != "" || len(in.Prefix) > 15 {
			bad = append(bad, ImportRow{Line: line, Error: fmt.Sprintf("prefix %q must be 1 to 15 digits", in.Prefix)})
			continue
		}
		m, err := in.toModel(groupID)
		if err != nil {
			bad = append(bad, ImportRow{Line: line, Error: err.Error()})
			continue
		}
		if in.EffectiveFrom == nil {
			m.EffectiveFrom = defaultFrom
		}
		key := m.Prefix + "@" + m.EffectiveFrom.Format(time.RFC3339)
		if seen[key] {
			bad = append(bad, ImportRow{Line: line, Error: "duplicate prefix " + m.Prefix + " with the same effective_from"})
			continue
		}
		seen[key] = true
		rates = append(rates, *m)
	}
	return rates, bad, total
}

// importRates godoc
// @Summary Import a CSV rate deck (multipart file or raw text/csv body); ?dry_run=true validates only; ?replace=true replaces the deck
// @Tags rates
// @Accept mpfd
// @Produce json
// @Param id path string true "rate group id"
// @Param file formData file true "csv"
// @Param dry_run query bool false "validate only"
// @Param replace query bool false "replace the whole deck"
// @Success 200 {object} ImportPreview
// @Router /rate-groups/{id}/rates/import [post]
func (h *Handler) importRates(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := h.Store.RateGroupByID(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	var body io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			fail(w, http.StatusBadRequest, "bad multipart body")
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			fail(w, http.StatusBadRequest, "file field missing")
			return
		}
		defer func() { _ = f.Close() }()
		body = f
	}
	dry := r.URL.Query().Get("dry_run") == "true"
	replace := r.URL.Query().Get("replace") == "true"
	rates, bad, total := parseRatesCSV(id, body, time.Now().Truncate(time.Second))
	prev := ImportPreview{Total: total, Valid: len(rates), Invalid: len(bad), DryRun: dry, Replace: replace, Errors: bad}
	for i := 0; i < len(rates) && i < 10; i++ {
		rr := rates[i]
		prev.Sample = append(prev.Sample, ImportRow{Line: i + 2, Rate: &rr})
	}
	if prev.Errors == nil {
		prev.Errors = []ImportRow{}
	}
	if dry || len(bad) > 0 && r.URL.Query().Get("partial") != "true" {
		if len(bad) > 0 && !dry {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		writeJSON(w, http.StatusOK, prev)
		return
	}
	n, err := h.Store.ImportRates(r.Context(), id, rates, replace)
	if err != nil {
		failErr(w, err)
		return
	}
	prev.Imported = n
	h.audit(r, "rate.import", "rate_group", id.String(), nil, map[string]any{"imported": n, "replace": replace, "invalid": len(bad)})
	h.ratesChanged(r, id)
	writeJSON(w, http.StatusOK, prev)
}

// exportRates godoc
// @Summary Export a rate deck as CSV
// @Tags rates
// @Produce text/csv
// @Param id path string true "rate group id"
// @Success 200 {string} string "csv"
// @Router /rate-groups/{id}/rates/export [get]
func (h *Handler) exportRates(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rg, err := h.Store.RateGroupByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	rates, err := h.Store.AllRates(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rg.Name+"-rates.csv"))
	cw := csv.NewWriter(w)
	_ = cw.Write(csvHeader)
	for _, x := range rates {
		to := ""
		if x.EffectiveTo != nil {
			to = x.EffectiveTo.UTC().Format(time.RFC3339)
		}
		_ = cw.Write([]string{x.Prefix, x.Destination, x.RatePerMin.StringFixed(6), x.ConnectFee.StringFixed(6), strconv.Itoa(x.InitialIncrement),
			strconv.Itoa(x.SubsequentIncrement), strconv.Itoa(x.MinDuration), x.EffectiveFrom.UTC().Format(time.RFC3339), to, strconv.FormatBool(x.Enabled)})
	}
	cw.Flush()
}

// testRate godoc
// @Summary Longest-prefix match of a number in a rate group (trie and SQL must agree)
// @Tags rates
// @Produce json
// @Param id path string true "rate group id"
// @Param number query string true "E.164 digits"
// @Success 200 {object} map[string]any
// @Router /rate-groups/{id}/rates/test [get]
func (h *Handler) testRate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	number := strings.TrimSpace(r.URL.Query().Get("number"))
	if number == "" {
		fail(w, http.StatusBadRequest, "number required")
		return
	}
	trieRate, matched, err := h.Tables.MatchRate(r.Context(), id, number)
	if err != nil {
		failErr(w, err)
		return
	}
	sqlRate, sqlErr := h.Store.LongestPrefixSQL(r.Context(), id, number, time.Now())
	out := map[string]any{"number": number, "matched": matched, "rate": trieRate}
	if sqlErr == nil {
		out["sql_agrees"] = trieRate != nil && sqlRate.ID == trieRate.ID
	} else {
		out["sql_agrees"] = !matched
	}
	if trieRate != nil {
		rr := rating.Rate{PerMinute: trieRate.RatePerMin, ConnectFee: trieRate.ConnectFee, Increments: rating.Increments{Initial: trieRate.InitialIncrement, Subsequent: trieRate.SubsequentIncrement, MinDuration: trieRate.MinDuration}}
		_, oneMin := rating.Price(60, rr)
		out["price_60s"] = oneMin.StringFixed(6)
		out["reserve_amount"] = rating.ReserveAmount(h.Cfg.Billing.ReserveMinutes, rr).StringFixed(6)
	}
	writeJSON(w, http.StatusOK, out)
}

type fxInput struct {
	Base          string  `json:"base" validate:"required,len=3"`
	Quote         string  `json:"quote" validate:"required,len=3"`
	Rate          string  `json:"rate" validate:"required"`
	EffectiveFrom *string `json:"effective_from"`
}

// listFX godoc
// @Summary Current exchange rates
// @Tags rates
// @Produce json
// @Success 200 {array} model.FXRate
// @Router /fx-rates [get]
func (h *Handler) listFX(w http.ResponseWriter, r *http.Request) {
	out, err := h.Store.FXRates(r.Context())
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addFX godoc
// @Summary Add an exchange rate (1 base = rate quote)
// @Tags rates
// @Accept json
// @Produce json
// @Param body body fxInput true "rate"
// @Success 201 {object} model.FXRate
// @Router /fx-rates [post]
func (h *Handler) addFX(w http.ResponseWriter, r *http.Request) {
	var in fxInput
	if !h.decode(w, r, &in) {
		return
	}
	if d, err := decimal.NewFromString(in.Rate); err != nil || !d.IsPositive() {
		fail(w, http.StatusBadRequest, "rate must be a positive decimal string")
		return
	}
	from := time.Now()
	if in.EffectiveFrom != nil && *in.EffectiveFrom != "" {
		t, err := parseTime(*in.EffectiveFrom)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		from = t
	}
	by := principal(r.Context()).Email
	out, err := h.Store.AddFXRate(r.Context(), in.Base, in.Quote, in.Rate, from, &by)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "fx.add", "fx_rate", out.ID.String(), nil, out)
	h.Tables.InvalidateFX()
	h.publish(r.Context(), cache.ChanFXChanged, out.ID.String())
	writeJSON(w, http.StatusCreated, out)
}
