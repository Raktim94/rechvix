// Package app is the inventory module's application/use-case layer.
// Mirrors internal/modules/catalogue/app's shape. RecordMovement is the
// one place stock_balances is ever written — every public entry point
// (opening stock, adjustment, transfer) and every other module's finalize
// flow (purchases' goods receipt) funnels through it, so the
// lock-then-update discipline that makes Scenario D hold lives in exactly
// one function.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	cataloguedomain "rechvix/internal/modules/catalogue/domain"
	"rechvix/internal/modules/inventory/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool            database.Runner
	movements       domain.StockMovementRepository
	balances        domain.StockBalanceRepository
	reservations    domain.StockReservationRepository
	batches         domain.StockBatchRepository
	costLots        domain.StockCostLotRepository
	serials         domain.SerialNumberRepository
	policies        domain.StockPolicyRepository
	transfers       domain.StockTransferRepository
	adjustments     domain.StockAdjustmentRepository
	variants        cataloguedomain.ProductVariantRepository
	products        cataloguedomain.ProductRepository
	unitConversions cataloguedomain.UnitConversionRepository
	costing         domain.CostingStrategy
	permissions     *permissions.Checker
	audit           audit.Recorder
	now             func() time.Time
}

func NewService(
	pool database.Runner,
	movements domain.StockMovementRepository,
	balances domain.StockBalanceRepository,
	reservations domain.StockReservationRepository,
	batches domain.StockBatchRepository,
	costLots domain.StockCostLotRepository,
	serials domain.SerialNumberRepository,
	policies domain.StockPolicyRepository,
	transfers domain.StockTransferRepository,
	adjustments domain.StockAdjustmentRepository,
	variants cataloguedomain.ProductVariantRepository,
	products cataloguedomain.ProductRepository,
	unitConversions cataloguedomain.UnitConversionRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{
		pool: pool, movements: movements, balances: balances, reservations: reservations,
		batches: batches, costLots: costLots, serials: serials, policies: policies, transfers: transfers, adjustments: adjustments,
		variants: variants, products: products, unitConversions: unitConversions,
		costing: domain.WeightedAverageCostingStrategy{}, permissions: checker, audit: recorder, now: time.Now,
	}
}

func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "inventory.view", permissions.Scope{})
}
func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "inventory.manage", permissions.Scope{})
}
func (s *Service) adjustPerm(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "inventory.adjust", permissions.Scope{})
}
func (s *Service) transferPerm(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "inventory.transfer", permissions.Scope{})
}

// RecordMovementParams describes one movement to post. Quantity is always
// positive and expressed in UnitID (the transacted unit, which may differ
// from the product's base stocking unit — e.g. receiving a BOX when the
// product is stocked in PCS); RecordMovement resolves the base-unit
// quantity itself via the product's unit_conversions.
type RecordMovementParams struct {
	WarehouseID      uuid.UUID
	ProductVariantID uuid.UUID
	MovementType     domain.MovementType
	UnitID           uuid.UUID
	Quantity         decimal.Decimal
	// UnitCost is required when domain.IsReceipt(MovementType) is true
	// (OPENING, PURCHASE_RECEIPT) and ignored otherwise — an outward
	// movement consumes stock at the existing weighted-average cost, it
	// doesn't set a new one.
	UnitCost        *decimal.Decimal
	BatchCode       *string
	ManufactureDate *time.Time
	ExpiryDate      *time.Time
	SerialCode      *string
	ReferenceType   string
	ReferenceID     *uuid.UUID
	Notes           string
}

