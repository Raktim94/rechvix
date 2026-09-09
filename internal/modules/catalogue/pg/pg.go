// Package pg is the catalogue module's PostgreSQL repository
// implementation. Same shape as internal/modules/organisation/pg — one
// small repo struct per domain interface, reading its Querier from
// database.Pool.Q(ctx) so it works both inside and outside an active
// RunScoped transaction.
package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rechvix/internal/modules/catalogue/domain"
	"rechvix/internal/platform/database"
)

// pgUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505), so repository methods can translate it into a
// domain-specific sentinel (ErrDuplicateSKU, ErrDuplicateCode) instead of
// a caller having to parse driver errors itself.
func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- Units of measure ---

type UnitOfMeasureRepo struct{ pool *database.Pool }

func NewUnitOfMeasureRepo(pool *database.Pool) *UnitOfMeasureRepo {
	return &UnitOfMeasureRepo{pool: pool}
}

func (r *UnitOfMeasureRepo) Create(ctx context.Context, u *domain.UnitOfMeasure) error {
	const q = `
		INSERT INTO units_of_measure (id, organisation_id, code, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, u.ID, u.OrganisationID, u.Code, u.Name, u.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateCode
		}
		return fmt.Errorf("catalogue: inserting unit_of_measure: %w", err)
	}
	return nil
}

func (r *UnitOfMeasureRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.UnitOfMeasure, error) {
	const q = `SELECT id, organisation_id, code, name, created_at, updated_at FROM units_of_measure WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var u domain.UnitOfMeasure
	if err := row.Scan(&u.ID, &u.OrganisationID, &u.Code, &u.Name, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying unit_of_measure: %w", err)
	}
	return &u, nil
}

