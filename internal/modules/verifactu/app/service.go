// Package app is the VERI*FACTU use-case layer: it appends invoice records to a company's hash chain inside the
// same transaction that finalizes or cancels an invoice, and sends them to AEAT from the background worker.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/modules/verifactu/aeat"
	"rechvix/internal/modules/verifactu/domain"
	"rechvix/internal/modules/verifactu/pg"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

// EventTypeSubmit is the outbox event the worker handles to send pending records to AEAT.
const EventTypeSubmit = "verifactu.submit"

// batchSize is how many records go in one request (AEAT accepts up to 1000).
const batchSize = 100

// Producer identifies the software producer (NODEDR) in every record's SistemaInformatico block. The identifier is a
// legal fact only the producer can supply, so it comes from configuration and enabling is refused while it is missing.
type Producer struct {
	Name     string
	Country  string // ISO 3166-1 alpha-2 of the producer's tax residence
	IDType   string // 02 VAT no., 03 passport, 04 official ID of country of residence, 05, 06, 07
	ID       string // tax identifier (NIF when Country is ES)
	SystemID string // two characters
	Version  string
}

// ProducerFromEnv reads the producer identity from the environment. The name defaults to NODEDR INFOTECH PRIVATE
// LIMITED; the identifier has no default because it is a legal fact only the producer can supply.
func ProducerFromEnv(getenv func(string) string, version string) Producer {
	def := func(k, d string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return d
	}
	return Producer{
		Name:     def("VERIFACTU_PRODUCER_NAME", "NODEDR INFOTECH PRIVATE LIMITED"),
		Country:  def("VERIFACTU_PRODUCER_COUNTRY", "IN"),
		IDType:   def("VERIFACTU_PRODUCER_ID_TYPE", "04"),
		ID:       def("VERIFACTU_PRODUCER_ID", ""),
		SystemID: def("VERIFACTU_SYSTEM_ID", "RX"),
		Version:  version,
	}
}

// Configured reports whether the producer block can be written.
func (p Producer) Configured() bool {
	return strings.TrimSpace(p.Name) != "" && strings.TrimSpace(p.ID) != "" && len(p.SystemID) == 2
}

func (p Producer) system(installation string) domain.System {
	s := domain.System{ProducerName: p.Name, SystemName: "Rechvix", SystemID: p.SystemID, Version: p.Version,
		InstallationNumber: installation, OnlyVeriFactu: true}
	if strings.EqualFold(p.Country, "ES") {
		s.ProducerNIF = p.ID
	} else {
		s.ProducerOther = &domain.IDOther{CountryCode: strings.ToUpper(p.Country), IDType: p.IDType, ID: p.ID}
	}
	return s
}

type Service struct {
	pool        database.Runner
	repo        *pg.Repo
	aead        *appcrypto.AEAD
	outbox      outbox.Writer
	org         *orgapp.Service
	producer    Producer
	permissions *permissions.Checker
	audit       audit.Recorder
	logger      *slog.Logger
	now         func() time.Time
	// transport, when set, replaces certificate loading and the AEAT call (tests only).
	transport func(ctx context.Context, env domain.Environment, body string) (*aeat.Response, error)
	// sender is the real AEAT call.
	sender func(ctx context.Context, env domain.Environment, cert tls.Certificate, body string) (*aeat.Response, error)
}

func NewService(pool database.Runner, repo *pg.Repo, aead *appcrypto.AEAD, ob outbox.Writer, org *orgapp.Service,
	producer Producer, checker *permissions.Checker, recorder audit.Recorder, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{pool: pool, repo: repo, aead: aead, outbox: ob, org: org, producer: producer, permissions: checker, audit: recorder, logger: logger, now: time.Now}
	s.sender = defaultSender
	return s
}

// WithTransport replaces the AEAT call, and the certificate it needs, with fn. It exists so tests can exercise the
// submission flow without a real certificate or network access.
func (s *Service) WithTransport(fn func(ctx context.Context, env domain.Environment, body string) (*aeat.Response, error)) *Service {
	s.transport = fn
	return s
}

// Issued is what the sales module needs back after an invoice has been recorded.
type Issued struct {
	RecordID uuid.UUID
	Seq      int64
	Hash     string
	QRURL    string
}

// IssueRequest is everything needed to build the record for a finalized sales document.
type IssueRequest struct {
	OrganisationID  uuid.UUID
	LegalEntityID   uuid.UUID
	SalesDocumentID uuid.UUID
	IssuerName      string
	IssuerNIF       string
	CountryCode     string
	DocumentType    string // TAX_INVOICE, POS_INVOICE, CREDIT_NOTE, SALES_RETURN, ...
	Number          string
	IssueDate       time.Time
	CurrencyCode    string
	CustomerName    string
	CustomerTaxID   string
	CustomerCountry string
	// For a credit note or return: the invoice it corrects.
	ReferenceNumber string
	ReferenceDate   *time.Time
	Lines           []domain.TaxLine
	GrandTotal      decimal.Decimal
}

