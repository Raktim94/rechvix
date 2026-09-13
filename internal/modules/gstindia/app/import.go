package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	"rechvix/internal/modules/gstindia/domain"
	inventoryapp "rechvix/internal/modules/inventory/app"
	inventorydomain "rechvix/internal/modules/inventory/domain"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

// ImportTaxRates bulk-creates tax_rate_master rows from parsed
// spreadsheet rows, and optionally updates an existing product's HSN/SAC
// code and/or stock on hand (brief follow-up to "Look up a rate by
// HSN/SAC code": catalogue's own ImportProducts CSV import only ever
// creates brand-new products, it can't touch one that already exists —
// this is the CSV path for updating an existing SKU's HSN/rate/stock).
// Expected columns: hsn_sac_code (required), gst_rate (required, plain
// decimal percentage), sku_code (optional — updates that product's
// HSN/SAC code to this row's hsn_sac_code), quantity (optional, only
// takes effect together with sku_code — records a stock increase of
// this many units via inventory's ADJUSTMENT_IN movement type).
// quantity only takes effect when the caller also passes a non-nil
// warehouseID (the warehouse a CSV row can't itself name) — same
// non-fatal, note-on-the-row treatment as catalogue.Service.
// ImportProducts' own opening_qty column when no warehouse is selected.
//
// Every row's rate creation reuses CreateRate — the same method backing
// the manual "add rate" form — rather than duplicating its logic.
// sku_code/quantity effects are best-effort, same non-fatal-per-row
// convention as ImportProducts' price/gst_rate/opening_qty columns: a
// failure there is a note on an otherwise COMMITTED row, never what
// makes the row itself fail (the tax rate it named was still saved).
//
// dryRun=true validates and reports without writing anything.
func (s *Service) ImportTaxRates(ctx context.Context, principal permissions.Principal, rows []importer.Row, dryRun bool, warehouseID *uuid.UUID) (importer.Report, error) {
	if err := s.manage(ctx, principal); err != nil {
		return importer.Report{}, err
	}
	b := importer.NewBuilder(dryRun)
	today := s.now()

	for _, row := range rows {
		hsn := strings.TrimSpace(row.Fields["hsn_sac_code"])
		gstRateRaw := strings.TrimSpace(row.Fields["gst_rate"])
		skuCode := strings.ToUpper(strings.TrimSpace(row.Fields["sku_code"]))
		quantityRaw := strings.TrimSpace(row.Fields["quantity"])

		if hsn == "" {
			b.Error(row.Number, "hsn_sac_code is required")
			continue
		}
		if gstRateRaw == "" {
			b.Error(row.Number, "gst_rate is required")
			continue
		}
		gstRate, err := decimal.NewFromString(gstRateRaw)
		if err != nil {
			b.Error(row.Number, "gst_rate %q is not a valid number", gstRateRaw)
			continue
		}
		var quantity *decimal.Decimal
		if quantityRaw != "" {
			q, err := decimal.NewFromString(quantityRaw)
			if err != nil {
				b.Error(row.Number, "quantity %q is not a valid number", quantityRaw)
				continue
			}
			quantity = &q
		}

		if dryRun {
			b.Valid(row.Number)
			continue
		}

		if _, err := s.CreateRate(ctx, principal, CreateRateParams{
			HSNSACCode: hsn, Classification: domain.ClassificationTaxable,
			GSTRate: gstRate, CessRate: decimal.Zero, ValidFrom: today,
		}); err != nil {
			b.Error(row.Number, "could not save tax rate: %s", err.Error())
			continue
		}

		var notes []string
		if skuCode != "" {
			if note, err := s.applySKUUpdate(ctx, principal, skuCode, hsn, quantity, warehouseID); err != nil {
				notes = append(notes, err.Error())
			} else if note != "" {
				notes = append(notes, note)
			}
		} else if quantity != nil {
			notes = append(notes, "quantity given but ignored: no sku_code on this row")
		}
		b.Committed(row.Number, notes...)
	}
	return b.Report(), nil
}

// applySKUUpdate is ImportTaxRates' best-effort sku_code/quantity
// handling, split out so the main loop stays focused on validation. Each
// call into catalogue/inventory below runs sequentially, AFTER
// CreateRate's own transaction has already committed — never nested
// inside a shared RunScoped block, since CreateRate/UpdateProduct/
// RecordAdjustment each open their OWN top-level transaction (pgx has no
// ambient-transaction detection, so nesting would silently start a
// second, independent one instead of participating) — same reasoning
// catalogue.Service.ImportProducts' pendingPostCommit doc comment
// already documents for its own price/tax/opening-stock hooks.
func (s *Service) applySKUUpdate(ctx context.Context, principal permissions.Principal, skuCode, hsn string, quantity *decimal.Decimal, warehouseID *uuid.UUID) (string, error) {
	if s.catalogue == nil {
		return "", fmt.Errorf("sku_code %q given but product updates are not wired", skuCode)
	}
	variant, product, err := s.catalogue.GetVariantBySKU(ctx, principal, skuCode)
	if err != nil {
		return "", fmt.Errorf("sku_code %q: %w", skuCode, err)
	}
	if _, err := s.catalogue.UpdateProduct(ctx, principal, product.ID, catalogueapp.UpdateProductParams{
		CategoryID: product.CategoryID, BrandID: product.BrandID, BaseUOMID: product.BaseUOMID,
		Name: product.Name, Description: product.Description, HSNSACCode: hsn,
	}); err != nil {
		return "", fmt.Errorf("sku_code %q: could not update HSN/SAC code: %w", skuCode, err)
	}
	if quantity == nil {
		return "", nil
	}
	if warehouseID == nil {
		return fmt.Sprintf("sku_code %q: quantity given but ignored: no warehouse selected", skuCode), nil
	}
	if s.inventory == nil {
		return fmt.Sprintf("sku_code %q: quantity given but ignored: stock updates are not wired", skuCode), nil
	}
	if _, _, err := s.inventory.RecordAdjustment(ctx, principal, inventoryapp.RecordAdjustmentParams{
		WarehouseID: *warehouseID, Reason: "Tax rate CSV import", Notes: "sku_code=" + skuCode,
		Lines: []inventoryapp.AdjustmentLineParams{{
			ProductVariantID: variant.ID, UnitID: product.BaseUOMID, Quantity: *quantity,
			MovementType: inventorydomain.MovementAdjustmentIn,
		}},
	}); err != nil {
		return "", fmt.Errorf("sku_code %q: could not adjust stock: %w", skuCode, err)
	}
	return "", nil
}
