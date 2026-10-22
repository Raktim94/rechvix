package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/modules/sales/printing"
	httpx "rechvix/internal/platform/http"
)

// previewDocument returns the finalized document in the shape the printed
// invoice uses, as JSON, so the screen can show the same seller, customer,
// itemised tax and totals as the PDF. Like the PDF it renders numbers computed
// at finalization and never recalculates them. ?lang= picks the document language.
func (h *Handlers) previewDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	lang := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang")))
	if !orgapp.ValidInvoiceLanguage(lang) {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_INVOICE_LANGUAGE", "lang must be one of en, es, fr, de, it, pt, nl, hi."))
		return
	}
	data, err := h.svc.BuildInvoiceDataInLanguage(r.Context(), principal(r), id, lang)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, newPreview(data))
}

type previewParty struct {
	Name     string   `json:"name"`
	TaxLabel string   `json:"tax_label"`
	TaxID    string   `json:"tax_id"`
	Address  []string `json:"address"`
}

type previewLine struct {
	No          int    `json:"no"`
	Description string `json:"description"`
	Code        string `json:"code"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit"`
	Rate        string `json:"rate"`
	Taxable     string `json:"taxable"`
	TaxRate     string `json:"tax_rate"`
	Tax         string `json:"tax"`
	Total       string `json:"total"`
}

type previewTax struct {
	Label  string `json:"label"`
	Rate   string `json:"rate,omitempty"`
	Amount string `json:"amount"`
}

type previewBank struct {
	Name    string `json:"name"`
	Account string `json:"account"`
	Code    string `json:"code"`
	UPI     string `json:"upi"`
}

type preview struct {
	Title        string        `json:"title"`
	Number       string        `json:"number"`
	IssueDate    time.Time     `json:"issue_date"`
	DueDate      *time.Time    `json:"due_date"`
	Currency     string        `json:"currency"`
	GenericTax   bool          `json:"generic_tax"`
	Seller       previewParty  `json:"seller"`
	SellerPhone  string        `json:"seller_phone"`
	SellerEmail  string        `json:"seller_email"`
	SellerWeb    string        `json:"seller_website"`
	BillTo       previewParty  `json:"bill_to"`
	PlaceSupply  string        `json:"place_of_supply"`
	Reference    string        `json:"reference"`
	Lines        []previewLine `json:"lines"`
	Subtotal     string        `json:"subtotal"`
	Taxes        []previewTax  `json:"taxes"`
	GrandTotal   string        `json:"grand_total"`
	BaseCurrency string        `json:"base_currency,omitempty"`
	ExchangeRate string        `json:"exchange_rate,omitempty"`
	TotalBase    string        `json:"grand_total_base,omitempty"`
	Bank         previewBank   `json:"bank"`
	Notes        string        `json:"notes"`
	Terms        string        `json:"terms"`
	Signatory    string        `json:"signatory"`
	TaxNotes     []string      `json:"tax_notes"`
	SignedNote   string        `json:"signed_note,omitempty"`
}

func partyOf(name, label, id string, address []string) previewParty {
	if address == nil {
		address = []string{}
	}
	return previewParty{Name: name, TaxLabel: label, TaxID: id, Address: address}
}

func newPreview(d *printing.InvoiceData) preview {
	generic := d.GenericTax
	label, sellerID, billID := "GSTIN", d.Seller.GSTIN, d.BillTo.GSTIN
	if generic {
		label, sellerID, billID = d.Seller.TaxIDLabel, d.Seller.TaxID, d.BillTo.TaxID
	}
	p := preview{
		Title: d.DocumentTypeLabel, Number: d.DocumentNumber, IssueDate: d.IssueDate, DueDate: d.DueDate,
		Currency: d.Currency, GenericTax: generic,
		Seller:      partyOf(d.Seller.LegalName, label, sellerID, d.Seller.AddressLines),
		SellerPhone: d.Seller.Phone, SellerEmail: d.Seller.Email, SellerWeb: d.Seller.Website,
		BillTo:      partyOf(d.BillTo.Name, label, billID, d.BillTo.AddressLines),
		PlaceSupply: d.PlaceOfSupply, Reference: d.CustomerReference,
		Lines: []previewLine{}, Subtotal: d.SubtotalTaxable, Taxes: []previewTax{}, GrandTotal: d.GrandTotal,
		BaseCurrency: d.BaseCurrency, ExchangeRate: d.ExchangeRate, TotalBase: d.GrandTotalBase,
		Bank:  previewBank{Name: d.Seller.BankName, Account: d.Seller.BankAccount, Code: d.Seller.BankIFSC, UPI: d.Seller.UPIID},
		Notes: d.Notes, Terms: d.TermsAndConditions, Signatory: d.AuthorizedSignatoryName,
		TaxNotes: append([]string{}, d.TaxNotes...), SignedNote: d.SignedNote,
	}
	for _, l := range d.Lines {
		line := previewLine{No: l.SNo, Description: l.Description, Code: l.HSNSAC, Quantity: l.Quantity, Unit: l.UnitOfMeasure, Rate: l.Rate, Taxable: l.TaxableValue, Total: l.LineTotal}
		if generic {
			line.TaxRate, line.Tax = l.TaxRate, l.TaxValue
		} else {
			// India: one combined rate and amount per line, whichever of CGST+SGST or IGST applied.
			line.TaxRate, line.Tax = indiaLineTax(l)
		}
		p.Lines = append(p.Lines, line)
	}
	if generic {
		for _, t := range d.TaxTotals {
			p.Taxes = append(p.Taxes, previewTax{Label: t.Label, Rate: t.Rate, Amount: t.Amount})
		}
	} else {
		for _, t := range []previewTax{{Label: "CGST", Amount: d.TotalCGST}, {Label: "SGST", Amount: d.TotalSGST}, {Label: "IGST", Amount: d.TotalIGST}, {Label: "Cess", Amount: d.TotalCess}} {
			if t.Amount != "" && t.Amount != "0" && t.Amount != "0.00" {
				p.Taxes = append(p.Taxes, t)
			}
		}
	}
	return p
}

// indiaLineTax folds a line's CGST+SGST or IGST into one rate and amount.
func indiaLineTax(l printing.LineItem) (rate, amount string) {
	if l.IGSTRate != "" {
		return l.IGSTRate, l.IGSTValue
	}
	sum := func(a, b string) string {
		da, errA := decimal.NewFromString(a)
		db, errB := decimal.NewFromString(b)
		if errA != nil && errB != nil {
			return ""
		}
		return da.Add(db).String()
	}
	return sum(l.CGSTRate, l.SGSTRate), sum(l.CGSTValue, l.SGSTValue)
}
