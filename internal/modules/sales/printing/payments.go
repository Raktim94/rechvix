package printing

import "strings"

// PaymentLine is one receipt against the invoice, already formatted for printing.
type PaymentLine struct {
	Date, Method, Reference, Amount string
}

// payWords are the words of the payment summary. Anything missing in a language falls back to English.
type payWords struct {
	Payments, Paid, BalanceDue, InvoiceTotal, Received, Unpaid, PartPaid, FullyPaid string
	Methods                                                                         map[string]string
}

var payWordSets = map[string]payWords{
	"en": {Payments: "Payments received", Paid: "Total paid", BalanceDue: "Balance due", InvoiceTotal: "Invoice total", Received: "Received", Unpaid: "UNPAID", PartPaid: "PARTIALLY PAID", FullyPaid: "PAID",
		Methods: map[string]string{"CASH": "Cash", "UPI": "UPI", "CARD": "Card", "BANK_TRANSFER": "Bank transfer", "CHEQUE": "Cheque", "OTHER": "Other"}},
	"hi": {Payments: "प्राप्त भुगतान", Paid: "कुल भुगतान", BalanceDue: "शेष राशि", InvoiceTotal: "इनवॉइस कुल", Unpaid: "अदत्त", PartPaid: "आंशिक भुगतान", FullyPaid: "भुगतान हो गया"},
	"es": {Payments: "Pagos recibidos", Paid: "Total pagado", BalanceDue: "Pendiente", InvoiceTotal: "Total factura", Unpaid: "SIN PAGAR", PartPaid: "PAGO PARCIAL", FullyPaid: "PAGADA"},
	"fr": {Payments: "Paiements reçus", Paid: "Total payé", BalanceDue: "Reste à payer", InvoiceTotal: "Total facture", Unpaid: "IMPAYÉE", PartPaid: "PARTIELLEMENT PAYÉE", FullyPaid: "PAYÉE"},
	"de": {Payments: "Erhaltene Zahlungen", Paid: "Gesamt bezahlt", BalanceDue: "Offener Betrag", InvoiceTotal: "Rechnungsbetrag", Unpaid: "UNBEZAHLT", PartPaid: "TEILWEISE BEZAHLT", FullyPaid: "BEZAHLT"},
	"it": {Payments: "Pagamenti ricevuti", Paid: "Totale pagato", BalanceDue: "Residuo", InvoiceTotal: "Totale fattura", Unpaid: "NON PAGATA", PartPaid: "PAGATA IN PARTE", FullyPaid: "PAGATA"},
	"pt": {Payments: "Pagamentos recebidos", Paid: "Total pago", BalanceDue: "Em aberto", InvoiceTotal: "Total da fatura", Unpaid: "NÃO PAGA", PartPaid: "PAGA EM PARTE", FullyPaid: "PAGA"},
	"nl": {Payments: "Ontvangen betalingen", Paid: "Totaal betaald", BalanceDue: "Openstaand", InvoiceTotal: "Factuurtotaal", Unpaid: "ONBETAALD", PartPaid: "GEDEELTELIJK BETAALD", FullyPaid: "BETAALD"},
}

func payWordsFor(lang string) payWords {
	en := payWordSets["en"]
	l, ok := payWordSets[strings.ToLower(lang)]
	if !ok {
		return en
	}
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	out := payWords{Payments: pick(l.Payments, en.Payments), Paid: pick(l.Paid, en.Paid), BalanceDue: pick(l.BalanceDue, en.BalanceDue),
		InvoiceTotal: pick(l.InvoiceTotal, en.InvoiceTotal), Received: pick(l.Received, en.Received), Unpaid: pick(l.Unpaid, en.Unpaid),
		PartPaid: pick(l.PartPaid, en.PartPaid), FullyPaid: pick(l.FullyPaid, en.FullyPaid), Methods: en.Methods}
	if l.Methods != nil {
		out.Methods = l.Methods
	}
	return out
}

func (w payWords) method(code string) string {
	if v, ok := w.Methods[code]; ok {
		return v
	}
	if v, ok := payWordSets["en"].Methods[code]; ok {
		return v
	}
	return code
}

func (w payWords) status(code string) string {
	switch code {
	case "PAID":
		return w.FullyPaid
	case "PARTIAL":
		return w.PartPaid
	}
	return w.Unpaid
}

// payRows are the label/value rows printed under the grand total: what was paid, what is still due, and the status.
// The invoice total itself is never replaced — it stays the grand total line above these rows.
func (d InvoiceData) payRows(w payWords) [][2]string {
	if !d.ShowPayments {
		return nil
	}
	rows := [][2]string{{w.Paid + ":", d.AmountPaid}, {w.BalanceDue + ":", d.BalanceDue}}
	return rows
}

// payHistory is the printable one-line-per-receipt list.
func (d InvoiceData) payHistory(w payWords) []string {
	var out []string
	for _, p := range d.Payments {
		line := p.Date + "  " + w.method(p.Method)
		if p.Reference != "" {
			line += " (" + p.Reference + ")"
		}
		out = append(out, line+"  "+p.Amount)
	}
	return out
}
