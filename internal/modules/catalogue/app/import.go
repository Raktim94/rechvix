package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"rechvix/internal/modules/catalogue/domain"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

// ImportProducts bulk-creates products from parsed spreadsheet rows
// (brief §53). Expected columns (case-sensitive header match): name,
// hsn_sac_code (optional), base_uom_code (must already exist for this
// organisation — create units first), sku_code (optional — generated
// from name when blank, same slug scheme CataloguePage's manual "add
// product" flow already uses client-side). Every row gets an outcome in
// the returned Report — a malformed row is recorded as an error, never
// silently skipped. Duplicate detection is by exact, case-insensitive
// product name within the organisation.
//
// Every committed product also gets a real ProductVariant — a product
// with zero variants is invisible everywhere else in the app (billing
// lookup, inventory, purchases all key off ProductVariantID, never
// ProductID), which is exactly the state an earlier version of this
// method left an imported product in: found and fixed as part of
// wiring the first real UI onto this endpoint (docs/TODO.md Stage 14),
// not a change made for its own sake.
//
// dryRun=true validates and reports without writing anything — the
// caller can show the report to a user before committing.
func (s *Service) ImportProducts(ctx context.Context, principal permissions.Principal, rows []importer.Row, dryRun bool) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	b := importer.NewBuilder(dryRun)

	var existingNames map[string]bool
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		existing, err := s.products.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		existingNames = make(map[string]bool, len(existing))
		for _, p := range existing {
			existingNames[strings.ToLower(strings.TrimSpace(p.Name))] = true
		}

		units, err := s.units.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		unitByCode := make(map[string]uuid.UUID, len(units))
		for _, u := range units {
			unitByCode[strings.ToUpper(u.Code)] = u.ID
		}

		// Tracks SKUs this batch has already claimed, so two rows in the
		// SAME file that'd otherwise generate the same slug don't both
		// try to claim it — a real, in-process race the per-candidate
		// GetBySKU lookup below can't see on its own, since dry_run never
		// writes anything for GetBySKU to find.
		claimedSKUs := make(map[string]bool)

		for _, row := range rows {
			name := strings.TrimSpace(row.Fields["name"])
			hsnSac := strings.TrimSpace(row.Fields["hsn_sac_code"])
			uomCode := strings.ToUpper(strings.TrimSpace(row.Fields["base_uom_code"]))
			requestedSKU := strings.ToUpper(strings.TrimSpace(row.Fields["sku_code"]))

			if name == "" {
				b.Error(row.Number, "name is required")
				continue
			}
			uomID, ok := unitByCode[uomCode]
			if !ok {
				b.Error(row.Number, "base_uom_code %q does not match any existing unit of measure for this organisation", uomCode)
				continue
			}
			key := strings.ToLower(name)
			if existingNames[key] {
				b.Duplicate(row.Number, "a product named %q already exists", name)
				continue
			}

			skuCode, err := s.resolveImportSKU(ctx, principal.OrganisationID, requestedSKU, name, claimedSKUs)
			if err != nil {
				b.Error(row.Number, "could not assign a SKU: %s", err.Error())
				continue
			}

			if dryRun {
				b.Valid(row.Number)
				existingNames[key] = true // a later row in the same file with the same name is still a duplicate
				claimedSKUs[skuCode] = true
				continue
			}

			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			now := s.now()
			p := &domain.Product{ID: id, OrganisationID: principal.OrganisationID, BaseUOMID: uomID,
				Name: name, HSNSACCode: hsnSac, Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now}
			if err := s.products.Create(ctx, p); err != nil {
				return err
			}
			variantID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			v := &domain.ProductVariant{ID: variantID, OrganisationID: principal.OrganisationID, ProductID: id,
				SKUCode: skuCode, Attributes: map[string]any{}, Status: domain.StatusActive, CreatedAt: now, UpdatedAt: now}
			if err := s.variants.Create(ctx, v); err != nil {
				return err
			}
			existingNames[key] = true
			claimedSKUs[skuCode] = true
			b.Committed(row.Number)
		}
		return nil
	})
	if err != nil {
		return importer.Report{}, err
	}
	return b.Report(), nil
}

// resolveImportSKU returns requestedSKU if set (still checked for
// collision — a caller-specified SKU is still a real SKU), or generates
// one from name using the same slug scheme apps/web's CataloguePage
// already uses client-side for its manual "add product" flow
// (uppercase, non-alphanumeric runs collapsed to a single "-", trimmed
// to a reasonable length) — kept here too, in Go, since ImportProducts
// has no client-side step to rely on. Appends -2, -3, ... on collision
// against both this organisation's existing variants (GetBySKU) and
// this same import batch (claimedSKUs, mutated by the caller once a
// candidate is accepted — this function only reads it).
func (s *Service) resolveImportSKU(ctx context.Context, orgID uuid.UUID, requestedSKU, name string, claimedSKUs map[string]bool) (string, error) {
	base := requestedSKU
	if base == "" {
		base = slugifySKU(name)
	}
	if base == "" {
		return "", fmt.Errorf("could not derive a SKU from name %q", name)
	}
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1) // base, base-2, base-3, ...
		}
		if claimedSKUs[candidate] {
			continue
		}
		_, err := s.variants.GetBySKU(ctx, orgID, candidate)
		if errors.Is(err, domain.ErrNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find a free SKU based on %q after 1000 attempts", base)
}

func slugifySKU(name string) string {
	var b strings.Builder
	lastWasDash := false
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasDash = false
		case !lastWasDash:
			b.WriteRune('-')
			lastWasDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}
