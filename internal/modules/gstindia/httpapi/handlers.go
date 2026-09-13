// Package httpapi is the gstindia module's HTTP transport layer — admin
// CRUD for tax_rate_master only. Mirrors internal/modules/catalogue/httpapi's
// shape.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/gstindia/app"
	"rechvix/internal/modules/gstindia/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Post("/gst/tax-rates", h.createRate)
	r.Post("/gst/tax-rates/import", h.importTaxRates)
	r.Get("/gst/tax-rates/{hsn}", h.listRatesByHSN)
	r.Get("/gst/state-codes", h.listStateCodes)
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

type createRateRequest struct {
	HSNSACCode     string  `json:"hsn_sac_code"`
	Classification string  `json:"classification"`
	GSTRate        string  `json:"gst_rate"`
	CessRate       string  `json:"cess_rate"`
	ValidFrom      string  `json:"valid_from"` // YYYY-MM-DD
	ValidTo        *string `json:"valid_to"`
}

func (h *Handlers) createRate(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createRateRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	gstRate, err := decimal.NewFromString(req.GSTRate)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_GST_RATE", "gst_rate must be a decimal string."))
		return
	}
	cessRate := decimal.Zero
	if req.CessRate != "" {
		cessRate, err = decimal.NewFromString(req.CessRate)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_CESS_RATE", "cess_rate must be a decimal string."))
			return
		}
	}
	validFrom, err := time.Parse("2006-01-02", req.ValidFrom)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VALID_FROM", "valid_from must be YYYY-MM-DD."))
		return
	}
	var validTo *time.Time
	if req.ValidTo != nil && *req.ValidTo != "" {
		t, err := time.Parse("2006-01-02", *req.ValidTo)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VALID_TO", "valid_to must be YYYY-MM-DD."))
			return
		}
		validTo = &t
	}
	classification := domain.RateClassification(req.Classification)
	if classification == "" {
		classification = domain.ClassificationTaxable
	}
	rate, err := h.svc.CreateRate(r.Context(), principal(r), app.CreateRateParams{
		HSNSACCode: req.HSNSACCode, Classification: classification,
		GSTRate: gstRate, CessRate: cessRate, ValidFrom: validFrom, ValidTo: validTo,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rate)
}

func (h *Handlers) listStateCodes(w http.ResponseWriter, r *http.Request) {
	states, err := h.svc.ListStateCodes(r.Context())
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"state_codes": states})
}

// importTaxRates bulk-imports tax_rate_master rows (and optionally
// updates a product's HSN/SAC code and stock) from an uploaded CSV or
// XLSX file (see app.Service.ImportTaxRates' own doc comment). Query
// params: format=csv|xlsx (required), dry_run=true|false (default
// false), warehouse_id (optional — only needed when the file carries a
// quantity column). The request body is the raw file content.
func (h *Handlers) importTaxRates(w http.ResponseWriter, r *http.Request) {
	rows, ok := parseImportBody(w, r)
	if !ok {
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	var warehouseID *uuid.UUID
	if raw := r.URL.Query().Get("warehouse_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id is not a valid UUID."))
			return
		}
		warehouseID = &id
	}
	report, err := h.svc.ImportTaxRates(r.Context(), principal(r), rows, dryRun, warehouseID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

// parseImportBody reads and parses r.Body per the "format" query
// parameter, writing an error response and returning ok=false on
// failure. Same shape as contacts/catalogue httpapi's identical helper —
// kept per-package rather than shared, same rationale as those.
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

func (h *Handlers) listRatesByHSN(w http.ResponseWriter, r *http.Request) {
	hsn := chi.URLParam(r, "hsn")
	list, err := h.svc.ListRatesByHSN(r.Context(), principal(r), hsn)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tax_rates": list})
}
