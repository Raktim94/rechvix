package app

import (
	"context"
	"fmt"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	taxdomain "rechvix/internal/modules/taxation/domain"
	verifactuapp "rechvix/internal/modules/verifactu/app"
	"rechvix/internal/platform/geo"
	"strconv"
	"strings"
	"time"

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
	return s.BuildInvoiceDataInLanguage(ctx, principal, documentID, "")
}

// BuildInvoiceDataInLanguage is BuildInvoiceData with a language chosen for this
// one copy ("" = no override). The language used is, in order: the override, the
// document's own setting, the company's setting, the language of the company's
// country. It only affects the generic (non-India) layout.
func (s *Service) BuildInvoiceDataInLanguage(ctx context.Context, principal permissions.Principal, documentID uuid.UUID, override string) (*printing.InvoiceData, error) {
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
				billAddr = addressLines(a, legalEntity.CountryCode)
			}
			if doc.ShippingAddressID != nil && a.ID == *doc.ShippingAddressID {
				shipAddr = addressLines(a, legalEntity.CountryCode)
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

	// Non-GST regimes: itemise tax by name and rate, and collect the legal notes.
	genericTax := legalEntity.CountryCode != "IN" && legalEntity.CountryCode != ""
	var taxTotals []printing.TaxTotal
	var taxNotes []string
	if genericTax {
		taxTotals, taxNotes = genericTaxSummary(taxLines, componentsByLine)
		perLine := genericLineTaxes(taxLines, componentsByLine)
		for i := range items {
			if lt, ok := perLine[items[i].SNo]; ok {
				items[i].TaxRate, items[i].TaxValue = lt.rate, lt.value
				// Outside India the printed line total includes the tax, matching the grand total.
				items[i].LineTotal = lt.gross
				items[i].HSNSAC = taxCategoryKind(items[i].HSNSAC)
			}
		}
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

	out := &printing.InvoiceData{
		Seller: printing.SellerInfo{
			LegalName:    legalEntity.LegalName,
			GSTIN:        indiaOnly(genericTax, legalEntity.GSTIN),
			TaxID:        legalEntity.TaxID,
			TaxIDLabel:   taxIDLabel(genericTax),
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
		BillTo:            printing.PartyInfo{Name: customer.LegalName, GSTIN: indiaOnly(genericTax, customerGSTIN), TaxID: indiaOnly(!genericTax, customerGSTIN), TaxIDLabel: taxIDLabel(genericTax), AddressLines: billAddr},
		ShipTo:            printing.PartyInfo{Name: customer.LegalName, AddressLines: shipAddr},
		DocumentTypeLabel: documentLabel(doc.DocumentType, genericTax),
		DocumentNumber:    doc.DocumentNumber,
		IssueDate:         doc.IssueDate,
		DueDate:           doc.DueDate,
		PlaceOfSupply:     placeOfSupplyLabel(doc.PlaceOfSupplyStateCode),
		CustomerReference: doc.CustomerReference,
		Transporter:       doc.Transporter,
		VehicleNumber:     doc.VehicleNumber,
		Lines:             items,
		GenericTax:        genericTax,
		BaseCurrency:      foreignBase(doc),
		ExchangeRate:      foreignRate(doc),
		GrandTotalBase:    foreignTotalBase(doc),
		Language:          firstNonEmpty(override, doc.InvoiceLanguage, legalEntity.InvoiceLanguage, printing.LanguageFor(legalEntity.CountryCode)),
		DateLayout:        dateLayoutForDocument(genericTax, legalEntity.CountryCode),
		SupplyDate:        supplyDateIfGeneric(genericTax, doc.SupplyDate),
		Currency:          taxDoc.CurrencyCode,
		TaxTotals:         taxTotals,
		TaxNotes:          taxNotes,
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
	}
	if s.verifactu != nil {
		var qr *verifactuapp.QRInfo
		if err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
			var err error
			qr, err = s.verifactu.QRForDocument(ctx, doc.ID)
			return err
		}); err != nil {
			return nil, fmt.Errorf("sales: loading the VERI*FACTU QR for print: %w", err)
		}
		if qr != nil {
			out.Verifactu = &printing.VerifactuQR{URL: qr.URL, Heading: qr.Heading, Legend: qr.Legend, Short: qr.Short}
		}
	}
	return out, nil
}

// indiaOnly returns v unless the document uses a non-GST regime, in which case
// the India-specific field stays empty.
func indiaOnly(generic bool, v string) string {
	if generic {
		return ""
	}
	return v
}

func taxIDLabel(generic bool) string {
	if generic {
		return "Tax ID"
	}
	return "GSTIN"
}

// documentLabel is the printed heading. Outside India a tax invoice is simply an
// "INVOICE"; the GST wording would be wrong on a Spanish, UK or US document.
func documentLabel(t domain.DocumentType, generic bool) string {
	label := printing.DocumentTypeLabel(t)
	if generic && label == "TAX INVOICE" {
		return "INVOICE"
	}
	return label
}

// taxCategoryKind reduces the stored tax code to its kind (STANDARD, REDUCED, ZERO,
// EXEMPT, TAXABLE); the renderer words it in the document language and the rate
// itself is printed in the Tax % column.
func taxCategoryKind(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	switch {
	case c == "STANDARD", c == "ZERO", c == "EXEMPT", c == "TAXABLE":
		return c
	case strings.HasPrefix(c, "RATE:"), c == "REDUCED":
		return "REDUCED"
	}
	return code
}

type lineTax struct{ rate, value, gross string }

