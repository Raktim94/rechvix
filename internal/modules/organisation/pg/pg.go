// Package pg is the organisation module's PostgreSQL repository
// implementation. Every method reads its Querier from
// database.Pool.Q(ctx), so it works correctly whether ctx carries an
// active database.Pool.RunScoped transaction (the normal case) or not.
// One small repository struct per domain interface, so each implements
// its interface's method names (Create, GetByID, ...) directly rather
// than needing renamed adapter methods.
package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rechvix/internal/modules/organisation/domain"
	"rechvix/internal/platform/database"
)

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Organisations ---

type OrganisationRepo struct{ pool *database.Pool }

func NewOrganisationRepo(pool *database.Pool) *OrganisationRepo { return &OrganisationRepo{pool: pool} }

func (r *OrganisationRepo) Create(ctx context.Context, o *domain.Organisation) error {
	const q = `
		INSERT INTO organisations (id, name, default_currency_code, default_timezone, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, o.ID, o.Name, o.DefaultCurrencyCode, o.DefaultTimezone, string(o.Status), o.CreatedAt)
	if err != nil {
		return fmt.Errorf("organisation: inserting organisation: %w", err)
	}
	return nil
}

func (r *OrganisationRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Organisation, error) {
	const q = `
		SELECT id, name, default_currency_code, default_timezone, ewaybill_mode, status, created_at, updated_at
		FROM organisations WHERE id = $1`
	row := r.pool.Q(ctx).QueryRow(ctx, q, id)
	var o domain.Organisation
	var status string
	if err := row.Scan(&o.ID, &o.Name, &o.DefaultCurrencyCode, &o.DefaultTimezone, &o.EWayBillMode, &status, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("organisation: querying organisation: %w", err)
	}
	o.Status = domain.Status(status)
	return &o, nil
}

func (r *OrganisationRepo) UpdateEWayBillMode(ctx context.Context, id uuid.UUID, mode string) error {
	const q = `UPDATE organisations SET ewaybill_mode = $2, updated_at = now() WHERE id = $1`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, id, mode)
	if err != nil {
		return fmt.Errorf("organisation: updating ewaybill_mode: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *OrganisationRepo) Exists(ctx context.Context) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM organisations)`
	var exists bool
	if err := r.pool.Q(ctx).QueryRow(ctx, q).Scan(&exists); err != nil {
		return false, fmt.Errorf("organisation: checking whether any organisation exists: %w", err)
	}
	return exists, nil
}

// --- Legal entities ---

type LegalEntityRepo struct{ pool *database.Pool }

func NewLegalEntityRepo(pool *database.Pool) *LegalEntityRepo { return &LegalEntityRepo{pool: pool} }

func (r *LegalEntityRepo) Create(ctx context.Context, le *domain.LegalEntity) error {
	const q = `
		INSERT INTO legal_entities (id, organisation_id, legal_name, country_code, base_currency_code, gstin, gst_state_code, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, le.ID, le.OrganisationID, le.LegalName, le.CountryCode, le.BaseCurrencyCode,
		nullIfEmpty(le.GSTIN), nullIfEmpty(le.GSTStateCode), string(le.Status), le.CreatedAt)
	if err != nil {
		return fmt.Errorf("organisation: inserting legal_entity: %w", err)
	}
	return nil
}

func (r *LegalEntityRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.LegalEntity, error) {
	const q = `
		SELECT id, organisation_id, legal_name, country_code, base_currency_code, COALESCE(gstin, ''), COALESCE(gst_state_code, ''), status, created_at, updated_at
		FROM legal_entities WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var le domain.LegalEntity
	var status string
	if err := row.Scan(&le.ID, &le.OrganisationID, &le.LegalName, &le.CountryCode, &le.BaseCurrencyCode, &le.GSTIN, &le.GSTStateCode, &status, &le.CreatedAt, &le.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("organisation: querying legal_entity: %w", err)
	}
	le.Status = domain.Status(status)
	return &le, nil
}

func (r *LegalEntityRepo) UpdateGSTDetails(ctx context.Context, orgID, id uuid.UUID, gstin, gstStateCode string) (*domain.LegalEntity, error) {
	const q = `
		UPDATE legal_entities SET gstin = NULLIF($3, ''), gst_state_code = NULLIF($4, ''), updated_at = now()
		WHERE organisation_id = $1 AND id = $2
		RETURNING id, organisation_id, legal_name, country_code, base_currency_code, COALESCE(gstin, ''), COALESCE(gst_state_code, ''), status, created_at, updated_at`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id, gstin, gstStateCode)
	var le domain.LegalEntity
	var status string
	if err := row.Scan(&le.ID, &le.OrganisationID, &le.LegalName, &le.CountryCode, &le.BaseCurrencyCode, &le.GSTIN, &le.GSTStateCode, &status, &le.CreatedAt, &le.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("organisation: updating legal_entity GST details: %w", err)
	}
	le.Status = domain.Status(status)
	return &le, nil
}

