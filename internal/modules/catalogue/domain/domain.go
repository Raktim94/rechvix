// Package domain holds the catalogue module's entity types and repository
// interfaces (docs/architecture.md §2). No I/O, no framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusInactive Status = "INACTIVE"
)

type UnitOfMeasure struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	Code           string
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UnitConversion states "1 unit of FromUnitID = Factor units of ToUnitID"
// (brief §11), e.g. FromUnit=BOX, ToUnit=PCS, Factor=25.
type UnitConversion struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	FromUnitID     uuid.UUID
	ToUnitID       uuid.UUID
	Factor         decimal.Decimal
	CreatedAt      time.Time
}

// Convert applies the conversion to a quantity expressed in FromUnitID,
// returning the equivalent quantity in ToUnitID (e.g. 3 BOX * 25 = 75 PCS).
// Pure arithmetic, no I/O — Stage 4 (inventory) is the intended real
// caller, converting a transacted sale/purchase quantity into a product's
// stocking unit before writing a stock_movement row.
func (c UnitConversion) Convert(quantityInFromUnit decimal.Decimal) decimal.Decimal {
	return quantityInFromUnit.Mul(c.Factor)
}

// Invert returns the reverse conversion (ToUnitID -> FromUnitID), e.g. if
// 1 BOX = 25 PCS then Invert converts a PCS quantity back into BOX. Factor
// is guaranteed positive by both the app-layer check in
// catalogue/app.Service.CreateUnitConversion and the database CHECK
// constraint in migrations/0008_catalogue.up.sql, so this never divides by
// zero.
func (c UnitConversion) Invert() UnitConversion {
	return UnitConversion{
		ID: c.ID, OrganisationID: c.OrganisationID,
		FromUnitID: c.ToUnitID, ToUnitID: c.FromUnitID,
		Factor: decimal.NewFromInt(1).Div(c.Factor), CreatedAt: c.CreatedAt,
	}
}

type Category struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	ParentID       *uuid.UUID
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Brand struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	Name           string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Product struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	CategoryID     *uuid.UUID
	BrandID        *uuid.UUID
	BaseUOMID      uuid.UUID
	Name           string
	Description    string
	// HSN (goods) or SAC (services). Data modeling only here — see
	// migrations/0008_catalogue.up.sql; tax-calculation use of this field
	// is Stage 5 (gstindia).
	HSNSACCode string
	Status     Status
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ProductVariant struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	ProductID      uuid.UUID
	SKUCode        string
	// Attributes is a free-form bag (size, colour, ...) — see the schema
	// comment in migrations/0008_catalogue.up.sql for why this is jsonb
	// rather than fixed columns.
	Attributes map[string]any
	Status     Status
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Barcode struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	VariantID      uuid.UUID
	UnitID         uuid.UUID
	Barcode        string
	CreatedAt      time.Time
}

type UnitOfMeasureRepository interface {
	Create(ctx context.Context, u *UnitOfMeasure) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*UnitOfMeasure, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*UnitOfMeasure, error)
}

type UnitConversionRepository interface {
	Create(ctx context.Context, c *UnitConversion) error
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*UnitConversion, error)
	// Find returns the conversion between two specific units, if one is
	// defined, so a caller can convert a transacted quantity into a
	// product's stocking unit (brief §11). Not yet used outside this
	// module in Stage 3 — inventory (Stage 4) is the real consumer.
	Find(ctx context.Context, orgID, fromUnitID, toUnitID uuid.UUID) (*UnitConversion, error)
}

type CategoryRepository interface {
	Create(ctx context.Context, c *Category) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Category, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*Category, error)
}

type BrandRepository interface {
	Create(ctx context.Context, b *Brand) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Brand, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*Brand, error)
}

type ProductRepository interface {
	Create(ctx context.Context, p *Product) error
	// Update overwrites every editable field (category/brand/unit/name/
	// description/HSN) of an existing product, scoped by
	// OrganisationID+ID — a full replace, not a partial patch, matching
	// how the edit form always resends the complete set of fields it
	// loaded. Does not touch Status; see SetStatus for that.
	Update(ctx context.Context, p *Product) error
	// SetStatus is the "delete"/"restore" a product actually does —
	// products are never hard-deleted (they're referenced by historical
	// sales/purchase lines and stock movements that must keep resolving),
	// so "delete" flips Status to INACTIVE. Returns ErrNotFound if no row
	// matches orgID+id.
	SetStatus(ctx context.Context, orgID, id uuid.UUID, status Status) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Product, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*Product, error)
	// SearchByName does a trigram similarity search against the
	// idx_products_name_trgm index (migrations/0008_catalogue.up.sql).
	// Only ever returns ACTIVE products — this is the billing-counter/
	// quick-add search path (sales.Service.BillingLookup), and a
	// "deleted" (INACTIVE) product must not be addable to a new sale.
	SearchByName(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]*Product, error)
}

type ProductVariantRepository interface {
	Create(ctx context.Context, v *ProductVariant) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*ProductVariant, error)
	ListByProduct(ctx context.Context, productID uuid.UUID) ([]*ProductVariant, error)
	GetBySKU(ctx context.Context, orgID uuid.UUID, skuCode string) (*ProductVariant, error)
}

type BarcodeRepository interface {
	Create(ctx context.Context, b *Barcode) error
	ListByVariant(ctx context.Context, variantID uuid.UUID) ([]*Barcode, error)
	// GetByBarcode is the billing-counter scan lookup — must stay a single
	// indexed exact-match query (brief §25: feels instantaneous).
	GetByBarcode(ctx context.Context, orgID uuid.UUID, barcode string) (*Barcode, error)
}