// RecordMovementForOtherModule is the entry point another module's
// application layer calls to post a stock effect as part of its own
// already-permission-checked operation (e.g.
// purchases/app.Service.FinalizeDocument finalizing a GOODS_RECEIPT).
// Deliberately does NOT check any inventory.* permission itself — the
// caller's own permission (e.g. purchases.finalize) is what authorized
// this, and inventory shouldn't demand a second, redundant grant for an
// internal server-side call.
//
// The caller MUST already be inside an active database.RunScoped
// transaction for the same organisation (ctx must carry it) — this
// method takes a row lock via GetForUpdate, which only serializes
// correctly against concurrent movements when it participates in the
// caller's transaction. Calling this with no active transaction in ctx
// would still execute (database.Pool.Q falls back to the bare pool) but
// the lock would be released immediately after the single statement,
// defeating the point; there is no automated guard against this
// misuse today beyond this doc comment and the fact that every current
// caller (this package's own public methods, purchases.FinalizeDocument)
// already wraps its call in RunScoped.
func (s *Service) RecordMovementForOtherModule(ctx context.Context, orgID, actorUserID uuid.UUID, p RecordMovementParams) (*domain.StockMovement, error) {
	return s.recordMovement(ctx, orgID, actorUserID, p)
}

func (s *Service) recordMovement(ctx context.Context, orgID, actorUserID uuid.UUID, p RecordMovementParams) (*domain.StockMovement, error) {
	direction, ok := domain.DirectionOf(p.MovementType)
	if !ok {
		return nil, domain.ErrInvalidMovementType
	}
	if !p.Quantity.IsPositive() {
		return nil, fmt.Errorf("inventory: quantity must be positive")
	}
	if domain.IsReceipt(p.MovementType) && p.UnitCost == nil {
		return nil, fmt.Errorf("inventory: unit_cost is required for %s", p.MovementType)
	}

	baseUnitID, err := s.baseUnit(ctx, orgID, p.ProductVariantID)
	if err != nil {
		return nil, err
	}
	factor := decimal.NewFromInt(1)
	if p.UnitID != baseUnitID {
		factor, err = s.conversionFactor(ctx, orgID, p.UnitID, baseUnitID)
		if err != nil {
			return nil, err
		}
	}
	baseQty := p.Quantity.Mul(factor)

	var batchID *uuid.UUID
	if p.BatchCode != nil && *p.BatchCode != "" {
		b, err := s.batches.GetOrCreate(ctx, orgID, p.ProductVariantID, *p.BatchCode, p.ManufactureDate, p.ExpiryDate)
		if err != nil {
			return nil, fmt.Errorf("inventory: resolving batch: %w", err)
		}
		batchID = &b.ID
	}

	var serialID *uuid.UUID
	if p.SerialCode != nil && *p.SerialCode != "" {
		id, err := s.resolveSerial(ctx, orgID, p.ProductVariantID, p.WarehouseID, *p.SerialCode, direction)
		if err != nil {
			return nil, err
		}
		serialID = &id
	}

	// Lock the balance row BEFORE reading it, so a concurrent movement
	// against the same (warehouse, variant) blocks here instead of both
	// transactions computing a new value from the same stale read
	// (Scenario D: two operators selling the last unit simultaneously).
	balBefore, err := s.balances.GetForUpdate(ctx, orgID, p.WarehouseID, p.ProductVariantID)
	if err != nil {
		return nil, err
	}

	newQty := balBefore.QuantityOnHand
	newAvgCost := balBefore.AverageCost
	if direction == domain.DirectionIn {
		if domain.IsReceipt(p.MovementType) {
			// p.UnitCost is cost per UnitID (the transacted unit — e.g.
			// "cost per BOX"), matching how p.Quantity is also expressed
			// in the transacted unit. stock_balances.average_cost is
			// cost per the product's BASE unit (it has to be: it's
			// compared/blended against balances that may have been built
			// up from receipts in different transacted units over time).
			// Feeding a per-BOX cost directly into a per-PCS average
			// would silently corrupt the average by a factor of the
			// conversion ratio — normalize by the same factor already
			// used to convert quantity, before it ever reaches costing.
			baseUnitCost := p.UnitCost.Div(factor)
			newAvgCost = s.costing.OnReceipt(balBefore.QuantityOnHand, balBefore.AverageCost, baseQty, baseUnitCost)

			// Cost-lot ledger (migrations/0036): additive to the weighted
			// average above, not a replacement for it — see
			// domain.StockCostLotRepository's doc comment. Same price as
			// an existing lot adds to it; a different price opens a new
			// one, which is exactly what a distributor bill at a changed
			// price needs to produce.
			if _, err := s.costLots.UpsertReceipt(ctx, orgID, p.WarehouseID, p.ProductVariantID, baseUnitCost, baseQty, p.ReferenceType, p.ReferenceID); err != nil {
				return nil, fmt.Errorf("inventory: recording cost lot: %w", err)
			}
		}
		newQty = newQty.Add(baseQty)
	} else {
		policy, perr := s.policies.Get(ctx, orgID, p.WarehouseID, p.ProductVariantID)
		if perr != nil && !errors.Is(perr, domain.ErrNotFound) {
			return nil, fmt.Errorf("inventory: loading stock policy: %w", perr)
		}
		allowNegative := policy != nil && policy.AllowNegativeStock
		resultingQty := newQty.Sub(baseQty)
		if !allowNegative && resultingQty.IsNegative() {
			return nil, domain.ErrInsufficientStock
		}
		newQty = resultingQty

		// Best-effort FIFO drawdown of the cost-lot ledger — see
		// ConsumeFIFO's doc comment for why a lack of lot coverage (stock
		// received before this table existed) never blocks this movement.
		if err := s.costLots.ConsumeFIFO(ctx, orgID, p.WarehouseID, p.ProductVariantID, baseQty); err != nil {
			return nil, fmt.Errorf("inventory: consuming cost lot: %w", err)
		}
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("inventory: generating stock_movement id: %w", err)
	}
	now := s.now()
	mv := &domain.StockMovement{
		ID: id, OrganisationID: orgID, WarehouseID: p.WarehouseID, ProductVariantID: p.ProductVariantID,
		MovementType: p.MovementType, UnitID: p.UnitID, Quantity: p.Quantity, BaseQuantity: baseQty,
		UnitCost: p.UnitCost, BatchID: batchID, SerialNumberID: serialID,
		ReferenceType: p.ReferenceType, ReferenceID: p.ReferenceID, Notes: p.Notes,
		CreatedBy: actorUserID, CreatedAt: now,
	}
	if err := s.movements.Create(ctx, mv); err != nil {
		return nil, err
	}

	if err := s.balances.Upsert(ctx, domain.StockBalance{
		OrganisationID: orgID, WarehouseID: p.WarehouseID, ProductVariantID: p.ProductVariantID,
		QuantityOnHand: newQty, QuantityReserved: balBefore.QuantityReserved, AverageCost: newAvgCost,
	}); err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, audit.Entry{
		OrganisationID: orgID, ActorUserID: &actorUserID, ActorType: audit.ActorUser,
		Action: "stock_movement.recorded", EntityType: "stock_movement", EntityID: &id,
		AfterState: map[string]any{
			"movement_type": string(p.MovementType), "warehouse_id": p.WarehouseID, "product_variant_id": p.ProductVariantID,
			"quantity": p.Quantity.String(), "base_quantity": baseQty.String(),
		},
		At: now,
	}); err != nil {
		return nil, err
	}

	return mv, nil
}

