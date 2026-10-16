package admin

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/imfanee/supersbc/internal/cache"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/store"
)

func (h *Handler) mountAreaCodes(r chi.Router) {
	r.Get("/area-codes", h.listAreaCodes)
	r.Get("/area-codes/lookup", h.lookupAreaCode)
	r.With(operators).Post("/area-codes", h.createAreaCode)
	r.With(operators).Put("/area-codes/{id}", h.updateAreaCode)
	r.With(operators).Delete("/area-codes/{id}", h.deleteAreaCode)
	r.With(operators).Post("/area-codes/import", h.importAreaCodes)
	r.With(operators).Post("/area-codes/backfill", h.backfillAreaCodes)
}

// listAreaCodes godoc
// @Summary List area codes (country code, area code, description, operator)
// @Tags area-codes
// @Produce json
// @Param search query string false "prefix, description, operator or country"
// @Param country_code query string false "country code"
// @Param kind query string false "mobile|fixed|other"
// @Success 200 {object} store.Listing[model.AreaCode]
// @Router /area-codes [get]
func (h *Handler) listAreaCodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.Store.AreaCodes(r.Context(), store.AreaCodeFilter{
		Search: q.Get("search"), CountryCode: q.Get("country_code"), Kind: q.Get("kind"),
	}, pageParams(r))
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// lookupAreaCode godoc
// @Summary The area code entry that matches a number (longest prefix first)
// @Tags area-codes
// @Produce json
// @Param number query string true "number in E.164 digits"
// @Success 200 {object} model.AreaCode
// @Router /area-codes/lookup [get]
func (h *Handler) lookupAreaCode(w http.ResponseWriter, r *http.Request) {
	number := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("number")), "+")
	if number == "" {
		fail(w, http.StatusBadRequest, "number required")
		return
	}
	a, err := h.Tables.Area(r.Context(), number)
	if err != nil {
		failErr(w, err)
		return
	}
	if a == nil {
		writeJSON(w, http.StatusOK, map[string]any{"number": number, "match": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"number": number, "match": a})
}

type areaCodeInput struct {
	CountryCode string `json:"country_code" validate:"required,numeric,max=4"`
	AreaCode    string `json:"area_code" validate:"omitempty,numeric,max=10"`
	Country     string `json:"country" validate:"max=100"`
	Description string `json:"description" validate:"required,max=200"`
	Operator    string `json:"operator" validate:"max=200"`
	Kind        string `json:"kind" validate:"omitempty,oneof=mobile fixed other"`
	Enabled     *bool  `json:"enabled"`
	Notes       string `json:"notes" validate:"max=500"`
}

func (in areaCodeInput) toModel() *model.AreaCode {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return &model.AreaCode{CountryCode: in.CountryCode, AreaCode: in.AreaCode, Country: in.Country,
		Description: in.Description, Operator: in.Operator, Kind: in.Kind, Enabled: enabled, Notes: in.Notes}
}

// createAreaCode godoc
// @Summary Add an area code (replaces an existing entry with the same country and area code)
// @Tags area-codes
// @Accept json
// @Produce json
// @Param body body areaCodeInput true "area code"
// @Success 201 {object} model.AreaCode
// @Router /area-codes [post]
func (h *Handler) createAreaCode(w http.ResponseWriter, r *http.Request) {
	var in areaCodeInput
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.Store.UpsertAreaCode(r.Context(), in.toModel())
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "area_code.create", "area_code", out.ID.String(), nil, out)
	h.areaCodesChanged(r)
	writeJSON(w, http.StatusCreated, out)
}

// updateAreaCode godoc
// @Summary Update one area code
// @Tags area-codes
// @Accept json
// @Produce json
// @Param id path string true "area code id"
// @Param body body areaCodeInput true "area code"
// @Success 200 {object} model.AreaCode
// @Router /area-codes/{id} [put]
func (h *Handler) updateAreaCode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var in areaCodeInput
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.Store.UpdateAreaCode(r.Context(), id, in.toModel())
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "area_code.update", "area_code", id.String(), nil, out)
	h.areaCodesChanged(r)
	writeJSON(w, http.StatusOK, out)
}

// deleteAreaCode godoc
// @Summary Delete one area code
// @Tags area-codes
// @Param id path string true "area code id"
// @Success 204
// @Router /area-codes/{id} [delete]
func (h *Handler) deleteAreaCode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := h.Store.DeleteAreaCode(r.Context(), id); err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "area_code.delete", "area_code", id.String(), nil, nil)
	h.areaCodesChanged(r)
	w.WriteHeader(http.StatusNoContent)
}

// importAreaCodes godoc
// @Summary Import area codes from CSV (country_code, area_code, description, operator, country, kind), replacing entries with the same codes
// @Tags area-codes
// @Accept multipart/form-data
// @Produce json
// @Success 200 {object} map[string]any
// @Router /area-codes/import [post]
func (h *Handler) importAreaCodes(w http.ResponseWriter, r *http.Request) {
	f, _, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "a CSV file is required in the \"file\" field")
		return
	}
	defer func() { _ = f.Close() }()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		fail(w, http.StatusBadRequest, "cannot read the header row: "+err.Error())
		return
	}
	col := map[string]int{}
	for i, name := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = i
	}
	if _, ok := col["country_code"]; !ok {
		fail(w, http.StatusBadRequest, "the header must contain country_code")
		return
	}
	if _, ok := col["description"]; !ok {
		fail(w, http.StatusBadRequest, "the header must contain description")
		return
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	type rowErr struct {
		Line  int    `json:"line"`
		Error string `json:"error"`
	}
	var errs []rowErr
	imported := 0
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errs = append(errs, rowErr{line, err.Error()})
			continue
		}
		a := &model.AreaCode{
			CountryCode: strings.TrimPrefix(get(rec, "country_code"), "+"),
			AreaCode:    get(rec, "area_code"),
			Country:     get(rec, "country"),
			Description: get(rec, "description"),
			Operator:    get(rec, "operator"),
			Kind:        strings.ToLower(get(rec, "kind")),
			Enabled:     !strings.EqualFold(get(rec, "enabled"), "false"),
			Notes:       get(rec, "notes"),
		}
		if a.CountryCode == "" || a.Description == "" {
			errs = append(errs, rowErr{line, "country_code and description are required"})
			continue
		}
		if _, err := h.Store.UpsertAreaCode(r.Context(), a); err != nil {
			errs = append(errs, rowErr{line, err.Error()})
			continue
		}
		imported++
	}
	h.audit(r, "area_code.import", "area_code", "", nil, map[string]int{"imported": imported, "invalid": len(errs)})
	h.areaCodesChanged(r)
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "invalid": len(errs), "errors": errs})
}

// backfillAreaCodes godoc
// @Summary Fill area and operator in on existing CDRs (all=true also refreshes the ones that already have them)
// @Tags area-codes
// @Produce json
// @Param all query string false "true to recompute every CDR"
// @Success 200 {object} map[string]int64
// @Router /area-codes/backfill [post]
func (h *Handler) backfillAreaCodes(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	n, err := h.Store.BackfillCDRAreaCodes(r.Context(), all)
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "area_code.backfill", "cdrs", "", nil, map[string]any{"updated": n, "all": all})
	writeJSON(w, http.StatusOK, map[string]int64{"updated": n})
}

func (h *Handler) areaCodesChanged(r *http.Request) {
	h.Tables.InvalidateAreas()
	h.publish(r.Context(), cache.ChanAreaCodesChanged, "")
}
