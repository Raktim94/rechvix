// Package httpapi is the sales module's HTTP transport layer. Mirrors
// internal/modules/purchases/httpapi's shape.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	ewaybillapp "rechvix/internal/modules/ewaybill/app"
	inventorydomain "rechvix/internal/modules/inventory/domain"
	"rechvix/internal/modules/sales/app"
	"rechvix/internal/modules/sales/domain"
	"rechvix/internal/modules/sales/printing"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/database"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct {
	svc *app.Service
	// pool/ewaybill are only used by printDocument, to enrich a printed
	// invoice with its e-Way Bill number if one has been generated (a
	// printed invoice is exactly what a driver needs at a roadside check
	// — see the doc comment on printing.InvoiceData.EWBNumber). Nil-
	// guarded so a composition root that hasn't wired e-Way Bill support
	// yet still gets a working print path, just without that one line.
	pool     *database.Pool
	ewaybill *ewaybillapp.Service
}

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

// WithEWayBill returns a copy of h with the e-Way Bill enrichment
// dependency wired in — a separate step, mirroring ewaybill.Service's own
// WithFreePortal pattern, so every existing NewHandlers call site keeps
// working unchanged.
func (h *Handlers) WithEWayBill(pool *database.Pool, ewaybillSvc *ewaybillapp.Service) *Handlers {
	cp := *h
	cp.pool, cp.ewaybill = pool, ewaybillSvc
	return &cp
}

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/sales/documents", h.listDocuments)
	r.Post("/sales/documents", h.createDocument)
	r.Get("/sales/documents/{id}", h.getDocument)
	r.Post("/sales/documents/{id}/lines", h.addLine)
	r.Post("/sales/documents/{id}/finalize", h.finalizeDocument)
	r.Post("/sales/documents/{id}/convert", h.convertDocument)
	r.Get("/sales/documents/{id}/print", h.printDocument)
	r.Get("/sales/billing-lookup", h.billingLookup)
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
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DOCUMENT_TYPE", "That is not a recognized sales document type."))
	case errors.Is(err, domain.ErrDocumentNotDraft):
		httpx.WriteError(w, r, httpx.NewConflict("DOCUMENT_NOT_DRAFT", "This document is not in DRAFT status and cannot be modified or finalized again."))
	case errors.Is(err, domain.ErrDocumentNotFinalized):
		httpx.WriteError(w, r, httpx.NewConflict("DOCUMENT_NOT_FINALIZED", "Only a FINALIZED document can be converted."))
	case errors.Is(err, domain.ErrEmptyDocument):
		httpx.WriteError(w, r, httpx.NewConflict("EMPTY_DOCUMENT", "A document needs at least one line before it can be finalized."))
	case errors.Is(err, domain.ErrZeroValueDocument):
		httpx.WriteError(w, r, httpx.NewConflict("ZERO_VALUE_DOCUMENT", "This document's total is ₹0.00. Check that every item has a price, then try again."))
	case errors.Is(err, domain.ErrDuplicateNumber):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_NUMBER", "That document number is already in use."))
	case errors.Is(err, domain.ErrReturnQuantityExceedsSource):
		httpx.WriteError(w, r, httpx.NewBadRequest("RETURN_QUANTITY_EXCEEDS_SOURCE", "You can't return more of an item than was on the original document."))
	case errors.Is(err, taxdomain.ErrRateNotConfigured):
		httpx.WriteError(w, r, httpx.NewBadRequest("TAX_RATE_NOT_CONFIGURED", "One or more items on this document don't have a GST rate set up for their HSN/SAC code yet. Add a tax rate for it under GST / Tax, then try again."))
	case errors.Is(err, inventorydomain.ErrInsufficientStock):
		httpx.WriteError(w, r, httpx.NewConflict("INSUFFICIENT_STOCK", "There isn't enough stock on hand for one or more items on this document. Record a purchase or stock adjustment first, then try again."))
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
	LegalEntityID             uuid.UUID  `json:"legal_entity_id"`
	BranchID                  uuid.UUID  `json:"branch_id"`
	WarehouseID               uuid.UUID  `json:"warehouse_id"`
	CustomerPartyID           uuid.UUID  `json:"customer_party_id"`
	DocumentType              string     `json:"document_type"`
	ReferenceDocumentID       *uuid.UUID `json:"reference_document_id"`
	IssueDate                 *time.Time `json:"issue_date"`
	DueDate                   *time.Time `json:"due_date"`
	SupplyDate                *time.Time `json:"supply_date"`
	BillingAddressID          *uuid.UUID `json:"billing_address_id"`
	ShippingAddressID         *uuid.UUID `json:"shipping_address_id"`
	CustomerTaxRegistrationID *uuid.UUID `json:"customer_tax_registration_id"`
	PlaceOfSupplyStateCode    string     `json:"place_of_supply_state_code"`
	SalespersonUserID         *uuid.UUID `json:"salesperson_user_id"`
	PriceListID               *uuid.UUID `json:"price_list_id"`
	CurrencyCode              string     `json:"currency_code"`
	BaseCurrencyCode          string     `json:"base_currency_code"`
	ExchangeRate              string     `json:"exchange_rate"`
	PricingMode               string     `json:"pricing_mode"`
	CustomerReference         string     `json:"customer_reference"`
	Transporter               string     `json:"transporter"`
	VehicleNumber             string     `json:"vehicle_number"`
	ShippingTerms             string     `json:"shipping_terms"`
	Notes                     string     `json:"notes"`
	TermsAndConditions        string     `json:"terms_and_conditions"`
	PaymentTermsDays          int        `json:"payment_terms_days"`
}