func (s *Service) baseUnit(ctx context.Context, orgID, variantID uuid.UUID) (uuid.UUID, error) {
	v, err := s.variants.GetByID(ctx, orgID, variantID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("inventory: resolving product variant: %w", err)
	}
	p, err := s.products.GetByID(ctx, orgID, v.ProductID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("inventory: resolving product: %w", err)
	}
	return p.BaseUOMID, nil
}

// conversionFactor returns how many toUnitID units equal one fromUnitID
// unit, trying the conversion in either recorded direction (a
// unit_conversions row for A->B also defines B->A via UnitConversion.Invert,
// per catalogue/domain — a business shouldn't have to enter both
// directions).
func (s *Service) conversionFactor(ctx context.Context, orgID, fromUnitID, toUnitID uuid.UUID) (decimal.Decimal, error) {
	if fromUnitID == toUnitID {
		return decimal.NewFromInt(1), nil
	}
	c, err := s.unitConversions.Find(ctx, orgID, fromUnitID, toUnitID)
	if err == nil {
		return c.Factor, nil
	}
	if !errors.Is(err, cataloguedomain.ErrNotFound) {
		return decimal.Decimal{}, fmt.Errorf("inventory: looking up unit conversion: %w", err)
	}
	c2, err2 := s.unitConversions.Find(ctx, orgID, toUnitID, fromUnitID)
	if err2 != nil {
		return decimal.Decimal{}, fmt.Errorf("inventory: no unit conversion defined between the transacted unit and the product's base unit")
	}
	return c2.Invert().Factor, nil
}

