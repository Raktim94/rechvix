// Package app is the contacts module's application/use-case layer.
package app

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/contacts/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

// phonePattern is the 10-digit-only phone format enforced on every party
// (brief follow-up: phone doubles as the sale-counter customer lookup
// key, so it needs a fixed, predictable shape).
var phonePattern = regexp.MustCompile(`^\d{10}$`)

type Service struct {
	pool             database.Runner
	parties          domain.PartyRepository
	addresses        domain.AddressRepository
	taxRegistrations domain.TaxRegistrationRepository
	permissions      *permissions.Checker
	audit            audit.Recorder
	now              func() time.Time
}

func NewService(
	pool database.Runner,
	parties domain.PartyRepository,
	addresses domain.AddressRepository,
	taxRegistrations domain.TaxRegistrationRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{pool: pool, parties: parties, addresses: addresses, taxRegistrations: taxRegistrations, permissions: checker, audit: recorder, now: time.Now}
}

// view/manage use Checker.HasAny, not Require — parties (customers/
// suppliers) have no legal_entity_id of their own, shared org-wide
// across every company, same rationale as
// catalogue/app.Service.view/manage's identical comment.
func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.HasAny(ctx, principal, "contacts.view")
}

func (s *Service) manage(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.HasAny(ctx, principal, "contacts.manage")
}

type CreatePartyParams struct {
	PartyType         domain.PartyType
	LegalName         string
	TradeName         string
	Phone             string
	Email             string
	CurrencyCode      string
	CreditLimitAmount *decimal.Decimal
	PaymentTermsDays  *int
	Notes             string
}

// ValidateCreateParty checks CreatePartyParams against the same rules the
// database CHECK constraints enforce (migrations/0009_contacts.up.sql), so
// a bad request comes back as a clean 400 (contacts.domain.ErrInvalidPartyType
// / ErrLegalNameRequired) instead of a raw driver error. Pure and
// side-effect free, deliberately exported so it's independently unit
// testable without a database or fake repositories.
func ValidateCreateParty(p CreatePartyParams) error {
	if !domain.ValidPartyType(p.PartyType) {
		return domain.ErrInvalidPartyType
	}
	if p.LegalName == "" {
		return domain.ErrLegalNameRequired
	}
	if p.Phone != "" && !phonePattern.MatchString(p.Phone) {
		return domain.ErrInvalidPhone
	}
	return nil
}

func (s *Service) CreateParty(ctx context.Context, principal permissions.Principal, p CreatePartyParams) (*domain.Party, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if err := ValidateCreateParty(p); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("contacts: generating party id: %w", err)
	}
	now := s.now()
	party := &domain.Party{
		ID: id, OrganisationID: principal.OrganisationID, PartyType: p.PartyType, LegalName: p.LegalName,
		TradeName: p.TradeName, Phone: p.Phone, Email: p.Email, CurrencyCode: p.CurrencyCode,
		CreditLimitAmount: p.CreditLimitAmount, PaymentTermsDays: p.PaymentTermsDays, Notes: p.Notes,
		Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if p.Phone != "" {
			exists, err := s.parties.ExistsByPhone(ctx, principal.OrganisationID, p.Phone)
			if err != nil {
				return err
			}
			if exists {
				return domain.ErrDuplicatePhone
			}
		}
		if err := s.parties.Create(ctx, party); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "party.created", EntityType: "party", EntityID: &id,
			AfterState: map[string]any{"legal_name": p.LegalName, "party_type": p.PartyType}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return party, nil
}

func (s *Service) GetParty(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*domain.Party, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.Party
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.parties.GetByID(ctx, principal.OrganisationID, id)
		return err
	})
	return result, err
}

func (s *Service) ListParties(ctx context.Context, principal permissions.Principal) ([]*domain.Party, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.Party
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.parties.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return result, err
}

func (s *Service) SearchParties(ctx context.Context, principal permissions.Principal, query string, limit int) ([]*domain.Party, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var result []*domain.Party
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.parties.SearchByName(ctx, principal.OrganisationID, query, limit)
		return err
	})
	return result, err
}

type AddAddressParams struct {
	PartyID     uuid.UUID
	AddressType domain.AddressType
	Line1       string
	Line2       string
	City        string
	State       string
	PostalCode  string
	CountryCode string
	IsDefault   bool
}

