// Package domain holds the inventory module's entity types, the
// movement-direction/costing rules, and repository interfaces
// (docs/architecture.md §2, §6, brief §11). No I/O, no framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type MovementType string

const (
	MovementOpening         MovementType = "OPENING"
	MovementPurchaseReceipt MovementType = "PURCHASE_RECEIPT"
	MovementPurchaseReturn  MovementType = "PURCHASE_RETURN"
	MovementSale            MovementType = "SALE"
	MovementSaleReturn      MovementType = "SALE_RETURN"
	MovementTransferOut     MovementType = "TRANSFER_OUT"
	MovementTransferIn      MovementType = "TRANSFER_IN"
	MovementAdjustmentIn    MovementType = "ADJUSTMENT_IN"
	MovementAdjustmentOut   MovementType = "ADJUSTMENT_OUT"
	MovementAssemblyIn      MovementType = "ASSEMBLY_IN"
	MovementAssemblyOut     MovementType = "ASSEMBLY_OUT"
	MovementDamage          MovementType = "DAMAGE"
	MovementExpiry          MovementType = "EXPIRY"
)

// Direction is which way a movement_type moves stock_balances.quantity_on_hand.
type Direction int

const (
	DirectionIn  Direction = 1
	DirectionOut Direction = -1
)

// directions is the single Go-side source of truth for which movement
// types increase vs. decrease stock. Mirrored by the exhaustive CHECK
// constraint in migrations/0012_inventory.up.sql (the list of valid
// types) — that constraint doesn't encode direction itself, only
// validity, so this map is where direction actually lives. TestAllMovementTypesHaveDirection
// (movement_test.go) asserts every constant above has an entry here, so
// a future new movement_type that's added to the CHECK constraint but
// forgotten here fails a unit test immediately instead of silently
// corrupting stock_balances the first time it's used.
var directions = map[MovementType]Direction{
	MovementOpening:         DirectionIn,
	MovementPurchaseReceipt: DirectionIn,
	MovementPurchaseReturn:  DirectionOut,
	MovementSale:            DirectionOut,
	MovementSaleReturn:      DirectionIn,
	MovementTransferOut:     DirectionOut,
	MovementTransferIn:      DirectionIn,
	MovementAdjustmentIn:    DirectionIn,
	MovementAdjustmentOut:   DirectionOut,
	MovementAssemblyIn:      DirectionIn,
	MovementAssemblyOut:     DirectionOut,
	MovementDamage:          DirectionOut,
	MovementExpiry:          DirectionOut,
}

// AllMovementTypes lists every valid movement type, in the same order as
// migrations/0012_inventory.up.sql's CHECK constraint — used by
// movement_test.go to assert directions above is exhaustive, and
// available to callers (e.g. an httpapi enum validator) that need the
// full list without duplicating it.
var AllMovementTypes = []MovementType{
	MovementOpening, MovementPurchaseReceipt, MovementPurchaseReturn, MovementSale, MovementSaleReturn,
	MovementTransferOut, MovementTransferIn, MovementAdjustmentIn, MovementAdjustmentOut,
	MovementAssemblyIn, MovementAssemblyOut, MovementDamage, MovementExpiry,
}

// DirectionOf returns the movement type's effect on stock_balances, and
// false if t is not a recognized movement type.
func DirectionOf(t MovementType) (Direction, bool) {
	d, ok := directions[t]
	return d, ok
}

// IsReceipt reports whether t is a movement type that establishes a cost
// basis (i.e. unit_cost is meaningful and feeds the costing strategy) —
// currently OPENING and PURCHASE_RECEIPT. Every other movement type
// consumes stock at whatever the existing average cost already is.
func IsReceipt(t MovementType) bool {
	return t == MovementOpening || t == MovementPurchaseReceipt
}

type StockMovement struct {
	ID               uuid.UUID
	OrganisationID   uuid.UUID
	WarehouseID      uuid.UUID
	ProductVariantID uuid.UUID
	MovementType     MovementType
	UnitID           uuid.UUID
	Quantity         decimal.Decimal
	BaseQuantity     decimal.Decimal
	UnitCost         *decimal.Decimal
	BatchID          *uuid.UUID
	SerialNumberID   *uuid.UUID
	ReferenceType    string
	ReferenceID      *uuid.UUID
	Notes            string
	CreatedBy        uuid.UUID
	CreatedAt        time.Time
}

type StockBalance struct {
	OrganisationID   uuid.UUID
	WarehouseID      uuid.UUID
	ProductVariantID uuid.UUID
	QuantityOnHand   decimal.Decimal
	QuantityReserved decimal.Decimal
	AverageCost      decimal.Decimal
	UpdatedAt        time.Time
}

// Available is the quantity actually free to sell/reserve: on-hand minus
// what's already reserved by someone else.
func (b StockBalance) Available() decimal.Decimal {
	return b.QuantityOnHand.Sub(b.QuantityReserved)
}

type ReservationStatus string

const (
	ReservationActive   ReservationStatus = "ACTIVE"
	ReservationReleased ReservationStatus = "RELEASED"
	ReservationConsumed ReservationStatus = "CONSUMED"
)

type StockReservation struct {
	ID               uuid.UUID
	OrganisationID   uuid.UUID
	WarehouseID      uuid.UUID
	ProductVariantID uuid.UUID
	Quantity         decimal.Decimal
	ReferenceType    string
	ReferenceID      uuid.UUID
	Status           ReservationStatus
	CreatedAt        time.Time
	ReleasedAt       *time.Time
}

