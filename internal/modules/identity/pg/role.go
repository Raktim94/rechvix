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
