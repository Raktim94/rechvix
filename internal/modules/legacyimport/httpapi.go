package legacyimport

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Post("/migration/opening-balances/inspect", h.inspect(OpeningBalanceFields))
	r.Post("/migration/opening-balances/import", h.importOpening)
	r.Post("/migration/invoices/inspect", h.inspect(HistoricalInvoiceFields))
	r.Post("/migration/invoices/import", h.importInvoices)
	r.Get("/migration/invoices", h.listInvoices)
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_REQUEST", err.Error()))
	default:
		httpx.WriteError(w, r, err)
	}
}

// readRows parses the body: a spreadsheet, or (format=tally) a Tally day-book
// export, where the kind query parameter picks Sales or Purchase vouchers.
func readRows(r *http.Request, body io.Reader) ([]importer.Row, error) {
	if r.URL.Query().Get("format") == "tally" {
		k := importer.TallySalesVouchers
		if strings.EqualFold(r.URL.Query().Get("kind"), "PURCHASE") {
			k = importer.TallyPurchaseVouchers
		}
		return importer.ParseTally(body, k)
	}
	return importer.ParseFormat(r.URL.Query().Get("format"), body)
}

// mapped reads the upload, then applies the optional mapping query parameter (or the suggested mapping).
func mapped(w http.ResponseWriter, r *http.Request, fields []importer.Field) ([]importer.Row, bool) {
	rows, err := readRows(r, http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FILE", "Could not read the uploaded file: "+err.Error()))
		return nil, false
	}
	mapping, err := importer.ParseMapping(r.URL.Query().Get("mapping"))
	if err == nil {
		rows, err = importer.ApplyMapping(rows, fields, mapping)
	}
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_MAPPING", err.Error()))
		return nil, false
	}
	return rows, true
}

func (h *Handlers) inspect(fields []importer.Field) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := readRows(r, http.MaxBytesReader(w, r.Body, 32<<20))
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FILE", "Could not read the uploaded file: "+err.Error()))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, importer.Inspect(rows, fields))
	}
}

func (h *Handlers) importOpening(w http.ResponseWriter, r *http.Request) {
	rows, ok := mapped(w, r, OpeningBalanceFields)
	if !ok {
		return
	}
	asOf, _ := ParseDate(r.URL.Query().Get("as_of")) // zero means today
	if raw := r.URL.Query().Get("as_of"); raw != "" && asOf.IsZero() {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "as_of must be a date such as 2026-04-01."))
		return
	}
	report, err := h.svc.ImportOpeningBalances(r.Context(), principal(r), rows, asOf, r.URL.Query().Get("dry_run") == "true")
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

func (h *Handlers) importInvoices(w http.ResponseWriter, r *http.Request) {
	rows, ok := mapped(w, r, HistoricalInvoiceFields)
	if !ok {
		return
	}
	report, err := h.svc.ImportHistoricalInvoices(r.Context(), principal(r), strings.ToUpper(r.URL.Query().Get("kind")), rows, r.URL.Query().Get("dry_run") == "true")
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

func (h *Handlers) listInvoices(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := h.svc.ListHistoricalInvoices(r.Context(), principal(r), strings.ToUpper(r.URL.Query().Get("kind")), r.URL.Query().Get("q"), limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"invoices": rows})
}
