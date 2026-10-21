// Package taxpack generates offline accountant handoffs, never submissions.
package taxpack

import (
	"fmt"
	"github.com/shopspring/decimal"
	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/platform/money"
	"sort"
	"strings"
	"time"
)

const Version = "2026-10-21.1"

type Check struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Document string `json:"document,omitempty"`
	Message  string `json:"message"`
}
type Box struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Amount string `json:"amount"`
	Status string `json:"status"`
	Note   string `json:"note"`
}
type Table struct {
	Name    string
	Headers []string
	Rows    [][]string
}
type Package struct {
	Version       string                   `json:"version"`
	Company       domain.AccountantCompany `json:"company"`
	From          string                   `json:"from"`
	To            string                   `json:"to"`
	SnapshotAt    time.Time                `json:"snapshot_at"`
	Status        string                   `json:"status"`
	DocumentCount int                      `json:"document_count"`
	Checks        []Check                  `json:"checks"`
	Boxes         []Box                    `json:"boxes"`
	Sources       []string                 `json:"sources"`
	Tables        []Table                  `json:"-"`
	Data          domain.AccountantData    `json:"-"`
}

func signed(s string, factor decimal.Decimal) string {
	if s == "" {
		return ""
	}
	return number(s).Mul(factor).String()
}
func number(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, _ := decimal.NewFromString(s)
	return d
}
func Sign(d domain.AccountantDocument) decimal.Decimal {
	if (d.Direction == "OUTPUT" && (d.Type == "CREDIT_NOTE" || d.Type == "SALES_RETURN")) || (d.Direction == "INPUT" && (d.Type == "PURCHASE_RETURN" || d.Type == "DEBIT_NOTE")) {
		return decimal.NewFromInt(-1)
	}
	return decimal.NewFromInt(1)
}
func (p *Package) check(severity, code, doc, message string) {
	p.Checks = append(p.Checks, Check{severity, code, doc, message})
}
func (p *Package) box(code, label string, amount decimal.Decimal, note string) {
	p.Boxes = append(p.Boxes, Box{code, label, amount.Round(2).StringFixed(2), "CALCULATED_REVIEW", note})
}
func (p *Package) unknown(code, label, note string) {
	p.Boxes = append(p.Boxes, Box{code, label, "", "CA_INPUT_REQUIRED", note})
}

