// Package app is the organisation module's application/use-case layer: it
// orchestrates transactions, permission checks, and audit logging around
// the domain repositories. HTTP handlers call this, never the pg package
// directly (docs/architecture.md §2).
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/organisation/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool          database.Runner
	organisations domain.OrganisationRepository
	legalEntities domain.LegalEntityRepository
	branches      domain.BranchRepository
	warehouses    domain.WarehouseRepository
	permissions   *permissions.Checker
	audit         audit.Recorder
	now           func() time.Time
}

func NewService(
	pool database.Runner,
	organisations domain.OrganisationRepository,
	legalEntities domain.LegalEntityRepository,
	branches domain.BranchRepository,
	warehouses domain.WarehouseRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{
		pool:          pool,
		organisations: organisations,
		legalEntities: legalEntities,
		branches:      branches,
		warehouses:    warehouses,
		permissions:   checker,
		audit:         recorder,
		now:           time.Now,
	}
}

// ProvisionParams describes a brand-new organisation's starting shape:
// one legal entity, one branch, one warehouse. Deliberately minimal —
// additional legal entities/branches/warehouses are added afterward
// through the normal Create* methods below, which DO require
// authentication and settings.manage.
type ProvisionParams struct {
	OrganisationName    string
	DefaultCurrencyCode string
	DefaultTimezone     string
	LegalEntityName     string
	CountryCode         string
	// GSTIN/GSTStateCode are optional (Stage 5b addition) — a fresh
	// organisation can provision without GST registration and add it
	// later via CreateLegalEntity for a second/updated legal entity;
	// most test fixtures that exercise sales/tax flows set these directly
	// here for convenience.
	GSTIN         string
	GSTStateCode  string
	BranchCode    string
	BranchName    string
	WarehouseCode string
	WarehouseName string
}

type ProvisionResult struct {
	OrganisationID uuid.UUID
	LegalEntityID  uuid.UUID
	BranchID       uuid.UUID
	WarehouseID    uuid.UUID
}

// Exists reports whether any organisation has been provisioned yet.
// Deliberately no permission check, same reasoning as Provision below: the
// composition root calls this pre-auth, at startup, to decide whether the
// bootstrap endpoint should be reachable at all.
func (s *Service) Exists(ctx context.Context) (bool, error) {
	return s.organisations.Exists(ctx)
}

// Provision creates a brand-new organisation and its first legal entity,
// branch, and warehouse. There is deliberately no permission check here —
// by definition, nothing can hold a permission grant scoped to an
// organisation that doesn't exist yet. Callers must ensure this is only
// reachable from a genuine first-time-setup flow (identity's Bootstrap
// use case), not a regular authenticated endpoint. Everything commits in
// one transaction: a half-created organisation (e.g. org row but no
// warehouse) is never observable.
func (s *Service) Provision(ctx context.Context, p ProvisionParams) (ProvisionResult, error) {
	orgID, err := uuid.NewV7()
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("organisation: generating organisation id: %w", err)
	}
	legalEntityID, err := uuid.NewV7()
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("organisation: generating legal_entity id: %w", err)
	}
	branchID, err := uuid.NewV7()
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("organisation: generating branch id: %w", err)
	}
	warehouseID, err := uuid.NewV7()
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("organisation: generating warehouse id: %w", err)
	}

	now := s.now()

	err = s.pool.Run(ctx, func(ctx context.Context) error {
		// The organisation doesn't exist yet, so there is no prior
		// app.current_organisation_id to inherit — this freshly generated
		// orgID becomes the scope for the rest of this transaction,
		// satisfying organisations' own RLS WITH CHECK (id = scope) the
		// instant the row is inserted below.
		if err := s.pool.SetOrganisationScope(ctx, orgID); err != nil {
			return err
		}

		if err := s.organisations.Create(ctx, &domain.Organisation{
			ID:                  orgID,
			Name:                p.OrganisationName,
			DefaultCurrencyCode: p.DefaultCurrencyCode,
			DefaultTimezone:     p.DefaultTimezone,
			Status:              domain.StatusActive,
			CreatedAt:           now,
			UpdatedAt:           now,
		}); err != nil {
			return fmt.Errorf("organisation: creating organisation: %w", err)
		}

		if err := s.legalEntities.Create(ctx, &domain.LegalEntity{
			ID:               legalEntityID,
			OrganisationID:   orgID,
			LegalName:        p.LegalEntityName,
			CountryCode:      p.CountryCode,
			BaseCurrencyCode: p.DefaultCurrencyCode,
			GSTIN:            p.GSTIN,
			GSTStateCode:     p.GSTStateCode,
			Status:           domain.StatusActive,
			CreatedAt:        now,
			UpdatedAt:        now,
		}); err != nil {
			return fmt.Errorf("organisation: creating legal entity: %w", err)
		}

		if err := s.branches.Create(ctx, &domain.Branch{
			ID:             branchID,
			OrganisationID: orgID,
			LegalEntityID:  legalEntityID,
			Code:           p.BranchCode,
			Name:           p.BranchName,
			Timezone:       p.DefaultTimezone,
			Status:         domain.StatusActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			return fmt.Errorf("organisation: creating branch: %w", err)
		}

		if err := s.warehouses.Create(ctx, &domain.Warehouse{
			ID:             warehouseID,
			OrganisationID: orgID,
			BranchID:       branchID,
			Code:           p.WarehouseCode,
			Name:           p.WarehouseName,
			Status:         domain.StatusActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			return fmt.Errorf("organisation: creating warehouse: %w", err)
		}

		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: orgID,
			ActorType:      audit.ActorSystem,
			Action:         "organisation.provisioned",
			EntityType:     "organisation",
			EntityID:       &orgID,
			AfterState: map[string]any{
				"name":            p.OrganisationName,
				"legal_entity_id": legalEntityID,
				"branch_id":       branchID,
				"warehouse_id":    warehouseID,
			},
			At: now,
		})
	})
	if err != nil {
		return ProvisionResult{}, err
	}

	return ProvisionResult{
		OrganisationID: orgID,
		LegalEntityID:  legalEntityID,
		BranchID:       branchID,
		WarehouseID:    warehouseID,
	}, nil
}

