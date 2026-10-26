package datasync

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

// Handlers exposes both halves: /sync/info, /sync/push and /sync/pull answer a linked device (central role), and
// /sync/status, /sync/link and /sync/now are what this install's own settings screen uses (device role).
type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/sync/info", h.info)
	r.Post("/sync/push", h.push)
	r.Get("/sync/pull", h.pull)
	r.Get("/sync/status", h.status)
	r.Put("/sync/link", h.link)
	r.Delete("/sync/link", h.unlink)
	r.Post("/sync/now", h.now)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_SYNC_REQUEST", err.Error()))
	case errors.Is(err, ErrNotLinked):
		httpx.WriteError(w, r, httpx.NewBadRequest("NOT_LINKED", "This install is not linked to a server yet."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusBadGateway, Code: "SYNC_FAILED", Message: err.Error(), Cause: err})
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func (h *Handlers) info(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Info(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handlers) push(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DeviceID string   `json:"device_id"`
		Changes  []Change `json:"changes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	res, err := h.svc.Push(r.Context(), principal(r), in.DeviceID, in.Changes)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handlers) pull(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	res, err := h.svc.Pull(r.Context(), principal(r), r.URL.Query().Get("device_id"), after, limit)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handlers) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Status(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handlers) link(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&in); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	st, err := h.svc.Link(r.Context(), principal(r), in.URL, in.APIKey)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handlers) unlink(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Unlink(r.Context(), principal(r)); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) now(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.SyncNow(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}
