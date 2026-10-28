package crm

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
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/crm/stages", h.stages)
	r.Get("/crm/owners", h.owners)
	r.Post("/crm/opportunities/{id}/assign", h.assign)
	r.Get("/crm/opportunities", h.list)
	r.Post("/crm/opportunities", h.create)
	r.Get("/crm/opportunities/{id}", h.get)
	r.Put("/crm/opportunities/{id}", h.update)
	r.Delete("/crm/opportunities/{id}", h.delete)
	r.Post("/crm/opportunities/{id}/move", h.move)
	r.Post("/crm/opportunities/{id}/make-customer", h.makeCustomer)
	r.Get("/crm/opportunities/{id}/activities", h.activities)
	r.Post("/crm/opportunities/{id}/activities", h.addActivity)
	r.Post("/crm/activities/{id}/done", h.done)
	r.Post("/crm/activities/{id}/reopen", h.reopen)
	r.Get("/crm/tasks", h.tasks)
	r.Post("/crm/opportunities/import", h.importLeads)
	r.Post("/crm/opportunities/import/inspect", h.inspectLeads)
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

type oppBody struct {
	Title         string     `json:"title"`
	PartyID       *uuid.UUID `json:"party_id"`
	CompanyName   string     `json:"company_name"`
	ContactName   string     `json:"contact_name"`
	ContactEmail  string     `json:"contact_email"`
	ContactPhone  string     `json:"contact_phone"`
	Value         string     `json:"value"`
	Currency      string     `json:"currency"`
	OwnerUserID   *uuid.UUID `json:"owner_user_id"`
	ExpectedClose string     `json:"expected_close"`
	Source        string     `json:"source"`
	Notes         string     `json:"notes"`
}

func (b oppBody) input() (Input, error) {
	v := decimal.Zero
	if b.Value != "" {
		d, err := decimal.NewFromString(b.Value)
		if err != nil {
			return Input{}, ErrInvalid
		}
		v = d
	}
	in := Input{Title: b.Title, PartyID: b.PartyID, CompanyName: b.CompanyName, ContactName: b.ContactName, ContactEmail: b.ContactEmail,
		ContactPhone: b.ContactPhone, Value: v, Currency: b.Currency, OwnerUserID: b.OwnerUserID, Source: b.Source, Notes: b.Notes}
	if b.ExpectedClose != "" {
		t, err := time.Parse("2006-01-02", b.ExpectedClose)
		if err != nil {
			return Input{}, ErrInvalid
		}
		in.ExpectedClose = &t
	}
	return in, nil
}

func (h *Handlers) stages(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Stages(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"stages": rows})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.List(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"opportunities": rows})
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[oppBody](w, r)
	if !ok {
		return
	}
	in, err := b.input()
	if err != nil {
		fail(w, r, err)
		return
	}
	o, err := h.svc.Create(r.Context(), principal(r), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, o)
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	o, err := h.svc.Get(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *Handlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[oppBody](w, r)
	if !ok {
		return
	}
	in, err := b.input()
	if err != nil {
		fail(w, r, err)
		return
	}
	o, err := h.svc.Update(r.Context(), principal(r), id, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
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

func (h *Handlers) move(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		StageID uuid.UUID `json:"stage_id"`
	}](w, r)
	if !ok {
		return
	}
	o, err := h.svc.Move(r.Context(), principal(r), id, b.StageID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *Handlers) makeCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	o, err := h.svc.MakeCustomer(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

func (h *Handlers) activities(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	rows, err := h.svc.Activities(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"activities": rows})
}

func (h *Handlers) addActivity(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		Kind    string `json:"kind"`
		Subject string `json:"subject"`
		Notes   string `json:"notes"`
		DueAt   string `json:"due_at"`
	}](w, r)
	if !ok {
		return
	}
	var due *time.Time
	if b.DueAt != "" {
		t, err := time.Parse(time.RFC3339, b.DueAt)
		if err != nil {
			t2, err2 := time.Parse("2006-01-02", b.DueAt)
			if err2 != nil {
				fail(w, r, ErrInvalid)
				return
			}
			t = t2
		}
		due = &t
	}
	a, err := h.svc.AddActivity(r.Context(), principal(r), id, b.Kind, b.Subject, b.Notes, due)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handlers) done(w http.ResponseWriter, r *http.Request)   { h.setDone(w, r, true) }
func (h *Handlers) reopen(w http.ResponseWriter, r *http.Request) { h.setDone(w, r, false) }

func (h *Handlers) setDone(w http.ResponseWriter, r *http.Request, done bool) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	if err := h.svc.CompleteActivity(r.Context(), principal(r), id, done); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) tasks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.OpenTasks(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": rows})
}

func (h *Handlers) owners(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Owners(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"owners": rows})
}
func (h *Handlers) assign(w http.ResponseWriter, r *http.Request) {
	id, ok := pid(w, r)
	if !ok {
		return
	}
	body, ok := decode[struct {
		OwnerUserID *uuid.UUID `json:"owner_user_id"`
	}](w, r)
	if !ok {
		return
	}
	if err := h.svc.Assign(r.Context(), principal(r), id, body.OwnerUserID); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readLeadRows parses a spreadsheet upload and applies the optional column mapping.
func readLeadRows(w http.ResponseWriter, r *http.Request, apply bool) ([]importer.Row, bool) {
	rows, err := importer.ParseFormat(r.URL.Query().Get("format"), http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FILE", "Could not read the uploaded file: "+err.Error()))
		return nil, false
	}
	if !apply {
		return rows, true
	}
	mapping, err := importer.ParseMapping(r.URL.Query().Get("mapping"))
	if err == nil {
		rows, err = importer.ApplyMapping(rows, LeadFields, mapping)
	}
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_MAPPING", err.Error()))
		return nil, false
	}
	return rows, true
}

func (h *Handlers) inspectLeads(w http.ResponseWriter, r *http.Request) {
	rows, ok := readLeadRows(w, r, false)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, importer.Inspect(rows, LeadFields))
}

func (h *Handlers) importLeads(w http.ResponseWriter, r *http.Request) {
	rows, ok := readLeadRows(w, r, true)
	if !ok {
		return
	}
	rep, err := h.svc.Import(r.Context(), principal(r), rows, r.URL.Query().Get("dry_run") == "true")
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}
