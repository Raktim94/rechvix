package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rechvix/internal/modules/einvoice/domain"
	"rechvix/internal/platform/database"
)

type RecordRepo struct {
	pool *database.Pool
}

func NewRecordRepo(pool *database.Pool) *RecordRepo {
	return &RecordRepo{pool: pool}
}

var _ domain.Repository = (*RecordRepo)(nil)

const selectCols = `id, organisation_id, sales_document_id, provider, status, request_version,
	request_payload_hash, response_payload, irn, ack_number, ack_date, signed_invoice,
	signed_qr_payload, error_code, error_message, correlation_id, cancelled_at, cancel_reason,
	created_at, updated_at`

func scanRecord(row pgx.Row) (*domain.Record, error) {
	var r domain.Record
	var status string
	err := row.Scan(&r.ID, &r.OrganisationID, &r.SalesDocumentID, &r.Provider, &status, &r.RequestVersion,
		&r.RequestHash, &r.ResponsePayload, &r.IRN, &r.AckNumber, &r.AckDate, &r.SignedInvoice,
		&r.SignedQRPayload, &r.ErrorCode, &r.ErrorMessage, &r.CorrelationID, &r.CancelledAt, &r.CancelReason,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	r.Status = domain.Status(status)
	return &r, nil
}

func (repo *RecordRepo) GetBySalesDocumentID(ctx context.Context, salesDocumentID uuid.UUID) (*domain.Record, error) {
	row := repo.pool.Q(ctx).QueryRow(ctx,
		`SELECT `+selectCols+` FROM einvoice_records WHERE sales_document_id = $1`, salesDocumentID)
	r, err := scanRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("einvoice: querying record by sales_document_id: %w", err)
	}
	return r, nil
}

func (repo *RecordRepo) Create(ctx context.Context, r *domain.Record) error {
	const q = `
		INSERT INTO einvoice_records
			(id, organisation_id, sales_document_id, provider, status, request_version)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := repo.pool.Q(ctx).Exec(ctx, q, r.ID, r.OrganisationID, r.SalesDocumentID, r.Provider, string(r.Status), r.RequestVersion)
	if err != nil {
		return fmt.Errorf("einvoice: inserting record: %w", err)
	}
	return nil
}

// UpdateStatus applies a status transition plus any non-nil UpdateFields —
// nil fields use COALESCE to leave the existing column value untouched
// (never silently reset to NULL on a partial update, same convention as
// every other module's UpdateFields-shaped call).
func (repo *RecordRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status, f domain.UpdateFields) error {
	const q = `
		UPDATE einvoice_records SET
			status = $2,
			provider = COALESCE($3, provider),
			response_payload = COALESCE($4, response_payload),
			irn = COALESCE($5, irn),
			ack_number = COALESCE($6, ack_number),
			ack_date = COALESCE($7, ack_date),
			signed_invoice = COALESCE($8, signed_invoice),
			signed_qr_payload = COALESCE($9, signed_qr_payload),
			error_code = COALESCE($10, error_code),
			error_message = COALESCE($11, error_message),
			correlation_id = COALESCE($12, correlation_id),
			cancelled_at = COALESCE($13, cancelled_at),
			cancel_reason = COALESCE($14, cancel_reason),
			updated_at = now()
		WHERE id = $1`
	n, err := repo.pool.Q(ctx).Exec(ctx, q, id, string(status), f.Provider,
		nilIfEmptyBytes(f.ResponsePayload), f.IRN, f.AckNumber, f.AckDate, f.SignedInvoice, f.SignedQRPayload,
		f.ErrorCode, f.ErrorMessage, f.CorrelationID, f.CancelledAt, f.CancelReason)
	if err != nil {
		return fmt.Errorf("einvoice: updating record status: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func nilIfEmptyBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

type CredentialsRepo struct {
	pool *database.Pool
}

func NewCredentialsRepo(pool *database.Pool) *CredentialsRepo {
	return &CredentialsRepo{pool: pool}
}

var _ domain.CredentialsRepository = (*CredentialsRepo)(nil)

func (repo *CredentialsRepo) Get(ctx context.Context, orgID, legalEntityID uuid.UUID, provider string) (*domain.ProviderCredentials, error) {
	row := repo.pool.Q(ctx).QueryRow(ctx,
		`SELECT id, organisation_id, legal_entity_id, provider, encrypted_credentials, created_at, updated_at
		 FROM einvoice_provider_credentials
		 WHERE organisation_id = $1 AND legal_entity_id = $2 AND provider = $3`,
		orgID, legalEntityID, provider)
	var c domain.ProviderCredentials
	err := row.Scan(&c.ID, &c.OrganisationID, &c.LegalEntityID, &c.Provider, &c.EncryptedCredentials, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("einvoice: querying provider credentials: %w", err)
	}
	return &c, nil
}

// Upsert relies on the table's own UNIQUE (organisation_id, legal_entity_id,
// provider) constraint (migrations/0024) — one credentials row per legal
// entity per provider, replacing whatever was there before rather than
// accumulating stale rows across re-saves.
func (repo *CredentialsRepo) Upsert(ctx context.Context, c *domain.ProviderCredentials) error {
	const q = `
		INSERT INTO einvoice_provider_credentials
			(id, organisation_id, legal_entity_id, provider, encrypted_credentials)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organisation_id, legal_entity_id, provider) DO UPDATE SET
			encrypted_credentials = EXCLUDED.encrypted_credentials,
			updated_at = now()`
	_, err := repo.pool.Q(ctx).Exec(ctx, q, c.ID, c.OrganisationID, c.LegalEntityID, c.Provider, c.EncryptedCredentials)
	if err != nil {
		return fmt.Errorf("einvoice: upserting provider credentials: %w", err)
	}
	return nil
}

func (repo *CredentialsRepo) Delete(ctx context.Context, orgID, legalEntityID uuid.UUID, provider string) error {
	_, err := repo.pool.Q(ctx).Exec(ctx,
		`DELETE FROM einvoice_provider_credentials WHERE organisation_id = $1 AND legal_entity_id = $2 AND provider = $3`,
		orgID, legalEntityID, provider)
	if err != nil {
		return fmt.Errorf("einvoice: deleting provider credentials: %w", err)
	}
	return nil
}
