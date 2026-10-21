// Package app is the taxation module's application layer: it orchestrates
// a TaxEngine calculation and persists the result as an immutable snapshot
// (docs/architecture.md §5). It has no permission checks of its own — like
// internal/modules/inventory's RecordMovementForOtherModule, this is a
// cross-module entry point meant to be called from another module's
// already-permission-checked application-layer method (Stage 5b's sales
// module, once it exists), not directly from an HTTP handler.
package app

import (
	"context"
	"fmt"
	"github.com/shopspring/decimal"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/database"
)

type Service struct {
	pool       database.Runner
	engine     domain.TaxEngine
	documents  domain.TaxDocumentRepository
	lines      domain.TaxLineRepository
	components domain.TaxComponentRepository
}

func NewService(
	pool database.Runner,
	engine domain.TaxEngine,
	documents domain.TaxDocumentRepository,
	lines domain.TaxLineRepository,
	components domain.TaxComponentRepository,
) *Service {
	return &Service{pool: pool, engine: engine, documents: documents, lines: lines, components: components}
}

// SnapshotRequest is CalculateAndSnapshot's input: a calculation input plus
// what business document it belongs to (Stage 5b's sales module supplies
// referenceType="SALES_INVOICE" etc.; this module's own tests use a
// standalone reference since no such document exists yet in Stage 5a).
type SnapshotRequest struct {
	ReferenceType string
	ReferenceID   *uuid.UUID
	Input         domain.TaxCalculationInput
}

// CalculateAndSnapshot runs the configured TaxEngine and persists the
// result as a tax_documents/tax_lines/tax_components snapshot in one
// transaction — this is the ONE place a tax calculation is resolved and
// stored (brief §7, §55: a finalized document's tax must never be
// recalculated later against an updated tax_rate_master). Callers must
// already be inside a RunScoped transaction scoped to in.OrganisationID
// for their own document's state change (e.g. Stage 5b's sales-invoice
// finalize) — this method opens its OWN RunScoped block, so call it
// either before or after that document's own transaction commits, not
// nested inside it, to avoid a needless cross-transaction dependency;
// Stage 5b should re-evaluate this once a real caller exists and prefers
// true single-transaction atomicity with the sales invoice write.
func (s *Service) CalculateAndSnapshot(ctx context.Context, req SnapshotRequest) (*domain.TaxDocument, []*domain.TaxLine, []*domain.TaxComponentRow, error) {
	if len(req.Input.Lines) == 0 {
		return nil, nil, nil, domain.ErrNoLines
	}
	// A caller that forgets to set SupplyType gets a sensible default
	// (B2C — the most common case) rather than a cryptic
	// tax_documents_supply_type_check constraint violation from Postgres;
	// caught by TestTaxation_FinalizedSnapshot_UnaffectedByLaterRateMasterUpdate
	// during development, whose test helper didn't set it.
	if req.Input.SupplyType == "" {
		req.Input.SupplyType = domain.SupplyB2C
	}

	var doc *domain.TaxDocument
	var persistedLines []*domain.TaxLine
	var persistedComponents []*domain.TaxComponentRow

	// The whole thing — calculation AND persistence — runs inside ONE
	// RunScoped block. The engine's rate lookup (tax_rate_master) is an
	// RLS-protected tenant table exactly like everything else since
	// migration 0001: calling s.engine.Calculate OUTSIDE a scoped
	// transaction would run it against an unscoped connection, which
	// fails closed (sees zero rows) and surfaces as a spurious "no tax
	// rate configured" error — this exact bug was caught by
	// TestTaxation_CalculateAndSnapshot_PersistsAndIsRetrievable during
	// development, the same class of ordering mistake Stage 2's
	// permissions.Checker hit and fixed by self-scoping.
	err := s.pool.RunScoped(ctx, req.Input.OrganisationID, func(ctx context.Context) error {
		var err error
		doc, persistedLines, persistedComponents, err = s.calculateAndPersist(ctx, req)
		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return doc, persistedLines, persistedComponents, nil
}

// CalculateAndSnapshotTx is CalculateAndSnapshot's nested-transaction-safe
// twin: it does NOT open its own RunScoped, so it can be called from
// inside a caller's already-open transaction (the same shape as
// inventory.RecordMovementForOtherModule, docs/architecture.md §2) for
// true single-transaction atomicity between a business document's
// finalize state change and its tax snapshot — e.g. sales.FinalizeDocument
// (Stage 5b) calling this in the same transaction it also calls
// inventory.RecordMovementForOtherModule and updates the document's
// status, so tax calculation, stock movement, and finalization commit or
// roll back together. This is exactly the gap CalculateAndSnapshot's own
// doc comment flagged for Stage 5b to resolve. The caller MUST already be
// inside a transaction scoped to req.Input.OrganisationID (via
// database.Runner.RunScoped) — calling this with no active transaction in
// ctx would still execute against the bare pool but with no organisation
// scope set, which fails closed against every RLS-protected table this
// touches (tax_rate_master, tax_documents, ...), the same misuse class
// documented on RecordMovementForOtherModule.
func (s *Service) CalculateAndSnapshotTx(ctx context.Context, req SnapshotRequest) (*domain.TaxDocument, []*domain.TaxLine, []*domain.TaxComponentRow, error) {
	if len(req.Input.Lines) == 0 {
		return nil, nil, nil, domain.ErrNoLines
	}
	if req.Input.SupplyType == "" {
		req.Input.SupplyType = domain.SupplyB2C
	}
	return s.calculateAndPersist(ctx, req)
}

// calculateAndPersist is the shared calculation+persistence body both
// CalculateAndSnapshot (standalone, opens its own transaction) and
// CalculateAndSnapshotTx (nested, assumes an existing scoped transaction)
// call — see CalculateAndSnapshotTx's comment for why these need to be
// two entry points sharing one implementation rather than one method.
func (s *Service) calculateAndPersist(ctx context.Context, req SnapshotRequest) (*domain.TaxDocument, []*domain.TaxLine, []*domain.TaxComponentRow, error) {
	result, err := s.engine.Calculate(ctx, req.Input)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("calculating: %w", err)
	}

	docID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generating tax_document id: %w", err)
	}
	now := time.Now()
	doc := &domain.TaxDocument{
		ID:                 docID,
		OrganisationID:     req.Input.OrganisationID,
		ReferenceType:      req.ReferenceType,
		ReferenceID:        req.ReferenceID,
		DocumentDate:       req.Input.DocumentDate,
		CurrencyCode:       req.Input.CurrencyCode,
		SupplierStateCode:  req.Input.SupplierStateCode,
		PlaceOfSupplyCode:  req.Input.SupplyPlace.StateCode,
		SupplierCountry:    strings.ToUpper(strings.TrimSpace(req.Input.SupplierCountry)),
		CustomerCountry:    strings.ToUpper(strings.TrimSpace(req.Input.CustomerCountry)),
		SupplyRegion:       req.Input.SupplyRegion,
		ExchangeRate:       rateOrOne(req.Input.ExchangeRate),
		SupplyType:         req.Input.SupplyType,
		ReverseCharge:      req.Input.ReverseCharge,
		TotalTaxableAmount: result.TotalTaxableAmount,
		TotalTaxAmount:     result.TotalTaxAmount,
		GrandTotal:         result.GrandTotal,
		CreatedAt:          now,
	}

	if err := s.documents.Create(ctx, doc); err != nil {
		return nil, nil, nil, fmt.Errorf("creating tax_document: %w", err)
	}
	var persistedLines []*domain.TaxLine
	var persistedComponents []*domain.TaxComponentRow
	for _, lr := range result.Lines {
		lineID, err := uuid.NewV7()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("generating tax_line id: %w", err)
		}
		line := &domain.TaxLine{
			ID:              lineID,
			OrganisationID:  req.Input.OrganisationID,
			TaxDocumentID:   docID,
			LineRef:         lr.LineRef,
			HSNSACCode:      lr.HSNSACCode,
			PricingMode:     lr.PricingMode,
			GrossAmount:     lr.GrossAmount,
			TaxableAmount:   lr.TaxableAmount,
			TotalTaxAmount:  lr.TotalTax,
			Classification:  lr.Classification,
			TaxRateMasterID: lr.RateMasterID,
			Regime:          lr.Regime,
			RatePackVersion: lr.RatePackVersion,
			CreatedAt:       now,
		}
		if err := s.lines.Create(ctx, line); err != nil {
			return nil, nil, nil, fmt.Errorf("creating tax_line: %w", err)
		}
		persistedLines = append(persistedLines, line)

		for _, c := range lr.Components {
			compID, err := uuid.NewV7()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("generating tax_component id: %w", err)
			}
			row := &domain.TaxComponentRow{
				ID:             compID,
				OrganisationID: req.Input.OrganisationID,
				TaxLineID:      lineID,
				ComponentType:  c.Type,
				Rate:           c.Rate,
				Amount:         c.Amount,
				CreatedAt:      now,
			}
			if err := s.components.Create(ctx, row); err != nil {
				return nil, nil, nil, fmt.Errorf("creating tax_component: %w", err)
			}
			persistedComponents = append(persistedComponents, row)
		}
	}
	return doc, persistedLines, persistedComponents, nil
}

