// Package app is the purchases module's application/use-case layer.
// Mirrors internal/modules/catalogue/app's shape. FinalizeDocument is
// where a GOODS_RECEIPT/PURCHASE_RETURN's stock effect actually posts —
// it calls inventory's app.Service.RecordMovementForOtherModule from
// inside this module's own RunScoped transaction, per
// docs/architecture.md §2 ("cross-module calls go through the other
// module's application-layer interface").
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	inventoryapp "rechvix/internal/modules/inventory/app"
	inventorydomain "rechvix/internal/modules/inventory/domain"
	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/modules/purchases/domain"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool      database.Runner
	documents domain.DocumentRepository
	lines     domain.DocumentLineRepository
	inventory *inventoryapp.Service
	// catalogue/taxation/contacts/organisation are required, same as
	// sales/app.Service's identical set — AddLine unconditionally
	// snapshots a line's HSN/SAC from catalogue, and FinalizeDocument
	// unconditionally attempts an inward tax calculation (skipping only
	// when the specific supplier has no GST registration on file, not
	// when the dependency itself is absent).
	catalogue    *catalogueapp.Service
	taxation     *taxationapp.Service
	contacts     *contactsapp.Service
	organisation *orgapp.Service
	// accounting is optional — see sales/app.Service's identical field
	// comment for why (pre-Stage-6 callers/tests keep working with nil).
	accounting  *accountingapp.Service
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(
	pool database.Runner,
	documents domain.DocumentRepository,
	lines domain.DocumentLineRepository,
	inventory *inventoryapp.Service,
	catalogueSvc *catalogueapp.Service,
	taxationSvc *taxationapp.Service,
	contactsSvc *contactsapp.Service,
	organisationSvc *orgapp.Service,
	accountingSvc *accountingapp.Service,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{pool: pool, documents: documents, lines: lines, inventory: inventory,
		catalogue: catalogueSvc, taxation: taxationSvc, contacts: contactsSvc, organisation: organisationSvc,
		accounting: accountingSvc, permissions: checker, audit: recorder, now: time.Now}
}

// Permission codes are the ones migrations/0002_rbac_catalog.up.sql
// already pre-seeded for this module (brief §26's example list uses
// singular "purchase", not "purchases") — see
// migrations/0014_stage4_permissions.up.sql for why this module doesn't
// define its own parallel set.
func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "purchase.view", permissions.Scope{})
}
func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "purchase.create", permissions.Scope{})
}
func (s *Service) finalizePerm(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "purchase.finalize", permissions.Scope{})
}

type CreateDocumentParams struct {
	BranchID              uuid.UUID
	WarehouseID           uuid.UUID
	SupplierPartyID       uuid.UUID
	DocumentType          domain.DocumentType
	ReferenceDocumentID   *uuid.UUID
	SupplierInvoiceNumber string
	SupplierInvoiceDate   *time.Time
	DocumentDate          time.Time
	CurrencyCode          string
	Notes                 string
}

