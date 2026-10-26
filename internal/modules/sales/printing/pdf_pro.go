package printing

import (
	"bytes"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// The A4/compact document layout: a two-column header (seller left, title and
// document details right), two-column parties, an item table whose rows grow
// to fit wrapped descriptions, and a totals box beside the payment block.
// Thermal rolls keep the stacked layout in pdf.go. Nothing here recalculates
// a figure; every amount is a string the caller already rounded.

var (
	mutedText = rgb{105, 113, 123}
	ruleGray  = rgb{214, 219, 224}
	panelGray = rgb{244, 246, 248}
)

const (
	proMargin  = 12.0
	proRowPad  = 1.3
	proLineH   = 4.2
	proFootGap = 14.0 // room kept clear at the bottom of a page
)

func textRGB(pdf *fpdf.Fpdf, c rgb) { pdf.SetTextColor(c.r, c.g, c.b) }
func drawRGB(pdf *fpdf.Fpdf, c rgb) { pdf.SetDrawColor(c.r, c.g, c.b) }
func fillRGB(pdf *fpdf.Fpdf, c rgb) { pdf.SetFillColor(c.r, c.g, c.b) }

// lineCount is how many lines text needs in a cell of width w (mm).
func lineCount(pdf *fpdf.Fpdf, text string, w float64) int {
	if strings.TrimSpace(text) == "" {
		return 1
	}
	n := 0
	for _, part := range strings.Split(text, "\n") {
		n += len(pdf.SplitLines([]byte(part), w))
		if len(pdf.SplitLines([]byte(part), w)) == 0 {
			n++
		}
	}
	return n
}

func drawProDocument(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, title string, lb Labels) error {
	pageW, _ := pdf.GetPageSize()
	if err := drawVerifactuQR(pdf, data, lo, st); err != nil {
		return err
	}
	drawProHeader(pdf, data, lo, st, title, lb, pageW)
	drawProParties(pdf, data, lo, st, lb, pageW)
	drawProItems(pdf, data, lo, st, lb, pageW)
	drawProTotals(pdf, data, lo, st, lb, pageW)
	return nil
}

func drawProHeader(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, title string, lb Labels, pageW float64) {
	layoutDate := data.DateLayout
	if layoutDate == "" {
		layoutDate = "02-Jan-2006"
	}
	left := []string{}
	for _, l := range data.Seller.AddressLines {
		if strings.TrimSpace(l) != "" {
			left = append(left, l)
		}
	}
	if c := sellerContactLine(data.Seller, lb); c != "" {
		left = append(left, c)
	}
	if data.Seller.GSTIN != "" {
		left = append(left, "GSTIN: "+data.Seller.GSTIN)
	}
	if data.Seller.TaxID != "" {
		left = append(left, lb.TaxID+": "+data.Seller.TaxID)
	}
	meta := [][2]string{{lb.No, data.DocumentNumber}, {lb.Date, data.IssueDate.Format(layoutDate)}}
	if data.DueDate != nil {
		meta = append(meta, [2]string{"Due", data.DueDate.Format(layoutDate)})
	}
	if data.GenericTax && data.SupplyDate != nil && !data.SupplyDate.Equal(data.IssueDate) {
		meta = append(meta, [2]string{lb.SupplyDate, data.SupplyDate.Format(layoutDate)})
	}
	if data.PlaceOfSupply != "" {
		meta = append(meta, [2]string{"Place of Supply", data.PlaceOfSupply})
	}
	if data.IRN != "" {
		meta = append(meta, [2]string{"IRN", data.IRN})
	}
	if data.EWBNumber != "" {
		v := data.EWBNumber
		if data.EWBValidUntil != nil {
			v += "  (valid until " + data.EWBValidUntil.Format("02-Jan-2006 15:04") + ")"
		}
		meta = append(meta, [2]string{"e-Way Bill", v})
	}

	logoH := 0.0
	if len(data.Seller.LogoPNG) > 0 {
		logoH = 16
	}
	leftH := logoH + 8 + float64(len(left))*proLineH
	rightH := 10 + float64(len(meta))*proLineH
	band := 10 + maxf(leftH, rightH)
	top := 12.0
	filled := st.headerFill
	if filled {
		fillRGB(pdf, st.accent)
		pdf.Rect(0, 0, pageW, band+top-4, "F")
	} else if !st.boxedTable {
		fillRGB(pdf, st.accent)
		pdf.Rect(0, 0, pageW, 2.5, "F")
	}
	ink := rgb{22, 30, 38}
	soft := mutedText
	if filled {
		ink, soft = rgb{255, 255, 255}, rgb{225, 232, 244}
	}

	x, y := proMargin, top
	if logoH > 0 {
		opts := fpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}
		pdf.RegisterImageOptionsReader("seller-logo", opts, bytes.NewReader(data.Seller.LogoPNG))
		pdf.ImageOptions("seller-logo", x, y, 0, logoH, false, opts, 0, "")
		y += logoH + 2
	}
	textRGB(pdf, ink)
	setFont(pdf, lo, st, "B", 14)
	pdf.SetXY(x, y)
	pdf.CellFormat(105, 6.5, data.Seller.LegalName, "", 1, "L", false, 0, "")
	setFont(pdf, lo, st, "", 8.5)
	textRGB(pdf, soft)
	for _, l := range left {
		pdf.SetX(x)
		pdf.CellFormat(105, proLineH, l, "", 1, "L", false, 0, "")
	}
	leftEnd := pdf.GetY()

	rw := 82.0
	rx := pageW - proMargin - rw
	ry := top
	setFont(pdf, lo, st, st.titleStyle, 19)
	if filled {
		textRGB(pdf, ink)
	} else {
		setAccentText(pdf, st)
	}
	pdf.SetXY(rx, ry)
	pdf.CellFormat(rw, 9, strings.ToUpper(title), "", 1, "R", false, 0, "")
	setFont(pdf, lo, st, "", 8.5)
	for _, m := range meta {
		label := m[0] + ":"
		setFont(pdf, lo, st, "", 8.5)
		lw := pdf.GetStringWidth(label) + 1.5
		setFont(pdf, lo, st, "B", 8.5)
		vw := pdf.GetStringWidth(m[1]) + 1
		if lw+vw > rw {
			vw = rw - lw
		}
		pdf.SetXY(rx+rw-lw-vw, pdf.GetY())
		setFont(pdf, lo, st, "", 8.5)
		textRGB(pdf, soft)
		pdf.CellFormat(lw, proLineH, label, "", 0, "L", false, 0, "")
		textRGB(pdf, ink)
		setFont(pdf, lo, st, "B", 8.5)
		pdf.CellFormat(vw, proLineH, m[1], "", 1, "L", false, 0, "")
	}
	rightEnd := pdf.GetY()

	end := maxf(leftEnd, rightEnd)
	if filled {
		end = maxf(end, band+top-4)
	}
	resetTextColor(pdf)
	if !filled {
		drawRGB(pdf, ruleGray)
		if st.boxedTable {
			setAccentDraw(pdf, st)
			pdf.SetLineWidth(0.6)
		}
		pdf.Line(proMargin, end+2, pageW-proMargin, end+2)
		pdf.SetLineWidth(st.ruleWidth)
		resetDrawColor(pdf)
	}
	pdf.SetXY(proMargin, end+6)
}