// GetByReference re-fetches a previously persisted tax_document snapshot —
// used to prove a document's tax never changes after the fact even if
// tax_rate_master is later updated (brief §7), and (Stage 5b) to build a
// printed invoice's tax breakdown. componentsByLine is keyed by
// TaxLine.ID — added in Stage 5b for the print renderer, which needs each
// line's individual CGST/SGST/IGST/CESS components, not just the line's
// already-summed TotalTaxAmount.
func (s *Service) GetByReference(ctx context.Context, orgID uuid.UUID, referenceType string, referenceID uuid.UUID) (*domain.TaxDocument, []*domain.TaxLine, map[uuid.UUID][]*domain.TaxComponentRow, error) {
	var doc *domain.TaxDocument
	var lines []*domain.TaxLine
	componentsByLine := make(map[uuid.UUID][]*domain.TaxComponentRow)
	err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		var err error
		doc, err = s.documents.GetByReference(ctx, orgID, referenceType, referenceID)
		if err != nil {
			return err
		}
		lines, err = s.lines.ListByDocument(ctx, orgID, doc.ID)
		if err != nil {
			return err
		}
		for _, l := range lines {
			comps, err := s.components.ListByLine(ctx, orgID, l.ID)
			if err != nil {
				return err
			}
			componentsByLine[l.ID] = comps
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return doc, lines, componentsByLine, nil
}

// rateOrOne treats an unset exchange rate as 1 (a document in the company currency).
func rateOrOne(r decimal.Decimal) decimal.Decimal {
	if r.IsZero() {
		return decimal.NewFromInt(1)
	}
	return r
}
