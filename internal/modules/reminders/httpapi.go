package reminders

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

// Handlers exposes the reminder settings and the log of reminders sent.
type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/reminders/settings", h.get)
	r.Put("/reminders/settings", h.put)
	r.Get("/reminders/log", h.log)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_SETTINGS", err.Error()))
	default:
		httpx.WriteError(w, r, err)
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.Settings(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handlers) put(w http.ResponseWriter, r *http.Request) {
	var in Settings
	dec := json.NewDecoder(io.LimitReader(r.Body, 16384))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if err := h.svc.Save(r.Context(), principal(r), in); err != nil {
		fail(w, r, err)
		return
	}
	h.get(w, r)
}

func (h *Handlers) log(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Log(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reminders": rows})
}
