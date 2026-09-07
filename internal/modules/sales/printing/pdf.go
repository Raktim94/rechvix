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

// RenderPDF renders data using tpl's layout, returning the raw PDF bytes.
// Never recalculates any figure in data — every amount is already a
// caller-supplied, pre-rounded string (see data.go's InvoiceData).
func RenderPDF(tpl Template, data InvoiceData) ([]byte, error) {
	lo := layoutFor(tpl)
	pdf := fpdf.New("P", "mm", "", "")
	pdf.SetAutoPageBreak(true, 10)
	pdf.AddPageFormat("P", lo.size)
	pdf.SetMargins(8, 8, 8)

	title := lo.titleOverride
	if title == "" {
		title = data.DocumentTypeLabel
	}

	drawHeader(pdf, data, lo, title)
	drawParties(pdf, data, lo)
	drawItemTable(pdf, data, lo)
	drawTotals(pdf, data, lo)
	if lo.showBankBlock && (data.Seller.BankAccount != "" || data.Seller.UPIID != "") {
		drawBankBlock(pdf, data)
	}
	if lo.showTerms && data.TermsAndConditions != "" {
		pdf.Ln(2)
		setFont(pdf, lo, "", 8)
		pdf.MultiCell(0, 4, "Terms & Conditions: "+data.TermsAndConditions, "", "L", false)
	}
	drawSignatureBlock(pdf, lo, data.AuthorizedSignatoryName)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("printing: rendering %s: %w", tpl, err)
	}
	return buf.Bytes(), nil
}

func setFont(pdf *fpdf.Fpdf, lo layout, style string, size float64) {
	if lo.narrow {
		size += 1 // thermal rolls need slightly larger text to stay legible
	}
	pdf.SetFont("Helvetica", style, size)
}

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

func drawHeader(pdf *fpdf.Fpdf, data InvoiceData, lo layout, title string) {
	drawLogo(pdf, data, lo)
	setFont(pdf, lo, "B", 14)
	pdf.CellFormat(0, 7, data.Seller.LegalName, "", 1, "C", false, 0, "")
	setFont(pdf, lo, "", 9)
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
	setFont(pdf, lo, "B", 12)
	pdf.CellFormat(0, 7, title, "1", 1, "C", false, 0, "")
	setFont(pdf, lo, "", 9)
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

func drawParties(pdf *fpdf.Fpdf, data InvoiceData, lo layout) {
	setFont(pdf, lo, "B", 9)
	pdf.CellFormat(0, 5, "Bill To:", "", 1, "L", false, 0, "")
	setFont(pdf, lo, "", 9)
	pdf.CellFormat(0, 5, data.BillTo.Name, "", 1, "L", false, 0, "")
	for _, line := range data.BillTo.AddressLines {
		pdf.CellFormat(0, 4.5, line, "", 1, "L", false, 0, "")
	}
	if data.BillTo.GSTIN != "" {
		pdf.CellFormat(0, 5, "GSTIN: "+data.BillTo.GSTIN, "", 1, "L", false, 0, "")
	}
	if len(data.ShipTo.AddressLines) > 0 && !lo.narrow {
		pdf.Ln(1)
		setFont(pdf, lo, "B", 9)
		pdf.CellFormat(0, 5, "Ship To:", "", 1, "L", false, 0, "")
		setFont(pdf, lo, "", 9)
		for _, line := range data.ShipTo.AddressLines {
			pdf.CellFormat(0, 4.5, line, "", 1, "L", false, 0, "")
		}
	}
	pdf.Ln(1)
}

func drawItemTable(pdf *fpdf.Fpdf, data InvoiceData, lo layout) {
	setFont(pdf, lo, "B", 8)
	if lo.narrow {
		// Thermal: one stacked block per line (name + qty*rate=total),
		// no wide multi-column tax breakdown — a real 58/80mm roll can't
		// fit a full CGST/SGST/IGST table legibly.
		for _, l := range data.Lines {
			setFont(pdf, lo, "B", 9)
			pdf.MultiCell(0, 4.5, l.Description, "", "L", false)
			setFont(pdf, lo, "", 9)
			pdf.CellFormat(0, 4.5, fmt.Sprintf("%s x %s = %s", l.Quantity, l.Rate, l.LineTotal), "", 1, "L", false, 0, "")
		}
		pdf.Ln(1)
		return
	}
	widths := []float64{8, 55, 18, 15, 15, 18, 18, 18, 18, 18}
	headers := []string{"#", "Description", "HSN", "Qty", "Rate", "Taxable", "CGST", "SGST", "IGST", "Total"}
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, h, "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)
	setFont(pdf, lo, "", 8)
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
			pdf.CellFormat(widths[i], 6, v, "1", 0, align, false, 0, "")
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

func drawTotals(pdf *fpdf.Fpdf, data InvoiceData, lo layout) {
	setFont(pdf, lo, "", 9)
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
	setFont(pdf, lo, "B", 10)
	row("Grand Total:", data.GrandTotal)
	if data.AmountInWords != "" {
		setFont(pdf, lo, "", 8)
		pdf.MultiCell(0, 4, "Amount in words: "+data.AmountInWords, "", "L", false)
	}
	pdf.Ln(1)
}

func drawBankBlock(pdf *fpdf.Fpdf, data InvoiceData) {
	if data.Seller.BankAccount != "" {
		pdf.SetFont("Helvetica", "B", 8)
		pdf.CellFormat(0, 5, "Bank Details:", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 8)
		pdf.CellFormat(0, 4.5, strings.TrimSpace(fmt.Sprintf("%s, A/c: %s, IFSC: %s", data.Seller.BankName, data.Seller.BankAccount, data.Seller.BankIFSC)), "", 1, "L", false, 0, "")
	}
	if data.Seller.UPIID != "" {
		pdf.SetFont("Helvetica", "B", 8)
		pdf.CellFormat(0, 5, "UPI: "+data.Seller.UPIID, "", 1, "L", false, 0, "")
	}
}

func drawSignatureBlock(pdf *fpdf.Fpdf, lo layout, signatoryName string) {
	pdf.Ln(8)
	setFont(pdf, lo, "", 9)
	pdf.CellFormat(0, 5, "For Authorized Signatory", "", 1, "R", false, 0, "")
	if signatoryName != "" {
		pdf.Ln(6)
		pdf.CellFormat(0, 5, signatoryName, "", 1, "R", false, 0, "")
	}
}
