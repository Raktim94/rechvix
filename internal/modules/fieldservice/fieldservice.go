// Package fieldservice covers the work done at a customer's site: the equipment they own (assets), incidents raised
// against it with a work log and time spent, and maintenance plans that raise an incident when a service falls due.
package fieldservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid  = errors.New("fieldservice: invalid input")
	ErrNotFound = errors.New("fieldservice: not found")
)

var (
	priorities = map[string]bool{"LOW": true, "NORMAL": true, "HIGH": true, "URGENT": true}
	statuses   = map[string]bool{"OPEN": true, "IN_PROGRESS": true, "WAITING": true, "RESOLVED": true, "CLOSED": true}
)

type Asset struct {
	ID            uuid.UUID        `json:"id"`
	PartyID       uuid.UUID        `json:"party_id"`
	Customer      string           `json:"customer"`
	Name          string           `json:"name"`
	Model         string           `json:"model,omitempty"`
	SerialNumber  string           `json:"serial_number,omitempty"`
	LocationNote  string           `json:"location_note,omitempty"`
	Latitude      *decimal.Decimal `json:"latitude,omitempty"`
	Longitude     *decimal.Decimal `json:"longitude,omitempty"`
	InstalledOn   *string          `json:"installed_on,omitempty"`
	WarrantyUntil *string          `json:"warranty_until,omitempty"`
	InWarranty    bool             `json:"in_warranty"`
	Status        string           `json:"status"`
	Notes         string           `json:"notes,omitempty"`
}

type AssetInput struct {
	PartyID       uuid.UUID
	Name          string
	Model         string
	SerialNumber  string
	LocationNote  string
	Latitude      *decimal.Decimal
	Longitude     *decimal.Decimal
	InstalledOn   *time.Time
	WarrantyUntil *time.Time
	Status        string
	Notes         string
}

type Incident struct {
	ID             uuid.UUID        `json:"id"`
	Number         string           `json:"number"`
	PartyID        uuid.UUID        `json:"party_id"`
	Customer       string           `json:"customer"`
	AssetID        *uuid.UUID       `json:"asset_id,omitempty"`
	AssetName      string           `json:"asset_name,omitempty"`
	Title          string           `json:"title"`
	Description    string           `json:"description,omitempty"`
	Priority       string           `json:"priority"`
	Status         string           `json:"status"`
	AssignedUserID *uuid.UUID       `json:"assigned_user_id,omitempty"`
	ScheduledFor   *time.Time       `json:"scheduled_for,omitempty"`
	Latitude       *decimal.Decimal `json:"latitude,omitempty"`
	Longitude      *decimal.Decimal `json:"longitude,omitempty"`
	Source         string           `json:"source"`
	ResolvedAt     *time.Time       `json:"resolved_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	MinutesSpent   int              `json:"minutes_spent"`
}

type IncidentInput struct {
	PartyID        uuid.UUID
	AssetID        *uuid.UUID
	Title          string
	Description    string
	Priority       string
	AssignedUserID *uuid.UUID
	ScheduledFor   *time.Time
	Latitude       *decimal.Decimal
	Longitude      *decimal.Decimal
}

type Update struct {
	ID        uuid.UUID  `json:"id"`
	Kind      string     `json:"kind"`
	Body      string     `json:"body"`
	Minutes   *int       `json:"minutes_spent,omitempty"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Plan struct {
	ID             uuid.UUID  `json:"id"`
	AssetID        uuid.UUID  `json:"asset_id"`
	AssetName      string     `json:"asset_name"`
	Customer       string     `json:"customer"`
	Title          string     `json:"title"`
	EveryDays      int        `json:"every_days"`
	NextDue        string     `json:"next_due"`
	Priority       string     `json:"priority"`
	AssignedUserID *uuid.UUID `json:"assigned_user_id,omitempty"`
	Active         bool       `json:"active"`
}

type PlanInput struct {
	AssetID        uuid.UUID
	Title          string
	EveryDays      int
	FirstDue       time.Time
	Priority       string
	AssignedUserID *uuid.UUID
}

type Service struct {
	pool        *database.Pool
	permissions *permissions.Checker
	audit       audit.Recorder
	logger      *slog.Logger
	now         func() time.Time
}

func NewService(pool *database.Pool, p *permissions.Checker, a audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, permissions: p, audit: a, logger: logger, now: time.Now}
}

