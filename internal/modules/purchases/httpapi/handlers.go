// Package httpapi is the purchases module's HTTP transport layer.
// Mirrors internal/modules/catalogue/httpapi's shape.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/purchases/app"
	"rechvix/internal/modules/purchases/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/purchases/documents", h.listDocuments)
	r.Post("/purchases/documents", h.createDocument)
	r.Get("/purchases/documents/{id}", h.getDocument)
	r.Post("/purchases/documents/{id}/lines", h.addLine)
	r.Post("/purchases/documents/{id}/finalize", h.finalizeDocument)
	r.Post("/purchases/documents/{id}/cancel", h.cancelDocument)
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
	case errors.Is(err, domain.ErrInvalidDocumentType):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DOCUMENT_TYPE", "That is not a recognized purchase document type."))
	case errors.Is(err, domain.ErrDocumentNotDraft):
		httpx.WriteError(w, r, httpx.NewConflict("DOCUMENT_NOT_DRAFT", "This document is not in DRAFT status and cannot be modified or finalized again."))
	case errors.Is(err, domain.ErrEmptyDocument):
		httpx.WriteError(w, r, httpx.NewConflict("EMPTY_DOCUMENT", "A document needs at least one line before it can be finalized."))
	case errors.Is(err, domain.ErrDocumentNotFinalized):
		httpx.WriteError(w, r, httpx.NewConflict("DOCUMENT_NOT_FINALIZED", "Only a FINALIZED document can be cancelled."))
	case errors.Is(err, domain.ErrDuplicateNumber):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_NUMBER", "That document number is already in use."))
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

type createDocumentRequest struct {
	BranchID              uuid.UUID  `json:"branch_id"`
	WarehouseID           uuid.UUID  `json:"warehouse_id"`
	SupplierPartyID       uuid.UUID  `json:"supplier_party_id"`
	DocumentType          string     `json:"document_type"`
	ReferenceDocumentID   *uuid.UUID `json:"reference_document_id"`
	SupplierInvoiceNumber string     `json:"supplier_invoice_number"`
	SupplierInvoiceDate   *time.Time `json:"supplier_invoice_date"`
	DocumentDate          *time.Time `json:"document_date"`
	CurrencyCode          string     `json:"currency_code"`
	Notes                 string     `json:"notes"`
}

func (h *Handlers) createDocument(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createDocumentRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	var docDate time.Time
	if req.DocumentDate != nil {
		docDate = *req.DocumentDate
	}
	d, err := h.svc.CreateDocument(r.Context(), principal(r), app.CreateDocumentParams{
		BranchID: req.BranchID, WarehouseID: req.WarehouseID, SupplierPartyID: req.SupplierPartyID,
		DocumentType: domain.DocumentType(req.DocumentType), ReferenceDocumentID: req.ReferenceDocumentID,
		SupplierInvoiceNumber: req.SupplierInvoiceNumber, SupplierInvoiceDate: req.SupplierInvoiceDate,
		DocumentDate: docDate, CurrencyCode: req.CurrencyCode, Notes: req.Notes,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (h *Handlers) getDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	doc, lines, err := h.svc.GetDocument(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"document": doc, "lines": lines})
}

func (h *Handlers) listDocuments(w http.ResponseWriter, r *http.Request) {
	var docType *domain.DocumentType
	if q := r.URL.Query().Get("document_type"); q != "" {
		t := domain.DocumentType(q)
		docType = &t
	}
	var legalEntityID *uuid.UUID
	if q := r.URL.Query().Get("legal_entity_id"); q != "" {
		id, err := uuid.Parse(q)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_LEGAL_ENTITY_ID", "legal_entity_id must be a UUID."))
			return
		}
		legalEntityID = &id
	}
	list, err := h.svc.ListDocuments(r.Context(), principal(r), docType, legalEntityID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"documents": list})
}

type addLineRequest struct {
	ProductVariantID uuid.UUID `json:"product_variant_id"`
	UnitID           uuid.UUID `json:"unit_id"`
	Quantity         string    `json:"quantity"`
	UnitPrice        string    `json:"unit_price"`
	BatchCode        string    `json:"batch_code"`
}

func (h *Handlers) addLine(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[addLineRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	qty, err := decimal.NewFromString(req.Quantity)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_QUANTITY", "quantity must be a decimal string."))
		return
	}
	price, err := decimal.NewFromString(req.UnitPrice)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_UNIT_PRICE", "unit_price must be a decimal string."))
		return
	}
	line, err := h.svc.AddLine(r.Context(), principal(r), app.AddLineParams{
		DocumentID: id, ProductVariantID: req.ProductVariantID, UnitID: req.UnitID,
		Quantity: qty, UnitPrice: price, BatchCode: req.BatchCode,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, line)
}

func (h *Handlers) finalizeDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	doc, err := h.svc.FinalizeDocument(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, doc)
}

func (h *Handlers) cancelDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	doc, err := h.svc.CancelDocument(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, doc)
}
