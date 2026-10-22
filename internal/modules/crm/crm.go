// Package crm is a simple sales pipeline: opportunities move through stages, with activities (calls, emails, meetings,
// notes, tasks) recorded against them.
package crm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid  = errors.New("crm: invalid input")
	ErrNotFound = errors.New("crm: not found")
)

type Stage struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
	Kind      string    `json:"kind"`
}

type Opportunity struct {
	ID            uuid.UUID       `json:"id"`
	Title         string          `json:"title"`
	PartyID       *uuid.UUID      `json:"party_id,omitempty"`
	CustomerName  string          `json:"customer_name,omitempty"`
	CompanyName   string          `json:"company_name,omitempty"`
	ContactName   string          `json:"contact_name,omitempty"`
	ContactEmail  string          `json:"contact_email,omitempty"`
	ContactPhone  string          `json:"contact_phone,omitempty"`
	Value         decimal.Decimal `json:"value"`
	Currency      string          `json:"currency"`
	StageID       uuid.UUID       `json:"stage_id"`
	StageKind     string          `json:"stage_kind"`
	OwnerUserID   *uuid.UUID      `json:"owner_user_id,omitempty"`
	ExpectedClose *string         `json:"expected_close,omitempty"`
	Source        string          `json:"source,omitempty"`
	Notes         string          `json:"notes,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	ClosedAt      *time.Time      `json:"closed_at,omitempty"`
	OpenTasks     int             `json:"open_tasks"`
}

type Activity struct {
	ID            uuid.UUID  `json:"id"`
	OpportunityID uuid.UUID  `json:"opportunity_id"`
	Title         string     `json:"opportunity_title,omitempty"`
	Kind          string     `json:"kind"`
	Subject       string     `json:"subject"`
	Notes         string     `json:"notes,omitempty"`
	DueAt         *time.Time `json:"due_at,omitempty"`
	DoneAt        *time.Time `json:"done_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type Input struct {
	Title         string
	PartyID       *uuid.UUID
	CompanyName   string
	ContactName   string
	ContactEmail  string
	ContactPhone  string
	Value         decimal.Decimal
	Currency      string
	OwnerUserID   *uuid.UUID
	ExpectedClose *time.Time
	Source        string
	Notes         string
}

type Service struct {
	pool        *database.Pool
	contacts    *contactsapp.Service
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(pool *database.Pool, contacts *contactsapp.Service, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, contacts: contacts, permissions: p, audit: a, now: time.Now}
}

func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.HasAny(ctx, p, "contacts.view")
}
func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.HasAny(ctx, p, "contacts.manage")
}

var defaultStages = []Stage{
	{Name: "New", SortOrder: 1, Kind: "OPEN"}, {Name: "Contacted", SortOrder: 2, Kind: "OPEN"},
	{Name: "Proposal", SortOrder: 3, Kind: "OPEN"}, {Name: "Negotiation", SortOrder: 4, Kind: "OPEN"},
	{Name: "Won", SortOrder: 5, Kind: "WON"}, {Name: "Lost", SortOrder: 6, Kind: "LOST"},
}