func (s *Service) resolveSerial(ctx context.Context, orgID, variantID, warehouseID uuid.UUID, code string, direction domain.Direction) (uuid.UUID, error) {
	if direction == domain.DirectionIn {
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.UUID{}, fmt.Errorf("inventory: generating serial id: %w", err)
		}
		sn := &domain.SerialNumber{ID: id, OrganisationID: orgID, ProductVariantID: variantID, SerialCode: code,
			WarehouseID: &warehouseID, Status: domain.SerialInStock, CreatedAt: s.now(), UpdatedAt: s.now()}
		if err := s.serials.Create(ctx, sn); err != nil {
			return uuid.UUID{}, fmt.Errorf("inventory: recording serial number: %w", err)
		}
		return id, nil
	}
	sn, err := s.serials.GetByCode(ctx, orgID, variantID, code)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("inventory: looking up serial number %q: %w", code, err)
	}
	if err := s.serials.UpdateStatus(ctx, sn.ID, domain.SerialSold, nil); err != nil {
		return uuid.UUID{}, fmt.Errorf("inventory: updating serial number status: %w", err)
	}
	return sn.ID, nil
}

// --- Public, permission-checked entry points ---

func (s *Service) RecordOpeningStock(ctx context.Context, principal permissions.Principal, p RecordMovementParams) (*domain.StockMovement, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	p.MovementType = domain.MovementOpening
	var mv *domain.StockMovement
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		mv, err = s.recordMovement(ctx, principal.OrganisationID, principal.UserID, p)
		return err
	})
	if err != nil {
		return nil, err
	}
	return mv, nil
}

type AdjustmentLineParams struct {
	ProductVariantID uuid.UUID
	UnitID           uuid.UUID
	Quantity         decimal.Decimal
	// MovementType must be one of ADJUSTMENT_IN, ADJUSTMENT_OUT, DAMAGE, EXPIRY.
	MovementType domain.MovementType
	BatchCode    *string
}

type RecordAdjustmentParams struct {
	WarehouseID uuid.UUID
	Reason      string
	Notes       string
	Lines       []AdjustmentLineParams
}

func isAdjustmentMovementType(t domain.MovementType) bool {
	switch t {
	case domain.MovementAdjustmentIn, domain.MovementAdjustmentOut, domain.MovementDamage, domain.MovementExpiry:
		return true
	default:
		return false
	}
}

