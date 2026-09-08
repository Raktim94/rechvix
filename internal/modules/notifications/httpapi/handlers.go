package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/notifications/app"
	"rechvix/internal/modules/notifications/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

// DocumentRenderer resolves a redeemed share link's (organisation,
// creator, document type, document id) to a printable document's raw
// bytes — wired from the composition root (apps/server/main.go, via
// WithDocumentRenderer), same WithX-returns-a-copy convention as
// identity's WithPostBootstrapHook and sales/httpapi's WithEWayBill.
// notifications can't import sales directly (docs/adr/0003's layering
// rule — a lower module doesn't import a higher one), so the actual
// rendering call lives in main.go, which already constructs both.
//
// createdBy is passed through, not dropped: the real authorization for
// this anonymous read is "impersonate the link's own creator, who
// already passed a real permission check when they created it" (see
// notifications/app.Service.RedeemShareLink's doc comment) — not a
// bypass, and it fails closed if that user's own access has since been
// revoked. This function itself performs no authorization of its own;
// the caller (redeemPDF below) only ever invokes it with values from an
// already-validated, unexpired, unrevoked share_links row.
type DocumentRenderer func(ctx context.Context, orgID, createdBy uuid.UUID, documentType string, documentID uuid.UUID) (data []byte, contentType, filename string, err error)

type Handlers struct {
	svc      *app.Service
	renderer DocumentRenderer
}

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) WithDocumentRenderer(fn DocumentRenderer) *Handlers {
	cp := *h
	cp.renderer = fn
	return &cp
}

// Mount registers the authenticated document-sharing routes into the
// same authenticated group every other module mounts into
// (apps/server/main.go). MountPublic registers the UNAUTHENTICATED
// redeem endpoints separately — a share link's whole point is that the
// recipient has no session or API key (brief §21).
func (h *Handlers) Mount(r chi.Router) {
	r.Post("/share-links", h.create)
	r.Get("/share-links", h.list)
	r.Delete("/share-links/{id}", h.revoke)
	r.Post("/notifications/send", h.send)
}

func (h *Handlers) MountPublic(r chi.Router) {
	r.Get("/share/{token}", h.redeem)
	r.Get("/share/{token}/pdf", h.redeemPDF)
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
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, domain.ErrLinkInvalid):
		httpx.WriteError(w, r, httpx.NewNotFound("LINK_INVALID", "This link is invalid, expired, or revoked."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

type createShareLinkRequest struct {
	DocumentType string    `json:"document_type"`
	DocumentID   uuid.UUID `json:"document_id"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[createShareLinkRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	token, err := h.svc.CreateShareLink(r.Context(), principal, req.DocumentType, req.DocumentID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	// Shown exactly once — the URL a caller builds from this token is the
	// only copy; the server never returns it again (brief §21).
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"token": token})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	documentType := r.URL.Query().Get("document_type")
	documentID, err := uuid.Parse(r.URL.Query().Get("document_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DOCUMENT_ID", "document_id query parameter must be a UUID."))
		return
	}
	links, err := h.svc.ListShareLinksForDocument(r.Context(), principal, documentType, documentID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"share_links": links})
}

func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	if err := h.svc.RevokeShareLink(r.Context(), principal, id); err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// redeem is intentionally minimal: it proves the token is valid and
// reports what it grants access to. Actually streaming the shared
// document's PDF bytes to an anonymous, un-authenticated recipient needs
// a dedicated "system-level" cross-module read path (every existing
// print/report method requires a permissions.Principal, which an
// anonymous link recipient by definition doesn't have) — flagged as a
// real, undone follow-up rather than quietly bypassed with a fabricated
// principal.
func (h *Handlers) redeem(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	link, err := h.svc.RedeemShareLink(r.Context(), token)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"document_type": link.DocumentType, "document_id": link.DocumentID})
}

// redeemPDF is the URL a WhatsApp/email share message actually points
// at — unlike redeem above, it streams the real document (today: sales
// invoices/quotations/etc. via the A4 template) inline, so the recipient
// sees it directly in their browser with no app/JS of their own needed.
// Falls back to a plain 404-shaped error (not a 500) when no renderer is
// wired (e.g. a document type this pass doesn't support yet) or the
// document itself can't be resolved, rather than exposing which case it
// was — same "don't leak detail" posture as an invalid token.
func (h *Handlers) redeemPDF(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	link, err := h.svc.RedeemShareLink(r.Context(), token)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.renderer == nil {
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_AVAILABLE", "This link can't be opened yet."))
		return
	}
	data, contentType, filename, err := h.renderer(r.Context(), link.OrganisationID, link.CreatedBy, link.DocumentType, link.DocumentID)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_AVAILABLE", "This link can't be opened right now."))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+filename+`"`)
	w.Write(data)
}

type sendRequest struct {
	Channel      string    `json:"channel"`
	Recipient    string    `json:"recipient"`
	DocumentType string    `json:"document_type"`
	DocumentID   uuid.UUID `json:"document_id"`
	Subject      string    `json:"subject,omitempty"`
	BodyHTML     string    `json:"body_html,omitempty"`
	TemplateName string    `json:"template_name,omitempty"`
	TemplateArgs []string  `json:"template_args,omitempty"`
}

func (h *Handlers) send(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[sendRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	err = h.svc.QueueSend(r.Context(), principal, app.SendPayload{
		Channel: domain.Channel(req.Channel), Recipient: req.Recipient,
		DocumentType: req.DocumentType, DocumentID: req.DocumentID,
		Subject: req.Subject, BodyHTML: req.BodyHTML,
		TemplateName: req.TemplateName, TemplateArgs: req.TemplateArgs,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}
