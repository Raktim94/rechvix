package printing

import "strings"

// Labels are the words printed on an invoice outside India. India keeps the
// fixed English GST layout; every other country gets the labels of its own
// language (Spanish for Spain, and so on), falling back to English.
//
// Translations were drafted by an AI model and should be reviewed by a native
// speaker (and, for tax wording, by an accountant) before relying on them.
type Labels struct {
	DocTypes   map[string]string // keyed by the English heading, e.g. "CREDIT NOTE"
	Categories map[string]string // STANDARD, REDUCED, ZERO, EXEMPT, TAXABLE
	Notes      map[string]string // keyed by tax classification, see TaxNotes

	No, Date, SupplyDate, BillTo, ShipTo, TaxID               string
	Phone, Email, Web                                         string
	Description, TaxCategory, Qty, Rate, Taxable, TaxPct, Tax string
	Total, TaxableAmount, RoundOff, GrandTotal, PrevBalance   string
	AmountInWords, Terms, Signature, BankDetails, Bank, BIC   string
}

// LanguageFor is the invoice language for a legal entity's country.
func LanguageFor(country string) string {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "ES":
		return "es"
	case "FR":
		return "fr"
	case "DE", "AT":
		return "de"
	case "IT":
		return "it"
	case "PT":
		return "pt"
	case "NL":
		return "nl"
	}
	return "en"
}

// LabelsFor returns the labels for lang, with English filling any gap.
func LabelsFor(lang string) Labels {
	l, ok := labelSets[strings.ToLower(lang)]
	if !ok {
		return labelSets["en"]
	}
	en := labelSets["en"]
	merge := func(dst, src map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range src {
			out[k] = v
		}
		for k, v := range dst {
			out[k] = v
		}
		return out
	}
	l.DocTypes = merge(l.DocTypes, en.DocTypes)
	l.Categories = merge(l.Categories, en.Categories)
	l.Notes = merge(l.Notes, en.Notes)
	return l
}

// DocType localises an English document heading ("INVOICE", "CREDIT NOTE").
func (l Labels) DocType(english string) string {
	if v, ok := l.DocTypes[english]; ok {
		return v
	}
	return english
}