func (r *UnitOfMeasureRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.UnitOfMeasure, error) {
	const q = `SELECT id, organisation_id, code, name, created_at, updated_at FROM units_of_measure WHERE organisation_id = $1 ORDER BY code`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing units_of_measure: %w", err)
	}
	defer rows.Close()
	var out []*domain.UnitOfMeasure
	for rows.Next() {
		var u domain.UnitOfMeasure
		if err := rows.Scan(&u.ID, &u.OrganisationID, &u.Code, &u.Name, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalogue: scanning unit_of_measure row: %w", err)
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

// --- Unit conversions ---

type UnitConversionRepo struct{ pool *database.Pool }

func NewUnitConversionRepo(pool *database.Pool) *UnitConversionRepo {
	return &UnitConversionRepo{pool: pool}
}

func (r *UnitConversionRepo) Create(ctx context.Context, c *domain.UnitConversion) error {
	const q = `
		INSERT INTO unit_conversions (id, organisation_id, from_unit_id, to_unit_id, factor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, c.ID, c.OrganisationID, c.FromUnitID, c.ToUnitID, c.Factor, c.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return fmt.Errorf("catalogue: %w", errors.New("a conversion between these units already exists"))
		}
		return fmt.Errorf("catalogue: inserting unit_conversion: %w", err)
	}
	return nil
}

func (r *UnitConversionRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.UnitConversion, error) {
	const q = `SELECT id, organisation_id, from_unit_id, to_unit_id, factor, created_at FROM unit_conversions WHERE organisation_id = $1`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing unit_conversions: %w", err)
	}
	defer rows.Close()
	var out []*domain.UnitConversion
	for rows.Next() {
		var c domain.UnitConversion
		if err := rows.Scan(&c.ID, &c.OrganisationID, &c.FromUnitID, &c.ToUnitID, &c.Factor, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("catalogue: scanning unit_conversion row: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *UnitConversionRepo) Find(ctx context.Context, orgID, fromUnitID, toUnitID uuid.UUID) (*domain.UnitConversion, error) {
	const q = `
		SELECT id, organisation_id, from_unit_id, to_unit_id, factor, created_at
		FROM unit_conversions WHERE organisation_id = $1 AND from_unit_id = $2 AND to_unit_id = $3`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, fromUnitID, toUnitID)
	var c domain.UnitConversion
	if err := row.Scan(&c.ID, &c.OrganisationID, &c.FromUnitID, &c.ToUnitID, &c.Factor, &c.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying unit_conversion: %w", err)
	}
	return &c, nil
}

// --- Categories ---

type CategoryRepo struct{ pool *database.Pool }

func NewCategoryRepo(pool *database.Pool) *CategoryRepo { return &CategoryRepo{pool: pool} }

func (r *CategoryRepo) Create(ctx context.Context, c *domain.Category) error {
	const q = `
		INSERT INTO categories (id, organisation_id, parent_id, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, c.ID, c.OrganisationID, c.ParentID, c.Name, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("catalogue: inserting category: %w", err)
	}
	return nil
}

func (r *CategoryRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Category, error) {
	const q = `SELECT id, organisation_id, parent_id, name, created_at, updated_at FROM categories WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var c domain.Category
	if err := row.Scan(&c.ID, &c.OrganisationID, &c.ParentID, &c.Name, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying category: %w", err)
	}
	return &c, nil
}

func (r *CategoryRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.Category, error) {
	const q = `SELECT id, organisation_id, parent_id, name, created_at, updated_at FROM categories WHERE organisation_id = $1 ORDER BY name`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing categories: %w", err)
	}
	defer rows.Close()
	var out []*domain.Category
	for rows.Next() {
		var c domain.Category
		if err := rows.Scan(&c.ID, &c.OrganisationID, &c.ParentID, &c.Name, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalogue: scanning category row: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// --- Brands ---

type BrandRepo struct{ pool *database.Pool }

func NewBrandRepo(pool *database.Pool) *BrandRepo { return &BrandRepo{pool: pool} }

func (r *BrandRepo) Create(ctx context.Context, b *domain.Brand) error {
	const q = `INSERT INTO brands (id, organisation_id, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, b.ID, b.OrganisationID, b.Name, b.CreatedAt)
	if err != nil {
		return fmt.Errorf("catalogue: inserting brand: %w", err)
	}
	return nil
}

func (r *BrandRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Brand, error) {
	const q = `SELECT id, organisation_id, name, created_at, updated_at FROM brands WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var b domain.Brand
	if err := row.Scan(&b.ID, &b.OrganisationID, &b.Name, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying brand: %w", err)
	}
	return &b, nil
}

func (r *BrandRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.Brand, error) {
	const q = `SELECT id, organisation_id, name, created_at, updated_at FROM brands WHERE organisation_id = $1 ORDER BY name`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing brands: %w", err)
	}
	defer rows.Close()
	var out []*domain.Brand
	for rows.Next() {
		var b domain.Brand
		if err := rows.Scan(&b.ID, &b.OrganisationID, &b.Name, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalogue: scanning brand row: %w", err)
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// --- Products ---

type ProductRepo struct{ pool *database.Pool }

func NewProductRepo(pool *database.Pool) *ProductRepo { return &ProductRepo{pool: pool} }

func (r *ProductRepo) Create(ctx context.Context, p *domain.Product) error {
	const q = `
		INSERT INTO products (id, organisation_id, category_id, brand_id, base_uom_id, name, description, hsn_sac_code, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, p.ID, p.OrganisationID, p.CategoryID, p.BrandID, p.BaseUOMID, p.Name, nullIfEmpty(p.Description), nullIfEmpty(p.HSNSACCode), string(p.Status), p.CreatedAt)
	if err != nil {
		return fmt.Errorf("catalogue: inserting product: %w", err)
	}
	return nil
}

func (r *ProductRepo) Update(ctx context.Context, p *domain.Product) error {
	const q = `
		UPDATE products SET category_id = $3, brand_id = $4, base_uom_id = $5, name = $6, description = $7, hsn_sac_code = $8, updated_at = $9
		WHERE organisation_id = $1 AND id = $2`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, p.OrganisationID, p.ID, p.CategoryID, p.BrandID, p.BaseUOMID, p.Name, nullIfEmpty(p.Description), nullIfEmpty(p.HSNSACCode), p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("catalogue: updating product: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *ProductRepo) SetStatus(ctx context.Context, orgID, id uuid.UUID, status domain.Status) error {
	const q = `UPDATE products SET status = $3 WHERE organisation_id = $1 AND id = $2`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, orgID, id, string(status))
	if err != nil {
		return fmt.Errorf("catalogue: setting product status: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *ProductRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Product, error) {
	const q = `
		SELECT id, organisation_id, category_id, brand_id, base_uom_id, name, COALESCE(description, ''), COALESCE(hsn_sac_code, ''), status, created_at, updated_at
		FROM products WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	return scanProduct(row)
}

func (r *ProductRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.Product, error) {
	const q = `
		SELECT id, organisation_id, category_id, brand_id, base_uom_id, name, COALESCE(description, ''), COALESCE(hsn_sac_code, ''), status, created_at, updated_at
		FROM products WHERE organisation_id = $1 ORDER BY name`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing products: %w", err)
	}
	defer rows.Close()
	return scanProducts(rows)
}

// SearchByName ranks by pg_trgm similarity against idx_products_name_trgm
// (migrations/0008_catalogue.up.sql) — the fast, fuzzy billing-counter
// search path (brief §24/§25).
func (r *ProductRepo) SearchByName(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]*domain.Product, error) {
	const q = `
		SELECT id, organisation_id, category_id, brand_id, base_uom_id, name, COALESCE(description, ''), COALESCE(hsn_sac_code, ''), status, created_at, updated_at
		FROM products
		WHERE organisation_id = $1 AND name % $2 AND status = 'ACTIVE'
		ORDER BY similarity(name, $2) DESC
		LIMIT $3`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("catalogue: searching products: %w", err)
	}
	defer rows.Close()
	return scanProducts(rows)
}

func scanProduct(row pgx.Row) (*domain.Product, error) {
	var p domain.Product
	var status string
	if err := row.Scan(&p.ID, &p.OrganisationID, &p.CategoryID, &p.BrandID, &p.BaseUOMID, &p.Name, &p.Description, &p.HSNSACCode, &status, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying product: %w", err)
	}
	p.Status = domain.Status(status)
	return &p, nil
}

func scanProducts(rows pgx.Rows) ([]*domain.Product, error) {
	var out []*domain.Product
	for rows.Next() {
		var p domain.Product
		var status string
		if err := rows.Scan(&p.ID, &p.OrganisationID, &p.CategoryID, &p.BrandID, &p.BaseUOMID, &p.Name, &p.Description, &p.HSNSACCode, &status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalogue: scanning product row: %w", err)
		}
		p.Status = domain.Status(status)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Product variants ---

type ProductVariantRepo struct{ pool *database.Pool }

func NewProductVariantRepo(pool *database.Pool) *ProductVariantRepo {
	return &ProductVariantRepo{pool: pool}
}

func (r *ProductVariantRepo) Create(ctx context.Context, v *domain.ProductVariant) error {
	attrs, err := json.Marshal(v.Attributes)
	if err != nil {
		return fmt.Errorf("catalogue: marshaling variant attributes: %w", err)
	}
	const q = `
		INSERT INTO product_variants (id, organisation_id, product_id, sku_code, attributes, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`
	_, err = r.pool.Q(ctx).Exec(ctx, q, v.ID, v.OrganisationID, v.ProductID, v.SKUCode, attrs, string(v.Status), v.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateSKU
		}
		return fmt.Errorf("catalogue: inserting product_variant: %w", err)
	}
	return nil
}

func (r *ProductVariantRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.ProductVariant, error) {
	const q = `SELECT id, organisation_id, product_id, sku_code, attributes, status, created_at, updated_at FROM product_variants WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	return scanVariant(row)
}

func (r *ProductVariantRepo) ListByProduct(ctx context.Context, productID uuid.UUID) ([]*domain.ProductVariant, error) {
	const q = `SELECT id, organisation_id, product_id, sku_code, attributes, status, created_at, updated_at FROM product_variants WHERE product_id = $1 ORDER BY sku_code`
	rows, err := r.pool.Q(ctx).Query(ctx, q, productID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing product_variants: %w", err)
	}
	defer rows.Close()
	var out []*domain.ProductVariant
	for rows.Next() {
		v, err := scanVariantRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *ProductVariantRepo) GetBySKU(ctx context.Context, orgID uuid.UUID, skuCode string) (*domain.ProductVariant, error) {
	const q = `SELECT id, organisation_id, product_id, sku_code, attributes, status, created_at, updated_at FROM product_variants WHERE organisation_id = $1 AND sku_code = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, skuCode)
	return scanVariant(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanVariant(row pgx.Row) (*domain.ProductVariant, error) {
	v, err := scanVariantRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying product_variant: %w", err)
	}
	return v, nil
}

func scanVariantRow(row scannable) (*domain.ProductVariant, error) {
	var v domain.ProductVariant
	var status string
	var attrs []byte
	if err := row.Scan(&v.ID, &v.OrganisationID, &v.ProductID, &v.SKUCode, &attrs, &status, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	v.Status = domain.Status(status)
	if len(attrs) > 0 {
		if err := json.Unmarshal(attrs, &v.Attributes); err != nil {
			return nil, fmt.Errorf("catalogue: unmarshaling variant attributes: %w", err)
		}
	}
	return &v, nil
}

// --- Barcodes ---

type BarcodeRepo struct{ pool *database.Pool }

func NewBarcodeRepo(pool *database.Pool) *BarcodeRepo { return &BarcodeRepo{pool: pool} }

func (r *BarcodeRepo) Create(ctx context.Context, b *domain.Barcode) error {
	const q = `
		INSERT INTO product_barcodes (id, organisation_id, variant_id, unit_id, barcode, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, b.ID, b.OrganisationID, b.VariantID, b.UnitID, b.Barcode, b.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return fmt.Errorf("catalogue: %w", errors.New("barcode already in use"))
		}
		return fmt.Errorf("catalogue: inserting product_barcode: %w", err)
	}
	return nil
}

func (r *BarcodeRepo) ListByVariant(ctx context.Context, variantID uuid.UUID) ([]*domain.Barcode, error) {
	const q = `SELECT id, organisation_id, variant_id, unit_id, barcode, created_at FROM product_barcodes WHERE variant_id = $1`
	rows, err := r.pool.Q(ctx).Query(ctx, q, variantID)
	if err != nil {
		return nil, fmt.Errorf("catalogue: listing product_barcodes: %w", err)
	}
	defer rows.Close()
	var out []*domain.Barcode
	for rows.Next() {
		var b domain.Barcode
		if err := rows.Scan(&b.ID, &b.OrganisationID, &b.VariantID, &b.UnitID, &b.Barcode, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("catalogue: scanning product_barcode row: %w", err)
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

func (r *BarcodeRepo) GetByBarcode(ctx context.Context, orgID uuid.UUID, barcode string) (*domain.Barcode, error) {
	const q = `SELECT id, organisation_id, variant_id, unit_id, barcode, created_at FROM product_barcodes WHERE organisation_id = $1 AND barcode = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, barcode)
	var b domain.Barcode
	if err := row.Scan(&b.ID, &b.OrganisationID, &b.VariantID, &b.UnitID, &b.Barcode, &b.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("catalogue: querying product_barcode: %w", err)
	}
	return &b, nil
}
