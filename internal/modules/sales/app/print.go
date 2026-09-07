package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	orgdomain "rechvix/internal/modules/organisation/domain"
	"rechvix/internal/modules/sales/domain"
	"rechvix/internal/modules/sales/printing"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

// BuildInvoiceData assembles a printing.InvoiceData for a document —
// requires sales.view (this is a read, not a mutation, so it uses the
// view permission, not finalize) — by pulling together this module's own
// document/lines, the taxation snapshot (Stage 5a), the customer's
// contact details, and the seller's legal entity, exactly the set of
// modules docs/architecture.md §19 says an A4 GST invoice needs. Only
// FINALIZED documents have a tax snapshot to print from; printing a DRAFT
// is not supported (there is nothing statutory to print yet) and returns
// domain.ErrDocumentNotDraft's sibling check inverted — a DRAFT has no
// TaxDocumentID, so the nil check below is what actually enforces this,
// deliberately, rather than adding a redundant status check.
// BuildInvoiceDataForShareLink resolves documentID for an anonymous
// share-link recipient (internal/modules/notifications' redeemPDF flow,
// wired via httpapi.DocumentRenderer in apps/server/main.go) —
// authorized by IMPERSONATING createdBy, the real user who created the
// share link and so already passed notifications.share's own permission
// check at creation time, not by skipping authorization. The document
// actually returned is still hard-scoped to exactly documentID (never
// anything else createdBy could otherwise read), so this widens nothing
// beyond "let this one already-shared document be seen by whoever holds
// the unguessable link" — the whole point of a share link existing. If
// createdBy's own access is later revoked, BuildInvoiceData's own
// s.view check correctly starts failing here too (fails closed, never
// open).
func (s *Service) BuildInvoiceDataForShareLink(ctx context.Context, orgID, createdBy, documentID uuid.UUID) (*printing.InvoiceData, error) {
	return s.BuildInvoiceData(ctx, permissions.Principal{UserID: createdBy, OrganisationID: orgID}, documentID)
}

