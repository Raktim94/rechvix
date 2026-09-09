// Package app is the sales module's application/use-case layer. Mirrors
// internal/modules/purchases/app's shape. FinalizeDocument is where a
// document's tax snapshot (via taxation.Service.CalculateAndSnapshotTx)
// and stock effect (via inventory.RecordMovementForOtherModule) actually
// post — both inside this module's own RunScoped transaction, so the
// document's finalized state, its tax numbers, and its stock movement
// commit or roll back together (docs/architecture.md §2, brief §32).
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
	pricingapp "rechvix/internal/modules/pricing/app"
	"rechvix/internal/modules/sales/domain"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool         database.Runner
	documents    domain.DocumentRepository
	lines        domain.DocumentLineRepository
	inventory    *inventoryapp.Service
	taxation     *taxationapp.Service
	catalogue    *catalogueapp.Service
	contacts     *contactsapp.Service
	organisation *orgapp.Service
	// pricing is optional (BillingLookup's price-resolution enrichment is
	// best-effort — a nil pricing service just means BillingLookup never
	// populates UnitPrice, everything else still works).
	pricing   *pricingapp.Service
	numbering *numbering.Service
	// accounting is optional (nil in Stage 5a/5b-era callers or tests that
	// don't need accounting posting) — a nil accounting means
	// FinalizeDocument skips the journal-posting step entirely rather than
	// panicking, so existing sales tests that predate Stage 6 keep working
	// unchanged. Any caller that wants Scenario E's ledger behavior must
	// pass a real *accountingapp.Service.
	accounting *accountingapp.Service
	// outbox is optional (nil in pre-Stage-8 callers/tests) — same
	// nil-checked pattern as accounting. A nil outbox means
	// FinalizeDocument simply doesn't queue an e-Invoice generation
	// request; everything else about finalize is unaffected (Stage 8's
	// government-integration is additive, not a hard dependency of
	// finalizing a sale).
	outbox      outbox.Writer
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(
	pool database.Runner,
	documents domain.DocumentRepository,
	lines domain.DocumentLineRepository,
	inventorySvc *inventoryapp.Service,
	taxationSvc *taxationapp.Service,
	catalogueSvc *catalogueapp.Service,
	contactsSvc *contactsapp.Service,
	organisationSvc *orgapp.Service,
	pricingSvc *pricingapp.Service,
	numberingSvc *numbering.Service,
	accountingSvc *accountingapp.Service,
	outboxWriter outbox.Writer,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{pool: pool, documents: documents, lines: lines, inventory: inventorySvc, taxation: taxationSvc,
		catalogue: catalogueSvc, contacts: contactsSvc, organisation: organisationSvc, pricing: pricingSvc, numbering: numberingSvc,
		accounting: accountingSvc, outbox: outboxWriter, permissions: checker, audit: recorder, now: time.Now}
}

// Permission codes are the ones migrations/0002_rbac_catalog.up.sql
// already pre-seeded for this module's exact brief §26 example list —
// same reuse-don't-duplicate rationale as purchases (migrations/0014's
// header comment).
func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "sales.view", permissions.Scope{})
}
func (s *Service) create(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "sales.create", permissions.Scope{})
}
func (s *Service) editDraft(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "sales.edit_draft", permissions.Scope{})
}
func (s *Service) finalizePerm(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "sales.finalize", permissions.Scope{})
}
func (s *Service) discountPerm(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "sales.discount", permissions.Scope{})
}

func documentPrefix(t domain.DocumentType) string {
	switch t {
	case domain.DocQuotation:
		return "QTN"
	case domain.DocProformaInvoice:
		return "PI"
	case domain.DocSalesOrder:
		return "SO"
	case domain.DocDeliveryChallan:
		return "DC"
	case domain.DocTaxInvoice:
		return "INV"
	case domain.DocPOSInvoice:
		return "POS"
	case domain.DocCreditNote:
		return "CN"
	case domain.DocDebitNote:
		return "DN"
	case domain.DocSalesReturn:
		return "SR"
	case domain.DocRecurringInvoice:
		return "RINV"
	default:
		return "SDOC"
	}
}

