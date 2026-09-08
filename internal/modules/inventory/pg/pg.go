// Package pg is the inventory module's PostgreSQL repository
// implementation. Same shape as internal/modules/catalogue/pg.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/inventory/domain"
	"rechvix/internal/platform/database"
)

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- Stock movements ---

type StockMovementRepo struct{ pool *database.Pool }

func NewStockMovementRepo(pool *database.Pool) *StockMovementRepo {
	return &StockMovementRepo{pool: pool}
}

func (r *StockMovementRepo) Create(ctx context.Context, m *domain.StockMovement) error {
	const q = `
		INSERT INTO stock_movements (
			id, organisation_id, warehouse_id, product_variant_id, movement_type, unit_id,
			quantity, base_quantity, unit_cost, batch_id, serial_number_id,
			reference_type, reference_id, notes, created_by, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
	_, err := r.pool.Q(ctx).Exec(ctx, q,
		m.ID, m.OrganisationID, m.WarehouseID, m.ProductVariantID, string(m.MovementType), m.UnitID,
		m.Quantity, m.BaseQuantity, m.UnitCost, m.BatchID, m.SerialNumberID,
		nullIfEmpty(m.ReferenceType), m.ReferenceID, nullIfEmpty(m.Notes), m.CreatedBy, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inventory: inserting stock_movement: %w", err)
	}
	return nil
}

const movementCols = `id, organisation_id, warehouse_id, product_variant_id, movement_type, unit_id,
	quantity, base_quantity, unit_cost, batch_id, serial_number_id,
	COALESCE(reference_type, ''), reference_id, COALESCE(notes, ''), created_by, created_at`

func (r *StockMovementRepo) ListByVariantWarehouse(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, limit int) ([]*domain.StockMovement, error) {
	q := fmt.Sprintf(`SELECT %s FROM stock_movements
		WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3
		ORDER BY created_at DESC LIMIT $4`, movementCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, warehouseID, variantID, limit)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing stock_movements: %w", err)
	}
	defer rows.Close()
	return scanMovements(rows)
}

func (r *StockMovementRepo) ListByReference(ctx context.Context, orgID uuid.UUID, referenceType string, referenceID uuid.UUID) ([]*domain.StockMovement, error) {
	q := fmt.Sprintf(`SELECT %s FROM stock_movements
		WHERE organisation_id = $1 AND reference_type = $2 AND reference_id = $3
		ORDER BY created_at`, movementCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, referenceType, referenceID)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing stock_movements by reference: %w", err)
	}
	defer rows.Close()
	return scanMovements(rows)
}

func scanMovements(rows pgx.Rows) ([]*domain.StockMovement, error) {
	var out []*domain.StockMovement
	for rows.Next() {
		var m domain.StockMovement
		var movementType string
		if err := rows.Scan(&m.ID, &m.OrganisationID, &m.WarehouseID, &m.ProductVariantID, &movementType, &m.UnitID,
			&m.Quantity, &m.BaseQuantity, &m.UnitCost, &m.BatchID, &m.SerialNumberID,
			&m.ReferenceType, &m.ReferenceID, &m.Notes, &m.CreatedBy, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("inventory: scanning stock_movement row: %w", err)
		}
		m.MovementType = domain.MovementType(movementType)
		out = append(out, &m)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Stock balances ---

type StockBalanceRepo struct{ pool *database.Pool }

func NewStockBalanceRepo(pool *database.Pool) *StockBalanceRepo { return &StockBalanceRepo{pool: pool} }

func (r *StockBalanceRepo) Get(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (domain.StockBalance, error) {
	return r.get(ctx, orgID, warehouseID, variantID, false)
}

// GetForUpdate takes a row lock via SELECT ... FOR UPDATE so the
// read-modify-write in app.Service.RecordMovement serializes against
// concurrent movements on the same (warehouse, variant) — Scenario D's
// building block ("two operators selling the last unit simultaneously").
// A missing row (never-moved product) has nothing to lock; the caller's
// subsequent Upsert INSERTs it, and Postgres's normal MVCC/unique-
// constraint behavior still prevents two concurrent first-inserts from
// both succeeding silently (one blocks on the other's uncommitted insert,
// then either sees it after commit or the transaction serializes).
func (r *StockBalanceRepo) GetForUpdate(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (domain.StockBalance, error) {
	return r.get(ctx, orgID, warehouseID, variantID, true)
}

func (r *StockBalanceRepo) get(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, forUpdate bool) (domain.StockBalance, error) {
	q := `SELECT organisation_id, warehouse_id, product_variant_id, quantity_on_hand, quantity_reserved, average_cost, updated_at
		FROM stock_balances WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3`
	if forUpdate {
		q += " FOR UPDATE"
	}
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, warehouseID, variantID)
	var b domain.StockBalance
	if err := row.Scan(&b.OrganisationID, &b.WarehouseID, &b.ProductVariantID, &b.QuantityOnHand, &b.QuantityReserved, &b.AverageCost, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No movements yet for this (warehouse, variant): an implicit
			// zero balance, not an error — see the interface doc comment.
			return domain.StockBalance{OrganisationID: orgID, WarehouseID: warehouseID, ProductVariantID: variantID,
				QuantityOnHand: decimal.Zero, QuantityReserved: decimal.Zero, AverageCost: decimal.Zero}, nil
		}
		return domain.StockBalance{}, fmt.Errorf("inventory: querying stock_balance: %w", err)
	}
	return b, nil
}

func (r *StockBalanceRepo) Upsert(ctx context.Context, b domain.StockBalance) error {
	const q = `
		INSERT INTO stock_balances (organisation_id, warehouse_id, product_variant_id, quantity_on_hand, quantity_reserved, average_cost, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (organisation_id, warehouse_id, product_variant_id)
		DO UPDATE SET quantity_on_hand = EXCLUDED.quantity_on_hand, quantity_reserved = EXCLUDED.quantity_reserved,
			average_cost = EXCLUDED.average_cost, updated_at = now()`
	_, err := r.pool.Q(ctx).Exec(ctx, q, b.OrganisationID, b.WarehouseID, b.ProductVariantID, b.QuantityOnHand, b.QuantityReserved, b.AverageCost)
	if err != nil {
		return fmt.Errorf("inventory: upserting stock_balance: %w", err)
	}
	return nil
}

func (r *StockBalanceRepo) ListLowStock(ctx context.Context, orgID, warehouseID uuid.UUID) ([]domain.StockBalance, error) {
	const q = `
		SELECT b.organisation_id, b.warehouse_id, b.product_variant_id, b.quantity_on_hand, b.quantity_reserved, b.average_cost, b.updated_at
		FROM stock_balances b
		JOIN stock_policies p ON p.organisation_id = b.organisation_id AND p.warehouse_id = b.warehouse_id AND p.product_variant_id = b.product_variant_id
		WHERE b.organisation_id = $1 AND b.warehouse_id = $2 AND b.quantity_on_hand <= p.reorder_level`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, warehouseID)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing low stock: %w", err)
	}
	defer rows.Close()
	var out []domain.StockBalance
	for rows.Next() {
		var b domain.StockBalance
		if err := rows.Scan(&b.OrganisationID, &b.WarehouseID, &b.ProductVariantID, &b.QuantityOnHand, &b.QuantityReserved, &b.AverageCost, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("inventory: scanning low stock row: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- Stock reservations ---

type StockReservationRepo struct{ pool *database.Pool }

func NewStockReservationRepo(pool *database.Pool) *StockReservationRepo {
	return &StockReservationRepo{pool: pool}
}

func (r *StockReservationRepo) Create(ctx context.Context, res *domain.StockReservation) error {
	const q = `
		INSERT INTO stock_reservations (id, organisation_id, warehouse_id, product_variant_id, quantity, reference_type, reference_id, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, res.ID, res.OrganisationID, res.WarehouseID, res.ProductVariantID,
		res.Quantity, res.ReferenceType, res.ReferenceID, string(res.Status), res.CreatedAt)
	if err != nil {
		return fmt.Errorf("inventory: inserting stock_reservation: %w", err)
	}
	return nil
}

func (r *StockReservationRepo) Release(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE stock_reservations SET status = 'RELEASED', released_at = now() WHERE id = $1 AND status = 'ACTIVE'`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("inventory: releasing stock_reservation: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *StockReservationRepo) SumActive(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (decimal.Decimal, error) {
	const q = `
		SELECT COALESCE(SUM(quantity), 0) FROM stock_reservations
		WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3 AND status = 'ACTIVE'`
	var sum decimal.Decimal
	if err := r.pool.Q(ctx).QueryRow(ctx, q, orgID, warehouseID, variantID).Scan(&sum); err != nil {
		return decimal.Zero, fmt.Errorf("inventory: summing active reservations: %w", err)
	}
	return sum, nil
}

// --- Batches ---

type StockBatchRepo struct{ pool *database.Pool }

func NewStockBatchRepo(pool *database.Pool) *StockBatchRepo { return &StockBatchRepo{pool: pool} }

func (r *StockBatchRepo) Create(ctx context.Context, b *domain.StockBatch) error {
	const q = `
		INSERT INTO stock_batches (id, organisation_id, product_variant_id, batch_code, manufacture_date, expiry_date, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, b.ID, b.OrganisationID, b.ProductVariantID, b.BatchCode, b.ManufactureDate, b.ExpiryDate, b.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateBatchCode
		}
		return fmt.Errorf("inventory: inserting stock_batch: %w", err)
	}
	return nil
}

