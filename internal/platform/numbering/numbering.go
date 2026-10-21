// Package numbering is the real, configurable document-numbering system
// (brief §51) — concurrency-safe sequential allocation scoped to
// (organisation, branch, document_type, financial_year), with a
// configurable prefix per series. purchases' Stage 4
// purchase_document_counters was an explicit minimal placeholder; this is
// the shared mechanism it was meant to eventually move onto
// (migrations/0013's header comment). Not migrated onto by purchases in
// this stage — tracked as a follow-up, not done here to avoid touching a
// verified Stage 4 module.
package numbering

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/database"
)

// Repository is the persistence contract — one implementation (pg.go)
// backed by document_number_counters.
type Repository interface {
	// Next atomically allocates and returns the next sequence value for
	// (orgID, branchID, documentType, financialYear), creating the counter
	// row (starting at 1) on first use. Implemented as a single INSERT ...
	// ON CONFLICT DO UPDATE ... RETURNING so two concurrent callers can
	// never receive the same value (Scenario I) — see pg.go.
	Next(ctx context.Context, orgID, branchID uuid.UUID, documentType, financialYear, prefix string) (int64, error)
}

// Service formats an allocated sequence number into the final business
// document number, e.g. "INV/2026-27/000133".
type Service struct {
	pool database.Runner
	repo Repository
}

func NewService(pool database.Runner, repo Repository) *Service {
	return &Service{pool: pool, repo: repo}
}

// NextDocumentNumber allocates and formats a business document number.
// Must be called from inside a transaction already scoped to orgID (this
// method does NOT open its own RunScoped — same nested-transaction-safe
// shape as inventory.RecordMovementForOtherModule, docs/architecture.md
// §2 — so a caller like sales.CreateDocument can allocate a number in the
// same transaction as the document row it belongs to).
func (s *Service) NextDocumentNumber(ctx context.Context, orgID, branchID uuid.UUID, documentType, prefix string, asOf time.Time) (string, error) {
	return s.NextDocumentNumberFor(ctx, orgID, branchID, documentType, prefix, asOf, "IN")
}

// NextDocumentNumberFor is NextDocumentNumber for a legal entity in countryCode:
// India keeps the April-March "2026-27" series; every other country uses the
// calendar year ("2026"), which is what Spanish, UK and US businesses expect on
// an invoice. A new series starts at 1 each period.
func (s *Service) NextDocumentNumberFor(ctx context.Context, orgID, branchID uuid.UUID, documentType, prefix string, asOf time.Time, countryCode string) (string, error) {
	fy := SeriesPeriodFor(countryCode, asOf)
	seq, err := s.repo.Next(ctx, orgID, branchID, documentType, fy, prefix)
	if err != nil {
		return "", fmt.Errorf("numbering: allocating next number: %w", err)
	}
	return fmt.Sprintf("%s/%s/%06d", prefix, fy, seq), nil
}

// FinancialYearFor returns the India-default financial year label (April 1
// -> March 31, brief §52) an asOf date falls in, e.g. 2026-06-15 -> "2026-27",
// 2027-02-01 -> "2026-27". This is the ONLY financial-year calendar
// implemented today — brief §52 explicitly warns not every country uses
// April-March, so a non-India deployment needing a different fiscal
// calendar is a documented, deliberate gap (same "explicitly deferred, not
// silently wrong" pattern as FIFO costing/MSIX signing elsewhere in this
// project), not something this function silently gets wrong for you.
func FinancialYearFor(asOf time.Time) string {
	y := asOf.Year()
	if asOf.Month() < time.April {
		return fmt.Sprintf("%d-%02d", y-1, y%100)
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}

// SeriesPeriodFor is the numbering period label for a country: India's
// April-March financial year, or the calendar year elsewhere. Countries whose
// businesses number by a different fiscal calendar (for example the UK tax year)
// still get the calendar year here; a company-configurable calendar is not
// implemented.
func SeriesPeriodFor(countryCode string, asOf time.Time) string {
	switch strings.ToUpper(strings.TrimSpace(countryCode)) {
	case "", "IN":
		return FinancialYearFor(asOf)
	default:
		return fmt.Sprintf("%d", asOf.Year())
	}
}