type CreateDocumentParams struct {
	LegalEntityID             uuid.UUID
	BranchID                  uuid.UUID
	WarehouseID               uuid.UUID
	CustomerPartyID           uuid.UUID
	DocumentType              domain.DocumentType
	ReferenceDocumentID       *uuid.UUID
	IssueDate                 time.Time
	DueDate                   *time.Time
	SupplyDate                *time.Time
	BillingAddressID          *uuid.UUID
	ShippingAddressID         *uuid.UUID
	CustomerTaxRegistrationID *uuid.UUID
	// PlaceOfSupplyStateCode is taken as a direct input rather than
	// re-derived from the customer's address at finalize time — where a
	// supply is "deemed to occur" for GST purposes has real legal nuance
	// (bill-to vs. ship-to, specific-goods exceptions) this project isn't
	// going to silently guess (brief Rule 2: never invent a government
	// rule); the caller (eventually a UI defaulting it from the shipping
	// address, still overridable) supplies it explicitly, and it is then
	// immutable once set on the document (brief §55).
	PlaceOfSupplyStateCode string
	SalespersonUserID      *uuid.UUID
	PriceListID            *uuid.UUID
	CurrencyCode           string
	BaseCurrencyCode       string
	ExchangeRate           decimal.Decimal
	PricingMode            taxdomain.PricingMode
	CustomerReference      string
	Transporter            string
	VehicleNumber          string
	ShippingTerms          string
	Notes                  string
	TermsAndConditions     string
	PaymentTermsDays       int
}