func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.HasAny(ctx, p, "fieldservice.view")
}
func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.HasAny(ctx, p, "fieldservice.manage")
}

func coords(lat, lng *decimal.Decimal) error {
	if (lat == nil) != (lng == nil) {
		return fmt.Errorf("%w: give both latitude and longitude, or neither", ErrInvalid)
	}
	if lat != nil && (lat.LessThan(decimal.NewFromInt(-90)) || lat.GreaterThan(decimal.NewFromInt(90)) || lng.LessThan(decimal.NewFromInt(-180)) || lng.GreaterThan(decimal.NewFromInt(180))) {
		return fmt.Errorf("%w: latitude must be within -90 and 90, longitude within -180 and 180", ErrInvalid)
	}
	return nil
}

func nz(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// ---- Assets

const assetSelect = `
	SELECT a.id, a.party_id, p.legal_name, a.name, COALESCE(a.model, ''), COALESCE(a.serial_number, ''), COALESCE(a.location_note, ''), a.latitude, a.longitude,
	       to_char(a.installed_on, 'YYYY-MM-DD'), to_char(a.warranty_until, 'YYYY-MM-DD'), COALESCE(a.warranty_until >= current_date, false), a.status, COALESCE(a.notes, '')
	FROM customer_assets a JOIN parties p ON p.id = a.party_id`

type scanner interface{ Scan(dest ...any) error }

func scanAsset(r scanner) (Asset, error) {
	var a Asset
	err := r.Scan(&a.ID, &a.PartyID, &a.Customer, &a.Name, &a.Model, &a.SerialNumber, &a.LocationNote, &a.Latitude, &a.Longitude, &a.InstalledOn, &a.WarrantyUntil, &a.InWarranty, &a.Status, &a.Notes)
	return a, err
}

func (s *Service) ListAssets(ctx context.Context, principal permissions.Principal, partyID *uuid.UUID) ([]Asset, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Asset{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, assetSelect+` WHERE a.organisation_id = $1 AND ($2::uuid IS NULL OR a.party_id = $2) ORDER BY p.legal_name, a.name LIMIT 1000`, principal.OrganisationID, partyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAsset(rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (in AssetInput) validate() error {
	if t := strings.TrimSpace(in.Name); t == "" || len([]rune(t)) > 160 {
		return fmt.Errorf("%w: a name of 1 to 160 characters is required", ErrInvalid)
	}
	if in.Status != "" && in.Status != "ACTIVE" && in.Status != "RETIRED" {
		return fmt.Errorf("%w: status must be active or retired", ErrInvalid)
	}
	return coords(in.Latitude, in.Longitude)
}

func (s *Service) CreateAsset(ctx context.Context, principal permissions.Principal, in AssetInput) (*Asset, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	id, _ := uuid.NewV7()
	var out Asset
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM parties WHERE id = $1`, in.PartyID).Scan(&one); err != nil {
			return fmt.Errorf("%w: unknown customer", ErrInvalid)
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO customer_assets (id, organisation_id, party_id, name, model, serial_number, location_note, latitude, longitude, installed_on, warranty_until, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, id, principal.OrganisationID, in.PartyID, strings.TrimSpace(in.Name), nz(in.Model), nz(in.SerialNumber),
			nz(in.LocationNote), in.Latitude, in.Longitude, in.InstalledOn, in.WarrantyUntil, nz(in.Notes), principal.UserID); err != nil {
			return err
		}
		var err error
		out, err = scanAsset(s.pool.Q(ctx).QueryRow(ctx, assetSelect+` WHERE a.id = $1`, id))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Service) UpdateAsset(ctx context.Context, principal permissions.Principal, id uuid.UUID, in AssetInput) (*Asset, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	status := in.Status
	if status == "" {
		status = "ACTIVE"
	}
	var out Asset
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `
			UPDATE customer_assets SET name = $2, model = $3, serial_number = $4, location_note = $5, latitude = $6, longitude = $7, installed_on = $8,
				warranty_until = $9, status = $10, notes = $11, updated_at = now() WHERE id = $1`, id, strings.TrimSpace(in.Name), nz(in.Model), nz(in.SerialNumber),
			nz(in.LocationNote), in.Latitude, in.Longitude, in.InstalledOn, in.WarrantyUntil, status, nz(in.Notes))
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		out, err = scanAsset(s.pool.Q(ctx).QueryRow(ctx, assetSelect+` WHERE a.id = $1`, id))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Incidents

const incSelect = `
	SELECT i.id, i.number, i.party_id, p.legal_name, i.asset_id, COALESCE(a.name, ''), i.title, COALESCE(i.description, ''), i.priority, i.status,
	       i.assigned_user_id, i.scheduled_for, i.latitude, i.longitude, i.source, i.resolved_at, i.created_at,
	       COALESCE((SELECT sum(u.minutes_spent) FROM incident_updates u WHERE u.incident_id = i.id), 0)::int
	FROM incidents i JOIN parties p ON p.id = i.party_id LEFT JOIN customer_assets a ON a.id = i.asset_id`

func scanInc(r scanner) (Incident, error) {
	var i Incident
	err := r.Scan(&i.ID, &i.Number, &i.PartyID, &i.Customer, &i.AssetID, &i.AssetName, &i.Title, &i.Description, &i.Priority, &i.Status,
		&i.AssignedUserID, &i.ScheduledFor, &i.Latitude, &i.Longitude, &i.Source, &i.ResolvedAt, &i.CreatedAt, &i.MinutesSpent)
	return i, err
}

func (in IncidentInput) validate() error {
	if t := strings.TrimSpace(in.Title); t == "" || len([]rune(t)) > 200 {
		return fmt.Errorf("%w: a title of 1 to 200 characters is required", ErrInvalid)
	}
	if in.Priority != "" && !priorities[in.Priority] {
		return fmt.Errorf("%w: priority must be low, normal, high or urgent", ErrInvalid)
	}
	return coords(in.Latitude, in.Longitude)
}

func (s *Service) nextNumber(ctx context.Context, orgID uuid.UUID) (string, error) {
	var n int64
	if err := s.pool.Q(ctx).QueryRow(ctx, `
		INSERT INTO incident_counters (organisation_id, next_number) VALUES ($1, 2)
		ON CONFLICT (organisation_id) DO UPDATE SET next_number = incident_counters.next_number + 1
		RETURNING next_number - 1`, orgID).Scan(&n); err != nil {
		return "", err
	}
	return fmt.Sprintf("INC-%06d", n), nil
}

func (s *Service) createIncident(ctx context.Context, orgID, userID uuid.UUID, in IncidentInput, source string) (uuid.UUID, error) {
	if in.Priority == "" {
		in.Priority = "NORMAL"
	}
	var partyID = in.PartyID
	if in.AssetID != nil { // an incident on an asset is for that asset's owner, and inherits its site if none is given
		var assetParty uuid.UUID
		var lat, lng *decimal.Decimal
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT party_id, latitude, longitude FROM customer_assets WHERE id = $1`, *in.AssetID).Scan(&assetParty, &lat, &lng); err != nil {
			return uuid.Nil, fmt.Errorf("%w: unknown asset", ErrInvalid)
		}
		partyID = assetParty
		if in.Latitude == nil {
			in.Latitude, in.Longitude = lat, lng
		}
	}
	var one int
	if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM parties WHERE id = $1`, partyID).Scan(&one); err != nil {
		return uuid.Nil, fmt.Errorf("%w: unknown customer", ErrInvalid)
	}
	number, err := s.nextNumber(ctx, orgID)
	if err != nil {
		return uuid.Nil, err
	}
	id, _ := uuid.NewV7()
	if _, err := s.pool.Q(ctx).Exec(ctx, `
		INSERT INTO incidents (id, organisation_id, number, party_id, asset_id, title, description, priority, assigned_user_id, scheduled_for, latitude, longitude, source, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, orgID, number, partyID, in.AssetID, strings.TrimSpace(in.Title), nz(in.Description), in.Priority,
		in.AssignedUserID, in.ScheduledFor, in.Latitude, in.Longitude, source, userID); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s *Service) CreateIncident(ctx context.Context, principal permissions.Principal, in IncidentInput) (*Incident, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var out Incident
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		id, err := s.createIncident(ctx, principal.OrganisationID, principal.UserID, in, "MANUAL")
		if err != nil {
			return err
		}
		if err := s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "incident.created", EntityType: "incident", EntityID: &id, AfterState: map[string]any{"title": in.Title}, At: s.now()}); err != nil {
			return err
		}
		out, err = scanInc(s.pool.Q(ctx).QueryRow(ctx, incSelect+` WHERE i.id = $1`, id))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

type IncidentFilter struct {
	Status  string // "" = every open one (not CLOSED)
	PartyID *uuid.UUID
	AssetID *uuid.UUID
}

func (s *Service) ListIncidents(ctx context.Context, principal permissions.Principal, f IncidentFilter) ([]Incident, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Incident{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, incSelect+`
			WHERE i.organisation_id = $1 AND ($2 = '' AND i.status <> 'CLOSED' OR i.status = $2) AND ($3::uuid IS NULL OR i.party_id = $3) AND ($4::uuid IS NULL OR i.asset_id = $4)
			ORDER BY CASE i.priority WHEN 'URGENT' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'NORMAL' THEN 2 ELSE 3 END, i.created_at DESC LIMIT 500`,
			principal.OrganisationID, f.Status, f.PartyID, f.AssetID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInc(rows)
			if err != nil {
				return err
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) GetIncident(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*Incident, []Update, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, nil, err
	}
	var inc Incident
	ups := []Update{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		inc, err = scanInc(s.pool.Q(ctx).QueryRow(ctx, incSelect+` WHERE i.id = $1`, id))
		if err != nil {
			return ErrNotFound
		}
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, kind, body, minutes_spent, created_by, created_at FROM incident_updates WHERE incident_id = $1 ORDER BY created_at`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u Update
			if err := rows.Scan(&u.ID, &u.Kind, &u.Body, &u.Minutes, &u.CreatedBy, &u.CreatedAt); err != nil {
				return err
			}
			ups = append(ups, u)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	return &inc, ups, nil
}

func (s *Service) addUpdate(ctx context.Context, orgID, incidentID uuid.UUID, userID *uuid.UUID, kind, body string, minutes *int) error {
	id, _ := uuid.NewV7()
	_, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO incident_updates (id, organisation_id, incident_id, kind, body, minutes_spent, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, orgID, incidentID, kind, body, minutes, userID)
	return err
}

// Comment adds a note to the work log, optionally with the minutes spent.
func (s *Service) Comment(ctx context.Context, principal permissions.Principal, id uuid.UUID, body string, minutes *int) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" || len(body) > 4000 {
		return fmt.Errorf("%w: write a note of up to 4000 characters", ErrInvalid)
	}
	if minutes != nil && (*minutes < 1 || *minutes > 1440) {
		return fmt.Errorf("%w: minutes must be between 1 and 1440", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM incidents WHERE id = $1`, id).Scan(&one); err != nil {
			return ErrNotFound
		}
		if err := s.addUpdate(ctx, principal.OrganisationID, id, &principal.UserID, "COMMENT", strings.TrimSpace(body), minutes); err != nil {
			return err
		}
		_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE incidents SET updated_at = now() WHERE id = $1`, id)
		return err
	})
}

// SetStatus moves an incident along its life; resolving stamps the time and reopening clears it.
func (s *Service) SetStatus(ctx context.Context, principal permissions.Principal, id uuid.UUID, status string) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	if !statuses[status] {
		return fmt.Errorf("%w: unknown status", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `
			UPDATE incidents SET status = $2, updated_at = now(),
				resolved_at = CASE WHEN $2 IN ('RESOLVED', 'CLOSED') THEN COALESCE(resolved_at, now()) ELSE NULL END WHERE id = $1`, id, status)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.addUpdate(ctx, principal.OrganisationID, id, &principal.UserID, "STATUS", status, nil)
	})
}

// Assign sets who handles the incident and when the visit is planned (either may be cleared with nil).
func (s *Service) Assign(ctx context.Context, principal permissions.Principal, id uuid.UUID, user *uuid.UUID, scheduled *time.Time) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE incidents SET assigned_user_id = $2, scheduled_for = $3, updated_at = now() WHERE id = $1`, id, user, scheduled)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		note := "Unassigned"
		if user != nil {
			note = "Assigned"
		}
		if scheduled != nil {
			note += ", visit planned for " + scheduled.Format("2006-01-02 15:04")
		}
		return s.addUpdate(ctx, principal.OrganisationID, id, &principal.UserID, "ASSIGN", note, nil)
	})
}

