// Package domain holds the organisation module's entity types and
// repository interfaces (docs/architecture.md §2 — domain defines the
// interface, pg implements it). No I/O, no framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
	// EWayBillThresholdOverride, when set, replaces the consignment-value
	// threshold the eligibility engine (ewaybill/eligibility.Evaluate)
	// would otherwise pick from the global ewaybill_eligibility_rules
	// table (migrations/0028) — nil means "use the national/state rule
	// as-is", the same as before this field existed. See
	// migrations/0039_ewaybill_threshold_override for why this is an
	// override, not a replacement for that rules table.
	EWayBillThresholdOverride *decimal.Decimal
	Status                    Status
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
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
	// Invoice branding fields (migrations/0034) — the actual source
	// internal/modules/sales/printing has been able to render since
	// Stage 5b (logo, address, bank details) but had nowhere to read
	// from until this migration; see sales/app/print.go's
	// BuildInvoiceData. All nullable/empty by default, same as
	// GSTIN/GSTStateCode above.
	Phone                     string
	Email                     string
	Website                   string
	Address                   string
	BankName                  string
	BankAccountNumber         string
	BankIFSC                  string
	UPIID                     string
	AuthorizedSignatoryName   string
	DefaultTermsAndConditions string
	// LogoPNG is always a PNG regardless of what format was uploaded —
	// httpapi's decodeAndReencodeLogo decodes-then-reencodes any upload
	// before it ever reaches this field, both as validation (a JPEG/GIF
	// that fails to decode never gets this far) and so the print layer
	// only ever needs to handle one image format.
	LogoPNG   []byte
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InvoiceBrandingUpdate is LegalEntityRepository.UpdateInvoiceBranding's
// parameter — a struct rather than 10+ positional args, same reasoning
// as any other multi-field update in this codebase. Every text field is
// a full replace (empty string clears it, same NULLIF($n, ”) convention
// UpdateGSTDetails already uses) — logo is the one field that needs an
// explicit "leave unchanged" state distinct from "clear it", since a nil
// []byte can't itself distinguish those two, hence RemoveLogo.
type InvoiceBrandingUpdate struct {
	Phone                     string
	Email                     string
	Website                   string
	Address                   string
	BankName                  string
	BankAccountNumber         string
	BankIFSC                  string
	UPIID                     string
	AuthorizedSignatoryName   string
	DefaultTermsAndConditions string
	// LogoPNG non-nil replaces the stored logo. nil + RemoveLogo=false
	// leaves the existing logo untouched. nil + RemoveLogo=true clears it.
	LogoPNG    []byte
	RemoveLogo bool
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
	// UpdateEWayBillThreshold sets or clears (nil) the organisation's
	// e-Way Bill threshold override — see Organisation.
	// EWayBillThresholdOverride's doc comment.
	UpdateEWayBillThreshold(ctx context.Context, id uuid.UUID, value *decimal.Decimal) error
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
	// UpdateInvoiceBranding is the equivalent fix/set path for
	// everything a printed invoice can show beyond GSTIN — see
	// InvoiceBrandingUpdate's doc comment.
	UpdateInvoiceBranding(ctx context.Context, orgID, id uuid.UUID, u InvoiceBrandingUpdate) (*LegalEntity, error)
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
