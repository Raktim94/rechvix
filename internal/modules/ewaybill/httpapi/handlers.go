// Package httpapi is the free-first e-Way Bill workflow's HTTP transport
// layer (docs/architecture.md §9b, Stage 8c). Unlike most modules' httpapi
// packages, ewaybill/app.Service is NOT self-scoping (its own doc comments
// say so explicitly: "must run inside a caller-provided RunScoped block ...
// only intended caller is httpapi") — every handler here does its own
// permission check and its own database.Pool.RunScoped wrap, mirroring
// what organisation/logistics's app-layer Service.* methods do internally.
//
// Permission codes reused from the existing catalog (migrations/0002):
// "sales.view" for read-only status, "ewaybill.generate" for anything that
// prepares/records/imports a result — there is no separate ewaybill.view
// code, and inventing one for a single read endpoint isn't worth a new
// migration when sales.view already gates "can this person see this
// invoice's details."
package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/ewaybill/app"
	"rechvix/internal/modules/ewaybill/domain"
	"rechvix/internal/modules/ewaybill/govportal"
	"rechvix/internal/modules/ewaybill/portal"
	"rechvix/internal/platform/database"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct {
	svc         *app.Service
	pool        *database.Pool
	permissions *permissions.Checker
	portal      *govportal.Service
}

func NewHandlers(svc *app.Service, pool *database.Pool, checker *permissions.Checker, portalSvc *govportal.Service) *Handlers {
	return &Handlers{svc: svc, pool: pool, permissions: checker, portal: portalSvc}
}

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/sales/documents/{id}/ewaybill", h.getStatus)
	r.Post("/sales/documents/{id}/ewaybill/prepare", h.prepare)
	r.Post("/sales/documents/{id}/ewaybill/transport-info", h.updateTransportInfo)
	r.Post("/sales/documents/{id}/ewaybill/manual-result", h.manualResult)
	r.Post("/sales/documents/{id}/ewaybill/import-result", h.importResult)
	r.Get("/ewaybill/portal-url", h.portalURL)
	r.Post("/ewaybill/portal-batch", h.prepareBatch)
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func documentID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "id"))
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "No e-Way Bill information exists yet for this document."))
	case errors.Is(err, domain.ErrNotEligible):
		httpx.WriteError(w, r, httpx.NewConflict("NOT_ELIGIBLE", "This document does not require an e-Way Bill."))
	case errors.Is(err, domain.ErrResultMismatch):
		httpx.WriteError(w, r, httpx.NewConflict("RESULT_MISMATCH", "This e-Way Bill result appears to belong to another invoice."))
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

// getStatus evaluates (and persists) the current eligibility/status for a
// document's e-Way Bill and returns both the eligibility result and the
// underlying record, if one exists yet.
func (h *Handlers) getStatus(w http.ResponseWriter, r *http.Request) {
	docID, err := documentID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "sales.view", permissions.Scope{}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	var elig *app.EligibilityResult
	var rec *domain.Record
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		elig, err = h.svc.EvaluateEligibility(ctx, p.OrganisationID, docID)
		if err != nil {
			return err
		}
		rec, err = h.svc.GetRecordForDocument(ctx, p.OrganisationID, docID)
		return err
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"eligibility": elig, "record": rec})
}

func (h *Handlers) prepare(w http.ResponseWriter, r *http.Request) {
	docID, err := documentID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "ewaybill.generate", permissions.Scope{}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	var file struct {
		FileName string
		Content  []byte
	}
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		f, _, err := h.svc.PrepareFreePortalUpload(ctx, p.OrganisationID, docID)
		if err != nil {
			return err
		}
		file.FileName, file.Content = f.FileName, f.Content
		return nil
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file.FileName+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
}

type prepareBatchRequest struct {
	SalesDocumentIDs []uuid.UUID `json:"sales_document_ids"`
}

