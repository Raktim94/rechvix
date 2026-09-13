// Package httpapi is the contacts module's HTTP transport layer.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/contacts/app"
	"rechvix/internal/modules/contacts/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/contacts/parties", h.listOrSearchParties)
	r.Post("/contacts/parties", h.createParty)
	r.Get("/contacts/parties/{id}", h.getParty)
	r.Get("/contacts/parties/{id}/addresses", h.listAddresses)
	r.Post("/contacts/parties/{id}/addresses", h.addAddress)
	r.Get("/contacts/parties/{id}/tax-registrations", h.listTaxRegistrations)
	r.Post("/contacts/parties/{id}/tax-registrations", h.addTaxRegistration)
	r.Get("/contacts/tax-registrations/{number}", h.lookupTaxRegistration)
	r.Post("/contacts/parties/import", h.importParties)
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
	case errors.Is(err, domain.ErrDuplicateRegistration):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_REGISTRATION", "This registration number is already recorded for this party."))
	case errors.Is(err, domain.ErrInvalidPartyType):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_PARTY_TYPE", "party_type must be CUSTOMER, SUPPLIER, or BOTH."))
	case errors.Is(err, domain.ErrInvalidAddressType):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ADDRESS_TYPE", "address_type must be BILLING, SHIPPING, WAREHOUSE, or REGISTERED_OFFICE."))
	case errors.Is(err, domain.ErrLegalNameRequired):
		httpx.WriteError(w, r, httpx.NewBadRequest("LEGAL_NAME_REQUIRED", "legal_name is required."))
	case errors.Is(err, domain.ErrInvalidPhone):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_PHONE", "phone must be exactly 10 digits."))
	case errors.Is(err, domain.ErrDuplicatePhone):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_PHONE", "This phone number is already used by another customer/supplier."))
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

func (h *Handlers) listOrSearchParties(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q != "" {
		list, err := h.svc.SearchParties(r.Context(), principal(r), q, 20)
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"parties": list})
		return
	}
	list, err := h.svc.ListParties(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"parties": list})
}

type createPartyRequest struct {
	PartyType         string  `json:"party_type"`
	LegalName         string  `json:"legal_name"`
	TradeName         string  `json:"trade_name"`
	Phone             string  `json:"phone"`
	Email             string  `json:"email"`
	CurrencyCode      string  `json:"currency_code"`
	CreditLimitAmount *string `json:"credit_limit_amount"`
	PaymentTermsDays  *int    `json:"payment_terms_days"`
	Notes             string  `json:"notes"`
}

func (h *Handlers) createParty(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createPartyRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	var creditLimit *decimal.Decimal
	if req.CreditLimitAmount != nil {
		d, err := decimal.NewFromString(*req.CreditLimitAmount)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_CREDIT_LIMIT", "credit_limit_amount must be a decimal string."))
			return
		}
		creditLimit = &d
	}
	p, err := h.svc.CreateParty(r.Context(), principal(r), app.CreatePartyParams{
		PartyType: domain.PartyType(req.PartyType), LegalName: req.LegalName, TradeName: req.TradeName,
		Phone: req.Phone, Email: req.Email, CurrencyCode: req.CurrencyCode,
		CreditLimitAmount: creditLimit, PaymentTermsDays: req.PaymentTermsDays, Notes: req.Notes,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handlers) getParty(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	p, err := h.svc.GetParty(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handlers) listAddresses(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	list, err := h.svc.ListAddresses(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"addresses": list})
}

type addAddressRequest struct {
	AddressType string `json:"address_type"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	CountryCode string `json:"country_code"`
	IsDefault   bool   `json:"is_default"`
}

func (h *Handlers) addAddress(w http.ResponseWriter, r *http.Request) {
	partyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[addAddressRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	a, err := h.svc.AddAddress(r.Context(), principal(r), app.AddAddressParams{
		PartyID: partyID, AddressType: domain.AddressType(req.AddressType), Line1: req.Line1, Line2: req.Line2,
		City: req.City, State: req.State, PostalCode: req.PostalCode, CountryCode: req.CountryCode, IsDefault: req.IsDefault,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handlers) listTaxRegistrations(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	list, err := h.svc.ListTaxRegistrations(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tax_registrations": list})
}

type addTaxRegistrationRequest struct {
	CountryCode        string `json:"country_code"`
	RegistrationNumber string `json:"registration_number"`
	StateCode          string `json:"state_code"`
	IsPrimary          bool   `json:"is_primary"`
}

func (h *Handlers) addTaxRegistration(w http.ResponseWriter, r *http.Request) {
	partyID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[addTaxRegistrationRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	t, err := h.svc.AddTaxRegistration(r.Context(), principal(r), app.AddTaxRegistrationParams{
		PartyID: partyID, CountryCode: req.CountryCode, RegistrationNumber: req.RegistrationNumber,
		StateCode: req.StateCode, IsPrimary: req.IsPrimary,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handlers) lookupTaxRegistration(w http.ResponseWriter, r *http.Request) {
	number := chi.URLParam(r, "number")
	t, err := h.svc.LookupByRegistrationNumber(r.Context(), principal(r), number)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

// importParties bulk-imports customers/suppliers from an uploaded CSV or
// XLSX file (brief §53). Query params: format=csv|xlsx (required),
// dry_run=true|false (default false — pass true to validate and preview
// without writing anything). The request body is the raw file content.
func (h *Handlers) importParties(w http.ResponseWriter, r *http.Request) {
	rows, ok := parseImportBody(w, r)
	if !ok {
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	report, err := h.svc.ImportParties(r.Context(), principal(r), rows, dryRun)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

// parseImportBody reads and parses r.Body per the "format" query
// parameter, writing an error response and returning ok=false on
// failure. Shared shape every module's import endpoint uses.
func parseImportBody(w http.ResponseWriter, r *http.Request) ([]importer.Row, bool) {
	switch r.URL.Query().Get("format") {
	case "csv":
		rows, err := importer.ParseCSV(r.Body)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_CSV", "Could not parse the uploaded file as CSV: "+err.Error()))
			return nil, false
		}
		return rows, true
	case "xlsx":
		rows, err := importer.ParseXLSX(r.Body)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_XLSX", "Could not parse the uploaded file as XLSX: "+err.Error()))
			return nil, false
		}
		return rows, true
	default:
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FORMAT", `format query parameter must be "csv" or "xlsx".`))
		return nil, false
	}
}
