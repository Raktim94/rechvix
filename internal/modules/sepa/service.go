package sepa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid  = errors.New("sepa: invalid request")
	ErrNotFound = errors.New("sepa: not found")
)

type Settings struct {
	CreditorName string `json:"creditor_name"`
	CreditorID   string `json:"creditor_id"`
	IBAN         string `json:"iban"`
	BIC          string `json:"bic,omitempty"`
	Configured   bool   `json:"configured"`
}

type Mandate struct {
	ID            uuid.UUID `json:"id"`
	PartyID       uuid.UUID `json:"party_id"`
	Customer      string    `json:"customer"`
	Ref           string    `json:"mandate_ref"`
	SignedOn      string    `json:"signed_on"`
	IBANLast4     string    `json:"iban_last4"`
	BIC           string    `json:"bic,omitempty"`
	AccountHolder string    `json:"account_holder"`
	Status        string    `json:"status"`
	FirstUsed     bool      `json:"first_collected"`
}

type MandateInput struct {
	PartyID       uuid.UUID
	Ref           string
	SignedOn      time.Time
	IBAN          string
	BIC           string
	AccountHolder string
}

type Batch struct {
	ID             uuid.UUID  `json:"id"`
	MsgID          string     `json:"msg_id"`
	CollectionDate string     `json:"collection_date"`
	Total          string     `json:"total"`
	Count          int        `json:"count"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
}

type Collection struct {
	ID           uuid.UUID `json:"id"`
	BatchID      uuid.UUID `json:"batch_id"`
	DocumentID   uuid.UUID `json:"sales_document_id"`
	Invoice      string    `json:"invoice"`
	Customer     string    `json:"customer"`
	Amount       string    `json:"amount"`
	EndToEndID   string    `json:"end_to_end_id"`
	SequenceType string    `json:"sequence_type"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason,omitempty"`
}

type Service struct {
	pool        *database.Pool
	accounting  *accountingapp.Service
	aead        *appcrypto.AEAD
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(pool *database.Pool, acct *accountingapp.Service, aead *appcrypto.AEAD, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, accounting: acct, aead: aead, permissions: p, audit: a, now: time.Now}
}

func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "settings.manage", permissions.Scope{})
}
func (s *Service) post(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "accounting.post", permissions.Scope{})
}
func (s *Service) view(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "accounting.view", permissions.Scope{})
}

func aad(org uuid.UUID, ref string) []byte { return []byte(org.String() + ":mandate:" + ref) }

// ---- settings

func (s *Service) GetSettings(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID) (*Settings, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := &Settings{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var bic *string
		err := s.pool.Q(ctx).QueryRow(ctx, `SELECT creditor_name, creditor_id, iban, bic FROM sepa_settings WHERE legal_entity_id = $1`, legalEntityID).Scan(&out.CreditorName, &out.CreditorID, &out.IBAN, &bic)
		if err != nil {
			return nil
		}
		out.Configured = true
		if bic != nil {
			out.BIC = *bic
		}
		return nil
	})
	return out, err
}

