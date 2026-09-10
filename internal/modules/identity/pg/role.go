package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rechvix/internal/modules/identity/domain"
	"rechvix/internal/platform/database"
)

// RoleRepo implements the minimal RBAC-catalog operations identity's
// bootstrap flow needs. General role/permission management (custom
// roles, editing grants) is Stage 2's `roles.manage` permission surface,
// exposed as a later HTTP endpoint — not built out fully here, since
// nothing yet depends on it beyond bootstrap.
type RoleRepo struct{ pool *database.Pool }

func NewRoleRepo(pool *database.Pool) *RoleRepo { return &RoleRepo{pool: pool} }

func (r *RoleRepo) CreateRole(ctx context.Context, id, organisationID uuid.UUID, code, name string, isSystem bool, at time.Time) error {
	const q = `
		INSERT INTO roles (id, organisation_id, code, name, is_system, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, id, organisationID, code, name, isSystem, at)
	if err != nil {
		return fmt.Errorf("identity: inserting role: %w", err)
	}
	return nil
}

// GrantAllPermissions grants roleID every permission in the global
// catalog. Used only for the bootstrap Owner role — every other role
// (Administrator, Accountant, ...) gets a curated grant set added by a
// later, explicit roles.manage-gated endpoint rather than this bootstrap
// path, since "what exactly an Accountant can do" is a product decision,
// not a Stage 2 infrastructure concern.
func (r *RoleRepo) GrantAllPermissions(ctx context.Context, roleID uuid.UUID) error {
	const q = `INSERT INTO role_permissions (role_id, permission_code) SELECT $1, code FROM permissions`
	_, err := r.pool.Q(ctx).Exec(ctx, q, roleID)
	if err != nil {
		return fmt.Errorf("identity: granting all permissions: %w", err)
	}
	return nil
}

func (r *RoleRepo) GetIDByCode(ctx context.Context, organisationID uuid.UUID, code string) (uuid.UUID, error) {
	const q = `SELECT id FROM roles WHERE organisation_id = $1 AND code = $2`
	var id uuid.UUID
	err := r.pool.Q(ctx).QueryRow(ctx, q, organisationID, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, domain.ErrNotFound
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("identity: querying role by code: %w", err)
	}
	return id, nil
}

func (r *RoleRepo) AssignUserRole(ctx context.Context, id, organisationID, userID, roleID uuid.UUID, at time.Time) error {
	const q = `
		INSERT INTO user_roles (id, organisation_id, user_id, role_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, id, organisationID, userID, roleID, at)
	if err != nil {
		return fmt.Errorf("identity: assigning user role: %w", err)
	}
	return nil
}

// ReplaceUserCompanyAccess deletes every existing user_roles row for
// (userID, roleID) and inserts rows — both via r.pool.Q(ctx), so this
// participates in whatever transaction the caller's app.Service method
// already opened with database.Pool.RunScoped (same as every other
// multi-statement repo method in this codebase; a raw pgx transaction
// opened here directly would run OUTSIDE that scoping, meaning
// app.current_organisation_id would never get set on it and user_roles'
// RLS policy would reject every statement). The caller wrapping this in
// RunScoped is therefore what makes "delete then re-insert" atomic, not
// anything in this method itself.
func (r *RoleRepo) ReplaceUserCompanyAccess(ctx context.Context, organisationID, userID, roleID uuid.UUID, rows []domain.UserRoleScope, at time.Time) error {
	if _, err := r.pool.Q(ctx).Exec(ctx, `DELETE FROM user_roles WHERE organisation_id = $1 AND user_id = $2 AND role_id = $3`,
		organisationID, userID, roleID); err != nil {
		return fmt.Errorf("identity: clearing existing company access: %w", err)
	}
	for _, row := range rows {
		if _, err := r.pool.Q(ctx).Exec(ctx,
			`INSERT INTO user_roles (id, organisation_id, user_id, role_id, legal_entity_id, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			row.ID, organisationID, userID, roleID, row.LegalEntityID, at); err != nil {
			return fmt.Errorf("identity: inserting company access row: %w", err)
		}
	}
	return nil
}

// ListUserCompanyAccess mirrors permissions.Checker.AllowedLegalEntities'
// own contract (nil legal_entity_id on any row means unrestricted) —
// see that method's doc comment for the fail-closed "zero rows means
// zero allowed companies, not every company" rule this also follows.
func (r *RoleRepo) ListUserCompanyAccess(ctx context.Context, organisationID, userID, roleID uuid.UUID) (bool, []uuid.UUID, error) {
	const q = `SELECT legal_entity_id FROM user_roles WHERE organisation_id = $1 AND user_id = $2 AND role_id = $3`
	rows, err := r.pool.Q(ctx).Query(ctx, q, organisationID, userID, roleID)
	if err != nil {
		return false, nil, fmt.Errorf("identity: listing user company access: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var legalEntityID *uuid.UUID
		if err := rows.Scan(&legalEntityID); err != nil {
			return false, nil, fmt.Errorf("identity: scanning user_roles row: %w", err)
		}
		if legalEntityID == nil {
			return true, nil, nil
		}
		ids = append(ids, *legalEntityID)
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("identity: iterating user_roles rows: %w", err)
	}
	return false, ids, nil
}
