// Package internalapi is the call control API consumed by sbc_inbound.lua
// and the CDR receiver for mod_json_cdr (Section 8, internal listener).
package internalapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/opensbc/opensbc/internal/billing"
	"github.com/opensbc/opensbc/internal/callcontrol"
	"github.com/opensbc/opensbc/internal/logging"
	"github.com/opensbc/opensbc/internal/model"
	"github.com/opensbc/opensbc/internal/store"
)

// Handler serves the internal API.
type Handler struct {
	secret string
	log    *slog.Logger
	pipe   *callcontrol.Pipeline
	bill   *billing.Engine
	st     *store.Store
}

// New creates the handler.
func New(secret string, log *slog.Logger, pipe *callcontrol.Pipeline, bill *billing.Engine, st *store.Store) *Handler {
	return &Handler{secret: secret, log: log, pipe: pipe, bill: bill, st: st}
}

// Mount registers the routes under /internal/v1.
func (h *Handler) Mount(r chi.Router) {
	r.Route("/internal/v1", func(r chi.Router) {
		r.Use(h.auth)
		r.Post("/call/setup", h.setup)
		r.Post("/call/authorize", h.authorize)
		r.Post("/call/rate", h.rate)
		r.Post("/call/route", h.route)
		r.Post("/call/attempt", h.attempt)
		r.Post("/call/release", h.release)
		r.Post("/cdr", h.cdr)
	})
}

// auth accepts X-SBC-Secret or HTTP basic auth with the secret as password
// (mod_json_cdr only supports basic auth).
func (h *Handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-SBC-Secret")
		if got == "" {
			if _, p, ok := r.BasicAuth(); ok {
				got = p
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(h.secret)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	return dec.Decode(v)
}

func (h *Handler) setup(w http.ResponseWriter, r *http.Request) {
	var req callcontrol.SetupRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := logging.WithCallUUID(r.Context(), h.log, req.CallUUID)
	resp, err := h.pipe.Setup(ctx, req)
	if err != nil {
		logging.FromContext(ctx, h.log).Error("setup failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	var req callcontrol.SetupRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := h.pipe.Authorize(r.Context(), req.SrcIP, req.SrcPort, req.Transport)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if res.Admitted && res.Customer != nil {
		h.pipe.LeaveAdmission(r.Context(), res.Customer.ID) // probe only, do not hold a slot
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer": res.Customer, "reject": res.Reject})
}

func (h *Handler) rate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomerID uuid.UUID `json:"customer_id"`
		Called     string    `json:"called"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cust, err := h.st.CustomerByID(r.Context(), req.CustomerID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "customer not found"})
		return
	}
	rate, err := h.pipe.SellRateFor(r.Context(), cust, req.Called)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rate": rate})
}

func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomerID uuid.UUID `json:"customer_id"`
		Called     string    `json:"called"`
		Caller     string    `json:"caller"`
	}
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cust, err := h.st.CustomerByID(r.Context(), req.CustomerID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "customer not found"})
		return
	}
	sell, _ := h.pipe.SellRateFor(r.Context(), cust, req.Called)
	res, err := h.pipe.Route(r.Context(), cust, req.Called, req.Caller, sell, uuid.New().String())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) attempt(w http.ResponseWriter, r *http.Request) {
	var req callcontrol.AttemptRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := logging.WithCallUUID(r.Context(), h.log, req.CallUUID)
	resp, err := h.pipe.RecordAttempt(ctx, req)
	if err != nil {
		logging.FromContext(ctx, h.log).Error("attempt failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) release(w http.ResponseWriter, r *http.Request) {
	var req callcontrol.ReleaseRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := uuid.Parse(req.CallUUID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad call_uuid"})
		return
	}
	code := req.SIPCode
	if code == 0 {
		code = 503
	}
	reason := req.Reason
	if reason == "" {
		reason = "released"
	}
	if err := h.pipe.ReleaseReservation(r.Context(), id, code, reason, model.DispositionFailed); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "released"})
}

// cdr receives the mod_json_cdr POST. FreeSWITCH retries on non-2xx and
// spools to disk after the retries, so the handler returns 200 only when the
// billing transaction committed (or was already done).
func (h *Handler) cdr(w http.ResponseWriter, r *http.Request) {
	var doc struct {
		Variables map[string]any `json:"variables"`
	}
	if err := decode(r, &doc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	vars := make(map[string]string, len(doc.Variables))
	for k, v := range doc.Variables {
		switch t := v.(type) {
		case string:
			vars[k] = t
		default:
			vars[k] = fmt.Sprint(t)
		}
	}
	if strings.EqualFold(vars["direction"], "outbound") {
		// b-legs are not billed (log-b-leg=false, belt and braces)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	info, ok := billing.FromVariables(vars, "json_cdr")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing uuid"})
		return
	}
	ctx := logging.WithCallUUID(r.Context(), h.log, info.CallUUID.String())
	out, err := h.bill.Bill(ctx, info)
	if err != nil {
		logging.FromContext(ctx, h.log).Error("bill from json_cdr failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
