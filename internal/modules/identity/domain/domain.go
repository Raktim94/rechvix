// Package domain holds the identity module's entity types, repository
// interfaces, and sentinel errors. No I/O, no framework imports
// (docs/architecture.md §2).
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type UserStatus string

const (
	UserStatusActive   UserStatus = "ACTIVE"
	UserStatusDisabled UserStatus = "DISABLED"
)

type User struct {
	ID                   uuid.UUID
	OrganisationID       uuid.UUID
	Email                string
	FullName             string
	PasswordHash         string
	Status               UserStatus
	MFAEnabled           bool
	LastLoginAt          *time.Time
	LastPasswordChangeAt time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// AuthLookup is the minimal projection returned by the SECURITY DEFINER
// auth_lookup_user_by_email() function (see
// migrations/0003_users.up.sql) — deliberately narrower than User,
// since this is the one query that runs before organisation scope is
// established.
type AuthLookup struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	PasswordHash   string
	Status         UserStatus
	MFAEnabled     bool
}

type Session struct {
	ID                uuid.UUID
	OrganisationID    uuid.UUID
	UserID            uuid.UUID
	TokenHash         string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
	IP                string
	UserAgent         string
}

type PasswordResetToken struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	UserID         uuid.UUID
	TokenHash      string
	ExpiresAt      time.Time
	UsedAt         *time.Time
	CreatedAt      time.Time
}

type MFASecret struct {
	UserID          uuid.UUID
	OrganisationID  uuid.UUID
	SecretEncrypted []byte
	Enabled         bool
}

// APIScope is one of brief §36's coarse, fixed-vocabulary API key scopes.
// See internal/platform/permissions/apikeyscope.go for how a scope
// expands into the RBAC permission codes it actually authorizes.
type APIScope string

const (
	ScopeProductsRead   APIScope = "products:read"
	ScopeInventoryRead  APIScope = "inventory:read"
	ScopeCustomersRead  APIScope = "customers:read"
	ScopeCustomersWrite APIScope = "customers:write"
	ScopeInvoicesRead   APIScope = "invoices:read"
	ScopeInvoicesWrite  APIScope = "invoices:write"
	ScopeReportsRead    APIScope = "reports:read"
)

// ValidScopes is the complete, closed vocabulary — CreateAPIKey rejects
// anything not in this set, and the api_keys.scopes column's values are
// drawn only from here (enforced in Go, not a DB CHECK, since text[]
// element-level CHECKs are awkward in Postgres; ValidateScopes is called
// on every write path, not just the HTTP layer, so this isn't just a UI
// nicety).
var ValidScopes = map[APIScope]bool{
	ScopeProductsRead: true, ScopeInventoryRead: true,
	ScopeCustomersRead: true, ScopeCustomersWrite: true,
	ScopeInvoicesRead: true, ScopeInvoicesWrite: true,
	ScopeReportsRead: true,
}

type APIKey struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	UserID         uuid.UUID
	Name           string
	KeyPrefix      string
	KeyHash        string
	Scopes         []APIScope
	ExpiresAt      *time.Time
	AllowedIP      *string
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
	CreatedAt      time.Time
	CreatedBy      uuid.UUID
}

// --- Repository interfaces (implemented in internal/modules/identity/pg) ---

type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	// LookupForAuth calls auth_lookup_user_by_email — the one
	// intentionally organisation-unscoped read in this module. See that
	// function's comment in migrations/0003_users.up.sql.
	LookupForAuth(ctx context.Context, email string) (*AuthLookup, error)
	UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string, at time.Time) error
	UpdateLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error
	SetMFAEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error
	ListByOrganisation(ctx context.Context, organisationID uuid.UUID) ([]*User, error)
}

type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	Touch(ctx context.Context, id uuid.UUID, lastSeenAt time.Time) error
	Revoke(ctx context.Context, id uuid.UUID, at time.Time) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID, at time.Time) error
	ListActiveForUser(ctx context.Context, userID uuid.UUID) ([]*Session, error)
}

type PasswordResetRepository interface {
	Create(ctx context.Context, t *PasswordResetToken) error
	GetByTokenHash(ctx context.Context, tokenHash string) (*PasswordResetToken, error)
	MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error
}

type MFARepository interface {
	UpsertSecret(ctx context.Context, secret *MFASecret) error
	GetSecret(ctx context.Context, userID uuid.UUID) (*MFASecret, error)
	SetEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error
	ReplaceRecoveryCodes(ctx context.Context, orgID, userID uuid.UUID, codeHashes []string) error
	// ConsumeRecoveryCode atomically marks one unused, matching code as
	// used and reports whether it found one — must be atomic (a
	// SELECT-then-UPDATE race would let the same recovery code be used
	// twice by concurrent requests).
	ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string, at time.Time) (bool, error)
}

// APIKeyRepository. GetByHash, like SessionRepository.GetByTokenHash, is
// deliberately called against an unscoped transaction — see
// migrations/0025_api_keys.up.sql.
type APIKeyRepository interface {
	Create(ctx context.Context, k *APIKey) error
	GetByHash(ctx context.Context, keyHash string) (*APIKey, error)
	Touch(ctx context.Context, id uuid.UUID, at time.Time) error
	Revoke(ctx context.Context, id uuid.UUID, at time.Time) error
	ListActiveForOrganisation(ctx context.Context, organisationID uuid.UUID) ([]*APIKey, error)
}

// RoleRepository is the minimal slice of the RBAC catalog identity's
// bootstrap use case needs: creating the organisation's starter system
// roles and granting the Owner role every permission that exists.
type RoleRepository interface {
	CreateRole(ctx context.Context, id, organisationID uuid.UUID, code, name string, isSystem bool, at time.Time) error
	GrantAllPermissions(ctx context.Context, roleID uuid.UUID) error
	AssignUserRole(ctx context.Context, id, organisationID, userID, roleID uuid.UUID, at time.Time) error
	// GetIDByCode looks up an existing role by its org-unique code (e.g.
	// "OWNER") — used to attach a newly-invited team member to a role
	// that bootstrap already created, rather than minting a duplicate one.
	GetIDByCode(ctx context.Context, organisationID uuid.UUID, code string) (uuid.UUID, error)
}