func (h *Handlers) createDocument(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createDocumentRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	var issueDate time.Time
	if req.IssueDate != nil {
		issueDate = *req.IssueDate
	}
	exchangeRate := decimal.NewFromInt(1)
	if req.ExchangeRate != "" {
		exchangeRate, err = decimal.NewFromString(req.ExchangeRate)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_EXCHANGE_RATE", "exchange_rate must be a decimal string."))
			return
		}
	}
	d, err := h.svc.CreateDocument(r.Context(), principal(r), app.CreateDocumentParams{
		LegalEntityID: req.LegalEntityID, BranchID: req.BranchID, WarehouseID: req.WarehouseID,
		CustomerPartyID: req.CustomerPartyID, DocumentType: domain.DocumentType(req.DocumentType),
		ReferenceDocumentID: req.ReferenceDocumentID, IssueDate: issueDate, DueDate: req.DueDate, SupplyDate: req.SupplyDate,
		BillingAddressID: req.BillingAddressID, ShippingAddressID: req.ShippingAddressID,
		CustomerTaxRegistrationID: req.CustomerTaxRegistrationID, PlaceOfSupplyStateCode: req.PlaceOfSupplyStateCode,
		SalespersonUserID: req.SalespersonUserID, PriceListID: req.PriceListID, CurrencyCode: req.CurrencyCode,
		BaseCurrencyCode: req.BaseCurrencyCode, ExchangeRate: exchangeRate, PricingMode: taxdomain.PricingMode(req.PricingMode),
		CustomerReference: req.CustomerReference, Transporter: req.Transporter, VehicleNumber: req.VehicleNumber,
		ShippingTerms: req.ShippingTerms, Notes: req.Notes, TermsAndConditions: req.TermsAndConditions,
		PaymentTermsDays: req.PaymentTermsDays,
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
	list, err := h.svc.ListDocuments(r.Context(), principal(r), docType)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"documents": list})
}