func drawProParties(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, lb Labels, pageW float64) {
	colW := (pageW - 2*proMargin - 8) / 2
	y0 := pdf.GetY()
	block := func(x float64, label, name string, lines []string) float64 {
		pdf.SetXY(x, y0)
		setFont(pdf, lo, st, "B", 7.5)
		setAccentText(pdf, st)
		pdf.CellFormat(colW, 4, label, "", 1, "L", false, 0, "")
		pdf.SetX(x)
		resetTextColor(pdf)
		setFont(pdf, lo, st, "B", 10)
		pdf.CellFormat(colW, 5.2, name, "", 1, "L", false, 0, "")
		setFont(pdf, lo, st, "", 8.5)
		textRGB(pdf, mutedText)
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			pdf.SetX(x)
			pdf.CellFormat(colW, proLineH, l, "", 1, "L", false, 0, "")
		}
		resetTextColor(pdf)
		return pdf.GetY()
	}
	bill := append([]string{}, data.BillTo.AddressLines...)
	if data.BillTo.GSTIN != "" {
		bill = append(bill, "GSTIN: "+data.BillTo.GSTIN)
	}
	if data.BillTo.TaxID != "" {
		bill = append(bill, lb.TaxID+": "+data.BillTo.TaxID)
	}
	end := block(proMargin, lb.BillTo+":", data.BillTo.Name, bill)
	x2 := proMargin + colW + 8
	switch {
	case len(data.ShipTo.AddressLines) > 0:
		end = maxf(end, block(x2, lb.ShipTo+":", data.ShipTo.Name, data.ShipTo.AddressLines))
	case data.CustomerReference != "" || data.Transporter != "" || data.VehicleNumber != "":
		var extra []string
		if data.Transporter != "" {
			extra = append(extra, "Transporter: "+data.Transporter)
		}
		if data.VehicleNumber != "" {
			extra = append(extra, "Vehicle: "+data.VehicleNumber)
		}
		name := data.CustomerReference
		if name == "" {
			name = "-"
		}
		end = maxf(end, block(x2, "Reference:", name, extra))
	}
	pdf.SetXY(proMargin, end+4)
}

