// Package pg is the taxation module's PostgreSQL repository implementation.
package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
)

type scannable interface {
	Scan(dest ...any) error
}

// --- tax_documents ---

type TaxDocumentRepo struct{ pool *database.Pool }

func NewTaxDocumentRepo(pool *database.Pool) *TaxDocumentRepo { return &TaxDocumentRepo{pool: pool} }

func (r *TaxDocumentRepo) Create(ctx context.Context, d *domain.TaxDocument) error {
	const q = `
		INSERT INTO tax_documents (
			id, organisation_id, reference_type, reference_id, document_date, currency_code,
			supplier_state_code, place_of_supply_code, supply_type, reverse_charge,
			total_taxable_amount, total_tax_amount, grand_total, created_at,
			supplier_country, customer_country, supply_region, exchange_rate
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`
	_, err := r.pool.Q(ctx).Exec(ctx, q,
		d.ID, d.OrganisationID, d.ReferenceType, d.ReferenceID, d.DocumentDate, d.CurrencyCode,
		nullIfEmpty(d.SupplierStateCode), nullIfEmpty(d.PlaceOfSupplyCode), string(d.SupplyType), d.ReverseCharge,
		d.TotalTaxableAmount.Decimal(), d.TotalTaxAmount.Decimal(), d.GrandTotal.Decimal(), d.CreatedAt,
		nullIfEmpty(d.SupplierCountry), nullIfEmpty(d.CustomerCountry), nullIfEmpty(d.SupplyRegion), d.ExchangeRate)
	if err != nil {
		return fmt.Errorf("taxation: inserting tax_document: %w", err)
	}
	return nil
}

