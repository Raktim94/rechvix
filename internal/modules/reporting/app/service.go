// Package app is the reporting module's application layer: permission
// checks and orchestration around domain.Repository (docs/architecture.md
// §2). Read-only throughout — no audit logging (no state changes to
// record) beyond what the underlying data's own modules already log.
//
// Every repository call is wrapped in pool.RunScoped, exactly like every
// other module — this is not optional plumbing. Stages 2, 5a, and 6 each
// independently hit and fixed the same bug (a query running outside the
// RunScoped-opened transaction never gets app.current_organisation_id set,
// so every RLS-protected table it touches sees zero rows and fails
// closed with a spurious "not found"); reporting reads from more
// RLS-protected tables than any prior module, so this is the single most
// important thing to get right in this file.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	accountingapp "rechvix/internal/modules/accounting/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool       database.Runner
	repo       domain.Repository
	accounting *accountingapp.Service // reused for per-party ageing (docs/architecture.md §2 — don't reimplement)
	contacts   *contactsapp.Service   // resolves PartyOutstandingRow's name/phone (GetPartyForOtherModule)
	perms      *permissions.Checker
	now        func() time.Time
}

func NewService(pool database.Runner, repo domain.Repository, accounting *accountingapp.Service, contacts *contactsapp.Service, checker *permissions.Checker) *Service {
	return &Service{pool: pool, repo: repo, accounting: accounting, contacts: contacts, perms: checker, now: time.Now}
}

var ErrInvalidGroupDimension = fmt.Errorf("reporting: invalid group dimension")

// view/export are coarse "can run reports at all" gates — see
// sales/app.Service.view's identical rationale/doc comment for why this
// is Checker.HasAny, not Require. The actual per-company restriction is
// enforced by Filter.LegalEntityID below, resolved from
// AllowedLegalEntities("reports.view") the same way
// sales/app.Service.ListDocuments resolves its own filter.
func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.perms.HasAny(ctx, principal, "reports.view")
}

func (s *Service) export(ctx context.Context, principal permissions.Principal) error {
	return s.perms.HasAny(ctx, principal, "reports.export")
}

// scoped fills f.OrganisationID from principal rather than trusting a
// client-supplied value (brief Rule 5), mirroring every other module's
// convention of taking OrganisationID from the authenticated session.
func scoped(principal permissions.Principal, f domain.Filter) domain.Filter {
	f.OrganisationID = principal.OrganisationID
	return f
}

// resolvedFilter intersects f.LegalEntityID (the caller's optional
// single-company request) with what AllowedLegalEntities reports for
// reports.view, filling f.LegalEntityIDs with the concrete restriction
// every report query below actually applies — see
// permissions.ResolveLegalEntityFilter for the exact intersection rule.
// sees=false means the result is "matches nothing" (a company-restricted
// caller either has zero allowed companies, or asked for one outside
// their grants) — the caller should return an empty result immediately
// rather than querying.
//
// MUST be called before the report method's own RunScoped opens:
// AllowedLegalEntities self-scopes its own transaction (same as
// permissions.Checker.Require), and nesting it inside an already-open
// RunScoped would open a second, independent connection/transaction
// instead of participating in the outer one — see
// purchases/app.Service.manageForBranch's identical note for the full
// hazard this avoids.
func (s *Service) resolvedFilter(ctx context.Context, principal permissions.Principal, f domain.Filter) (domain.Filter, bool, error) {
	unrestricted, allowed, err := s.perms.AllowedLegalEntities(ctx, principal, "reports.view")
	if err != nil {
		return f, false, err
	}
	f.LegalEntityIDs = permissions.ResolveLegalEntityFilter(unrestricted, allowed, f.LegalEntityID)
	return f, f.LegalEntityIDs == nil || len(f.LegalEntityIDs) > 0, nil
}

