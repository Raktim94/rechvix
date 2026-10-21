package fieldservice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/service/assets", h.listAssets)
	r.Post("/service/assets", h.createAsset)
	r.Put("/service/assets/{id}", h.updateAsset)
	r.Get("/service/incidents", h.listIncidents)
	r.Post("/service/incidents", h.createIncident)
	r.Get("/service/incidents/{id}", h.getIncident)
	r.Post("/service/incidents/{id}/comments", h.comment)
	r.Post("/service/incidents/{id}/status", h.status)
	r.Post("/service/incidents/{id}/assign", h.assign)
	r.Get("/service/plans", h.listPlans)
	r.Post("/service/plans", h.createPlan)
	r.Post("/service/plans/{id}/pause", h.pausePlan)
	r.Post("/service/plans/{id}/resume", h.resumePlan)
	r.Delete("/service/plans/{id}", h.deletePlan)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_INPUT", err.Error()))
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

func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	dec := json.NewDecoder(io.LimitReader(r.Body, 32768))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return v, false
	}
	return v, true
}

func optUUID(r *http.Request, key string) *uuid.UUID {
	if v, err := uuid.Parse(r.URL.Query().Get(key)); err == nil {
		return &v
	}
	return nil
}

func date(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, ErrInvalid
	}
	return &t, nil
}

func ts(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04", s); err == nil {
		return &t, nil
	}
	return nil, ErrInvalid
}

func dec(s string) (*decimal.Decimal, error) {
	if s == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return nil, ErrInvalid
	}
	return &d, nil
}

type assetBody struct {
	PartyID       uuid.UUID `json:"party_id"`
	Name          string    `json:"name"`
	Model         string    `json:"model"`
	SerialNumber  string    `json:"serial_number"`
	LocationNote  string    `json:"location_note"`
	Latitude      string    `json:"latitude"`
	Longitude     string    `json:"longitude"`
	InstalledOn   string    `json:"installed_on"`
	WarrantyUntil string    `json:"warranty_until"`
	Status        string    `json:"status"`
	Notes         string    `json:"notes"`
}

func (b assetBody) input() (AssetInput, error) {
	lat, e1 := dec(b.Latitude)
	lng, e2 := dec(b.Longitude)
	inst, e3 := date(b.InstalledOn)
	war, e4 := date(b.WarrantyUntil)
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		return AssetInput{}, ErrInvalid
	}
	return AssetInput{PartyID: b.PartyID, Name: b.Name, Model: b.Model, SerialNumber: b.SerialNumber, LocationNote: b.LocationNote, Latitude: lat, Longitude: lng,
		InstalledOn: inst, WarrantyUntil: war, Status: b.Status, Notes: b.Notes}, nil
}

func (h *Handlers) listAssets(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListAssets(r.Context(), principal(r), optUUID(r, "party_id"))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"assets": rows})
}

func (h *Handlers) createAsset(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[assetBody](w, r)
	if !ok {
		return
	}
	in, err := b.input()
	if err != nil {
		fail(w, r, err)
		return
	}
	a, err := h.svc.CreateAsset(r.Context(), principal(r), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handlers) updateAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[assetBody](w, r)
	if !ok {
		return
	}
	in, err := b.input()
	if err != nil {
		fail(w, r, err)
		return
	}
	a, err := h.svc.UpdateAsset(r.Context(), principal(r), id, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *Handlers) listIncidents(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListIncidents(r.Context(), principal(r), IncidentFilter{Status: r.URL.Query().Get("status"), PartyID: optUUID(r, "party_id"), AssetID: optUUID(r, "asset_id")})
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"incidents": rows})
}

