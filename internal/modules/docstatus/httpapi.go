package docstatus

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/document-statuses", h.list)
	r.Post("/document-statuses", h.create)
	r.Put("/document-statuses/{id}", h.update)
	r.Delete("/document-statuses/{id}", h.delete)
	r.Get("/document-statuses/assignments", h.assignments)
	r.Put("/sales/documents/{id}/custom-status", h.assign)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrDuplicate):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE", err.Error()))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_STATUS", err.Error()))
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

type body struct {
	Name      string `json:"name"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
	Active    *bool  `json:"active"`
}

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return v, false
	}
	return v, true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.List(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"statuses": rows})
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[body](w, r)
	if !ok {
		return
	}
	st, err := h.svc.Create(r.Context(), principal(r), b.Name, b.Color, b.SortOrder)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, st)
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[body](w, r)
	if !ok {
		return
	}
	active := true
	if b.Active != nil {
		active = *b.Active
	}
	if err := h.svc.Update(r.Context(), principal(r), id, b.Name, b.Color, b.SortOrder, active); err != nil {
		fail(w, r, err)
		return
	}
	h.list(w, r)
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) assignments(w http.ResponseWriter, r *http.Request) {
	var ids []uuid.UUID
	for _, p := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "ids must be UUIDs."))
			return
		}
		ids = append(ids, id)
		if len(ids) > 500 {
			httpx.WriteError(w, r, httpx.NewBadRequest("TOO_MANY", "At most 500 ids."))
			return
		}
	}
	rows, err := h.svc.For(r.Context(), principal(r), ids)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assignments": rows})
}

func (h *Handlers) assign(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		StatusID *uuid.UUID `json:"status_id"`
	}](w, r)
	if !ok {
		return
	}
	if err := h.svc.Assign(r.Context(), principal(r), id, b.StatusID); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
