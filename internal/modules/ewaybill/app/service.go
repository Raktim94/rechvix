// Package app is e-Way Bill's application layer. GenerateForDocument is
// called directly (via httpapi), NOT auto-enqueued from
// sales.FinalizeDocument the way einvoice's is — whether a given sale
// needs an e-Way Bill depends on real-world facts this codebase doesn't
// model yet (goods-movement distance/value thresholds, exempt-goods
// lists), so auto-triggering it would be guessing at a business rule
// rather than implementing one. An operator/API caller decides when to
// generate one, same as they'd decide when to actually dispatch goods.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	contactsapp "rechvix/internal/modules/contacts/app"
	einvoicedomain "rechvix/internal/modules/einvoice/domain"
	"rechvix/internal/modules/ewaybill/canonical"
	"rechvix/internal/modules/ewaybill/domain"
	"rechvix/internal/modules/ewaybill/eligibility"
	"rechvix/internal/modules/ewaybill/portal"
	portalv1 "rechvix/internal/modules/ewaybill/portal/v1"
	orgapp "rechvix/internal/modules/organisation/app"
	salesapp "rechvix/internal/modules/sales/app"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/audit"
)

type Service struct {
	records  domain.Repository
	provider einvoicedomain.EInvoiceProvider
	sales    *salesapp.Service

	// The fields below are only required for the FREE_PORTAL flow
	// (docs/architecture.md §9b) — nil-guarded in every method that uses
	// them, same convention as einvoice.Service.outbox, so existing
	// AUTOMATIC_API-only callers/tests don't have to change.
	organisation *orgapp.Service
	contacts     *contactsapp.Service
	taxation     *taxationapp.Service
	eligibility  eligibility.Repository
	exporter     portal.Exporter
	audit        audit.Recorder

	now func() time.Time
}

func NewService(records domain.Repository, provider einvoicedomain.EInvoiceProvider, salesSvc *salesapp.Service) *Service {
	return &Service{records: records, provider: provider, sales: salesSvc, now: time.Now}
}

// WithFreePortal returns a copy of s with the FREE_PORTAL-mode
// dependencies wired in — a separate constructor step (rather than
// growing NewService's parameter list) so every existing AUTOMATIC_API-
// only call site keeps working unchanged.
func (s *Service) WithFreePortal(organisationSvc *orgapp.Service, contactsSvc *contactsapp.Service,
	taxationSvc *taxationapp.Service, eligibilityRepo eligibility.Repository, exporter portal.Exporter, recorder audit.Recorder) *Service {
	cp := *s
	cp.organisation, cp.contacts, cp.taxation, cp.eligibility, cp.exporter, cp.audit = organisationSvc, contactsSvc, taxationSvc, eligibilityRepo, exporter, recorder
	return &cp
}

// recordAudit is a nil-guarded best-effort audit write (brief §30) — a
// failure to write an audit entry must never fail the underlying
// operation it's describing (same principle as sales/einvoice's
// nil-guarded outbox enqueue calls elsewhere in this codebase).
func (s *Service) recordAudit(ctx context.Context, orgID uuid.UUID, action, entityType string, entityID uuid.UUID, after any) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, audit.Entry{
		OrganisationID: orgID, ActorType: audit.ActorSystem, Action: action,
		EntityType: entityType, EntityID: &entityID, AfterState: after,
	}); err != nil {
		// Best-effort: log-and-continue would need a logger threaded
		// through this service, which doesn't otherwise exist here —
		// swallowing is consistent with "must never fail the underlying
		// operation," and this is not a security control on its own
		// (RLS/permission checks are), just a trail.
		_ = err
	}
}

type GenerateParams struct {
	SalesDocumentID uuid.UUID
	IRN             string // from the sales document's already-GENERATED einvoice_records row
	TransporterID   string
	TransporterName string
	TransportMode   string
	VehicleNumber   string
	DistanceKM      decimal.Decimal
	// ShipToGSTIN: the 2026-08-01 GSTN requirement — pass "URP" explicitly
	// for an unregistered/not-applicable recipient, never leave it empty
	// when ship-to details are present (docs/research.md).
	ShipToGSTIN string
}