// Build rejects malformed numeric data; unsupported facts are explicit checks,
// never silently converted into final zero-valued return fields.
func Build(data domain.AccountantData, from, to time.Time) (*Package, error) {
	if len(data.Documents) > 20000 || len(data.Lines) > 100000 {
		return nil, fmt.Errorf("export exceeds 20,000 documents or 100,000 tax lines; select a shorter period")
	}
	for _, d := range data.Documents {
		for _, v := range []string{d.Rate, d.Taxable, d.Tax, d.Total, d.Debit, d.Credit, d.LedgerTax} {
			if v != "" {
				if _, err := decimal.NewFromString(v); err != nil {
					return nil, fmt.Errorf("invalid decimal in document %s", d.ID)
				}
			}
		}
	}
	for _, l := range data.Lines {
		for _, v := range []string{l.Quantity, l.Taxable, l.Tax, l.Total} {
			if _, err := decimal.NewFromString(v); err != nil {
				return nil, fmt.Errorf("invalid decimal in tax line %s/%s", l.DocumentID, l.Ref)
			}
		}
		for _, c := range l.Components {
			if _, err := decimal.NewFromString(c.Amount); err != nil {
				return nil, err
			}
			if _, err := decimal.NewFromString(c.Rate); err != nil {
				return nil, err
			}
		}
	}
	p := &Package{Version: Version, Company: data.Company, From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), SnapshotAt: data.SnapshotAt, Status: "CA_REVIEW_REQUIRED", Checks: []Check{}, Boxes: []Box{}, Data: data}
	expected := map[string]string{"IN": "INR", "ES": "EUR", "GB": "GBP", "US": "USD"}[data.Company.Country]
	if expected == "" {
		return nil, fmt.Errorf("accountant filing worksheets currently support India, Spain, UK and US only")
	}
	if data.Company.Currency != expected {
		p.check("ERROR", "BOOK_CURRENCY", "", "Statutory worksheets require "+expected+"; book currency is "+data.Company.Currency+". CA must translate using the required tax exchange rates.")
	}
	if strings.TrimSpace(data.Company.TaxID) == "" {
		p.check("ERROR", "TAX_ID_MISSING", "", "Add the company's tax registration identifier in Settings.")
	}
	p.check("REVIEW", "COMPLETENESS", "", "CA must confirm period, tax points, all external transactions, adjustments, eligibility and the applicable filing scheme. Current party names/identifiers are master data, not immutable historical identity snapshots.")
	docs := map[string]domain.AccountantDocument{}
	sums := map[string][3]decimal.Decimal{}
	for _, l := range data.Lines {
		var componentTax decimal.Decimal
		for _, c := range l.Components {
			componentTax = componentTax.Add(number(c.Amount))
		}
		if !componentTax.Equal(number(l.Tax)) {
			p.check("ERROR", "COMPONENT_MISMATCH", l.DocumentID, "Tax components do not reconcile with line tax; CA must correct the snapshot.")
		}
		a := sums[l.DocumentID]
		a[0] = a[0].Add(number(l.Taxable))
		a[1] = a[1].Add(number(l.Tax))
		a[2] = a[2].Add(number(l.Total))
		sums[l.DocumentID] = a
	}
	register := Table{"Documents", []string{"Direction", "Document ID", "Type", "Number", "Date", "Supply date", "Status", "Party", "Tax ID", "Original reference", "Supplier invoice", "Supplier invoice date", "Address", "Currency", "Exchange rate", "Taxable (signed)", "Tax (signed)", "Total (signed)", "Base total (signed)", "Ledger tax", "Journal debit", "Journal credit"}, nil}
	seen := map[string]bool{}
	for _, d := range data.Documents {
		docs[d.ID] = d
		sign := Sign(d)
		rate := number(d.Rate)
		if d.Status != "FINALIZED" {
			p.check("REVIEW", "EXCLUDED_"+d.Status, d.Number, "Excluded from calculated return figures; included in document register for completeness.")
		} else {
			p.DocumentCount++
			if d.Direction == "INPUT" && (d.SupplierNumber == "" || d.SupplierDate == "") {
				p.check("REVIEW", "SUPPLIER_REFERENCE", d.Number, "Verify original supplier invoice number/date against the supporting invoice.")
			}
			if d.SupplierDate != "" && d.SupplierDate != d.Date {
				p.check("REVIEW", "PURCHASE_TAX_POINT", d.Number, "Supplier invoice date differs from recorded document date; CA must confirm the tax period.")
			}
			if data.Company.Country == "US" && d.Direction == "OUTPUT" && d.PartyAddress == "" {
				p.check("ERROR", "US_DESTINATION_ADDRESS", d.Number, "Destination address/ZIP is missing; local-jurisdiction allocation requires supporting records.")
			}

			if !rate.IsPositive() {
				p.check("ERROR", "EXCHANGE_RATE", d.Number, "Missing or invalid exchange rate.")
			}
			if d.TaxDocumentID == "" {
				p.check("ERROR", "NO_TAX_SNAPSHOT", d.Number, "No tax snapshot. CA must reconcile this source document; it is not silently treated as a tax-free purchase.")
			} else {
				a := sums[d.ID]
				if !a[0].Equal(number(d.Taxable)) || !a[1].Equal(number(d.Tax)) || !a[2].Equal(number(d.Total)) {
					p.check("ERROR", "SNAPSHOT_MISMATCH", d.Number, "Tax line totals differ from the document snapshot.")
				}
				digits, ok := money.MinorDigits(data.Company.Currency)
				if !ok {
					return nil, fmt.Errorf("unknown book currency")
				}
				wantTax := number(d.Tax).Mul(rate).Round(digits).Mul(sign)
				if !wantTax.Equal(number(d.LedgerTax).Round(digits)) {
					p.check("ERROR", "LEDGER_TAX_MISMATCH", d.Number, "Posted tax account does not reconcile with the signed tax snapshot at the stored rate.")
				}
			}
			if d.JournalCount == 0 || !number(d.Debit).Equal(number(d.Credit)) {
				p.check("ERROR", "JOURNAL_RECONCILIATION", d.Number, "Missing or unbalanced accounting posting.")
			}
			k := d.Direction + "/" + d.PartyTaxID + "/" + d.Number
			if seen[k] {
				p.check("ERROR", "DUPLICATE_NUMBER", d.Number, "Duplicate document number in the registration/party scope.")
			}
			seen[k] = true
			if (d.Type == "CREDIT_NOTE" || d.Type == "SALES_RETURN" || d.Type == "PURCHASE_RETURN" || d.Type == "DEBIT_NOTE") && d.Reference == "" {
				p.check("REVIEW", "ADJUSTMENT_REFERENCE", d.Number, "Verify the original invoice reference for this adjustment.")
			}
			if d.SupplyDate != "" && d.SupplyDate != d.Date {
				p.check("REVIEW", "TAX_POINT", d.Number, "Supply date differs from issue date. This export selects issue/document dates; CA must confirm the statutory tax period.")
			}
		}
		register.Rows = append(register.Rows, []string{d.Direction, d.ID, d.Type, d.Number, d.Date, d.SupplyDate, d.Status, d.Party, d.PartyTaxID, d.Reference, d.SupplierNumber, d.SupplierDate, d.PartyAddress, d.Currency, d.Rate, signed(d.Taxable, sign), signed(d.Tax, sign), number(d.Total).Mul(sign).String(), number(d.Total).Mul(rate).Mul(sign).String(), d.LedgerTax, d.Debit, d.Credit})
	}
	p.Tables = append(p.Tables, register)
	lines := Table{"Tax lines", []string{"Direction", "Document", "Line", "Classification", "HSN or category", "Description", "Quantity", "Unit", "Supply type", "Place of supply", "Region", "Currency", "FX rate", "Taxable (signed)", "Tax (signed)", "Gross (signed)", "Book taxable (signed)", "Book tax (signed)", "Tax regime", "Rate pack or rate ID"}, nil}
	comps := Table{"Tax components", []string{"Direction", "Document", "Line", "Tax", "Rate %", "Amount (signed)", "Currency", "Book amount (signed)"}, nil}
	for _, l := range data.Lines {
		d, ok := docs[l.DocumentID]
		if !ok {
			return nil, fmt.Errorf("orphan tax line")
		}
		if d.Status != "FINALIZED" {
			continue
		}
		s := Sign(d)
		r := number(d.Rate)
		lines.Rows = append(lines.Rows, []string{d.Direction, d.Number, l.Ref, l.Classification, l.HSN, l.Description, l.Quantity, l.Unit, l.SupplyType, l.State, l.Region, d.Currency, d.Rate, number(l.Taxable).Mul(s).String(), number(l.Tax).Mul(s).String(), number(l.Total).Mul(s).String(), number(l.Taxable).Mul(s).Mul(r).String(), number(l.Tax).Mul(s).Mul(r).String(), l.TaxRegime, l.RatePackVersion})
		for _, c := range l.Components {
			comps.Rows = append(comps.Rows, []string{d.Direction, d.Number, l.Ref, c.Name, c.Rate, number(c.Amount).Mul(s).String(), d.Currency, number(c.Amount).Mul(s).Mul(r).String()})
		}
	}
	p.Tables = append(p.Tables, lines, comps)
	if len(data.Lines) == 0 {
		p.check("REVIEW", "NO_TAX_LINES", "", "No finalized tax lines in this period. This is not confirmation of a nil return.")
	}
	switch data.Company.Country {
	case "GB":
		p.uk(docs)
	case "ES":
		p.spain(docs)
	case "IN":
		p.india(docs)
	case "US":
		p.us(docs)
	}
	for _, c := range p.Checks {
		if c.Severity == "ERROR" {
			p.Status = "BLOCKED_ITEMS_PRESENT"
			break
		}
	}
	sort.SliceStable(p.Checks, func(i, j int) bool { return p.Checks[i].Severity < p.Checks[j].Severity })
	return p, nil
}
