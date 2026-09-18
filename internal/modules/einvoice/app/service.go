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
	sandboxprovider "rechvix/internal/modules/einvoice/v1/sandbox"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/outbox"

	contactsapp "rechvix/internal/modules/contacts/app"
	orgapp "rechvix/internal/modules/organisation/app"
	salesapp "rechvix/internal/modules/sales/app"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxdomain "rechvix/internal/modules/taxation/domain"
)

// ProviderNICSandboxV1 identifies the real, network-calling NIC e-Invoice
// adapter (internal/modules/einvoice/v1/sandbox) — both as the value
// stored in einvoice_provider_credentials.provider and the one
// buildEInvoiceProvider-equivalent resolveProvider below recognizes as
// "use these DB-stored credentials, not the env-var default".
const ProviderNICSandboxV1 = "nic-sandbox-v1"

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
	// credentials/aead are optional (nil until WithCredentialsStore is
	// called) — same nil-guarded pattern as outbox above. Until wired,
	// resolveProvider always falls back to the single env-var-configured
	// provider/providerName below, exactly the pre-existing behavior.
	credentials domain.CredentialsRepository
	aead        *appcrypto.AEAD
	now         func() time.Time
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

// WithCredentialsStore returns a copy of s with per-legal-entity encrypted
// provider credentials wired in (Settings screen support) — a separate
// step, not a NewService parameter, so every existing call site keeps
// compiling unchanged (same convention as ewaybill/app.Service.
// WithFreePortal).
func (s *Service) WithCredentialsStore(repo domain.CredentialsRepository, aead *appcrypto.AEAD) *Service {
	cp := *s
	cp.credentials, cp.aead = repo, aead
	return &cp
}

// SandboxCredentials is the plaintext shape saved/loaded for
// ProviderNICSandboxV1 — encrypted as JSON via AEAD before it ever
// touches the database, decrypted only inside resolveProvider (never
// returned from any HTTP handler; GetCredentialsStatus below returns a
// separate, deliberately-thin status type instead).
type SandboxCredentials struct {
	ClientID     string
	ClientSecret string
	GSTIN        string
	Username     string
	Password     string
	// BaseURL is optional — empty uses sandboxprovider.DefaultBaseURL
	// (NIC's actual sandbox host). Set explicitly to point at a real
	// production/GSP endpoint once an operator has real credentials for
	// one — the adapter itself doesn't care which host it's talking to.
	BaseURL string
}

// CredentialsStatus is what a Settings screen is allowed to see — never
// ClientSecret or Password. GSTIN/Username are already visible elsewhere
// on a real invoice/login screen, not secrets in the same sense.
type CredentialsStatus struct {
	Configured bool
	GSTIN      string
	Username   string
	BaseURL    string
}

// SaveSandboxCredentials encrypts and upserts a legal entity's
// ProviderNICSandboxV1 credentials. Requires WithCredentialsStore to have
// been called (returns an error otherwise — a nil credentials/aead pair
// is a composition-root wiring bug, not a normal runtime state).
func (s *Service) SaveSandboxCredentials(ctx context.Context, orgID, legalEntityID uuid.UUID, creds SandboxCredentials) error {
	if s.credentials == nil || s.aead == nil {
		return fmt.Errorf("einvoice: credential storage is not configured on this server")
	}
	// Same org-ownership check organisation/pg.LegalEntityRepo.
	// UpdateInvoiceBranding's own `WHERE organisation_id = $1 AND id = $2`
	// enforces directly — without it, nothing stops the caller's own org
	// id being paired with a legal_entity_id belonging to a different
	// organisation in the row this upserts.
	if _, err := s.organisation.GetLegalEntityForOtherModule(ctx, orgID, legalEntityID); err != nil {
		return fmt.Errorf("einvoice: loading legal entity: %w", err)
	}
	plaintext, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("einvoice: marshaling credentials: %w", err)
	}
	sealed, err := s.aead.Seal(plaintext, credentialsAAD(orgID, legalEntityID, ProviderNICSandboxV1))
	if err != nil {
		return fmt.Errorf("einvoice: encrypting credentials: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("einvoice: generating credentials id: %w", err)
	}
	return s.credentials.Upsert(ctx, &domain.ProviderCredentials{
		ID: id, OrganisationID: orgID, LegalEntityID: legalEntityID,
		Provider: ProviderNICSandboxV1, EncryptedCredentials: sealed,
	})
}