func (s *Service) SalesSummary(ctx context.Context, principal permissions.Principal, f domain.Filter, group domain.GroupDimension) ([]domain.SummaryRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidGroupDimension(group) {
		return nil, ErrInvalidGroupDimension
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.SummaryRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.SalesSummary(ctx, scoped(principal, f), group)
		return err
	})
	return out, err
}

func (s *Service) SalesInvoiceDetail(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.DocumentDetailRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.DocumentDetailRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.SalesInvoiceDetail(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) GrossProfit(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.GrossProfitRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.GrossProfitRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.GrossProfit(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) PurchaseSummary(ctx context.Context, principal permissions.Principal, f domain.Filter, group domain.GroupDimension) ([]domain.SummaryRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidGroupDimension(group) {
		return nil, ErrInvalidGroupDimension
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.SummaryRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.PurchaseSummary(ctx, scoped(principal, f), group)
		return err
	})
	return out, err
}

func (s *Service) PurchaseDetail(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.DocumentDetailRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.DocumentDetailRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.PurchaseDetail(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) StockValuation(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.StockValuationRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.StockValuationRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.StockValuation(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) LowStock(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.LowStockRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.LowStockRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.LowStock(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) StockMovements(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.StockMovementRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.StockMovementRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.StockMovements(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) TrialBalance(ctx context.Context, principal permissions.Principal, asOf time.Time) ([]domain.TrialBalanceRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []domain.TrialBalanceRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.TrialBalance(ctx, principal.OrganisationID, asOf)
		return err
	})
	return out, err
}

// ReceivablesSummary/PayablesSummary batch accounting.Service.GetAgeing
// across every party with AR/AP activity — reusing Stage 6's per-party
// FIFO ageing algorithm rather than duplicating it (docs/architecture.md
// §2). accounting.Service.GetAgeing opens its own RunScoped internally
// (same self-scoping pattern as permissions.Checker, see Stage 2), so the
// per-party loop below is deliberately NOT nested inside a second
// RunScoped here — only the initial party-ID lookup needs one.
func (s *Service) ReceivablesSummary(ctx context.Context, principal permissions.Principal, asOf time.Time) ([]domain.PartyOutstandingRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		ids, err = s.repo.ReceivablesSummaryParties(ctx, principal.OrganisationID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.ageingRows(ctx, principal, ids, asOf)
}

// ReceivablesDetailed is ReceivablesSummary plus each party's phone (for
// the WhatsApp reminder button) and reminder history (for the "first
// reminder sent" column) — a separate, richer JSON endpoint rather than
// a change to ReceivablesSummary itself, so the existing CSV/PDF/Excel
// export path (httpapi/reports.go's writeAgeingTable) is untouched.
func (s *Service) ReceivablesDetailed(ctx context.Context, principal permissions.Principal, asOf time.Time) ([]domain.PartyOutstandingWithContact, error) {
	rows, err := s.ReceivablesSummary(ctx, principal, asOf)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.PartyID
	}
	var reminders map[uuid.UUID]domain.ReminderRecord
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		reminders, err = s.repo.RemindersByParty(ctx, principal.OrganisationID, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.PartyOutstandingWithContact, len(rows))
	for i, row := range rows {
		out[i] = domain.PartyOutstandingWithContact{PartyOutstandingRow: row}
		if rec, ok := reminders[row.PartyID]; ok {
			firstSent, lastSent := rec.FirstSentAt, rec.LastSentAt
			out[i].FirstReminderSentAt = &firstSent
			out[i].LastReminderSentAt = &lastSent
			out[i].ReminderCount = rec.SentCount
		}
	}
	return out, nil
}

// RecordReminderSent logs that a payment reminder was sent to this party
// (brief follow-up: the receivables screen's WhatsApp reminder button
// calls this right after opening the wa.me link) — gated on the same
// reports.view permission as the receivables screen itself, since this
// is a lightweight annotation on that same report rather than a
// standalone write-capable feature warranting its own permission code.
func (s *Service) RecordReminderSent(ctx context.Context, principal permissions.Principal, partyID uuid.UUID) (domain.ReminderRecord, error) {
	if err := s.view(ctx, principal); err != nil {
		return domain.ReminderRecord{}, err
	}
	var rec domain.ReminderRecord
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		rec, err = s.repo.RecordReminderSent(ctx, principal.OrganisationID, partyID, s.now())
		return err
	})
	return rec, err
}

func (s *Service) PayablesSummary(ctx context.Context, principal permissions.Principal, asOf time.Time) ([]domain.PartyOutstandingRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		ids, err = s.repo.PayablesSummaryParties(ctx, principal.OrganisationID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.ageingRows(ctx, principal, ids, asOf)
}

func (s *Service) ageingRows(ctx context.Context, principal permissions.Principal, partyIDs []uuid.UUID, asOf time.Time) ([]domain.PartyOutstandingRow, error) {
	out := make([]domain.PartyOutstandingRow, 0, len(partyIDs))
	for _, id := range partyIDs {
		bucket, err := s.accounting.GetAgeing(ctx, principal, id, asOf)
		if err != nil {
			return nil, fmt.Errorf("reporting: ageing for party %s: %w", id, err)
		}
		if bucket.Total.IsZero() {
			continue // fully settled — not "outstanding"
		}
		out = append(out, domain.PartyOutstandingRow{
			PartyID: id,
			Current: bucket.Current, Days1To30: bucket.Days1To30, Days31To60: bucket.Days31To60,
			Days61To90: bucket.Days61To90, Days90Plus: bucket.Days90Plus, Total: bucket.Total,
		})
	}
	// accountingdomain.AgeingBucket carries no name/phone (it's a pure
	// amounts bucket) — resolved separately here via contacts'
	// cross-module read, batched into a single RunScoped block since
	// GetPartyForOtherModule doesn't open its own (see its doc comment).
	if len(out) > 0 {
		err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
			for i := range out {
				party, err := s.contacts.GetPartyForOtherModule(ctx, principal.OrganisationID, out[i].PartyID)
				if err != nil {
					return fmt.Errorf("reporting: resolving party %s: %w", out[i].PartyID, err)
				}
				out[i].PartyName = party.LegalName
				out[i].Phone = party.Phone
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) AccountLedger(ctx context.Context, principal permissions.Principal, accountID uuid.UUID, f domain.Filter) ([]domain.AccountLedgerRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.AccountLedgerRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.AccountLedger(ctx, principal.OrganisationID, accountID, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) HSNSummary(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.HSNSummaryRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.HSNSummaryRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.HSNSummary(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) TaxRateSummary(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.TaxRateSummaryRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.TaxRateSummaryRow
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.TaxRateSummary(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

// GSTR1 requires reports.export (not just reports.view) — this shapes
// invoice-level GSTIN-bearing data intended for export/filing prep, a
// more sensitive operation than viewing an aggregate on screen.
// resolvedFilter still checks against reports.view (not .export) grants,
// matching how every other report's filter is resolved — export is an
// additional gate on TOP of view, not a separate scope dimension.
func (s *Service) GSTR1(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.GSTR1Line, error) {
	if err := s.export(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.GSTR1Line
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.GSTR1(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

// GSTR3B is GSTR1's identical export-permission rationale — same
// gate, same reasoning, a different shape of tax-filing-prep data.
func (s *Service) GSTR3B(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.GSTR3BLine, error) {
	if err := s.export(ctx, principal); err != nil {
		return nil, err
	}
	f, sees, err := s.resolvedFilter(ctx, principal, f)
	if err != nil || !sees {
		return nil, err
	}
	var out []domain.GSTR3BLine
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.GSTR3B(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

func (s *Service) Dashboard(ctx context.Context, principal permissions.Principal) (domain.DashboardSummary, error) {
	if err := s.view(ctx, principal); err != nil {
		return domain.DashboardSummary{}, err
	}
	var out domain.DashboardSummary
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.Dashboard(ctx, principal.OrganisationID, s.now())
		return err
	})
	return out, err
}