// ---- Maintenance plans

func (s *Service) ListPlans(ctx context.Context, principal permissions.Principal) ([]Plan, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Plan{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT m.id, m.asset_id, a.name, p.legal_name, m.title, m.every_days, to_char(m.next_due, 'YYYY-MM-DD'), m.priority, m.assigned_user_id, m.active
			FROM maintenance_plans m JOIN customer_assets a ON a.id = m.asset_id JOIN parties p ON p.id = a.party_id
			WHERE m.organisation_id = $1 ORDER BY m.next_due`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Plan
			if err := rows.Scan(&x.ID, &x.AssetID, &x.AssetName, &x.Customer, &x.Title, &x.EveryDays, &x.NextDue, &x.Priority, &x.AssignedUserID, &x.Active); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) CreatePlan(ctx context.Context, principal permissions.Principal, in PlanInput) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	if t := strings.TrimSpace(in.Title); t == "" || len(t) > 200 {
		return fmt.Errorf("%w: a title is required", ErrInvalid)
	}
	if in.EveryDays < 1 || in.EveryDays > 3650 {
		return fmt.Errorf("%w: repeat every 1 to 3650 days", ErrInvalid)
	}
	if in.Priority == "" {
		in.Priority = "NORMAL"
	}
	if !priorities[in.Priority] {
		return fmt.Errorf("%w: unknown priority", ErrInvalid)
	}
	id, _ := uuid.NewV7()
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM customer_assets WHERE id = $1 AND status = 'ACTIVE'`, in.AssetID).Scan(&one); err != nil {
			return fmt.Errorf("%w: unknown or retired asset", ErrInvalid)
		}
		_, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO maintenance_plans (id, organisation_id, asset_id, title, every_days, next_due, priority, assigned_user_id, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, principal.OrganisationID, in.AssetID, strings.TrimSpace(in.Title), in.EveryDays, in.FirstDue, in.Priority, in.AssignedUserID, principal.UserID)
		return err
	})
}