// GetOrCreate is the common receiving-flow entry point: a GRN line may
// name a batch code that already exists (repeat delivery of the same
// batch) or a brand-new one. Uses INSERT ... ON CONFLICT DO UPDATE (a
// no-op update of created_at to itself) with RETURNING so this is one
// round trip and race-safe against a concurrent receipt of the same
// batch code, rather than a separate SELECT-then-INSERT that could lose
// a race.
func (r *StockBatchRepo) GetOrCreate(ctx context.Context, orgID, variantID uuid.UUID, batchCode string, manufactureDate, expiryDate *time.Time) (*domain.StockBatch, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("inventory: generating stock_batch id: %w", err)
	}
	const q = `
		INSERT INTO stock_batches (id, organisation_id, product_variant_id, batch_code, manufacture_date, expiry_date, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (organisation_id, product_variant_id, batch_code)
		DO UPDATE SET batch_code = EXCLUDED.batch_code
		RETURNING id, organisation_id, product_variant_id, batch_code, manufacture_date, expiry_date, created_at`
	row := r.pool.Q(ctx).QueryRow(ctx, q, id, orgID, variantID, batchCode, manufactureDate, expiryDate)
	var b domain.StockBatch
	if err := row.Scan(&b.ID, &b.OrganisationID, &b.ProductVariantID, &b.BatchCode, &b.ManufactureDate, &b.ExpiryDate, &b.CreatedAt); err != nil {
		return nil, fmt.Errorf("inventory: get-or-creating stock_batch: %w", err)
	}
	return &b, nil
}