func (r *LegalEntityRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.LegalEntity, error) {
	const q = `
		SELECT id, organisation_id, legal_name, country_code, base_currency_code, COALESCE(gstin, ''), COALESCE(gst_state_code, ''), status, created_at, updated_at
		FROM legal_entities WHERE organisation_id = $1 ORDER BY created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organisation: listing legal_entities: %w", err)
	}
	defer rows.Close()

	var out []*domain.LegalEntity
	for rows.Next() {
		var le domain.LegalEntity
		var status string
		if err := rows.Scan(&le.ID, &le.OrganisationID, &le.LegalName, &le.CountryCode, &le.BaseCurrencyCode, &le.GSTIN, &le.GSTStateCode, &status, &le.CreatedAt, &le.UpdatedAt); err != nil {
			return nil, fmt.Errorf("organisation: scanning legal_entity row: %w", err)
		}
		le.Status = domain.Status(status)
		out = append(out, &le)
	}
	return out, rows.Err()
}

// --- Branches ---

type BranchRepo struct{ pool *database.Pool }

func NewBranchRepo(pool *database.Pool) *BranchRepo { return &BranchRepo{pool: pool} }

func (r *BranchRepo) Create(ctx context.Context, b *domain.Branch) error {
	const q = `
		INSERT INTO branches (id, organisation_id, legal_entity_id, code, name, timezone, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, b.ID, b.OrganisationID, b.LegalEntityID, b.Code, b.Name, b.Timezone, string(b.Status), b.CreatedAt)
	if err != nil {
		return fmt.Errorf("organisation: inserting branch: %w", err)
	}
	return nil
}

func (r *BranchRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Branch, error) {
	const q = `
		SELECT id, organisation_id, legal_entity_id, code, name, timezone, status, created_at, updated_at
		FROM branches WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var b domain.Branch
	var status string
	if err := row.Scan(&b.ID, &b.OrganisationID, &b.LegalEntityID, &b.Code, &b.Name, &b.Timezone, &status, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("organisation: querying branch: %w", err)
	}
	b.Status = domain.Status(status)
	return &b, nil
}

func (r *BranchRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.Branch, error) {
	const q = `
		SELECT id, organisation_id, legal_entity_id, code, name, timezone, status, created_at, updated_at
		FROM branches WHERE organisation_id = $1 ORDER BY created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("organisation: listing branches: %w", err)
	}
	defer rows.Close()

	var out []*domain.Branch
	for rows.Next() {
		var b domain.Branch
		var status string
		if err := rows.Scan(&b.ID, &b.OrganisationID, &b.LegalEntityID, &b.Code, &b.Name, &b.Timezone, &status, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("organisation: scanning branch row: %w", err)
		}
		b.Status = domain.Status(status)
		out = append(out, &b)
	}
	return out, rows.Err()
}

// --- Warehouses ---

type WarehouseRepo struct{ pool *database.Pool }

func NewWarehouseRepo(pool *database.Pool) *WarehouseRepo { return &WarehouseRepo{pool: pool} }

func (r *WarehouseRepo) Create(ctx context.Context, w *domain.Warehouse) error {
	const q = `
		INSERT INTO warehouses (id, organisation_id, branch_id, code, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, w.ID, w.OrganisationID, w.BranchID, w.Code, w.Name, string(w.Status), w.CreatedAt)
	if err != nil {
		return fmt.Errorf("organisation: inserting warehouse: %w", err)
	}
	return nil
}

func (r *WarehouseRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Warehouse, error) {
	const q = `
		SELECT id, organisation_id, branch_id, code, name, status, created_at, updated_at
		FROM warehouses WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var w domain.Warehouse
	var status string
	if err := row.Scan(&w.ID, &w.OrganisationID, &w.BranchID, &w.Code, &w.Name, &status, &w.CreatedAt, &w.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("organisation: querying warehouse: %w", err)
	}
	w.Status = domain.Status(status)
	return &w, nil
}

func (r *WarehouseRepo) ListByBranch(ctx context.Context, branchID uuid.UUID) ([]*domain.Warehouse, error) {
	const q = `
		SELECT id, organisation_id, branch_id, code, name, status, created_at, updated_at
		FROM warehouses WHERE branch_id = $1 ORDER BY created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, branchID)
	if err != nil {
		return nil, fmt.Errorf("organisation: listing warehouses: %w", err)
	}
	defer rows.Close()

	var out []*domain.Warehouse
	for rows.Next() {
		var w domain.Warehouse
		var status string
		if err := rows.Scan(&w.ID, &w.OrganisationID, &w.BranchID, &w.Code, &w.Name, &status, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("organisation: scanning warehouse row: %w", err)
		}
		w.Status = domain.Status(status)
		out = append(out, &w)
	}
	return out, rows.Err()
}