type incidentBody struct {
	PartyID        uuid.UUID  `json:"party_id"`
	AssetID        *uuid.UUID `json:"asset_id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Priority       string     `json:"priority"`
	AssignedUserID *uuid.UUID `json:"assigned_user_id"`
	ScheduledFor   string     `json:"scheduled_for"`
	Latitude       string     `json:"latitude"`
	Longitude      string     `json:"longitude"`
}

func (h *Handlers) createIncident(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[incidentBody](w, r)
	if !ok {
		return
	}
	lat, e1 := dec(b.Latitude)
	lng, e2 := dec(b.Longitude)
	sched, e3 := ts(b.ScheduledFor)
	if err := errors.Join(e1, e2, e3); err != nil {
		fail(w, r, ErrInvalid)
		return
	}
	i, err := h.svc.CreateIncident(r.Context(), principal(r), IncidentInput{PartyID: b.PartyID, AssetID: b.AssetID, Title: b.Title, Description: b.Description,
		Priority: b.Priority, AssignedUserID: b.AssignedUserID, ScheduledFor: sched, Latitude: lat, Longitude: lng})
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, i)
}

func (h *Handlers) getIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	i, ups, err := h.svc.GetIncident(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"incident": i, "updates": ups})
}

func (h *Handlers) comment(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		Body    string `json:"body"`
		Minutes *int   `json:"minutes_spent"`
	}](w, r)
	if !ok {
		return
	}
	if err := h.svc.Comment(r.Context(), principal(r), id, b.Body, b.Minutes); err != nil {
		fail(w, r, err)
		return
	}
	h.getIncident(w, r)
}

func (h *Handlers) status(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		Status string `json:"status"`
	}](w, r)
	if !ok {
		return
	}
	if err := h.svc.SetStatus(r.Context(), principal(r), id, b.Status); err != nil {
		fail(w, r, err)
		return
	}
	h.getIncident(w, r)
}

func (h *Handlers) assign(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		UserID       *uuid.UUID `json:"user_id"`
		AssignToMe   bool       `json:"assign_to_me"`
		ScheduledFor string     `json:"scheduled_for"`
	}](w, r)
	if !ok {
		return
	}
	sched, err := ts(b.ScheduledFor)
	if err != nil {
		fail(w, r, err)
		return
	}
	user := b.UserID
	if b.AssignToMe {
		me := principal(r).UserID
		user = &me
	}
	if err := h.svc.Assign(r.Context(), principal(r), id, user, sched); err != nil {
		fail(w, r, err)
		return
	}
	h.getIncident(w, r)
}

func (h *Handlers) listPlans(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListPlans(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plans": rows})
}

func (h *Handlers) createPlan(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[struct {
		AssetID        uuid.UUID  `json:"asset_id"`
		Title          string     `json:"title"`
		EveryDays      int        `json:"every_days"`
		FirstDue       string     `json:"first_due"`
		Priority       string     `json:"priority"`
		AssignedUserID *uuid.UUID `json:"assigned_user_id"`
	}](w, r)
	if !ok {
		return
	}
	first, err := date(b.FirstDue)
	if err != nil || first == nil {
		fail(w, r, ErrInvalid)
		return
	}
	if err := h.svc.CreatePlan(r.Context(), principal(r), PlanInput{AssetID: b.AssetID, Title: b.Title, EveryDays: b.EveryDays, FirstDue: *first, Priority: b.Priority, AssignedUserID: b.AssignedUserID}); err != nil {
		fail(w, r, err)
		return
	}
	h.listPlans(w, r)
}

func (h *Handlers) pausePlan(w http.ResponseWriter, r *http.Request)  { h.setPlan(w, r, false) }
func (h *Handlers) resumePlan(w http.ResponseWriter, r *http.Request) { h.setPlan(w, r, true) }

func (h *Handlers) setPlan(w http.ResponseWriter, r *http.Request, active bool) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.SetPlanActive(r.Context(), principal(r), id, active); err != nil {
		fail(w, r, err)
		return
	}
	h.listPlans(w, r)
}

func (h *Handlers) deletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeletePlan(r.Context(), principal(r), id); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