type proColumn struct {
	head  string
	w     float64
	align string
}

func proColumns(data InvoiceData, lb Labels, content float64) []proColumn {
	var cols []proColumn
	if data.GenericTax {
		cols = []proColumn{{"#", 8, "C"}, {lb.Description, 0, "L"}, {lb.TaxCategory, 24, "L"}, {lb.Qty, 19, "R"}, {lb.Rate, 20, "R"}, {lb.Taxable, 24, "R"}, {lb.TaxPct, 13, "R"}, {lb.Tax, 20, "R"}, {lb.Total, 24, "R"}}
	} else {
		cols = []proColumn{{"#", 8, "C"}, {lb.Description, 0, "L"}, {"HSN/SAC", 18, "L"}, {lb.Qty, 19, "R"}, {lb.Rate, 20, "R"}, {lb.Taxable, 24, "R"}, {"CGST / SGST / IGST", 38, "R"}, {lb.Total, 26, "R"}}
	}
	fixed := 0.0
	for _, c := range cols {
		fixed += c.w
	}
	for i := range cols {
		if cols[i].w == 0 {
			cols[i].w = content - fixed
		}
	}
	return cols
}

func proRow(data InvoiceData, l LineItem, lb Labels) []string {
	if data.GenericTax {
		rate, tax := l.TaxRate, l.TaxValue
		if rate == "" {
			rate = "0%"
		}
		if tax == "" {
			tax = "0"
		}
		cat := l.HSNSAC
		if v, ok := lb.Categories[cat]; ok {
			cat = v
		}
		return []string{fmt.Sprintf("%d", l.SNo), l.Description, cat, l.Quantity + " " + l.UnitOfMeasure, l.Rate, l.TaxableValue, rate, tax, l.LineTotal}
	}
	var gst []string
	for _, p := range [][3]string{{"CGST", l.CGSTRate, l.CGSTValue}, {"SGST", l.SGSTRate, l.SGSTValue}, {"IGST", l.IGSTRate, l.IGSTValue}} {
		if p[1] != "" && p[1] != "0" {
			gst = append(gst, fmt.Sprintf("%s %s%%  %s", p[0], p[1], p[2]))
		}
	}
	if len(gst) == 0 {
		gst = []string{"-"}
	}
	return []string{fmt.Sprintf("%d", l.SNo), l.Description, l.HSNSAC, l.Quantity + " " + l.UnitOfMeasure, l.Rate, l.TaxableValue, strings.Join(gst, "\n"), l.LineTotal}
}

func drawProItems(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, lb Labels, pageW float64) {
	_, pageH := pdf.GetPageSize()
	cols := proColumns(data, lb, pageW-2*proMargin)

	header := func() {
		x, y := proMargin, pdf.GetY()
		h := 7.0
		if st.tableHeaderFill {
			fillRGB(pdf, st.accent)
			pdf.Rect(x, y, pageW-2*proMargin, h, "F")
			pdf.SetTextColor(255, 255, 255)
		} else {
			fillRGB(pdf, panelGray)
			pdf.Rect(x, y, pageW-2*proMargin, h, "F")
			setAccentText(pdf, st)
		}
		setFont(pdf, lo, st, "B", 8)
		for _, c := range cols {
			pdf.SetXY(x+0.8, y)
			pdf.CellFormat(c.w-1.6, h, c.head, "", 0, c.align, false, 0, "")
			x += c.w
		}
		resetTextColor(pdf)
		pdf.SetXY(proMargin, y+h)
	}
	header()

	for i, l := range data.Lines {
		row := proRow(data, l, lb)
		lines := 1
		for ci, c := range cols {
			if ci == 1 {
				setFont(pdf, lo, st, "B", 8.5)
			} else {
				setFont(pdf, lo, st, "", 8.5)
			}
			if n := lineCount(pdf, row[ci], c.w-1.6); n > lines {
				lines = n
			}
		}
		h := float64(lines)*proLineH + 2*proRowPad
		if pdf.GetY()+h > pageH-proFootGap {
			pdf.AddPage()
			pdf.SetY(proMargin)
			header()
		}
		x, y := proMargin, pdf.GetY()
		if i%2 == 1 && !st.boxedTable {
			fillRGB(pdf, rgb{250, 251, 252})
			pdf.Rect(x, y, pageW-2*proMargin, h, "F")
		}
		for ci, c := range cols {
			if st.boxedTable {
				drawRGB(pdf, ruleGray)
				pdf.Rect(x, y, c.w, h, "D")
			}
			pdf.SetXY(x+0.8, y+proRowPad)
			if ci == 1 {
				setFont(pdf, lo, st, "B", 8.5)
			} else {
				setFont(pdf, lo, st, "", 8.5)
			}
			pdf.MultiCell(c.w-1.6, proLineH, row[ci], "", c.align, false)
			x += c.w
		}
		if !st.boxedTable {
			drawRGB(pdf, ruleGray)
			pdf.Line(proMargin, y+h, pageW-proMargin, y+h)
		}
		resetDrawColor(pdf)
		pdf.SetXY(proMargin, y+h)
	}
	pdf.Ln(4)
}