func (s *Service) SetPlanActive(ctx context.Context, principal permissions.Principal, id uuid.UUID, active bool) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE maintenance_plans SET active = $2 WHERE id = $1`, id, active)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *Service) DeletePlan(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM maintenance_plans WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RunMaintenance raises an incident for every plan that is due in one organisation, then moves each plan to its next
// date (counted forward from the due date so it never drifts). A plan that is several periods behind raises one incident,
// not a backlog. Retired assets are skipped. It returns how many incidents it raised.
func (s *Service) RunMaintenance(ctx context.Context, orgID uuid.UUID) (int, error) {
	raised := 0
	err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT m.id, m.asset_id, m.title, m.every_days, m.next_due, m.priority, m.assigned_user_id, m.created_by, a.status
			FROM maintenance_plans m JOIN customer_assets a ON a.id = m.asset_id
			WHERE m.organisation_id = $1 AND m.active AND m.next_due <= current_date ORDER BY m.next_due FOR UPDATE OF m`, orgID)
		if err != nil {
			return err
		}
		type due struct {
			id, asset, createdBy         uuid.UUID
			title, priority, assetStatus string
			every                        int
			next                         time.Time
			assignee                     *uuid.UUID
		}
		var items []due
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.id, &d.asset, &d.title, &d.every, &d.next, &d.priority, &d.assignee, &d.createdBy, &d.assetStatus); err != nil {
				rows.Close()
				return err
			}
			items = append(items, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		today := s.now().UTC().Truncate(24 * time.Hour)
		for _, d := range items {
			if d.assetStatus == "ACTIVE" {
				asset := d.asset
				if _, err := s.createIncident(ctx, orgID, d.createdBy, IncidentInput{AssetID: &asset, Title: d.title, Priority: d.priority, AssignedUserID: d.assignee,
					Description: "Scheduled maintenance, due " + d.next.Format("2006-01-02")}, "MAINTENANCE"); err != nil {
					return err
				}
				raised++
			}
			next := d.next
			for !next.After(today) {
				next = next.AddDate(0, 0, d.every)
			}
			if _, err := s.pool.Q(ctx).Exec(ctx, `UPDATE maintenance_plans SET next_due = $2 WHERE id = $1`, d.id, next); err != nil {
				return err
			}
		}
		return nil
	})
	return raised, err
}

