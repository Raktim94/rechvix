package printing

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// layout is the per-template geometry — page size and which optional
// blocks/columns to show. Thermal formats are narrow, tall receipt rolls;
// A4/compact are standard document sizes. This one struct plus one shared
// draw routine (below) is the "template engine" brief §19 asks for across
// all eleven listed layouts — a real, working, parameterized renderer
// rather than eleven hand-copied near-duplicates.
type layout struct {
	size          fpdf.SizeType
	narrow        bool // thermal: single-column stacked item block, larger font
	showBankBlock bool
	showTerms     bool
	titleOverride string // e.g. "PURCHASE ORDER" for the PO template, "" to use DocumentTypeLabel
}

func layoutFor(t Template) layout {
	switch t {
	case TemplateA4GSTInvoice:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, showBankBlock: true, showTerms: true}
	case TemplateCompactInvoice:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 148.5}, showBankBlock: false, showTerms: false}
	case TemplateThermal80mm:
		return layout{size: fpdf.SizeType{Wd: 80, Ht: 800}, narrow: true}
	case TemplateThermal58mm:
		return layout{size: fpdf.SizeType{Wd: 58, Ht: 800}, narrow: true}
	case TemplateQuotation:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, showTerms: true}
	case TemplatePurchaseOrder:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, showTerms: true, titleOverride: "PURCHASE ORDER"}
	case TemplateDeliveryChallan:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}}
	case TemplateReceipt:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 148.5}, titleOverride: "RECEIPT"}
	case TemplateStatement:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, titleOverride: "STATEMENT OF ACCOUNT"}
	case TemplateCreditNote:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, showTerms: true, titleOverride: "CREDIT NOTE"}
	case TemplateDebitNote:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, showTerms: true, titleOverride: "DEBIT NOTE"}
	default:
		return layout{size: fpdf.SizeType{Wd: 210, Ht: 297}, showBankBlock: true, showTerms: true}
	}
}

// rgb is a small named-fields color, only so styleFor's table below reads
// as a palette rather than a wall of positional ints.
type rgb struct{ r, g, b int }

// style is the per-theme VISUAL treatment — orthogonal to layout's
// per-document-type GEOMETRY. Every (Template, Theme) pair is valid: a
// theme changes font family/accent color/border-vs-rule treatment, never
// page size or which blocks appear. ThemeClassic is deliberately styled
// to render identically to this package's original single hardcoded
// look (black text, full-grid borders, no fill) — existing callers that
// don't pass a theme yet keep getting exactly what they got before
// themes existed.
type style struct {
	fontFamily string // fpdf core font: "Helvetica", "Times", or "Courier" — no embedded font files, matching this project's no-external-dependency PDF approach
	accent     rgb
	// headerFill/tableHeaderFill: true paints a solid accent block behind
	// that section (bold, high-contrast look); false leaves it
	// transparent with accent-colored text/rules instead (lighter look).
	headerFill      bool
	tableHeaderFill bool
	// boxedTable: true draws a full cell grid (every row/column bordered,
	// the original look); false draws only a rule under the header row
	// and under each data row (a plainer, more minimal look).
	boxedTable bool
	ruleWidth  float64 // mm, drawn line weight for rules/borders this theme uses
	titleStyle string  // fpdf font style for the title/heading: "B", "BI", "I"
}

// Theme selects a PDF's visual design — independent of Template's
// per-document-type geometry (a TAX_INVOICE and a QUOTATION can both be
// rendered in, say, ThemeModern). Named after the look each is going for,
// not a numbered scheme, so a shop owner picking one in PrintTemplateMenu
// sees a description they can actually judge instead of "Theme 3".
type Theme string

