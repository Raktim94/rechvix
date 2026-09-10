// Package pg is the sales module's PostgreSQL repository implementation.
// Same shape as internal/modules/purchases/pg.
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

	"rechvix/internal/modules/sales/domain"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
)

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type DocumentRepo struct{ pool *database.Pool }

func NewDocumentRepo(pool *database.Pool) *DocumentRepo { return &DocumentRepo{pool: pool} }

func (r *DocumentRepo) Create(ctx context.Context, d *domain.Document) error {
	const q = `
		INSERT INTO sales_documents (
			id, organisation_id, legal_entity_id, branch_id, warehouse_id, customer_party_id, document_type, document_number,
			reference_document_id, status, issue_date, due_date, supply_date, billing_address_id, shipping_address_id,
			customer_tax_registration_id, place_of_supply_state_code, salesperson_user_id, price_list_id,
			currency_code, base_currency_code, exchange_rate, pricing_mode, customer_reference, transporter,
			vehicle_number, shipping_terms, notes, terms_and_conditions, payment_terms_days,
			created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$32)`
	_, err := r.pool.Q(ctx).Exec(ctx, q,
		d.ID, d.OrganisationID, d.LegalEntityID, d.BranchID, d.WarehouseID, d.CustomerPartyID, string(d.DocumentType), d.DocumentNumber,
		d.ReferenceDocumentID, string(d.Status), d.IssueDate, d.DueDate, d.SupplyDate, d.BillingAddressID, d.ShippingAddressID,
		d.CustomerTaxRegistrationID, nullIfEmpty(d.PlaceOfSupplyStateCode), d.SalespersonUserID, d.PriceListID,
		d.CurrencyCode, d.BaseCurrencyCode, d.ExchangeRate, string(d.PricingMode), nullIfEmpty(d.CustomerReference), nullIfEmpty(d.Transporter),
		nullIfEmpty(d.VehicleNumber), nullIfEmpty(d.ShippingTerms), nullIfEmpty(d.Notes), nullIfEmpty(d.TermsAndConditions), d.PaymentTermsDays,
		d.CreatedBy, d.CreatedAt,
	)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateNumber
		}
		return fmt.Errorf("sales: inserting sales_document: %w", err)
	}
	return nil
}

const documentCols = `id, organisation_id, legal_entity_id, branch_id, warehouse_id, customer_party_id, document_type, document_number,
	reference_document_id, status, issue_date, due_date, supply_date, billing_address_id, shipping_address_id,
	customer_tax_registration_id, COALESCE(place_of_supply_state_code, ''), salesperson_user_id, price_list_id,
	currency_code, base_currency_code, exchange_rate, pricing_mode, COALESCE(customer_reference, ''), COALESCE(transporter, ''),
	COALESCE(vehicle_number, ''), COALESCE(shipping_terms, ''), COALESCE(notes, ''), COALESCE(terms_and_conditions, ''), payment_terms_days,
	tax_document_id, grand_total_amount, created_by, created_at, updated_at, finalized_at`

func (r *DocumentRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Document, error) {
	q := fmt.Sprintf(`SELECT %s FROM sales_documents WHERE organisation_id = $1 AND id = $2`, documentCols)
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	d, err := scanDocumentRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("sales: querying sales_document: %w", err)
	}
	return d, nil
}

