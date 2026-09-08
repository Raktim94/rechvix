// Package httpapi is the accounting module's HTTP transport layer.
// Mirrors internal/modules/purchases/httpapi's shape.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/accounting/app"
	"rechvix/internal/modules/accounting/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/accounting/accounts", h.listAccounts)
	r.Post("/accounting/accounts/ensure-default-chart", h.ensureDefaultChart)
	r.Get("/accounting/journals", h.listJournals)
	r.Get("/accounting/expenses", h.listExpenses)
	r.Post("/accounting/journals", h.postJournal)
	r.Get("/accounting/journals/{id}", h.getJournal)
	r.Post("/accounting/receipts", h.recordReceipt)
	r.Post("/accounting/payments", h.recordPayment)
	r.Get("/accounting/sales-documents/{id}/receipts", h.listReceiptsForSalesDocument)
	r.Get("/accounting/purchase-documents/{id}/payments", h.listPaymentsForPurchaseDocument)
	r.Get("/accounting/parties/{id}/ledger", h.getLedger)
	r.Get("/accounting/parties/{id}/ageing", h.getAgeing)
	r.Get("/accounting/fiscal-periods", h.listFiscalPeriods)
	r.Post("/accounting/fiscal-periods", h.createFiscalPeriod)
	r.Post("/accounting/fiscal-periods/{id}/lock", h.lockPeriod)
	r.Post("/accounting/fiscal-periods/{id}/unlock", h.unlockPeriod)
	r.Get("/accounting/bank-accounts", h.listBankAccounts)
	r.Post("/accounting/bank-accounts", h.createBankAccount)
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "The requested resource was not found."))
	case errors.Is(err, domain.ErrAccountNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("ACCOUNT_NOT_FOUND", "That account was not found."))
	case errors.Is(err, domain.ErrUnbalancedJournal):
		httpx.WriteError(w, r, httpx.NewBadRequest("UNBALANCED_JOURNAL", "Journal debits and credits do not sum equal."))
	case errors.Is(err, domain.ErrEmptyJournal):
		httpx.WriteError(w, r, httpx.NewBadRequest("EMPTY_JOURNAL", "A journal needs at least two lines."))
	case errors.Is(err, domain.ErrPeriodLocked):
		httpx.WriteError(w, r, httpx.NewConflict("PERIOD_LOCKED", "This fiscal period is locked."))
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func (h *Handlers) listAccounts(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListAccounts(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handlers) ensureDefaultChart(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := h.svc.EnsureDefaultChartOfAccounts(r.Context(), p, p.OrganisationID); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listJournals(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.svc.ListJournals(r.Context(), principal(r), limit)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"journals": out})
}

func (h *Handlers) listReceiptsForSalesDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	out, err := h.svc.ListReceiptsForSalesDocument(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"receipts": out})
}

func (h *Handlers) listPaymentsForPurchaseDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	out, err := h.svc.ListPaymentsForPurchaseDocument(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"payments": out})
}

func (h *Handlers) listExpenses(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.svc.ListExpenseEntries(r.Context(), principal(r), limit)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"expenses": out})
}

type postJournalLineRequest struct {
	AccountCode string          `json:"account_code"`
	PartyID     *uuid.UUID      `json:"party_id"`
	Debit       decimal.Decimal `json:"debit"`
	Credit      decimal.Decimal `json:"credit"`
	Description string          `json:"description"`
}

type postJournalRequest struct {
	SourceType  string                   `json:"source_type"`
	JournalDate *time.Time               `json:"journal_date"`
	Description string                   `json:"description"`
	Lines       []postJournalLineRequest `json:"lines"`
}

// postJournal is the HTTP face of Service.Post's own doc comment ("a
// manual adjustment journal posted directly from an accounting screen")
// — never previously exposed over HTTP. The Expenses page is its first
// real caller: two lines (debit an expense account, credit Cash/Bank),
// but this stays a general balanced-journal endpoint rather than an
// "expenses"-specific one, since Post itself already is.
func (h *Handlers) postJournal(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[postJournalRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	journalDate := time.Now()
	if req.JournalDate != nil {
		journalDate = *req.JournalDate
	}
	lines := make([]app.JournalLineRequest, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, app.JournalLineRequest{
			AccountCode: l.AccountCode, PartyID: l.PartyID, Debit: l.Debit, Credit: l.Credit, Description: l.Description,
		})
	}
	p := principal(r)
	j, err := h.svc.Post(r.Context(), p, app.JournalRequest{
		OrganisationID: p.OrganisationID, SourceType: req.SourceType, JournalDate: journalDate,
		Description: req.Description, CreatedBy: p.UserID, Lines: lines,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, j)
}

func (h *Handlers) getJournal(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "That is not a valid journal id."))
		return
	}
	j, lines, err := h.svc.GetJournal(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"journal": j, "lines": lines})
}