func (s *Service) ensureStages(ctx context.Context, orgID uuid.UUID) error {
	var n int
	if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM crm_stages WHERE organisation_id = $1`, orgID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, st := range defaultStages {
		id, _ := uuid.NewV7()
		if _, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO crm_stages (id, organisation_id, name, sort_order, kind) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, id, orgID, st.Name, st.SortOrder, st.Kind); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Stages(ctx context.Context, principal permissions.Principal) ([]Stage, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []Stage
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.ensureStages(ctx, principal.OrganisationID); err != nil {
			return err
		}
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, name, sort_order, kind FROM crm_stages WHERE organisation_id = $1 ORDER BY sort_order`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Stage
			if err := rows.Scan(&x.ID, &x.Name, &x.SortOrder, &x.Kind); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

const oppSelect = `
	SELECT o.id, o.title, o.party_id, COALESCE(p.legal_name, ''), COALESCE(o.company_name, ''), COALESCE(o.contact_name, ''), COALESCE(o.contact_email, ''),
	       COALESCE(o.contact_phone, ''), o.value_amount, o.currency_code, o.stage_id, st.kind, o.owner_user_id, to_char(o.expected_close, 'YYYY-MM-DD'),
	       COALESCE(o.source, ''), COALESCE(o.notes, ''), o.created_at, o.closed_at,
	       (SELECT count(*) FROM crm_activities a WHERE a.opportunity_id = o.id AND a.kind = 'TASK' AND a.done_at IS NULL)
	FROM crm_opportunities o JOIN crm_stages st ON st.id = o.stage_id LEFT JOIN parties p ON p.id = o.party_id`

type scanner interface{ Scan(dest ...any) error }

func scanOpp(r scanner) (Opportunity, error) {
	var o Opportunity
	err := r.Scan(&o.ID, &o.Title, &o.PartyID, &o.CustomerName, &o.CompanyName, &o.ContactName, &o.ContactEmail, &o.ContactPhone, &o.Value, &o.Currency,
		&o.StageID, &o.StageKind, &o.OwnerUserID, &o.ExpectedClose, &o.Source, &o.Notes, &o.CreatedAt, &o.ClosedAt, &o.OpenTasks)
	return o, err
}

// List returns every opportunity (the board groups them by stage on the client).
func (s *Service) List(ctx context.Context, principal permissions.Principal) ([]Opportunity, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Opportunity{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, oppSelect+` WHERE o.organisation_id = $1 ORDER BY o.updated_at DESC LIMIT 1000`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOpp(rows)
			if err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

func (in Input) validate() error {
	if t := strings.TrimSpace(in.Title); t == "" || len([]rune(t)) > 160 {
		return fmt.Errorf("%w: a title of 1 to 160 characters is required", ErrInvalid)
	}
	if in.Value.IsNegative() {
		return fmt.Errorf("%w: the value cannot be negative", ErrInvalid)
	}
	if len(in.Currency) != 3 {
		return fmt.Errorf("%w: a 3-letter currency code is required", ErrInvalid)
	}
	if in.ContactEmail != "" && !strings.Contains(in.ContactEmail, "@") {
		return fmt.Errorf("%w: the email address looks wrong", ErrInvalid)
	}
	if in.PartyID == nil && strings.TrimSpace(in.CompanyName) == "" && strings.TrimSpace(in.ContactName) == "" {
		return fmt.Errorf("%w: choose a customer or enter a company or contact name", ErrInvalid)
	}
	return nil
}

func nz(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// validateReferences adapts Zulivio's organisation-scoped assignment checks to
// Rechvix's non-owning PostgreSQL runtime role. Run inside RunScoped: foreign
// keys alone do not stop references to another tenant's otherwise hidden rows.
func (s *Service) validateReferences(ctx context.Context, orgID uuid.UUID, in Input) error {
	for _, ref := range []struct {
		id             *uuid.UUID
		query, message string
	}{
		{in.OwnerUserID, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND organisation_id = $2 AND status = 'ACTIVE')`, "choose an active owner in this organisation"},
		{in.PartyID, `SELECT EXISTS (SELECT 1 FROM parties WHERE id = $1 AND organisation_id = $2)`, "choose a customer in this organisation"},
	} {
		if ref.id == nil {
			continue
		}
		var exists bool
		if err := s.pool.Q(ctx).QueryRow(ctx, ref.query, *ref.id, orgID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: %s", ErrInvalid, ref.message)
		}
	}
	return nil
}

type Owner struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (s *Service) Owners(ctx context.Context, principal permissions.Principal) ([]Owner, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Owner{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, full_name FROM users WHERE organisation_id = $1 AND status = 'ACTIVE' ORDER BY full_name, id`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o Owner
			if err := rows.Scan(&o.ID, &o.Name); err != nil {
				return err
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) Assign(ctx context.Context, principal permissions.Principal, id uuid.UUID, owner *uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.validateReferences(ctx, principal.OrganisationID, Input{OwnerUserID: owner}); err != nil {
			return err
		}
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE crm_opportunities SET owner_user_id = $2, updated_at = now() WHERE id = $1`, id, owner)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "crm.opportunity_assigned", EntityType: "crm_opportunity", EntityID: &id, AfterState: map[string]any{"owner_user_id": owner}, At: s.now()})
	})
}

func (s *Service) Create(ctx context.Context, principal permissions.Principal, in Input) (*Opportunity, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	id, _ := uuid.NewV7()
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.validateReferences(ctx, principal.OrganisationID, in); err != nil {
			return err
		}
		if err := s.ensureStages(ctx, principal.OrganisationID); err != nil {
			return err
		}
		var first uuid.UUID
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT id FROM crm_stages WHERE organisation_id = $1 AND kind = 'OPEN' ORDER BY sort_order LIMIT 1`, principal.OrganisationID).Scan(&first); err != nil {
			return err
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO crm_opportunities (id, organisation_id, title, party_id, company_name, contact_name, contact_email, contact_phone, value_amount, currency_code,
				stage_id, owner_user_id, expected_close, source, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			id, principal.OrganisationID, strings.TrimSpace(in.Title), in.PartyID, nz(in.CompanyName), nz(in.ContactName), nz(in.ContactEmail), nz(in.ContactPhone),
			in.Value, strings.ToUpper(in.Currency), first, in.OwnerUserID, in.ExpectedClose, nz(in.Source), nz(in.Notes), principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "crm.opportunity_created", EntityType: "crm_opportunity", EntityID: &id, AfterState: map[string]any{"title": in.Title}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) Get(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*Opportunity, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var o Opportunity
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		o, err = scanOpp(s.pool.Q(ctx).QueryRow(ctx, oppSelect+` WHERE o.id = $1`, id))
		return err
	})
	if err != nil {
		return nil, ErrNotFound
	}
	return &o, nil
}

func (s *Service) Update(ctx context.Context, principal permissions.Principal, id uuid.UUID, in Input) (*Opportunity, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.validateReferences(ctx, principal.OrganisationID, in); err != nil {
			return err
		}
		tag, err := s.pool.Q(ctx).Exec(ctx, `
			UPDATE crm_opportunities SET title = $2, party_id = $3, company_name = $4, contact_name = $5, contact_email = $6, contact_phone = $7,
				value_amount = $8, currency_code = $9, owner_user_id = $10, expected_close = $11, source = $12, notes = $13, updated_at = now()
			WHERE id = $1`, id, strings.TrimSpace(in.Title), in.PartyID, nz(in.CompanyName), nz(in.ContactName), nz(in.ContactEmail), nz(in.ContactPhone),
			in.Value, strings.ToUpper(in.Currency), in.OwnerUserID, in.ExpectedClose, nz(in.Source), nz(in.Notes))
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, principal, id)
}

// Move puts an opportunity in another stage. Moving into a Won or Lost stage closes it; moving back reopens it.
func (s *Service) Move(ctx context.Context, principal permissions.Principal, id, stageID uuid.UUID) (*Opportunity, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var kind, name string
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT kind, name FROM crm_stages WHERE id = $1`, stageID).Scan(&kind, &name); err != nil {
			return ErrNotFound
		}
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE crm_opportunities SET stage_id = $2, closed_at = CASE WHEN $3 = 'OPEN' THEN NULL ELSE COALESCE(closed_at, now()) END, updated_at = now() WHERE id = $1`, id, stageID, kind)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "crm.opportunity_moved", EntityType: "crm_opportunity", EntityID: &id, AfterState: map[string]any{"stage": name}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) Delete(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM crm_opportunities WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "crm.opportunity_deleted", EntityType: "crm_opportunity", EntityID: &id, At: s.now()})
	})
}

