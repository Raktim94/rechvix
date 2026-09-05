// Package pg is the identity module's PostgreSQL repository
// implementation.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rechvix/internal/modules/identity/domain"
	"rechvix/internal/platform/database"
)

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type UserRepo struct{ pool *database.Pool }

func NewUserRepo(pool *database.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	const q = `
		INSERT INTO users (id, organisation_id, email, full_name, password_hash, status,
			mfa_enabled, last_password_change_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`
	_, err := r.pool.Q(ctx).Exec(ctx, q,
		u.ID, u.OrganisationID, u.Email, u.FullName, u.PasswordHash, string(u.Status),
		u.MFAEnabled, u.LastPasswordChangeAt, u.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrEmailAlreadyExists
		}
		return fmt.Errorf("identity: inserting user: %w", err)
	}
	return nil
}

// ListByOrganisation powers the Settings > Team screen — every user
// belonging to the caller's organisation, newest first.
func (r *UserRepo) ListByOrganisation(ctx context.Context, organisationID uuid.UUID) ([]*domain.User, error) {
	const q = `
		SELECT id, organisation_id, email, full_name, status, mfa_enabled,
			last_login_at, last_password_change_at, created_at, updated_at
		FROM users WHERE organisation_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Q(ctx).Query(ctx, q, organisationID)
	if err != nil {
		return nil, fmt.Errorf("identity: listing users: %w", err)
	}
	defer rows.Close()
	var out []*domain.User
	for rows.Next() {
		var u domain.User
		var status string
		if err := rows.Scan(&u.ID, &u.OrganisationID, &u.Email, &u.FullName, &status, &u.MFAEnabled,
			&u.LastLoginAt, &u.LastPasswordChangeAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("identity: scanning user row: %w", err)
		}
		u.Status = domain.UserStatus(status)
		out = append(out, &u)
	}
	return out, rows.Err()
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	const q = `
		SELECT id, organisation_id, email, full_name, password_hash, status, mfa_enabled,
			last_login_at, last_password_change_at, created_at, updated_at
		FROM users WHERE id = $1`
	row := r.pool.Q(ctx).QueryRow(ctx, q, id)
	var u domain.User
	var status string
	if err := row.Scan(&u.ID, &u.OrganisationID, &u.Email, &u.FullName, &u.PasswordHash, &status,
		&u.MFAEnabled, &u.LastLoginAt, &u.LastPasswordChangeAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("identity: querying user: %w", err)
	}
	u.Status = domain.UserStatus(status)
	return &u, nil
}

// LookupForAuth calls the auth_lookup_user_by_email SECURITY DEFINER
// function directly — see migrations/0003_users.up.sql for why this one
// query must bypass the users table's normal RLS tenant isolation.
func (r *UserRepo) LookupForAuth(ctx context.Context, email string) (*domain.AuthLookup, error) {
	const q = `SELECT id, organisation_id, password_hash, status, mfa_enabled FROM auth_lookup_user_by_email($1)`
	row := r.pool.Q(ctx).QueryRow(ctx, q, email)
	var a domain.AuthLookup
	var status string
	if err := row.Scan(&a.ID, &a.OrganisationID, &a.PasswordHash, &status, &a.MFAEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("identity: auth lookup: %w", err)
	}
	a.Status = domain.UserStatus(status)
	return &a, nil
}

func (r *UserRepo) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string, at time.Time) error {
	const q = `UPDATE users SET password_hash = $2, last_password_change_at = $3, updated_at = $3 WHERE id = $1`
	tag, err := r.pool.Q(ctx).Exec(ctx, q, userID, hash, at)
	if err != nil {
		return fmt.Errorf("identity: updating password hash: %w", err)
	}
	if tag == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *UserRepo) UpdateLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	const q = `UPDATE users SET last_login_at = $2, updated_at = $2 WHERE id = $1`
	_, err := r.pool.Q(ctx).Exec(ctx, q, userID, at)
	if err != nil {
		return fmt.Errorf("identity: updating last_login_at: %w", err)
	}
	return nil
}

func (r *UserRepo) SetMFAEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	const q = `UPDATE users SET mfa_enabled = $2, updated_at = now() WHERE id = $1`
	_, err := r.pool.Q(ctx).Exec(ctx, q, userID, enabled)
	if err != nil {
		return fmt.Errorf("identity: updating mfa_enabled: %w", err)
	}
	return nil
}
