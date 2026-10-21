package esign

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Mount registers the authenticated routes.
func (h *Handlers) Mount(r chi.Router) {
	r.Post("/sales/documents/{id}/signature-requests", h.create)
	r.Get("/sales/documents/{id}/signature-requests", h.list)
	r.Delete("/signature-requests/{id}", h.revoke)
}

// MountPublic registers the signer's routes. They carry only the bearer token, like share links.
func (h *Handlers) MountPublic(r chi.Router) {
	r.Get("/sign/{token}", h.lookup)
	r.Get("/sign/{token}/document", h.document)
	r.Post("/sign/{token}", h.submit)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_REQUEST", err.Error()))
	case errors.Is(err, ErrChanged):
		httpx.WriteError(w, r, httpx.NewConflict("DOCUMENT_CHANGED", err.Error()))
	case errors.Is(err, ErrLink):
		httpx.WriteError(w, r, httpx.NewNotFound("LINK_INVALID", err.Error()))
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

func publicBase(r *http.Request) string {
	if v := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); v != "" {
		return v
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	var b struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	base := publicBase(r)
	req, raw, err := h.svc.Create(r.Context(), principal(r), "sales_document", id, b.Name, b.Email, base)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"request": req, "link": strings.TrimRight(base, "/") + "/sign/" + raw})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	rows, err := h.svc.List(r.Context(), principal(r), "sales_document", id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"requests": rows})
}

func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	if err := h.svc.Revoke(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) lookup(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Lookup(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handlers) document(w http.ResponseWriter, r *http.Request) {
	data, ct, name, err := h.svc.Document(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func clientIP(r *http.Request) string {
	for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handlers) submit(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name    string `json:"name"`
		Drawing string `json:"drawing"`
		Agree   bool   `json:"agree"`
		Decline bool   `json:"decline"`
		Reason  string `json:"reason"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 300<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	png, err := DataURLToPNG(b.Drawing)
	if err != nil {
		fail(w, r, err)
		return
	}
	p, err := h.svc.Submit(r.Context(), chi.URLParam(r, "token"), SignInput{Name: b.Name, DrawingPNG: png, Agree: b.Agree, Decline: b.Decline, Reason: b.Reason, IP: clientIP(r), Agent: r.UserAgent()})
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