const (
	// ThemeClassic is the default and matches this package's original,
	// single hardcoded look exactly — full-grid bordered tables, black
	// text, no color — so nothing regresses for an existing installation
	// until it deliberately picks a different theme.
	ThemeClassic Theme = "CLASSIC"
	// ThemeModern: sans-serif, a blue accent on the title and totals,
	// rule-only tables (no full grid) for a cleaner, less boxy look.
	ThemeModern Theme = "MODERN"
	// ThemeMinimal: the lightest-weight theme — thin gray rules only,
	// no fills, no bold title band, maximum whitespace.
	ThemeMinimal Theme = "MINIMAL"
	// ThemeBold: solid accent-color fills behind the header and table
	// header row, white reversed text on those fills, heavier rules —
	// the highest-contrast, most attention-grabbing theme.
	ThemeBold Theme = "BOLD"
	// ThemeElegant: a serif font throughout with a deep accent color, for
	// a more formal/traditional printed-document look.
	ThemeElegant Theme = "ELEGANT"
)

// AllThemes is every valid Theme, in the order a picker UI should list
// them — used by the frontend's theme-selection menu via the themes
// listing endpoint, so the set of valid values lives in exactly one
// place.
var AllThemes = []Theme{ThemeClassic, ThemeModern, ThemeMinimal, ThemeBold, ThemeElegant}

func styleFor(th Theme) style {
	switch th {
	case ThemeModern:
		return style{fontFamily: "Helvetica", accent: rgb{30, 100, 170}, tableHeaderFill: true, ruleWidth: 0.3, titleStyle: "B"}
	case ThemeMinimal:
		return style{fontFamily: "Helvetica", accent: rgb{130, 130, 130}, ruleWidth: 0.2, titleStyle: ""}
	case ThemeBold:
		return style{fontFamily: "Helvetica", accent: rgb{20, 40, 90}, headerFill: true, tableHeaderFill: true, ruleWidth: 0.5, titleStyle: "B"}
	case ThemeElegant:
		return style{fontFamily: "Times", accent: rgb{110, 20, 45}, boxedTable: true, ruleWidth: 0.3, titleStyle: "BI"}
	default: // ThemeClassic
		return style{fontFamily: "Helvetica", accent: rgb{0, 0, 0}, boxedTable: true, ruleWidth: 0.2, titleStyle: "B"}
	}
}

// RenderPDF renders data using tpl's layout and th's visual theme,
// returning the raw PDF bytes. Never recalculates any figure in data —
// every amount is already a caller-supplied, pre-rounded string (see
// data.go's InvoiceData).
func RenderPDF(tpl Template, th Theme, data InvoiceData) ([]byte, error) {
	lo := layoutFor(tpl)
	st := styleFor(th)
	pdf := fpdf.New("P", "mm", "", "")
	pdf.SetAutoPageBreak(true, 10)
	pdf.AddPageFormat("P", lo.size)
	pdf.SetMargins(8, 8, 8)
	pdf.SetLineWidth(st.ruleWidth)

	title := lo.titleOverride
	if title == "" {
		title = data.DocumentTypeLabel
	}

	drawHeader(pdf, data, lo, st, title)
	drawParties(pdf, data, lo, st)
	drawItemTable(pdf, data, lo, st)
	drawTotals(pdf, data, lo, st)
	if lo.showBankBlock && (data.Seller.BankAccount != "" || data.Seller.UPIID != "") {
		drawBankBlock(pdf, data, st)
	}
	if lo.showTerms && data.TermsAndConditions != "" {
		pdf.Ln(2)
		setFont(pdf, lo, st, "", 8)
		pdf.MultiCell(0, 4, "Terms & Conditions: "+data.TermsAndConditions, "", "L", false)
	}
	drawSignatureBlock(pdf, lo, st, data.AuthorizedSignatoryName)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("printing: rendering %s/%s: %w", tpl, th, err)
	}
	return buf.Bytes(), nil
}

func setFont(pdf *fpdf.Fpdf, lo layout, st style, fontStyle string, size float64) {
	if lo.narrow {
		size += 1 // thermal rolls need slightly larger text to stay legible
	}
	pdf.SetFont(st.fontFamily, fontStyle, size)
}