// GenerateForDocument must run inside a caller-provided RunScoped block
// (same non-self-scoping convention as einvoice.Service — its only
// intended caller is httpapi, invoked after that layer's own permission
// check and RunScoped wrapper; see internal/modules/ewaybill/httpapi if/when
// it's added). Idempotent the same way einvoice is: an existing GENERATED
// record for this sales document is left alone.
func (s *Service) GenerateForDocument(ctx context.Context, orgID uuid.UUID, p GenerateParams) (*domain.Record, error) {
	existing, err := s.records.GetBySalesDocumentID(ctx, orgID, p.SalesDocumentID)
	if err != nil && err != domain.ErrNotFound {
		return nil, fmt.Errorf("ewaybill: loading existing record: %w", err)
	}
	if existing != nil && existing.Status.Terminal() {
		return existing, nil
	}

	if existing == nil {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("ewaybill: generating record id: %w", err)
		}
		irn := p.IRN
		rec := &domain.Record{
			ID: id, OrganisationID: orgID, SalesDocumentID: p.SalesDocumentID, IRN: &irn,
			Status: domain.StatusQueued, TransporterID: p.TransporterID, TransporterName: p.TransporterName,
			VehicleNumber: p.VehicleNumber,
		}
		if p.ShipToGSTIN != "" {
			rec.ShipToGSTIN = &p.ShipToGSTIN
		}
		distStr := p.DistanceKM.String()
		rec.DistanceKM = &distStr
		if err := s.records.Create(ctx, rec); err != nil {
			return nil, fmt.Errorf("ewaybill: creating record: %w", err)
		}
		existing = rec
	}

	if err := s.records.UpdateStatus(ctx, orgID, existing.ID, domain.StatusSubmitting, domain.UpdateFields{}); err != nil {
		return nil, fmt.Errorf("ewaybill: marking submitting: %w", err)
	}

	resp, genErr := s.provider.GenerateEWayBillByIRN(ctx, p.IRN, einvoicedomain.TransportDetails{
		TransporterID: p.TransporterID, TransporterName: p.TransporterName, TransportMode: p.TransportMode,
		VehicleNumber: p.VehicleNumber, DistanceKM: p.DistanceKM, ShipToGSTIN: p.ShipToGSTIN,
	})
	if genErr != nil {
		msg := genErr.Error()
		if updErr := s.records.UpdateStatus(ctx, orgID, existing.ID, domain.StatusFailedRetryable, domain.UpdateFields{ErrorMessage: &msg}); updErr != nil {
			return nil, fmt.Errorf("ewaybill: marking failed-retryable: %w", updErr)
		}
		return nil, fmt.Errorf("ewaybill: GenerateEWayBillByIRN: %w", genErr)
	}

	ewbNo := resp.EWBNumber
	validFrom, validUntil := resp.ValidFrom, resp.ValidUntil
	if err := s.records.UpdateStatus(ctx, orgID, existing.ID, domain.StatusGenerated, domain.UpdateFields{
		EWBNumber: &ewbNo, ValidFrom: &validFrom, ValidUntil: &validUntil,
	}); err != nil {
		return nil, fmt.Errorf("ewaybill: marking generated: %w", err)
	}
	existing.Status = domain.StatusGenerated
	existing.EWBNumber = &ewbNo
	return existing, nil
}

// UpdatePartB records a vehicle/transporter change en route — appended to
// history (brief §10's "Part-B history"), not overwritten.
func (s *Service) UpdatePartB(ctx context.Context, orgID, id uuid.UUID, update domain.PartBUpdate) error {
	update.At = s.now()
	return s.records.AppendPartBHistory(ctx, orgID, id, update)
}

// Close implements the 2026-08-01 GSTN voluntary EWB closure facility
// (docs/research.md): supplier/recipient/transporter/driver marks an
// already-GENERATED EWB CLOSED (the shipment happened) — distinct from
// Cancel (the movement never happened).
func (s *Service) Close(ctx context.Context, orgID, id uuid.UUID, role domain.ClosedByRole) error {
	switch role {
	case domain.ClosedBySupplier, domain.ClosedByRecipient, domain.ClosedByTransporter, domain.ClosedByDriver:
	default:
		return domain.ErrInvalidCloseRole
	}
	now := time.Now()
	return s.records.UpdateStatus(ctx, orgID, id, domain.StatusClosed, domain.UpdateFields{ClosedAt: &now, ClosedByRole: &role})
}