func (s *Service) CreateDocument(ctx context.Context, principal permissions.Principal, p CreateDocumentParams) (*domain.Document, error) {
	if err := s.create(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidDocumentType(p.DocumentType) {
		return nil, domain.ErrInvalidDocumentType
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("sales: generating sales_document id: %w", err)
	}
	now := s.now()
	issueDate := p.IssueDate
	if issueDate.IsZero() {
		issueDate = now
	}
	pricingMode := p.PricingMode
	if pricingMode == "" {
		pricingMode = taxdomain.PricingExclusive
	}
	exchangeRate := p.ExchangeRate
	if exchangeRate.IsZero() {
		exchangeRate = decimal.NewFromInt(1)
	}
	d := &domain.Document{
		ID: id, OrganisationID: principal.OrganisationID, LegalEntityID: p.LegalEntityID, BranchID: p.BranchID,
		WarehouseID: p.WarehouseID, CustomerPartyID: p.CustomerPartyID, DocumentType: p.DocumentType,
		ReferenceDocumentID: p.ReferenceDocumentID, Status: domain.StatusDraft, IssueDate: issueDate,
		DueDate: p.DueDate, SupplyDate: p.SupplyDate, BillingAddressID: p.BillingAddressID, ShippingAddressID: p.ShippingAddressID,
		CustomerTaxRegistrationID: p.CustomerTaxRegistrationID, PlaceOfSupplyStateCode: p.PlaceOfSupplyStateCode,
		SalespersonUserID: p.SalespersonUserID, PriceListID: p.PriceListID, CurrencyCode: p.CurrencyCode,
		BaseCurrencyCode: p.BaseCurrencyCode, ExchangeRate: exchangeRate, PricingMode: pricingMode,
		CustomerReference: p.CustomerReference, Transporter: p.Transporter, VehicleNumber: p.VehicleNumber,
		ShippingTerms: p.ShippingTerms, Notes: p.Notes, TermsAndConditions: p.TermsAndConditions,
		PaymentTermsDays: p.PaymentTermsDays, CreatedBy: principal.UserID, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		number, err := s.numbering.NextDocumentNumber(ctx, principal.OrganisationID, p.BranchID, string(p.DocumentType), documentPrefix(p.DocumentType), issueDate)
		if err != nil {
			return err
		}
		d.DocumentNumber = number
		if err := s.documents.Create(ctx, d); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sales_document.created", EntityType: "sales_document", EntityID: &id,
			AfterState: map[string]any{"document_type": string(p.DocumentType), "document_number": d.DocumentNumber}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return d, nil
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

// GetDocumentForOtherModule is a cross-module read (Stage 8) — same
// pattern and rationale as organisation.GetLegalEntityForOtherModule: no
// permission check (the caller's own already-checked path authorizes
// this), and does NOT open its own transaction — the only caller
// (einvoice.Service, invoked from apps/worker's outbox poller) is already
// inside a RunScoped(ctx, orgID, ...) block when it calls this.
func (s *Service) GetDocumentForOtherModule(ctx context.Context, orgID, id uuid.UUID) (*domain.Document, []*domain.DocumentLine, error) {
	doc, err := s.documents.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, nil, err
	}
	lines, err := s.lines.ListByDocument(ctx, id)
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
	DocumentID         uuid.UUID
	ProductVariantID   uuid.UUID
	UnitID             uuid.UUID
	Quantity           decimal.Decimal
	UnitPrice          decimal.Decimal
	LineDiscountAmount decimal.Decimal
	BatchCode          string
	SerialCode         string
}

// AddLine appends a line to a DRAFT document. Business-document
// immutability (brief §31): a FINALIZED or CANCELLED document rejects
// this with ErrDocumentNotDraft. The catalogue lookup (for the HSN/SAC
// snapshot) runs BEFORE this method opens its own transaction, not nested
// inside it — a deliberate choice, same reasoning taxation.Service's own
// doc comment gives for CalculateAndSnapshot: a plain read doesn't need
// single-transaction atomicity with the write that follows, and this
// avoids a nested RunScoped call entirely rather than relying on it being
// merely harmless.
func (s *Service) AddLine(ctx context.Context, principal permissions.Principal, p AddLineParams) (*domain.DocumentLine, error) {
	if err := s.editDraft(ctx, principal); err != nil {
		return nil, err
	}
	if p.LineDiscountAmount.IsPositive() {
		if err := s.discountPerm(ctx, principal); err != nil {
			return nil, err
		}
	}
	_, product, err := s.catalogue.GetVariantWithProduct(ctx, principal, p.ProductVariantID)
	if err != nil {
		return nil, fmt.Errorf("sales: resolving product for line: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("sales: generating sales_document_line id: %w", err)
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
			return fmt.Errorf("sales: %w", err)
		}
		discount, err := money.New(p.LineDiscountAmount, doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("sales: %w", err)
		}
		lineTotalDecimal := p.Quantity.Mul(p.UnitPrice).Sub(p.LineDiscountAmount)
		lineTotal, err := money.New(lineTotalDecimal, doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("sales: %w", err)
		}
		line = &domain.DocumentLine{
			ID: id, OrganisationID: principal.OrganisationID, SalesDocumentID: p.DocumentID,
			LineNumber: len(existing) + 1, ProductVariantID: p.ProductVariantID, UnitID: p.UnitID,
			Quantity: p.Quantity, UnitPrice: unitPrice, LineDiscountAmount: discount,
			HSNSACCode: product.HSNSACCode, LineTotal: lineTotal,
			BatchCode: p.BatchCode, SerialCode: p.SerialCode, CreatedAt: now,
		}
		if err := s.lines.Create(ctx, line); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sales_document_line.added", EntityType: "sales_document_line", EntityID: &id,
			AfterState: map[string]any{"document_id": p.DocumentID, "quantity": p.Quantity.String(), "unit_price": p.UnitPrice.String()}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return line, nil
}

type UpdateLineParams struct {
	DocumentID         uuid.UUID
	LineID             uuid.UUID
	Quantity           decimal.Decimal
	UnitPrice          decimal.Decimal
	LineDiscountAmount decimal.Decimal
}

// UpdateLine changes an existing line's quantity/price/discount on a
// DRAFT document — the billing counter's "fix a quantity" action, which
// had no path at all before this (see DocumentLineRepository's own doc
// comment). Recomputes LineTotal itself rather than trusting a
// caller-supplied total, same "server recalculates, never trusts a
// client-sent total" rule AddLine already follows.
func (s *Service) UpdateLine(ctx context.Context, principal permissions.Principal, p UpdateLineParams) (*domain.DocumentLine, error) {
	if err := s.editDraft(ctx, principal); err != nil {
		return nil, err
	}
	if p.LineDiscountAmount.IsPositive() {
		if err := s.discountPerm(ctx, principal); err != nil {
			return nil, err
		}
	}
	if !p.Quantity.IsPositive() {
		return nil, fmt.Errorf("sales: quantity must be positive")
	}
	var line *domain.DocumentLine
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		doc, err := s.documents.GetByID(ctx, principal.OrganisationID, p.DocumentID)
		if err != nil {
			return err
		}
		if doc.Status != domain.StatusDraft {
			return domain.ErrDocumentNotDraft
		}
		existing, err := s.lines.GetByID(ctx, principal.OrganisationID, p.LineID)
		if err != nil {
			return err
		}
		if existing.SalesDocumentID != p.DocumentID {
			return domain.ErrNotFound
		}
		unitPrice, err := money.New(p.UnitPrice, doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("sales: %w", err)
		}
		discount, err := money.New(p.LineDiscountAmount, doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("sales: %w", err)
		}
		lineTotalDecimal := p.Quantity.Mul(p.UnitPrice).Sub(p.LineDiscountAmount)
		lineTotal, err := money.New(lineTotalDecimal, doc.CurrencyCode)
		if err != nil {
			return fmt.Errorf("sales: %w", err)
		}
		existing.Quantity = p.Quantity
		existing.UnitPrice = unitPrice
		existing.LineDiscountAmount = discount
		existing.LineTotal = lineTotal
		if err := s.lines.Update(ctx, existing); err != nil {
			return err
		}
		line = existing
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sales_document_line.updated", EntityType: "sales_document_line", EntityID: &p.LineID,
			AfterState: map[string]any{"document_id": p.DocumentID, "quantity": p.Quantity.String(), "unit_price": p.UnitPrice.String()}, At: s.now(),
		})
	})
	if err != nil {
		return nil, err
	}
	return line, nil
}

