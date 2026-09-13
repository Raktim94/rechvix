package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"rechvix/internal/modules/contacts/domain"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

// ImportParties bulk-creates customers/suppliers from parsed spreadsheet
// rows (brief §53). Expected columns: party_type (CUSTOMER, SUPPLIER, or
// BOTH), legal_name, phone (optional), email (optional), currency_code.
// Every row gets an outcome in the returned Report; a malformed row is
// recorded as an error, never silently skipped. Duplicate detection is by
// exact, case-insensitive (legal_name, party_type) pair within the
// organisation.
//
// dryRun=true validates and reports without writing anything.
func (s *Service) ImportParties(ctx context.Context, principal permissions.Principal, rows []importer.Row, dryRun bool) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	b := importer.NewBuilder(dryRun)

	var seen map[string]bool
	var seenPhones map[string]bool
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		existing, err := s.parties.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		seen = make(map[string]bool, len(existing))
		seenPhones = make(map[string]bool, len(existing))
		for _, p := range existing {
			seen[dedupeKey(string(p.PartyType), p.LegalName)] = true
			if p.Phone != "" {
				seenPhones[p.Phone] = true
			}
		}

		for _, row := range rows {
			partyType := domain.PartyType(strings.ToUpper(strings.TrimSpace(row.Fields["party_type"])))
			legalName := strings.TrimSpace(row.Fields["legal_name"])
			phone := strings.TrimSpace(row.Fields["phone"])
			email := strings.TrimSpace(row.Fields["email"])
			currencyCode := strings.ToUpper(strings.TrimSpace(row.Fields["currency_code"]))

			if !domain.ValidPartyType(partyType) {
				b.Error(row.Number, "party_type %q must be CUSTOMER, SUPPLIER, or BOTH", row.Fields["party_type"])
				continue
			}
			if legalName == "" {
				b.Error(row.Number, "legal_name is required")
				continue
			}
			if currencyCode == "" {
				b.Error(row.Number, "currency_code is required")
				continue
			}
			if phone != "" && !phonePattern.MatchString(phone) {
				b.Error(row.Number, "phone %q must be exactly 10 digits", phone)
				continue
			}
			if phone != "" && seenPhones[phone] {
				b.Duplicate(row.Number, "phone %q is already used by another customer/supplier", phone)
				continue
			}
			key := dedupeKey(string(partyType), legalName)
			if seen[key] {
				b.Duplicate(row.Number, "a %s named %q already exists", partyType, legalName)
				continue
			}

			if dryRun {
				b.Valid(row.Number)
				seen[key] = true
				if phone != "" {
					seenPhones[phone] = true
				}
				continue
			}

			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			now := s.now()
			p := &domain.Party{ID: id, OrganisationID: principal.OrganisationID, PartyType: partyType,
				LegalName: legalName, Phone: phone, Email: email, CurrencyCode: currencyCode,
				Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now}
			if err := s.parties.Create(ctx, p); err != nil {
				return err
			}
			seen[key] = true
			if phone != "" {
				seenPhones[phone] = true
			}
			b.Committed(row.Number)
		}
		return nil
	})
	if err != nil {
		return importer.Report{}, err
	}
	return b.Report(), nil
}

func dedupeKey(partyType, legalName string) string {
	return partyType + "|" + strings.ToLower(strings.TrimSpace(legalName))
}
