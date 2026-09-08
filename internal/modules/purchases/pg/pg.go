// Package pg is the purchases module's PostgreSQL repository
// implementation. Same shape as internal/modules/catalogue/pg.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/purchases/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
)

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type DocumentRepo struct{ pool *database.Pool }

func NewDocumentRepo(pool *database.Pool) *DocumentRepo { return &DocumentRepo{pool: pool} }

func (r *DocumentRepo) Create(ctx context.Context, d *domain.Document) error {
	const q = `
		INSERT INTO purchase_documents (
			id, organisation_id, branch_id, warehouse_id, supplier_party_id, document_type, document_number,
			reference_document_id, status, supplier_invoice_number, supplier_invoice_date, document_date,
			currency_code, notes, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)`
	_, err := r.pool.Q(ctx).Exec(ctx, q,
		d.ID, d.OrganisationID, d.BranchID, d.WarehouseID, d.SupplierPartyID, string(d.DocumentType), d.DocumentNumber,
		d.ReferenceDocumentID, string(d.Status), nullIfEmpty(d.SupplierInvoiceNumber), d.SupplierInvoiceDate, d.DocumentDate,
		d.CurrencyCode, nullIfEmpty(d.Notes), d.CreatedBy, d.CreatedAt,
	)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateNumber
		}
		return fmt.Errorf("purchases: inserting purchase_document: %w", err)
	}
	return nil
}

const documentCols = `id, organisation_id, branch_id, warehouse_id, supplier_party_id, document_type, document_number,
	reference_document_id, status, COALESCE(supplier_invoice_number, ''), supplier_invoice_date, document_date,
	currency_code, COALESCE(notes, ''), tax_document_id, created_by, created_at, updated_at, finalized_at`

func (r *DocumentRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Document, error) {
	q := fmt.Sprintf(`SELECT %s FROM purchase_documents WHERE organisation_id = $1 AND id = $2`, documentCols)
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	return scanDocument(row)
}