func (r *StockBatchRepo) ListExpiringBefore(ctx context.Context, orgID uuid.UUID, before time.Time) ([]*domain.StockBatch, error) {
	const q = `
		SELECT id, organisation_id, product_variant_id, batch_code, manufacture_date, expiry_date, created_at
		FROM stock_batches WHERE organisation_id = $1 AND expiry_date IS NOT NULL AND expiry_date < $2
		ORDER BY expiry_date`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, before)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing expiring batches: %w", err)
	}
	defer rows.Close()
	var out []*domain.StockBatch
	for rows.Next() {
		var b domain.StockBatch
		if err := rows.Scan(&b.ID, &b.OrganisationID, &b.ProductVariantID, &b.BatchCode, &b.ManufactureDate, &b.ExpiryDate, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("inventory: scanning stock_batch row: %w", err)
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// --- Stock cost lots ---

type StockCostLotRepo struct{ pool *database.Pool }

func NewStockCostLotRepo(pool *database.Pool) *StockCostLotRepo { return &StockCostLotRepo{pool: pool} }

const costLotCols = `id, organisation_id, warehouse_id, product_variant_id, unit_cost,
	quantity_received, quantity_remaining, COALESCE(source_reference_type, ''), source_reference_id,
	received_at, created_at, updated_at`

func scanCostLot(row interface {
	Scan(dest ...any) error
}) (*domain.StockCostLot, error) {
	var lot domain.StockCostLot
	err := row.Scan(&lot.ID, &lot.OrganisationID, &lot.WarehouseID, &lot.ProductVariantID, &lot.UnitCost,
		&lot.QuantityReceived, &lot.QuantityRemaining, &lot.SourceReferenceType, &lot.SourceReferenceID,
		&lot.ReceivedAt, &lot.CreatedAt, &lot.UpdatedAt)
	return &lot, err
}

// UpsertReceipt is the same INSERT ... ON CONFLICT DO UPDATE ... RETURNING
// shape as StockBatchRepo.GetOrCreate above, for the same reason (one
// round trip, race-safe against a concurrent receipt at the identical
// price). received_at is deliberately left untouched by the DO UPDATE
// branch — a lot's FIFO position is when it was FIRST opened, not when
// it was last topped up.
func (r *StockCostLotRepo) UpsertReceipt(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, unitCost, quantity decimal.Decimal, sourceRefType string, sourceRefID *uuid.UUID) (*domain.StockCostLot, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("inventory: generating stock_cost_lot id: %w", err)
	}
	q := fmt.Sprintf(`
		INSERT INTO stock_cost_lots (id, organisation_id, warehouse_id, product_variant_id, unit_cost,
			quantity_received, quantity_remaining, source_reference_type, source_reference_id, received_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$8,now(),now(),now())
		ON CONFLICT (organisation_id, warehouse_id, product_variant_id, unit_cost)
		DO UPDATE SET
			quantity_received = stock_cost_lots.quantity_received + EXCLUDED.quantity_received,
			quantity_remaining = stock_cost_lots.quantity_remaining + EXCLUDED.quantity_remaining,
			updated_at = now()
		RETURNING %s`, costLotCols)
	row := r.pool.Q(ctx).QueryRow(ctx, q, id, orgID, warehouseID, variantID, unitCost, quantity, nullIfEmpty(sourceRefType), sourceRefID)
	lot, err := scanCostLot(row)
	if err != nil {
		return nil, fmt.Errorf("inventory: upserting stock_cost_lot: %w", err)
	}
	return lot, nil
}

// ConsumeFIFO decrements the oldest lot(s) first until quantity is
// accounted for. Runs as a loop of single-row SELECT ... FOR UPDATE +
// UPDATE statements rather than one aggregate UPDATE, since "take from
// lot A, and whatever's left over take from lot B" isn't expressible as
// a single SQL statement once more than one lot is involved. Stops
// silently (not an error) once no lot with remaining quantity is left —
// see the interface doc comment for why that has to be a no-op rather
// than a failure.
func (r *StockCostLotRepo) ConsumeFIFO(ctx context.Context, orgID, warehouseID, variantID uuid.UUID, quantity decimal.Decimal) error {
	remaining := quantity
	for remaining.IsPositive() {
		const selectQ = `
			SELECT id, quantity_remaining FROM stock_cost_lots
			WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3 AND quantity_remaining > 0
			ORDER BY received_at ASC LIMIT 1 FOR UPDATE`
		row := r.pool.Q(ctx).QueryRow(ctx, selectQ, orgID, warehouseID, variantID)
		var lotID uuid.UUID
		var lotRemaining decimal.Decimal
		if err := row.Scan(&lotID, &lotRemaining); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("inventory: selecting cost lot to consume: %w", err)
		}
		take := remaining
		if lotRemaining.LessThan(take) {
			take = lotRemaining
		}
		const updateQ = `UPDATE stock_cost_lots SET quantity_remaining = quantity_remaining - $1, updated_at = now() WHERE id = $2`
		if _, err := r.pool.Q(ctx).Exec(ctx, updateQ, take, lotID); err != nil {
			return fmt.Errorf("inventory: decrementing stock_cost_lot: %w", err)
		}
		remaining = remaining.Sub(take)
	}
	return nil
}

