// Package app is the catalogue module's application/use-case layer:
// permission checks, transactions, and audit logging around the domain
// repositories (docs/architecture.md §2). Mirrors
// internal/modules/organisation/app's shape.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/catalogue/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool            database.Runner
	units           domain.UnitOfMeasureRepository
	unitConversions domain.UnitConversionRepository
	categories      domain.CategoryRepository
	brands          domain.BrandRepository
	products        domain.ProductRepository
	variants        domain.ProductVariantRepository
	barcodes        domain.BarcodeRepository
	permissions     *permissions.Checker
	audit           audit.Recorder
	now             func() time.Time
}

func NewService(
	pool database.Runner,
	units domain.UnitOfMeasureRepository,
	unitConversions domain.UnitConversionRepository,
	categories domain.CategoryRepository,
	brands domain.BrandRepository,
	products domain.ProductRepository,
	variants domain.ProductVariantRepository,
	barcodes domain.BarcodeRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{
		pool: pool, units: units, unitConversions: unitConversions, categories: categories,
		brands: brands, products: products, variants: variants, barcodes: barcodes,
		permissions: checker, audit: recorder, now: time.Now,
	}
}

func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "catalogue.view", permissions.Scope{})
}

func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "catalogue.manage", permissions.Scope{})
}

// --- Units of measure ---

type CreateUnitOfMeasureParams struct {
	Code string
	Name string
}

func (s *Service) CreateUnitOfMeasure(ctx context.Context, principal permissions.Principal, p CreateUnitOfMeasureParams) (*domain.UnitOfMeasure, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating unit_of_measure id: %w", err)
	}
	now := s.now()
	u := &domain.UnitOfMeasure{ID: id, OrganisationID: principal.OrganisationID, Code: p.Code, Name: p.Name, CreatedAt: now, UpdatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.units.Create(ctx, u); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "unit_of_measure.created", EntityType: "unit_of_measure", EntityID: &id,
			AfterState: map[string]any{"code": p.Code, "name": p.Name}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Service) ListUnitsOfMeasure(ctx context.Context, principal permissions.Principal) ([]*domain.UnitOfMeasure, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.UnitOfMeasure
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.units.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

type CreateUnitConversionParams struct {
	FromUnitID uuid.UUID
	ToUnitID   uuid.UUID
	Factor     decimal.Decimal
}

func (s *Service) CreateUnitConversion(ctx context.Context, principal permissions.Principal, p CreateUnitConversionParams) (*domain.UnitConversion, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if !p.Factor.IsPositive() {
		return nil, fmt.Errorf("catalogue: conversion factor must be positive")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating unit_conversion id: %w", err)
	}
	now := s.now()
	c := &domain.UnitConversion{ID: id, OrganisationID: principal.OrganisationID, FromUnitID: p.FromUnitID, ToUnitID: p.ToUnitID, Factor: p.Factor, CreatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.unitConversions.Create(ctx, c); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "unit_conversion.created", EntityType: "unit_conversion", EntityID: &id,
			AfterState: map[string]any{"from_unit_id": p.FromUnitID, "to_unit_id": p.ToUnitID, "factor": p.Factor.String()}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// --- Categories ---

func (s *Service) CreateCategory(ctx context.Context, principal permissions.Principal, name string, parentID *uuid.UUID) (*domain.Category, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating category id: %w", err)
	}
	now := s.now()
	c := &domain.Category{ID: id, OrganisationID: principal.OrganisationID, ParentID: parentID, Name: name, CreatedAt: now, UpdatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.categories.Create(ctx, c); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "category.created", EntityType: "category", EntityID: &id, AfterState: map[string]any{"name": name}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) ListCategories(ctx context.Context, principal permissions.Principal) ([]*domain.Category, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.Category
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.categories.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

// --- Brands ---

func (s *Service) CreateBrand(ctx context.Context, principal permissions.Principal, name string) (*domain.Brand, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating brand id: %w", err)
	}
	now := s.now()
	b := &domain.Brand{ID: id, OrganisationID: principal.OrganisationID, Name: name, CreatedAt: now, UpdatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.brands.Create(ctx, b); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "brand.created", EntityType: "brand", EntityID: &id, AfterState: map[string]any{"name": name}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) ListBrands(ctx context.Context, principal permissions.Principal) ([]*domain.Brand, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.Brand
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.brands.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

// --- Products ---

type CreateProductParams struct {
	CategoryID  *uuid.UUID
	BrandID     *uuid.UUID
	BaseUOMID   uuid.UUID
	Name        string
	Description string
	HSNSACCode  string
}

func (s *Service) CreateProduct(ctx context.Context, principal permissions.Principal, p CreateProductParams) (*domain.Product, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating product id: %w", err)
	}
	now := s.now()
	prod := &domain.Product{
		ID: id, OrganisationID: principal.OrganisationID, CategoryID: p.CategoryID, BrandID: p.BrandID,
		BaseUOMID: p.BaseUOMID, Name: p.Name, Description: p.Description, HSNSACCode: p.HSNSACCode,
		Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.products.Create(ctx, prod); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "product.created", EntityType: "product", EntityID: &id,
			AfterState: map[string]any{"name": p.Name, "hsn_sac_code": p.HSNSACCode}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return prod, nil
}

// UpdateProductParams mirrors CreateProductParams — the edit form always
// resends the full set of editable fields it loaded, so this is a full
// replace, not a partial patch (same convention as ProductRepository.
// Update itself).
type UpdateProductParams struct {
	CategoryID  *uuid.UUID
	BrandID     *uuid.UUID
	BaseUOMID   uuid.UUID
	Name        string
	Description string
	HSNSACCode  string
}

func (s *Service) UpdateProduct(ctx context.Context, principal permissions.Principal, id uuid.UUID, p UpdateProductParams) (*domain.Product, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	now := s.now()
	prod := &domain.Product{
		ID: id, OrganisationID: principal.OrganisationID, CategoryID: p.CategoryID, BrandID: p.BrandID,
		BaseUOMID: p.BaseUOMID, Name: p.Name, Description: p.Description, HSNSACCode: p.HSNSACCode, UpdatedAt: now,
	}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.products.Update(ctx, prod); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "product.updated", EntityType: "product", EntityID: &id,
			AfterState: map[string]any{"name": p.Name, "hsn_sac_code": p.HSNSACCode}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.GetProduct(ctx, principal, id)
}

// SetProductStatus is what the catalogue UI's "Delete"/"Restore" actions
// actually call — see ProductRepository.SetStatus's doc comment for why
// this is a status flip, not a row delete.
func (s *Service) SetProductStatus(ctx context.Context, principal permissions.Principal, id uuid.UUID, status domain.Status) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	now := s.now()
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.products.SetStatus(ctx, principal.OrganisationID, id, status); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "product.status_changed", EntityType: "product", EntityID: &id,
			AfterState: map[string]any{"status": string(status)}, At: now,
		})
	})
}