// GetRecordForDocument is a thin read wrapper for httpapi's status
// endpoint — domain.ErrNotFound (unwrapped) means no e-Way Bill record
// exists yet for this document at all, distinct from NOT_REQUIRED, which
// is a record that exists and was evaluated as not needed.
func (s *Service) GetRecordForDocument(ctx context.Context, orgID, salesDocumentID uuid.UUID) (*domain.Record, error) {
	return s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
}

// Cancel marks an EWB CANCELLED (the movement never happened), as opposed
// to Close (it happened, it's just no longer in transit).
func (s *Service) Cancel(ctx context.Context, orgID, salesDocumentID uuid.UUID, reason string) error {
	rec, err := s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
	if err != nil {
		return fmt.Errorf("ewaybill: loading record to cancel: %w", err)
	}
	if rec.Status != domain.StatusGenerated || rec.EWBNumber == nil {
		return domain.ErrNotGenerated
	}
	if err := s.provider.CancelEWayBill(ctx, *rec.EWBNumber, reason); err != nil {
		return fmt.Errorf("ewaybill: provider cancel: %w", err)
	}
	now := time.Now()
	return s.records.UpdateStatus(ctx, rec.OrganisationID, rec.ID, domain.StatusCancelled, domain.UpdateFields{CancelledAt: &now, CancelReason: &reason})
}

// ============================================================
// FREE_PORTAL mode (docs/architecture.md §9b, Stage 8c)
// ============================================================

// EligibilityResult is EvaluateEligibility's return shape.
type EligibilityResult struct {
	Requirement eligibility.Requirement
	Missing     []eligibility.MissingInfo
	RecordID    uuid.UUID
}

// TransportInfoParams patches the transport section of an already-captured
// canonical snapshot — the "user fills in what's missing" step the source
// spec's §9/§35 describes (vehicle, transporter, distance). sales.Document
// does not carry a distance field at all (a real, pre-existing gap this
// stage doesn't redesign sales' schema to close), so distance can only
// ever be supplied here, never derived from live sales data — which is
// exactly right: it's genuinely user-entered information, not a business
// fact the invoice itself records.
type TransportInfoParams struct {
	VehicleNumber   *string
	TransporterID   *string
	TransporterName *string
	DistanceKM      *decimal.Decimal
}

// UpdateTransportInfo patches the stored canonical snapshot's transport
// fields and returns the freshly re-evaluated eligibility — must run
// inside a caller-provided RunScoped block. Requires a record to already
// exist (call EvaluateEligibility first).
func (s *Service) UpdateTransportInfo(ctx context.Context, orgID, salesDocumentID uuid.UUID, p TransportInfoParams) (*EligibilityResult, error) {
	rec, err := s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
	if err != nil {
		return nil, fmt.Errorf("ewaybill: loading record: %w", err)
	}
	var bill canonical.CanonicalEWayBill
	if err := json.Unmarshal(rec.CanonicalSnapshot, &bill); err != nil {
		return nil, fmt.Errorf("ewaybill: unmarshaling stored canonical snapshot: %w", err)
	}
	if p.VehicleNumber != nil {
		bill.Transport.VehicleNumber = *p.VehicleNumber
	}
	if p.TransporterID != nil {
		bill.Transport.TransporterID = *p.TransporterID
	}
	if p.TransporterName != nil {
		bill.Transport.TransporterName = *p.TransporterName
	}
	if p.DistanceKM != nil {
		bill.Transport.DistanceKM = *p.DistanceKM
	}
	snapshot, err := json.Marshal(bill)
	if err != nil {
		return nil, fmt.Errorf("ewaybill: marshaling updated snapshot: %w", err)
	}
	if err := s.records.UpdateStatus(ctx, orgID, rec.ID, rec.Status, domain.UpdateFields{CanonicalSnapshot: snapshot}); err != nil {
		return nil, fmt.Errorf("ewaybill: persisting updated snapshot: %w", err)
	}
	return s.EvaluateEligibility(ctx, orgID, salesDocumentID)
}

