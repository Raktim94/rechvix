// Package domain holds the pricing module's entity types and repository
// interfaces (docs/architecture.md §2).
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/money"
)

type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusInactive Status = "INACTIVE"
)

type PriceList struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	Name           string
	CurrencyCode   string
	IsDefault      bool
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type PriceListItem struct {
	ID               uuid.UUID
	OrganisationID   uuid.UUID
	PriceListID      uuid.UUID
	ProductVariantID uuid.UUID
	UnitID           uuid.UUID
	Price            money.Money
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type PriceListRepository interface {
	Create(ctx context.Context, pl *PriceList) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*PriceList, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*PriceList, error)
}

type PriceListItemRepository interface {
	Upsert(ctx context.Context, item *PriceListItem) error
	ListByPriceList(ctx context.Context, priceListID uuid.UUID) ([]*PriceListItem, error)
	// Resolve is the billing-screen price lookup: "what does this variant,
	// in this unit, cost on this price list" (brief §48 — the sales screen
	// must show product price alongside stock/tax at billing speed). Not
	// consumed outside this module yet — Stage 5 (sales) is the real
	// caller — but the query belongs with the schema it reads.
	Resolve(ctx context.Context, priceListID, variantID, unitID uuid.UUID) (*PriceListItem, error)
	// DeleteByVariant removes every price_list_item (across every price
	// list) for a variant that's about to be hard-deleted — see
	// catalogue/app.DeletePriceHookFunc's own doc comment for the caller.
	DeleteByVariant(ctx context.Context, orgID, variantID uuid.UUID) error
}