// BulkSetProductStatus applies SetProductStatus to every id in one
// transaction (the multi-select "Delete N products" action) — all-or-
// nothing, same as a single delete, so a bad id in the batch doesn't
// leave the selection half-deleted.
func (s *Service) BulkSetProductStatus(ctx context.Context, principal permissions.Principal, ids []uuid.UUID, status domain.Status) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	now := s.now()
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		for _, id := range ids {
			if err := s.products.SetStatus(ctx, principal.OrganisationID, id, status); err != nil {
				return err
			}
			if err := s.audit.Record(ctx, audit.Entry{
				OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
				Action: "product.status_changed", EntityType: "product", EntityID: &id,
				AfterState: map[string]any{"status": string(status)}, At: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) GetProduct(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*domain.Product, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.Product
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.products.GetByID(ctx, principal.OrganisationID, id)
		return err
	})
	return result, err
}

func (s *Service) ListProducts(ctx context.Context, principal permissions.Principal) ([]*domain.Product, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.Product
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.products.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

func (s *Service) SearchProducts(ctx context.Context, principal permissions.Principal, query string, limit int) ([]*domain.Product, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var result []*domain.Product
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.products.SearchByName(ctx, principal.OrganisationID, query, limit)
		return err
	})
	return result, err
}

// --- Product variants ---

type CreateVariantParams struct {
	ProductID  uuid.UUID
	SKUCode    string
	Attributes map[string]any
}

func (s *Service) CreateVariant(ctx context.Context, principal permissions.Principal, p CreateVariantParams) (*domain.ProductVariant, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating product_variant id: %w", err)
	}
	now := s.now()
	attrs := p.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	v := &domain.ProductVariant{
		ID: id, OrganisationID: principal.OrganisationID, ProductID: p.ProductID, SKUCode: p.SKUCode,
		Attributes: attrs, Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.variants.Create(ctx, v); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "product_variant.created", EntityType: "product_variant", EntityID: &id,
			AfterState: map[string]any{"sku_code": p.SKUCode}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// GetVariantWithProduct returns a variant and its parent product together
// — added for Stage 5b (sales), which needs a line's HSN/SAC and base
// unit of measure (both live on Product, not ProductVariant) starting
// from just the variant ID a sales line references. Follows
// docs/architecture.md §2 ("cross-module calls go through the other
// module's application-layer interface") rather than sales reaching into
// catalogue's repositories directly.
func (s *Service) GetVariantWithProduct(ctx context.Context, principal permissions.Principal, variantID uuid.UUID) (*domain.ProductVariant, *domain.Product, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, nil, err
	}
	var variant *domain.ProductVariant
	var product *domain.Product
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		variant, err = s.variants.GetByID(ctx, principal.OrganisationID, variantID)
		if err != nil {
			return err
		}
		product, err = s.products.GetByID(ctx, principal.OrganisationID, variant.ProductID)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return variant, product, nil
}

func (s *Service) ListVariantsByProduct(ctx context.Context, principal permissions.Principal, productID uuid.UUID) ([]*domain.ProductVariant, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.ProductVariant
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.variants.ListByProduct(ctx, productID)
		return err
	})
	return result, err
}

// --- Barcodes ---

type AddBarcodeParams struct {
	VariantID uuid.UUID
	UnitID    uuid.UUID
	Barcode   string
}

func (s *Service) AddBarcode(ctx context.Context, principal permissions.Principal, p AddBarcodeParams) (*domain.Barcode, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("catalogue: generating barcode id: %w", err)
	}
	now := s.now()
	b := &domain.Barcode{ID: id, OrganisationID: principal.OrganisationID, VariantID: p.VariantID, UnitID: p.UnitID, Barcode: p.Barcode, CreatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.barcodes.Create(ctx, b); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "barcode.created", EntityType: "product_barcode", EntityID: &id,
			AfterState: map[string]any{"barcode": p.Barcode}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// LookupBarcode is the billing-counter scan path — kept as a single fast
// call so a future HTTP handler doesn't need to orchestrate multiple
// round trips per scan (brief §25).
func (s *Service) LookupBarcode(ctx context.Context, principal permissions.Principal, barcode string) (*domain.Barcode, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.Barcode
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.barcodes.GetByBarcode(ctx, principal.OrganisationID, barcode)
		return err
	})
	return result, err
}
