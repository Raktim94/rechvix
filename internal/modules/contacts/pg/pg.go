// Package pg is the contacts module's PostgreSQL repository implementation.
package pg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rechvix/internal/modules/contacts/domain"
	"rechvix/internal/platform/database"
)

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- Parties ---

type PartyRepo struct{ pool *database.Pool }

func NewPartyRepo(pool *database.Pool) *PartyRepo { return &PartyRepo{pool: pool} }

func (r *PartyRepo) Create(ctx context.Context, p *domain.Party) error {
	const q = `
		INSERT INTO parties (id, organisation_id, party_type, legal_name, trade_name, phone, email, currency_code, credit_limit_amount, payment_terms_days, notes, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, p.ID, p.OrganisationID, string(p.PartyType), p.LegalName, nullIfEmpty(p.TradeName),
		nullIfEmpty(p.Phone), nullIfEmpty(p.Email), p.CurrencyCode, p.CreditLimitAmount, p.PaymentTermsDays, nullIfEmpty(p.Notes), string(p.Status), p.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicatePhone
		}
		return fmt.Errorf("contacts: inserting party: %w", err)
	}
	return nil
}

// ExistsByPhone is the app-layer uniqueness check that runs before the
// insert, so a duplicate phone comes back as domain.ErrDuplicatePhone
// from CreateParty's validation step rather than only from the race-safe
// unique-index translation in Create above.
func (r *PartyRepo) ExistsByPhone(ctx context.Context, orgID uuid.UUID, phone string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM parties WHERE organisation_id = $1 AND phone = $2)`
	var exists bool
	if err := r.pool.Q(ctx).QueryRow(ctx, q, orgID, phone).Scan(&exists); err != nil {
		return false, fmt.Errorf("contacts: checking phone uniqueness: %w", err)
	}
	return exists, nil
}

func (r *PartyRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Party, error) {
	const q = `
		SELECT id, organisation_id, party_type, legal_name, COALESCE(trade_name,''), COALESCE(phone,''), COALESCE(email,''),
		       currency_code, credit_limit_amount, payment_terms_days, COALESCE(notes,''), status, created_at, updated_at
		FROM parties WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	return scanParty(row)
}

func (r *PartyRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.Party, error) {
	const q = `
		SELECT id, organisation_id, party_type, legal_name, COALESCE(trade_name,''), COALESCE(phone,''), COALESCE(email,''),
		       currency_code, credit_limit_amount, payment_terms_days, COALESCE(notes,''), status, created_at, updated_at
		FROM parties WHERE organisation_id = $1 ORDER BY legal_name`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("contacts: listing parties: %w", err)
	}
	defer rows.Close()
	return scanParties(rows)
}

// SearchByName matches on name similarity (trigram) as well as a plain
// substring match on phone, so the sale-counter search box can find a
// customer by typing part of their phone number, not just their name.
func (r *PartyRepo) SearchByName(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]*domain.Party, error) {
	const q = `
		SELECT id, organisation_id, party_type, legal_name, COALESCE(trade_name,''), COALESCE(phone,''), COALESCE(email,''),
		       currency_code, credit_limit_amount, payment_terms_days, COALESCE(notes,''), status, created_at, updated_at
		FROM parties
		WHERE organisation_id = $1 AND (legal_name % $2 OR trade_name % $2 OR phone ILIKE '%' || $2 || '%')
		ORDER BY GREATEST(similarity(legal_name, $2), similarity(COALESCE(trade_name, ''), $2)) DESC
		LIMIT $3`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("contacts: searching parties: %w", err)
	}
	defer rows.Close()
	return scanParties(rows)
}

func scanParty(row pgx.Row) (*domain.Party, error) {
	var p domain.Party
	var partyType, status string
	if err := row.Scan(&p.ID, &p.OrganisationID, &partyType, &p.LegalName, &p.TradeName, &p.Phone, &p.Email,
		&p.CurrencyCode, &p.CreditLimitAmount, &p.PaymentTermsDays, &p.Notes, &status, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("contacts: querying party: %w", err)
	}
	p.PartyType = domain.PartyType(partyType)
	p.Status = domain.Status(status)
	return &p, nil
}

func scanParties(rows pgx.Rows) ([]*domain.Party, error) {
	var out []*domain.Party
	for rows.Next() {
		var p domain.Party
		var partyType, status string
		if err := rows.Scan(&p.ID, &p.OrganisationID, &partyType, &p.LegalName, &p.TradeName, &p.Phone, &p.Email,
			&p.CurrencyCode, &p.CreditLimitAmount, &p.PaymentTermsDays, &p.Notes, &status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("contacts: scanning party row: %w", err)
		}
		p.PartyType = domain.PartyType(partyType)
		p.Status = domain.Status(status)
		out = append(out, &p)
	}
	return out, rows.Err()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- Addresses ---

type AddressRepo struct{ pool *database.Pool }

func NewAddressRepo(pool *database.Pool) *AddressRepo { return &AddressRepo{pool: pool} }

func (r *AddressRepo) Create(ctx context.Context, a *domain.Address) error {
	const q = `
		INSERT INTO party_addresses (id, organisation_id, party_id, address_type, line1, line2, city, state, postal_code, country_code, is_default, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, a.ID, a.OrganisationID, a.PartyID, string(a.AddressType), a.Line1, nullIfEmpty(a.Line2),
		nullIfEmpty(a.City), nullIfEmpty(a.State), nullIfEmpty(a.PostalCode), a.CountryCode, a.IsDefault, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("contacts: inserting party_address: %w", err)
	}
	return nil
}

