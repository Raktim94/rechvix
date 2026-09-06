// Package domain holds the organisation module's entity types and
// repository interfaces (docs/architecture.md §2 — domain defines the
// interface, pg implements it). No I/O, no framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "ACTIVE"
	StatusSuspended Status = "SUSPENDED"
)

type Organisation struct {
	ID                  uuid.UUID
	Name                string
	DefaultCurrencyCode string
	DefaultTimezone     string
	// EWayBillMode is docs/architecture.md §9b's per-organisation setting
	// — "FREE_PORTAL" (default, no paid API required) or "AUTOMATIC_API"
	// (the optional Stage 8 EWayBillProvider path).
	EWayBillMode string
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type LegalEntity struct {
	ID               uuid.UUID
	OrganisationID   uuid.UUID
	LegalName        string
	CountryCode      string
	BaseCurrencyCode string
	// GSTIN/GSTStateCode are additive (migrations/0017_legal_entity_gstin,
	// Stage 5b) — Stage 2 predates the tax module. Nullable: a legal
	// entity in a country without GST, or one not yet registered, has
	// neither.
	GSTIN        string
	GSTStateCode string
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Branch struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	LegalEntityID  uuid.UUID
	Code           string
	Name           string
	Timezone       string
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Warehouse struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	BranchID       uuid.UUID
	Code           string
	Name           string
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type OrganisationRepository interface {
	Create(ctx context.Context, o *Organisation) error
	GetByID(ctx context.Context, id uuid.UUID) (*Organisation, error)
	UpdateEWayBillMode(ctx context.Context, id uuid.UUID, mode string) error
	// Exists reports whether any organisation has been provisioned yet —
	// the composition root uses this to auto-close the bootstrap endpoint
	// once first-run setup has happened, on top of the ENABLE_BOOTSTRAP
	// env gate (see identity/httpapi.Handlers.Mount's doc comment).
	Exists(ctx context.Context) (bool, error)
}

type LegalEntityRepository interface {
	Create(ctx context.Context, le *LegalEntity) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*LegalEntity, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*LegalEntity, error)
	// UpdateGSTDetails is the fix path for a legal entity bootstrapped
	// (or created) before its GSTIN/state code were known — genuinely
	// necessary: without this, a legal entity with no GSTStateCode can
	// NEVER finalize a sales document (tax_documents.supplier_state_code
	// has a NOT NULL foreign key to gst_state_codes), and until this was
	// added there was no way to set it after the fact at all.
	UpdateGSTDetails(ctx context.Context, orgID, id uuid.UUID, gstin, gstStateCode string) (*LegalEntity, error)
}

type BranchRepository interface {
	Create(ctx context.Context, b *Branch) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Branch, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*Branch, error)
}

type WarehouseRepository interface {
	Create(ctx context.Context, w *Warehouse) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Warehouse, error)
	ListByBranch(ctx context.Context, branchID uuid.UUID) ([]*Warehouse, error)
}
