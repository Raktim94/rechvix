// Package app is the pricing module's application/use-case layer.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/pricing/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool        database.Runner
	priceLists  domain.PriceListRepository
	items       domain.PriceListItemRepository
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(
	pool database.Runner,
	priceLists domain.PriceListRepository,
	items domain.PriceListItemRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{pool: pool, priceLists: priceLists, items: items, permissions: checker, audit: recorder, now: time.Now}
}

// view/manage use Checker.HasAny, not Require — price lists have no
// legal_entity_id of their own, shared org-wide across every company,
// same rationale as catalogue/app.Service.view/manage's identical
// comment.
func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.HasAny(ctx, principal, "pricing.view")
}

func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.HasAny(ctx, principal, "pricing.manage")
}

type CreatePriceListParams struct {
	Name         string
	CurrencyCode string
	IsDefault    bool
}

func (s *Service) CreatePriceList(ctx context.Context, principal permissions.Principal, p CreatePriceListParams) (*domain.PriceList, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("pricing: generating price_list id: %w", err)
	}
	now := s.now()
	pl := &domain.PriceList{ID: id, OrganisationID: principal.OrganisationID, Name: p.Name, CurrencyCode: p.CurrencyCode, IsDefault: p.IsDefault, Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.priceLists.Create(ctx, pl); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "price_list.created", EntityType: "price_list", EntityID: &id,
			AfterState: map[string]any{"name": p.Name, "currency_code": p.CurrencyCode}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return pl, nil
}

func (s *Service) GetPriceList(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*domain.PriceList, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.PriceList
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.priceLists.GetByID(ctx, principal.OrganisationID, id)
		return err
	})
	return result, err
}

// EnsureDefaultPriceList returns the organisation's one price list,
// creating it (named "Default", IsDefault=true) if none exists yet —
// same "auto-provision required setup instead of erroring" pattern as
// catalogue/app.Service.EnsureDefaultUnits. This is what makes a fresh
// install's CSV price column (catalogue.ImportProducts' price-hook,
// wired in apps/server/main.go) and the New Product form's own Price
// field work with zero setup: previously, a fresh organisation with no
// price list yet made both silently fail to set anything, with only a
// per-row import-report note as any indication — see this method's own
// introduction for the bug report that prompted it. If more than one
// price list already exists (pre-dating this method, or created
// directly via CreatePriceList), the one marked IsDefault wins, else
// simply the first — never creates a second list once any exist.
func (s *Service) EnsureDefaultPriceList(ctx context.Context, principal permissions.Principal, currencyCode string) (*domain.PriceList, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.PriceList
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		existing, err := s.priceLists.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			result = existing[0]
			for _, pl := range existing {
				if pl.IsDefault {
					result = pl
					break
				}
			}
			return nil
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("pricing: generating price_list id: %w", err)
		}
		now := s.now()
		pl := &domain.PriceList{
			ID: id, OrganisationID: principal.OrganisationID, Name: "Default",
			CurrencyCode: currencyCode, IsDefault: true, Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.priceLists.Create(ctx, pl); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "price_list.created", EntityType: "price_list", EntityID: &id,
			AfterState: map[string]any{"name": "Default", "currency_code": currencyCode, "auto_created": true}, At: now,
		}); err != nil {
			return err
		}
		result = pl
		return nil
	})
	return result, err
}

func (s *Service) ListPriceLists(ctx context.Context, principal permissions.Principal) ([]*domain.PriceList, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.PriceList
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.priceLists.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

type SetPriceParams struct {
	PriceListID      uuid.UUID
	ProductVariantID uuid.UUID
	UnitID           uuid.UUID
	Price            money.Money
}

// SetPrice creates or replaces the price for a variant+unit on a price
// list (upsert — see pg.PriceListItemRepo.Upsert for the constraint this
// relies on).
func (s *Service) SetPrice(ctx context.Context, principal permissions.Principal, p SetPriceParams) (*domain.PriceListItem, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if p.Price.IsNegative() {
		return nil, domain.ErrNegativePrice
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("pricing: generating price_list_item id: %w", err)
	}
	now := s.now()
	item := &domain.PriceListItem{ID: id, OrganisationID: principal.OrganisationID, PriceListID: p.PriceListID, ProductVariantID: p.ProductVariantID, UnitID: p.UnitID, Price: p.Price, CreatedAt: now, UpdatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.items.Upsert(ctx, item); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "price_list_item.set", EntityType: "price_list_item", EntityID: &item.ID,
			AfterState: map[string]any{"price_list_id": p.PriceListID, "product_variant_id": p.ProductVariantID, "price": p.Price.StringFixed(money.RoundHalfUp)},
			At:         now,
		})
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

// DeletePricesForVariant backs catalogue/app.DeletePriceHookFunc — see
// that type's own doc comment for why a hard-deleted product's price
// entries need cleaning up here rather than catalogue touching this
// module's tables directly.
func (s *Service) DeletePricesForVariant(ctx context.Context, principal permissions.Principal, variantID uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.items.DeleteByVariant(ctx, principal.OrganisationID, variantID)
	})
}

func (s *Service) ListPrices(ctx context.Context, principal permissions.Principal, priceListID uuid.UUID) ([]*domain.PriceListItem, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.PriceListItem
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.items.ListByPriceList(ctx, priceListID)
		return err
	})
	return result, err
}

// ResolvePrice is the billing-screen lookup (brief §48). Stage 5's sales
// module is the intended real caller; exposed here too since "what does
// this cost right now" is a legitimate pricing-module query in its own
// right (e.g. a price-list management UI previewing a lookup).
func (s *Service) ResolvePrice(ctx context.Context, principal permissions.Principal, priceListID, variantID, unitID uuid.UUID) (*domain.PriceListItem, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.PriceListItem
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.items.Resolve(ctx, priceListID, variantID, unitID)
		return err
	})
	return result, err
}