// prepareBatch is the bulk-selection queue docs/architecture.md §9b
// describes and Stage 8c's own pass left explicitly unbuilt ("no
// endpoint to select multiple invoices"). Returns one ZIP containing
// every batch file app.Service.PrepareFreePortalUploadBatch produced
// (portal.MaxFileSizeBytes-bounded, so a large selection is several
// numbered files, not one — the government portal's own upload limit,
// same reasoning the single-document prepare endpoint's file already
// respects) plus a MANIFEST.txt listing any document that couldn't be
// included and why, so a skip is visible to the operator instead of
// silently missing from the download.
func (h *Handlers) prepareBatch(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "ewaybill.generate", permissions.Scope{}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	req, err := decodeJSON[prepareBatchRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if len(req.SalesDocumentIDs) == 0 {
		httpx.WriteError(w, r, httpx.NewBadRequest("EMPTY_SELECTION", "Select at least one invoice."))
		return
	}

	var batches []portal.PreparedFile
	var skipped []app.BatchSkip
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		batches, skipped, err = h.svc.PrepareFreePortalUploadBatch(ctx, p.OrganisationID, req.SalesDocumentIDs)
		return err
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if len(batches) == 0 {
		httpx.WriteError(w, r, httpx.NewBadRequest("NONE_ELIGIBLE", "None of the selected invoices could be prepared — see the reasons and try individually from each invoice's own page."))
		return
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	for _, f := range batches {
		zf, err := zw.Create(f.FileName)
		if err != nil {
			httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "Could not build the batch archive.", Cause: err})
			return
		}
		if _, err := zf.Write(f.Content); err != nil {
			httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "Could not build the batch archive.", Cause: err})
			return
		}
	}
	manifest, err := zw.Create("MANIFEST.txt")
	if err == nil {
		fmt.Fprintf(manifest, "Prepared %d batch file(s) from %d invoice(s); %d skipped.\n\n", len(batches), len(req.SalesDocumentIDs), len(skipped))
		if len(skipped) > 0 {
			fmt.Fprintln(manifest, "Skipped:")
			for _, s := range skipped {
				fmt.Fprintf(manifest, "  %s: %s\n", s.SalesDocumentID, s.Reason)
			}
		}
	}
	if err := zw.Close(); err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "Could not finalize the batch archive.", Cause: err})
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="ewaybill-batch-`+time.Now().Format("20060102-150405")+`.zip"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(zipBuf.Bytes())
}

type updateTransportInfoRequest struct {
	VehicleNumber   *string `json:"vehicle_number"`
	TransporterID   *string `json:"transporter_id"`
	TransporterName *string `json:"transporter_name"`
	DistanceKM      *string `json:"distance_km"`
}

func (h *Handlers) updateTransportInfo(w http.ResponseWriter, r *http.Request) {
	docID, err := documentID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[updateTransportInfoRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	params := app.TransportInfoParams{VehicleNumber: req.VehicleNumber, TransporterID: req.TransporterID, TransporterName: req.TransporterName}
	if req.DistanceKM != nil {
		d, err := decimal.NewFromString(*req.DistanceKM)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DISTANCE", "distance_km must be a decimal string."))
			return
		}
		params.DistanceKM = &d
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "ewaybill.generate", permissions.Scope{}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	var elig *app.EligibilityResult
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		elig, err = h.svc.UpdateTransportInfo(ctx, p.OrganisationID, docID, params)
		return err
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, elig)
}

type manualResultRequest struct {
	EWBNumber  string    `json:"ewb_number"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
}

func (h *Handlers) manualResult(w http.ResponseWriter, r *http.Request) {
	docID, err := documentID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[manualResultRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "ewaybill.generate", permissions.Scope{}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	var rec *domain.Record
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		rec, err = h.svc.RecordManualResult(ctx, p.OrganisationID, docID, app.ManualResultParams{
			EWBNumber: req.EWBNumber, ValidFrom: req.ValidFrom, ValidUntil: req.ValidUntil,
		})
		return err
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

type importResultRequest struct {
	EWBNumber            string    `json:"ewb_number"`
	ValidFrom            time.Time `json:"valid_from"`
	ValidUntil           time.Time `json:"valid_until"`
	ClaimedInvoiceNumber string    `json:"claimed_invoice_number"`
	ClaimedInvoiceDate   time.Time `json:"claimed_invoice_date"`
	ClaimedSupplierGSTIN string    `json:"claimed_supplier_gstin"`
	ClaimedDocumentType  string    `json:"claimed_document_type"`
}

// importResult is the "Import Government File" path (docs/architecture.md
// §9b). This pass only accepts already-parsed fields, not raw file/PDF
// upload+parsing — parsing the government portal's own result file/PDF
// format is real, separate scope (format not yet reverse-engineered from a
// live example) left for a follow-up pass; the manual-entry path above is
// the universal fallback and is fully functional today.
func (h *Handlers) importResult(w http.ResponseWriter, r *http.Request) {
	docID, err := documentID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[importResultRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "ewaybill.generate", permissions.Scope{}); err != nil {
		writeServiceError(w, r, err)
		return
	}
	var rec *domain.Record
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		rec, err = h.svc.ImportAndVerifyResult(ctx, p.OrganisationID, docID, app.ImportedResultParams{
			ManualResultParams:   app.ManualResultParams{EWBNumber: req.EWBNumber, ValidFrom: req.ValidFrom, ValidUntil: req.ValidUntil},
			ClaimedInvoiceNumber: req.ClaimedInvoiceNumber, ClaimedInvoiceDate: req.ClaimedInvoiceDate,
			ClaimedSupplierGSTIN: req.ClaimedSupplierGSTIN, ClaimedDocumentType: req.ClaimedDocumentType,
		})
		return err
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

// portalURL exposes govportal.Service's fixed, backend-configured,
// allowlisted URL (docs/architecture.md §9b — "never a user-editable
// arbitrary URL"). Any authenticated user may read it; there is nothing
// tenant-specific about it.
func (h *Handlers) portalURL(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": h.portal.GetOfficialEWayBillPortalURL()})
}
