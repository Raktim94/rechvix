// Package recurring creates invoices automatically on a schedule by cloning a finalized invoice.
package recurring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	notificationsapp "rechvix/internal/modules/notifications/app"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid  = errors.New("recurring: invalid schedule")
	ErrNotFound = errors.New("recurring: schedule not found")
)

// maxCatchUp bounds how many missed periods one run creates (for example after the worker was down).
const maxCatchUp = 12

type Frequency string

const (
	Weekly    Frequency = "WEEKLY"
	Monthly   Frequency = "MONTHLY"
	Quarterly Frequency = "QUARTERLY"
	Yearly    Frequency = "YEARLY"
)

// Next returns the date after d for the rhythm. Month-based steps clamp to the last day of a short month
// (31 January plus one month is 28 or 29 February, not 3 March) and never drift: callers advance from the scheduled
// date, not from the day the run happened.
func Next(d time.Time, f Frequency, n int) time.Time {
	switch f {
	case Weekly:
		return d.AddDate(0, 0, 7*n)
	case Quarterly:
		return addMonths(d, 3*n)
	case Yearly:
		return addMonths(d, 12*n)
	default:
		return addMonths(d, n)
	}
}

// Nth returns the k-th run date counted from the first run (k = 0 is the anchor itself). Counting from the anchor,
// rather than stepping from the previous date, keeps a schedule that started on the 31st on the 31st in months that have one.
func Nth(anchor time.Time, f Frequency, every, k int) time.Time {
	switch f {
	case Weekly:
		return anchor.AddDate(0, 0, 7*every*k)
	case Quarterly:
		return addMonths(anchor, 3*every*k)
	case Yearly:
		return addMonths(anchor, 12*every*k)
	default:
		return addMonths(anchor, every*k)
	}
}

func addMonths(d time.Time, n int) time.Time {
	y, m, day := d.Date()
	total := int(m) - 1 + n
	y += total / 12
	m = time.Month(total%12 + 1)
	if last := time.Date(y, m+1, 0, 0, 0, 0, 0, d.Location()).Day(); day > last {
		day = last
	}
	return time.Date(y, m, day, 0, 0, 0, 0, d.Location())
}

type Schedule struct {
	ID               uuid.UUID `json:"id"`
	SourceDocumentID uuid.UUID `json:"source_document_id"`
	SourceNumber     string    `json:"source_number"`
	Customer         string    `json:"customer"`
	Frequency        Frequency `json:"frequency"`
	IntervalCount    int       `json:"interval_count"`
	NextRunDate      string    `json:"next_run_date"`
	EndDate          *string   `json:"end_date,omitempty"`
	AutoFinalize     bool      `json:"auto_finalize"`
	EmailTo          string    `json:"email_to,omitempty"`
	Active           bool      `json:"active"`
	RunCount         int       `json:"run_count"`
}

type CreateParams struct {
	SourceDocumentID uuid.UUID
	Frequency        Frequency
	IntervalCount    int
	FirstRunDate     time.Time
	EndDate          *time.Time
	AutoFinalize     bool
	EmailTo          string
}

type Service struct {
	pool          *database.Pool
	sales         *salesapp.Service
	notifications *notificationsapp.Service
	permissions   *permissions.Checker
	audit         audit.Recorder
	logger        *slog.Logger
	now           func() time.Time
	baseURL       func() string
}

func NewService(pool *database.Pool, sales *salesapp.Service, notifications *notificationsapp.Service, p *permissions.Checker, a audit.Recorder, logger *slog.Logger, baseURL func() string) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, sales: sales, notifications: notifications, permissions: p, audit: a, logger: logger, now: time.Now, baseURL: baseURL}
}

func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.HasAny(ctx, p, "sales.view")
}
func (s *Service) manage(ctx context.Context, p permissions.Principal, legalEntityID uuid.UUID) error {
	return s.permissions.Require(ctx, p, "sales.create", permissions.Scope{LegalEntityID: &legalEntityID})
}

