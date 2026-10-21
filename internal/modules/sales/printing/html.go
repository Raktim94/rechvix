package printing

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"html/template"
	"strings"
)

//go:embed fonts/NotoSansDevanagari-Regular.ttf
var devanagariFont []byte

// NeedsBrowserPrint selects browser shaping for Devanagari instead of emitting
// missing glyphs or incorrectly ordered vowel/conjunct forms in the PDF library.
func NeedsBrowserPrint(data InvoiceData) bool {
	needed := data.Language == "hi"
	everyString(data, func(s string) {
		for _, r := range s {
			if r >= '\u0900' && r <= '\u097f' {
				needed = true
			}
		}
	})
	return needed
}

type htmlInvoice struct {
	Data                                          InvoiceData
	Labels                                        Labels
	Title, Date, Due, Supply, Language, PageWidth string
	Font, Logo                                    template.URL
	QRSVG                                         template.HTML
}

// RenderHTML uses escaped text and embedded assets only. The browser supplies
// complex-script shaping and Print/Save as PDF; this renderer never calculates money.
func RenderHTML(tpl Template, data InvoiceData) ([]byte, error) {
	lang := data.Language
	if _, ok := labelSets[lang]; !ok {
		lang = "en"
	}
	lb := LabelsFor(lang)
	layoutDate := data.DateLayout
	if layoutDate == "" {
		layoutDate = "02-Jan-2006"
	}
	v := htmlInvoice{Data: data, Labels: lb, Title: lb.DocType(data.DocumentTypeLabel), Date: data.IssueDate.Format(layoutDate), Language: lang, PageWidth: "210mm"}
	if data.DueDate != nil {
		v.Due = data.DueDate.Format(layoutDate)
	}
	if data.SupplyDate != nil {
		v.Supply = data.SupplyDate.Format(layoutDate)
	}
	if tpl == TemplateThermal58mm {
		v.PageWidth = "58mm"
	}
	if tpl == TemplateThermal80mm {
		v.PageWidth = "80mm"
	}
	// URLs are built solely from embedded font bytes and stored PNG bytes, never input URLs.
	v.Font = template.URL("data:font/ttf;base64," + base64.StdEncoding.EncodeToString(devanagariFont))
	if len(data.Seller.LogoPNG) > 0 {
		v.Logo = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Seller.LogoPNG))
	}
	if data.Verifactu != nil {
		svg, err := qrSVG(data.Verifactu.URL)
		if err != nil {
			return nil, err
		}
		v.QRSVG = svg
	}
	var b bytes.Buffer
	err := htmlInvoiceTemplate.Execute(&b, v)
	return b.Bytes(), err
}

