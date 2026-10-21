package sepa

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/sepa/settings", h.getSettings)
	r.Put("/sepa/settings", h.putSettings)
	r.Get("/sepa/mandates", h.mandates)
	r.Post("/sepa/mandates", h.createMandate)
	r.Post("/sepa/mandates/{id}/revoke", h.revokeMandate)
	r.Get("/sepa/batches", h.batches)
	r.Post("/sepa/batches", h.createBatch)
	r.Get("/sepa/batches/{id}/collections", h.collections)
	r.Get("/sepa/batches/{id}/file.xml", h.file)
	r.Post("/sepa/batches/{id}/sent", h.sent)
	r.Delete("/sepa/batches/{id}", h.discard)
	r.Post("/sepa/collections/{id}/result", h.result)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_REQUEST", err.Error()))
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "Not found."))
	default:
		httpx.WriteError(w, r, err)
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func pid(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	v, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return uuid.Nil, false
	}
	return v, true
}

func qid(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	v, err := uuid.Parse(r.URL.Query().Get(key))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", key+" must be a UUID."))
		return uuid.Nil, false
	}
	return v, true
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	dec := json.NewDecoder(io.LimitReader(r.Body, 65536))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return v, false
	}
	return v, true
}

func (h *Handlers) getSettings(w http.ResponseWriter, r *http.Request) {
	le, ok := qid(w, r, "legal_entity_id")
	if !ok {
		return
	}
	s, err := h.svc.GetSettings(r.Context(), principal(r), le)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handlers) putSettings(w http.ResponseWriter, r *http.Request) {
	le, ok := qid(w, r, "legal_entity_id")
	if !ok {
		return
	}
	b, ok := decode[Settings](w, r)
	if !ok {
		return
	}
	if err := h.svc.SaveSettings(r.Context(), principal(r), le, b); err != nil {
		fail(w, r, err)
		return
	}
	h.getSettings(w, r)
}

func (h *Handlers) mandates(w http.ResponseWriter, r *http.Request) {
	var party *uuid.UUID
	if v, err := uuid.Parse(r.URL.Query().Get("party_id")); err == nil {
		party = &v
	}
	rows, err := h.svc.Mandates(r.Context(), principal(r), party)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"mandates": rows})
}

func (h *Handlers) createMandate(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[struct {
		PartyID       uuid.UUID `json:"party_id"`
		Ref           string    `json:"mandate_ref"`
		SignedOn      string    `json:"signed_on"`
		IBAN          string    `json:"iban"`
		BIC           string    `json:"bic"`
		AccountHolder string    `json:"account_holder"`
	}](w, r)
	if !ok {
		return
	}
	signed, err := time.Parse("2006-01-02", b.SignedOn)
	if err != nil {
		fail(w, r, ErrInvalid)
		return
	}
	m, err := h.svc.CreateMandate(r.Context(), principal(r), MandateInput{PartyID: b.PartyID, Ref: b.Ref, SignedOn: signed, IBAN: b.IBAN, BIC: b.BIC, AccountHolder: b.AccountHolder})
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, m)
}

func (h *Handlers) revokeMandate(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.RevokeMandate(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) batches(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Batches(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batches": rows})
}

func (h *Handlers) createBatch(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[struct {
		LegalEntityID  uuid.UUID   `json:"legal_entity_id"`
		CollectionDate string      `json:"collection_date"`
		DocumentIDs    []uuid.UUID `json:"document_ids"`
	}](w, r)
	if !ok {
		return
	}
	d, err := time.Parse("2006-01-02", b.CollectionDate)
	if err != nil {
		fail(w, r, ErrInvalid)
		return
	}
	batch, err := h.svc.CreateBatch(r.Context(), principal(r), b.LegalEntityID, d, b.DocumentIDs)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, batch)
}

func (h *Handlers) collections(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	rows, err := h.svc.Collections(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"collections": rows})
}

func (h *Handlers) file(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	xml, name, err := h.svc.XML(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write([]byte(xml))
}

func (h *Handlers) sent(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.MarkSent(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	h.batches(w, r)
}

func (h *Handlers) discard(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.Discard(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) result(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		Collected bool   `json:"collected"`
		Reason    string `json:"reason"`
	}](w, r)
	if !ok {
		return
	}
	if err := h.svc.Result(r.Context(), principal(r), id, b.Collected, b.Reason); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