var labelSets = map[string]Labels{
	"en": {
		DocTypes:   map[string]string{"INVOICE": "INVOICE", "TAX INVOICE": "TAX INVOICE", "CREDIT NOTE": "CREDIT NOTE", "DEBIT NOTE": "DEBIT NOTE", "QUOTATION": "QUOTATION", "PROFORMA INVOICE": "PROFORMA INVOICE", "SALES ORDER": "SALES ORDER", "DELIVERY CHALLAN": "DELIVERY NOTE", "SALES RETURN": "SALES RETURN", "RECURRING INVOICE": "RECURRING INVOICE"},
		Categories: map[string]string{"STANDARD": "Standard", "REDUCED": "Reduced", "ZERO": "Zero rate", "EXEMPT": "Exempt", "TAXABLE": "Taxable"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Reverse charge: the customer accounts for the VAT.", "INTRA_EU_B2B": "Intra-Community supply: VAT not charged, reverse charge applies.",
			"EXPORT": "Export outside the supplier's VAT area: VAT not charged.", "EXEMPT": "Exempt supply.", "ZERO_RATED": "Zero-rated supply.",
			"EXEMPT_CERTIFICATE": "No sales tax charged: customer exemption certificate on file.", "NOT_COLLECTED": "No sales tax charged: not registered to collect in the destination state.",
			"STATE_HAS_NO_SALES_TAX": "No state sales tax applies in the destination state.",
		},
		No: "No", Date: "Date", SupplyDate: "Date of supply", BillTo: "Bill To", ShipTo: "Ship To", TaxID: "Tax ID",
		Phone: "Phone", Email: "Email", Web: "Web",
		Description: "Description", TaxCategory: "Tax category", Qty: "Qty", Rate: "Rate", Taxable: "Taxable", TaxPct: "Tax %", Tax: "Tax", Total: "Total",
		TaxableAmount: "Taxable Amount", RoundOff: "Round Off", GrandTotal: "Grand Total", PrevBalance: "Previous Balance",
		AmountInWords: "Amount in words", Terms: "Terms & Conditions", Signature: "Authorised signature", BankDetails: "Bank Details", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
	"es": {
		DocTypes:   map[string]string{"INVOICE": "FACTURA", "TAX INVOICE": "FACTURA", "CREDIT NOTE": "FACTURA RECTIFICATIVA (ABONO)", "DEBIT NOTE": "NOTA DE DÉBITO", "QUOTATION": "PRESUPUESTO", "PROFORMA INVOICE": "FACTURA PROFORMA", "SALES ORDER": "PEDIDO DE VENTA", "DELIVERY CHALLAN": "ALBARÁN", "SALES RETURN": "DEVOLUCIÓN DE VENTA", "RECURRING INVOICE": "FACTURA RECURRENTE"},
		Categories: map[string]string{"STANDARD": "General", "REDUCED": "Reducido", "ZERO": "Tipo cero", "EXEMPT": "Exento", "TAXABLE": "Sujeto"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Inversión del sujeto pasivo: el cliente liquida el IVA.", "INTRA_EU_B2B": "Entrega intracomunitaria: IVA no repercutido, con inversión del sujeto pasivo.",
			"EXPORT": "Exportación fuera del ámbito del IVA del proveedor: IVA no repercutido.", "EXEMPT": "Operación exenta.", "ZERO_RATED": "Operación a tipo cero.",
		},
		No: "Nº", Date: "Fecha", SupplyDate: "Fecha de la operación", BillTo: "Cliente", ShipTo: "Enviar a", TaxID: "NIF / IVA",
		Phone: "Tel.", Email: "Correo", Web: "Web",
		Description: "Descripción", TaxCategory: "Categoría", Qty: "Cant.", Rate: "Precio", Taxable: "Base imp.", TaxPct: "% Imp.", Tax: "Impuesto", Total: "Total",
		TaxableAmount: "Base imponible", RoundOff: "Redondeo", GrandTotal: "Total", PrevBalance: "Saldo anterior",
		AmountInWords: "Importe en letras", Terms: "Condiciones", Signature: "Firma autorizada", BankDetails: "Datos bancarios", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
	"fr": {
		DocTypes:   map[string]string{"INVOICE": "FACTURE", "TAX INVOICE": "FACTURE", "CREDIT NOTE": "AVOIR", "DEBIT NOTE": "NOTE DE DÉBIT", "QUOTATION": "DEVIS", "PROFORMA INVOICE": "FACTURE PROFORMA", "SALES ORDER": "COMMANDE CLIENT", "DELIVERY CHALLAN": "BON DE LIVRAISON", "SALES RETURN": "RETOUR CLIENT", "RECURRING INVOICE": "FACTURE RÉCURRENTE"},
		Categories: map[string]string{"STANDARD": "Normal", "REDUCED": "Réduit", "ZERO": "Taux zéro", "EXEMPT": "Exonéré", "TAXABLE": "Taxable"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Autoliquidation : le client est redevable de la TVA.", "INTRA_EU_B2B": "Livraison intracommunautaire : TVA non facturée, autoliquidation.",
			"EXPORT": "Exportation hors du champ de la TVA du fournisseur : TVA non facturée.", "EXEMPT": "Opération exonérée.", "ZERO_RATED": "Opération à taux zéro.",
		},
		No: "N°", Date: "Date", SupplyDate: "Date de l'opération", BillTo: "Client", ShipTo: "Livrer à", TaxID: "N° de TVA",
		Phone: "Tél.", Email: "E-mail", Web: "Web",
		Description: "Désignation", TaxCategory: "Catégorie", Qty: "Qté", Rate: "Prix", Taxable: "Base HT", TaxPct: "% Taxe", Tax: "Taxe", Total: "Total",
		TaxableAmount: "Total HT", RoundOff: "Arrondi", GrandTotal: "Total TTC", PrevBalance: "Solde précédent",
		AmountInWords: "Montant en lettres", Terms: "Conditions", Signature: "Signature autorisée", BankDetails: "Coordonnées bancaires", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
	"de": {
		DocTypes:   map[string]string{"INVOICE": "RECHNUNG", "TAX INVOICE": "RECHNUNG", "CREDIT NOTE": "GUTSCHRIFT", "DEBIT NOTE": "BELASTUNGSANZEIGE", "QUOTATION": "ANGEBOT", "PROFORMA INVOICE": "PROFORMA-RECHNUNG", "SALES ORDER": "AUFTRAGSBESTÄTIGUNG", "DELIVERY CHALLAN": "LIEFERSCHEIN", "SALES RETURN": "RETOURE", "RECURRING INVOICE": "WIEDERKEHRENDE RECHNUNG"},
		Categories: map[string]string{"STANDARD": "Regelsatz", "REDUCED": "Ermäßigt", "ZERO": "Nullsatz", "EXEMPT": "Steuerfrei", "TAXABLE": "Steuerpflichtig"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Steuerschuldnerschaft des Leistungsempfängers (Reverse Charge).", "INTRA_EU_B2B": "Innergemeinschaftliche Lieferung: steuerfrei, Steuerschuldnerschaft des Empfängers.",
			"EXPORT": "Ausfuhrlieferung: Umsatzsteuer nicht berechnet.", "EXEMPT": "Steuerfreie Leistung.", "ZERO_RATED": "Leistung zum Nullsatz.",
		},
		No: "Nr.", Date: "Datum", SupplyDate: "Leistungsdatum", BillTo: "Rechnungsempfänger", ShipTo: "Lieferadresse", TaxID: "USt-IdNr.",
		Phone: "Tel.", Email: "E-Mail", Web: "Web",
		Description: "Beschreibung", TaxCategory: "Steuerart", Qty: "Menge", Rate: "Preis", Taxable: "Netto", TaxPct: "% USt", Tax: "USt", Total: "Gesamt",
		TaxableAmount: "Nettobetrag", RoundOff: "Rundung", GrandTotal: "Gesamtbetrag", PrevBalance: "Vorheriger Saldo",
		AmountInWords: "Betrag in Worten", Terms: "Bedingungen", Signature: "Autorisierte Unterschrift", BankDetails: "Bankverbindung", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
	"it": {
		DocTypes:   map[string]string{"INVOICE": "FATTURA", "TAX INVOICE": "FATTURA", "CREDIT NOTE": "NOTA DI CREDITO", "DEBIT NOTE": "NOTA DI DEBITO", "QUOTATION": "PREVENTIVO", "PROFORMA INVOICE": "FATTURA PROFORMA", "SALES ORDER": "ORDINE DI VENDITA", "DELIVERY CHALLAN": "DOCUMENTO DI TRASPORTO", "SALES RETURN": "RESO DI VENDITA", "RECURRING INVOICE": "FATTURA RICORRENTE"},
		Categories: map[string]string{"STANDARD": "Ordinaria", "REDUCED": "Ridotta", "ZERO": "Aliquota zero", "EXEMPT": "Esente", "TAXABLE": "Imponibile"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Inversione contabile: il cliente assolve l'IVA.", "INTRA_EU_B2B": "Cessione intracomunitaria: IVA non addebitata, inversione contabile.",
			"EXPORT": "Esportazione fuori dal campo IVA del fornitore: IVA non addebitata.", "EXEMPT": "Operazione esente.", "ZERO_RATED": "Operazione ad aliquota zero.",
		},
		No: "N.", Date: "Data", SupplyDate: "Data dell'operazione", BillTo: "Cliente", ShipTo: "Spedire a", TaxID: "P. IVA",
		Phone: "Tel.", Email: "E-mail", Web: "Web",
		Description: "Descrizione", TaxCategory: "Categoria", Qty: "Q.tà", Rate: "Prezzo", Taxable: "Imponibile", TaxPct: "% IVA", Tax: "IVA", Total: "Totale",
		TaxableAmount: "Imponibile", RoundOff: "Arrotondamento", GrandTotal: "Totale", PrevBalance: "Saldo precedente",
		AmountInWords: "Importo in lettere", Terms: "Condizioni", Signature: "Firma autorizzata", BankDetails: "Coordinate bancarie", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
	"pt": {
		DocTypes:   map[string]string{"INVOICE": "FATURA", "TAX INVOICE": "FATURA", "CREDIT NOTE": "NOTA DE CRÉDITO", "DEBIT NOTE": "NOTA DE DÉBITO", "QUOTATION": "ORÇAMENTO", "PROFORMA INVOICE": "FATURA PRO FORMA", "SALES ORDER": "ENCOMENDA DE VENDA", "DELIVERY CHALLAN": "GUIA DE REMESSA", "SALES RETURN": "DEVOLUÇÃO DE VENDA", "RECURRING INVOICE": "FATURA RECORRENTE"},
		Categories: map[string]string{"STANDARD": "Normal", "REDUCED": "Reduzida", "ZERO": "Taxa zero", "EXEMPT": "Isento", "TAXABLE": "Tributável"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Autoliquidação: o cliente liquida o IVA.", "INTRA_EU_B2B": "Transmissão intracomunitária: IVA não liquidado, com autoliquidação.",
			"EXPORT": "Exportação fora do âmbito do IVA do fornecedor: IVA não liquidado.", "EXEMPT": "Operação isenta.", "ZERO_RATED": "Operação à taxa zero.",
		},
		No: "N.º", Date: "Data", SupplyDate: "Data da operação", BillTo: "Cliente", ShipTo: "Enviar para", TaxID: "NIF",
		Phone: "Tel.", Email: "E-mail", Web: "Web",
		Description: "Descrição", TaxCategory: "Categoria", Qty: "Qtd.", Rate: "Preço", Taxable: "Base", TaxPct: "% Imp.", Tax: "Imposto", Total: "Total",
		TaxableAmount: "Base tributável", RoundOff: "Arredondamento", GrandTotal: "Total", PrevBalance: "Saldo anterior",
		AmountInWords: "Valor por extenso", Terms: "Condições", Signature: "Assinatura autorizada", BankDetails: "Dados bancários", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
	"nl": {
		DocTypes:   map[string]string{"INVOICE": "FACTUUR", "TAX INVOICE": "FACTUUR", "CREDIT NOTE": "CREDITNOTA", "DEBIT NOTE": "DEBETNOTA", "QUOTATION": "OFFERTE", "PROFORMA INVOICE": "PROFORMAFACTUUR", "SALES ORDER": "VERKOOPORDER", "DELIVERY CHALLAN": "PAKBON", "SALES RETURN": "RETOUR", "RECURRING INVOICE": "TERUGKERENDE FACTUUR"},
		Categories: map[string]string{"STANDARD": "Standaard", "REDUCED": "Verlaagd", "ZERO": "Nultarief", "EXEMPT": "Vrijgesteld", "TAXABLE": "Belastbaar"},
		Notes: map[string]string{
			"REVERSE_CHARGE": "Btw verlegd naar de afnemer.", "INTRA_EU_B2B": "Intracommunautaire levering: btw niet berekend, verlegd.",
			"EXPORT": "Export buiten het btw-gebied van de leverancier: btw niet berekend.", "EXEMPT": "Vrijgestelde levering.", "ZERO_RATED": "Levering tegen nultarief.",
		},
		No: "Nr.", Date: "Datum", SupplyDate: "Leveringsdatum", BillTo: "Klant", ShipTo: "Verzenden naar", TaxID: "Btw-nr.",
		Phone: "Tel.", Email: "E-mail", Web: "Web",
		Description: "Omschrijving", TaxCategory: "Categorie", Qty: "Aantal", Rate: "Prijs", Taxable: "Excl. btw", TaxPct: "% btw", Tax: "Btw", Total: "Totaal",
		TaxableAmount: "Totaal excl. btw", RoundOff: "Afronding", GrandTotal: "Totaal", PrevBalance: "Vorig saldo",
		AmountInWords: "Bedrag in letters", Terms: "Voorwaarden", Signature: "Geautoriseerde handtekening", BankDetails: "Bankgegevens", Bank: "IBAN", BIC: "BIC/SWIFT",
	},
}