func today(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Create schedules a finalized invoice to repeat.
func (s *Service) Create(ctx context.Context, principal permissions.Principal, p CreateParams) (*Schedule, error) {
	switch p.Frequency {
	case Weekly, Monthly, Quarterly, Yearly:
	default:
		return nil, fmt.Errorf("%w: frequency must be weekly, monthly, quarterly or yearly", ErrInvalid)
	}
	if p.IntervalCount < 1 || p.IntervalCount > 24 {
		return nil, fmt.Errorf("%w: repeat every 1 to 24 periods", ErrInvalid)
	}
	first := today(p.FirstRunDate)
	if first.Before(today(s.now())) {
		return nil, fmt.Errorf("%w: the first run date cannot be in the past", ErrInvalid)
	}
	if p.EndDate != nil && today(*p.EndDate).Before(first) {
		return nil, fmt.Errorf("%w: the end date is before the first run", ErrInvalid)
	}
	if p.EmailTo != "" && !strings.Contains(p.EmailTo, "@") {
		return nil, fmt.Errorf("%w: email address looks wrong", ErrInvalid)
	}
	doc, _, err := s.sales.GetDocument(ctx, principal, p.SourceDocumentID)
	if err != nil {
		return nil, err
	}
	if err := s.manage(ctx, principal, doc.LegalEntityID); err != nil {
		return nil, err
	}
	if doc.Status != salesdomain.StatusFinalized || doc.DocumentType != salesdomain.DocTaxInvoice {
		return nil, fmt.Errorf("%w: only a finalized tax invoice can be repeated", ErrInvalid)
	}
	id, _ := uuid.NewV7()
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO recurring_schedules (id, organisation_id, legal_entity_id, source_document_id, frequency, interval_count, anchor_date, next_run_date, end_date, auto_finalize, email_to, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,NULLIF($10,''),$11)`,
			id, principal.OrganisationID, doc.LegalEntityID, doc.ID, string(p.Frequency), p.IntervalCount, first, p.EndDate, p.AutoFinalize, p.EmailTo, principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "recurring.created", EntityType: "sales_document", EntityID: &doc.ID,
			AfterState: map[string]any{"frequency": p.Frequency, "every": p.IntervalCount, "first_run": first.Format("2006-01-02"), "auto_finalize": p.AutoFinalize}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	list, err := s.List(ctx, principal)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

// List returns the organisation's schedules.
func (s *Service) List(ctx context.Context, principal permissions.Principal) ([]Schedule, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []Schedule
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT r.id, r.source_document_id, d.document_number, p.legal_name, r.frequency, r.interval_count, to_char(r.next_run_date, 'YYYY-MM-DD'),
			       to_char(r.end_date, 'YYYY-MM-DD'), r.auto_finalize, COALESCE(r.email_to, ''), r.active, r.run_count
			FROM recurring_schedules r JOIN sales_documents d ON d.id = r.source_document_id JOIN parties p ON p.id = d.customer_party_id
			WHERE r.organisation_id = $1 ORDER BY r.next_run_date, r.created_at`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x Schedule
			var f string
			if err := rows.Scan(&x.ID, &x.SourceDocumentID, &x.SourceNumber, &x.Customer, &f, &x.IntervalCount, &x.NextRunDate, &x.EndDate, &x.AutoFinalize, &x.EmailTo, &x.Active, &x.RunCount); err != nil {
				return err
			}
			x.Frequency = Frequency(f)
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

// SetActive pauses or resumes a schedule.
func (s *Service) SetActive(ctx context.Context, principal permissions.Principal, id uuid.UUID, active bool) error {
	return s.mutate(ctx, principal, id, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE recurring_schedules SET active = $2 WHERE id = $1`, id, active)
		return err
	}, "recurring.paused_or_resumed", map[string]any{"active": active})
}

// Delete removes a schedule (invoices already created stay).
func (s *Service) Delete(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	return s.mutate(ctx, principal, id, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM recurring_schedules WHERE id = $1`, id)
		return err
	}, "recurring.deleted", nil)
}