type addLineRequest struct {
	ProductVariantID   uuid.UUID `json:"product_variant_id"`
	UnitID             uuid.UUID `json:"unit_id"`
	Quantity           string    `json:"quantity"`
	UnitPrice          string    `json:"unit_price"`
	LineDiscountAmount string    `json:"line_discount_amount"`
	BatchCode          string    `json:"batch_code"`
	SerialCode         string    `json:"serial_code"`
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
	discount := decimal.Zero
	if req.LineDiscountAmount != "" {
		discount, err = decimal.NewFromString(req.LineDiscountAmount)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DISCOUNT", "line_discount_amount must be a decimal string."))
			return
		}
	}
	line, err := h.svc.AddLine(r.Context(), principal(r), app.AddLineParams{
		DocumentID: id, ProductVariantID: req.ProductVariantID, UnitID: req.UnitID,
		Quantity: qty, UnitPrice: price, LineDiscountAmount: discount,
		BatchCode: req.BatchCode, SerialCode: req.SerialCode,
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

type convertDocumentRequest struct {
	TargetType string `json:"target_type"`
	// LineQuantities is optional — see Service.ConvertDocument's own doc
	// comment: omitted/empty copies every source line at full quantity
	// (unchanged from before this field existed); keyed by source line
	// id, it restricts and rescales the copy to a partial return.
	LineQuantities map[uuid.UUID]string `json:"line_quantities"`
}

func (h *Handlers) convertDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[convertDocumentRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	var lineQuantities map[uuid.UUID]decimal.Decimal
	if len(req.LineQuantities) > 0 {
		lineQuantities = make(map[uuid.UUID]decimal.Decimal, len(req.LineQuantities))
		for lineID, qtyStr := range req.LineQuantities {
			qty, err := decimal.NewFromString(qtyStr)
			if err != nil {
				httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_QUANTITY", "Every line_quantities value must be a decimal string."))
				return
			}
			lineQuantities[lineID] = qty
		}
	}
	target, err := h.svc.ConvertDocument(r.Context(), principal(r), id, domain.DocumentType(req.TargetType), lineQuantities)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, target)
}

// printDocument renders a finalized document to PDF. ?template= selects
// the layout (brief §19); defaults to the A4 GST invoice.
func (h *Handlers) printDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	tpl := printing.Template(r.URL.Query().Get("template"))
	if tpl == "" {
		tpl = printing.TemplateA4GSTInvoice
	}
	data, err := h.svc.BuildInvoiceData(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.enrichWithEWayBill(r.Context(), principal(r).OrganisationID, id, data)
	pdfBytes, err := printing.RenderPDF(tpl, *data)
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "RENDER_FAILED", Message: "Could not render the document.", Cause: err})
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// enrichWithEWayBill best-effort-populates data.EWBNumber/EWBValidUntil
// from any e-Way Bill record generated for this document. Never fails
// the print itself — an invoice a shop owner needs right now to hand a
// driver must still render even if the e-Way Bill lookup errors for some
// unrelated reason; the printed invoice just won't carry the EWB number
// in that case, same "degrade, don't block" principle BuildInvoiceData
// itself uses for PreviousBalance.
func (h *Handlers) enrichWithEWayBill(ctx context.Context, orgID, documentID uuid.UUID, data *printing.InvoiceData) {
	if h.ewaybill == nil || h.pool == nil {
		return
	}
	_ = h.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		rec, err := h.ewaybill.GetRecordForDocument(ctx, orgID, documentID)
		if err != nil || rec == nil || rec.EWBNumber == nil {
			return nil
		}
		data.EWBNumber = *rec.EWBNumber
		data.EWBValidUntil = rec.ValidUntil
		return nil
	})
}

// billingLookup is the sales-screen search endpoint (brief §24/§25):
// product search + stock + price in one call.
func (h *Handlers) billingLookup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var warehouseID, priceListID *uuid.UUID
	if v := r.URL.Query().Get("warehouse_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id must be a UUID."))
			return
		}
		warehouseID = &id
	}
	if v := r.URL.Query().Get("price_list_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_PRICE_LIST_ID", "price_list_id must be a UUID."))
			return
		}
		priceListID = &id
	}
	results, err := h.svc.BillingLookup(r.Context(), principal(r), q, warehouseID, priceListID, 10)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}
