// Package pg persists the VERI*FACTU record chain, per-company settings and submission progress.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"rechvix/internal/platform/database"
)

type Repo struct{ pool *database.Pool }

func NewRepo(pool *database.Pool) *Repo { return &Repo{pool: pool} }

var ErrNotFound = errors.New("verifactu: not found")

type Settings struct {
	LegalEntityID      uuid.UUID
	OrganisationID     uuid.UUID
	Enabled            bool
	Environment        string
	InstallationNumber string
	CertEncrypted      []byte
	CertPassword       []byte
	CertSubject        *string
	CertNotAfter       *time.Time
	UpdatedAt          time.Time
}

func (r *Repo) GetSettings(ctx context.Context, legalEntityID uuid.UUID) (*Settings, error) {
	var s Settings
	err := r.pool.Q(ctx).QueryRow(ctx, `
		SELECT legal_entity_id, organisation_id, enabled, environment, installation_number,
		       cert_p12_encrypted, cert_password_encrypted, cert_subject, cert_not_after, updated_at
		FROM verifactu_settings WHERE legal_entity_id = $1`, legalEntityID).
		Scan(&s.LegalEntityID, &s.OrganisationID, &s.Enabled, &s.Environment, &s.InstallationNumber,
			&s.CertEncrypted, &s.CertPassword, &s.CertSubject, &s.CertNotAfter, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("verifactu: reading settings: %w", err)
	}
	return &s, nil
}

func (r *Repo) UpsertSettings(ctx context.Context, s *Settings) error {
	_, err := r.pool.Q(ctx).Exec(ctx, `
		INSERT INTO verifactu_settings (legal_entity_id, organisation_id, enabled, environment, installation_number,
			cert_p12_encrypted, cert_password_encrypted, cert_subject, cert_not_after)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (legal_entity_id) DO UPDATE SET enabled = EXCLUDED.enabled, environment = EXCLUDED.environment,
			cert_p12_encrypted = EXCLUDED.cert_p12_encrypted, cert_password_encrypted = EXCLUDED.cert_password_encrypted,
			cert_subject = EXCLUDED.cert_subject, cert_not_after = EXCLUDED.cert_not_after, updated_at = now()`,
		s.LegalEntityID, s.OrganisationID, s.Enabled, s.Environment, s.InstallationNumber,
		s.CertEncrypted, s.CertPassword, s.CertSubject, s.CertNotAfter)
	if err != nil {
		return fmt.Errorf("verifactu: saving settings: %w", err)
	}
	return nil
}

// Head is the tip of a company's chain.
type Head struct {
	Seq       int64
	Hash      string
	Number    string
	IssueDate *time.Time
}

