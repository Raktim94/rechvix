// Package app is einvoice's application/use-case layer. Its one real entry
// point, GenerateForDocument, is called by apps/worker's outbox poller
// (never inline with an HTTP request — docs/architecture.md §9) and is
// itself idempotent: reprocessing an already-GENERATED document's outbox
// event is a safe no-op, not a duplicate IRN.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/einvoice/domain"
	"rechvix/internal/platform/outbox"

	contactsapp "rechvix/internal/modules/contacts/app"
	orgapp "rechvix/internal/modules/organisation/app"
	salesapp "rechvix/internal/modules/sales/app"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
)

type Service struct {
	records      domain.Repository
	provider     domain.EInvoiceProvider
	providerName string
	sales        *salesapp.Service
	taxation     *taxationapp.Service
	organisation *orgapp.Service
	contacts     *contactsapp.Service
	// outbox is optional (nil in some existing test fixtures/callers built
	// before Stage 9) — every enqueue call below is nil-guarded, exactly
	// like sales.Service.outbox's existing nil guard.
	outbox outbox.Writer
	now    func() time.Time
}

func NewService(
	records domain.Repository,
	provider domain.EInvoiceProvider,
	providerName string,
	salesSvc *salesapp.Service,
	taxationSvc *taxationapp.Service,
	organisationSvc *orgapp.Service,
	contactsSvc *contactsapp.Service,
	outboxWriter outbox.Writer,
) *Service {
	return &Service{
		records: records, provider: provider, providerName: providerName,
		sales: salesSvc, taxation: taxationSvc, organisation: organisationSvc, contacts: contactsSvc,
		outbox: outboxWriter, now: time.Now,
	}
}

// GetRecordForDocument returns the e-Invoice record for a sales
// document, or (nil, nil) if IRN generation was never applicable/never
// ran for it (a document type sales.FinalizeDocument doesn't enqueue
// einvoice.generate for, e.g. a QUOTATION — or one that's simply
// finalized too recently for the outbox worker to have picked it up
// yet). Read-only, permission-agnostic by design (same convention as
// ewaybill/app.Service.GetRecordForDocument) — the httpapi caller does
// its own "sales.view" check before calling this.
func (s *Service) GetRecordForDocument(ctx context.Context, orgID, salesDocumentID uuid.UUID) (*domain.Record, error) {
	rec, err := s.records.GetBySalesDocumentID(ctx, salesDocumentID)
	if err == domain.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rec.OrganisationID != orgID {
		return nil, domain.ErrNotFound
	}
	return rec, nil
}

// EventTypeGenerate is the outbox event_type sales.FinalizeDocument
// enqueues (Stage 8's addition to sales/app/service.go) and the one this
// service's Handler processes.
const EventTypeGenerate = "einvoice.generate"

// GeneratePayload is the outbox event's jsonb payload shape.
type GeneratePayload struct {
	SalesDocumentID uuid.UUID `json:"sales_document_id"`
}

// Handler adapts GenerateForDocument to outbox.Handler's signature, for
// apps/worker to register against EventTypeGenerate.
func (s *Service) Handler() outbox.Handler {
	return func(ctx context.Context, event outbox.Event) error {
		var p GeneratePayload
		if err := unmarshalPayload(event.Payload, &p); err != nil {
			return outbox.Permanent(fmt.Errorf("einvoice: malformed outbox payload: %w", err))
		}
		return s.GenerateForDocument(ctx, event.OrganisationID, p.SalesDocumentID)
	}
}