func (r *StockCostLotRepo) ListRemaining(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) ([]*domain.StockCostLot, error) {
	q := fmt.Sprintf(`SELECT %s FROM stock_cost_lots
		WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3 AND quantity_remaining > 0
		ORDER BY received_at ASC`, costLotCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, warehouseID, variantID)
	if err != nil {
		return nil, fmt.Errorf("inventory: listing stock_cost_lots: %w", err)
	}
	defer rows.Close()
	var out []*domain.StockCostLot
	for rows.Next() {
		lot, err := scanCostLot(rows)
		if err != nil {
			return nil, fmt.Errorf("inventory: scanning stock_cost_lot row: %w", err)
		}
		out = append(out, lot)
	}
	return out, rows.Err()
}

// --- Serial numbers ---

type SerialNumberRepo struct{ pool *database.Pool }

func NewSerialNumberRepo(pool *database.Pool) *SerialNumberRepo { return &SerialNumberRepo{pool: pool} }

func (r *SerialNumberRepo) Create(ctx context.Context, s *domain.SerialNumber) error {
	const q = `
		INSERT INTO stock_serial_numbers (id, organisation_id, product_variant_id, serial_code, warehouse_id, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, s.ID, s.OrganisationID, s.ProductVariantID, s.SerialCode, s.WarehouseID, string(s.Status), s.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateSerial
		}
		return fmt.Errorf("inventory: inserting stock_serial_number: %w", err)
	}
	return nil
}

func (r *SerialNumberRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.SerialStatus, warehouseID *uuid.UUID) error {
	const q = `UPDATE stock_serial_numbers SET status = $2, warehouse_id = $3, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, id, string(status), warehouseID)
	if err != nil {
		return fmt.Errorf("inventory: updating stock_serial_number: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SerialNumberRepo) GetByCode(ctx context.Context, orgID, variantID uuid.UUID, code string) (*domain.SerialNumber, error) {
	const q = `
		SELECT id, organisation_id, product_variant_id, serial_code, warehouse_id, status, created_at, updated_at
		FROM stock_serial_numbers WHERE organisation_id = $1 AND product_variant_id = $2 AND serial_code = $3`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, variantID, code)
	var s domain.SerialNumber
	var status string
	if err := row.Scan(&s.ID, &s.OrganisationID, &s.ProductVariantID, &s.SerialCode, &s.WarehouseID, &status, &s.CreatedAt, &s.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("inventory: querying stock_serial_number: %w", err)
	}
	s.Status = domain.SerialStatus(status)
	return &s, nil
}

