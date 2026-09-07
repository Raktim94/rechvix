// Package httpapi is the backup module's HTTP transport layer. Mirrors
// internal/modules/catalogue/httpapi's raw-request-body upload pattern
// (no multipart wrapper) for the same reason the CSV/XLSX importer uses
// it: the whole request body already IS the file, nothing else needs to
// share the request.
package httpapi

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"rechvix/internal/modules/backup/app"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, app.ErrNotConfigured):
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusNotImplemented, Code: "NOT_CONFIGURED",
			Message: "Backup/restore isn't set up on this deployment yet — BACKUP_DATABASE_DSN is not configured."})
	case errors.Is(err, app.ErrInvalidFile):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FILE", err.Error()))
	case errors.Is(err, app.ErrChecksumMismatch):
		httpx.WriteError(w, r, httpx.NewBadRequest("CHECKSUM_MISMATCH", err.Error()))
	case errors.Is(err, app.ErrConfirmationRequired):
		httpx.WriteError(w, r, httpx.NewBadRequest("CONFIRMATION_REQUIRED", "Restore requires the exact confirmation phrase."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/backup/status", h.status)
	r.Post("/backup/export", h.export)
	r.Post("/backup/inspect", h.inspect)
	r.Post("/backup/restore", h.restore)
}

func (h *Handlers) status(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": h.svc.Enabled()})
}

func (h *Handlers) export(w http.ResponseWriter, r *http.Request) {
	data, filename, err := h.svc.Export(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Write(data)
}

const maxBackupFileBytes = 2 << 30 // 2GiB — generous for a small-business dataset; bounds request body memory use

func readUploadedFile(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, maxBackupFileBytes+1))
}

func (h *Handlers) inspect(w http.ResponseWriter, r *http.Request) {
	// Read-only preview: intentionally requires no permission check of
	// its own beyond being authenticated (already true of this whole
	// module's route group) — it reveals only what Header carries
	// (timestamp, Postgres version, byte count), nothing about restoring
	// or the actual business data, so gating it behind backup.manage
	// specifically isn't warranted the way Export/Restore's real actions
	// are.
	data, err := readUploadedFile(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("READ_FAILED", "Could not read the uploaded file."))
		return
	}
	header, err := h.svc.Inspect(data)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, header)
}

func (h *Handlers) restore(w http.ResponseWriter, r *http.Request) {
	confirm := r.URL.Query().Get("confirm")
	data, err := readUploadedFile(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("READ_FAILED", "Could not read the uploaded file."))
		return
	}
	if err := h.svc.Restore(r.Context(), principal(r), data, confirm); err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}