type recordReceiptRequest struct {
	PartyID         uuid.UUID       `json:"party_id"`
	SalesDocumentID *uuid.UUID      `json:"sales_document_id"`
	Amount          decimal.Decimal `json:"amount"`
	BankAccountID   *uuid.UUID      `json:"bank_account_id"`
	Method          string          `json:"method"`
	ReferenceNumber string          `json:"reference_number"`
	ReceivedAt      *time.Time      `json:"received_at"`
}

func (h *Handlers) recordReceipt(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[recordReceiptRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	var receivedAt time.Time
	if req.ReceivedAt != nil {
		receivedAt = *req.ReceivedAt
	}
	rec, err := h.svc.RecordReceipt(r.Context(), principal(r), app.RecordReceiptParams{
		PartyID: req.PartyID, SalesDocumentID: req.SalesDocumentID, Amount: req.Amount, BankAccountID: req.BankAccountID,
		Method: domain.PaymentMethod(req.Method), ReferenceNumber: req.ReferenceNumber, ReceivedAt: receivedAt,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rec)
}

type recordPaymentRequest struct {
	PartyID            uuid.UUID       `json:"party_id"`
	PurchaseDocumentID *uuid.UUID      `json:"purchase_document_id"`
	Amount             decimal.Decimal `json:"amount"`
	BankAccountID      *uuid.UUID      `json:"bank_account_id"`
	Method             string          `json:"method"`
	ReferenceNumber    string          `json:"reference_number"`
	PaidAt             *time.Time      `json:"paid_at"`
}

func (h *Handlers) recordPayment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[recordPaymentRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	var paidAt time.Time
	if req.PaidAt != nil {
		paidAt = *req.PaidAt
	}
	pay, err := h.svc.RecordPayment(r.Context(), principal(r), app.RecordPaymentParams{
		PartyID: req.PartyID, PurchaseDocumentID: req.PurchaseDocumentID, Amount: req.Amount, BankAccountID: req.BankAccountID,
		Method: domain.PaymentMethod(req.Method), ReferenceNumber: req.ReferenceNumber, PaidAt: paidAt,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, pay)
}

func asOfParam(r *http.Request) time.Time {
	if v := r.URL.Query().Get("as_of"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return t
		}
	}
	return time.Now()
}

func (h *Handlers) getLedger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "That is not a valid party id."))
		return
	}
	entries, err := h.svc.GetPartyLedger(r.Context(), principal(r), id, asOfParam(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, entries)
}

func (h *Handlers) getAgeing(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "That is not a valid party id."))
		return
	}
	bucket, err := h.svc.GetAgeing(r.Context(), principal(r), id, asOfParam(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bucket)
}

func (h *Handlers) listFiscalPeriods(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListFiscalPeriods(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createFiscalPeriodRequest struct {
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	Label     string    `json:"label"`
}

func (h *Handlers) createFiscalPeriod(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createFiscalPeriodRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	fp, err := h.svc.CreateFiscalPeriod(r.Context(), principal(r), app.CreateFiscalPeriodParams{
		StartDate: req.StartDate, EndDate: req.EndDate, Label: req.Label,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, fp)
}

func (h *Handlers) lockPeriod(w http.ResponseWriter, r *http.Request) {
	h.setPeriodLock(w, r, true)
}
func (h *Handlers) unlockPeriod(w http.ResponseWriter, r *http.Request) {
	h.setPeriodLock(w, r, false)
}
func (h *Handlers) setPeriodLock(w http.ResponseWriter, r *http.Request, locked bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "That is not a valid fiscal period id."))
		return
	}
	if err := h.svc.SetPeriodLock(r.Context(), principal(r), id, locked); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listBankAccounts(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListBankAccounts(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createBankAccountRequest struct {
	LegalEntityID uuid.UUID `json:"legal_entity_id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	AccountNumber string    `json:"account_number"`
	BankName      string    `json:"bank_name"`
	IFSCCode      string    `json:"ifsc_code"`
	CurrencyCode  string    `json:"currency_code"`
}

func (h *Handlers) createBankAccount(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createBankAccountRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	b, err := h.svc.CreateBankAccount(r.Context(), principal(r), app.CreateBankAccountParams{
		LegalEntityID: req.LegalEntityID, Name: req.Name, Kind: domain.BankAccountKind(req.Kind),
		AccountNumber: req.AccountNumber, BankName: req.BankName, IFSCCode: req.IFSCCode, CurrencyCode: req.CurrencyCode,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, b)
}