// MakeCustomer creates a customer record from a lead (no customer yet) and links it, so a quotation can be raised for it.
func (s *Service) MakeCustomer(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*Opportunity, error) {
	o, err := s.Get(ctx, principal, id)
	if err != nil {
		return nil, err
	}
	if o.PartyID != nil {
		return o, nil
	}
	name := strings.TrimSpace(o.CompanyName)
	if name == "" {
		name = strings.TrimSpace(o.ContactName)
	}
	party, err := s.contacts.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: name, Phone: o.ContactPhone, Email: o.ContactEmail, CurrencyCode: o.Currency,
		Notes: "Created from CRM opportunity: " + o.Title,
	})
	if err != nil {
		return nil, err
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE crm_opportunities SET party_id = $2, updated_at = now() WHERE id = $1`, id, party.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, principal, id)
}

var kinds = map[string]bool{"CALL": true, "EMAIL": true, "MEETING": true, "NOTE": true, "TASK": true}

func (s *Service) AddActivity(ctx context.Context, principal permissions.Principal, oppID uuid.UUID, kind, subject, notes string, due *time.Time) (*Activity, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	kind = strings.ToUpper(kind)
	if !kinds[kind] {
		return nil, fmt.Errorf("%w: kind must be call, email, meeting, note or task", ErrInvalid)
	}
	if t := strings.TrimSpace(subject); t == "" || len([]rune(t)) > 200 {
		return nil, fmt.Errorf("%w: a subject of 1 to 200 characters is required", ErrInvalid)
	}
	id, _ := uuid.NewV7()
	a := &Activity{ID: id, OpportunityID: oppID, Kind: kind, Subject: strings.TrimSpace(subject), Notes: notes, DueAt: due, CreatedAt: s.now()}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM crm_opportunities WHERE id = $1`, oppID).Scan(&one); err != nil {
			return ErrNotFound
		}
		var done *time.Time
		if kind != "TASK" { // calls, emails, meetings and notes are records of something that already happened
			n := s.now()
			done = &n
			a.DoneAt = done
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO crm_activities (id, organisation_id, opportunity_id, kind, subject, notes, due_at, done_at, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, principal.OrganisationID, oppID, kind, a.Subject, nz(notes), due, done, principal.UserID); err != nil {
			return err
		}
		_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE crm_opportunities SET updated_at = now() WHERE id = $1`, oppID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) CompleteActivity(ctx context.Context, principal permissions.Principal, id uuid.UUID, done bool) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var t *time.Time
		if done {
			n := s.now()
			t = &n
		}
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE crm_activities SET done_at = $2 WHERE id = $1 AND kind = 'TASK'`, id, t)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *Service) Activities(ctx context.Context, principal permissions.Principal, oppID uuid.UUID) ([]Activity, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Activity{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, opportunity_id, kind, subject, COALESCE(notes, ''), due_at, done_at, created_at FROM crm_activities WHERE opportunity_id = $1 ORDER BY created_at DESC`, oppID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Activity
			if err := rows.Scan(&a.ID, &a.OpportunityID, &a.Kind, &a.Subject, &a.Notes, &a.DueAt, &a.DoneAt, &a.CreatedAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

// OpenTasks returns open follow-up tasks across the pipeline, soonest due first.
func (s *Service) OpenTasks(ctx context.Context, principal permissions.Principal) ([]Activity, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Activity{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT a.id, a.opportunity_id, o.title, a.kind, a.subject, COALESCE(a.notes, ''), a.due_at, a.done_at, a.created_at
			FROM crm_activities a JOIN crm_opportunities o ON o.id = a.opportunity_id
			WHERE a.organisation_id = $1 AND a.kind = 'TASK' AND a.done_at IS NULL ORDER BY a.due_at NULLS LAST, a.created_at LIMIT 200`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Activity
			if err := rows.Scan(&a.ID, &a.OpportunityID, &a.Title, &a.Kind, &a.Subject, &a.Notes, &a.DueAt, &a.DoneAt, &a.CreatedAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}