// GetByID is the additive extension Stage 8c needs (same rationale as
// TaxRegistrationRepository.GetByID, Stage 8): sales_documents stores
// billing_address_id/shipping_address_id, and building a CanonicalEWayBill
// needs to resolve those specific addresses by ID, not list-by-party.
func (r *AddressRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Address, error) {
	const q = `
		SELECT id, organisation_id, party_id, address_type, line1, COALESCE(line2,''), COALESCE(city,''), COALESCE(state,''), COALESCE(postal_code,''), country_code, is_default, created_at, updated_at
		FROM party_addresses WHERE organisation_id = $1 AND id = $2`
	var a domain.Address
	var addressType string
	err := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id).Scan(&a.ID, &a.OrganisationID, &a.PartyID, &addressType, &a.Line1, &a.Line2, &a.City, &a.State, &a.PostalCode, &a.CountryCode, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("contacts: querying party_address by id: %w", err)
	}
	a.AddressType = domain.AddressType(addressType)
	return &a, nil
}

func (r *AddressRepo) ListByParty(ctx context.Context, partyID uuid.UUID) ([]*domain.Address, error) {
	const q = `
		SELECT id, organisation_id, party_id, address_type, line1, COALESCE(line2,''), COALESCE(city,''), COALESCE(state,''), COALESCE(postal_code,''), country_code, is_default, created_at, updated_at
		FROM party_addresses WHERE party_id = $1 ORDER BY created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, partyID)
	if err != nil {
		return nil, fmt.Errorf("contacts: listing party_addresses: %w", err)
	}
	defer rows.Close()
	var out []*domain.Address
	for rows.Next() {
		var a domain.Address
		var addressType string
		if err := rows.Scan(&a.ID, &a.OrganisationID, &a.PartyID, &addressType, &a.Line1, &a.Line2, &a.City, &a.State, &a.PostalCode, &a.CountryCode, &a.IsDefault, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("contacts: scanning party_address row: %w", err)
		}
		a.AddressType = domain.AddressType(addressType)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// --- Tax registrations ---

type TaxRegistrationRepo struct{ pool *database.Pool }

func NewTaxRegistrationRepo(pool *database.Pool) *TaxRegistrationRepo {
	return &TaxRegistrationRepo{pool: pool}
}

func (r *TaxRegistrationRepo) Create(ctx context.Context, t *domain.TaxRegistration) error {
	const q = `
		INSERT INTO party_tax_registrations (id, organisation_id, party_id, country_code, registration_number, state_code, is_primary, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, t.ID, t.OrganisationID, t.PartyID, t.CountryCode, t.RegistrationNumber, nullIfEmpty(t.StateCode), t.IsPrimary, t.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return domain.ErrDuplicateRegistration
		}
		return fmt.Errorf("contacts: inserting party_tax_registration: %w", err)
	}
	return nil
}

func (r *TaxRegistrationRepo) ListByParty(ctx context.Context, partyID uuid.UUID) ([]*domain.TaxRegistration, error) {
	const q = `
		SELECT id, organisation_id, party_id, country_code, registration_number, COALESCE(state_code,''), is_primary, created_at
		FROM party_tax_registrations WHERE party_id = $1`
	rows, err := r.pool.Q(ctx).Query(ctx, q, partyID)
	if err != nil {
		return nil, fmt.Errorf("contacts: listing party_tax_registrations: %w", err)
	}
	defer rows.Close()
	var out []*domain.TaxRegistration
	for rows.Next() {
		var t domain.TaxRegistration
		if err := rows.Scan(&t.ID, &t.OrganisationID, &t.PartyID, &t.CountryCode, &t.RegistrationNumber, &t.StateCode, &t.IsPrimary, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("contacts: scanning party_tax_registration row: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (r *TaxRegistrationRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.TaxRegistration, error) {
	const q = `
		SELECT id, organisation_id, party_id, country_code, registration_number, COALESCE(state_code,''), is_primary, created_at
		FROM party_tax_registrations WHERE organisation_id = $1 AND id = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, id)
	var t domain.TaxRegistration
	if err := row.Scan(&t.ID, &t.OrganisationID, &t.PartyID, &t.CountryCode, &t.RegistrationNumber, &t.StateCode, &t.IsPrimary, &t.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("contacts: querying party_tax_registration by id: %w", err)
	}
	return &t, nil
}

func (r *TaxRegistrationRepo) GetByRegistrationNumber(ctx context.Context, orgID uuid.UUID, registrationNumber string) (*domain.TaxRegistration, error) {
	const q = `
		SELECT id, organisation_id, party_id, country_code, registration_number, COALESCE(state_code,''), is_primary, created_at
		FROM party_tax_registrations WHERE organisation_id = $1 AND registration_number = $2`
	row := r.pool.Q(ctx).QueryRow(ctx, q, orgID, registrationNumber)
	var t domain.TaxRegistration
	if err := row.Scan(&t.ID, &t.OrganisationID, &t.PartyID, &t.CountryCode, &t.RegistrationNumber, &t.StateCode, &t.IsPrimary, &t.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("contacts: querying party_tax_registration: %w", err)
	}
	return &t, nil
}