func (r *DocumentRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID, documentType *domain.DocumentType, legalEntityIDs []uuid.UUID) ([]*domain.Document, error) {
	where := "organisation_id = $1"
	args := []any{orgID}
	if documentType != nil {
		args = append(args, string(*documentType))
		where += fmt.Sprintf(" AND document_type = $%d", len(args))
	}
	if legalEntityIDs != nil {
		args = append(args, legalEntityIDs)
		where += fmt.Sprintf(" AND legal_entity_id = ANY($%d)", len(args))
	}
	q := fmt.Sprintf(`SELECT %s FROM sales_documents WHERE %s ORDER BY issue_date DESC`, documentCols, where)
	rows, err := r.pool.Q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sales: listing sales_documents: %w", err)
	}
	defer rows.Close()
	var out []*domain.Document
	for rows.Next() {
		d, err := scanDocumentRow(rows)
		if err != nil {
			return nil, fmt.Errorf("sales: scanning sales_document row: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DocumentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DocumentStatus, finalizedAt *time.Time) error {
	const q = `UPDATE sales_documents SET status = $2, finalized_at = $3, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, id, string(status), finalizedAt)
	if err != nil {
		return fmt.Errorf("sales: updating sales_document status: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DocumentRepo) UpdateFinalizedTotals(ctx context.Context, id, taxDocumentID uuid.UUID, grandTotal decimal.Decimal) error {
	const q = `UPDATE sales_documents SET tax_document_id = $2, grand_total_amount = $3, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, id, taxDocumentID, grandTotal)
	if err != nil {
		return fmt.Errorf("sales: updating sales_document finalized totals: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanDocumentRow(row scannable) (*domain.Document, error) {
	var d domain.Document
	var docType, status, pricingMode string
	var taxDocID *uuid.UUID
	var grandTotalAmount *decimal.Decimal
	if err := row.Scan(&d.ID, &d.OrganisationID, &d.LegalEntityID, &d.BranchID, &d.WarehouseID, &d.CustomerPartyID, &docType, &d.DocumentNumber,
		&d.ReferenceDocumentID, &status, &d.IssueDate, &d.DueDate, &d.SupplyDate, &d.BillingAddressID, &d.ShippingAddressID,
		&d.CustomerTaxRegistrationID, &d.PlaceOfSupplyStateCode, &d.SalespersonUserID, &d.PriceListID,
		&d.CurrencyCode, &d.BaseCurrencyCode, &d.ExchangeRate, &pricingMode, &d.CustomerReference, &d.Transporter,
		&d.VehicleNumber, &d.ShippingTerms, &d.Notes, &d.TermsAndConditions, &d.PaymentTermsDays,
		&taxDocID, &grandTotalAmount, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.FinalizedAt); err != nil {
		return nil, err
	}
	d.DocumentType = domain.DocumentType(docType)
	d.Status = domain.DocumentStatus(status)
	d.PricingMode = taxdomain.PricingMode(pricingMode)
	d.TaxDocumentID = taxDocID
	if grandTotalAmount != nil {
		m, err := money.New(*grandTotalAmount, d.CurrencyCode)
		if err != nil {
			return nil, fmt.Errorf("constructing grand total money value: %w", err)
		}
		d.GrandTotalAmount = &m
	}
	return &d, nil
}

// --- Document lines ---

type DocumentLineRepo struct{ pool *database.Pool }

func NewDocumentLineRepo(pool *database.Pool) *DocumentLineRepo { return &DocumentLineRepo{pool: pool} }

func (r *DocumentLineRepo) Create(ctx context.Context, l *domain.DocumentLine) error {
	const q = `
		INSERT INTO sales_document_lines (
			id, organisation_id, sales_document_id, line_number, product_variant_id, unit_id,
			quantity, unit_price_amount, line_discount_amount, hsn_sac_code, line_total_amount, batch_code, serial_code, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, l.ID, l.OrganisationID, l.SalesDocumentID, l.LineNumber, l.ProductVariantID, l.UnitID,
		l.Quantity, l.UnitPrice.Decimal(), l.LineDiscountAmount.Decimal(), l.HSNSACCode, l.LineTotal.Decimal(),
		nullIfEmpty(l.BatchCode), nullIfEmpty(l.SerialCode), l.CreatedAt)
	if err != nil {
		return fmt.Errorf("sales: inserting sales_document_line: %w", err)
	}
	return nil
}

func (r *DocumentLineRepo) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]*domain.DocumentLine, error) {
	const q = `
		SELECT l.id, l.organisation_id, l.sales_document_id, l.line_number, l.product_variant_id, l.unit_id,
			l.quantity, l.unit_price_amount, l.line_discount_amount, l.hsn_sac_code, l.line_total_amount,
			COALESCE(l.batch_code, ''), COALESCE(l.serial_code, ''), l.created_at, d.currency_code
		FROM sales_document_lines l
		JOIN sales_documents d ON d.id = l.sales_document_id
		WHERE l.sales_document_id = $1
		ORDER BY l.line_number`
	rows, err := r.pool.Q(ctx).Query(ctx, q, documentID)
	if err != nil {
		return nil, fmt.Errorf("sales: listing sales_document_lines: %w", err)
	}
	defer rows.Close()
	var out []*domain.DocumentLine
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, fmt.Errorf("sales: scanning sales_document_line row: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *DocumentLineRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.DocumentLine, error) {
	const q = `
		SELECT l.id, l.organisation_id, l.sales_document_id, l.line_number, l.product_variant_id, l.unit_id,
			l.quantity, l.unit_price_amount, l.line_discount_amount, l.hsn_sac_code, l.line_total_amount,
			COALESCE(l.batch_code, ''), COALESCE(l.serial_code, ''), l.created_at, d.currency_code
		FROM sales_document_lines l
		JOIN sales_documents d ON d.id = l.sales_document_id
		WHERE l.organisation_id = $1 AND l.id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	l, err := scanLine(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("sales: querying sales_document_line: %w", err)
	}
	return l, nil
}

func (r *DocumentLineRepo) Update(ctx context.Context, l *domain.DocumentLine) error {
	const q = `
		UPDATE sales_document_lines
		SET quantity = $3, unit_price_amount = $4, line_discount_amount = $5, line_total_amount = $6
		WHERE organisation_id = $1 AND id = $2`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, l.OrganisationID, l.ID, l.Quantity, l.UnitPrice.Decimal(), l.LineDiscountAmount.Decimal(), l.LineTotal.Decimal())
	if err != nil {
		return fmt.Errorf("sales: updating sales_document_line: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *DocumentLineRepo) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	const q = `DELETE FROM sales_document_lines WHERE organisation_id = $1 AND id = $2`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, orgID, id)
	if err != nil {
		return fmt.Errorf("sales: deleting sales_document_line: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanLine(row scannable) (*domain.DocumentLine, error) {
	var l domain.DocumentLine
	var unitPriceAmount, discountAmount, lineTotalAmount decimal.Decimal
	var currencyCode string
	if err := row.Scan(&l.ID, &l.OrganisationID, &l.SalesDocumentID, &l.LineNumber, &l.ProductVariantID, &l.UnitID,
		&l.Quantity, &unitPriceAmount, &discountAmount, &l.HSNSACCode, &lineTotalAmount,
		&l.BatchCode, &l.SerialCode, &l.CreatedAt, &currencyCode); err != nil {
		return nil, err
	}
	unitPrice, err := money.New(unitPriceAmount, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("constructing unit price money value: %w", err)
	}
	discount, err := money.New(discountAmount, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("constructing discount money value: %w", err)
	}
	lineTotal, err := money.New(lineTotalAmount, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("constructing line total money value: %w", err)
	}
	l.UnitPrice = unitPrice
	l.LineDiscountAmount = discount
	l.LineTotal = lineTotal
	return &l, nil
}