func (s *Service) CreateDocument(ctx context.Context, principal permissions.Principal, p CreateDocumentParams) (*domain.Document, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidDocumentType(p.DocumentType) {
		return nil, domain.ErrInvalidDocumentType
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("purchases: generating purchase_document id: %w", err)
	}
	now := s.now()
	docDate := p.DocumentDate
	if docDate.IsZero() {
		docDate = now
	}
	d := &domain.Document{
		ID: id, OrganisationID: principal.OrganisationID, BranchID: p.BranchID, WarehouseID: p.WarehouseID,
		SupplierPartyID: p.SupplierPartyID, DocumentType: p.DocumentType, ReferenceDocumentID: p.ReferenceDocumentID,
		Status: domain.StatusDraft, SupplierInvoiceNumber: p.SupplierInvoiceNumber, SupplierInvoiceDate: p.SupplierInvoiceDate,
		DocumentDate: docDate, CurrencyCode: p.CurrencyCode, Notes: p.Notes, CreatedBy: principal.UserID, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		seq, err := s.documents.NextNumber(ctx, principal.OrganisationID, p.DocumentType)
		if err != nil {
			return err
		}
		d.DocumentNumber = fmt.Sprintf("%s-%06d", documentPrefix(p.DocumentType), seq)
		if err := s.documents.Create(ctx, d); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "purchase_document.created", EntityType: "purchase_document", EntityID: &id,
			AfterState: map[string]any{"document_type": string(p.DocumentType), "document_number": d.DocumentNumber}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

func documentPrefix(t domain.DocumentType) string {
	switch t {
	case domain.DocPurchaseOrder:
		return "PO"
	case domain.DocGoodsReceipt:
		return "GRN"
	case domain.DocPurchaseInvoice:
		return "PINV"
	case domain.DocPurchaseReturn:
		return "PRET"
	case domain.DocDebitNote:
		return "DBN"
	default:
		return "PDOC"
	}
}

func (s *Service) GetDocument(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*domain.Document, []*domain.DocumentLine, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, nil, err
	}
	var doc *domain.Document
	var lines []*domain.DocumentLine
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		doc, err = s.documents.GetByID(ctx, principal.OrganisationID, id)
		if err != nil {
			return err
		}
		lines, err = s.lines.ListByDocument(ctx, id)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return doc, lines, nil
}

func (s *Service) ListDocuments(ctx context.Context, principal permissions.Principal, documentType *domain.DocumentType) ([]*domain.Document, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.Document
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.documents.ListByOrganisation(ctx, principal.OrganisationID, documentType)
		return err
	})
	return result, err
}

type AddLineParams struct {
	DocumentID       uuid.UUID
	ProductVariantID uuid.UUID
	UnitID           uuid.UUID
	Quantity         decimal.Decimal
	UnitPrice        decimal.Decimal
	BatchCode        string
}