// EvaluateEligibility implements EvaluateEWayBillRequirement (docs/
// architecture.md §9b). Must run inside a caller-provided RunScoped block,
// same convention as GenerateForDocument.
//
// Idempotency/snapshot discipline: if a FREE_PORTAL-mode record already
// exists for this document with a captured canonical_snapshot, this
// re-evaluates against THAT stored snapshot (never re-derives from live
// catalogue/contacts data — the whole point of §9b's immutability
// guarantee). Only when no FREE_PORTAL-mode record exists yet does this
// build a fresh canonical.CanonicalEWayBill from the invoice's current
// finalized-snapshot data (sales/taxation, both already immutable per
// brief §55) and persist it as the new record's one-time capture.
func (s *Service) EvaluateEligibility(ctx context.Context, orgID, salesDocumentID uuid.UUID) (*EligibilityResult, error) {
	rec, bill, err := s.getOrCreateFreePortalRecord(ctx, orgID, salesDocumentID)
	if err != nil {
		return nil, err
	}
	rules, err := s.eligibility.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("ewaybill: loading eligibility rules: %w", err)
	}
	// A per-organisation threshold override (Organisation.
	// EWayBillThresholdOverride) replaces every candidate rule's
	// MinConsignmentValue before Evaluate picks the one applicable to
	// this invoice's state/date — state selection and validity windows
	// are untouched, only the value threshold changes. Evaluate itself
	// stays pure (docs/architecture.md §9b) — the substitution happens
	// here, at the call site, not inside the eligibility package. s.
	// organisation is nil-guarded (see the Service struct's own doc
	// comment) so this degrades to "no override" if the organisation
	// module wasn't wired in for this composition.
	if s.organisation != nil {
		org, err := s.organisation.GetOrganisationForOtherModule(ctx, orgID)
		if err != nil {
			return nil, fmt.Errorf("ewaybill: loading organisation for threshold override: %w", err)
		}
		if org.EWayBillThresholdOverride != nil {
			overridden := make([]eligibility.Rule, len(rules))
			for i, rule := range rules {
				rule.MinConsignmentValue = *org.EWayBillThresholdOverride
				overridden[i] = rule
			}
			rules = overridden
		}
	}
	req, missing := eligibility.Evaluate(rules, bill, s.now())

	status := domain.StatusNotRequired
	switch req {
	case eligibility.Ready:
		status = domain.StatusReady
	case eligibility.NeedsInformation:
		status = domain.StatusNeedsInformation
	case eligibility.Required:
		status = domain.StatusNeedsInformation // REQUIRED-but-not-yet-evaluated-for-completeness collapses to NEEDS_INFORMATION until Ready
	}
	if !rec.Status.Terminal() || rec.Status == domain.StatusNotRequired {
		if err := s.records.UpdateStatus(ctx, orgID, rec.ID, status, domain.UpdateFields{}); err != nil {
			return nil, fmt.Errorf("ewaybill: updating eligibility status: %w", err)
		}
	}
	s.recordAudit(ctx, orgID, "ewaybill.eligibility_evaluated", "ewaybill_record", rec.ID,
		map[string]any{"sales_document_id": salesDocumentID, "requirement": req})
	return &EligibilityResult{Requirement: req, Missing: missing, RecordID: rec.ID}, nil
}

// PrepareFreePortalUpload builds the government-portal upload file from
// the document's (already-captured, immutable) canonical snapshot. Must
// run inside a caller-provided RunScoped block.
func (s *Service) PrepareFreePortalUpload(ctx context.Context, orgID, salesDocumentID uuid.UUID) (portal.PreparedFile, *domain.Record, error) {
	elig, err := s.EvaluateEligibility(ctx, orgID, salesDocumentID)
	if err != nil {
		return portal.PreparedFile{}, nil, err
	}
	if elig.Requirement == eligibility.NotRequired {
		return portal.PreparedFile{}, nil, domain.ErrNotEligible
	}
	if elig.Requirement != eligibility.Ready {
		return portal.PreparedFile{}, nil, fmt.Errorf("ewaybill: cannot prepare upload, missing information: %+v", elig.Missing)
	}

	rec, err := s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
	if err != nil {
		return portal.PreparedFile{}, nil, fmt.Errorf("ewaybill: loading record: %w", err)
	}
	var bill canonical.CanonicalEWayBill
	if err := json.Unmarshal(rec.CanonicalSnapshot, &bill); err != nil {
		return portal.PreparedFile{}, nil, fmt.Errorf("ewaybill: unmarshaling stored canonical snapshot: %w", err)
	}

	if err := s.records.UpdateStatus(ctx, orgID, rec.ID, domain.StatusPreparing, domain.UpdateFields{}); err != nil {
		return portal.PreparedFile{}, nil, fmt.Errorf("ewaybill: marking preparing: %w", err)
	}

	file, err := s.exporter.PrepareUpload(ctx, bill)
	if err != nil {
		msg := err.Error()
		_ = s.records.UpdateStatus(ctx, orgID, rec.ID, domain.StatusNeedsInformation, domain.UpdateFields{ErrorMessage: &msg})
		return portal.PreparedFile{}, nil, fmt.Errorf("ewaybill: preparing portal upload: %w", err)
	}

	now := s.now()
	if err := s.records.UpdateStatus(ctx, orgID, rec.ID, domain.StatusAwaitingPortalCompletion, domain.UpdateFields{
		PreparedFileName: &file.FileName, PreparedAt: &now,
	}); err != nil {
		return portal.PreparedFile{}, nil, fmt.Errorf("ewaybill: marking awaiting portal completion: %w", err)
	}
	rec.Status = domain.StatusAwaitingPortalCompletion
	rec.PreparedFileName = &file.FileName
	s.recordAudit(ctx, orgID, "ewaybill.portal_upload_prepared", "ewaybill_record", rec.ID,
		map[string]any{"sales_document_id": salesDocumentID, "file_name": file.FileName})
	return file, rec, nil
}