func (r *TaxDocumentRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.TaxDocument, error) {
	const q = `
		SELECT id, organisation_id, reference_type, reference_id, document_date, currency_code,
			COALESCE(supplier_state_code, ''), COALESCE(place_of_supply_code, ''), supply_type, reverse_charge,
			total_taxable_amount, total_tax_amount, grand_total, created_at,
			COALESCE(supplier_country, ''), COALESCE(customer_country, ''), COALESCE(supply_region, '')
		FROM tax_documents WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	return scanTaxDocument(row)
}

func (r *TaxDocumentRepo) GetByReference(ctx context.Context, orgID uuid.UUID, referenceType string, referenceID uuid.UUID) (*domain.TaxDocument, error) {
	const q = `
		SELECT id, organisation_id, reference_type, reference_id, document_date, currency_code,
			COALESCE(supplier_state_code, ''), COALESCE(place_of_supply_code, ''), supply_type, reverse_charge,
			total_taxable_amount, total_tax_amount, grand_total, created_at,
			COALESCE(supplier_country, ''), COALESCE(customer_country, ''), COALESCE(supply_region, '')
		FROM tax_documents WHERE organisation_id = $1 AND reference_type = $2 AND reference_id = $3`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, referenceType, referenceID)
	return scanTaxDocument(row)
}

func scanTaxDocument(row scannable) (*domain.TaxDocument, error) {
	var d domain.TaxDocument
	var supplyType string
	var taxable, tax, grand decimal.Decimal
	err := row.Scan(&d.ID, &d.OrganisationID, &d.ReferenceType, &d.ReferenceID, &d.DocumentDate, &d.CurrencyCode,
		&d.SupplierStateCode, &d.PlaceOfSupplyCode, &supplyType, &d.ReverseCharge,
		&taxable, &tax, &grand, &d.CreatedAt, &d.SupplierCountry, &d.CustomerCountry, &d.SupplyRegion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	d.SupplyType = domain.SupplyType(supplyType)
	if d.TotalTaxableAmount, err = money.New(taxable, d.CurrencyCode); err != nil {
		return nil, err
	}
	if d.TotalTaxAmount, err = money.New(tax, d.CurrencyCode); err != nil {
		return nil, err
	}
	if d.GrandTotal, err = money.New(grand, d.CurrencyCode); err != nil {
		return nil, err
	}
	return &d, nil
}

// --- tax_lines ---

type TaxLineRepo struct{ pool *database.Pool }

func NewTaxLineRepo(pool *database.Pool) *TaxLineRepo { return &TaxLineRepo{pool: pool} }

func (r *TaxLineRepo) Create(ctx context.Context, l *domain.TaxLine) error {
	const q = `
		INSERT INTO tax_lines (
			id, organisation_id, tax_document_id, line_ref, hsn_sac_code, pricing_mode,
			gross_amount, taxable_amount, total_tax_amount, classification, tax_rate_master_id, created_at,
			tax_regime, rate_pack_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`
	_, err := r.pool.Q(ctx).Exec(ctx, q,
		l.ID, l.OrganisationID, l.TaxDocumentID, l.LineRef, l.HSNSACCode, string(l.PricingMode),
		l.GrossAmount.Decimal(), l.TaxableAmount.Decimal(), l.TotalTaxAmount.Decimal(), l.Classification, nullUUID(l.TaxRateMasterID), l.CreatedAt,
		nullIfEmpty(l.Regime), nullIfEmpty(l.RatePackVersion))
	if err != nil {
		return fmt.Errorf("taxation: inserting tax_line: %w", err)
	}
	return nil
}

func (r *TaxLineRepo) ListByDocument(ctx context.Context, orgID, taxDocumentID uuid.UUID) ([]*domain.TaxLine, error) {
	const q = `
		SELECT tl.id, tl.organisation_id, tl.tax_document_id, tl.line_ref, tl.hsn_sac_code, tl.pricing_mode,
			tl.gross_amount, tl.taxable_amount, tl.total_tax_amount, tl.classification, tl.tax_rate_master_id, tl.created_at,
			td.currency_code, COALESCE(tl.tax_regime, ''), COALESCE(tl.rate_pack_version, '')
		FROM tax_lines tl JOIN tax_documents td ON td.id = tl.tax_document_id
		WHERE tl.organisation_id = $1 AND tl.tax_document_id = $2 ORDER BY tl.created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, taxDocumentID)
	if err != nil {
		return nil, fmt.Errorf("taxation: listing tax_lines: %w", err)
	}
	defer rows.Close()
	var out []*domain.TaxLine
	for rows.Next() {
		l, err := scanTaxLine(rows)
		if err != nil {
			return nil, fmt.Errorf("taxation: scanning tax_line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func scanTaxLine(row scannable) (*domain.TaxLine, error) {
	var l domain.TaxLine
	var pricingMode, currencyCode string
	var gross, taxable, tax decimal.Decimal
	var rateMasterID *uuid.UUID
	err := row.Scan(&l.ID, &l.OrganisationID, &l.TaxDocumentID, &l.LineRef, &l.HSNSACCode, &pricingMode,
		&gross, &taxable, &tax, &l.Classification, &rateMasterID, &l.CreatedAt, &currencyCode, &l.Regime, &l.RatePackVersion)
	if err != nil {
		return nil, err
	}
	if rateMasterID != nil {
		l.TaxRateMasterID = *rateMasterID
	}
	l.PricingMode = domain.PricingMode(pricingMode)
	if l.GrossAmount, err = money.New(gross, currencyCode); err != nil {
		return nil, err
	}
	if l.TaxableAmount, err = money.New(taxable, currencyCode); err != nil {
		return nil, err
	}
	if l.TotalTaxAmount, err = money.New(tax, currencyCode); err != nil {
		return nil, err
	}
	return &l, nil
}

// --- tax_components ---

type TaxComponentRepo struct{ pool *database.Pool }

func NewTaxComponentRepo(pool *database.Pool) *TaxComponentRepo { return &TaxComponentRepo{pool: pool} }

func (r *TaxComponentRepo) Create(ctx context.Context, c *domain.TaxComponentRow) error {
	const q = `
		INSERT INTO tax_components (id, organisation_id, tax_line_id, component_type, rate, amount, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, c.ID, c.OrganisationID, c.TaxLineID, c.ComponentType, c.Rate, c.Amount.Decimal(), c.CreatedAt)
	if err != nil {
		return fmt.Errorf("taxation: inserting tax_component: %w", err)
	}
	return nil
}

func (r *TaxComponentRepo) ListByLine(ctx context.Context, orgID, taxLineID uuid.UUID) ([]*domain.TaxComponentRow, error) {
	const q = `
		SELECT tc.id, tc.organisation_id, tc.tax_line_id, tc.component_type, tc.rate, tc.amount, tc.created_at, td.currency_code
		FROM tax_components tc
		JOIN tax_lines tl ON tl.id = tc.tax_line_id
		JOIN tax_documents td ON td.id = tl.tax_document_id
		WHERE tc.organisation_id = $1 AND tc.tax_line_id = $2 ORDER BY tc.created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, taxLineID)
	if err != nil {
		return nil, fmt.Errorf("taxation: listing tax_components: %w", err)
	}
	defer rows.Close()
	var out []*domain.TaxComponentRow
	for rows.Next() {
		var c domain.TaxComponentRow
		var amount decimal.Decimal
		var currencyCode string
		if err := rows.Scan(&c.ID, &c.OrganisationID, &c.TaxLineID, &c.ComponentType, &c.Rate, &amount, &c.CreatedAt, &currencyCode); err != nil {
			return nil, fmt.Errorf("taxation: scanning tax_component: %w", err)
		}
		m, err := money.New(amount, currencyCode)
		if err != nil {
			return nil, err
		}
		c.Amount = m
		out = append(out, &c)
	}
	return out, rows.Err()
}

// nullIfEmpty stores "" as SQL NULL (state codes, regime names).
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullUUID stores uuid.Nil as SQL NULL; non-India tax lines have no rate-master row.
func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