// GetCredentialsStatus reports whether NIC sandbox credentials are on file
// for a legal entity, without ever exposing the secret fields.
func (s *Service) GetCredentialsStatus(ctx context.Context, orgID, legalEntityID uuid.UUID) (CredentialsStatus, error) {
	if s.credentials == nil || s.aead == nil {
		return CredentialsStatus{}, nil
	}
	row, err := s.credentials.Get(ctx, orgID, legalEntityID, ProviderNICSandboxV1)
	if err == domain.ErrNotFound {
		return CredentialsStatus{}, nil
	}
	if err != nil {
		return CredentialsStatus{}, fmt.Errorf("einvoice: loading credentials: %w", err)
	}
	plaintext, err := s.aead.Open(row.EncryptedCredentials, credentialsAAD(orgID, legalEntityID, ProviderNICSandboxV1))
	if err != nil {
		return CredentialsStatus{}, fmt.Errorf("einvoice: decrypting credentials: %w", err)
	}
	var creds SandboxCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return CredentialsStatus{}, fmt.Errorf("einvoice: unmarshaling credentials: %w", err)
	}
	return CredentialsStatus{Configured: true, GSTIN: creds.GSTIN, Username: creds.Username, BaseURL: creds.BaseURL}, nil
}

// DeleteCredentials removes a legal entity's NIC sandbox credentials —
// resolveProvider then falls straight back to the env-var-configured
// default provider for that legal entity's future documents.
func (s *Service) DeleteCredentials(ctx context.Context, orgID, legalEntityID uuid.UUID) error {
	if s.credentials == nil {
		return fmt.Errorf("einvoice: credential storage is not configured on this server")
	}
	return s.credentials.Delete(ctx, orgID, legalEntityID, ProviderNICSandboxV1)
}

// resolveProvider picks which EInvoiceProvider a specific legal entity's
// document should use: DB-stored NIC sandbox credentials (Settings screen)
// take priority when present, falling back to the single env-var-
// configured provider (s.provider/s.providerName, apps/worker's
// buildEInvoiceProvider) every call already used before this existed —
// so a deployment that never touches the new Settings screen behaves
// exactly as before.
func (s *Service) resolveProvider(ctx context.Context, orgID, legalEntityID uuid.UUID) (domain.EInvoiceProvider, string, error) {
	if s.credentials == nil || s.aead == nil {
		return s.provider, s.providerName, nil
	}
	row, err := s.credentials.Get(ctx, orgID, legalEntityID, ProviderNICSandboxV1)
	if err == domain.ErrNotFound {
		return s.provider, s.providerName, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("einvoice: loading provider credentials: %w", err)
	}
	plaintext, err := s.aead.Open(row.EncryptedCredentials, credentialsAAD(orgID, legalEntityID, ProviderNICSandboxV1))
	if err != nil {
		return nil, "", fmt.Errorf("einvoice: decrypting provider credentials: %w", err)
	}
	var creds SandboxCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, "", fmt.Errorf("einvoice: unmarshaling provider credentials: %w", err)
	}
	return sandboxprovider.New(creds.BaseURL, sandboxprovider.Credentials{
		ClientID: creds.ClientID, ClientSecret: creds.ClientSecret, GSTIN: creds.GSTIN,
		Username: creds.Username, Password: creds.Password,
	}, nil), ProviderNICSandboxV1, nil
}

// credentialsAAD binds an encrypted credentials blob to the exact row it
// belongs to (same reasoning as backup/app.Service's header-binding use of
// AEAD.Seal's additionalData) — a ciphertext copied into a different
// legal_entity_id/provider row fails to decrypt instead of silently
// "working" against the wrong business's credentials.
func credentialsAAD(orgID, legalEntityID uuid.UUID, provider string) []byte {
	return []byte(orgID.String() + ":" + legalEntityID.String() + ":" + provider)
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
	if existing != nil && existing.Status.Terminal() {
		return nil // already handled — see idempotency note above
	}
	return s.generate(ctx, orgID, salesDocumentID, existing)
}

// RetryDocument re-attempts IRN generation for a document whose e-Invoice
// record is currently FAILED_FINAL or FAILED_RETRYABLE — an explicit,
// user-triggered action for exactly the case GenerateForDocument's own
// Terminal() guard above exists to prevent happening automatically. A
// FAILED_FINAL record (e.g. "legal entity has no GSTIN configured") is
// wrapped in outbox.Permanent specifically so the outbox poller never
// retries it — that's correct as long as the underlying problem is still
// unfixed, but once someone actually adds the missing GSTIN in Settings,
// there was previously no way to get an IRN for that already-finalized
// invoice at all short of a database edit. This is that path, called
// from an explicit "Retry" action, never from the outbox.
func (s *Service) RetryDocument(ctx context.Context, orgID, salesDocumentID uuid.UUID) error {
	existing, err := s.records.GetBySalesDocumentID(ctx, salesDocumentID)
	if err != nil {
		if err == domain.ErrNotFound {
			return fmt.Errorf("einvoice: %w: no e-Invoice record exists yet for this document", domain.ErrNotFound)
		}
		return fmt.Errorf("einvoice: loading existing record: %w", err)
	}
	if existing.OrganisationID != orgID {
		return domain.ErrNotFound
	}
	switch existing.Status {
	case domain.StatusFailedFinal, domain.StatusFailedRetryable:
		// only a failed record has anything to retry
	default:
		return fmt.Errorf("%w: currently %s", domain.ErrNotRetryable, existing.Status)
	}
	return s.generate(ctx, orgID, salesDocumentID, existing)
}