// Loop raises due maintenance incidents hourly until ctx is cancelled.
func (s *Service) Loop(ctx context.Context, interval time.Duration) {
	tick := func() {
		var orgs []uuid.UUID
		if err := s.pool.Run(ctx, func(ctx context.Context) error {
			rows, err := s.pool.Q(ctx).Query(ctx, `SELECT organisation_id FROM maintenance_due_organisations()`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					return err
				}
				orgs = append(orgs, id)
			}
			return rows.Err()
		}); err != nil {
			s.logger.Error("fieldservice: listing organisations", "error", err)
			return
		}
		for _, id := range orgs {
			if n, err := s.RunMaintenance(ctx, id); err != nil {
				s.logger.Error("fieldservice: maintenance run failed", "organisation", id, "error", err)
			} else if n > 0 {
				s.logger.Info("fieldservice: maintenance incidents raised", "organisation", id, "count", n)
			}
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// ---- Address locations

type AddressLocation struct {
	AddressID uuid.UUID       `json:"address_id"`
	Latitude  decimal.Decimal `json:"latitude"`
	Longitude decimal.Decimal `json:"longitude"`
}

// AddressLocations returns the saved map points of a customer's addresses.
func (s *Service) AddressLocations(ctx context.Context, principal permissions.Principal, partyID uuid.UUID) ([]AddressLocation, error) {
	if err := s.permissions.HasAny(ctx, principal, "contacts.view"); err != nil {
		return nil, err
	}
	out := []AddressLocation{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT l.address_id, l.latitude, l.longitude FROM address_locations l JOIN party_addresses a ON a.id = l.address_id WHERE a.party_id = $1`, partyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x AddressLocation
			if err := rows.Scan(&x.AddressID, &x.Latitude, &x.Longitude); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// SetAddressLocation saves (or with nil coordinates, clears) the map point of an address.
func (s *Service) SetAddressLocation(ctx context.Context, principal permissions.Principal, addressID uuid.UUID, lat, lng *decimal.Decimal) error {
	if err := s.permissions.HasAny(ctx, principal, "contacts.manage"); err != nil {
		return err
	}
	if err := coords(lat, lng); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM party_addresses WHERE id = $1`, addressID).Scan(&one); err != nil {
			return ErrNotFound
		}
		if lat == nil {
			_, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM address_locations WHERE address_id = $1`, addressID)
			return err
		}
		_, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO address_locations (address_id, organisation_id, latitude, longitude) VALUES ($1,$2,$3,$4)
			ON CONFLICT (address_id) DO UPDATE SET latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude, updated_at = now()`, addressID, principal.OrganisationID, lat, lng)
		return err
	})
}