func (s *Service) SaveSettings(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID, in Settings) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	iban, err := NormalizeIBAN(in.IBAN)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	bic, err := NormalizeBIC(in.BIC)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	cid, err := ValidateCreditorID(in.CreditorID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	name := Clean(in.CreditorName, 70)
	if name == "" {
		return fmt.Errorf("%w: the creditor name is required", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO sepa_settings (legal_entity_id, organisation_id, creditor_name, creditor_id, iban, bic) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
			ON CONFLICT (legal_entity_id) DO UPDATE SET creditor_name = EXCLUDED.creditor_name, creditor_id = EXCLUDED.creditor_id, iban = EXCLUDED.iban, bic = EXCLUDED.bic, updated_at = now()`,
			legalEntityID, principal.OrganisationID, name, cid, iban, bic); err != nil {
			return fmt.Errorf("%w: unknown company", ErrInvalid)
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sepa.settings_changed", EntityType: "legal_entity", EntityID: &legalEntityID, At: s.now()})
	})
}

// ---- mandates

func (s *Service) CreateMandate(ctx context.Context, principal permissions.Principal, in MandateInput) (*Mandate, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	iban, err := NormalizeIBAN(in.IBAN)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	bic, err := NormalizeBIC(in.BIC)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	holder := Clean(in.AccountHolder, 70)
	if holder == "" {
		return nil, fmt.Errorf("%w: the account holder's name is required", ErrInvalid)
	}
	if in.SignedOn.After(s.now().Add(24 * time.Hour)) {
		return nil, fmt.Errorf("%w: the signature date cannot be in the future", ErrInvalid)
	}
	id, _ := uuid.NewV7()
	ref := Clean(strings.TrimSpace(in.Ref), 35)
	if ref == "" {
		ref = "MND-" + strings.ToUpper(id.String()[24:32])
	}
	sealed, err := s.aead.Seal([]byte(iban), aad(principal.OrganisationID, ref))
	if err != nil {
		return nil, err
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM parties WHERE id = $1`, in.PartyID).Scan(&one); err != nil {
			return fmt.Errorf("%w: unknown customer", ErrInvalid)
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO sepa_mandates (id, organisation_id, party_id, mandate_ref, signed_on, iban_encrypted, iban_last4, bic, account_holder, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10)`, id, principal.OrganisationID, in.PartyID, ref, in.SignedOn, sealed, iban[len(iban)-4:], bic, holder, principal.UserID); err != nil {
			if strings.Contains(err.Error(), "23505") {
				return fmt.Errorf("%w: a mandate with that reference already exists", ErrInvalid)
			}
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sepa.mandate_created", EntityType: "sepa_mandate", EntityID: &id, AfterState: map[string]any{"party_id": in.PartyID.String(), "ref": ref, "iban_last4": iban[len(iban)-4:]}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	list, err := s.Mandates(ctx, principal, &in.PartyID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) Mandates(ctx context.Context, principal permissions.Principal, party *uuid.UUID) ([]Mandate, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Mandate{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT m.id, m.party_id, p.legal_name, m.mandate_ref, to_char(m.signed_on, 'YYYY-MM-DD'), m.iban_last4, COALESCE(m.bic, ''), m.account_holder, m.status, m.first_collected
			FROM sepa_mandates m JOIN parties p ON p.id = m.party_id WHERE m.organisation_id = $1 AND ($2::uuid IS NULL OR m.party_id = $2) ORDER BY m.created_at DESC`, principal.OrganisationID, party)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Mandate
			if err := rows.Scan(&m.ID, &m.PartyID, &m.Customer, &m.Ref, &m.SignedOn, &m.IBANLast4, &m.BIC, &m.AccountHolder, &m.Status, &m.FirstUsed); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) RevokeMandate(ctx context.Context, principal permissions.Principal, id uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE sepa_mandates SET status = 'REVOKED' WHERE id = $1 AND status = 'ACTIVE'`, id)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sepa.mandate_revoked", EntityType: "sepa_mandate", EntityID: &id, At: s.now()})
	})
}

// ---- batches

// earliest returns the first allowed collection date: two business days after today (a safe margin over the SEPA Core
// rule of one business day), skipping weekends. Bank holidays are not known here; the bank's own calendar still applies.
func earliest(now time.Time) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for added := 0; added < 2; {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			added++
		}
	}
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

type invoice struct {
	id          uuid.UUID
	number      string
	party       uuid.UUID
	customer    string
	outstanding decimal.Decimal
}

// CreateBatch builds a collection file for the chosen invoices. Every invoice must be a finalized euro invoice with money
// outstanding, its customer must have an active mandate, and none may already be in an open or collected direct debit.
func (s *Service) CreateBatch(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID, collectionDate time.Time, documentIDs []uuid.UUID) (*Batch, error) {
	if err := s.post(ctx, principal); err != nil {
		return nil, err
	}
	if len(documentIDs) == 0 || len(documentIDs) > 1000 {
		return nil, fmt.Errorf("%w: choose between 1 and 1000 invoices", ErrInvalid)
	}
	collectionDate = time.Date(collectionDate.Year(), collectionDate.Month(), collectionDate.Day(), 0, 0, 0, 0, time.UTC)
	if collectionDate.Before(earliest(s.now())) {
		return nil, fmt.Errorf("%w: the collection date must be at least two business days from today (%s or later)", ErrInvalid, earliest(s.now()).Format("2006-01-02"))
	}
	if collectionDate.Weekday() == time.Saturday || collectionDate.Weekday() == time.Sunday {
		return nil, fmt.Errorf("%w: the collection date must be a business day", ErrInvalid)
	}
	var batch *Batch
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var cred Creditor
		var bic *string
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT creditor_name, creditor_id, iban, bic FROM sepa_settings WHERE legal_entity_id = $1`, legalEntityID).Scan(&cred.Name, &cred.CreditorID, &cred.IBAN, &bic); err != nil {
			return fmt.Errorf("%w: enter the creditor details (SEPA creditor identifier and IBAN) first", ErrInvalid)
		}
		if bic != nil {
			cred.BIC = *bic
		}
		id, _ := uuid.NewV7()
		msgID := "RX" + s.now().UTC().Format("20060102150405") + strings.ToUpper(id.String()[28:32])
		var items []DebitItem
		type pending struct {
			doc, mandate uuid.UUID
			amount       decimal.Decimal
			e2e, seq     string
		}
		var rows []pending
		seen := map[uuid.UUID]bool{}
		for _, did := range documentIDs {
			if seen[did] {
				continue
			}
			seen[did] = true
			var inv invoice
			var status, docType, cur, base string
			var le uuid.UUID
			var total, paid, credited decimal.Decimal
			err := s.pool.Q(ctx).QueryRow(ctx, `
				SELECT d.id, d.document_number, d.customer_party_id, p.legal_name, d.status, d.document_type, d.currency_code, d.base_currency_code, d.legal_entity_id, COALESCE(d.grand_total_amount, 0),
				       COALESCE((SELECT SUM(r.amount) FROM receipts r WHERE r.sales_document_id = d.id), 0),
				       COALESCE((SELECT SUM(COALESCE(c.grand_total_amount, 0)) FROM sales_documents c WHERE c.reference_document_id = d.id AND c.status = 'FINALIZED' AND c.document_type IN ('CREDIT_NOTE', 'SALES_RETURN')), 0)
				FROM sales_documents d JOIN parties p ON p.id = d.customer_party_id WHERE d.id = $1`, did).
				Scan(&inv.id, &inv.number, &inv.party, &inv.customer, &status, &docType, &cur, &base, &le, &total, &paid, &credited)
			if err != nil {
				return fmt.Errorf("%w: unknown invoice", ErrInvalid)
			}
			if le != legalEntityID || status != "FINALIZED" || (docType != "TAX_INVOICE" && docType != "POS_INVOICE" && docType != "DEBIT_NOTE") {
				return fmt.Errorf("%w: %s is not a finalized invoice of this company", ErrInvalid, inv.number)
			}
			if !strings.EqualFold(cur, "EUR") || !strings.EqualFold(base, "EUR") {
				return fmt.Errorf("%w: %s is not in euros; SEPA direct debit only collects euros", ErrInvalid, inv.number)
			}
			inv.outstanding = total.Sub(paid).Sub(credited).Round(2)
			if !inv.outstanding.IsPositive() {
				return fmt.Errorf("%w: nothing is outstanding on %s", ErrInvalid, inv.number)
			}
			var mid uuid.UUID
			var mref, holder string
			var signed time.Time
			var sealed []byte
			var mbic *string
			var first bool
			if err := s.pool.Q(ctx).QueryRow(ctx, `
				SELECT id, mandate_ref, signed_on, iban_encrypted, bic, account_holder, first_collected FROM sepa_mandates
				WHERE organisation_id = $1 AND party_id = $2 AND status = 'ACTIVE' ORDER BY created_at DESC LIMIT 1`, principal.OrganisationID, inv.party).
				Scan(&mid, &mref, &signed, &sealed, &mbic, &holder, &first); err != nil {
				return fmt.Errorf("%w: %s has no active direct-debit mandate (customer %s)", ErrInvalid, inv.number, inv.customer)
			}
			var open int
			if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM sepa_collections WHERE sales_document_id = $1 AND status IN ('PENDING', 'COLLECTED')`, did).Scan(&open); err != nil {
				return err
			}
			if open > 0 {
				return fmt.Errorf("%w: %s is already in a direct debit", ErrInvalid, inv.number)
			}
			ib, err := s.aead.Open(sealed, aad(principal.OrganisationID, mref))
			if err != nil {
				return fmt.Errorf("sepa: a stored IBAN cannot be read (was AEAD_ENCRYPTION_KEY changed?): %w", err)
			}
			seq := "RCUR"
			if !first {
				seq = "FRST"
			}
			// A mandate that already sits in an unsent draft with FRST keeps FRST for the other drafts too; it advances on "sent".
			e2e := Clean("INV-"+inv.number, 35)
			bicv := ""
			if mbic != nil {
				bicv = *mbic
			}
			items = append(items, DebitItem{EndToEndID: e2e, Amount: inv.outstanding, MandateRef: mref, MandateDate: signed, DebtorName: holder, DebtorIBAN: string(ib), DebtorBIC: bicv, Remittance: "Invoice " + inv.number, SequenceType: seq})
			rows = append(rows, pending{doc: did, mandate: mid, amount: inv.outstanding, e2e: e2e, seq: seq})
		}
		xml, total, err := BuildFile(cred, msgID, s.now(), collectionDate, items)
		if err != nil {
			return err
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO sepa_batches (id, organisation_id, legal_entity_id, msg_id, collection_date, total, item_count, xml, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, principal.OrganisationID, legalEntityID, msgID, collectionDate, total, len(items), xml, principal.UserID); err != nil {
			return err
		}
		for _, r := range rows {
			cid, _ := uuid.NewV7()
			if _, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO sepa_collections (id, organisation_id, batch_id, mandate_id, sales_document_id, amount, end_to_end_id, sequence_type) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				cid, principal.OrganisationID, id, r.mandate, r.doc, r.amount, r.e2e, r.seq); err != nil {
				if strings.Contains(err.Error(), "23505") {
					return fmt.Errorf("%w: an invoice is already in a direct debit", ErrInvalid)
				}
				return err
			}
		}
		batch = &Batch{ID: id, MsgID: msgID, CollectionDate: collectionDate.Format("2006-01-02"), Total: total.StringFixed(2), Count: len(items), Status: "DRAFT", CreatedAt: s.now()}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sepa.batch_created", EntityType: "sepa_batch", EntityID: &id, AfterState: map[string]any{"msg_id": msgID, "count": len(items), "total": total.StringFixed(2)}, At: s.now()})
	})
	return batch, err
}

func (s *Service) Batches(ctx context.Context, principal permissions.Principal) ([]Batch, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Batch{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, msg_id, to_char(collection_date, 'YYYY-MM-DD'), total::text, item_count, status, created_at, sent_at FROM sepa_batches WHERE organisation_id = $1 ORDER BY created_at DESC LIMIT 100`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b Batch
			if err := rows.Scan(&b.ID, &b.MsgID, &b.CollectionDate, &b.Total, &b.Count, &b.Status, &b.CreatedAt, &b.SentAt); err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

// XML returns the collection file for download.
func (s *Service) XML(ctx context.Context, principal permissions.Principal, batchID uuid.UUID) (string, string, error) {
	if err := s.post(ctx, principal); err != nil {
		return "", "", err
	}
	var xml, msg string
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT xml, msg_id FROM sepa_batches WHERE id = $1`, batchID).Scan(&xml, &msg)
	})
	if err != nil {
		return "", "", ErrNotFound
	}
	return xml, msg + ".xml", nil
}

func (s *Service) Collections(ctx context.Context, principal permissions.Principal, batchID uuid.UUID) ([]Collection, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	out := []Collection{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT c.id, c.batch_id, c.sales_document_id, d.document_number, p.legal_name, c.amount::text, c.end_to_end_id, c.sequence_type, c.status, COALESCE(c.reason, '')
			FROM sepa_collections c JOIN sales_documents d ON d.id = c.sales_document_id JOIN parties p ON p.id = d.customer_party_id
			WHERE c.batch_id = $1 ORDER BY d.document_number`, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c Collection
			if err := rows.Scan(&c.ID, &c.BatchID, &c.DocumentID, &c.Invoice, &c.Customer, &c.Amount, &c.EndToEndID, &c.SequenceType, &c.Status, &c.Reason); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// MarkSent records that the file was handed to the bank; from now on the customers' mandates count as used once.
func (s *Service) MarkSent(ctx context.Context, principal permissions.Principal, batchID uuid.UUID) error {
	if err := s.post(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE sepa_batches SET status = 'SENT', sent_at = now() WHERE id = $1 AND status = 'DRAFT'`, batchID)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `UPDATE sepa_mandates SET first_collected = true WHERE id IN (SELECT mandate_id FROM sepa_collections WHERE batch_id = $1)`, batchID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "sepa.batch_sent", EntityType: "sepa_batch", EntityID: &batchID, At: s.now()})
	})
}

// Discard removes a draft batch that was never sent, releasing its invoices.
func (s *Service) Discard(ctx context.Context, principal permissions.Principal, batchID uuid.UUID) error {
	if err := s.post(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var status string
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT status FROM sepa_batches WHERE id = $1`, batchID).Scan(&status); err != nil {
			return ErrNotFound
		}
		if status != "DRAFT" {
			return fmt.Errorf("%w: a batch already sent to the bank cannot be discarded", ErrInvalid)
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM sepa_collections WHERE batch_id = $1`, batchID); err != nil {
			return err
		}
		_, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM sepa_batches WHERE id = $1`, batchID)
		return err
	})
}

// Result records what the bank reported for one collection: collected (a receipt is booked against the invoice) or rejected.
func (s *Service) Result(ctx context.Context, principal permissions.Principal, collectionID uuid.UUID, collected bool, reason string) error {
	if err := s.post(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var doc, party uuid.UUID
		var amount decimal.Decimal
		var e2e, status, batchStatus string
		if err := s.pool.Q(ctx).QueryRow(ctx, `
			SELECT c.sales_document_id, d.customer_party_id, c.amount, c.end_to_end_id, c.status, b.status
			FROM sepa_collections c JOIN sales_documents d ON d.id = c.sales_document_id JOIN sepa_batches b ON b.id = c.batch_id WHERE c.id = $1`, collectionID).
			Scan(&doc, &party, &amount, &e2e, &status, &batchStatus); err != nil {
			return ErrNotFound
		}
		if batchStatus != "SENT" {
			return fmt.Errorf("%w: mark the batch as sent to the bank first", ErrInvalid)
		}
		if status != "PENDING" {
			return fmt.Errorf("%w: this collection already has a result", ErrInvalid)
		}
		if !collected {
			_, err := s.pool.Q(ctx).Exec(ctx, `UPDATE sepa_collections SET status = 'REJECTED', reason = NULLIF($2, ''), updated_at = now() WHERE id = $1`, collectionID, Clean(reason, 200))
			return err
		}
		rec, err := s.accounting.RecordReceipt(ctx, principal, accountingapp.RecordReceiptParams{PartyID: party, SalesDocumentID: &doc, Amount: amount, Method: accountingdomain.MethodBankTransfer, ReferenceNumber: "SDD " + e2e, ReceivedAt: s.now()})
		if err != nil {
			return err
		}
		_, err = s.pool.Q(ctx).Exec(ctx, `UPDATE sepa_collections SET status = 'COLLECTED', receipt_id = $2, updated_at = now() WHERE id = $1`, collectionID, rec.ID)
		return err
	})
}