func (s *Service) mutate(ctx context.Context, principal permissions.Principal, id uuid.UUID, fn func(context.Context) error, action string, after map[string]any) error {
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var le uuid.UUID
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT legal_entity_id FROM recurring_schedules WHERE id = $1`, id).Scan(&le); err != nil {
			return ErrNotFound
		}
		if err := s.manage(ctx, principal, le); err != nil {
			return err
		}
		if err := fn(ctx); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: action, EntityType: "recurring_schedule", EntityID: &id, AfterState: after, At: s.now()})
	})
}

type due struct {
	ID        uuid.UUID
	Source    uuid.UUID
	Freq      Frequency
	Every     int
	Next      time.Time
	End       *time.Time
	Finalize  bool
	EmailTo   string
	CreatedBy uuid.UUID
	Anchor    time.Time
	Done      int
}

// RunOnce creates the invoices that are due for one organisation and returns how many it created.
func (s *Service) RunOnce(ctx context.Context, orgID uuid.UUID) (int, error) {
	var items []due
	if err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT id, source_document_id, frequency, interval_count, next_run_date, end_date, auto_finalize, COALESCE(email_to, ''), created_by, anchor_date, run_count
			FROM recurring_schedules WHERE organisation_id = $1 AND active AND next_run_date <= current_date ORDER BY next_run_date`, orgID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d due
			var f string
			if err := rows.Scan(&d.ID, &d.Source, &f, &d.Every, &d.Next, &d.End, &d.Finalize, &d.EmailTo, &d.CreatedBy, &d.Anchor, &d.Done); err != nil {
				return err
			}
			d.Freq = Frequency(f)
			items = append(items, d)
		}
		return rows.Err()
	}); err != nil {
		return 0, err
	}
	created := 0
	for _, d := range items {
		principal := permissions.Principal{UserID: d.CreatedBy, OrganisationID: orgID}
		for i := 0; i < maxCatchUp && !today(d.Next).After(today(s.now())); i++ {
			if d.End != nil && today(d.Next).After(today(*d.End)) {
				break
			}
			status, docID, runErr := s.createOne(ctx, principal, d)
			if err := s.record(ctx, orgID, d, docID, status, runErr); err != nil {
				return created, err
			}
			if runErr != nil {
				s.logger.Warn("recurring: run failed", "schedule", d.ID, "error", runErr)
				// Do not advance past a failure: the next loop retries the same period.
				break
			}
			created++
			d.Done++
			d.Next = Nth(d.Anchor, d.Freq, d.Every, d.Done)
			if err := s.advance(ctx, orgID, d); err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

func (s *Service) createOne(ctx context.Context, principal permissions.Principal, d due) (string, *uuid.UUID, error) {
	doc, err := s.sales.ConvertDocument(ctx, principal, d.Source, salesdomain.DocTaxInvoice, nil)
	if err != nil {
		return "FAILED", nil, err
	}
	if !d.Finalize {
		return "CREATED", &doc.ID, nil
	}
	fin, err := s.sales.FinalizeDocument(ctx, principal, doc.ID)
	if err != nil {
		return "FAILED", &doc.ID, err
	}
	if d.EmailTo != "" && s.notifications != nil && s.notifications.EmailConfigured() && s.baseURL() != "" {
		if err := s.notifications.EmailDocument(ctx, principal, s.baseURL(), "sales_document", fin.ID, d.EmailTo, "Invoice "+fin.DocumentNumber, ""); err != nil {
			s.logger.Warn("recurring: invoice created but could not be emailed", "document", fin.DocumentNumber, "error", err)
		}
	}
	return "FINALIZED", &fin.ID, nil
}

func (s *Service) record(ctx context.Context, orgID uuid.UUID, d due, doc *uuid.UUID, status string, runErr error) error {
	id, _ := uuid.NewV7()
	var msg *string
	if runErr != nil {
		m := runErr.Error()
		msg = &m
	}
	return s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO recurring_runs (id, organisation_id, schedule_id, run_date, created_document_id, status, error)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (schedule_id, run_date) DO UPDATE SET created_document_id = EXCLUDED.created_document_id, status = EXCLUDED.status, error = EXCLUDED.error`,
			id, orgID, d.ID, today(d.Next), doc, status, msg)
		return err
	})
}

func (s *Service) advance(ctx context.Context, orgID uuid.UUID, d due) error {
	return s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		active := d.End == nil || !today(d.Next).After(today(*d.End))
		_, err := s.pool.Q(ctx).Exec(ctx, `
			UPDATE recurring_schedules SET next_run_date = $2, run_count = $4, last_run_at = now(), active = $3 WHERE id = $1`, d.ID, today(d.Next), active, d.Done)
		return err
	})
}

// Loop runs RunOnce for every organisation with something due, every interval, until ctx is cancelled.
func (s *Service) Loop(ctx context.Context, interval time.Duration) {
	tick := func() {
		var orgs []uuid.UUID
		if err := s.pool.Run(ctx, func(ctx context.Context) error {
			rows, err := s.pool.Q(ctx).Query(ctx, `SELECT organisation_id FROM recurring_due_organisations()`)
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
			s.logger.Error("recurring: listing organisations", "error", err)
			return
		}
		for _, id := range orgs {
			if n, err := s.RunOnce(ctx, id); err != nil {
				s.logger.Error("recurring: run failed", "organisation", id, "error", err)
			} else if n > 0 {
				s.logger.Info("recurring: invoices created", "organisation", id, "count", n)
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