func setAccentText(pdf *fpdf.Fpdf, st style) { pdf.SetTextColor(st.accent.r, st.accent.g, st.accent.b) }
func resetTextColor(pdf *fpdf.Fpdf)          { pdf.SetTextColor(0, 0, 0) }
func setAccentDraw(pdf *fpdf.Fpdf, st style) { pdf.SetDrawColor(st.accent.r, st.accent.g, st.accent.b) }
func resetDrawColor(pdf *fpdf.Fpdf)          { pdf.SetDrawColor(0, 0, 0) }

// drawLogo places the seller's logo in the top-left corner using absolute
// positioning (flow=false) — it doesn't move the cursor, so the centered
// legal-name/address block below draws exactly as if the logo weren't
// there. Skipped on thermal layouts: a 58/80mm roll has no room for a
// corner image next to centered header text at any usable size.
func drawLogo(pdf *fpdf.Fpdf, data InvoiceData, lo layout) {
	if lo.narrow || len(data.Seller.LogoPNG) == 0 {
		return
	}
	opts := fpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}
	pdf.RegisterImageOptionsReader("seller-logo", opts, bytes.NewReader(data.Seller.LogoPNG))
	pdf.ImageOptions("seller-logo", 8, 8, 18, 0, false, opts, 0, "")
}

func drawHeader(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, title string) {
	drawLogo(pdf, data, lo)
	setFont(pdf, lo, st, "B", 14)
	pdf.CellFormat(0, 7, data.Seller.LegalName, "", 1, "C", false, 0, "")
	setFont(pdf, lo, st, "", 9)
	for _, line := range data.Seller.AddressLines {
		pdf.CellFormat(0, 5, line, "", 1, "C", false, 0, "")
	}
	if contact := sellerContactLine(data.Seller); contact != "" {
		pdf.CellFormat(0, 5, contact, "", 1, "C", false, 0, "")
	}
	if data.Seller.GSTIN != "" {
		pdf.CellFormat(0, 5, "GSTIN: "+data.Seller.GSTIN, "", 1, "C", false, 0, "")
	}
	pdf.Ln(2)

	setFont(pdf, lo, st, st.titleStyle, 12)
	switch {
	case st.headerFill:
		pdf.SetFillColor(st.accent.r, st.accent.g, st.accent.b)
		pdf.SetTextColor(255, 255, 255)
		pdf.CellFormat(0, 8, title, "", 1, "C", true, 0, "")
		resetTextColor(pdf)
	case st.boxedTable: // Classic/Elegant: a bordered title cell, same as this package's original look
		pdf.CellFormat(0, 7, title, "1", 1, "C", false, 0, "")
	default: // Modern/Minimal: plain accent-colored text, no box
		setAccentText(pdf, st)
		pdf.CellFormat(0, 7, title, "", 1, "C", false, 0, "")
		resetTextColor(pdf)
	}

	setFont(pdf, lo, st, "", 9)
	pdf.CellFormat(0, 5, fmt.Sprintf("No: %s   Date: %s", data.DocumentNumber, data.IssueDate.Format("02-Jan-2006")), "", 1, "L", false, 0, "")
	if data.PlaceOfSupply != "" {
		pdf.CellFormat(0, 5, "Place of Supply: "+data.PlaceOfSupply, "", 1, "L", false, 0, "")
	}
	if data.IRN != "" {
		pdf.CellFormat(0, 5, "IRN: "+data.IRN, "", 1, "L", false, 0, "")
	}
	if data.EWBNumber != "" {
		line := "e-Way Bill No: " + data.EWBNumber
		if data.EWBValidUntil != nil {
			line += "   Valid Until: " + data.EWBValidUntil.Format("02-Jan-2006 15:04")
		}
		pdf.CellFormat(0, 5, line, "", 1, "L", false, 0, "")
	}
	pdf.Ln(1)
}

