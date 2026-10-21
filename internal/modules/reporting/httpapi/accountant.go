package httpapi

import (
	"errors"
	"net/http"
	"rechvix/internal/modules/reporting/app"
	"rechvix/internal/modules/reporting/domain"
	httpx "rechvix/internal/platform/http"
	"time"
)

func (h *Handlers) accountantPackage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for k := range q {
		if k != "from" && k != "to" && k != "legal_entity_id" && k != "format" {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FILTER", "Accountant exports cover the complete company; unsupported filter: "+k))
			return
		}
	}
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if f.LegalEntityID == nil || f.From == nil || f.To == nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("REQUIRED_PERIOD_COMPANY", "Select a company, start date and end date."))
		return
	}
	format := q.Get("format")
	if format != "" && format != "json" && format != "zip" && format != "xlsx" {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FORMAT", "Choose json, zip or xlsx."))
		return
	}
	pack, err := h.svc.AccountantPackage(r.Context(), principal(r), *f.LegalEntityID, *f.From, *f.To)
	if err != nil {
		if errors.Is(err, domain.ErrAccountantCompanyNotFound) {
			httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "Company not found."))
		} else if errors.Is(err, app.ErrInvalidAccountantRequest) {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ACCOUNTANT_EXPORT", err.Error()))
		} else {
			writeServiceError(w, r, err)
		}
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if format == "" || format == "json" {
		httpx.WriteJSON(w, http.StatusOK, pack)
		return
	}
	var body []byte
	var contentType string
	if format == "zip" {
		body, err = pack.ZIP()
		contentType = "application/zip"
	} else {
		body, err = pack.Workbook()
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: 500, Code: "EXPORT_FAILED", Message: "Could not create the accountant files. No partial archive was sent.", Cause: err})
		return
	}
	w.Header().Set("Content-Type", contentType)
	// Identifier/date-only filename, never interpolated user-provided business names.
	w.Header().Set("Content-Disposition", `attachment; filename="accountant-`+pack.Company.Country+`-`+f.From.Format(time.DateOnly)+`-`+f.To.Format(time.DateOnly)+`.`+format+`"`)
	_, _ = w.Write(body)
}
