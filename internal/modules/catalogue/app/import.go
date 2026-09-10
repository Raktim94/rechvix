package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/catalogue/domain"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

// pendingPriceTax is a committed row whose price and/or gst_rate columns
// still need setting — collected during the transaction (which has the
// variant id), applied AFTER it commits (see ImportProducts' own doc
// comment for why setting them can't happen inside the same
// transaction: SetPriceHook/SetTaxRateHook call into pricing/gstindia's
// own Service methods, which open their OWN top-level transaction via
// the same database.Pool.RunScoped this method is already inside —
// pgx has no ambient-transaction detection, so nesting would silently
// open a second, independent connection/transaction instead of
// participating in this one, breaking atomicity and (under READ
// COMMITTED) visibility of the not-yet-committed product/variant rows).
type pendingPriceTax struct {
	rowNumber  int
	variantID  uuid.UUID
	unitID     uuid.UUID
	hsnSacCode string
	price      *decimal.Decimal
	gstRate    *decimal.Decimal
}

// ImportProducts bulk-creates products from parsed spreadsheet rows
// (brief §53). Expected columns (case-sensitive header match): name,
// hsn_sac_code (optional), base_uom_code (must already exist for this
// organisation — create units first), sku_code (optional — generated
// from name when blank, same slug scheme CataloguePage's manual "add
// product" flow already uses client-side), price (optional, plain
// decimal, sets this variant's price on the organisation's default
// price list), gst_rate (optional, plain decimal percentage, sets a
// TAXABLE tax_rate_master row for the row's HSN/SAC code),
// category_name (optional, auto-created if it doesn't already exist for
// this organisation — same as clicking "Add" inline on the manual form),
// brand_name (optional, same auto-create behaviour), barcode (optional,
// must be unique for this organisation, checked against both existing
// rows in the database and earlier rows in this same file). Opening
// stock is deliberately NOT an import column — it's edited afterwards
// on the Inventory page, same path a manually-added product already
// uses. Every row gets an outcome in the returned Report — a malformed
// row is recorded as an error, never silently skipped. Duplicate
// detection is by exact, case-insensitive product name within the
// organisation.
//
// Every committed product also gets a real ProductVariant — a product
// with zero variants is invisible everywhere else in the app (billing
// lookup, inventory, purchases all key off ProductVariantID, never
// ProductID), which is exactly the state an earlier version of this
// method left an imported product in: found and fixed as part of
// wiring the first real UI onto this endpoint (docs/TODO.md Stage 14),
// not a change made for its own sake.
//
// price/gst_rate are best-effort, NOT part of what makes a row commit
// or fail: the product+variant is the row's real content, price/tax are
// a convenience on top. If SetPriceHook/SetTaxRateHook aren't wired
// (nil) or either fails for a specific row (e.g. no price list exists
// yet), that row still shows COMMITTED, just with a note in its Message
// explaining what wasn't set and needs finishing manually on the
// Pricing/GST pages — never a silent, invisible gap between "the CSV
// said this had a price" and "this product still has none."
//
// dryRun=true validates and reports without writing anything — the
// caller can show the report to a user before committing.
func (s *Service) ImportProducts(ctx context.Context, principal permissions.Principal, rows []importer.Row, dryRun bool) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	b := importer.NewBuilder(dryRun)
	var pending []pendingPriceTax

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

		// Unlike base_uom_code (which must already exist — a unit affects
		// stock/pricing math too broadly to guess), category_name and
		// brand_name are auto-created on first use, same as clicking
		// "Add" inline on the manual New Product form — a plain lookup
		// table with no downstream implications from getting one wrong.
		// Keyed lowercase so "Snacks" and "snacks" resolve to the same
		// row instead of creating a near-duplicate.
		categories, err := s.categories.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		categoryIDByName := make(map[string]uuid.UUID, len(categories))
		for _, c := range categories {
			categoryIDByName[strings.ToLower(strings.TrimSpace(c.Name))] = c.ID
		}
		brands, err := s.brands.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		brandIDByName := make(map[string]uuid.UUID, len(brands))
		for _, br := range brands {
			brandIDByName[strings.ToLower(strings.TrimSpace(br.Name))] = br.ID
		}

		// Tracks SKUs this batch has already claimed, so two rows in the
		// SAME file that'd otherwise generate the same slug don't both
		// try to claim it — a real, in-process race the per-candidate
		// GetBySKU lookup below can't see on its own, since dry_run never
		// writes anything for GetBySKU to find.
		claimedSKUs := make(map[string]bool)
		// Same idea for barcode, checked against the row's raw value
		// (case-sensitive, matching the UNIQUE(organisation_id, barcode)
		// constraint) rather than a slug.
		claimedBarcodes := make(map[string]bool)

		for _, row := range rows {
			name := strings.TrimSpace(row.Fields["name"])
			hsnSac := strings.TrimSpace(row.Fields["hsn_sac_code"])
			uomCode := strings.ToUpper(strings.TrimSpace(row.Fields["base_uom_code"]))
			requestedSKU := strings.ToUpper(strings.TrimSpace(row.Fields["sku_code"]))
			categoryName := strings.TrimSpace(row.Fields["category_name"])
			brandName := strings.TrimSpace(row.Fields["brand_name"])
			barcode := strings.TrimSpace(row.Fields["barcode"])

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

			price, err := parseOptionalDecimal(row.Fields["price"])
			if err != nil {
				b.Error(row.Number, "price %q is not a valid number", row.Fields["price"])
				continue
			}
			gstRate, err := parseOptionalDecimal(row.Fields["gst_rate"])
			if err != nil {
				b.Error(row.Number, "gst_rate %q is not a valid number", row.Fields["gst_rate"])
				continue
			}

			if barcode != "" {
				if claimedBarcodes[barcode] {
					b.Error(row.Number, "barcode %q is used by another row in this file", barcode)
					continue
				}
				if _, err := s.barcodes.GetByBarcode(ctx, principal.OrganisationID, barcode); err == nil {
					b.Error(row.Number, "barcode %q is already in use", barcode)
					continue
				} else if !errors.Is(err, domain.ErrNotFound) {
					return err
				}
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
				if barcode != "" {
					claimedBarcodes[barcode] = true
				}
				continue
			}

			now := s.now()

			var categoryID *uuid.UUID
			if categoryName != "" {
				ckey := strings.ToLower(categoryName)
				catID, ok := categoryIDByName[ckey]
				if !ok {
					catID, err = uuid.NewV7()
					if err != nil {
						return err
					}
					if err := s.categories.Create(ctx, &domain.Category{ID: catID, OrganisationID: principal.OrganisationID, Name: categoryName, CreatedAt: now, UpdatedAt: now}); err != nil {
						return err
					}
					categoryIDByName[ckey] = catID
				}
				categoryID = &catID
			}
			var brandID *uuid.UUID
			if brandName != "" {
				bkey := strings.ToLower(brandName)
				brID, ok := brandIDByName[bkey]
				if !ok {
					brID, err = uuid.NewV7()
					if err != nil {
						return err
					}
					if err := s.brands.Create(ctx, &domain.Brand{ID: brID, OrganisationID: principal.OrganisationID, Name: brandName, CreatedAt: now, UpdatedAt: now}); err != nil {
						return err
					}
					brandIDByName[bkey] = brID
				}
				brandID = &brID
			}

			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			p := &domain.Product{ID: id, OrganisationID: principal.OrganisationID, CategoryID: categoryID, BrandID: brandID, BaseUOMID: uomID,
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
			if barcode != "" {
				barcodeID, err := uuid.NewV7()
				if err != nil {
					return err
				}
				if err := s.barcodes.Create(ctx, &domain.Barcode{ID: barcodeID, OrganisationID: principal.OrganisationID, VariantID: variantID, UnitID: uomID, Barcode: barcode, CreatedAt: now}); err != nil {
					return err
				}
				claimedBarcodes[barcode] = true
			}
			existingNames[key] = true
			claimedSKUs[skuCode] = true

			if price != nil || gstRate != nil {
				pending = append(pending, pendingPriceTax{rowNumber: row.Number, variantID: variantID, unitID: uomID, hsnSacCode: hsnSac, price: price, gstRate: gstRate})
			} else {
				b.Committed(row.Number)
			}
		}
		return nil
	})
	if err != nil {
		return importer.Report{}, err
	}

	// Only reached once the transaction above has actually committed —
	// see pendingPriceTax's own doc comment for why this can't run
	// inside it.
	for _, pp := range pending {
		var notes []string
		if pp.price != nil {
			if s.setPriceHook == nil {
				notes = append(notes, "price was not set (no price list configured)")
			} else if err := s.setPriceHook(ctx, principal, pp.variantID, pp.unitID, *pp.price); err != nil {
				notes = append(notes, fmt.Sprintf("price could not be set: %s", err.Error()))
			}
		}
		if pp.gstRate != nil {
			if s.setTaxRateHook == nil {
				notes = append(notes, "gst_rate was not set")
			} else if err := s.setTaxRateHook(ctx, principal, pp.hsnSacCode, *pp.gstRate); err != nil {
				notes = append(notes, fmt.Sprintf("gst_rate could not be set: %s", err.Error()))
			}
		}
		b.Committed(pp.rowNumber, notes...)
	}

	return b.Report(), nil
}

// parseOptionalDecimal returns nil for an empty/whitespace-only field
// (the column was simply left blank — not every row needs a price or
// tax rate), or an error for anything present but not a valid decimal.
func parseOptionalDecimal(raw string) (*decimal.Decimal, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(trimmed)
	if err != nil {
		return nil, err
	}
	return &d, nil
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
