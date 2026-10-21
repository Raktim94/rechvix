package recurring

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

// Handlers exposes recurring schedules.
type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/recurring", h.list)
	r.Post("/sales/documents/{id}/recurring", h.create)
	r.Post("/recurring/{id}/pause", h.pause)
	r.Post("/recurring/{id}/resume", h.resume)
	r.Delete("/recurring/{id}", h.delete)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_SCHEDULE", err.Error()))
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "That schedule does not exist."))
	default:
		httpx.WriteError(w, r, err)
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func id(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	v, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return uuid.Nil, false
	}
	return v, true
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.List(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"schedules": rows})
}

type createBody struct {
	Frequency     Frequency `json:"frequency"`
	IntervalCount int       `json:"interval_count"`
	FirstRunDate  string    `json:"first_run_date"`
	EndDate       string    `json:"end_date"`
	AutoFinalize  bool      `json:"auto_finalize"`
	EmailTo       string    `json:"email_to"`
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	docID, ok := id(w, r)
	if !ok {
		return
	}
	var b createBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	first, err := time.Parse("2006-01-02", b.FirstRunDate)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "first_run_date must be YYYY-MM-DD."))
		return
	}
	p := CreateParams{SourceDocumentID: docID, Frequency: b.Frequency, IntervalCount: b.IntervalCount, FirstRunDate: first, AutoFinalize: b.AutoFinalize, EmailTo: b.EmailTo}
	if b.EndDate != "" {
		e, err := time.Parse("2006-01-02", b.EndDate)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "end_date must be YYYY-MM-DD."))
			return
		}
		p.EndDate = &e
	}
	s, err := h.svc.Create(r.Context(), principal(r), p)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, s)
}

func (h *Handlers) pause(w http.ResponseWriter, r *http.Request)  { h.setActive(w, r, false) }
func (h *Handlers) resume(w http.ResponseWriter, r *http.Request) { h.setActive(w, r, true) }

func (h *Handlers) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	v, ok := id(w, r)
	if !ok {
		return
	}
	if err := h.svc.SetActive(r.Context(), principal(r), v, active); err != nil {
		fail(w, r, err)
		return
	}
	h.list(w, r)
}

func (h *Handlers) delete(w http.ResponseWriter, r *http.Request) {
	v, ok := id(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), principal(r), v); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