func drawProTotals(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, lb Labels, pageW float64) {
	_, pageH := pdf.GetPageSize()
	type kv struct{ k, v string }
	var rows []kv
	add := func(k, v string) {
		if v != "" {
			rows = append(rows, kv{k, v})
		}
	}
	add(lb.TaxableAmount+":", data.SubtotalTaxable)
	if data.GenericTax {
		for _, t := range data.TaxTotals {
			name := t.Label
			if v, ok := lb.TaxNames[name]; ok {
				name = v
			}
			add(fmt.Sprintf("%s %s%%:", name, t.Rate), t.Amount)
		}
	} else {
		add("CGST:", data.TotalCGST)
		add("SGST:", data.TotalSGST)
		add("IGST:", data.TotalIGST)
		add("Cess:", data.TotalCess)
	}
	add(lb.RoundOff+":", data.RoundOff)
	if data.PreviousBalance != nil {
		add(lb.PrevBalance+":", data.PreviousBalance.StringFixed(0))
	}
	grandLabel := lb.GrandTotal + ":"
	if (data.GenericTax || data.BaseCurrency != "") && data.Currency != "" {
		grandLabel = lb.GrandTotal + " (" + data.Currency + "):"
	}

	need := float64(len(rows))*5.6 + 12 + 30
	if pdf.GetY()+need > pageH-proFootGap {
		pdf.AddPage()
		pdf.SetY(proMargin)
	}
	y0 := pdf.GetY()
	bw := 86.0
	bx := pageW - proMargin - bw

	// Payment block on the left, beside the totals.
	payEnd := drawProPayment(pdf, data, lo, st, lb, y0)

	pdf.SetXY(bx, y0)
	setFont(pdf, lo, st, "", 9)
	for _, r := range rows {
		pdf.SetX(bx)
		textRGB(pdf, mutedText)
		pdf.CellFormat(bw*0.55, 5.6, r.k, "", 0, "L", false, 0, "")
		resetTextColor(pdf)
		pdf.CellFormat(bw*0.45, 5.6, r.v, "", 1, "R", false, 0, "")
	}
	pdf.Ln(1)
	gy := pdf.GetY()
	setFont(pdf, lo, st, "B", 10.5)
	if st.boxedTable {
		setAccentDraw(pdf, st)
		pdf.SetLineWidth(0.5)
		pdf.Line(bx, gy, bx+bw, gy)
		pdf.Line(bx, gy+9, bx+bw, gy+9)
		pdf.SetLineWidth(st.ruleWidth)
		resetDrawColor(pdf)
		pdf.SetXY(bx, gy)
		pdf.CellFormat(bw*0.55, 9, grandLabel, "", 0, "L", false, 0, "")
		pdf.CellFormat(bw*0.45, 9, data.GrandTotal, "", 1, "R", false, 0, "")
	} else {
		fillRGB(pdf, st.accent)
		pdf.Rect(bx, gy, bw, 9, "F")
		pdf.SetTextColor(255, 255, 255)
		pdf.SetXY(bx+2, gy)
		pdf.CellFormat(bw*0.55-2, 9, grandLabel, "", 0, "L", false, 0, "")
		pdf.CellFormat(bw*0.45-2, 9, data.GrandTotal, "", 1, "R", false, 0, "")
		resetTextColor(pdf)
	}
	if data.BaseCurrency != "" && data.ExchangeRate != "" {
		setFont(pdf, lo, st, "", 8.5)
		textRGB(pdf, mutedText)
		pdf.SetX(bx)
		pdf.CellFormat(bw, 5, fmt.Sprintf("%s: 1 %s = %s %s", lb.ExchangeRate, data.Currency, data.ExchangeRate, data.BaseCurrency), "", 1, "R", false, 0, "")
		if data.GrandTotalBase != "" {
			pdf.SetX(bx)
			pdf.CellFormat(bw, 5, fmt.Sprintf("%s %s: %s", lb.EquivalentIn, data.BaseCurrency, data.GrandTotalBase), "", 1, "R", false, 0, "")
		}
		resetTextColor(pdf)
	}
	pdf.SetXY(proMargin, maxf(pdf.GetY(), payEnd)+4)

	w := pageW - 2*proMargin
	if len(data.TaxNotes) > 0 {
		setFont(pdf, lo, st, "", 8)
		for _, code := range data.TaxNotes {
			if text, ok := lb.Notes[code]; ok {
				pdf.SetX(proMargin)
				pdf.MultiCell(w, 4, text, "", "L", false)
			}
		}
	}
	if data.AmountInWords != "" {
		setFont(pdf, lo, st, "I", 8.5)
		textRGB(pdf, mutedText)
		pdf.SetX(proMargin)
		pdf.MultiCell(w, 4.2, lb.AmountInWords+": "+data.AmountInWords, "", "L", false)
		resetTextColor(pdf)
	}
	if data.Notes != "" {
		setFont(pdf, lo, st, "", 8.5)
		pdf.Ln(1)
		pdf.SetX(proMargin)
		pdf.MultiCell(w, 4.2, data.Notes, "", "L", false)
	}
	pdf.Ln(1)
}

