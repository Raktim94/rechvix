// Package reminders sends automatic overdue-invoice reminder emails. Each reminder carries a secure share link, so the
// customer opening it is recorded as a read receipt on the invoice.
package reminders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	notificationsapp "rechvix/internal/modules/notifications/app"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Settings struct {
	Enabled         bool   `json:"enabled"`
	DaysAfterDue    int    `json:"days_after_due"`
	RepeatEveryDays int    `json:"repeat_every_days"`
	MaxReminders    int    `json:"max_reminders"`
	Subject         string `json:"subject"`
	Message         string `json:"message"`
	// Read-only readiness flags for the settings screen.
	EmailConfigured bool `json:"email_configured"`
	BaseURLSet      bool `json:"public_base_url_set"`
}

type LogRow struct {
	DocumentID     uuid.UUID `json:"document_id"`
	DocumentNumber string    `json:"document_number"`
	SentTo         string    `json:"sent_to"`
	ReminderNumber int       `json:"reminder_number"`
	Outstanding    string    `json:"outstanding"`
	QueuedAt       time.Time `json:"queued_at"`
}

type Service struct {
	pool          *database.Pool
	notifications *notificationsapp.Service
	permissions   *permissions.Checker
	audit         audit.Recorder
	logger        *slog.Logger
	now           func() time.Time
	getenv        func(string) string
}

func NewService(pool *database.Pool, n *notificationsapp.Service, p *permissions.Checker, a audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, notifications: n, permissions: p, audit: a, logger: logger, now: time.Now, getenv: os.Getenv}
}

func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "settings.view", permissions.Scope{})
}
func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "settings.manage", permissions.Scope{})
}

func (s *Service) Settings(ctx context.Context, principal permissions.Principal) (*Settings, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := &Settings{DaysAfterDue: 3, RepeatEveryDays: 7, MaxReminders: 3,
		Subject: "Payment reminder: invoice {{number}}",
		Message: "This is a friendly reminder that invoice {{number}} for {{amount}} was due on {{due_date}}. Please arrange payment or contact us if you have already paid."}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		err := s.pool.Q(ctx).QueryRow(ctx, `
			SELECT enabled, days_after_due, repeat_every_days, max_reminders, subject, message FROM reminder_settings WHERE organisation_id = $1`,
			principal.OrganisationID).Scan(&out.Enabled, &out.DaysAfterDue, &out.RepeatEveryDays, &out.MaxReminders, &out.Subject, &out.Message)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			return err
		}
		return nil
	})
	out.EmailConfigured = s.notifications.EmailConfigured()
	out.BaseURLSet = strings.TrimSpace(s.getenv("PUBLIC_BASE_URL")) != ""
	return out, err
}

var ErrInvalid = errors.New("reminders: invalid settings")