// BatchSkip records one document PrepareFreePortalUploadBatch could not
// prepare (not eligible, missing information, already generated, ...) —
// surfaced to the caller instead of silently dropping it from the batch,
// same "never silently skip" posture as brief §53's import reports.
type BatchSkip struct {
	SalesDocumentID uuid.UUID
	Reason          string
}

// PrepareFreePortalUploadBatch is the bulk-selection flow
// docs/architecture.md §9b describes but Stage 8c's own pass left
// unbuilt ("the SplitBatch primitive exists and is tested, but no
// endpoint to select multiple invoices — no apps/web to hang it on
// yet"). Runs PrepareFreePortalUpload per document (this is why it must
// run inside a caller-provided RunScoped block, same requirement that
// method itself documents) — one ineligible or already-prepared document
// in the selection is recorded as a BatchSkip and the rest continue,
// never aborting the whole batch over one bad row. The successfully
// prepared documents' JSON content is then grouped into
// portal.MaxFileSizeBytes-bounded batch files via the existing,
// already-tested SplitBatch — this function is genuinely just the
// "loop + call the two already-built primitives" glue that was missing.
func (s *Service) PrepareFreePortalUploadBatch(ctx context.Context, orgID uuid.UUID, salesDocumentIDs []uuid.UUID) (batches []portal.PreparedFile, skipped []BatchSkip, err error) {
	var contents [][]byte
	for _, docID := range salesDocumentIDs {
		file, _, err := s.PrepareFreePortalUpload(ctx, orgID, docID)
		if err != nil {
			skipped = append(skipped, BatchSkip{SalesDocumentID: docID, Reason: err.Error()})
			continue
		}
		contents = append(contents, file.Content)
	}
	if len(contents) == 0 {
		return nil, skipped, nil
	}
	batches, err = portalv1.SplitBatch(contents, portalv1.MaxFileSizeBytes)
	if err != nil {
		return nil, skipped, fmt.Errorf("ewaybill: splitting batch: %w", err)
	}
	return batches, skipped, nil
}

// ManualResultParams is the universal fallback path (docs/architecture.md
// §9b: "manual entry MUST work solidly since it's the universal
// fallback") — a user directly types the government-generated EWB
// number/validity for the invoice they're looking at. No cross-document
// mismatch check applies here (unlike ImportAndVerifyResult) because
// there's no separately-sourced file/PDF whose own identifying fields
// could disagree with the target invoice — the user IS looking at the
// target invoice while typing.
type ManualResultParams struct {
	EWBNumber  string
	ValidFrom  time.Time
	ValidUntil time.Time
}

