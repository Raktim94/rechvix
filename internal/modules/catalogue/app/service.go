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

// SetPriceHookFunc/SetTaxRateHookFunc are the layering-safe wiring
// docs/adr/0003-accounting-integration-point.md's point 6 describes,
// same pattern as identity's WithPostBootstrapHook and notifications'
// WithDocumentRenderer (apps/server/main.go): catalogue can't import
// pricing or gstindia directly (they depend on catalogue, not the other
// way around), but the composition root has both, so it wires these in
// as closures. Only ImportProducts (bulk CSV/XLSX import) calls them —
// see its own doc comment for why a price/tax-rate failure there is
// non-fatal to the row.
type SetPriceHookFunc func(ctx context.Context, principal permissions.Principal, variantID, unitID uuid.UUID, amount decimal.Decimal) error
type SetTaxRateHookFunc func(ctx context.Context, principal permissions.Principal, hsnSacCode string, gstRate decimal.Decimal) error

// SetOpeningStockHookFunc is the same layering-safe wiring as
// SetPriceHookFunc, one module over: catalogue can't import inventory
// directly, so the composition root wires this in as a closure over
// inventory.Service.RecordOpeningStock. Only ImportProducts' optional
// opening_qty/opening_cost columns call it, and only when the import
// request also carried a warehouse to record against (see ImportProducts'
// own doc comment) — same non-fatal-per-row treatment as price/gst_rate.
type SetOpeningStockHookFunc func(ctx context.Context, principal permissions.Principal, warehouseID, variantID, unitID uuid.UUID, quantity decimal.Decimal, unitCost decimal.Decimal) error

// DeletePriceHookFunc is SetPriceHookFunc's counterpart for hard-deleting
// a product (DeleteProductsIfUnused below) — best-effort cleanup of any
// price_list_items row for variantID before the variant itself is
// deleted, since pricing owns that table and catalogue can't touch it
// directly (same layering reason as the two hooks above). Unlike
// HasTransactionHistory's other 9 tables, a price entry is never treated
// as "history" that blocks a hard delete — it's just an attribute to
// clean up, same as a barcode.
type DeletePriceHookFunc func(ctx context.Context, principal permissions.Principal, variantID uuid.UUID) error

type Service struct {
	pool                database.Runner
	units               domain.UnitOfMeasureRepository
	unitConversions     domain.UnitConversionRepository
	categories          domain.CategoryRepository
	brands              domain.BrandRepository
	products            domain.ProductRepository
	variants            domain.ProductVariantRepository
	barcodes            domain.BarcodeRepository
	permissions         *permissions.Checker
	audit               audit.Recorder
	now                 func() time.Time
	setPriceHook        SetPriceHookFunc
	setTaxRateHook      SetTaxRateHookFunc
	deletePriceHook     DeletePriceHookFunc
	setOpeningStockHook SetOpeningStockHookFunc
}

// WithPriceHook/WithTaxRateHook/WithDeletePriceHook wire the optional
// cross-module hooks above — nil-guarded everywhere they're called, so a
// composition that doesn't set them just means ImportProducts never
// attempts to set a price/tax rate (the price/gst_rate CSV columns are
// simply ignored, same as before these hooks existed) and
// DeleteProductsIfUnused never attempts to clean up a price entry before
// a hard delete (harmless — an orphaned price_list_items row just never
// resolves to a real product afterward), not an error either way.
func (s *Service) WithPriceHook(f SetPriceHookFunc) *Service {
	s.setPriceHook = f
	return s
}
func (s *Service) WithTaxRateHook(f SetTaxRateHookFunc) *Service {
	s.setTaxRateHook = f
	return s
}
func (s *Service) WithDeletePriceHook(f DeletePriceHookFunc) *Service {
	s.deletePriceHook = f
	return s
}
func (s *Service) WithOpeningStockHook(f SetOpeningStockHookFunc) *Service {
	s.setOpeningStockHook = f
	return s
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

// view/manage use Checker.HasAny, not Require — products/categories/
// brands/units have no legal_entity_id of their own (they're shared
// org-wide across every company, unlike sales/purchase/inventory
// documents), so there is no per-company scope to enforce here in the
// first place; a team member restricted to one company (see
// identity.CreateTeamMemberParams.LegalEntityIDs) still holds
// company-scoped catalogue.* grants (every permission the OWNER role has
// gets scoped together) and must still be able to use the catalogue —
// Require(ctx, principal, code, Scope{}) would wrongly reject them since
// none of their grants is unrestricted.
func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.HasAny(ctx, principal, "catalogue.view")
}

func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.HasAny(ctx, principal, "catalogue.manage")
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

// defaultUnits is a starter set of common units of measure — without
// this, every fresh organisation has ZERO units, and nothing (creating
// a product, bulk CSV import, the billing counter) can proceed until
// someone manually adds at least one first. Deliberately small and
// generic (not India-GST-specific unit-quantity codes) since this is
// just a convenience starting point, fully editable/deletable
// afterward like any other unit.
var defaultUnits = []CreateUnitOfMeasureParams{
	{Code: "PCS", Name: "Pieces"},
	{Code: "KG", Name: "Kilogram"},
	{Code: "GM", Name: "Gram"},
	{Code: "LTR", Name: "Litre"},
	{Code: "ML", Name: "Millilitre"},
	{Code: "MTR", Name: "Metre"},
	{Code: "BOX", Name: "Box"},
	{Code: "DZN", Name: "Dozen"},
	{Code: "PKT", Name: "Packet"},
	{Code: "BAG", Name: "Bag"},
}