func (s *Service) Save(ctx context.Context, principal permissions.Principal, in Settings) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	if in.DaysAfterDue < 0 || in.DaysAfterDue > 365 || in.RepeatEveryDays < 1 || in.RepeatEveryDays > 365 || in.MaxReminders < 1 || in.MaxReminders > 20 {
		return fmt.Errorf("%w: days after due 0-365, repeat every 1-365 days, at most 1-20 reminders", ErrInvalid)
	}
	if strings.TrimSpace(in.Subject) == "" || strings.TrimSpace(in.Message) == "" || len(in.Subject) > 200 || len(in.Message) > 2000 {
		return fmt.Errorf("%w: subject and message are required (message up to 2000 characters)", ErrInvalid)
	}
	if in.Enabled && !s.notifications.EmailConfigured() {
		return fmt.Errorf("%w: email is not configured on this server (set SMTP_HOST)", ErrInvalid)
	}
	if in.Enabled && strings.TrimSpace(s.getenv("PUBLIC_BASE_URL")) == "" {
		return fmt.Errorf("%w: set PUBLIC_BASE_URL on the server so reminder links point to it", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO reminder_settings (organisation_id, enabled, days_after_due, repeat_every_days, max_reminders, subject, message, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (organisation_id) DO UPDATE SET enabled = EXCLUDED.enabled, days_after_due = EXCLUDED.days_after_due,
				repeat_every_days = EXCLUDED.repeat_every_days, max_reminders = EXCLUDED.max_reminders, subject = EXCLUDED.subject,
				message = EXCLUDED.message, created_by = EXCLUDED.created_by, updated_at = now()`,
			principal.OrganisationID, in.Enabled, in.DaysAfterDue, in.RepeatEveryDays, in.MaxReminders, in.Subject, in.Message, principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "reminders.settings_changed", EntityType: "organisation", EntityID: &principal.OrganisationID,
			AfterState: map[string]any{"enabled": in.Enabled, "days_after_due": in.DaysAfterDue, "repeat_every_days": in.RepeatEveryDays, "max_reminders": in.MaxReminders}, At: s.now()})
	})
}

func (s *Service) Log(ctx context.Context, principal permissions.Principal) ([]LogRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []LogRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT l.sales_document_id, d.document_number, l.sent_to, l.reminder_number, l.outstanding::text, l.queued_at
			FROM reminder_log l JOIN sales_documents d ON d.id = l.sales_document_id
			WHERE l.organisation_id = $1 ORDER BY l.queued_at DESC LIMIT 100`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r LogRow
			if err := rows.Scan(&r.DocumentID, &r.DocumentNumber, &r.SentTo, &r.ReminderNumber, &r.Outstanding, &r.QueuedAt); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

type overdue struct {
	DocID       uuid.UUID
	Number      string
	PartyID     uuid.UUID
	Customer    string
	Email       string
	Outstanding decimal.Decimal
	DueDate     time.Time
	Sent        int
}

// RunOnce queues reminders for one organisation and returns how many it queued. It is safe to call repeatedly: an invoice is
// only reminded again after repeat_every_days, and never more than max_reminders times.
func (s *Service) RunOnce(ctx context.Context, orgID uuid.UUID) (int, error) {
	queued := 0
	var cfg Settings
	var owner uuid.UUID
	var items []overdue
	err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		if err := s.pool.Q(ctx).QueryRow(ctx, `
			SELECT enabled, days_after_due, repeat_every_days, max_reminders, subject, message, created_by FROM reminder_settings WHERE organisation_id = $1`,
			orgID).Scan(&cfg.Enabled, &cfg.DaysAfterDue, &cfg.RepeatEveryDays, &cfg.MaxReminders, &cfg.Subject, &cfg.Message, &owner); err != nil {
			return nil
		}
		if !cfg.Enabled {
			return nil
		}
		rows, err := s.pool.Q(ctx).Query(ctx, `
			WITH inv AS (
				SELECT d.id, d.document_number, d.customer_party_id,
				       COALESCE(d.grand_total_base_amount, d.grand_total_amount, 0) AS total,
				       (COALESCE(d.due_date, d.issue_date + d.payment_terms_days))::date AS due
				FROM sales_documents d
				WHERE d.organisation_id = $1 AND d.status = 'FINALIZED' AND d.document_type IN ('TAX_INVOICE', 'POS_INVOICE', 'DEBIT_NOTE')
			)
			SELECT i.id, i.document_number, i.customer_party_id, p.legal_name, COALESCE(p.email, ''),
			       i.total - COALESCE((SELECT SUM(r.amount) FROM receipts r WHERE r.sales_document_id = i.id), 0)
			              - COALESCE((SELECT SUM(COALESCE(c.grand_total_base_amount, c.grand_total_amount, 0)) FROM sales_documents c
			                          WHERE c.reference_document_id = i.id AND c.status = 'FINALIZED' AND c.document_type IN ('CREDIT_NOTE', 'SALES_RETURN')), 0) AS outstanding,
			       i.due,
			       (SELECT count(*) FROM reminder_log l WHERE l.sales_document_id = i.id) AS sent
			FROM inv i JOIN parties p ON p.id = i.customer_party_id
			WHERE i.due + $2::int <= current_date
			  AND NOT EXISTS (SELECT 1 FROM reminder_log l WHERE l.sales_document_id = i.id AND l.queued_at > now() - ($3::int || ' days')::interval)
			ORDER BY i.due`, orgID, cfg.DaysAfterDue, cfg.RepeatEveryDays)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o overdue
			if err := rows.Scan(&o.DocID, &o.Number, &o.PartyID, &o.Customer, &o.Email, &o.Outstanding, &o.DueDate, &o.Sent); err != nil {
				return err
			}
			if o.Outstanding.IsPositive() && o.Sent < cfg.MaxReminders && strings.Contains(o.Email, "@") {
				items = append(items, o)
			}
		}
		return rows.Err()
	})
	if err != nil || len(items) == 0 {
		return 0, err
	}
	base := strings.TrimSpace(s.getenv("PUBLIC_BASE_URL"))
	if base == "" {
		return 0, errors.New("reminders: PUBLIC_BASE_URL is not set")
	}
	principal := permissions.Principal{UserID: owner, OrganisationID: orgID}
	for _, o := range items {
		subject := fill(cfg.Subject, o)
		message := fill(cfg.Message, o)
		if err := s.notifications.EmailDocument(ctx, principal, base, "sales_document", o.DocID, o.Email, subject, message); err != nil {
			s.logger.Warn("reminders: could not queue reminder", "document", o.Number, "error", err)
			continue
		}
		id, _ := uuid.NewV7()
		if err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
			_, err := s.pool.Q(ctx).Exec(ctx, `
				INSERT INTO reminder_log (id, organisation_id, sales_document_id, party_id, sent_to, reminder_number, outstanding)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, orgID, o.DocID, o.PartyID, o.Email, o.Sent+1, o.Outstanding)
			return err
		}); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}

func fill(tpl string, o overdue) string {
	r := strings.NewReplacer("{{number}}", o.Number, "{{amount}}", o.Outstanding.StringFixed(2), "{{due_date}}", o.DueDate.Format("2006-01-02"), "{{customer}}", o.Customer)
	return r.Replace(tpl)
}

// Loop checks every interval for organisations with reminders switched on. It never returns until ctx is cancelled.
func (s *Service) Loop(ctx context.Context, interval time.Duration) {
	tick := func() {
		var orgs []uuid.UUID
		if err := s.pool.Run(ctx, func(ctx context.Context) error {
			rows, err := s.pool.Q(ctx).Query(ctx, `SELECT organisation_id FROM reminder_organisations()`)
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
			s.logger.Error("reminders: listing organisations", "error", err)
			return
		}
		for _, id := range orgs {
			if n, err := s.RunOnce(ctx, id); err != nil {
				s.logger.Error("reminders: run failed", "organisation", id, "error", err)
			} else if n > 0 {
				s.logger.Info("reminders: queued", "organisation", id, "count", n)
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
