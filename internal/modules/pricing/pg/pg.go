// Package pg is the pricing module's PostgreSQL repository implementation.
package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/pricing/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
)

// --- Price lists ---

type PriceListRepo struct{ pool *database.Pool }

func NewPriceListRepo(pool *database.Pool) *PriceListRepo { return &PriceListRepo{pool: pool} }

func (r *PriceListRepo) Create(ctx context.Context, pl *domain.PriceList) error {
	const q = `
		INSERT INTO price_lists (id, organisation_id, name, currency_code, is_default, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, pl.ID, pl.OrganisationID, pl.Name, pl.CurrencyCode, pl.IsDefault, string(pl.Status), pl.CreatedAt)
	if err != nil {
		return fmt.Errorf("pricing: inserting price_list: %w", err)
	}
	return nil
}

func (r *PriceListRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.PriceList, error) {
	const q = `SELECT id, organisation_id, name, currency_code, is_default, status, created_at, updated_at FROM price_lists WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	return scanPriceList(row)
}

func (r *PriceListRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.PriceList, error) {
	const q = `SELECT id, organisation_id, name, currency_code, is_default, status, created_at, updated_at FROM price_lists WHERE organisation_id = $1 ORDER BY name`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("pricing: listing price_lists: %w", err)
	}
	defer rows.Close()
	var out []*domain.PriceList
	for rows.Next() {
		pl, err := scanPriceListRow(rows)
		if err != nil {
			return nil, fmt.Errorf("pricing: scanning price_list row: %w", err)
		}
		out = append(out, pl)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanPriceList(row pgx.Row) (*domain.PriceList, error) {
	pl, err := scanPriceListRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("pricing: querying price_list: %w", err)
	}
	return pl, nil
}

func scanPriceListRow(row scannable) (*domain.PriceList, error) {
	var pl domain.PriceList
	var status string
	if err := row.Scan(&pl.ID, &pl.OrganisationID, &pl.Name, &pl.CurrencyCode, &pl.IsDefault, &status, &pl.CreatedAt, &pl.UpdatedAt); err != nil {
		return nil, err
	}
	pl.Status = domain.Status(status)
	return &pl, nil
}

// --- Price list items ---

type PriceListItemRepo struct{ pool *database.Pool }

func NewPriceListItemRepo(pool *database.Pool) *PriceListItemRepo {
	return &PriceListItemRepo{pool: pool}
}

// Upsert relies on the same UNIQUE(organisation_id, price_list_id,
// product_variant_id, unit_id) constraint that migrations/0010_pricing.up.sql
// declares — one price per variant+unit per list, replaced in place rather
// than accumulating stale duplicate rows every time a price is revised.
func (r *PriceListItemRepo) Upsert(ctx context.Context, item *domain.PriceListItem) error {
	const q = `
		INSERT INTO price_list_items (id, organisation_id, price_list_id, product_variant_id, unit_id, price_amount, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (organisation_id, price_list_id, product_variant_id, unit_id)
		DO UPDATE SET price_amount = EXCLUDED.price_amount, updated_at = EXCLUDED.updated_at
		RETURNING id, created_at`
	row := r.pool.Q(ctx).QueryRow(ctx, q, item.ID, item.OrganisationID, item.PriceListID, item.ProductVariantID, item.UnitID, item.Price.Decimal(), item.CreatedAt)
	if err := row.Scan(&item.ID, &item.CreatedAt); err != nil {
		return fmt.Errorf("pricing: upserting price_list_item: %w", err)
	}
	return nil
}

// DeleteByVariant removes every price entry for variantID across every
// price list in the organisation — see
// domain.PriceListItemRepository.DeleteByVariant's own doc comment.
func (r *PriceListItemRepo) DeleteByVariant(ctx context.Context, orgID, variantID uuid.UUID) error {
	const q = `DELETE FROM price_list_items WHERE organisation_id = $1 AND product_variant_id = $2`
	if _, err := r.pool.Q(ctx).Exec(ctx, q, orgID, variantID); err != nil {
		return fmt.Errorf("pricing: deleting price_list_items: %w", err)
	}
	return nil
}

func (r *PriceListItemRepo) ListByPriceList(ctx context.Context, priceListID uuid.UUID) ([]*domain.PriceListItem, error) {
	const q = `
		SELECT i.id, i.organisation_id, i.price_list_id, i.product_variant_id, i.unit_id, i.price_amount, i.created_at, i.updated_at, l.currency_code
		FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
		WHERE i.price_list_id = $1`
	rows, err := r.pool.Q(ctx).Query(ctx, q, priceListID)
	if err != nil {
		return nil, fmt.Errorf("pricing: listing price_list_items: %w", err)
	}
	defer rows.Close()
	var out []*domain.PriceListItem
	for rows.Next() {
		item, err := scanPriceListItem(rows)
		if err != nil {
			return nil, fmt.Errorf("pricing: scanning price_list_item row: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *PriceListItemRepo) Resolve(ctx context.Context, priceListID, variantID, unitID uuid.UUID) (*domain.PriceListItem, error) {
	const q = `
		SELECT i.id, i.organisation_id, i.price_list_id, i.product_variant_id, i.unit_id, i.price_amount, i.created_at, i.updated_at, l.currency_code
		FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
		WHERE i.price_list_id = $1 AND i.product_variant_id = $2 AND i.unit_id = $3`
	row := r.pool.Q(ctx).QueryRow(ctx, q, priceListID, variantID, unitID)
	item, err := scanPriceListItem(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("pricing: resolving price_list_item: %w", err)
	}
	return item, nil
}

func scanPriceListItem(row scannable) (*domain.PriceListItem, error) {
	var item domain.PriceListItem
	var amount decimal.Decimal
	var currencyCode string
	if err := row.Scan(&item.ID, &item.OrganisationID, &item.PriceListID, &item.ProductVariantID, &item.UnitID, &amount, &item.CreatedAt, &item.UpdatedAt, &currencyCode); err != nil {
		return nil, err
	}
	m, err := money.New(amount, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("constructing price money value: %w", err)
	}
	item.Price = m
	return &item, nil
}