// generate is GenerateForDocument/RetryDocument's shared core: create the
// record row if this is the very first attempt (existing == nil), or
// reuse it if this is a retry (whether an automatic FAILED_RETRYABLE
// reprocess or an explicit RetryDocument call) — the UNIQUE constraint on
// einvoice_records.sales_document_id would reject a second Create
// either way, so this always writes onto the one row a document can ever
// have.
func (s *Service) generate(ctx context.Context, orgID, salesDocumentID uuid.UUID, existing *domain.Record) error {
	if existing == nil {
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

	req, legalEntityID, err := s.buildIRNRequest(ctx, orgID, salesDocumentID)
	if err != nil {
		msg := err.Error()
		_ = s.records.UpdateStatus(ctx, existing.ID, domain.StatusFailedFinal, domain.UpdateFields{ErrorMessage: &msg})
		s.enqueueWebhookEvent(ctx, orgID, "einvoice.failed", existing.ID, salesDocumentID, msg)
		// A malformed/unresolvable request (e.g. supplier has no GSTIN
		// configured) will never succeed on retry — permanent, not
		// retryable.
		return outbox.Permanent(fmt.Errorf("einvoice: building IRN request: %w", err))
	}

	provider, resolvedProviderName, err := s.resolveProvider(ctx, orgID, legalEntityID)
	if err != nil {
		msg := err.Error()
		_ = s.records.UpdateStatus(ctx, existing.ID, domain.StatusFailedRetryable, domain.UpdateFields{ErrorMessage: &msg})
		// Retryable, not Permanent: a decrypt/lookup failure here is
		// infrastructure trouble (e.g. AEAD_ENCRYPTION_KEY rotated
		// without re-saving credentials), not a fact about this
		// document — worth trying again, unlike a genuinely missing
		// GSTIN above.
		return fmt.Errorf("einvoice: resolving provider: %w", err)
	}

	if err := s.records.UpdateStatus(ctx, existing.ID, domain.StatusSubmitting, domain.UpdateFields{Provider: &resolvedProviderName}); err != nil {
		return fmt.Errorf("einvoice: marking submitting: %w", err)
	}

	resp, genErr := provider.GenerateIRN(ctx, req)
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
// buildIRNRequest's second return value is the document's legal entity ID
// — generate() needs it to resolveProvider before actually calling
// GenerateIRN, and every caller already loads the legal entity here
// anyway, so returning the id costs nothing extra.
func (s *Service) buildIRNRequest(ctx context.Context, orgID, salesDocumentID uuid.UUID) (domain.IRNRequest, uuid.UUID, error) {
	doc, lines, err := s.sales.GetDocumentForOtherModule(ctx, orgID, salesDocumentID)
	if err != nil {
		return domain.IRNRequest{}, uuid.Nil, fmt.Errorf("loading sales document: %w", err)
	}
	if doc.TaxDocumentID == nil {
		return domain.IRNRequest{}, uuid.Nil, fmt.Errorf("sales document %s has no tax snapshot (not finalized?)", salesDocumentID)
	}

	legalEntity, err := s.organisation.GetLegalEntityForOtherModule(ctx, orgID, doc.LegalEntityID)
	if err != nil {
		return domain.IRNRequest{}, uuid.Nil, fmt.Errorf("loading supplier legal entity: %w", err)
	}
	if legalEntity.GSTIN == "" {
		return domain.IRNRequest{}, uuid.Nil, fmt.Errorf("legal entity %s has no GSTIN configured", legalEntity.ID)
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
		return domain.IRNRequest{}, uuid.Nil, fmt.Errorf("loading tax snapshot: %w", err)
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
			return domain.IRNRequest{}, uuid.Nil, fmt.Errorf("no tax line found for sales document line %d", l.LineNumber)
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
	}, legalEntity.ID, nil
}

type taxLineWithComponents struct {
	line    *taxdomain.TaxLine
	gstRate decimal.Decimal
}

func unmarshalPayload(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}
