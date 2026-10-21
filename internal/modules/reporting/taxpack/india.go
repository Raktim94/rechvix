package taxpack

import (
	_ "embed"
	"encoding/json"
	"github.com/shopspring/decimal"
	"rechvix/internal/modules/gstindia"
	"rechvix/internal/modules/reporting/domain"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Headers copied from the official GST utility archive downloaded 2026-10-21.
// Source/archive SHA256 are recorded in docs/accountant-exports.md.
//
//go:embed testdata/gst_headers.json
var gstHeaderJSON []byte
var gstHeaders = func() map[string]string {
	var m map[string]string
	if err := json.Unmarshal(gstHeaderJSON, &m); err != nil {
		panic(err)
	}
	return m
}()
var invoiceNumber = regexp.MustCompile(`^[A-Za-z0-9/-]{1,16}$`)
var hsnCode = regexp.MustCompile(`^(?:[0-9]{4}|[0-9]{6}|[0-9]{8})$`)

func gstTable(name, key string) Table { return Table{name, strings.Split(gstHeaders[key], ","), nil} }
func dateForGST(s string) string {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return d.Format("02-Jan-2006")
}
func component(l domain.AccountantLine, name string) decimal.Decimal {
	var v decimal.Decimal
	for _, c := range l.Components {
		if c.Name == name {
			v = v.Add(number(c.Amount))
		}
	}
	return v
}
func gstRate(l domain.AccountantLine) decimal.Decimal {
	var v decimal.Decimal
	for _, c := range l.Components {
		if c.Name != "CESS" {
			v = v.Add(number(c.Rate))
		}
	}
	return v
}

func (p *Package) india(docs map[string]domain.AccountantDocument) {
	p.Sources = []string{"https://tutorial.gst.gov.in/offlineutilities/returns/gst_offline_tool.zip", "https://tutorial.gst.gov.in/downloads/invoiceuploadofflineutility.pdf", "https://tutorial.gst.gov.in/userguide/returns/GSTR3B.htm"}
	if _, err := gstindia.StateCodeFromGSTIN(p.Company.TaxID); err != nil {
		p.check("ERROR", "SELLER_GSTIN", "", "Company GSTIN is missing or malformed.")
	}
	first, _ := time.Parse("2006-01-02", p.From)
	last, _ := time.Parse("2006-01-02", p.To)
	monthly := first.Day() == 1 && last.Equal(first.AddDate(0, 1, 0).AddDate(0, 0, -1))
	quarterly := first.Day() == 1 && (int(first.Month())-1)%3 == 0 && last.Equal(first.AddDate(0, 3, 0).AddDate(0, 0, -1))
	if !monthly && !quarterly {
		p.check("ERROR", "GST_PERIOD", "", "GST utility exports require one complete calendar month or quarter. Source registers are still available.")
	}
	b2b := gstTable("gstr1/b2b.csv", "b2b,sez,de.csv")
	b2cl := gstTable("gstr1/b2cl.csv", "b2cl.csv")
	b2cs := gstTable("gstr1/b2cs.csv", "b2cs.csv")
	cdnr := gstTable("gstr1/cdnr.csv", "cdnr.csv")
	cdnur := gstTable("gstr1/cdnur.csv", "cdnur.csv")
	hsnB := gstTable("gstr1/hsn-b2b.csv", "hsn(b2b).csv")
	hsnC := gstTable("gstr1/hsn-b2c.csv", "hsn(b2c).csv")
	type agg struct{ base, cess decimal.Decimal }
	small := map[string]agg{}
	type hsna struct {
		row                                      domain.AccountantLine
		qty, base, total, igst, cgst, sgst, cess decimal.Decimal
		registered                               bool
		uqc                                      string
	}
	hsns := map[string]hsna{}
	totals := map[string]decimal.Decimal{}
	for _, d := range p.Data.Documents {
		if d.Status != "FINALIZED" {
			continue
		}
		if d.Direction == "OUTPUT" && !invoiceNumber.MatchString(d.Number) {
			p.check("ERROR", "GST_INVOICE_NUMBER", d.Number, "GST utility invoice/note numbers must be 1–16 alphanumeric, slash or hyphen characters. Issued documents are not renamed by an export.")
		}
		if d.Currency != "INR" {
			p.check("ERROR", "GST_FOREIGN_CURRENCY", d.Number, "GST utility figures require reviewed INR tax values, not an assumed conversion from a foreign-currency tax snapshot.")
		}
	}
	for _, l := range p.Data.Lines {
		d := docs[l.DocumentID]
		if d.Status != "FINALIZED" {
			continue
		}
		sign := Sign(d)
		rate := gstRate(l)
		base := number(l.Taxable)
		cess := component(l, "CESS")
		if d.Direction == "INPUT" {
			for _, c := range l.Components {
				totals["ITC_CANDIDATE_"+c.Name] = totals["ITC_CANDIDATE_"+c.Name].Add(number(c.Amount).Mul(sign))
			}
			continue
		}
		section := "3.1(a)"
		if l.SupplyType == "EXPORT" || l.SupplyType == "SEZ" {
			section = "3.1(b)"
		} else if l.Classification == "EXEMPT" || l.Classification == "NIL_RATED" {
			section = "3.1(c)"
		}
		totals[section+"_BASE"] = totals[section+"_BASE"].Add(base.Mul(sign))
		for _, c := range l.Components {
			totals[section+"_"+c.Name] = totals[section+"_"+c.Name].Add(number(c.Amount).Mul(sign))
		}
		if l.SupplyType == "EXPORT" || l.SupplyType == "SEZ" || l.ReverseCharge {
			p.check("ERROR", "GST_SPECIAL_SUPPLY", d.Number, "Export/SEZ/reverse-charge utility mapping requires additional evidence and fields. Included in source schedules; import files withheld until resolved.")
		}
		if l.State == "" || l.StateName == "" {
			p.check("ERROR", "GST_POS", d.Number, "Place of supply is missing or unrecognised.")
		}
		registered := d.PartyTaxID != ""
		if registered {
			if _, err := gstindia.StateCodeFromGSTIN(d.PartyTaxID); err != nil {
				p.check("ERROR", "RECIPIENT_GSTIN", d.Number, "Recipient GSTIN is malformed.")
			}
		}
		if !hsnCode.MatchString(l.HSN) {
			p.check("ERROR", "GST_HSN", d.Number, "HSN/SAC must contain 4, 6 or 8 digits; CA must confirm turnover-specific digit requirements.")
		}
		uqc := map[string]string{"PCS": "NOS-NUMBERS", "NOS": "NOS-NUMBERS", "KGS": "KGS-KILOGRAMS", "KG": "KGS-KILOGRAMS"}[l.Unit]
		if uqc == "" {
			p.check("ERROR", "GST_UQC", d.Number, "Unit "+l.Unit+" needs an explicit official UQC mapping; it is not guessed.")
		}
		hk := l.HSN + "/" + uqc + "/" + rate.String() + "/" + map[bool]string{true: "B2B", false: "B2C"}[registered]
		h := hsns[hk]
		h.row = l
		h.registered = registered
		h.uqc = uqc
		h.qty = h.qty.Add(number(l.Quantity).Mul(sign))
		h.base = h.base.Add(base.Mul(sign))
		h.total = h.total.Add(number(l.Total).Mul(sign))
		h.igst = h.igst.Add(component(l, "IGST").Mul(sign))
		h.cgst = h.cgst.Add(component(l, "CGST").Mul(sign))
		h.sgst = h.sgst.Add(component(l, "SGST").Add(component(l, "UTGST")).Mul(sign))
		h.cess = h.cess.Add(cess.Mul(sign))
		hsns[hk] = h
		if l.Classification != "TAXABLE" {
			p.check("REVIEW", "GST_NIL_EXEMPT", d.Number, "Nil/exempt/zero-rate supplies are listed in the GSTR-3B and source workpapers. CA must complete GSTR-1 nil/exempt/non-GST disclosures; they are not put into ordinary taxable import rows.")
			continue
		}
		pos := l.State + "-" + l.StateName
		date := dateForGST(d.Date)
		note := d.Type == "CREDIT_NOTE" || d.Type == "DEBIT_NOTE" || d.Type == "SALES_RETURN"
		if note {
			kind := "D"
			if sign.IsNegative() {
				kind = "C"
			}
			if registered {
				cdnr.Rows = append(cdnr.Rows, []string{d.PartyTaxID, d.Party, d.Number, date, kind, pos, "N", "Regular B2B", d.Total, "", rate.String(), base.StringFixed(2), cess.StringFixed(2)})
			} else {
				// Unregistered small-supply notes net B2CS; large-note classification
				// needs the original invoice value, which this schema does not snapshot.
				p.check("ERROR", "GST_UNREGISTERED_NOTE", d.Number, "CA must classify this note against its original B2CL/B2CS invoice. No import rows are silently guessed.")
			}
			continue
		}
		if registered {
			b2b.Rows = append(b2b.Rows, []string{d.PartyTaxID, d.Party, d.Number, date, d.Total, pos, "N", "", "Regular B2B", "", rate.String(), base.StringFixed(2), cess.StringFixed(2)})
		} else if l.State != p.Company.State && number(d.Total).GreaterThan(decimal.NewFromInt(100000)) {
			b2cl.Rows = append(b2cl.Rows, []string{d.Number, date, d.Total, pos, "", rate.String(), base.StringFixed(2), cess.StringFixed(2), ""})
		} else {
			k := pos + "/" + rate.String()
			a := small[k]
			a.base = a.base.Add(base)
			a.cess = a.cess.Add(cess)
			small[k] = a
		}
	}
	keys := []string{}
	for k := range small {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.Split(k, "/")
		a := small[k]
		b2cs.Rows = append(b2cs.Rows, []string{"OE", parts[0], parts[1], "", a.base.StringFixed(2), a.cess.StringFixed(2), ""})
	}
	keys = nil
	for k := range hsns {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := hsns[k]
		row := []string{h.row.HSN, h.row.Description, h.uqc, h.qty.String(), h.total.StringFixed(2), h.base.StringFixed(2), h.igst.StringFixed(2), h.cgst.StringFixed(2), h.sgst.StringFixed(2), h.cess.StringFixed(2), gstRate(h.row).String()}
		if h.registered {
			hsnB.Rows = append(hsnB.Rows, row)
		} else {
			hsnC.Rows = append(hsnC.Rows, row)
		}
	}
	for _, section := range []string{"3.1(a)", "3.1(b)", "3.1(c)"} {
		for _, tax := range []string{"BASE", "IGST", "CGST", "SGST", "UTGST", "CESS"} {
			p.box(section+"_"+tax, section+" "+tax, totals[section+"_"+tax], "Recorded sales, net of credits. Confirm return classification and external adjustments.")
		}
	}
	for _, tax := range []string{"IGST", "CGST", "SGST", "UTGST", "CESS"} {
		p.box("ITC_CANDIDATE_"+tax, "Recorded purchase tax candidate "+tax, totals["ITC_CANDIDATE_"+tax], "Not a claim of eligibility. Reconcile GSTR-2B and blocked/reversed ITC.")
		p.unknown("4(A)(5)_"+tax, "Eligible other ITC "+tax, "CA enters eligible ITC after reconciliation, including explicit zero when applicable.")
	}
	for _, box := range []string{"3.1(d)", "3.1(e)", "3.2", "4(A)(1-4)", "4(B)", "4(D)", "5", "5.1", "6.1"} {
		p.unknown(box, "GSTR-3B "+box, "CA must complete reverse charges, imports, reversals, ineligible ITC, interest, payment and outside-system items as applicable.")
	}
	p.check("REVIEW", "GST_COMPLETENESS", "", "GSTR-1 CSVs cover modelled ordinary supplies only. CA must check advances, amendments, e-commerce/TCS, nil/exempt disclosures, document series and active GSTINs in the current utility. GSTR-3B workpaper is not a portal JSON file.")
	collapseGST(&b2b, []int{0, 2, 3, 5, 10}, []int{11, 12})
	collapseGST(&b2cl, []int{0, 1, 3, 5}, []int{6, 7})
	collapseGST(&cdnr, []int{0, 2, 3, 4, 5, 10}, []int{11, 12})
	p.Tables = append(p.Tables, b2b, b2cl, b2cs, cdnr, cdnur, hsnB, hsnC)
	// Import files are emitted atomically only when no known blocking issue exists.
	// Source schedules and workpapers remain downloadable with every exception.
}

// GST import records are unique per document/rate, while the source register
// keeps every original line. Repeated equal-rate lines must sum, not overwrite.
func collapseGST(t *Table, keys, sums []int) {
	positions := map[string]int{}
	rows := [][]string{}
	for _, row := range t.Rows {
		keyParts := []string{}
		for _, col := range keys {
			keyParts = append(keyParts, row[col])
		}
		raw, _ := json.Marshal(keyParts)
		key := string(raw)
		if at, ok := positions[key]; ok {
			for _, col := range sums {
				rows[at][col] = number(rows[at][col]).Add(number(row[col])).StringFixed(2)
			}
		} else {
			positions[key] = len(rows)
			rows = append(rows, append([]string{}, row...))
		}
	}
	t.Rows = rows
}