// GenerateForDocument must be called from inside an already-open
// RunScoped(ctx, orgID, ...) block (same convention as
// organisation.GetLegalEntityForOtherModule) — apps/worker's outbox
// poller provides that; this method does not open its own transaction.
//
// Idempotency: the first thing this does is check for an existing record.
// If one exists and is Terminal() (GENERATED, CANCELLED, or FAILED_FINAL),
// this returns nil immediately without calling the provider again — so
// reprocessing the same outbox event (worker crash/restart, a retried
// FAILED_RETRYABLE attempt landing after a prior attempt actually
// succeeded) can never produce two IRNs for one document. The
// einvoice_records.sales_document_id UNIQUE constraint
// (migrations/0024_einvoice_ewaybill.up.sql) backs this up at the database
// level even if this in-memory check were somehow bypassed.
func (s *Service) GenerateForDocument(ctx context.Context, orgID, salesDocumentID uuid.UUID) error {
	existing, err := s.records.GetBySalesDocumentID(ctx, salesDocumentID)
	if err != nil && err != domain.ErrNotFound {
		return fmt.Errorf("einvoice: loading existing record: %w", err)
	}
	if existing != nil {
		if existing.Status.Terminal() {
			return nil // already handled — see idempotency note above
		}
		// A FAILED_RETRYABLE record from a prior attempt: retry using the
		// same record row rather than creating a second one (the UNIQUE
		// constraint would reject a second Create anyway).
	} else {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("einvoice: generating record id: %w", err)
		}
		rec := &domain.Record{
			ID: id, OrganisationID: orgID, SalesDocumentID: salesDocumentID,
			Provider: s.providerName, Status: domain.StatusQueued, RequestVersion: "v1",
		}
		if err := s.records.Create(ctx, rec); err != nil {
			return fmt.Errorf("einvoice: creating record: %w", err)
		}
		existing = rec
	}

	req, err := s.buildIRNRequest(ctx, orgID, salesDocumentID)
	if err != nil {
		msg := err.Error()
		_ = s.records.UpdateStatus(ctx, existing.ID, domain.StatusFailedFinal, domain.UpdateFields{ErrorMessage: &msg})
		s.enqueueWebhookEvent(ctx, orgID, "einvoice.failed", existing.ID, salesDocumentID, msg)
		// A malformed/unresolvable request (e.g. supplier has no GSTIN
		// configured) will never succeed on retry — permanent, not
		// retryable.
		return outbox.Permanent(fmt.Errorf("einvoice: building IRN request: %w", err))
	}

	if err := s.records.UpdateStatus(ctx, existing.ID, domain.StatusSubmitting, domain.UpdateFields{}); err != nil {
		return fmt.Errorf("einvoice: marking submitting: %w", err)
	}

	resp, genErr := s.provider.GenerateIRN(ctx, req)
	if genErr != nil {
		msg := genErr.Error()
		if updErr := s.records.UpdateStatus(ctx, existing.ID, domain.StatusFailedRetryable, domain.UpdateFields{ErrorMessage: &msg}); updErr != nil {
			return fmt.Errorf("einvoice: marking failed-retryable: %w", updErr)
		}
		// Intentionally NOT wrapped in outbox.Permanent — a provider
		// error (timeout, transient sandbox unavailability) is exactly
		// the retryable case docs/architecture.md §9/Scenario L
		// describes: the sale itself is already FINALIZED and unaffected
		// (this whole call runs long after FinalizeDocument's own
		// transaction committed); only this e-Invoice record's status
		// reflects the failure, and the outbox will retry it later.
		return fmt.Errorf("einvoice: GenerateIRN: %w", genErr)
	}

	irn, ack := resp.IRN, resp.AckNumber
	ackDate := resp.AckDate
	signedInvoice, signedQR := resp.SignedInvoice, resp.SignedQRCode
	if err := s.records.UpdateStatus(ctx, existing.ID, domain.StatusGenerated, domain.UpdateFields{
		IRN: &irn, AckNumber: &ack, AckDate: &ackDate, SignedInvoice: &signedInvoice, SignedQRPayload: &signedQR,
	}); err != nil {
		return err
	}
	s.enqueueWebhookEvent(ctx, orgID, "einvoice.generated", existing.ID, salesDocumentID, irn)
	return nil
}

// enqueueWebhookEvent fans a webhook-facing source event out via the
// outbox (docs/adr/0005) — nil-guarded and best-effort by design: a
// failure to queue the notification must never turn an otherwise-
// successful (or already correctly-recorded-as-failed) e-Invoice outcome
// into an error the outbox poller would retry. Retrying would re-run the
// whole idempotent GenerateForDocument call pointlessly, since the real
// work already finished either way. Logged, not silently swallowed.
func (s *Service) enqueueWebhookEvent(ctx context.Context, orgID uuid.UUID, eventType string, recordID, salesDocumentID uuid.UUID, detail string) {
	if s.outbox == nil {
		return
	}
	idempotencyKey := "webhook-source:" + eventType + ":" + recordID.String()
	payload := map[string]any{"einvoice_record_id": recordID, "sales_document_id": salesDocumentID, "detail": detail}
	if err := s.outbox.Enqueue(ctx, orgID, eventType, idempotencyKey, payload); err != nil {
		slog.WarnContext(ctx, "einvoice: failed to enqueue webhook source event", "event_type", eventType, "error", err)
	}
}