// DeleteLine removes a line from a DRAFT document — the billing
// counter's "remove an item scanned by mistake" action. Line numbers
// are not renumbered after a delete (a DRAFT-only, display-order
// concern, not worth the extra writes).
func (s *Service) DeleteLine(ctx context.Context, principal permissions.Principal, documentID, lineID uuid.UUID) error {
	if err := s.editDraft(ctx, principal); err != nil {
		return err
	}
	now := s.now()
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		doc, err := s.documents.GetByID(ctx, principal.OrganisationID, documentID)
		if err != nil {
			return err
		}
		if doc.Status != domain.StatusDraft {
			return domain.ErrDocumentNotDraft
		}
		existing, err := s.lines.GetByID(ctx, principal.OrganisationID, lineID)
		if err != nil {
			return err
		}
		if existing.SalesDocumentID != documentID {
			return domain.ErrNotFound
		}
		if err := s.lines.Delete(ctx, principal.OrganisationID, lineID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sales_document_line.deleted", EntityType: "sales_document_line", EntityID: &lineID,
			AfterState: map[string]any{"document_id": documentID}, At: now,
		})
	})
}

// FinalizeDocument transitions a DRAFT document to FINALIZED. In one
// transaction: calculates and snapshots tax (taxation.Service via
// gstindia), posts stock movements for StockAffecting document types
// (inventory.RecordMovementForOtherModule — rejecting finalization if
// stock is insufficient and the warehouse policy disallows negative
// stock, same as purchases), and updates the document's status and
// totals. If any step fails, nothing partial is left FINALIZED (brief
// §32).
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

		legalEntity, err := s.organisation.GetLegalEntityForOtherModule(ctx, principal.OrganisationID, doc.LegalEntityID)
		if err != nil {
			return fmt.Errorf("sales: resolving supplier legal entity: %w", err)
		}

		taxLines := make([]taxdomain.TaxableLine, 0, len(lines))
		for _, l := range lines {
			taxLines = append(taxLines, taxdomain.TaxableLine{
				LineRef:     fmt.Sprintf("%d", l.LineNumber),
				HSNSACCode:  l.HSNSACCode,
				Amount:      l.LineTotal,
				PricingMode: doc.PricingMode,
			})
		}
		taxDoc, _, _, err := s.taxation.CalculateAndSnapshotTx(ctx, taxationapp.SnapshotRequest{
			ReferenceType: "sales_document",
			ReferenceID:   &doc.ID,
			Input: taxdomain.TaxCalculationInput{
				OrganisationID:    principal.OrganisationID,
				Lines:             taxLines,
				SupplierStateCode: legalEntity.GSTStateCode,
				SupplyPlace:       taxdomain.PlaceOfSupply{StateCode: doc.PlaceOfSupplyStateCode},
				DocumentDate:      doc.IssueDate,
				SupplyType:        taxdomain.SupplyB2C,
				CurrencyCode:      doc.CurrencyCode,
			},
		})
		if err != nil {
			return fmt.Errorf("calculating tax: %w", err)
		}
		if domain.RevenueAffecting(doc.DocumentType) && !taxDoc.GrandTotal.Decimal().IsPositive() {
			return domain.ErrZeroValueDocument
		}

		if domain.StockAffecting(doc.DocumentType) {
			movementType := inventorydomain.MovementSale
			if domain.MovementTypeFor(doc.DocumentType) == "SALE_RETURN" {
				movementType = inventorydomain.MovementSaleReturn
			}
			for _, line := range lines {
				params := inventoryapp.RecordMovementParams{
					WarehouseID: doc.WarehouseID, ProductVariantID: line.ProductVariantID, MovementType: movementType,
					UnitID: line.UnitID, Quantity: line.Quantity,
					ReferenceType: "sales_document", ReferenceID: &doc.ID,
					Notes: fmt.Sprintf("%s %s line %d", doc.DocumentType, doc.DocumentNumber, line.LineNumber),
				}
				if line.BatchCode != "" {
					batchCode := line.BatchCode
					params.BatchCode = &batchCode
				}
				if line.SerialCode != "" {
					serialCode := line.SerialCode
					params.SerialCode = &serialCode
				}
				if _, err := s.inventory.RecordMovementForOtherModule(ctx, principal.OrganisationID, principal.UserID, params); err != nil {
					return fmt.Errorf("posting stock movement for line %d: %w", line.LineNumber, err)
				}
			}
		}

		if s.accounting != nil && domain.RevenueAffecting(doc.DocumentType) {
			customerID := doc.CustomerPartyID
			debitCode, creditSalesCode, creditTaxCode := accountingdomain.CodeAccountsReceivable, accountingdomain.CodeSales, accountingdomain.CodeGSTOutputTaxPayable
			lines := []accountingapp.JournalLineRequest{
				{AccountCode: debitCode, PartyID: &customerID, Debit: taxDoc.GrandTotal.Decimal(), Description: "Sale " + doc.DocumentNumber},
				{AccountCode: creditSalesCode, Credit: taxDoc.TotalTaxableAmount.Decimal(), Description: "Sale " + doc.DocumentNumber},
			}
			if taxDoc.TotalTaxAmount.Decimal().IsPositive() {
				lines = append(lines, accountingapp.JournalLineRequest{AccountCode: creditTaxCode, Credit: taxDoc.TotalTaxAmount.Decimal(), Description: "Sale " + doc.DocumentNumber})
			}
			if domain.ReducesReceivable(doc.DocumentType) {
				// A credit note / sales return reduces what the customer
				// owes and reduces recognized revenue — post with reversed
				// polarity (Cr AR / Dr Sales + Dr Tax) instead of building
				// a second code path: swap each line's Debit/Credit.
				for i := range lines {
					lines[i].Debit, lines[i].Credit = lines[i].Credit, lines[i].Debit
				}
			}
			if _, err := s.accounting.PostTx(ctx, principal, accountingapp.JournalRequest{
				OrganisationID: principal.OrganisationID, SourceType: "sales_document", SourceID: &doc.ID,
				JournalDate: doc.IssueDate, Description: "Sale " + doc.DocumentNumber, CreatedBy: principal.UserID, Lines: lines,
			}); err != nil {
				return fmt.Errorf("posting sale journal: %w", err)
			}
		}

		if err := s.documents.UpdateFinalizedTotals(ctx, documentID, taxDoc.ID, taxDoc.GrandTotal.Decimal()); err != nil {
			return err
		}
		if err := s.documents.UpdateStatus(ctx, documentID, domain.StatusFinalized, &now); err != nil {
			return err
		}
		doc.Status = domain.StatusFinalized
		doc.FinalizedAt = &now
		doc.TaxDocumentID = &taxDoc.ID
		grandTotal := taxDoc.GrandTotal
		doc.GrandTotalAmount = &grandTotal

		// Queue e-Invoice generation (Stage 8) atomically with
		// finalization itself — one more INSERT inside this same
		// transaction (docs/architecture.md §9/§34), never a second,
		// separate government-API call inline here. TAX_INVOICE only for
		// now (the document type e-invoicing actually applies to);
		// CREDIT_NOTE/DEBIT_NOTE e-invoicing is a real brief §9 scenario
		// but is left as a follow-up rather than guessed at under this
		// stage's time budget — see the Stage 8 report.
		if s.outbox != nil && doc.DocumentType == domain.DocTaxInvoice {
			if err := s.outbox.Enqueue(ctx, principal.OrganisationID, "einvoice.generate",
				"einvoice:generate:"+doc.ID.String(),
				map[string]any{"sales_document_id": doc.ID}); err != nil {
				return fmt.Errorf("queuing e-invoice generation: %w", err)
			}
		}

		// Webhook fan-out source event (Stage 9, docs/adr/0005). Sales has
		// no idea webhooks exists — this is a bare outbox enqueue using
		// brief §38's event catalog name; webhooks.Service registers its
		// own handler against "invoice.finalized" in apps/worker.
		if s.outbox != nil {
			if err := s.outbox.Enqueue(ctx, principal.OrganisationID, "invoice.finalized",
				"webhook-source:invoice.finalized:"+doc.ID.String(),
				map[string]any{"document_id": doc.ID, "document_number": doc.DocumentNumber,
					"document_type": string(doc.DocumentType)}); err != nil {
				return fmt.Errorf("queuing invoice.finalized webhook event: %w", err)
			}
		}

		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sales_document.finalized", EntityType: "sales_document", EntityID: &documentID,
			AfterState: map[string]any{"document_type": string(doc.DocumentType), "document_number": doc.DocumentNumber,
				"line_count": len(lines), "grand_total": taxDoc.GrandTotal.StringFixed(money.RoundHalfUp)}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// ConvertDocument creates a new DRAFT document of targetType, copying the