func (s *Service) RecordAdjustment(ctx context.Context, principal permissions.Principal, p RecordAdjustmentParams) (*domain.StockAdjustment, []*domain.StockMovement, error) {
	if err := s.adjustPerm(ctx, principal); err != nil {
		return nil, nil, err
	}
	if len(p.Lines) == 0 {
		return nil, nil, fmt.Errorf("inventory: adjustment must have at least one line")
	}
	for _, l := range p.Lines {
		if !isAdjustmentMovementType(l.MovementType) {
			return nil, nil, fmt.Errorf("inventory: %q is not a valid adjustment movement type", l.MovementType)
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("inventory: generating stock_adjustment id: %w", err)
	}
	now := s.now()
	adj := &domain.StockAdjustment{ID: id, OrganisationID: principal.OrganisationID, WarehouseID: p.WarehouseID,
		Reason: p.Reason, Notes: p.Notes, CreatedBy: principal.UserID, CreatedAt: now}
	var movements []*domain.StockMovement
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.adjustments.Create(ctx, adj); err != nil {
			return err
		}
		for _, line := range p.Lines {
			mv, err := s.recordMovement(ctx, principal.OrganisationID, principal.UserID, RecordMovementParams{
				WarehouseID: p.WarehouseID, ProductVariantID: line.ProductVariantID, MovementType: line.MovementType,
				UnitID: line.UnitID, Quantity: line.Quantity, BatchCode: line.BatchCode,
				ReferenceType: "stock_adjustment", ReferenceID: &adj.ID, Notes: p.Reason,
			})
			if err != nil {
				return err
			}
			movements = append(movements, mv)
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "stock_adjustment.created", EntityType: "stock_adjustment", EntityID: &adj.ID,
			AfterState: map[string]any{"reason": p.Reason, "line_count": len(p.Lines)}, At: now,
		})
	})
	if err != nil {
		return nil, nil, err
	}
	return adj, movements, nil
}

type RecordTransferParams struct {
	FromWarehouseID  uuid.UUID
	ToWarehouseID    uuid.UUID
	ProductVariantID uuid.UUID
	UnitID           uuid.UUID
	Quantity         decimal.Decimal
	Notes            string
}

func (s *Service) RecordTransfer(ctx context.Context, principal permissions.Principal, p RecordTransferParams) (*domain.StockTransfer, error) {
	if err := s.transferPerm(ctx, principal); err != nil {
		return nil, err
	}
	if p.FromWarehouseID == p.ToWarehouseID {
		return nil, fmt.Errorf("inventory: cannot transfer a warehouse to itself")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("inventory: generating stock_transfer id: %w", err)
	}
	now := s.now()
	t := &domain.StockTransfer{ID: id, OrganisationID: principal.OrganisationID, FromWarehouseID: p.FromWarehouseID,
		ToWarehouseID: p.ToWarehouseID, Status: domain.TransferCompleted, Notes: p.Notes, CreatedBy: principal.UserID, CreatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.transfers.Create(ctx, t); err != nil {
			return err
		}
		if _, err := s.recordMovement(ctx, principal.OrganisationID, principal.UserID, RecordMovementParams{
			WarehouseID: p.FromWarehouseID, ProductVariantID: p.ProductVariantID, MovementType: domain.MovementTransferOut,
			UnitID: p.UnitID, Quantity: p.Quantity, ReferenceType: "stock_transfer", ReferenceID: &t.ID, Notes: p.Notes,
		}); err != nil {
			return fmt.Errorf("transfer-out leg: %w", err)
		}
		if _, err := s.recordMovement(ctx, principal.OrganisationID, principal.UserID, RecordMovementParams{
			WarehouseID: p.ToWarehouseID, ProductVariantID: p.ProductVariantID, MovementType: domain.MovementTransferIn,
			UnitID: p.UnitID, Quantity: p.Quantity, ReferenceType: "stock_transfer", ReferenceID: &t.ID, Notes: p.Notes,
		}); err != nil {
			return fmt.Errorf("transfer-in leg: %w", err)
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "stock_transfer.created", EntityType: "stock_transfer", EntityID: &t.ID,
			AfterState: map[string]any{"from_warehouse_id": p.FromWarehouseID, "to_warehouse_id": p.ToWarehouseID, "quantity": p.Quantity.String()},
			At:         now,
		})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

type ReserveParams struct {
	WarehouseID      uuid.UUID
	ProductVariantID uuid.UUID
	Quantity         decimal.Decimal
	ReferenceType    string
	ReferenceID      uuid.UUID
}

// Reserve earmarks quantity without moving it (docs/architecture.md §6).
// Locks the balance row for the same reason recordMovement does — two
// concurrent reservations against the last unit of stock must not both
// succeed.
func (s *Service) Reserve(ctx context.Context, principal permissions.Principal, p ReserveParams) (*domain.StockReservation, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if !p.Quantity.IsPositive() {
		return nil, fmt.Errorf("inventory: reservation quantity must be positive")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("inventory: generating stock_reservation id: %w", err)
	}
	now := s.now()
	res := &domain.StockReservation{ID: id, OrganisationID: principal.OrganisationID, WarehouseID: p.WarehouseID,
		ProductVariantID: p.ProductVariantID, Quantity: p.Quantity, ReferenceType: p.ReferenceType, ReferenceID: p.ReferenceID,
		Status: domain.ReservationActive, CreatedAt: now}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		bal, err := s.balances.GetForUpdate(ctx, principal.OrganisationID, p.WarehouseID, p.ProductVariantID)
		if err != nil {
			return err
		}
		if bal.Available().LessThan(p.Quantity) {
			return domain.ErrInsufficientStock
		}
		if err := s.reservations.Create(ctx, res); err != nil {
			return err
		}
		sum, err := s.reservations.SumActive(ctx, principal.OrganisationID, p.WarehouseID, p.ProductVariantID)
		if err != nil {
			return err
		}
		bal.QuantityReserved = sum
		return s.balances.Upsert(ctx, bal)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) ReleaseReservation(ctx context.Context, principal permissions.Principal, reservationID, warehouseID, variantID uuid.UUID) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.reservations.Release(ctx, reservationID); err != nil {
			return err
		}
		bal, err := s.balances.GetForUpdate(ctx, principal.OrganisationID, warehouseID, variantID)
		if err != nil {
			return err
		}
		sum, err := s.reservations.SumActive(ctx, principal.OrganisationID, warehouseID, variantID)
		if err != nil {
			return err
		}
		bal.QuantityReserved = sum
		return s.balances.Upsert(ctx, bal)
	})
}

