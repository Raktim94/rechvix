// Package httpapi is the staff module's HTTP transport layer. Mirrors
// internal/modules/pricing/httpapi's shape.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/staff/app"
	"rechvix/internal/modules/staff/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/staff/members", h.listMembers)
	r.Post("/staff/members", h.createMember)
	r.Get("/staff/attendance", h.listAttendance)
	r.Post("/staff/attendance", h.markAttendance)
	r.Get("/staff/tasks", h.listTasks)
	r.Post("/staff/tasks", h.createTask)
	r.Post("/staff/tasks/{id}/status", h.setTaskStatus)
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
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "The requested resource was not found."))
	case errors.Is(err, domain.ErrInvalidAttendanceStatus):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_STATUS", "status must be PRESENT, ABSENT, or LEAVE."))
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

// parseDateRange reads "from"/"to" query params (YYYY-MM-DD); missing
// values default to a month centered on today so a first call from a
// fresh calendar page (no explicit range picked yet) still gets a
// sensible window instead of an error.
func parseDateRange(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -15)
	to := now.AddDate(0, 0, 15)
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	}
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}
	return from, to, nil
}

type createMemberRequest struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	RoleTitle string `json:"role_title"`
}

func (h *Handlers) createMember(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createMemberRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	m, err := h.svc.CreateStaffMember(r.Context(), principal(r), app.CreateStaffMemberParams{Name: req.Name, Phone: req.Phone, RoleTitle: req.RoleTitle})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, m)
}

func (h *Handlers) listMembers(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListStaffMembers(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"staff_members": out})
}

type markAttendanceRequest struct {
	StaffMemberID  uuid.UUID `json:"staff_member_id"`
	AttendanceDate string    `json:"attendance_date"`
	Status         string    `json:"status"`
	Notes          string    `json:"notes"`
}

func (h *Handlers) markAttendance(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[markAttendanceRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	date, err := time.Parse("2006-01-02", req.AttendanceDate)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "attendance_date must be YYYY-MM-DD."))
		return
	}
	rec, err := h.svc.MarkAttendance(r.Context(), principal(r), app.MarkAttendanceParams{
		StaffMemberID: req.StaffMemberID, AttendanceDate: date, Status: domain.AttendanceStatus(req.Status), Notes: req.Notes,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rec)
}

func (h *Handlers) listAttendance(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "from/to must be YYYY-MM-DD."))
		return
	}
	out, err := h.svc.ListAttendance(r.Context(), principal(r), from, to)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attendance": out})
}

type createTaskRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DueDate     string     `json:"due_date"`
	AssignedTo  *uuid.UUID `json:"assigned_to"`
}

func (h *Handlers) createTask(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createTaskRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	dueDate, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "due_date must be YYYY-MM-DD."))
		return
	}
	t, err := h.svc.CreateTask(r.Context(), principal(r), app.CreateTaskParams{
		Title: req.Title, Description: req.Description, DueDate: dueDate, AssignedTo: req.AssignedTo,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

func (h *Handlers) listTasks(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_DATE", "from/to must be YYYY-MM-DD."))
		return
	}
	out, err := h.svc.ListTasks(r.Context(), principal(r), from, to)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

type setTaskStatusRequest struct {
	Status string `json:"status"`
}

func (h *Handlers) setTaskStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[setTaskStatusRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Could not parse the request body."))
		return
	}
	t, err := h.svc.SetTaskStatus(r.Context(), principal(r), id, domain.TaskStatus(req.Status))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}
