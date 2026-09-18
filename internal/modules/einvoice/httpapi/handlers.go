// Package httpapi is the e-Invoice module's HTTP transport layer. IRN
// generation itself is fully automatic (sales.FinalizeDocument enqueues
// einvoice.generate, apps/worker's outbox poller does the rest, docs/
// architecture.md §9), so the read-only status endpoint was originally
// this package's only route. retryDocument is the one real exception:
// a FAILED_FINAL record (e.g. "legal entity has no GSTIN configured")
// is deliberately never retried by the outbox — see app.Service.
// RetryDocument's own doc comment — so once the underlying problem is
// actually fixed, this is the only way to get an IRN for that already-
// finalized invoice. Mirrors ewaybill/httpapi's shape: einvoice/app.
// Service is not self-scoping, so this handler does its own permission
// check and its own database.Pool.RunScoped wrap.
//
// Permission codes reused from the existing catalog (migrations/0002):
// "sales.view" for read-only status (same reasoning as ewaybill/httpapi's
// identical choice — no separate einvoice.view code, not worth a new
// migration), "einvoice.generate" for retry (already seeded for exactly
// this module, same precedent as ewaybill/httpapi reusing
// "ewaybill.generate" for its own mutating actions).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/einvoice/app"
	"rechvix/internal/modules/einvoice/domain"
	"rechvix/internal/platform/database"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct {
	svc         *app.Service
	pool        *database.Pool
	permissions *permissions.Checker
}

func NewHandlers(svc *app.Service, pool *database.Pool, checker *permissions.Checker) *Handlers {
	return &Handlers{svc: svc, pool: pool, permissions: checker}
}

// decodeJSON mirrors every other module's own identically-shaped helper
// (e.g. accounting/httpapi's) — same DisallowUnknownFields strictness,
// duplicated per package rather than shared, matching the existing
// convention across this codebase.
func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/sales/documents/{id}/einvoice", h.getStatus)
	r.Post("/sales/documents/{id}/einvoice/retry", h.retryDocument)
	r.Get("/legal-entities/{id}/einvoice-credentials", h.getCredentialsStatus)
	r.Put("/legal-entities/{id}/einvoice-credentials", h.saveCredentials)
	r.Delete("/legal-entities/{id}/einvoice-credentials", h.deleteCredentials)
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

// getStatus returns the e-Invoice record for a document, or {"record":
// null} if IRN generation was never applicable, or hasn't run yet
// (e.g. the outbox worker hasn't picked up the event, or is down).
func (h *Handlers) getStatus(w http.ResponseWriter, r *http.Request) {
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "sales.view", permissions.Scope{}); err != nil {
		var forbidden *permissions.ErrForbidden
		if errors.As(err, &forbidden) {
			httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
			return
		}
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	var rec *domain.Record
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		rec, err = h.svc.GetRecordForDocument(ctx, p.OrganisationID, docID)
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"record": rec})
}

// retryDocument re-attempts IRN generation for a document whose e-Invoice
// record is FAILED_FINAL or FAILED_RETRYABLE — see app.Service.
// RetryDocument's doc comment for why the outbox alone can never do this
// for a FAILED_FINAL record. Returns the same {"record": ...} shape as
// getStatus so the frontend can refresh its view from the response
// directly, whether the retry succeeded or failed again.
func (h *Handlers) retryDocument(w http.ResponseWriter, r *http.Request) {
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	p := principal(r)
	if err := h.permissions.Require(r.Context(), p, "einvoice.generate", permissions.Scope{}); err != nil {
		var forbidden *permissions.ErrForbidden
		if errors.As(err, &forbidden) {
			httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
			return
		}
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}

	var retryErr error
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		retryErr = h.svc.RetryDocument(ctx, p.OrganisationID, docID)
		return nil // retryErr is reported to the caller below, not treated as a transaction-aborting error
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	if retryErr != nil {
		switch {
		case errors.Is(retryErr, domain.ErrNotFound):
			httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "No e-Invoice record exists yet for this document."))
		case errors.Is(retryErr, domain.ErrNotRetryable):
			httpx.WriteError(w, r, httpx.NewConflict("NOT_RETRYABLE", "This e-Invoice isn't in a failed state, so there's nothing to retry."))
		default:
			// A real generation failure (still no GSTIN configured, the
			// provider rejected the request again, etc.) — worth showing
			// to the user directly rather than a generic message, same as
			// the error already surfaced on the record itself.
			httpx.WriteError(w, r, httpx.NewBadRequest("RETRY_FAILED", retryErr.Error()))
		}
		return
	}

	var rec *domain.Record
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		rec, err = h.svc.GetRecordForDocument(ctx, p.OrganisationID, docID)
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"record": rec})
}

// requireSettingsManage is shared by all three credentials endpoints below
// — GST API credentials are exactly the kind of thing "settings.manage"
// (the same permission organisation/httpapi's own updateInvoiceBranding
// requires) already exists to gate, not general sales.view territory.
func (h *Handlers) requireSettingsManage(w http.ResponseWriter, r *http.Request) bool {
	if err := h.permissions.Require(r.Context(), principal(r), "settings.manage", permissions.Scope{}); err != nil {
		var forbidden *permissions.ErrForbidden
		if errors.As(err, &forbidden) {
			httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
			return false
		}
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return false
	}
	return true
}

func legalEntityID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "id"))
}

// getCredentialsStatus never returns ClientSecret/Password — see
// app.Service.CredentialsStatus's own doc comment.
func (h *Handlers) getCredentialsStatus(w http.ResponseWriter, r *http.Request) {
	id, err := legalEntityID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	if !h.requireSettingsManage(w, r) {
		return
	}
	p := principal(r)
	var status app.CredentialsStatus
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		status, err = h.svc.GetCredentialsStatus(ctx, p.OrganisationID, id)
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, status)
}

type saveCredentialsRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	GSTIN        string `json:"gstin"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	BaseURL      string `json:"base_url"`
}

func (h *Handlers) saveCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := legalEntityID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[saveCredentialsRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if req.ClientID == "" || req.ClientSecret == "" || req.GSTIN == "" || req.Username == "" || req.Password == "" {
		httpx.WriteError(w, r, httpx.NewBadRequest("MISSING_FIELDS", "client_id, client_secret, gstin, username, and password are all required."))
		return
	}
	if !h.requireSettingsManage(w, r) {
		return
	}
	p := principal(r)
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		return h.svc.SaveSandboxCredentials(ctx, p.OrganisationID, id, app.SandboxCredentials{
			ClientID: req.ClientID, ClientSecret: req.ClientSecret, GSTIN: req.GSTIN,
			Username: req.Username, Password: req.Password, BaseURL: req.BaseURL,
		})
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	var status app.CredentialsStatus
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		var err error
		status, err = h.svc.GetCredentialsStatus(ctx, p.OrganisationID, id)
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, status)
}

func (h *Handlers) deleteCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := legalEntityID(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	if !h.requireSettingsManage(w, r) {
		return
	}
	p := principal(r)
	err = h.pool.RunScoped(r.Context(), p.OrganisationID, func(ctx context.Context) error {
		return h.svc.DeleteCredentials(ctx, p.OrganisationID, id)
	})
	if err != nil {
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
