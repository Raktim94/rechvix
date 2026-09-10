// Package httpapi is the reporting module's HTTP transport layer. Mirrors
// internal/modules/accounting/httpapi's shape. Every listing endpoint
// accepts an optional format=csv|xlsx|json|pdf query parameter (default
// json) and reuses internal/platform/export's writers — the export
// mechanism is written once here, not per report.
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/reporting/app"
	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/platform/export"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/reports/dashboard", h.dashboard)
	r.Get("/reports/sales/summary", h.salesSummary)
	r.Get("/reports/sales/invoices", h.salesInvoices)
	r.Get("/reports/sales/gross-profit", h.grossProfit)
	r.Get("/reports/purchases/summary", h.purchaseSummary)
	r.Get("/reports/purchases/documents", h.purchaseDocuments)
	r.Get("/reports/inventory/valuation", h.stockValuation)
	r.Get("/reports/inventory/low-stock", h.lowStock)
	r.Get("/reports/inventory/movements", h.stockMovements)
	r.Get("/reports/accounting/trial-balance", h.trialBalance)
	r.Get("/reports/accounting/receivables", h.receivables)
	r.Get("/reports/accounting/payables", h.payables)
	r.Get("/reports/accounting/accounts/{id}/ledger", h.accountLedger)
	r.Get("/reports/tax/hsn-summary", h.hsnSummary)
	r.Get("/reports/tax/rate-summary", h.taxRateSummary)
	r.Get("/reports/tax/gstr1", h.gstr1)
	r.Get("/reports/tax/gstr3b", h.gstr3b)
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to view this report."))
	case errors.Is(err, app.ErrInvalidGroupDimension):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_GROUP_BY", "group_by must be one of: day, month, customer, supplier, product, category, salesperson, branch, warehouse."))
	default:
		httpx.WriteError(w, r, err)
	}
}

// --- filter parsing (brief §22 — the shared query-building helper's HTTP
// side: one function turns query params into domain.Filter, instead of
// each handler hand-parsing its own subset) ---

func parseFilter(r *http.Request) (domain.Filter, error) {
	q := r.URL.Query()
	var f domain.Filter
	if v := q.Get("from"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return f, errBadDate("from", v)
		}
		f.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return f, errBadDate("to", v)
		}
		f.To = &t
	}
	if id, ok, err := parseOptionalUUID(q, "branch_id"); err != nil {
		return f, err
	} else if ok {
		f.BranchID = &id
	}
	if id, ok, err := parseOptionalUUID(q, "warehouse_id"); err != nil {
		return f, err
	} else if ok {
		f.WarehouseID = &id
	}
	if id, ok, err := parseOptionalUUID(q, "legal_entity_id"); err != nil {
		return f, err
	} else if ok {
		f.LegalEntityID = &id
	}
	if id, ok, err := parseOptionalUUID(q, "customer_id"); err != nil {
		return f, err
	} else if ok {
		f.CustomerPartyID = &id
	}
	if id, ok, err := parseOptionalUUID(q, "supplier_id"); err != nil {
		return f, err
	} else if ok {
		f.SupplierPartyID = &id
	}
	if id, ok, err := parseOptionalUUID(q, "product_variant_id"); err != nil {
		return f, err
	} else if ok {
		f.ProductVariantID = &id
	}
	if v := q.Get("hsn_sac_code"); v != "" {
		f.HSNSACCode = &v
	}
	if v := q.Get("document_type"); v != "" {
		f.DocumentType = &v
	}
	if v := q.Get("status"); v != "" {
		f.Status = &v
	}
	return f, nil
}

func parseOptionalUUID(q map[string][]string, key string) (uuid.UUID, bool, error) {
	v := ""
	if vals, ok := q[key]; ok && len(vals) > 0 {
		v = vals[0]
	}
	if v == "" {
		return uuid.UUID{}, false, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.UUID{}, false, httpx.NewBadRequest("INVALID_"+key, "Invalid UUID for "+key+".")
	}
	return id, true, nil
}

func errBadDate(param, value string) error {
	return httpx.NewBadRequest("INVALID_DATE", "Invalid date for "+param+" (want YYYY-MM-DD, got "+value+").")
}

// writeTable renders t in the format requested by the "format" query
// parameter (csv|xlsx|pdf|json, default json) — the one place every
// report handler's export logic converges.
func writeTable(w http.ResponseWriter, r *http.Request, t export.Table) {
	format := r.URL.Query().Get("format")
	name := reportFilename(t.Title)
	var err error
	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
		err = export.WriteCSV(w, t)
	case "xlsx":
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.xlsx"`)
		err = export.WriteXLSX(w, t)
	case "pdf":
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.pdf"`)
		err = export.WriteTablePDF(w, t)
	default:
		w.Header().Set("Content-Type", "application/json")
		err = export.WriteJSON(w, t)
	}
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("EXPORT_FAILED", "Could not render the report in the requested format."))
	}
}

// reportFilename turns a report's human title (e.g. "GSTR-1 Summary") plus
// today's date into a safe download filename ("gstr-1-summary-2026-09-07")
// — the previous hardcoded "report.csv" collided across every report and
// every export, forcing the user to rename each download by hand to tell
// them apart.
func reportFilename(title string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, title)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "report"
	}
	return slug + "-" + time.Now().Format("2006-01-02")
}