func (s *Service) RecordManualResult(ctx context.Context, orgID, salesDocumentID uuid.UUID, p ManualResultParams) (*domain.Record, error) {
	rec, err := s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
	if err != nil {
		return nil, fmt.Errorf("ewaybill: loading record: %w", err)
	}
	src := domain.SourceManualPortal
	if err := s.records.UpdateStatus(ctx, orgID, rec.ID, domain.StatusGenerated, domain.UpdateFields{
		EWBNumber: &p.EWBNumber, ValidFrom: &p.ValidFrom, ValidUntil: &p.ValidUntil, Source: &src,
	}); err != nil {
		return nil, fmt.Errorf("ewaybill: recording manual result: %w", err)
	}
	rec.Status, rec.EWBNumber, rec.ValidFrom, rec.ValidUntil, rec.Source = domain.StatusGenerated, &p.EWBNumber, &p.ValidFrom, &p.ValidUntil, &src
	s.recordAudit(ctx, orgID, "ewaybill.manual_result_recorded", "ewaybill_record", rec.ID,
		map[string]any{"sales_document_id": salesDocumentID, "ewb_number": p.EWBNumber})
	return rec, nil
}

// ImportedResultParams is the "Import Government File" path (docs/
// architecture.md §9b) — carries the imported result's OWN claimed
// identifying fields, which must match the target invoice's canonical
// snapshot before linking (§9b: "verified before linking, not trusted
// blindly").
type ImportedResultParams struct {
	ManualResultParams
	ClaimedInvoiceNumber string
	ClaimedInvoiceDate   time.Time
	ClaimedSupplierGSTIN string
	ClaimedDocumentType  string
}

// ImportAndVerifyResult rejects (domain.ErrResultMismatch) rather than
// links a result whose claimed identifying fields don't match the target
// invoice's own immutable snapshot — never auto-attaches a result that
// might belong to a different invoice.
func (s *Service) ImportAndVerifyResult(ctx context.Context, orgID, salesDocumentID uuid.UUID, p ImportedResultParams) (*domain.Record, error) {
	rec, err := s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
	if err != nil {
		return nil, fmt.Errorf("ewaybill: loading record: %w", err)
	}
	var bill canonical.CanonicalEWayBill
	if err := json.Unmarshal(rec.CanonicalSnapshot, &bill); err != nil {
		return nil, fmt.Errorf("ewaybill: unmarshaling stored canonical snapshot: %w", err)
	}
	if p.ClaimedInvoiceNumber != bill.InvoiceNumber ||
		!p.ClaimedInvoiceDate.Equal(bill.InvoiceDate) ||
		p.ClaimedSupplierGSTIN != bill.Supplier.GSTIN ||
		p.ClaimedDocumentType != bill.DocumentType {
		s.recordAudit(ctx, orgID, "ewaybill.import_result_mismatch_rejected", "ewaybill_record", rec.ID,
			map[string]any{"sales_document_id": salesDocumentID, "claimed_invoice_number": p.ClaimedInvoiceNumber})
		return nil, domain.ErrResultMismatch
	}
	src := domain.SourceImportedFile
	if err := s.records.UpdateStatus(ctx, orgID, rec.ID, domain.StatusGenerated, domain.UpdateFields{
		EWBNumber: &p.EWBNumber, ValidFrom: &p.ValidFrom, ValidUntil: &p.ValidUntil, Source: &src,
	}); err != nil {
		return nil, fmt.Errorf("ewaybill: recording imported result: %w", err)
	}
	rec.Status, rec.EWBNumber, rec.ValidFrom, rec.ValidUntil, rec.Source = domain.StatusGenerated, &p.EWBNumber, &p.ValidFrom, &p.ValidUntil, &src
	s.recordAudit(ctx, orgID, "ewaybill.import_result_linked", "ewaybill_record", rec.ID,
		map[string]any{"sales_document_id": salesDocumentID, "ewb_number": p.EWBNumber})
	return rec, nil
}