// --- Stock policies ---

type StockPolicyRepo struct{ pool *database.Pool }

func NewStockPolicyRepo(pool *database.Pool) *StockPolicyRepo { return &StockPolicyRepo{pool: pool} }

func (r *StockPolicyRepo) Upsert(ctx context.Context, p domain.StockPolicy) error {
	const q = `
		INSERT INTO stock_policies (organisation_id, warehouse_id, product_variant_id, reorder_level, safety_stock, allow_negative_stock, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (organisation_id, warehouse_id, product_variant_id)
		DO UPDATE SET reorder_level = EXCLUDED.reorder_level, safety_stock = EXCLUDED.safety_stock,
			allow_negative_stock = EXCLUDED.allow_negative_stock, updated_at = now()`
	_, err := r.pool.Q(ctx).Exec(ctx, q, p.OrganisationID, p.WarehouseID, p.ProductVariantID, p.ReorderLevel, p.SafetyStock, p.AllowNegativeStock)
	if err != nil {
		return fmt.Errorf("inventory: upserting stock_policy: %w", err)
	}
	return nil
}

func (r *StockPolicyRepo) Get(ctx context.Context, orgID, warehouseID, variantID uuid.UUID) (*domain.StockPolicy, error) {
	const q = `
		SELECT organisation_id, warehouse_id, product_variant_id, reorder_level, safety_stock, allow_negative_stock, updated_at
		FROM stock_policies WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, warehouseID, variantID)
	var p domain.StockPolicy
	if err := row.Scan(&p.OrganisationID, &p.WarehouseID, &p.ProductVariantID, &p.ReorderLevel, &p.SafetyStock, &p.AllowNegativeStock, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("inventory: querying stock_policy: %w", err)
	}
	return &p, nil
}

// --- Transfers / adjustments (header rows) ---

type StockTransferRepo struct{ pool *database.Pool }

func NewStockTransferRepo(pool *database.Pool) *StockTransferRepo {
	return &StockTransferRepo{pool: pool}
}

func (r *StockTransferRepo) Create(ctx context.Context, t *domain.StockTransfer) error {
	const q = `
		INSERT INTO stock_transfers (id, organisation_id, from_warehouse_id, to_warehouse_id, status, notes, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, t.ID, t.OrganisationID, t.FromWarehouseID, t.ToWarehouseID, string(t.Status), nullIfEmpty(t.Notes), t.CreatedBy, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("inventory: inserting stock_transfer: %w", err)
	}
	return nil
}

type StockAdjustmentRepo struct{ pool *database.Pool }

func NewStockAdjustmentRepo(pool *database.Pool) *StockAdjustmentRepo {
	return &StockAdjustmentRepo{pool: pool}
}

func (r *StockAdjustmentRepo) Create(ctx context.Context, a *domain.StockAdjustment) error {
	const q = `
		INSERT INTO stock_adjustments (id, organisation_id, warehouse_id, reason, notes, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, a.ID, a.OrganisationID, a.WarehouseID, a.Reason, nullIfEmpty(a.Notes), a.CreatedBy, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("inventory: inserting stock_adjustment: %w", err)
	}
	return nil
}