func (s *Service) GetBalance(ctx context.Context, principal permissions.Principal, warehouseID, variantID uuid.UUID) (domain.StockBalance, error) {
	if err := s.view(ctx, principal); err != nil {
		return domain.StockBalance{}, err
	}
	var bal domain.StockBalance
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		bal, err = s.balances.Get(ctx, principal.OrganisationID, warehouseID, variantID)
		return err
	})
	return bal, err
}

func (s *Service) ListMovements(ctx context.Context, principal permissions.Principal, warehouseID, variantID uuid.UUID, limit int) ([]*domain.StockMovement, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var result []*domain.StockMovement
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.movements.ListByVariantWarehouse(ctx, principal.OrganisationID, warehouseID, variantID, limit)
		return err
	})
	return result, err
}

// ListCostLots returns the priced receipt lots still carrying quantity
// for one product at one warehouse, oldest first — the Inventory page's
// "what did we actually pay for what's on the shelf" view, and what the
// purchases-review screen checks before an OCR-scanned bill's price
// would open a brand-new lot instead of adding to an existing one.
func (s *Service) ListCostLots(ctx context.Context, principal permissions.Principal, warehouseID, variantID uuid.UUID) ([]*domain.StockCostLot, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.StockCostLot
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.costLots.ListRemaining(ctx, principal.OrganisationID, warehouseID, variantID)
		return err
	})
	return result, err
}

func (s *Service) ListLowStock(ctx context.Context, principal permissions.Principal, warehouseID uuid.UUID) ([]domain.StockBalance, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []domain.StockBalance
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.balances.ListLowStock(ctx, principal.OrganisationID, warehouseID)
		return err
	})
	return result, err
}

func (s *Service) SetStockPolicy(ctx context.Context, principal permissions.Principal, p domain.StockPolicy) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	p.OrganisationID = principal.OrganisationID
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.policies.Upsert(ctx, p)
	})
}