// EnsureDefaultUnits idempotently seeds orgID's units of measure with
// defaultUnits, if it doesn't already have any — same "second call is a
// silent no-op" contract as accounting.Service.EnsureDefaultChartOfAccounts,
// which this mirrors. Called once from the post-bootstrap hook for every
// new organisation going forward, and exposed as its own endpoint for an
// organisation that predates this (a one-click fix, not silently applied
// behind their back).
func (s *Service) EnsureDefaultUnits(ctx context.Context, principal permissions.Principal) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		existing, err := s.units.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return nil
		}
		now := s.now()
		for _, du := range defaultUnits {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("catalogue: generating unit_of_measure id: %w", err)
			}
			if err := s.units.Create(ctx, &domain.UnitOfMeasure{
				ID: id, OrganisationID: principal.OrganisationID, Code: du.Code, Name: du.Name, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return fmt.Errorf("catalogue: seeding unit %s: %w", du.Code, err)
			}
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "catalogue.default_units_seeded", EntityType: "organisation", EntityID: &principal.OrganisationID,
			AfterState: map[string]any{"unit_count": len(defaultUnits)}, At: now,
		})
	})
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

// DeleteOutcome reports, per bulk-delete call, which products were
// actually removed vs. which fell back to deactivation — see
// DeleteProductsIfUnused's own doc comment for why both are possible
// outcomes of the same "Delete" action.
type DeleteOutcome struct {
	HardDeleted []uuid.UUID
	Deactivated []uuid.UUID
}

// DeleteProductsIfUnused is what the catalogue UI's bulk "Delete" action
// calls. Per product: if ProductRepository.HasTransactionHistory says
// it's never been referenced by a sales/purchase document line or any
// inventory activity, it's permanently removed (barcodes and any price
// entry cleaned up first, then its variants, then the product row
// itself); otherwise it falls back to SetProductStatus(INACTIVE), the
// same soft-delete this UI action used exclusively before this method
// existed — a product with real history cannot be hard-deleted without
// violating the FK every one of those referencing tables holds back to
// product_variants(id) (HasTransactionHistory's own doc comment lists
// them). Every id's actual outcome is reported back in DeleteOutcome,
// never a silent "some vanished, some didn't" — same "no silent partial
// outcome" philosophy ImportProducts' own Report already follows.
//
// Each product is processed as its own sequence of short transactions
// (history check, then variant listing, then the actual delete) rather
// than one big transaction for the whole batch — deletePriceHook (when
// wired) self-scopes its own RunScoped the same way setPriceHook does,
// so it must run between two of this method's own RunScoped calls, never
// nested inside one (see purchases/app.Service.manageForBranch's doc
// comment for the general hazard this avoids).
func (s *Service) DeleteProductsIfUnused(ctx context.Context, principal permissions.Principal, ids []uuid.UUID) (DeleteOutcome, error) {
	if err := s.manage(ctx, principal); err != nil {
		return DeleteOutcome{}, err
	}
	var out DeleteOutcome
	now := s.now()
	for _, id := range ids {
		var hasHistory bool
		err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
			var err error
			hasHistory, err = s.products.HasTransactionHistory(ctx, principal.OrganisationID, id)
			return err
		})
		if err != nil {
			return out, err
		}
		if hasHistory {
			if err := s.SetProductStatus(ctx, principal, id, domain.StatusInactive); err != nil {
				return out, err
			}
			out.Deactivated = append(out.Deactivated, id)
			continue
		}

		var variants []*domain.ProductVariant
		err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
			var err error
			variants, err = s.variants.ListByProduct(ctx, id)
			return err
		})
		if err != nil {
			return out, err
		}
		if s.deletePriceHook != nil {
			for _, v := range variants {
				if err := s.deletePriceHook(ctx, principal, v.ID); err != nil {
					return out, fmt.Errorf("catalogue: cleaning up price for variant %s before delete: %w", v.ID, err)
				}
			}
		}
		err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
			for _, v := range variants {
				if err := s.barcodes.DeleteByVariant(ctx, v.ID); err != nil {
					return err
				}
			}
			if err := s.variants.DeleteByProduct(ctx, principal.OrganisationID, id); err != nil {
				return err
			}
			if err := s.products.Delete(ctx, principal.OrganisationID, id); err != nil {
				return err
			}
			return s.audit.Record(ctx, audit.Entry{
				OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
				Action: "product.deleted", EntityType: "product", EntityID: &id,
				AfterState: map[string]any{"hard_deleted": true}, At: now,
			})
		})
		if err != nil {
			return out, err
		}
		out.HardDeleted = append(out.HardDeleted, id)
	}
	return out, nil
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