func (s *Service) AddAddress(ctx context.Context, principal permissions.Principal, p AddAddressParams) (*domain.Address, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	if !domain.ValidAddressType(p.AddressType) {
		return nil, domain.ErrInvalidAddressType
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("contacts: generating party_address id: %w", err)
	}
	now := s.now()
	a := &domain.Address{
		ID: id, OrganisationID: principal.OrganisationID, PartyID: p.PartyID, AddressType: p.AddressType,
		Line1: p.Line1, Line2: p.Line2, City: p.City, State: p.State, PostalCode: p.PostalCode,
		CountryCode: p.CountryCode, IsDefault: p.IsDefault, CreatedAt: now, UpdatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.addresses.Create(ctx, a); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "party_address.created", EntityType: "party_address", EntityID: &id,
			AfterState: map[string]any{"party_id": p.PartyID, "address_type": p.AddressType}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) ListAddresses(ctx context.Context, principal permissions.Principal, partyID uuid.UUID) ([]*domain.Address, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.Address
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.addresses.ListByParty(ctx, partyID)
		return err
	})
	return result, err
}

type AddTaxRegistrationParams struct {
	PartyID            uuid.UUID
	CountryCode        string
	RegistrationNumber string
	StateCode          string
	IsPrimary          bool
}

func (s *Service) AddTaxRegistration(ctx context.Context, principal permissions.Principal, p AddTaxRegistrationParams) (*domain.TaxRegistration, error) {
	if err := s.manage(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("contacts: generating party_tax_registration id: %w", err)
	}
	now := s.now()
	t := &domain.TaxRegistration{
		ID: id, OrganisationID: principal.OrganisationID, PartyID: p.PartyID, CountryCode: p.CountryCode,
		RegistrationNumber: p.RegistrationNumber, StateCode: p.StateCode, IsPrimary: p.IsPrimary, CreatedAt: now,
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if err := s.taxRegistrations.Create(ctx, t); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "party_tax_registration.created", EntityType: "party_tax_registration", EntityID: &id,
			AfterState: map[string]any{"party_id": p.PartyID, "registration_number": p.RegistrationNumber}, At: now,
		})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) ListTaxRegistrations(ctx context.Context, principal permissions.Principal, partyID uuid.UUID) ([]*domain.TaxRegistration, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result []*domain.TaxRegistration
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.taxRegistrations.ListByParty(ctx, partyID)
		return err
	})
	return result, err
}

// GetTaxRegistrationForOtherModule is a cross-module read (Stage 8) — same
// pattern and rationale as organisation.GetLegalEntityForOtherModule: no
// permission check of its own (the caller's own already-checked path is
// what authorizes this), and does NOT open its own transaction — callers
// (einvoice.Service, invoked from apps/worker's outbox poller) must
// already be inside a RunScoped block.
func (s *Service) GetTaxRegistrationForOtherModule(ctx context.Context, orgID, id uuid.UUID) (*domain.TaxRegistration, error) {
	return s.taxRegistrations.GetByID(ctx, orgID, id)
}

// GetPartyForOtherModule and GetAddressForOtherModule are Stage 8c's
// additive extensions of the same cross-module-read convention as
// GetTaxRegistrationForOtherModule above — ewaybill's canonical model
// builder needs to resolve a sales document's customer party and
// billing/shipping address IDs.
func (s *Service) GetPartyForOtherModule(ctx context.Context, orgID, id uuid.UUID) (*domain.Party, error) {
	return s.parties.GetByID(ctx, orgID, id)
}

func (s *Service) GetAddressForOtherModule(ctx context.Context, orgID, id uuid.UUID) (*domain.Address, error) {
	return s.addresses.GetByID(ctx, orgID, id)
}

// ListTaxRegistrationsForOtherModule is GetTaxRegistrationForOtherModule's
// counterpart for the "which registration(s) does this party have"
// question, needed where the caller only has a party id, not a specific
// registration id — purchases.Service.FinalizeDocument resolving a
// supplier's GST state code for an inward tax calculation is the first
// caller (purchase_documents has no supplier_tax_registration_id column
// the way sales_documents has customer_tax_registration_id, since a
// purchase's line items are never billed on this app's own paperwork
// the way a sale's are). Same no-permission-check, no-own-transaction
// convention as every other *ForOtherModule method in this file.
func (s *Service) ListTaxRegistrationsForOtherModule(ctx context.Context, orgID, partyID uuid.UUID) ([]*domain.TaxRegistration, error) {
	return s.taxRegistrations.ListByParty(ctx, partyID)
}

// LookupByRegistrationNumber is the GSTIN search path (brief §24).
func (s *Service) LookupByRegistrationNumber(ctx context.Context, principal permissions.Principal, registrationNumber string) (*domain.TaxRegistration, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var result *domain.TaxRegistration
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.taxRegistrations.GetByRegistrationNumber(ctx, principal.OrganisationID, registrationNumber)
		return err
	})
	return result, err
}
