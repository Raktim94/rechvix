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
	accountingdomain "rechvix/internal/modules/accounting/domain"
	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool       database.Runner
	repo       domain.Repository
	accounting *accountingapp.Service // reused for per-party ageing (docs/architecture.md §2 — don't reimplement)
	perms      *permissions.Checker
	now        func() time.Time
}

func NewService(pool database.Runner, repo domain.Repository, accounting *accountingapp.Service, checker *permissions.Checker) *Service {
	return &Service{pool: pool, repo: repo, accounting: accounting, perms: checker, now: time.Now}
}

var ErrInvalidGroupDimension = fmt.Errorf("reporting: invalid group dimension")

func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.perms.Require(ctx, principal, "reports.view", permissions.Scope{})
}

func (s *Service) export(ctx context.Context, principal permissions.Principal) error {
	return s.perms.Require(ctx, principal, "reports.export", permissions.Scope{})
}

// scoped fills f.OrganisationID from principal rather than trusting a
// client-supplied value (brief Rule 5), mirroring every other module's
// convention of taking OrganisationID from the authenticated session.
func scoped(principal permissions.Principal, f domain.Filter) domain.Filter {
	f.OrganisationID = principal.OrganisationID
	return f
}

func (s *Service) SalesSummary(ctx context.Context, principal permissions.Principal, f domain.Filter, group domain.GroupDimension) ([]domain.SummaryRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidGroupDimension(group) {
		return nil, ErrInvalidGroupDimension
	}
	var out []domain.SummaryRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.DocumentDetailRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.GrossProfitRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.SummaryRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.DocumentDetailRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.StockValuationRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.LowStockRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.StockMovementRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
			PartyID: id, PartyName: partyNameLookup(bucket),
			Current: bucket.Current, Days1To30: bucket.Days1To30, Days31To60: bucket.Days31To60,
			Days61To90: bucket.Days61To90, Days90Plus: bucket.Days90Plus, Total: bucket.Total,
		})
	}
	return out, nil
}

// partyNameLookup: accountingdomain.AgeingBucket does not carry the
// party's name (it's a pure amounts bucket) — this report shows PartyID
// only for now rather than adding a second lookup per party purely for a
// display label; a caller that needs the name already has it from
// whatever screen listed the party. Flagged as a small follow-up if a
// name-inline report is wanted later.
func partyNameLookup(_ accountingdomain.AgeingBucket) string { return "" }

func (s *Service) AccountLedger(ctx context.Context, principal permissions.Principal, accountID uuid.UUID, f domain.Filter) ([]domain.AccountLedgerRow, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []domain.AccountLedgerRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.HSNSummaryRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.TaxRateSummaryRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.repo.TaxRateSummary(ctx, scoped(principal, f))
		return err
	})
	return out, err
}

// GSTR1 requires reports.export (not just reports.view) — this shapes
// invoice-level GSTIN-bearing data intended for export/filing prep, a
// more sensitive operation than viewing an aggregate on screen.
func (s *Service) GSTR1(ctx context.Context, principal permissions.Principal, f domain.Filter) ([]domain.GSTR1Line, error) {
	if err := s.export(ctx, principal); err != nil {
		return nil, err
	}
	var out []domain.GSTR1Line
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
	var out []domain.GSTR3BLine
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
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