func (r *DocumentRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID, documentType *domain.DocumentType) ([]*domain.Document, error) {
	var (
		q    string
		args []any
	)
	if documentType != nil {
		q = fmt.Sprintf(`SELECT %s FROM purchase_documents WHERE organisation_id = $1 AND document_type = $2 ORDER BY document_date DESC`, documentCols)
		args = []any{orgID, string(*documentType)}
	} else {
		q = fmt.Sprintf(`SELECT %s FROM purchase_documents WHERE organisation_id = $1 ORDER BY document_date DESC`, documentCols)
		args = []any{orgID}
	}
	rows, err := r.pool.Q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("purchases: listing purchase_documents: %w", err)
	}
	defer rows.Close()
	var out []*domain.Document
	for rows.Next() {
		d, err := scanDocumentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("purchases: scanning purchase_document row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DocumentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DocumentStatus, finalizedAt *time.Time) error {
	const q = `UPDATE purchase_documents SET status = $2, finalized_at = $3, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, id, string(status), finalizedAt)
	if err != nil {
		return fmt.Errorf("purchases: updating purchase_document status: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DocumentRepo) UpdateTaxDocument(ctx context.Context, id, taxDocumentID uuid.UUID) error {
	const q = `UPDATE purchase_documents SET tax_document_id = $2, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, id, taxDocumentID)
	if err != nil {
		return fmt.Errorf("purchases: updating purchase_document tax_document_id: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// NextNumber uses INSERT ... ON CONFLICT DO UPDATE ... RETURNING as an
// atomic get-and-increment: the first call for a given (org, doc type)
// inserts the counter row at 1 and returns 1; every subsequent call
// increments and returns the new value. This is concurrency-safe the
// same way stock_balances' upsert-under-lock is — the UPDATE takes a row
// lock, so two concurrent callers serialize instead of both reading the
// same "next" value (Scenario I).
func (r *DocumentRepo) NextNumber(ctx context.Context, orgID uuid.UUID, docType domain.DocumentType) (int64, error) {
	// The first call for a given (org, doc type) inserts the counter row
	// already at 2 and returns 1 (next_number - 1); every subsequent call
	// hits the DO UPDATE branch, incrementing in place, and returns the
	// post-increment value. Either way the row lock taken by the
	// INSERT/UPDATE is what makes two concurrent callers serialize
	// instead of both computing the same "next" value (Scenario I).
	const q = `
		INSERT INTO purchase_document_counters (organisation_id, document_type, next_number)
		VALUES ($1, $2, 2)
		ON CONFLICT (organisation_id, document_type)
		DO UPDATE SET next_number = purchase_document_counters.next_number + 1
		RETURNING next_number - 1`
	var allocated int64
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, string(docType))
	if err := row.Scan(&allocated); err != nil {
		return 0, fmt.Errorf("purchases: allocating document number: %w", err)
	}
	return allocated, nil
}

func scanDocument(row pgx.Row) (*domain.Document, error) {
	d, err := scanDocumentRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("purchases: querying purchase_document: %w", err)
	}
	return d, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanDocumentRow(row scannable) (*domain.Document, error) {
	var d domain.Document
	var docType, status string
	if err := row.Scan(&d.ID, &d.OrganisationID, &d.BranchID, &d.WarehouseID, &d.SupplierPartyID, &docType, &d.DocumentNumber,
		&d.ReferenceDocumentID, &status, &d.SupplierInvoiceNumber, &d.SupplierInvoiceDate, &d.DocumentDate,
		&d.CurrencyCode, &d.Notes, &d.TaxDocumentID, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.FinalizedAt); err != nil {
		return nil, err
	}
	d.DocumentType = domain.DocumentType(docType)
	d.Status = domain.DocumentStatus(status)
	return &d, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Document lines ---

type DocumentLineRepo struct{ pool *database.Pool }

func NewDocumentLineRepo(pool *database.Pool) *DocumentLineRepo { return &DocumentLineRepo{pool: pool} }

func (r *DocumentLineRepo) Create(ctx context.Context, l *domain.DocumentLine) error {
	const q = `
		INSERT INTO purchase_document_lines (
			id, organisation_id, purchase_document_id, line_number, product_variant_id, unit_id,
			quantity, unit_price_amount, line_total_amount, batch_code, hsn_sac_code, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, l.ID, l.OrganisationID, l.PurchaseDocumentID, l.LineNumber, l.ProductVariantID, l.UnitID,
		l.Quantity, l.UnitPrice.Decimal(), l.LineTotal.Decimal(), nullIfEmpty(l.BatchCode), l.HSNSACCode, l.CreatedAt)
	if err != nil {
		return fmt.Errorf("purchases: inserting purchase_document_line: %w", err)
	}
	return nil
}

func (r *DocumentLineRepo) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]*domain.DocumentLine, error) {
	const q = `
		SELECT l.id, l.organisation_id, l.purchase_document_id, l.line_number, l.product_variant_id, l.unit_id,
			l.quantity, l.unit_price_amount, l.line_total_amount, COALESCE(l.batch_code, ''), l.hsn_sac_code, l.created_at, d.currency_code
		FROM purchase_document_lines l
		JOIN purchase_documents d ON d.id = l.purchase_document_id
		WHERE l.purchase_document_id = $1
		ORDER BY l.line_number`
	rows, err := r.pool.Q(ctx).Query(ctx, q, documentID)
	if err != nil {
		return nil, fmt.Errorf("purchases: listing purchase_document_lines: %w", err)
	}
	defer rows.Close()
	var out []*domain.DocumentLine
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, fmt.Errorf("purchases: scanning purchase_document_line row: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanLine(row scannable) (*domain.DocumentLine, error) {
	var l domain.DocumentLine
	var unitPriceAmount, lineTotalAmount decimal.Decimal
	var currencyCode string
	if err := row.Scan(&l.ID, &l.OrganisationID, &l.PurchaseDocumentID, &l.LineNumber, &l.ProductVariantID, &l.UnitID,
		&l.Quantity, &unitPriceAmount, &lineTotalAmount, &l.BatchCode, &l.HSNSACCode, &l.CreatedAt, &currencyCode); err != nil {
		return nil, err
	}
	unitPrice, err := money.New(unitPriceAmount, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("constructing unit price money value: %w", err)
	}
	lineTotal, err := money.New(lineTotalAmount, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("constructing line total money value: %w", err)
	}
	l.UnitPrice = unitPrice
	l.LineTotal = lineTotal
	return &l, nil
}