func (s *Service) BuildInvoiceData(ctx context.Context, principal permissions.Principal, documentID uuid.UUID) (*printing.InvoiceData, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	doc, lines, err := s.GetDocument(ctx, principal, documentID)
	if err != nil {
		return nil, err
	}
	if doc.TaxDocumentID == nil {
		return nil, fmt.Errorf("sales: %w: document has not been finalized yet, nothing to print", domain.ErrDocumentNotDraft)
	}

	taxDoc, taxLines, componentsByLine, err := s.taxation.GetByReference(ctx, principal.OrganisationID, "sales_document", doc.ID)
	if err != nil {
		return nil, fmt.Errorf("sales: loading tax snapshot for print: %w", err)
	}
	taxLineByRef := make(map[string]*struct {
		CGSTRate, CGSTValue, SGSTRate, SGSTValue, IGSTRate, IGSTValue, TaxableValue string
	})
	for _, tl := range taxLines {
		entry := &struct {
			CGSTRate, CGSTValue, SGSTRate, SGSTValue, IGSTRate, IGSTValue, TaxableValue string
		}{TaxableValue: tl.TaxableAmount.StringFixed(money.RoundHalfUp)}
		for _, c := range componentsByLine[tl.ID] {
			v := c.Amount.StringFixed(money.RoundHalfUp)
			r := c.Rate.String()
			switch c.ComponentType {
			case "CGST":
				entry.CGSTRate, entry.CGSTValue = r, v
			case "SGST", "UTGST":
				entry.SGSTRate, entry.SGSTValue = r, v
			case "IGST":
				entry.IGSTRate, entry.IGSTValue = r, v
			}
		}
		taxLineByRef[tl.LineRef] = entry
	}

	// GetLegalEntityForOtherModule is nest-safe (no own transaction) —
	// correct when FinalizeDocument calls it from inside an already-open
	// RunScoped block, but BuildInvoiceData has no such block of its own
	// (its other calls, GetDocument/GetByReference, each open and close
	// their own). Wrap this specific call so its RLS-protected read
	// actually has app.current_organisation_id set — omitting this was a
	// real bug caught by TestSales_Print_A4Invoice_RendersNonEmptyPDF:
	// without it, the read silently failed closed as ErrNotFound.
	var legalEntity *orgdomain.LegalEntity
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		legalEntity, err = s.organisation.GetLegalEntityForOtherModule(ctx, principal.OrganisationID, doc.LegalEntityID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sales: loading seller legal entity for print: %w", err)
	}
	customer, err := s.contacts.GetParty(ctx, principal, doc.CustomerPartyID)
	if err != nil {
		return nil, fmt.Errorf("sales: loading customer for print: %w", err)
	}
	var billAddr, shipAddr []string
	if doc.BillingAddressID != nil || doc.ShippingAddressID != nil {
		addresses, err := s.contacts.ListAddresses(ctx, principal, doc.CustomerPartyID)
		if err != nil {
			return nil, fmt.Errorf("sales: loading customer addresses for print: %w", err)
		}
		for _, a := range addresses {
			if doc.BillingAddressID != nil && a.ID == *doc.BillingAddressID {
				billAddr = []string{a.Line1, a.Line2, fmt.Sprintf("%s, %s %s", a.City, a.State, a.PostalCode)}
			}
			if doc.ShippingAddressID != nil && a.ID == *doc.ShippingAddressID {
				shipAddr = []string{a.Line1, a.Line2, fmt.Sprintf("%s, %s %s", a.City, a.State, a.PostalCode)}
			}
		}
	}
	var customerGSTIN string
	if doc.CustomerTaxRegistrationID != nil {
		regs, err := s.contacts.ListTaxRegistrations(ctx, principal, doc.CustomerPartyID)
		if err != nil {
			return nil, fmt.Errorf("sales: loading customer tax registrations for print: %w", err)
		}
		for _, reg := range regs {
			if reg.ID == *doc.CustomerTaxRegistrationID {
				customerGSTIN = reg.RegistrationNumber
			}
		}
	}

	items := make([]printing.LineItem, 0, len(lines))
	for _, l := range lines {
		_, product, err := s.catalogue.GetVariantWithProduct(ctx, principal, l.ProductVariantID)
		if err != nil {
			return nil, fmt.Errorf("sales: resolving product for print, line %d: %w", l.LineNumber, err)
		}
		tl := taxLineByRef[fmt.Sprintf("%d", l.LineNumber)]
		item := printing.LineItem{
			SNo: l.LineNumber, Description: product.Name, HSNSAC: l.HSNSACCode,
			Quantity: l.Quantity.String(), Rate: l.UnitPrice.StringFixed(money.RoundHalfUp),
			LineTotal: l.LineTotal.StringFixed(money.RoundHalfUp),
		}
		if tl != nil {
			item.TaxableValue = tl.TaxableValue
			item.CGSTRate, item.CGSTValue = tl.CGSTRate, tl.CGSTValue
			item.SGSTRate, item.SGSTValue = tl.SGSTRate, tl.SGSTValue
			item.IGSTRate, item.IGSTValue = tl.IGSTRate, tl.IGSTValue
		}
		items = append(items, item)
	}

	var totalCGST, totalSGST, totalIGST, totalCess string
	for _, tl := range taxLines {
		for _, c := range componentsByLine[tl.ID] {
			v := c.Amount.StringFixed(money.RoundHalfUp)
			switch c.ComponentType {
			case "CGST":
				totalCGST = addFixed(totalCGST, v)
			case "SGST", "UTGST":
				totalSGST = addFixed(totalSGST, v)
			case "IGST":
				totalIGST = addFixed(totalIGST, v)
			case "CESS":
				totalCess = addFixed(totalCess, v)
			}
		}
	}

	// A document's own TermsAndConditions (set at billing time) always
	// wins; the legal entity's DefaultTermsAndConditions (Settings →
	// invoice branding, migrations/0034) is only the fallback for a
	// document that never set one — never overwrites an explicit
	// per-invoice value.
	terms := doc.TermsAndConditions
	if terms == "" {
		terms = legalEntity.DefaultTermsAndConditions
	}

	return &printing.InvoiceData{
		Seller: printing.SellerInfo{
			LegalName:    legalEntity.LegalName,
			GSTIN:        legalEntity.GSTIN,
			AddressLines: splitNonEmptyLines(legalEntity.Address),
			Phone:        legalEntity.Phone,
			Email:        legalEntity.Email,
			Website:      legalEntity.Website,
			LogoPNG:      legalEntity.LogoPNG,
			BankName:     legalEntity.BankName,
			BankAccount:  legalEntity.BankAccountNumber,
			BankIFSC:     legalEntity.BankIFSC,
			UPIID:        legalEntity.UPIID,
		},
		BillTo:            printing.PartyInfo{Name: customer.LegalName, GSTIN: customerGSTIN, AddressLines: billAddr},
		ShipTo:            printing.PartyInfo{Name: customer.LegalName, AddressLines: shipAddr},
		DocumentTypeLabel: printing.DocumentTypeLabel(doc.DocumentType),
		DocumentNumber:    doc.DocumentNumber,
		IssueDate:         doc.IssueDate,
		DueDate:           doc.DueDate,
		PlaceOfSupply:     doc.PlaceOfSupplyStateCode,
		CustomerReference: doc.CustomerReference,
		Transporter:       doc.Transporter,
		VehicleNumber:     doc.VehicleNumber,
		Lines:             items,
		SubtotalTaxable:   taxDoc.TotalTaxableAmount.StringFixed(money.RoundHalfUp),
		TotalCGST:         totalCGST,
		TotalSGST:         totalSGST,
		TotalIGST:         totalIGST,
		TotalCess:         totalCess,
		GrandTotal:        taxDoc.GrandTotal.StringFixed(money.RoundHalfUp),
		// PreviousBalance intentionally left nil — see printing.InvoiceData's
		// doc comment: no customer ledger exists until Stage 6.
		Notes:                   doc.Notes,
		TermsAndConditions:      terms,
		AuthorizedSignatoryName: legalEntity.AuthorizedSignatoryName,
	}, nil
}

// splitNonEmptyLines turns LegalEntity.Address's free-text, newline
// separated storage into printing.SellerInfo.AddressLines — blank lines
// (a trailing newline, Windows \r\n, accidental double blank line) are
// dropped rather than rendered as an empty row on the printed header.
func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// addFixed adds two already-StringFixed decimal strings ("" treated as
// zero), returning a StringFixed result — a display-layer helper for
// re-summing already-computed, already-rounded component amounts into a
// document total. Not a tax calculation: every value it touches was
// already computed and rounded by gstindia.Engine.
func addFixed(a, b string) string {
	da, errA := decimal.NewFromString(a)
	if errA != nil {
		da = decimal.Zero
	}
	db, errB := decimal.NewFromString(b)
	if errB != nil {
		db = decimal.Zero
	}
	return da.Add(db).StringFixed(2)
}