// StockCostLot is a priced receipt lot (migrations/0036) — a distinct
// concept from StockBatch below: StockBatch is a manufacturer/expiry
// identity (its own explicit batch_code, no cost), while a cost lot has
// no batch_code at all and exists purely to answer "which price(s) make
// up the stock we're currently holding." QuantityRemaining is decremented
// FIFO as outward movements are recorded; it never goes negative and
// legacy (pre-this-table) stock simply has no lot to decrement — see
// StockCostLotRepository.ConsumeFIFO's doc comment.
type StockCostLot struct {
	ID                  uuid.UUID
	OrganisationID      uuid.UUID
	WarehouseID         uuid.UUID
	ProductVariantID    uuid.UUID
	UnitCost            decimal.Decimal
	QuantityReceived    decimal.Decimal
	QuantityRemaining   decimal.Decimal
	SourceReferenceType string
	SourceReferenceID   *uuid.UUID
	ReceivedAt          time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type StockBatch struct {
	ID               uuid.UUID
	OrganisationID   uuid.UUID
	ProductVariantID uuid.UUID
	BatchCode        string
	ManufactureDate  *time.Time
	ExpiryDate       *time.Time
	CreatedAt        time.Time
}

type SerialStatus string

const (
	SerialInStock  SerialStatus = "IN_STOCK"
	SerialReserved SerialStatus = "RESERVED"
	SerialSold     SerialStatus = "SOLD"
)

type SerialNumber struct {
	ID               uuid.UUID
	OrganisationID   uuid.UUID
	ProductVariantID uuid.UUID
	SerialCode       string
	WarehouseID      *uuid.UUID
	Status           SerialStatus
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type StockPolicy struct {
	OrganisationID     uuid.UUID
	WarehouseID        uuid.UUID
	ProductVariantID   uuid.UUID
	ReorderLevel       decimal.Decimal
	SafetyStock        decimal.Decimal
	AllowNegativeStock bool
	UpdatedAt          time.Time
}

type TransferStatus string

const (
	TransferCompleted TransferStatus = "COMPLETED"
	TransferCancelled TransferStatus = "CANCELLED"
)

type StockTransfer struct {
	ID              uuid.UUID
	OrganisationID  uuid.UUID
	FromWarehouseID uuid.UUID
	ToWarehouseID   uuid.UUID
	Status          TransferStatus
	Notes           string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
}

type StockAdjustment struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	WarehouseID    uuid.UUID
	Reason         string
	Notes          string
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
}

// --- Repository interfaces ---

type StockMovementRepository interface {
	Create(ctx context.Context, m *StockMovement) error
	ListByVariantWarehouse(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, limit int) ([]*StockMovement, error)
	ListByReference(ctx context.Context, orgID uuid.UUID, referenceType string, referenceID uuid.UUID) ([]*StockMovement, error)
}

type StockBalanceRepository interface {
	// Get returns the current balance row, or a zero-valued StockBalance
	// (not an error) if none exists yet — a product with no movements has
	// an implicit zero balance, not a "not found" error.
	Get(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (StockBalance, error)
	// GetForUpdate is Get but takes a row lock (SELECT ... FOR UPDATE),
	// used by app.Service.RecordMovement so two concurrent movements
	// against the same (warehouse, variant) serialize instead of racing
	// on a read-modify-write (Scenario D's building block).
	GetForUpdate(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (StockBalance, error)
	Upsert(ctx context.Context, b StockBalance) error
	ListLowStock(ctx context.Context, orgID, warehouseID uuid.UUID) ([]StockBalance, error)
}

type StockReservationRepository interface {
	Create(ctx context.Context, r *StockReservation) error
	Release(ctx context.Context, id uuid.UUID) error
	// SumActive returns the total ACTIVE reserved quantity for a
	// (warehouse, variant) — what StockBalance.QuantityReserved should
	// equal; used to keep the materialized quantity_reserved column
	// honest after a release.
	SumActive(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (decimal.Decimal, error)
}

type StockBatchRepository interface {
	Create(ctx context.Context, b *StockBatch) error
	GetOrCreate(ctx context.Context, orgID, variantID uuid.UUID, batchCode string, manufactureDate, expiryDate *time.Time) (*StockBatch, error)
	ListExpiringBefore(ctx context.Context, orgID uuid.UUID, before time.Time) ([]*StockBatch, error)
}

type StockCostLotRepository interface {
	// UpsertReceipt records an inward movement against the cost-lot
	// ledger: a lot already at this exact unit cost gains quantity ("same
	// stock, same price"); any other unit cost starts a new lot row
	// ("different batch, because price moved") — the UNIQUE(warehouse,
	// variant, unit_cost) constraint is the actual decision-maker via
	// ON CONFLICT, not application-layer branching.
	UpsertReceipt(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, unitCost, quantity decimal.Decimal, sourceRefType string, sourceRefID *uuid.UUID) (*StockCostLot, error)
	// ConsumeFIFO decrements quantity_remaining across the oldest lots
	// first until quantity is accounted for, clamping rather than erroring
	// if existing lots don't cover it (stock received before this table
	// existed, or before this deployment upgraded to it, has no lot at
	// all) — an outward movement must never fail just because its
	// informational cost-lot bookkeeping can't fully reconcile.
	ConsumeFIFO(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, quantity decimal.Decimal) error
	ListRemaining(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) ([]*StockCostLot, error)
}

type SerialNumberRepository interface {
	Create(ctx context.Context, s *SerialNumber) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status SerialStatus, warehouseID *uuid.UUID) error
	GetByCode(ctx context.Context, orgID, variantID uuid.UUID, code string) (*SerialNumber, error)
}

type StockPolicyRepository interface {
	Upsert(ctx context.Context, p StockPolicy) error
	Get(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (*StockPolicy, error)
}

type StockTransferRepository interface {
	Create(ctx context.Context, t *StockTransfer) error
}

type StockAdjustmentRepository interface {
	Create(ctx context.Context, a *StockAdjustment) error
}
