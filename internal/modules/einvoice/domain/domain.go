// Package domain defines the e-Invoice provider contract
// (docs/architecture.md §9) and the persisted record shape. Nothing here
// knows which provider implementation (mock, or a real IRP/GSP/ASP) is in
// use — that's internal/modules/einvoice/v1's job — and nothing here makes
// an HTTP call itself.
package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// EInvoiceProvider is the exact interface specified in
// docs/architecture.md §9. Implemented by v1/mock (used by every automated
// test) and v1/sandbox (calls the real NIC sandbox, exercised only by a
// human operator with real sandbox credentials — never by go test).
type EInvoiceProvider interface {
	Authenticate(ctx context.Context) error
	GenerateIRN(ctx context.Context, req IRNRequest) (IRNResponse, error)
	GetIRN(ctx context.Context, irn string) (IRNResponse, error)
	GetIRNByDocument(ctx context.Context, docType, docNo string, docDate time.Time) (IRNResponse, error)
	CancelIRN(ctx context.Context, irn, reason string) error
	GenerateEWayBillByIRN(ctx context.Context, irn string, transport TransportDetails) (EWBResponse, error)
	CancelEWayBill(ctx context.Context, ewbNo, reason string) error
	GetEWayBillByIRN(ctx context.Context, irn string) (EWBResponse, error)
	GetGSTIN(ctx context.Context, gstin string) (GSTINInfo, error)
	HealthCheck(ctx context.Context) error
}

type IRNLineItem struct {
	HSNSACCode   string
	Description  string
	Quantity     decimal.Decimal
	UnitPrice    decimal.Decimal
	TaxableValue decimal.Decimal
	GSTRate      decimal.Decimal
	TaxAmount    decimal.Decimal
}

type IRNRequest struct {
	SupplierGSTIN  string
	SupplierState  string
	BuyerGSTIN     string // empty for a genuine B2C supply
	BuyerState     string
	DocumentType   string // "INV", "CRN", "DBN" per NIC's schema
	DocumentNumber string
	DocumentDate   time.Time
	CurrencyCode   string
	TaxableValue   decimal.Decimal
	TotalTax       decimal.Decimal
	GrandTotal     decimal.Decimal
	Lines          []IRNLineItem
}

type IRNResponse struct {
	IRN           string
	AckNumber     string
	AckDate       time.Time
	SignedInvoice string
	SignedQRCode  string
	Status        string
}

// TransportDetails carries the fields GenerateEWayBillByIRN needs.
// ShipToGSTIN follows the 2026-08-01 GSTN advisory (docs/research.md):
// mandatory wherever ship-to details are present and an EWB is required;
// the literal "URP" is a valid value for an unregistered/not-applicable
// recipient, not a placeholder for "unset."
type TransportDetails struct {
	TransporterID   string
	TransporterName string
	TransportMode   string // ROAD, RAIL, AIR, SHIP
	VehicleNumber   string
	DistanceKM      decimal.Decimal
	ShipToGSTIN     string
}

type EWBResponse struct {
	EWBNumber  string
	ValidFrom  time.Time
	ValidUntil time.Time
}

type GSTINInfo struct {
	GSTIN     string
	LegalName string
	TradeName string
	StateCode string
	Status    string
}

// Status is the persisted e-Invoice state machine
// (docs/architecture.md §9, brief §10) — an explicit column, never
// inferred from which other fields happen to be set.
type Status string

const (
	StatusDraft           Status = "DRAFT"
	StatusQueued          Status = "QUEUED"
	StatusSubmitting      Status = "SUBMITTING"
	StatusGenerated       Status = "GENERATED"
	StatusFailedRetryable Status = "FAILED_RETRYABLE"
	StatusFailedFinal     Status = "FAILED_FINAL"
	StatusCancelPending   Status = "CANCEL_PENDING"
	StatusCancelled       Status = "CANCELLED"
)

// Terminal reports whether status will never be automatically transitioned
// again by the outbox worker (a human/admin action is required to move
// past it) — used to decide whether GenerateForDocument should treat an
// existing record as "already handled, nothing to do" (idempotency).
func (s Status) Terminal() bool {
	switch s {
	case StatusGenerated, StatusFailedFinal, StatusCancelled:
		return true
	default:
		return false
	}
}

type Record struct {
	ID              uuid.UUID
	OrganisationID  uuid.UUID
	SalesDocumentID uuid.UUID
	Provider        string
	Status          Status
	RequestVersion  string
	RequestHash     *string
	ResponsePayload []byte
	IRN             *string
	AckNumber       *string
	AckDate         *time.Time
	SignedInvoice   *string
	SignedQRPayload *string
	ErrorCode       *string
	ErrorMessage    *string
	CorrelationID   *string
	CancelledAt     *time.Time
	CancelReason    *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Repository interface {
	GetBySalesDocumentID(ctx context.Context, salesDocumentID uuid.UUID) (*Record, error)
	Create(ctx context.Context, r *Record) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status, fields UpdateFields) error
}

// UpdateFields is a sparse set of optional updates applied alongside a
// status transition — nil fields are left unchanged, mirroring the
// established .partial()-without-defaults convention this codebase uses
// elsewhere (see feedback memory on nodedr-pos's partial-update bug class:
// never let an "unset" field silently become NULL/zero on a partial
// update).
type UpdateFields struct {
	ResponsePayload []byte
	IRN             *string
	AckNumber       *string
	AckDate         *time.Time
	SignedInvoice   *string
	SignedQRPayload *string
	ErrorCode       *string
	ErrorMessage    *string
	CorrelationID   *string
	CancelledAt     *time.Time
	CancelReason    *string
}

var (
	ErrNotFound             = errors.New("einvoice: record not found")
	ErrAlreadyTerminal      = errors.New("einvoice: record already in a terminal state")
	ErrProviderCredsMissing = errors.New("einvoice: no provider credentials configured for this legal entity")
	// ErrNotRetryable: Service.RetryDocument only makes sense against a
	// FAILED_FINAL or FAILED_RETRYABLE record — anything else (still
	// QUEUED/SUBMITTING, or already GENERATED/CANCELLED/CANCEL_PENDING)
	// has nothing to retry.
	ErrNotRetryable = errors.New("einvoice: record is not in a failed state, nothing to retry")
)