// getOrCreateFreePortalRecord returns the FREE_PORTAL-mode record for
// this document — reusing an existing one's stored snapshot if present,
// or building a fresh canonical.CanonicalEWayBill from the invoice's
// current finalized data and persisting it as a NEW record if not. A
// pre-existing AUTOMATIC_API-mode record for the same document (e.g. a
// FAILED_RETRYABLE row from a failed API attempt) is left untouched — a
// free-portal attempt always gets its own record, which is also exactly
// what makes the API-failure-to-free-portal fallback (§9b) "just work"
// with no special-case code: the same underlying invoice, a new row.
func (s *Service) getOrCreateFreePortalRecord(ctx context.Context, orgID, salesDocumentID uuid.UUID) (*domain.Record, canonical.CanonicalEWayBill, error) {
	existing, err := s.records.GetBySalesDocumentID(ctx, orgID, salesDocumentID)
	if err != nil && err != domain.ErrNotFound {
		return nil, canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: loading existing record: %w", err)
	}
	if existing != nil && existing.Mode == domain.ModeFreePortal && len(existing.CanonicalSnapshot) > 0 {
		var bill canonical.CanonicalEWayBill
		if err := json.Unmarshal(existing.CanonicalSnapshot, &bill); err != nil {
			return nil, canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: unmarshaling stored canonical snapshot: %w", err)
		}
		return existing, bill, nil
	}

	bill, err := s.buildCanonicalFromLiveData(ctx, orgID, salesDocumentID)
	if err != nil {
		return nil, canonical.CanonicalEWayBill{}, err
	}
	snapshot, err := json.Marshal(bill)
	if err != nil {
		return nil, canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: marshaling canonical snapshot: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: generating record id: %w", err)
	}
	rec := &domain.Record{
		ID: id, OrganisationID: orgID, SalesDocumentID: salesDocumentID,
		Status: domain.StatusNotRequired, Mode: domain.ModeFreePortal,
		VehicleNumber: bill.Transport.VehicleNumber, TransporterID: bill.Transport.TransporterID,
		TransporterName: bill.Transport.TransporterName,
	}
	if err := s.records.Create(ctx, rec); err != nil {
		return nil, canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: creating free-portal record: %w", err)
	}
	if err := s.records.UpdateStatus(ctx, orgID, rec.ID, domain.StatusNotRequired, domain.UpdateFields{CanonicalSnapshot: snapshot}); err != nil {
		return nil, canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: persisting canonical snapshot: %w", err)
	}
	rec.CanonicalSnapshot = snapshot
	return rec, bill, nil
}