// AddLine appends a line to a DRAFT document. Business-document
// immutability (brief §31): a FINALIZED or CANCELLED document rejects
// this with ErrDocumentNotDraft — correction after finalize is a new
// document (return/debit note), never an edit to this one.
func (s *Service) AddLine(ctx context.Context, principal permissions.Principal, p AddLineParams) (*domain.DocumentLine, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	_, product, err := s.catalogue.GetVariantWithProduct(ctx, principal, p.ProductVariantID)
	if err != nil {
		return nil, fmt.Errorf("purchases: resolving product for line: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("purchases: generating purchase_document_line id: %w", err)
	}
	now := s.now()
	var line *domain.DocumentLine
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		doc, err := s.documents.GetByID(ctx, principal.OrganisationID, p.DocumentID)
		if err != nil {
			return err
		}
		if doc.Status != domain.StatusDraft {
			return domain.ErrDocumentNotDraft
		}
		existing, err := s.lines.ListByDocument(ctx, p.DocumentID)
		if err != nil {
			return err
		}
		unitPrice, err := money.New(p.UnitPrice, doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("purchases: %w", err)
		}
		lineTotal, err := money.New(p.Quantity.Mul(p.UnitPrice), doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("purchases: %w", err)
		}
		line = &domain.DocumentLine{
			ID: id, OrganisationID: principal.OrganisationID, PurchaseDocumentID: p.DocumentID,
			LineNumber: len(existing) + 1, ProductVariantID: p.ProductVariantID, UnitID: p.UnitID,
			Quantity: p.Quantity, UnitPrice: unitPrice, LineTotal: lineTotal, BatchCode: p.BatchCode,
			HSNSACCode: product.HSNSACCode, CreatedAt: now,
		}
		if err := s.lines.Create(ctx, line); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "purchase_document_line.added", EntityType: "purchase_document_line", EntityID: &id,
			AfterState: map[string]any{"document_id": p.DocumentID, "quantity": p.Quantity.String(), "unit_price": p.UnitPrice.String()}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return line, nil
}

// FinalizeDocument transitions a DRAFT document to FINALIZED. For
// GOODS_RECEIPT and PURCHASE_RETURN (domain.StockAffecting), this also
// posts one stock_movement per line via
// inventory.RecordMovementForOtherModule, inside this same transaction —
// the document's finalized state and its stock effect commit or roll
// back together, never independently.
func (s *Service) FinalizeDocument(ctx context.Context, principal permissions.Principal, documentID uuid.UUID) (*domain.Document, error) {
	if err := s.finalizePerm(ctx, principal); err != nil {
		return nil, err
	}
	now := s.now()
	var doc *domain.Document
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		doc, err = s.documents.GetByID(ctx, principal.OrganisationID, documentID)
		if err != nil {
			return err
		}
		if doc.Status != domain.StatusDraft {
			return domain.ErrDocumentNotDraft
		}
		lines, err := s.lines.ListByDocument(ctx, documentID)
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return domain.ErrEmptyDocument
		}

		// Inward tax calculation (GSTR-3B follow-up to GSTR-1, migrations/
		// 0038): only for document types that book a payable
		// (AccountingAffecting), and only when the supplier has a GST
		// registration on file — a genuinely unregistered small supplier
		// is common and legitimate, and there is no state code to
		// calculate against in that case (unlike sales, where the
		// "known" side is always our own legal entity's state, here the
		// unknown side is the supplier's, not ours). Skipping is the
		// honest behavior, not a guess at zero.
		var taxDoc *taxdomain.TaxDocument
		if domain.AccountingAffecting(doc.DocumentType) {
			branch, err := s.organisation.GetBranchForOtherModule(ctx, principal.OrganisationID, doc.BranchID)
			if err != nil {
				return fmt.Errorf("purchases: resolving branch for tax calculation: %w", err)
			}
			legalEntity, err := s.organisation.GetLegalEntityForOtherModule(ctx, principal.OrganisationID, branch.LegalEntityID)
			if err != nil {
				return fmt.Errorf("purchases: resolving legal entity for tax calculation: %w", err)
			}
			regs, err := s.contacts.ListTaxRegistrationsForOtherModule(ctx, principal.OrganisationID, doc.SupplierPartyID)
			if err != nil {
				return fmt.Errorf("purchases: resolving supplier tax registration: %w", err)
			}
			supplierStateCode := ""
			for _, r := range regs {
				if r.IsPrimary {
					supplierStateCode = r.StateCode
					break
				}
			}
			if supplierStateCode == "" && len(regs) > 0 {
				supplierStateCode = regs[0].StateCode
			}
			if supplierStateCode != "" {
				taxLines := make([]taxdomain.TaxableLine, 0, len(lines))
				for _, l := range lines {
					taxLines = append(taxLines, taxdomain.TaxableLine{
						LineRef: fmt.Sprintf("%d", l.LineNumber), HSNSACCode: l.HSNSACCode,
						Amount: l.LineTotal, PricingMode: taxdomain.PricingExclusive,
					})
				}
				taxDoc, _, _, err = s.taxation.CalculateAndSnapshotTx(ctx, taxationapp.SnapshotRequest{
					ReferenceType: "purchase_document", ReferenceID: &doc.ID,
					Input: taxdomain.TaxCalculationInput{
						OrganisationID: principal.OrganisationID, Lines: taxLines,
						SupplierStateCode: supplierStateCode, SupplyPlace: taxdomain.PlaceOfSupply{StateCode: legalEntity.GSTStateCode},
						DocumentDate: doc.DocumentDate, SupplyType: taxdomain.SupplyB2B, CurrencyCode: doc.CurrencyCode,
					},
				})
				if err != nil {
					return fmt.Errorf("purchases: calculating input tax: %w", err)
				}
			}
		}

		if domain.StockAffecting(doc.DocumentType, doc.ReferenceDocumentID) {
			movementType := inventorydomain.MovementPurchaseReceipt
			if doc.DocumentType == domain.DocPurchaseReturn {
				movementType = inventorydomain.MovementPurchaseReturn
			}
			for _, line := range lines {
				params := inventoryapp.RecordMovementParams{
					WarehouseID: doc.WarehouseID, ProductVariantID: line.ProductVariantID, MovementType: movementType,
					UnitID: line.UnitID, Quantity: line.Quantity,
					ReferenceType: "purchase_document", ReferenceID: &doc.ID,
					Notes: fmt.Sprintf("%s %s line %d", doc.DocumentType, doc.DocumentNumber, line.LineNumber),
				}
				if line.BatchCode != "" {
					batchCode := line.BatchCode
					params.BatchCode = &batchCode
				}
				if inventorydomain.IsReceipt(movementType) {
					cost := line.UnitPrice.Decimal()
					params.UnitCost = &cost
				}
				if _, err := s.inventory.RecordMovementForOtherModule(ctx, principal.OrganisationID, principal.UserID, params); err != nil {
					return fmt.Errorf("posting stock movement for line %d: %w", line.LineNumber, err)
				}
			}
		}

		if s.accounting != nil && domain.AccountingAffecting(doc.DocumentType) {
			supplierID := doc.SupplierPartyID
			var docLines []accountingapp.JournalLineRequest
			var grandTotal decimal.Decimal
			if taxDoc != nil {
				// Supplier has a GST registration on file — split the
				// taxable value and input tax onto their own accounts
				// (CodeGSTInputTaxCredit, "1400") rather than lumping the
				// whole grand total onto Purchases, mirroring
				// sales.FinalizeDocument's identical Sales/
				// GSTOutputTaxPayable split for the outward side.
				grandTotal = taxDoc.GrandTotal.Decimal()
				docLines = []accountingapp.JournalLineRequest{
					{AccountCode: accountingdomain.CodePurchases, Debit: taxDoc.TotalTaxableAmount.Decimal(), Description: "Purchase " + doc.DocumentNumber},
				}
				if taxDoc.TotalTaxAmount.Decimal().IsPositive() {
					docLines = append(docLines, accountingapp.JournalLineRequest{AccountCode: accountingdomain.CodeGSTInputTaxCredit, Debit: taxDoc.TotalTaxAmount.Decimal(), Description: "Purchase " + doc.DocumentNumber})
				}
				docLines = append(docLines, accountingapp.JournalLineRequest{AccountCode: accountingdomain.CodeAccountsPayable, PartyID: &supplierID, Credit: grandTotal, Description: "Purchase " + doc.DocumentNumber})
			} else {
				// No GST registration on file for this supplier — same
				// lump-sum-onto-Purchases behavior this method has always
				// had, since there is no tax breakdown to split.
				for _, l := range lines {
					grandTotal = grandTotal.Add(l.LineTotal.Decimal())
				}
				docLines = []accountingapp.JournalLineRequest{
					{AccountCode: accountingdomain.CodePurchases, Debit: grandTotal, Description: "Purchase " + doc.DocumentNumber},
					{AccountCode: accountingdomain.CodeAccountsPayable, PartyID: &supplierID, Credit: grandTotal, Description: "Purchase " + doc.DocumentNumber},
				}
			}
			if grandTotal.IsPositive() {
				if domain.ReducesPayable(doc.DocumentType) {
					for i := range docLines {
						docLines[i].Debit, docLines[i].Credit = docLines[i].Credit, docLines[i].Debit
					}
				}
				if _, err := s.accounting.PostTx(ctx, principal, accountingapp.JournalRequest{
					OrganisationID: principal.OrganisationID, SourceType: "purchase_document", SourceID: &doc.ID,
					JournalDate: doc.DocumentDate, Description: "Purchase " + doc.DocumentNumber, CreatedBy: principal.UserID, Lines: docLines,
				}); err != nil {
					return fmt.Errorf("posting purchase journal: %w", err)
				}
			}
		}

		if taxDoc != nil {
			if err := s.documents.UpdateTaxDocument(ctx, documentID, taxDoc.ID); err != nil {
				return err
			}
			doc.TaxDocumentID = &taxDoc.ID
		}

		if err := s.documents.UpdateStatus(ctx, documentID, domain.StatusFinalized, &now); err != nil {
			return err
		}
		doc.Status = domain.StatusFinalized
		doc.FinalizedAt = &now

		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "purchase_document.finalized", EntityType: "purchase_document", EntityID: &documentID,
			AfterState: map[string]any{"document_type": string(doc.DocumentType), "document_number": doc.DocumentNumber, "line_count": len(lines)}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// CancelDocument voids a mis-entered FINALIZED purchase document: mirrors
// sales.Service.CancelDocument (internal/modules/sales/app/service.go)
// exactly — reverses its stock effect (StockAffecting types only) and
// its accounting effect (AccountingAffecting types only) via a
// swapped-Debit/Credit reversal journal linked back through
// ReversedJournalID, rather than reconstructing one from the document's
// own totals. Uses purchases.finalize — the same permission that let
// this document be finalized is what's needed to undo that.
func (s *Service) CancelDocument(ctx context.Context, principal permissions.Principal, documentID uuid.UUID) (*domain.Document, error) {
	if err := s.finalizePerm(ctx, principal); err != nil {
		return nil, err
	}
	now := s.now()
	var doc *domain.Document
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		doc, err = s.documents.GetByID(ctx, principal.OrganisationID, documentID)
		if err != nil {
			return err
		}
		if doc.Status != domain.StatusFinalized {
			return domain.ErrDocumentNotFinalized
		}
		lines, err := s.lines.ListByDocument(ctx, documentID)
		if err != nil {
			return err
		}

		if domain.StockAffecting(doc.DocumentType, doc.ReferenceDocumentID) {
			// The reversal of whatever finalize originally posted: a
			// document that put stock in (GOODS_RECEIPT/PURCHASE_INVOICE)
			// gets it taken back out via PURCHASE_RETURN's movement type;
			// one that already took it out (PURCHASE_RETURN itself,
			// cancelled) puts it back in via the receipt movement type.
			movementType := inventorydomain.MovementPurchaseReturn
			if doc.DocumentType == domain.DocPurchaseReturn {
				movementType = inventorydomain.MovementPurchaseReceipt
			}
			for _, line := range lines {
				params := inventoryapp.RecordMovementParams{
					WarehouseID: doc.WarehouseID, ProductVariantID: line.ProductVariantID, MovementType: movementType,
					UnitID: line.UnitID, Quantity: line.Quantity,
					ReferenceType: "purchase_document", ReferenceID: &doc.ID,
					Notes: fmt.Sprintf("Cancellation of %s %s line %d", doc.DocumentType, doc.DocumentNumber, line.LineNumber),
				}
				if line.BatchCode != "" {
					batchCode := line.BatchCode
					params.BatchCode = &batchCode
				}
				if inventorydomain.IsReceipt(movementType) {
					cost := line.UnitPrice.Decimal()
					params.UnitCost = &cost
				}
				if _, err := s.inventory.RecordMovementForOtherModule(ctx, principal.OrganisationID, principal.UserID, params); err != nil {
					return fmt.Errorf("reversing stock movement for line %d: %w", line.LineNumber, err)
				}
			}
		}

		if s.accounting != nil && domain.AccountingAffecting(doc.DocumentType) {
			if _, err := s.accounting.ReverseJournalForSourceTx(ctx, principal, "purchase_document", doc.ID, "purchase_document_cancellation", "Cancellation of "+doc.DocumentNumber); err != nil {
				return fmt.Errorf("reversing purchase journal: %w", err)
			}
		}

		if err := s.documents.UpdateStatus(ctx, documentID, domain.StatusCancelled, &now); err != nil {
			return err
		}
		doc.Status = domain.StatusCancelled

		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "purchase_document.cancelled", EntityType: "purchase_document", EntityID: &doc.ID,
			AfterState: map[string]any{"document_number": doc.DocumentNumber, "document_type": string(doc.DocumentType)}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}
