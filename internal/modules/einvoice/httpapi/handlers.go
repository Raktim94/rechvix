// Package httpapi is the e-Invoice module's HTTP transport layer — a
// single read-only status endpoint. Unlike ewaybill's free-portal flow,
// IRN generation itself is fully automatic (sales.FinalizeDocument
// enqueues einvoice.generate, apps/worker's outbox poller does the
// rest, docs/architecture.md §9) — there is nothing for a user to
// trigger here, only a result to look at. Mirrors ewaybill/httpapi's
// shape: einvoice/app.Service is not self-scoping, so this handler does
// its own permission check and its own database.Pool.RunScoped wrap.
//
// Permission code reused from the existing catalog: "sales.view" — same
// reasoning as ewaybill/httpapi's identical choice, there is no separate
// einvoice.view code and inventing one for a single read endpoint isn't
// worth a new migration.
package httpapi

import (
	"context"
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

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/sales/documents/{id}/einvoice", h.getStatus)
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
