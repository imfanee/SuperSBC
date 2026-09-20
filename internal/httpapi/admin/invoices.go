package admin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/imfanee/supersbc/internal/invoice"
	"github.com/imfanee/supersbc/internal/model"
	"github.com/imfanee/supersbc/internal/store"
)

func (h *Handler) mountInvoices(r chi.Router) {
	if h.Invoices == nil {
		return
	}
	r.Get("/invoices", h.listInvoices)
	r.Get("/invoices/{id}", h.getInvoice)
	r.Get("/invoices/{id}.pdf", h.invoicePDF)
	r.With(operators).Post("/invoices/generate", h.generateInvoice)
	r.Get("/invoices/{id}/allocations", h.invoiceAllocations)
	r.Get("/invoice-ledger", h.invoiceLedger)
	r.With(operators).Post("/invoice-payments", h.createPayment)
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
	allocs, err := h.Invoices.InvoiceAllocations(r.Context(), inv.ID)
	if err != nil {
		failErr(w, err)
		return
	}
	pdf, err := h.Invoices.PDF(inv, allocs)
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
	// Period YYYY-MM for a monthly invoice, or From/To (dates, To exclusive; RFC 3339 or YYYY-MM-DD) for a custom one.
	Period string `json:"period"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// generateInvoice godoc
// @Summary Generate the invoice of a customer or carrier for a month (period=YYYY-MM) or a custom range (from, to); idempotent, 200 when it already exists, 409 when another invoice overlaps
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
	var start, end time.Time
	kind := "monthly"
	switch {
	case in.Period != "":
		at, err := time.Parse("2006-01", in.Period)
		if err != nil {
			fail(w, http.StatusBadRequest, "period must be YYYY-MM")
			return
		}
		start, end = invoice.Period(at)
	case in.From != "" && in.To != "":
		var err1, err2 error
		start, err1 = parseDay(in.From)
		end, err2 = parseDay(in.To)
		if err1 != nil || err2 != nil {
			fail(w, http.StatusBadRequest, "from and to must be dates (YYYY-MM-DD) or RFC 3339 timestamps")
			return
		}
		if !end.After(start) {
			fail(w, http.StatusBadRequest, "to must be after from")
			return
		}
		kind = "custom"
	default:
		fail(w, http.StatusBadRequest, "give period (YYYY-MM) or from and to")
		return
	}
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
	inv, err := h.Invoices.GeneratePeriod(r.Context(), acc.ID, start, end, kind)
	if errors.Is(err, invoice.ErrExists) {
		writeJSON(w, http.StatusOK, inv)
		return
	}
	if errors.Is(err, invoice.ErrOverlap) {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		failErr(w, err)
		return
	}
	h.audit(r, "invoice.generate", "invoice", inv.ID.String(), nil, inv)
	writeJSON(w, http.StatusCreated, inv)
}

// parseDay accepts YYYY-MM-DD (midnight UTC) or an RFC 3339 timestamp.
func parseDay(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	return t.UTC(), err
}

// invoiceAllocations godoc
// @Summary Payments applied to one invoice
// @Tags invoices
// @Produce json
// @Param id path string true "invoice id"
// @Success 200 {array} invoice.Allocation
// @Router /invoices/{id}/allocations [get]
func (h *Handler) invoiceAllocations(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	out, err := h.Invoices.InvoiceAllocations(r.Context(), id)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// invoiceLedger godoc
// @Summary Invoice ledger of a customer or carrier: invoices and payments with allocations, totals of invoiced, received, outstanding (D-72)
// @Tags invoices
// @Produce json
// @Param owner_type query string true "customer|carrier"
// @Param owner_id query string true "owner id"
// @Success 200 {object} invoice.Ledger
// @Router /invoice-ledger [get]
func (h *Handler) invoiceLedger(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	owner := queryUUID(r, "owner_id")
	if owner == nil || (q.Get("owner_type") != "customer" && q.Get("owner_type") != "carrier") {
		fail(w, http.StatusBadRequest, "owner_type and owner_id required")
		return
	}
	out, err := h.Invoices.InvoiceLedger(r.Context(), q.Get("owner_type"), *owner)
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type paymentAllocationInput struct {
	InvoiceID string `json:"invoice_id" validate:"required,uuid"`
	Amount    string `json:"amount" validate:"required"`
}

type paymentInput struct {
	OwnerType   string                   `json:"owner_type" validate:"required,oneof=customer carrier"`
	OwnerID     string                   `json:"owner_id" validate:"required,uuid"`
	Amount      string                   `json:"amount" validate:"required"`
	ReceivedAt  string                   `json:"received_at"` // RFC 3339 or YYYY-MM-DD; default now
	Reference   string                   `json:"reference" validate:"max=200"`
	Method      string                   `json:"method" validate:"max=50"`
	Notes       string                   `json:"notes" validate:"max=2000"`
	Allocations []paymentAllocationInput `json:"allocations" validate:"dive"`
	// AutoAllocate spreads the unallocated remainder over open invoices, oldest first.
	AutoAllocate bool `json:"auto_allocate"`
	// Topup also posts the amount to the prepaid call ledger as a top-up (customer) or a payment made (carrier).
	Topup bool `json:"topup"`
}

// createPayment godoc
// @Summary Record a payment against invoices (full, partial or several), optionally also topping up the prepaid account
// @Tags invoices
// @Accept json
// @Produce json
// @Param body body paymentInput true "payment"
// @Success 201 {object} invoice.Payment
// @Router /invoice-payments [post]
func (h *Handler) createPayment(w http.ResponseWriter, r *http.Request) {
	var in paymentInput
	if !h.decode(w, r, &in) {
		return
	}
	amount, err := decimal.NewFromString(in.Amount)
	if err != nil || !amount.IsPositive() {
		fail(w, http.StatusBadRequest, "amount must be a positive decimal")
		return
	}
	received := time.Now().UTC()
	if in.ReceivedAt != "" {
		if received, err = parseDay(in.ReceivedAt); err != nil {
			fail(w, http.StatusBadRequest, "received_at must be a date or RFC 3339 timestamp")
			return
		}
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
	var allocs []invoice.AllocationInput
	for _, a := range in.Allocations {
		id, err := uuid.Parse(a.InvoiceID)
		amt, err2 := decimal.NewFromString(a.Amount)
		if err != nil || err2 != nil {
			fail(w, http.StatusBadRequest, "allocation needs an invoice id and a decimal amount")
			return
		}
		allocs = append(allocs, invoice.AllocationInput{InvoiceID: id, Amount: amt})
	}
	by := principal(r.Context()).Email
	var out *invoice.Payment
	err = h.Store.WithTx(r.Context(), func(tx pgx.Tx) error {
		p, err := invoice.InsertPayment(r.Context(), tx, acc.ID, amount, acc.Currency, received, in.Reference, in.Method, in.Notes, allocs, in.AutoAllocate, nil, by)
		if err != nil {
			return err
		}
		if in.Topup {
			if _, err := store.LockAccount(r.Context(), tx, acc.ID); err != nil {
				return err
			}
			desc := "payment " + in.Reference
			if in.Reference == "" {
				desc = "payment received"
			}
			if in.OwnerType == "carrier" {
				desc = "payment to carrier " + in.Reference
			}
			if _, err := store.Post(r.Context(), tx, acc.ID, nil, model.LedgerTopup, amount, desc+" (invoice ledger "+p.ID.String()+")", &by); err != nil {
				return err
			}
			var entryID int64
			if err := tx.QueryRow(r.Context(), `SELECT max(id) FROM ledger_entries WHERE account_id = $1`, acc.ID).Scan(&entryID); err != nil {
				return err
			}
			if err := invoice.SetLedgerEntry(r.Context(), tx, p.ID, entryID); err != nil {
				return err
			}
			p.LedgerEntryID = &entryID
		}
		out = p
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			failErr(w, err)
			return
		}
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	h.audit(r, "invoice.payment", "payment", out.ID.String(), nil, out)
	writeJSON(w, http.StatusCreated, out)
}