// sellerContactLine joins whichever of phone/email/website are actually
// set into one "Phone: ... | Email: ... | Web: ..." line — blank pieces
// are simply omitted rather than rendering "Phone:  | Email: ...".
func sellerContactLine(s SellerInfo) string {
	var parts []string
	if s.Phone != "" {
		parts = append(parts, "Phone: "+s.Phone)
	}
	if s.Email != "" {
		parts = append(parts, "Email: "+s.Email)
	}
	if s.Website != "" {
		parts = append(parts, "Web: "+s.Website)
	}
	return strings.Join(parts, "  |  ")
}

func drawParties(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style) {
	setFont(pdf, lo, st, "B", 9)
	pdf.CellFormat(0, 5, "Bill To:", "", 1, "L", false, 0, "")
	setFont(pdf, lo, st, "", 9)
	pdf.CellFormat(0, 5, data.BillTo.Name, "", 1, "L", false, 0, "")
	for _, line := range data.BillTo.AddressLines {
		pdf.CellFormat(0, 4.5, line, "", 1, "L", false, 0, "")
	}
	if data.BillTo.GSTIN != "" {
		pdf.CellFormat(0, 5, "GSTIN: "+data.BillTo.GSTIN, "", 1, "L", false, 0, "")
	}
	if len(data.ShipTo.AddressLines) > 0 && !lo.narrow {
		pdf.Ln(1)
		setFont(pdf, lo, st, "B", 9)
		pdf.CellFormat(0, 5, "Ship To:", "", 1, "L", false, 0, "")
		setFont(pdf, lo, st, "", 9)
		for _, line := range data.ShipTo.AddressLines {
			pdf.CellFormat(0, 4.5, line, "", 1, "L", false, 0, "")
		}
	}
	pdf.Ln(1)
}

