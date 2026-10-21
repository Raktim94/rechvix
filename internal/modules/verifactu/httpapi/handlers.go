// Package httpapi exposes the VERI*FACTU settings, record register and certificate upload.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/verifactu/app"
	"rechvix/internal/modules/verifactu/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/legal-entities/{id}/verifactu", h.get)
	r.Put("/legal-entities/{id}/verifactu", h.save)
	r.Put("/legal-entities/{id}/verifactu/certificate", h.certificate)
	r.Get("/legal-entities/{id}/verifactu/records", h.records)
	r.Get("/legal-entities/{id}/verifactu/verify", h.verify)
	r.Get("/legal-entities/{id}/verifactu/export.xml", h.export)
	r.Post("/legal-entities/{id}/verifactu/send", h.send)
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func entityID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return uuid.Nil, false
	}
	return id, true
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, domain.ErrNoCertificate):
		httpx.WriteError(w, r, httpx.NewBadRequest("NO_CERTIFICATE", err.Error()))
	default:
		// Validation failures from the service are written for the user; anything else is a server error.
		httpx.WriteError(w, r, httpx.NewBadRequest("VERIFACTU_ERROR", err.Error()))
	}
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	v, err := h.svc.Settings(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *Handlers) save(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled     bool   `json:"enabled"`
		Environment string `json:"environment"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Send {enabled, environment}."))
		return
	}
	if err := h.svc.SaveSettings(r.Context(), principal(r), id, in.Enabled, in.Environment); err != nil {
		fail(w, r, err)
		return
	}
	h.get(w, r)
}

// certificate accepts the .p12/.pfx file as the request body and the password in the X-Certificate-Password header
// so the secret never appears in a URL or in a logged JSON body.
func (h *Handlers) certificate(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not read the certificate file."))
		return
	}
	if err := h.svc.UploadCertificate(r.Context(), principal(r), id, body, r.Header.Get("X-Certificate-Password")); err != nil {
		fail(w, r, err)
		return
	}
	h.get(w, r)
}

func (h *Handlers) records(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	rows, err := h.svc.Records(r.Context(), principal(r), id, limit, offset)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"records": rows})
}

func (h *Handlers) verify(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	rep, err := h.svc.VerifyChain(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handlers) export(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	xml, err := h.svc.ExportXML(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="verifactu-records.xml"`)
	_, _ = w.Write([]byte(xml))
}

func (h *Handlers) send(w http.ResponseWriter, r *http.Request) {
	id, ok := entityID(w, r)
	if !ok {
		return
	}
	if err := h.svc.SendNow(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	h.records(w, r)
}