// LockHead returns the chain tip, creating it on first use, and holds a row lock until the transaction ends so
// concurrent finalizations are serialised.
func (r *Repo) LockHead(ctx context.Context, orgID, legalEntityID uuid.UUID) (*Head, error) {
	if _, err := r.pool.Q(ctx).Exec(ctx, `
		INSERT INTO verifactu_chain_heads (legal_entity_id, organisation_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, legalEntityID, orgID); err != nil {
		return nil, fmt.Errorf("verifactu: creating chain head: %w", err)
	}
	var h Head
	err := r.pool.Q(ctx).QueryRow(ctx, `
		SELECT last_seq, last_hash, last_invoice_number, last_issue_date FROM verifactu_chain_heads
		WHERE legal_entity_id = $1 FOR UPDATE`, legalEntityID).Scan(&h.Seq, &h.Hash, &h.Number, &h.IssueDate)
	if err != nil {
		return nil, fmt.Errorf("verifactu: locking chain head: %w", err)
	}
	return &h, nil
}

type Record struct {
	ID              uuid.UUID
	OrganisationID  uuid.UUID
	LegalEntityID   uuid.UUID
	Seq             int64
	Type            string
	SalesDocumentID *uuid.UUID
	IssuerNIF       string
	InvoiceNumber   string
	IssueDate       time.Time
	InvoiceType     *string
	TaxTotal        *decimal.Decimal
	InvoiceTotal    *decimal.Decimal
	PrevHash        string
	Hash            string
	GeneratedAtText string
	XML             string
	QRURL           *string
	CreatedAt       time.Time
}

// Append inserts the next record and advances the chain tip. The database rejects a record that does not continue the chain.
func (r *Repo) Append(ctx context.Context, rec *Record) error {
	if _, err := r.pool.Q(ctx).Exec(ctx, `
		INSERT INTO verifactu_records (id, organisation_id, legal_entity_id, seq, record_type, sales_document_id, issuer_nif,
			invoice_number, issue_date, invoice_type, tax_total, invoice_total, prev_hash, hash, generated_at_text, xml, qr_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		rec.ID, rec.OrganisationID, rec.LegalEntityID, rec.Seq, rec.Type, rec.SalesDocumentID, rec.IssuerNIF,
		rec.InvoiceNumber, rec.IssueDate, rec.InvoiceType, rec.TaxTotal, rec.InvoiceTotal, rec.PrevHash, rec.Hash, rec.GeneratedAtText, rec.XML, rec.QRURL); err != nil {
		return fmt.Errorf("verifactu: appending record: %w", err)
	}
	if _, err := r.pool.Q(ctx).Exec(ctx, `
		UPDATE verifactu_chain_heads SET last_seq = $2, last_hash = $3, last_invoice_number = $4, last_issue_date = $5 WHERE legal_entity_id = $1`,
		rec.LegalEntityID, rec.Seq, rec.Hash, rec.InvoiceNumber, rec.IssueDate); err != nil {
		return fmt.Errorf("verifactu: advancing chain head: %w", err)
	}
	if _, err := r.pool.Q(ctx).Exec(ctx, `
		INSERT INTO verifactu_submissions (record_id, organisation_id, legal_entity_id, seq) VALUES ($1,$2,$3,$4)`,
		rec.ID, rec.OrganisationID, rec.LegalEntityID, rec.Seq); err != nil {
		return fmt.Errorf("verifactu: queuing submission: %w", err)
	}
	return nil
}

const recordCols = `id, organisation_id, legal_entity_id, seq, record_type, sales_document_id, issuer_nif, invoice_number, issue_date,
	invoice_type, tax_total, invoice_total, prev_hash, hash, generated_at_text, xml, qr_url, created_at`

func scanRecord(row pgx.Row) (*Record, error) {
	var x Record
	err := row.Scan(&x.ID, &x.OrganisationID, &x.LegalEntityID, &x.Seq, &x.Type, &x.SalesDocumentID, &x.IssuerNIF, &x.InvoiceNumber,
		&x.IssueDate, &x.InvoiceType, &x.TaxTotal, &x.InvoiceTotal, &x.PrevHash, &x.Hash, &x.GeneratedAtText, &x.XML, &x.QRURL, &x.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// AltaForDocument returns the invoice (alta) record issued for a sales document.
func (r *Repo) AltaForDocument(ctx context.Context, documentID uuid.UUID) (*Record, error) {
	x, err := scanRecord(r.pool.Q(ctx).QueryRow(ctx,
		`SELECT `+recordCols+` FROM verifactu_records WHERE sales_document_id = $1 AND record_type = 'ALTA'`, documentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("verifactu: reading record: %w", err)
	}
	return x, nil
}

// HasCancellation reports whether a cancellation record exists for the document.
func (r *Repo) HasCancellation(ctx context.Context, documentID uuid.UUID) (bool, error) {
	var n int
	err := r.pool.Q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM verifactu_records WHERE sales_document_id = $1 AND record_type = 'ANULACION'`, documentID).Scan(&n)
	return n > 0, err
}

type RecordRow struct {
	Record
	Status       string
	Attempts     int
	AEATCSV      *string
	ErrorCode    *string
	ErrorMessage *string
	LastAttempt  *time.Time
}

// List returns a company's records newest first with their submission state.
func (r *Repo) List(ctx context.Context, legalEntityID uuid.UUID, limit, offset int) ([]RecordRow, error) {
	rows, err := r.pool.Q(ctx).Query(ctx, `
		SELECT r.id, r.organisation_id, r.legal_entity_id, r.seq, r.record_type, r.sales_document_id, r.issuer_nif, r.invoice_number,
		       r.issue_date, r.invoice_type, r.tax_total, r.invoice_total, r.prev_hash, r.hash, r.generated_at_text, '' AS xml, r.qr_url, r.created_at,
		       s.status, s.attempts, s.aeat_csv, s.error_code, s.error_message, s.last_attempt_at
		FROM verifactu_records r JOIN verifactu_submissions s ON s.record_id = r.id
		WHERE r.legal_entity_id = $1 ORDER BY r.seq DESC LIMIT $2 OFFSET $3`, legalEntityID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("verifactu: listing records: %w", err)
	}
	defer rows.Close()
	var out []RecordRow
	for rows.Next() {
		var x RecordRow
		if err := rows.Scan(&x.ID, &x.OrganisationID, &x.LegalEntityID, &x.Seq, &x.Type, &x.SalesDocumentID, &x.IssuerNIF, &x.InvoiceNumber,
			&x.IssueDate, &x.InvoiceType, &x.TaxTotal, &x.InvoiceTotal, &x.PrevHash, &x.Hash, &x.GeneratedAtText, &x.XML, &x.QRURL, &x.CreatedAt,
			&x.Status, &x.Attempts, &x.AEATCSV, &x.ErrorCode, &x.ErrorMessage, &x.LastAttempt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// All returns every record of a company in chain order (for export and chain verification).
func (r *Repo) All(ctx context.Context, legalEntityID uuid.UUID) ([]Record, error) {
	rows, err := r.pool.Q(ctx).Query(ctx, `SELECT `+recordCols+` FROM verifactu_records WHERE legal_entity_id = $1 ORDER BY seq`, legalEntityID)
	if err != nil {
		return nil, fmt.Errorf("verifactu: reading chain: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		x, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// Due returns the next batch of records still to be sent, in chain order, provided the earliest one is due.
func (r *Repo) Due(ctx context.Context, legalEntityID uuid.UUID, limit int) ([]Record, error) {
	rows, err := r.pool.Q(ctx).Query(ctx, `
		SELECT `+prefixed("r", recordCols)+`
		FROM verifactu_records r JOIN verifactu_submissions s ON s.record_id = r.id
		WHERE r.legal_entity_id = $1 AND s.status IN ('PENDING', 'ERROR')
		ORDER BY r.seq LIMIT $2`, legalEntityID, limit)
	if err != nil {
		return nil, fmt.Errorf("verifactu: reading pending records: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		x, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// EarliestDue reports whether the oldest unsent record's retry time has arrived.
func (r *Repo) EarliestDue(ctx context.Context, legalEntityID uuid.UUID) (bool, error) {
	var due bool
	err := r.pool.Q(ctx).QueryRow(ctx, `
		SELECT COALESCE((SELECT next_attempt_at <= now() FROM verifactu_submissions
			WHERE legal_entity_id = $1 AND status IN ('PENDING','ERROR') ORDER BY seq LIMIT 1), false)`, legalEntityID).Scan(&due)
	return due, err
}

type Outcome struct {
	RecordID  uuid.UUID
	Status    string
	CSV       string
	ErrorCode string
	ErrorText string
	Response  string
	NextAfter time.Duration
}

// SetOutcome records AEAT's answer for one record.
func (r *Repo) SetOutcome(ctx context.Context, o Outcome) error {
	_, err := r.pool.Q(ctx).Exec(ctx, `
		UPDATE verifactu_submissions SET status = $2, attempts = attempts + 1, last_attempt_at = now(),
			next_attempt_at = now() + ($3 || ' seconds')::interval, aeat_csv = NULLIF($4, ''), error_code = NULLIF($5, ''),
			error_message = NULLIF($6, ''), response_xml = NULLIF($7, ''), updated_at = now()
		WHERE record_id = $1`, o.RecordID, o.Status, fmt.Sprintf("%d", int(o.NextAfter.Seconds())), o.CSV, o.ErrorCode, o.ErrorText, o.Response)
	if err != nil {
		return fmt.Errorf("verifactu: recording outcome: %w", err)
	}
	return nil
}

// RetryNow makes every errored or pending record due immediately.
func (r *Repo) RetryNow(ctx context.Context, legalEntityID uuid.UUID) error {
	_, err := r.pool.Q(ctx).Exec(ctx, `UPDATE verifactu_submissions SET next_attempt_at = now() WHERE legal_entity_id = $1 AND status IN ('PENDING','ERROR')`, legalEntityID)
	return err
}

func prefixed(alias, cols string) string {
	out := ""
	for _, c := range splitCols(cols) {
		if out != "" {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

func splitCols(s string) []string {
	var cols []string
	cur := ""
	for _, ch := range s {
		switch ch {
		case ',':
			cols = append(cols, trim(cur))
			cur = ""
		default:
			cur += string(ch)
		}
	}
	return append(cols, trim(cur))
}

func trim(s string) string {
	out := []rune{}
	for _, ch := range s {
		if ch != ' ' && ch != '\n' && ch != '\t' {
			out = append(out, ch)
		}
	}
	return string(out)
}