func drawItemTable(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style) {
	setFont(pdf, lo, st, "B", 8)
	if lo.narrow {
		// Thermal: one stacked block per line (name + qty*rate=total),
		// no wide multi-column tax breakdown — a real 58/80mm roll can't
		// fit a full CGST/SGST/IGST table legibly. Untouched by theme —
		// there's no room on a receipt roll for fills/accent rules.
		for _, l := range data.Lines {
			setFont(pdf, lo, st, "B", 9)
			pdf.MultiCell(0, 4.5, l.Description, "", "L", false)
			setFont(pdf, lo, st, "", 9)
			pdf.CellFormat(0, 4.5, fmt.Sprintf("%s x %s = %s", l.Quantity, l.Rate, l.LineTotal), "", 1, "L", false, 0, "")
		}
		pdf.Ln(1)
		return
	}
	widths := []float64{8, 55, 18, 15, 15, 18, 18, 18, 18, 18}
	headers := []string{"#", "Description", "HSN", "Qty", "Rate", "Taxable", "CGST", "SGST", "IGST", "Total"}
	headerBorder := "1"
	if !st.boxedTable {
		headerBorder = "B"
	}
	if st.tableHeaderFill {
		pdf.SetFillColor(st.accent.r, st.accent.g, st.accent.b)
		pdf.SetTextColor(255, 255, 255)
	} else if !st.boxedTable {
		setAccentDraw(pdf, st)
	}
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, h, headerBorder, 0, "C", st.tableHeaderFill, 0, "")
	}
	pdf.Ln(-1)
	resetTextColor(pdf)
	resetDrawColor(pdf)

	setFont(pdf, lo, st, "", 8)
	rowBorder := "1"
	if !st.boxedTable {
		rowBorder = "B"
	}
	for _, l := range data.Lines {
		row := []string{
			fmt.Sprintf("%d", l.SNo), l.Description, l.HSNSAC, l.Quantity, l.Rate, l.TaxableValue,
			taxCell(l.CGSTRate, l.CGSTValue), taxCell(l.SGSTRate, l.SGSTValue), taxCell(l.IGSTRate, l.IGSTValue), l.LineTotal,
		}
		for i, v := range row {
			align := "L"
			if i != 1 {
				align = "R"
			}
			pdf.CellFormat(widths[i], 6, v, rowBorder, 0, align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.Ln(1)
}

func taxCell(rate, value string) string {
	if rate == "" || rate == "0" {
		return "-"
	}
	return fmt.Sprintf("%s%%/%s", rate, value)
}

func drawTotals(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style) {
	setFont(pdf, lo, st, "", 9)
	row := func(label, value string) {
		if value == "" {
			return
		}
		pdf.CellFormat(0, 5, fmt.Sprintf("%-30s %s", label, value), "", 1, "R", false, 0, "")
	}
	row("Taxable Amount:", data.SubtotalTaxable)
	row("CGST:", data.TotalCGST)
	row("SGST:", data.TotalSGST)
	row("IGST:", data.TotalIGST)
	row("Cess:", data.TotalCess)
	row("Round Off:", data.RoundOff)
	if data.PreviousBalance != nil {
		row("Previous Balance:", data.PreviousBalance.StringFixed(0))
	}

	// Grand Total always gets the theme's strongest visual treatment — a
	// rule above it in the accent color, and accent-colored bold text
	// (white-on-fill for Bold, plain accent color for everything else).
	// Skipped on thermal layouts: a 58/80mm roll has no room for a themed
	// rule any wider than the item table already draws, same reasoning
	// as drawItemTable's own narrow-layout early return.
	if !lo.narrow {
		setAccentDraw(pdf, st)
		pageWidth, _ := pdf.GetPageSize()
		_, _, right, _ := pdf.GetMargins()
		x, y := pdf.GetX(), pdf.GetY()
		pdf.Line(x, y, pageWidth-right, y)
		resetDrawColor(pdf)
		pdf.Ln(1)
	}
	setFont(pdf, lo, st, "B", 10)
	if st.headerFill {
		pdf.SetFillColor(st.accent.r, st.accent.g, st.accent.b)
		pdf.SetTextColor(255, 255, 255)
		pdf.CellFormat(0, 7, fmt.Sprintf("%-30s %s", "Grand Total:", data.GrandTotal), "", 1, "R", true, 0, "")
		resetTextColor(pdf)
	} else {
		setAccentText(pdf, st)
		row("Grand Total:", data.GrandTotal)
		resetTextColor(pdf)
	}
	if data.AmountInWords != "" {
		setFont(pdf, lo, st, "", 8)
		pdf.MultiCell(0, 4, "Amount in words: "+data.AmountInWords, "", "L", false)
	}
	pdf.Ln(1)
}

func drawBankBlock(pdf *fpdf.Fpdf, data InvoiceData, st style) {
	if data.Seller.BankAccount != "" {
		pdf.SetFont(st.fontFamily, "B", 8)
		pdf.CellFormat(0, 5, "Bank Details:", "", 1, "L", false, 0, "")
		pdf.SetFont(st.fontFamily, "", 8)
		pdf.CellFormat(0, 4.5, strings.TrimSpace(fmt.Sprintf("%s, A/c: %s, IFSC: %s", data.Seller.BankName, data.Seller.BankAccount, data.Seller.BankIFSC)), "", 1, "L", false, 0, "")
	}
	if data.Seller.UPIID != "" {
		pdf.SetFont(st.fontFamily, "B", 8)
		pdf.CellFormat(0, 5, "UPI: "+data.Seller.UPIID, "", 1, "L", false, 0, "")
	}
}

func drawSignatureBlock(pdf *fpdf.Fpdf, lo layout, st style, signatoryName string) {
	pdf.Ln(8)
	setFont(pdf, lo, st, "", 9)
	pdf.CellFormat(0, 5, "For Authorized Signatory", "", 1, "R", false, 0, "")
	if signatoryName != "" {
		pdf.Ln(6)
		pdf.CellFormat(0, 5, signatoryName, "", 1, "R", false, 0, "")
	}
}