var htmlInvoiceTemplate = template.Must(template.New("invoice").Funcs(template.FuncMap{
	"join": strings.Join,
}).Parse(`<!doctype html>
<html lang="{{.Language}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}} {{.Data.DocumentNumber}}</title>
<style>
@font-face{font-family:InvoiceHindi;src:url('{{.Font}}') format('truetype');font-weight:100 900;font-display:block}
*{box-sizing:border-box}body{margin:0;color:#16252e;background:#edf1f3;font:14px/1.6 Arial,InvoiceHindi,sans-serif}.paper{max-width:{{.PageWidth}};margin:24px auto;padding:24px;background:white}h1{font-size:22px}h2{font-size:16px;margin-bottom:4px}.help{text-align:center;padding:12px}.aeatqr{text-align:center;margin:0 0 12px}.aeatqr svg{display:block;margin:4px auto}.qrhead,.qrlegend{margin:2px 0;font-size:14px;white-space:normal}.parties{display:flex;gap:24px;flex-wrap:wrap}.parties>section{flex:1;min-width:180px}p{margin:4px 0;white-space:pre-wrap;overflow-wrap:anywhere}.logo{max-height:64px;max-width:140px}table{width:100%;border-collapse:collapse;margin:20px 0;table-layout:fixed}th,td{padding:6px;border-bottom:1px solid #cad4d9;text-align:right;vertical-align:top;overflow-wrap:anywhere}th:nth-child(2),td:nth-child(2){text-align:left;width:30%}thead{display:table-header-group}tr{break-inside:avoid}.totals{text-align:right;break-inside:avoid}.grand{font-size:18px;font-weight:bold}.notes{margin-top:20px}footer{margin-top:32px;break-inside:avoid}@page{size:A4;margin:12mm}@media print{body{background:white}.paper{margin:0;padding:0;max-width:none}.help{display:none}thead{display:table-header-group}}@media(max-width:600px){.paper{margin:0;padding:12px}table{font-size:11px}th,td{padding:3px}}
</style></head><body><p class="help">Use your browser’s Print / Save as PDF command. हिन्दी के लिए ब्राउज़र से प्रिंट करें।</p><main class="paper">
{{if .QRSVG}}<section class="aeatqr"><p class="qrhead">{{.Data.Verifactu.Heading}}</p>{{.QRSVG}}<p class="qrlegend">{{.Data.Verifactu.Legend}}</p><p class="qrlegend"><strong>{{.Data.Verifactu.Short}}</strong></p></section>{{end}}
<header>{{if .Logo}}<img class="logo" src="{{.Logo}}" alt="">{{end}}<h1>{{.Data.Seller.LegalName}}</h1>{{range .Data.Seller.AddressLines}}<p>{{.}}</p>{{end}}
{{if .Data.Seller.GSTIN}}<p>GSTIN: {{.Data.Seller.GSTIN}}</p>{{end}}{{if .Data.Seller.TaxID}}<p>{{.Labels.TaxID}}: {{.Data.Seller.TaxID}}</p>{{end}}
<p>{{.Data.Seller.Phone}} {{.Data.Seller.Email}} {{.Data.Seller.Website}}</p><h2>{{.Title}}</h2><p>{{.Labels.No}}: {{.Data.DocumentNumber}} · {{.Labels.Date}}: {{.Date}}</p>{{if .Due}}<p>Due / देय: {{.Due}}</p>{{end}}{{if .Supply}}<p>{{.Labels.SupplyDate}}: {{.Supply}}</p>{{end}}{{if .Data.PlaceOfSupply}}<p>Place of supply / आपूर्ति स्थान: {{.Data.PlaceOfSupply}}</p>{{end}}{{if .Data.IRN}}<p>IRN: {{.Data.IRN}}</p>{{end}}{{if .Data.EWBNumber}}<p>e-Way Bill: {{.Data.EWBNumber}}{{if .Data.EWBValidUntil}} · {{.Data.EWBValidUntil}}{{end}}</p>{{end}}</header>
<div class="parties"><section><h2>{{.Labels.BillTo}}</h2><p>{{.Data.BillTo.Name}}</p>{{range .Data.BillTo.AddressLines}}<p>{{.}}</p>{{end}}{{if .Data.BillTo.GSTIN}}<p>GSTIN: {{.Data.BillTo.GSTIN}}</p>{{end}}{{if .Data.BillTo.TaxID}}<p>{{.Labels.TaxID}}: {{.Data.BillTo.TaxID}}</p>{{end}}</section>{{if .Data.ShipTo.AddressLines}}<section><h2>{{.Labels.ShipTo}}</h2><p>{{.Data.ShipTo.Name}}</p>{{range .Data.ShipTo.AddressLines}}<p>{{.}}</p>{{end}}</section>{{end}}</div>
<table><thead><tr><th>#</th><th>{{.Labels.Description}}</th><th>{{.Labels.Qty}}</th><th>{{.Labels.Rate}}</th><th>{{.Labels.Taxable}}</th><th>{{.Labels.Tax}}</th><th>{{.Labels.Total}}</th></tr></thead><tbody>
{{range .Data.Lines}}<tr><td>{{.SNo}}</td><td>{{.Description}}<br>{{.HSNSAC}}</td><td>{{.Quantity}} {{.UnitOfMeasure}}</td><td>{{.Rate}}</td><td>{{.TaxableValue}}</td><td>{{if $.Data.GenericTax}}{{.TaxRate}} {{.TaxValue}}{{else}}{{if .CGSTValue}}CGST {{.CGSTRate}}%: {{.CGSTValue}}<br>{{end}}{{if .SGSTValue}}SGST {{.SGSTRate}}%: {{.SGSTValue}}<br>{{end}}{{if .IGSTValue}}IGST {{.IGSTRate}}%: {{.IGSTValue}}{{end}}{{end}}</td><td>{{.LineTotal}}</td></tr>{{end}}</tbody></table>
<section class="totals"><p>{{.Labels.TaxableAmount}}: {{.Data.SubtotalTaxable}}</p>{{if .Data.GenericTax}}{{range .Data.TaxTotals}}<p>{{with index $.Labels.TaxNames .Label}}{{.}}{{else}}{{.Label}}{{end}} {{.Rate}}%: {{.Amount}}</p>{{end}}{{else}}{{if .Data.TotalCGST}}<p>CGST: {{.Data.TotalCGST}}</p>{{end}}{{if .Data.TotalSGST}}<p>SGST: {{.Data.TotalSGST}}</p>{{end}}{{if .Data.TotalIGST}}<p>IGST: {{.Data.TotalIGST}}</p>{{end}}{{if .Data.TotalCess}}<p>Cess: {{.Data.TotalCess}}</p>{{end}}{{end}}{{if .Data.RoundOff}}<p>{{.Labels.RoundOff}}: {{.Data.RoundOff}}</p>{{end}}<p class="grand">{{.Labels.GrandTotal}} ({{.Data.Currency}}): {{.Data.GrandTotal}}</p>{{if .Data.BaseCurrency}}<p>{{.Labels.ExchangeRate}}: 1 {{.Data.Currency}} = {{.Data.ExchangeRate}} {{.Data.BaseCurrency}}</p><p>{{.Labels.EquivalentIn}} {{.Data.BaseCurrency}}: {{.Data.GrandTotalBase}}</p>{{end}}</section>
<section class="notes">{{range .Data.TaxNotes}}<p>{{index $.Labels.Notes .}}</p>{{end}}{{if .Data.Notes}}<p>{{.Data.Notes}}</p>{{end}}{{if .Data.TermsAndConditions}}<h2>{{.Labels.Terms}}</h2><p>{{.Data.TermsAndConditions}}</p>{{end}}{{if .Data.Seller.BankAccount}}<h2>{{.Labels.BankDetails}}</h2><p>{{.Data.Seller.BankName}} · {{.Data.Seller.BankAccount}} · {{.Data.Seller.BankIFSC}}</p>{{end}}{{if .Data.Seller.UPIID}}<p>UPI: {{.Data.Seller.UPIID}}</p>{{end}}</section><footer>{{.Labels.Signature}}<p>{{.Data.AuthorizedSignatoryName}}</p></footer></main></body></html>`))

// HTMLContentSecurityPolicy permits only this generated stylesheet and embedded assets.
func HTMLContentSecurityPolicy(body []byte) string {
	start := bytes.Index(body, []byte("<style>")) + len("<style>")
	end := bytes.Index(body, []byte("</style>"))
	if start < len("<style>") || end < start {
		return "default-src 'none'; frame-ancestors 'none'"
	}
	sum := sha256.Sum256(body[start:end])
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; font-src data:; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}