// source document's header and lines (fresh line numbers, editable before
// the new document is itself finalized) — the estimate/quotation->invoice,
// sales-order->invoice, and delivery-challan->invoice conversion flows
// (brief §5) are all this one generic operation parameterized by
// targetType, not three separate code paths. The source must be
// FINALIZED (a DRAFT quotation isn't confirmed yet — converting an
// unconfirmed draft would be premature) — this is a judgment call, not a
// brief-mandated rule; a business that wants to convert from DRAFT can
// finalize the quotation first, which costs nothing since a quotation
// finalize doesn't affect stock.
// lineQuantities is an optional partial-quantity override, keyed by
// SOURCE line ID: nil or empty copies every line at its full original
// quantity (unchanged behavior — every existing caller of
// ConvertDocument gets exactly what it got before this parameter
// existed). Non-empty restricts the copy to only the line IDs present as
// keys, each at the given quantity — the SALES_RETURN/CREDIT_NOTE case,
// where a customer returning one defective item out of a five-line
// invoice must not silently also return the other four. There is no
// line-update/delete endpoint on ANY sales document (finalized or
// draft) to fix this up after the fact, so the override has to apply at
// copy time.
func (s *Service) ConvertDocument(ctx context.Context, principal permissions.Principal, sourceDocumentID uuid.UUID, targetType domain.DocumentType, lineQuantities map[uuid.UUID]decimal.Decimal) (*domain.Document, error) {
	if err := s.create(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidDocumentType(targetType) {
		return nil, domain.ErrInvalidDocumentType
	}
	source, sourceLines, err := s.GetDocument(ctx, principal, sourceDocumentID)
	if err != nil {
		return nil, err
	}
	if source.Status != domain.StatusFinalized {
		return nil, domain.ErrDocumentNotFinalized
	}
	if len(lineQuantities) > 0 {
		for _, sl := range sourceLines {
			qty, ok := lineQuantities[sl.ID]
			if !ok {
				continue
			}
			if qty.GreaterThan(sl.Quantity) {
				return nil, domain.ErrReturnQuantityExceedsSource
			}
		}
	}
	refID := source.ID
	target, err := s.CreateDocument(ctx, principal, CreateDocumentParams{
		LegalEntityID: source.LegalEntityID, BranchID: source.BranchID, WarehouseID: source.WarehouseID,
		CustomerPartyID: source.CustomerPartyID, DocumentType: targetType, ReferenceDocumentID: &refID,
		IssueDate: s.now(), BillingAddressID: source.BillingAddressID, ShippingAddressID: source.ShippingAddressID,
		CustomerTaxRegistrationID: source.CustomerTaxRegistrationID, PlaceOfSupplyStateCode: source.PlaceOfSupplyStateCode,
		SalespersonUserID: source.SalespersonUserID, PriceListID: source.PriceListID, CurrencyCode: source.CurrencyCode,
		BaseCurrencyCode: source.BaseCurrencyCode, ExchangeRate: source.ExchangeRate, PricingMode: source.PricingMode,
		CustomerReference: source.CustomerReference, Transporter: source.Transporter, VehicleNumber: source.VehicleNumber,
		ShippingTerms: source.ShippingTerms, Notes: source.Notes, TermsAndConditions: source.TermsAndConditions,
		PaymentTermsDays: source.PaymentTermsDays,
	})
	if err != nil {
		return nil, err
	}
	for _, sl := range sourceLines {
		qty := sl.Quantity
		if len(lineQuantities) > 0 {
			override, ok := lineQuantities[sl.ID]
			if !ok || !override.IsPositive() {
				continue
			}
			qty = override
		}
		if _, err := s.AddLine(ctx, principal, AddLineParams{
			DocumentID: target.ID, ProductVariantID: sl.ProductVariantID, UnitID: sl.UnitID,
			Quantity: qty, UnitPrice: sl.UnitPrice.Decimal(), LineDiscountAmount: sl.LineDiscountAmount.Decimal(),
			BatchCode: sl.BatchCode, SerialCode: sl.SerialCode,
		}); err != nil {
			return nil, fmt.Errorf("sales: copying line %d during conversion: %w", sl.LineNumber, err)
		}
	}
	return target, nil
}

// CancelDocument voids a mis-billed FINALIZED document: reverses its
// stock effect (StockAffecting types only — the same restocking a
// SALES_RETURN does, since "this sale is void" and "these goods came
// back" have the identical inventory consequence) and its accounting
// effect (RevenueAffecting types only — a journal with every line's
// Debit/Credit swapped from the original, same technique
// ReducesReceivable-handling already uses above, posted as its own
// entry rather than deleting/editing the original one: journal_lines
// are immutable by DB trigger, brief §7, and a real audit trail needs
// the reversal to be visible as its own event, not a silent edit).
// Uses sales.finalize — the same permission that let this document be
// finalized in the first place is what's needed to undo that.
//
// Deliberately does NOT touch any receipt/payment already recorded
// against this document (accounting.Receipt/Payment rows referencing
// it via SalesDocumentID) — whether an already-collected payment should
// be refunded, kept as store credit, or applied elsewhere is a business
// decision this method has no basis to make unilaterally. The caller
// (httpapi/frontend) surfaces that as a warning before cancelling, not
// as something this method resolves on its own.
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

		if domain.StockAffecting(doc.DocumentType) {
			// The reversal of whatever finalize originally posted: a
			// document that took stock out (SALE-type movements) gets it
			// back via SALE_RETURN; one that already put it back
			// (SALES_RETURN itself, cancelled) takes it back out via SALE.
			movementType := inventorydomain.MovementSaleReturn
			if domain.MovementTypeFor(doc.DocumentType) == "SALE_RETURN" {
				movementType = inventorydomain.MovementSale
			}
			for _, line := range lines {
				params := inventoryapp.RecordMovementParams{
					WarehouseID: doc.WarehouseID, ProductVariantID: line.ProductVariantID, MovementType: movementType,
					UnitID: line.UnitID, Quantity: line.Quantity,
					ReferenceType: "sales_document", ReferenceID: &doc.ID,
					Notes: fmt.Sprintf("Cancellation of %s %s line %d", doc.DocumentType, doc.DocumentNumber, line.LineNumber),
				}
				if line.BatchCode != "" {
					batchCode := line.BatchCode
					params.BatchCode = &batchCode
				}
				if line.SerialCode != "" {
					serialCode := line.SerialCode
					params.SerialCode = &serialCode
				}
				if _, err := s.inventory.RecordMovementForOtherModule(ctx, principal.OrganisationID, principal.UserID, params); err != nil {
					return fmt.Errorf("reversing stock movement for line %d: %w", line.LineNumber, err)
				}
			}
		}

		if s.accounting != nil && domain.RevenueAffecting(doc.DocumentType) {
			// Reverses the EXACT journal finalize posted (same accounts,
			// same taxable/tax split, whichever polarity ReducesReceivable
			// already gave it) rather than reconstructing one from
			// doc.GrandTotalAmount — sales_documents doesn't store the
			// taxable/tax split at all (only the grand total), so
			// rebuilding it here would either have to re-derive tax
			// (drifting from what was actually posted if a rate changed
			// since) or lump the whole total onto Sales with no separate
			// tax line. Reversing the real journal is both simpler and
			// exact.
			if _, err := s.accounting.ReverseJournalForSourceTx(ctx, principal, "sales_document", doc.ID, "sales_document_cancellation", "Cancellation of "+doc.DocumentNumber); err != nil {
				return fmt.Errorf("reversing sale journal: %w", err)
			}
		}

		if err := s.documents.UpdateStatus(ctx, documentID, domain.StatusCancelled, &now); err != nil {
			return err
		}
		doc.Status = domain.StatusCancelled

		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sales_document.cancelled", EntityType: "sales_document", EntityID: &doc.ID,
			AfterState: map[string]any{"document_number": doc.DocumentNumber, "document_type": string(doc.DocumentType)}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return doc, nil
}