// genericLineTaxes returns, by line number, the combined tax rate ("21%" or
// "21%+5.2%") and amount of each line for non-GST regimes. It only formats
// amounts the tax engine already computed.
func genericLineTaxes(taxLines []*taxdomain.TaxLine, comps map[uuid.UUID][]*taxdomain.TaxComponentRow) map[int]lineTax {
	out := make(map[int]lineTax, len(taxLines))
	for _, tl := range taxLines {
		n, err := strconv.Atoi(tl.LineRef)
		if err != nil {
			continue
		}
		var rates []string
		for _, c := range comps[tl.ID] {
			rates = append(rates, c.Rate.String()+"%")
		}
		out[n] = lineTax{rate: strings.Join(rates, "+"), value: tl.TotalTaxAmount.StringFixed(money.RoundHalfUp), gross: tl.GrossAmount.StringFixed(money.RoundHalfUp)}
	}
	return out
}

// taxNoteCode returns the classification itself when it needs a printed
// statement (the renderer owns the wording, per language), "" for ordinary taxed
// supplies.
func taxNoteCode(classification string) string {
	switch classification {
	case "REVERSE_CHARGE", "INTRA_EU_B2B", "EXPORT", "EXEMPT", "ZERO_RATED",
		"EXEMPT_CERTIFICATE", "NOT_COLLECTED", "STATE_HAS_NO_SALES_TAX":
		return classification
	}
	return ""
}

// genericTaxSummary aggregates the document's tax components by name and rate
// (in first-seen order) and collects the distinct legal notes.
func genericTaxSummary(taxLines []*taxdomain.TaxLine, comps map[uuid.UUID][]*taxdomain.TaxComponentRow) ([]printing.TaxTotal, []string) {
	type key struct{ name, rate string }
	sums := map[key]money.Money{}
	var order []key
	var notes []string
	seenNote := map[string]bool{}
	for _, tl := range taxLines {
		if n := taxNoteCode(tl.Classification); n != "" && !seenNote[n] {
			seenNote[n] = true
			notes = append(notes, n)
		}
		for _, c := range comps[tl.ID] {
			k := key{c.ComponentType, c.Rate.String()}
			cur, ok := sums[k]
			if !ok {
				order = append(order, k)
				sums[k] = c.Amount
				continue
			}
			if total, err := cur.Add(c.Amount); err == nil {
				sums[k] = total
			}
		}
	}
	out := make([]printing.TaxTotal, 0, len(order))
	for _, k := range order {
		out = append(out, printing.TaxTotal{Label: k.name, Rate: k.rate, Amount: sums[k].StringFixed(money.RoundHalfUp)})
	}
	return out, notes
}

// addressLines formats an address the way the seller's country writes it, dropping
// empty parts: India "City, State PIN" (unchanged), US "City, ST 12345", Spain and
// most of the EU "28001 Madrid (Madrid)", UK "City POSTCODE". A country line is added
// when the address is in a different country from the seller.
func addressLines(a *contactsdomain.Address, sellerCountry string) []string {
	var city string
	switch strings.ToUpper(strings.TrimSpace(sellerCountry)) {
	case "", "IN":
		return []string{a.Line1, a.Line2, fmt.Sprintf("%s, %s %s", a.City, a.State, a.PostalCode)}
	case "US":
		city = strings.TrimSpace(a.City + ", " + strings.TrimSpace(a.State+" "+a.PostalCode))
		city = strings.Trim(city, ", ")
	case "GB":
		city = strings.TrimSpace(a.City + " " + a.PostalCode)
	default:
		city = strings.TrimSpace(a.PostalCode + " " + a.City)
		if a.State != "" {
			city += " (" + a.State + ")"
		}
	}
	out := []string{a.Line1, a.Line2, city}
	if c := strings.ToUpper(strings.TrimSpace(a.CountryCode)); c != "" && c != strings.ToUpper(strings.TrimSpace(sellerCountry)) {
		if country, ok := geo.Lookup(c); ok {
			out = append(out, country.Name)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// dateLayoutFor is the date format of the seller's country, so a US invoice reads
// 10/21/2026, a German one 21.10.2026 and a Spanish one 21/10/2026; elsewhere the
// unambiguous 21-Oct-2026 (also India's long-standing format).
func dateLayoutFor(country string) string {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "US":
		return "01/02/2006"
	case "DE", "AT":
		return "02.01.2006"
	case "ES", "FR", "IT", "PT", "NL":
		return "02/01/2006"
	}
	return "02-Jan-2006"
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

func dateLayoutForDocument(generic bool, country string) string {
	if !generic {
		return ""
	}
	return dateLayoutFor(country)
}

func supplyDateIfGeneric(generic bool, d *time.Time) *time.Time {
	if !generic {
		return nil
	}
	return d
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// foreignBase is the company currency when the document is in another currency, else "".
func foreignBase(doc *domain.Document) string {
	if doc.CurrencyCode != doc.BaseCurrencyCode {
		return doc.BaseCurrencyCode
	}
	return ""
}

func foreignRate(doc *domain.Document) string {
	if doc.CurrencyCode == doc.BaseCurrencyCode {
		return ""
	}
	return doc.ExchangeRate.String()
}

func foreignTotalBase(doc *domain.Document) string {
	if doc.CurrencyCode == doc.BaseCurrencyCode || doc.GrandTotalBaseAmount == nil {
		return ""
	}
	return doc.GrandTotalBaseAmount.StringFixed(money.RoundHalfUp)
}
