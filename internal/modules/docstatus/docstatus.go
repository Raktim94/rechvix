// Package docstatus lets a business define its own workflow labels for sales documents and assign them.
package docstatus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid   = errors.New("docstatus: invalid status")
	ErrNotFound  = errors.New("docstatus: not found")
	ErrDuplicate = errors.New("docstatus: a status with that name already exists")
)

var colors = map[string]bool{"neutral": true, "blue": true, "green": true, "amber": true, "red": true, "purple": true}

type Status struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	SortOrder int       `json:"sort_order"`
	Active    bool      `json:"active"`
	InUse     int       `json:"in_use"`
}

type Assignment struct {
	DocumentID uuid.UUID `json:"document_id"`
	StatusID   uuid.UUID `json:"status_id"`
	Name       string    `json:"name"`
	Color      string    `json:"color"`
}

type Service struct {
	pool        *database.Pool
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(pool *database.Pool, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, permissions: p, audit: a, now: time.Now}
}

func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.HasAny(ctx, p, "sales.view")
}
func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "settings.manage", permissions.Scope{})
}

func (s *Service) List(ctx context.Context, principal permissions.Principal) ([]Status, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []Status
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT st.id, st.name, st.color, st.sort_order, st.active, (SELECT count(*) FROM sales_document_statuses a WHERE a.status_id = st.id)
			FROM document_statuses st WHERE st.organisation_id = $1 ORDER BY st.sort_order, st.name`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Status
			if err := rows.Scan(&x.ID, &x.Name, &x.Color, &x.SortOrder, &x.Active, &x.InUse); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

func validate(name, color string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return "", fmt.Errorf("%w: the name must be 1 to 40 characters", ErrInvalid)
	}
	if !colors[color] {
		return "", fmt.Errorf("%w: pick one of neutral, blue, green, amber, red, purple", ErrInvalid)
	}
	return name, nil
}

func isDuplicate(err error) bool { return err != nil && strings.Contains(err.Error(), "23505") }

func (s *Service) Create(ctx context.Context, principal permissions.Principal, name, color string, sort int) (*Status, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	name, err := validate(name, color)
	if err != nil {
		return nil, err
	}
	id, _ := uuid.NewV7()
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO document_statuses (id, organisation_id, name, color, sort_order) VALUES ($1,$2,$3,$4,$5)`, id, principal.OrganisationID, name, color, sort); err != nil {
			if isDuplicate(err) {
				return ErrDuplicate
			}
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "docstatus.created", EntityType: "document_status", EntityID: &id, AfterState: map[string]any{"name": name, "color": color}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	return &Status{ID: id, Name: name, Color: color, SortOrder: sort, Active: true}, nil
}

func (s *Service) Update(ctx context.Context, principal permissions.Principal, id uuid.UUID, name, color string, sort int, active bool) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	name, err := validate(name, color)
	if err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE document_statuses SET name = $2, color = $3, sort_order = $4, active = $5 WHERE id = $1`, id, name, color, sort, active)
		if err != nil {
			if isDuplicate(err) {
				return ErrDuplicate
			}
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "docstatus.updated", EntityType: "document_status", EntityID: &id, AfterState: map[string]any{"name": name, "color": color, "active": active}, At: s.now()})
	})
}

// Delete removes a status that no document uses; one in use should be deactivated instead.
func (s *Service) Delete(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var n int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM sales_document_statuses WHERE status_id = $1`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %d documents use it; deactivate it instead", ErrInvalid, n)
		}
		tag, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM document_statuses WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "docstatus.deleted", EntityType: "document_status", EntityID: &id, At: s.now()})
	})
}

// Assign sets (or with statusID nil, clears) a document's status.
func (s *Service) Assign(ctx context.Context, principal permissions.Principal, documentID uuid.UUID, statusID *uuid.UUID) error {
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var le uuid.UUID
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT legal_entity_id FROM sales_documents WHERE id = $1`, documentID).Scan(&le); err != nil {
			return ErrNotFound
		}
		if err := s.permissions.Require(ctx, principal, "sales.create", permissions.Scope{LegalEntityID: &le}); err != nil {
			return err
		}
		if statusID == nil {
			_, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM sales_document_statuses WHERE sales_document_id = $1`, documentID)
			return err
		}
		var active bool
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT active FROM document_statuses WHERE id = $1`, *statusID).Scan(&active); err != nil {
			return ErrNotFound
		}
		if !active {
			return fmt.Errorf("%w: that status is switched off", ErrInvalid)
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO sales_document_statuses (sales_document_id, organisation_id, status_id, set_by) VALUES ($1,$2,$3,$4)
			ON CONFLICT (sales_document_id) DO UPDATE SET status_id = EXCLUDED.status_id, set_by = EXCLUDED.set_by, set_at = now()`,
			documentID, principal.OrganisationID, *statusID, principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "docstatus.assigned", EntityType: "sales_document", EntityID: &documentID, AfterState: map[string]any{"status_id": statusID.String()}, At: s.now()})
	})
}

// For returns the statuses assigned to the given documents.
func (s *Service) For(ctx context.Context, principal permissions.Principal, ids []uuid.UUID) ([]Assignment, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []Assignment{}, nil
	}
	out := []Assignment{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT a.sales_document_id, st.id, st.name, st.color FROM sales_document_statuses a JOIN document_statuses st ON st.id = a.status_id
			WHERE a.sales_document_id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Assignment
			if err := rows.Scan(&x.DocumentID, &x.StatusID, &x.Name, &x.Color); err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}