// buildCanonicalFromLiveData assembles a CanonicalEWayBill from the
// invoice's OWN finalized, immutable snapshot data (sales_documents +
// tax_documents/tax_lines, both frozen at finalization per brief §55) and
// current identity master data (legal entity, customer party, addresses)
// — this is the ONE place live master data is ever read for this
// purpose, and only on first capture (see getOrCreateFreePortalRecord's
// doc comment); every subsequent regeneration re-serializes the stored
// snapshot instead of calling this again.
func (s *Service) buildCanonicalFromLiveData(ctx context.Context, orgID, salesDocumentID uuid.UUID) (canonical.CanonicalEWayBill, error) {
	if s.sales == nil || s.organisation == nil || s.contacts == nil || s.taxation == nil {
		return canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: FREE_PORTAL mode dependencies not configured — call WithFreePortal first")
	}
	doc, lines, err := s.sales.GetDocumentForOtherModule(ctx, orgID, salesDocumentID)
	if err != nil {
		return canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: loading sales document: %w", err)
	}
	if doc.TaxDocumentID == nil {
		return canonical.CanonicalEWayBill{}, fmt.Errorf("sales document %s has no tax snapshot (not finalized?)", salesDocumentID)
	}

	legalEntity, err := s.organisation.GetLegalEntityForOtherModule(ctx, orgID, doc.LegalEntityID)
	if err != nil {
		return canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: loading supplier legal entity: %w", err)
	}
	supplier := canonical.Party{LegalName: legalEntity.LegalName, GSTIN: legalEntity.GSTIN, StateCode: legalEntity.GSTStateCode}

	recipient := canonical.Party{}
	if doc.CustomerTaxRegistrationID != nil {
		reg, err := s.contacts.GetTaxRegistrationForOtherModule(ctx, orgID, *doc.CustomerTaxRegistrationID)
		if err == nil && reg != nil {
			recipient.GSTIN, recipient.StateCode = reg.RegistrationNumber, reg.StateCode
		}
	}
	if party, err := s.contacts.GetPartyForOtherModule(ctx, orgID, doc.CustomerPartyID); err == nil && party != nil {
		recipient.LegalName, recipient.TradeName, recipient.Phone = party.LegalName, party.TradeName, party.Phone
	}
	shipTo := recipient
	if doc.ShippingAddressID != nil {
		if addr, err := s.contacts.GetAddressForOtherModule(ctx, orgID, *doc.ShippingAddressID); err == nil && addr != nil {
			shipTo.AddressLine1, shipTo.AddressLine2, shipTo.City, shipTo.PostalCode = addr.Line1, addr.Line2, addr.City, addr.PostalCode
			if addr.State != "" {
				shipTo.StateCode = addr.State
			}
		}
	}

	taxDoc, taxLines, componentsByLine, err := s.taxation.GetByReference(ctx, orgID, "sales_document", doc.ID)
	if err != nil {
		return canonical.CanonicalEWayBill{}, fmt.Errorf("ewaybill: loading tax snapshot: %w", err)
	}
	taxLineByRef := make(map[string]*taxdomainLineWithComponents, len(taxLines))
	var totalCGST, totalSGST, totalIGST, totalCESS decimal.Decimal
	for _, tl := range taxLines {
		// cgstRate/sgstRate/igstRate are tracked SEPARATELY, not summed
		// into one combined rate — the official e-Way Bill portal's
		// itemList schema requires the per-component split (cgstRate/
		// sgstRate/igstRate/cessRate), not a single "gst_rate" figure.
		// gstRate (combined) is still derived below for callers that only
		// want a display total.
		var cgstRate, sgstRate, igstRate, cessRate decimal.Decimal
		for _, c := range componentsByLine[tl.ID] {
			switch c.ComponentType {
			case "CESS":
				cessRate = cessRate.Add(c.Rate)
				totalCESS = totalCESS.Add(c.Amount.Decimal())
			case "CGST":
				cgstRate = cgstRate.Add(c.Rate)
				totalCGST = totalCGST.Add(c.Amount.Decimal())
			case "SGST", "UTGST":
				sgstRate = sgstRate.Add(c.Rate)
				totalSGST = totalSGST.Add(c.Amount.Decimal())
			case "IGST":
				igstRate = igstRate.Add(c.Rate)
				totalIGST = totalIGST.Add(c.Amount.Decimal())
			}
		}
		gstRate := cgstRate.Add(sgstRate).Add(igstRate)
		taxLineByRef[tl.LineRef] = &taxdomainLineWithComponents{
			line: tl, gstRate: gstRate, cgstRate: cgstRate, sgstRate: sgstRate, igstRate: igstRate, cessRate: cessRate,
		}
	}

	items := make([]canonical.Item, 0, len(lines))
	for _, l := range lines {
		ref := fmt.Sprintf("%d", l.LineNumber)
		tl, ok := taxLineByRef[ref]
		if !ok {
			return canonical.CanonicalEWayBill{}, fmt.Errorf("no tax line found for sales document line %d", l.LineNumber)
		}
		items = append(items, canonical.Item{
			LineRef: ref, HSNSACCode: l.HSNSACCode, Quantity: l.Quantity,
			TaxableAmount: tl.line.TaxableAmount.Decimal(), GSTRate: tl.gstRate,
			CGSTRate: tl.cgstRate, SGSTRate: tl.sgstRate, IGSTRate: tl.igstRate, CessRate: tl.cessRate,
		})
	}

	docType := "INV"
	switch doc.DocumentType {
	case "CREDIT_NOTE":
		docType = "CRN"
	case "DEBIT_NOTE":
		docType = "DBN"
	}

	return canonical.Build(canonical.BuildInput{
		SalesDocumentID: doc.ID, InvoiceNumber: doc.DocumentNumber, InvoiceDate: doc.IssueDate,
		DocumentType: docType, SupplyPlaceCode: doc.PlaceOfSupplyStateCode,
		Supplier: supplier, Recipient: recipient, DispatchFrom: supplier, ShipTo: shipTo,
		Items: items,
		Tax: canonical.TaxTotals{
			TaxableValue: taxDoc.TotalTaxableAmount.Decimal(), CGST: totalCGST, SGST: totalSGST, IGST: totalIGST, CESS: totalCESS,
			GrandTotal: taxDoc.TotalTaxableAmount.Decimal().Add(taxDoc.TotalTaxAmount.Decimal()),
		},
		TransportMode: doc.Transporter, VehicleNumber: doc.VehicleNumber,
		DistanceKM: decimal.Zero,
	}), nil
}

type taxdomainLineWithComponents struct {
	line     *taxdomain.TaxLine
	gstRate  decimal.Decimal
	cgstRate decimal.Decimal
	sgstRate decimal.Decimal
	igstRate decimal.Decimal
	cessRate decimal.Decimal
}