// GetOrganisation returns the caller's own organisation. principal.
// OrganisationID is the only organisation a caller may ever request —
// there is no orgID parameter here, precisely so a handler cannot be
// tricked by a client-supplied ID into fetching another tenant's data
// (brief Rule 5).
func (s *Service) GetOrganisation(ctx context.Context, principal permissions.Principal) (*domain.Organisation, error) {
	if err := s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{}); err != nil {
		return nil, err
	}
	var result *domain.Organisation
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.organisations.GetByID(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

// SetEWayBillMode changes the organisation's e-Way Bill production mode
// (docs/architecture.md §9b). "settings.manage" (not "settings.view") is
// required — this changes real production behavior, not just a display
// preference.
func (s *Service) SetEWayBillMode(ctx context.Context, principal permissions.Principal, mode string) (*domain.Organisation, error) {
	if mode != "FREE_PORTAL" && mode != "AUTOMATIC_API" {
		return nil, fmt.Errorf("organisation: ewaybill_mode must be FREE_PORTAL or AUTOMATIC_API, got %q", mode)
	}
	if err := s.permissions.Require(ctx, principal, "settings.manage", permissions.Scope{}); err != nil {
		return nil, err
	}
	var result *domain.Organisation
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.organisations.UpdateEWayBillMode(ctx, principal.OrganisationID, mode); err != nil {
			return err
		}
		var err error
		result, err = s.organisations.GetByID(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

// GetLegalEntityForOtherModule is a cross-module read (added Stage 5b) —
// sales.FinalizeDocument needs the supplier-side GSTIN/state code, and
// should authorize on ITS OWN "sales.finalize" check, not require the
// calling principal to additionally hold organisation-settings
// permissions unrelated to billing. No permission check of its own, by
// design — same pattern and same rationale as
// inventory.RecordMovementForOtherModule and
// taxation.Service.CalculateAndSnapshotTx: the caller's own
// already-checked application-layer method is what authorizes this call.
// Not mounted as an HTTP endpoint for that reason — a direct "view legal
// entity" endpoint, if one is added later, needs its own real permission
// check and should be a separate method, not this one reused unsafely.
//
// Also does NOT open its own transaction (unlike most of this module's
// other methods) — callers like sales.FinalizeDocument call this from
// inside their own already-open RunScoped block, so it must be
// nested-transaction-safe the same way inventory.RecordMovementForOtherModule
// and taxation.Service.CalculateAndSnapshotTx are: it reads through
// database.Pool.Q(ctx), which picks up the caller's active transaction
// from ctx automatically. Calling this with no active transaction in ctx
// still works (Q falls back to the bare pool) but with no organisation
// scope set, which fails closed against legal_entities' RLS policy — same
// misuse class documented on those other two methods.
func (s *Service) GetLegalEntityForOtherModule(ctx context.Context, orgID, id uuid.UUID) (*domain.LegalEntity, error) {
	return s.legalEntities.GetByID(ctx, orgID, id)
}

type CreateLegalEntityParams struct {
	LegalName        string
	CountryCode      string
	BaseCurrencyCode string
	// GSTIN/GSTStateCode are additive (migrations/0017, Stage 5b) — a
	// legal entity's own GST registration, needed as the supplier side of
	// a tax calculation. Optional: a country without GST, or a
	// not-yet-registered entity, leaves both empty.
	GSTIN        string
	GSTStateCode string
}

func (s *Service) CreateLegalEntity(ctx context.Context, principal permissions.Principal, p CreateLegalEntityParams) (*domain.LegalEntity, error) {
	if err := s.permissions.Require(ctx, principal, "settings.manage", permissions.Scope{}); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("organisation: generating legal_entity id: %w", err)
	}
	now := s.now()
	le := &domain.LegalEntity{
		ID:               id,
		OrganisationID:   principal.OrganisationID,
		LegalName:        p.LegalName,
		CountryCode:      p.CountryCode,
		BaseCurrencyCode: p.BaseCurrencyCode,
		GSTIN:            p.GSTIN,
		GSTStateCode:     p.GSTStateCode,
		Status:           domain.StatusActive,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.legalEntities.Create(ctx, le); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "legal_entity.created",
			EntityType:     "legal_entity",
			EntityID:       &id,
			AfterState:     map[string]any{"legal_name": p.LegalName, "country_code": p.CountryCode},
			At:             now,
		})
	})
	if err != nil {
		return nil, err
	}
	return le, nil
}

