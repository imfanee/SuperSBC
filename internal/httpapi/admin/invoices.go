package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/opensbc/opensbc/internal/invoice"
)

func (h *Handler) mountInvoices(r chi.Router) {
	if h.Invoices == nil {
		return
	}
	r.Get("/invoices", h.listInvoices)
	r.Get("/invoices/{id}", h.getInvoice)
	r.Get("/invoices/{id}.pdf", h.invoicePDF)
	r.With(operators).Post("/invoices/generate", h.generateInvoice)
}

// listInvoices godoc
// @Summary List invoices (newest first), optionally of one customer or carrier
// @Tags invoices
// @Produce json
// @Param owner_type query string false "customer|carrier"
// @Param owner_id query string false "owner id"
// @Param limit query int false "max rows (default 100)"
// @Success 200 {array} invoice.Invoice
// @Router /invoices [get]
func (h *Handler) listInvoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := h.Invoices.List(r.Context(), q.Get("owner_type"), queryUUID(r, "owner_id"), limit)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// getInvoice godoc
// @Summary Get one invoice with its usage lines
// @Tags invoices
// @Produce json
// @Param id path string true "invoice id"
// @Success 200 {object} invoice.Invoice
// @Router /invoices/{id} [get]
func (h *Handler) getInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	inv, err := h.Invoices.ByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// invoicePDF godoc
// @Summary Download one invoice as PDF
// @Tags invoices
// @Produce application/pdf
// @Param id path string true "invoice id"
// @Success 200 {file} binary
// @Router /invoices/{id}.pdf [get]
func (h *Handler) invoicePDF(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	inv, err := h.Invoices.ByID(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	pdf, err := h.Invoices.PDF(inv)
	if err != nil {
		failErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+inv.Number+`.pdf"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdf)
}

type generateInvoiceInput struct {
	OwnerType string `json:"owner_type" validate:"required,oneof=customer carrier"`
	OwnerID   string `json:"owner_id" validate:"required,uuid"`
	Period    string `json:"period" validate:"required"` // YYYY-MM
}

// generateInvoice godoc
// @Summary Generate the invoice of a customer or carrier for a month (idempotent; 200 when it already exists)
// @Tags invoices
// @Accept json
// @Produce json
// @Param body body generateInvoiceInput true "owner and period YYYY-MM"
// @Success 201 {object} invoice.Invoice
// @Router /invoices/generate [post]
func (h *Handler) generateInvoice(w http.ResponseWriter, r *http.Request) {
	var in generateInvoiceInput
	if !h.decode(w, r, &in) {
		return
	}
	at, err := time.Parse("2006-01", in.Period)
	if err != nil {
		fail(w, http.StatusBadRequest, "period must be YYYY-MM")
		return
	}
	start, _ := invoice.Period(at)
	if !start.Before(time.Now().UTC()) {
		fail(w, http.StatusBadRequest, "period has not started")
		return
	}
	ownerID, err := uuidPtr(in.OwnerID)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	acc, err := h.Store.AccountByOwner(r.Context(), in.OwnerType, *ownerID)
	if err != nil {
		failErr(w, err)
		return
	}
	inv, err := h.Invoices.Generate(r.Context(), acc.ID, at)
	if errors.Is(err, invoice.ErrExists) {
		writeJSON(w, http.StatusOK, inv)
		return
	}
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "invoice.generate", "invoice", inv.ID.String(), nil, inv)
	writeJSON(w, http.StatusCreated, inv)
}