// drawProPayment prints bank details and, for Indian invoices with a UPI ID, a
// scannable UPI payment QR for the amount due. Returns the y it ended at.
func drawProPayment(pdf *fpdf.Fpdf, data InvoiceData, lo layout, st style, lb Labels, y0 float64) float64 {
	if !lo.showBankBlock {
		return y0
	}
	hasBank := data.Seller.BankAccount != ""
	upi := !data.GenericTax && data.Seller.UPIID != ""
	if !hasBank && !upi {
		return y0
	}
	pdf.SetXY(proMargin, y0)
	setFont(pdf, lo, st, "B", 7.5)
	setAccentText(pdf, st)
	pdf.CellFormat(90, 4, strings.ToUpper(lb.BankDetails), "", 1, "L", false, 0, "")
	resetTextColor(pdf)
	setFont(pdf, lo, st, "", 8.5)
	if hasBank {
		var lines []string
		if data.GenericTax {
			lines = []string{data.Seller.BankName, lb.Bank + ": " + data.Seller.BankAccount}
			if data.Seller.BankIFSC != "" {
				lines = append(lines, lb.BIC+": "+data.Seller.BankIFSC)
			}
		} else {
			lines = []string{data.Seller.BankName, "A/c: " + data.Seller.BankAccount, "IFSC: " + data.Seller.BankIFSC}
		}
		for _, l := range lines {
			if strings.TrimSpace(strings.Trim(l, ":")) == "" || strings.HasSuffix(l, ": ") {
				continue
			}
			pdf.SetX(proMargin)
			pdf.CellFormat(60, proLineH, l, "", 1, "L", false, 0, "")
		}
	}
	end := pdf.GetY()
	if upi {
		pdf.SetX(proMargin)
		pdf.CellFormat(60, proLineH, "UPI: "+data.Seller.UPIID, "", 1, "L", false, 0, "")
		end = pdf.GetY()
		if uri := upiPayURI(data); uri != "" {
			if img, err := qrPNG(uri); err == nil {
				opts := fpdf.ImageOptions{ImageType: "PNG", ReadDpi: false}
				pdf.RegisterImageOptionsReader("upi-qr", opts, bytes.NewReader(img))
				const side = 27.0
				pdf.ImageOptions("upi-qr", proMargin+60, y0, side, side, false, opts, 0, "")
				setFont(pdf, lo, st, "", 7)
				textRGB(pdf, mutedText)
				pdf.SetXY(proMargin+60, y0+side)
				pdf.CellFormat(side, 3.5, "Scan to pay (UPI)", "", 1, "C", false, 0, "")
				resetTextColor(pdf)
				end = maxf(end, y0+side+3.5)
			}
		}
	}
	return end
}

// upiPayURI is the standard UPI deep link (NPCI linking spec); empty when the
// total is not a positive amount.
func upiPayURI(data InvoiceData) string {
	amt, err := strconv.ParseFloat(strings.ReplaceAll(data.GrandTotal, ",", ""), 64)
	if err != nil || amt <= 0 {
		return ""
	}
	esc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	return fmt.Sprintf("upi://pay?pa=%s&pn=%s&am=%.2f&cu=INR&tn=%s", esc(data.Seller.UPIID), esc(data.Seller.LegalName), amt, esc(data.DocumentNumber))
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