// UpdateLegalEntityGST sets or corrects a legal entity's GSTIN/state
// code after creation — the fix path for an entity bootstrapped (or
// created via CreateLegalEntity) before its GST registration was known.
// Genuinely necessary, not a convenience: a legal entity with no
// GSTStateCode can never finalize a sales document at all (tax_documents.
// supplier_state_code is a NOT NULL foreign key), so without this method
// there was no way to recover once bootstrapped without one — see
// docs/adr/0007-bootstrap-and-legal-entity-gst-fields.md.
func (s *Service) UpdateLegalEntityGST(ctx context.Context, principal permissions.Principal, legalEntityID uuid.UUID, gstin, gstStateCode string) (*domain.LegalEntity, error) {
	if err := s.permissions.Require(ctx, principal, "settings.manage", permissions.Scope{}); err != nil {
		return nil, err
	}
	var le *domain.LegalEntity
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		le, err = s.legalEntities.UpdateGSTDetails(ctx, principal.OrganisationID, legalEntityID, gstin, gstStateCode)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "legal_entity.gst_updated",
			EntityType:     "legal_entity",
			EntityID:       &legalEntityID,
			AfterState:     map[string]any{"gstin": gstin, "gst_state_code": gstStateCode},
			At:             s.now(),
		})
	})
	if err != nil {
		return nil, err
	}
	return le, nil
}

type CreateBranchParams struct {
	LegalEntityID uuid.UUID
	Code          string
	Name          string
	Timezone      string
}

func (s *Service) CreateBranch(ctx context.Context, principal permissions.Principal, p CreateBranchParams) (*domain.Branch, error) {
	if err := s.permissions.Require(ctx, principal, "settings.manage", permissions.Scope{LegalEntityID: &p.LegalEntityID}); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("organisation: generating branch id: %w", err)
	}
	now := s.now()
	b := &domain.Branch{
		ID:             id,
		OrganisationID: principal.OrganisationID,
		LegalEntityID:  p.LegalEntityID,
		Code:           p.Code,
		Name:           p.Name,
		Timezone:       p.Timezone,
		Status:         domain.StatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.branches.Create(ctx, b); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "branch.created",
			EntityType:     "branch",
			EntityID:       &id,
			AfterState:     map[string]any{"code": p.Code, "name": p.Name},
			At:             now,
		})
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

type CreateWarehouseParams struct {
	BranchID uuid.UUID
	Code     string
	Name     string
}

func (s *Service) CreateWarehouse(ctx context.Context, principal permissions.Principal, p CreateWarehouseParams) (*domain.Warehouse, error) {
	if err := s.permissions.Require(ctx, principal, "settings.manage", permissions.Scope{BranchID: &p.BranchID}); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("organisation: generating warehouse id: %w", err)
	}
	now := s.now()
	w := &domain.Warehouse{
		ID:             id,
		OrganisationID: principal.OrganisationID,
		BranchID:       p.BranchID,
		Code:           p.Code,
		Name:           p.Name,
		Status:         domain.StatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.warehouses.Create(ctx, w); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "warehouse.created",
			EntityType:     "warehouse",
			EntityID:       &id,
			AfterState:     map[string]any{"code": p.Code, "name": p.Name},
			At:             now,
		})
	})
	if err != nil {
		return nil, err
	}
	return w, nil
}

func (s *Service) ListLegalEntities(ctx context.Context, principal permissions.Principal) ([]*domain.LegalEntity, error) {
	if err := s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{}); err != nil {
		return nil, err
	}
	var result []*domain.LegalEntity
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.legalEntities.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

func (s *Service) ListBranches(ctx context.Context, principal permissions.Principal) ([]*domain.Branch, error) {
	if err := s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{}); err != nil {
		return nil, err
	}
	var result []*domain.Branch
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.branches.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

func (s *Service) ListWarehouses(ctx context.Context, principal permissions.Principal, branchID uuid.UUID) ([]*domain.Warehouse, error) {
	if err := s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{BranchID: &branchID}); err != nil {
		return nil, err
	}
	var result []*domain.Warehouse
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.warehouses.ListByBranch(ctx, branchID)
		return err
	})
	return result, err
}