// buildIRNRequest assembles the government-facing request from data three
// other modules already own (docs/architecture.md §2: einvoice is an
// adapter boundary, it doesn't own tax/sales/organisation logic, only
// orchestrates a call using their already-computed, already-finalized
// numbers).
func (s *Service) buildIRNRequest(ctx context.Context, orgID, salesDocumentID uuid.UUID) (domain.IRNRequest, error) {
	doc, lines, err := s.sales.GetDocumentForOtherModule(ctx, orgID, salesDocumentID)
	if err != nil {
		return domain.IRNRequest{}, fmt.Errorf("loading sales document: %w", err)
	}
	if doc.TaxDocumentID == nil {
		return domain.IRNRequest{}, fmt.Errorf("sales document %s has no tax snapshot (not finalized?)", salesDocumentID)
	}

	legalEntity, err := s.organisation.GetLegalEntityForOtherModule(ctx, orgID, doc.LegalEntityID)
	if err != nil {
		return domain.IRNRequest{}, fmt.Errorf("loading supplier legal entity: %w", err)
	}
	if legalEntity.GSTIN == "" {
		return domain.IRNRequest{}, fmt.Errorf("legal entity %s has no GSTIN configured", legalEntity.ID)
	}

	buyerGSTIN, buyerState := "", ""
	if doc.CustomerTaxRegistrationID != nil {
		reg, err := s.contacts.GetTaxRegistrationForOtherModule(ctx, orgID, *doc.CustomerTaxRegistrationID)
		if err == nil && reg != nil {
			buyerGSTIN, buyerState = reg.RegistrationNumber, reg.StateCode
		}
		// A lookup failure here is NOT fatal — a genuine B2C sale
		// legitimately has no buyer GSTIN; GenerateIRN's payload simply
		// carries an empty BuyerDtls.Gstin in that case.
	}

	taxDoc, taxLines, componentsByLine, err := s.taxation.GetByReference(ctx, orgID, "sales_document", doc.ID)
	if err != nil {
		return domain.IRNRequest{}, fmt.Errorf("loading tax snapshot: %w", err)
	}

	taxLineByRef := make(map[string]*taxLineWithComponents, len(taxLines))
	for _, tl := range taxLines {
		gstRate := decimal.Zero
		for _, c := range componentsByLine[tl.ID] {
			if c.ComponentType != "CESS" {
				gstRate = gstRate.Add(c.Rate)
			}
		}
		taxLineByRef[tl.LineRef] = &taxLineWithComponents{line: tl, gstRate: gstRate}
	}

	items := make([]domain.IRNLineItem, 0, len(lines))
	for _, l := range lines {
		ref := fmt.Sprintf("%d", l.LineNumber)
		tl, ok := taxLineByRef[ref]
		if !ok {
			return domain.IRNRequest{}, fmt.Errorf("no tax line found for sales document line %d", l.LineNumber)
		}
		items = append(items, domain.IRNLineItem{
			HSNSACCode: l.HSNSACCode, Quantity: l.Quantity, UnitPrice: l.UnitPrice.Decimal(),
			TaxableValue: tl.line.TaxableAmount.Decimal(), GSTRate: tl.gstRate, TaxAmount: tl.line.TotalTaxAmount.Decimal(),
		})
	}

	docType := "INV"
	switch doc.DocumentType {
	case "CREDIT_NOTE":
		docType = "CRN"
	case "DEBIT_NOTE":
		docType = "DBN"
	}

	return domain.IRNRequest{
		SupplierGSTIN: legalEntity.GSTIN, SupplierState: legalEntity.GSTStateCode,
		BuyerGSTIN: buyerGSTIN, BuyerState: buyerState,
		DocumentType: docType, DocumentNumber: doc.DocumentNumber, DocumentDate: doc.IssueDate,
		CurrencyCode: doc.CurrencyCode,
		TaxableValue: taxDoc.TotalTaxableAmount.Decimal(), TotalTax: taxDoc.TotalTaxAmount.Decimal(),
		GrandTotal: taxDoc.TotalTaxableAmount.Decimal().Add(taxDoc.TotalTaxAmount.Decimal()),
		Lines:      items,
	}, nil
}

type taxLineWithComponents struct {
	line    *taxdomain.TaxLine
	gstRate decimal.Decimal
}

func unmarshalPayload(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}