// CancelRequest identifies an invoice whose cancellation must be recorded.
type CancelRequest struct {
	OrganisationID  uuid.UUID
	LegalEntityID   uuid.UUID
	SalesDocumentID uuid.UUID
	IssuerNIF       string
	Number          string
	IssueDate       time.Time
}

func isInvoiceType(t string) bool {
	switch t {
	case "TAX_INVOICE", "POS_INVOICE", "CREDIT_NOTE", "SALES_RETURN", "DEBIT_NOTE", "RECURRING_INVOICE":
		return true
	}
	return false
}

func (s *Service) enabled(ctx context.Context, legalEntityID uuid.UUID) (*pg.Settings, error) {
	st, err := s.repo.GetSettings(ctx, legalEntityID)
	if errors.Is(err, pg.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Enabled {
		return nil, nil
	}
	// Spain only: a company that is (or has since been moved to) another country never records, even if the
	// switch was left on.
	le, err := s.org.GetLegalEntityForOtherModule(ctx, st.OrganisationID, legalEntityID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(le.CountryCode, "ES") {
		return nil, nil
	}
	return st, nil
}

// RecordIssued appends the invoice record for a document being finalized. It returns (nil, nil) when VERI*FACTU
// is not enabled for the company or the document is not an invoice. It must run inside the finalizing transaction.
func (s *Service) RecordIssued(ctx context.Context, req IssueRequest) (*Issued, error) {
	st, err := s.enabled(ctx, req.LegalEntityID)
	if err != nil || st == nil {
		return nil, err
	}
	if !isInvoiceType(req.DocumentType) {
		return nil, nil
	}
	var invType, rectType string
	sign := int64(1)
	var recipients []domain.Party
	switch req.DocumentType {
	case "TAX_INVOICE":
		invType = "F1"
	case "POS_INVOICE":
		invType = "F2"
	case "CREDIT_NOTE", "SALES_RETURN":
		invType, rectType, sign = "R1", "I", -1
	default:
		return nil, fmt.Errorf("%w (%s)", domain.ErrUnsupportedType, req.DocumentType)
	}
	if !strings.EqualFold(req.CurrencyCode, "EUR") {
		return nil, domain.ErrNeedEUR
	}
	if id := strings.TrimSpace(req.CustomerTaxID); id != "" {
		p := domain.Party{Name: req.CustomerName}
		if cc := strings.ToUpper(req.CustomerCountry); cc == "" || cc == "ES" {
			p.NIF = strings.ToUpper(strings.ReplaceAll(id, " ", ""))
		} else {
			p.IDOther = &domain.IDOther{CountryCode: cc, IDType: "02", ID: id}
		}
		recipients = append(recipients, p)
	} else if invType == "F1" {
		// A complete invoice needs an identified recipient; without a customer tax ID it is a simplified invoice.
		invType = "F2"
	}
	if invType == "F2" {
		recipients = nil
	}

	breakdown, taxTotal, err := domain.BuildBreakdown(req.Lines, sign)
	if err != nil {
		return nil, err
	}
	total := req.GrandTotal.Mul(decimal.NewFromInt(sign))

	head, err := s.repo.LockHead(ctx, req.OrganisationID, req.LegalEntityID)
	if err != nil {
		return nil, err
	}
	var prev *domain.ChainRef
	if head.Seq > 0 && head.IssueDate != nil {
		prev = &domain.ChainRef{IssuerNIF: req.IssuerNIF, Number: head.Number, IssueDate: *head.IssueDate, Hash: head.Hash}
	}
	loc := domain.MadridLocation()
	alta := domain.Alta{
		ID:          domain.InvoiceID{IssuerNIF: strings.ToUpper(strings.TrimSpace(req.IssuerNIF)), Number: strings.TrimSpace(req.Number), IssueDate: req.IssueDate},
		IssuerName:  req.IssuerName,
		InvoiceType: invType, RectType: rectType,
		Description: describe(req.DocumentType),
		Recipients:  recipients, Breakdown: breakdown, TaxTotal: taxTotal, InvoiceTotal: total,
		Previous: prev, System: s.producer.system(st.InstallationNumber),
		GeneratedAt: s.now().In(loc),
	}
	if rectType != "" && req.ReferenceNumber != "" && req.ReferenceDate != nil {
		alta.Rectified = []domain.InvoiceID{{IssuerNIF: alta.ID.IssuerNIF, Number: req.ReferenceNumber, IssueDate: *req.ReferenceDate}}
	}
	if err := alta.Validate(); err != nil {
		return nil, err
	}
	env := domain.Environment(st.Environment)
	qr, err := domain.QRURL(env, alta.ID, alta.InvoiceTotal)
	if err != nil {
		return nil, err
	}
	rec, err := s.append(ctx, req.OrganisationID, req.LegalEntityID, head.Seq+1, domain.RecordAlta, &req.SalesDocumentID, alta.ID,
		&invType, &taxTotal, &total, prevHash(prev), alta.Hash(), alta.GeneratedAt, alta.XML(), &qr)
	if err != nil {
		return nil, err
	}
	return &Issued{RecordID: rec.ID, Seq: rec.Seq, Hash: rec.Hash, QRURL: qr}, nil
}

// RecordCancelled appends the cancellation record for an invoice that has a record in the chain.
func (s *Service) RecordCancelled(ctx context.Context, req CancelRequest) error {
	st, err := s.enabled(ctx, req.LegalEntityID)
	if err != nil || st == nil {
		return err
	}
	if _, err := s.repo.AltaForDocument(ctx, req.SalesDocumentID); err != nil {
		if errors.Is(err, pg.ErrNotFound) {
			return nil // issued before VERI*FACTU was switched on: nothing in the chain to cancel
		}
		return err
	}
	if done, err := s.repo.HasCancellation(ctx, req.SalesDocumentID); err != nil || done {
		return err
	}
	head, err := s.repo.LockHead(ctx, req.OrganisationID, req.LegalEntityID)
	if err != nil {
		return err
	}
	var prev *domain.ChainRef
	if head.Seq > 0 && head.IssueDate != nil {
		prev = &domain.ChainRef{IssuerNIF: req.IssuerNIF, Number: head.Number, IssueDate: *head.IssueDate, Hash: head.Hash}
	}
	c := domain.Cancel{
		ID:       domain.InvoiceID{IssuerNIF: strings.ToUpper(strings.TrimSpace(req.IssuerNIF)), Number: strings.TrimSpace(req.Number), IssueDate: req.IssueDate},
		Previous: prev, System: s.producer.system(st.InstallationNumber), GeneratedAt: s.now().In(domain.MadridLocation()),
	}
	_, err = s.append(ctx, req.OrganisationID, req.LegalEntityID, head.Seq+1, domain.RecordAnulacion, &req.SalesDocumentID, c.ID,
		nil, nil, nil, prevHash(prev), c.Hash(), c.GeneratedAt, c.XML(), nil)
	return err
}

func prevHash(p *domain.ChainRef) string {
	if p == nil {
		return ""
	}
	return p.Hash
}

func describe(docType string) string {
	switch docType {
	case "CREDIT_NOTE":
		return "Rectificación de factura (abono)"
	case "SALES_RETURN":
		return "Devolución de mercancías"
	default:
		return "Venta de bienes y prestación de servicios"
	}
}

func (s *Service) append(ctx context.Context, orgID, entityID uuid.UUID, seq int64, typ string, docID *uuid.UUID, id domain.InvoiceID,
	invType *string, tax, total *decimal.Decimal, prev, hash string, at time.Time, xml string, qr *string) (*pg.Record, error) {
	rid, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	rec := &pg.Record{ID: rid, OrganisationID: orgID, LegalEntityID: entityID, Seq: seq, Type: typ, SalesDocumentID: docID,
		IssuerNIF: id.IssuerNIF, InvoiceNumber: id.Number, IssueDate: id.IssueDate, InvoiceType: invType, TaxTotal: tax, InvoiceTotal: total,
		PrevHash: prev, Hash: hash, GeneratedAtText: at.Format(domain.TimestampLayout), XML: xml, QRURL: qr}
	if err := s.repo.Append(ctx, rec); err != nil {
		return nil, err
	}
	if s.outbox != nil {
		key := fmt.Sprintf("verifactu:submit:%s:%d", entityID, seq)
		if err := s.outbox.Enqueue(ctx, orgID, EventTypeSubmit, key, map[string]any{"legal_entity_id": entityID}); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

// QRForDocument returns the QR link and legends for a printed invoice, or nil when the document has no record.
type QRInfo struct {
	URL     string
	Heading string
	Legend  string
	Short   string
}

func (s *Service) QRForDocument(ctx context.Context, documentID uuid.UUID) (*QRInfo, error) {
	rec, err := s.repo.AltaForDocument(ctx, documentID)
	if errors.Is(err, pg.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rec.QRURL == nil {
		return nil, nil
	}
	return &QRInfo{URL: *rec.QRURL, Heading: domain.QRHeading, Legend: domain.QRVerifiable, Short: domain.QRShort}, nil
}
