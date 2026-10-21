package taxpack

import (
	"github.com/shopspring/decimal"
	"rechvix/internal/modules/reporting/domain"
	"sort"
)

func (p *Package) uk(docs map[string]domain.AccountantDocument) {
	p.Sources = []string{"https://www.gov.uk/guidance/how-to-fill-in-and-submit-your-vat-return-vat-notice-70012", "https://www.gov.uk/guidance/record-keeping-for-vat-notice-70021"}
	var output, input, sales, purchases decimal.Decimal
	for _, l := range p.Data.Lines {
		d := docs[l.DocumentID]
		if d.Status != "FINALIZED" {
			continue
		}
		factor := Sign(d).Mul(number(d.Rate))
		if d.Direction == "OUTPUT" {
			sales = sales.Add(number(l.Taxable).Mul(factor))
		} else {
			purchases = purchases.Add(number(l.Taxable).Mul(factor))
		}
		for _, c := range l.Components {
			if c.Name != "VAT" {
				p.check("ERROR", "UK_TAX_COMPONENT", d.Number, "Unexpected tax component for UK VAT workpaper.")
				continue
			}
			if d.Direction == "OUTPUT" {
				output = output.Add(number(c.Amount).Mul(factor))
			} else {
				input = input.Add(number(c.Amount).Mul(factor))
			}
		}
	}
	p.box("1", "VAT due on recorded sales", output, "Net of credits. CA must add non-recorded output adjustments, reverse charges and postponed import VAT as applicable.")
	p.unknown("2", "VAT on NI acquisitions", "NI goods are not modelled. CA must enter a verified amount, including explicit zero if not applicable.")
	p.unknown("3", "Total VAT due", "Workbook derives final Box 1 + Box 2 after CA completes them.")
	p.box("4", "Recorded input VAT candidate", input, "Not a finding of recoverability. CA must adjust partial exemption, blocked input VAT, imports and other claims.")
	p.unknown("5", "Net VAT payable or repayable", "Workbook derives ABS(Box 3 - Box 4); separate direction identifies payment or repayment.")
	p.box("6", "Recorded sales excluding VAT", sales, "Includes zero/exempt/export sales once each, not once per tax component. CA must confirm tax points and special schemes.")
	p.box("7", "Recorded purchases excluding VAT", purchases, "Source purchase register may require additions and exclusions by CA.")
	p.unknown("8", "NI dispatches to EU excluding VAT", "CA must confirm; not inferred from exports.")
	p.unknown("9", "NI acquisitions from EU excluding VAT", "CA must confirm; not inferred from purchases.")
	p.check("REVIEW", "UK_SCHEME", "", "Invoice-basis standard VAT workpaper. Cash, flat-rate, margin/retail schemes, NI and external adjustments require CA input. Use the completed workbook's digital cells with the CA's MTD bridging software; no paste-only filing workflow is claimed.")
}

func (p *Package) spain(docs map[string]domain.AccountantDocument) {
	p.Sources = []string{"https://sede.agenciatributaria.gob.es/Sede/todas-gestiones/impuestos-tasas/iva/modelo-303-iva-autoliquidacion_/instrucciones-2026.html", "https://sede.agenciatributaria.gob.es/Sede/todas-gestiones/impuestos-tasas/iva/modelo-303-iva-autoliquidacion_/instrucciones-2026/instrucciones-02-12-2t-4t-2026.html"}
	values := map[string]decimal.Decimal{}
	add := func(k string, v decimal.Decimal) { values[k] = values[k].Add(v) }
	for _, l := range p.Data.Lines {
		d := docs[l.DocumentID]
		if d.Status != "FINALIZED" {
			continue
		}
		f := Sign(d).Mul(number(d.Rate))
		base := number(l.Taxable).Mul(f)
		if d.Direction == "INPUT" {
			add("input_base", base)
			add("input_tax", number(l.Tax).Mul(f))
			continue
		}
		adjust := d.Type == "CREDIT_NOTE" || d.Type == "DEBIT_NOTE" || d.Type == "SALES_RETURN"
		if l.Classification == "INTRA_EU_B2B" {
			add("59", base)
		}
		if l.Classification == "EXPORT" {
			add("60", base)
		}
		if l.Classification == "REVERSE_CHARGE" {
			add("122", base)
		}
		if len(l.Components) == 0 && l.Classification == "ZERO_RATED" && !adjust {
			add("150", base)
		}
		for _, c := range l.Components {
			tax := number(c.Amount).Mul(f)
			if c.Name == "IVA" {
				if adjust {
					add("14", base)
					add("15", tax)
					continue
				}
				key := map[string]string{"0": "150", "4": "01", "10": "04", "21": "07"}[number(c.Rate).String()]
				if key == "" {
					p.check("ERROR", "ES_RATE_UNMAPPED", d.Number, "IVA rate has no verified 2026 Modelo 303 mapping.")
					continue
				}
				add(key, base)
				add(map[string]string{"150": "152", "01": "03", "04": "06", "07": "09"}[key], tax)
			} else if c.Name == "RE" {
				if adjust {
					add("25", base)
					add("26", tax)
					continue
				}
				key := map[string]string{"0.5": "168", "1.4": "19", "5.2": "22", "1.75": "156"}[number(c.Rate).String()]
				if key == "" {
					p.check("ERROR", "ES_SURCHARGE_UNMAPPED", d.Number, "Recargo rate has no verified mapping.")
					continue
				}
				add(key, base)
				add(map[string]string{"168": "170", "19": "21", "22": "24", "156": "158"}[key], tax)
			} else {
				p.check("ERROR", "ES_TAX_COMPONENT", d.Number, "Unexpected component in Spanish worksheet.")
			}
		}
	}
	for _, r := range []struct{ base, ratebox, taxbox, rate string }{{"150", "151", "152", "0"}, {"01", "02", "03", "4"}, {"04", "05", "06", "10"}, {"07", "08", "09", "21"}, {"168", "169", "170", "0.5"}, {"19", "20", "21", "1.4"}, {"22", "23", "24", "5.2"}, {"156", "157", "158", "1.75"}} {
		p.box(r.base, "Base at "+r.rate+"%", values[r.base], "Ordinary recorded supplies only; rectifications are separate.")
		p.box(r.ratebox, "Rate %", number(r.rate), "Statutory rate, not a monetary amount.")
		p.box(r.taxbox, "Tax at "+r.rate+"%", values[r.taxbox], "Recorded components, not recomputed from rounded bases.")
	}
	for _, r := range []struct{ code, label string }{{"14", "Rectification of IVA bases"}, {"15", "Rectification of IVA tax"}, {"25", "Rectification of recargo bases"}, {"26", "Rectification of recargo tax"}, {"59", "Intra-community supplies"}, {"60", "Exports and assimilated supplies"}, {"122", "Domestic reverse-charge outward supplies"}} {
		p.box(r.code, r.label, values[r.code], "CA must verify classification and evidence.")
	}
	var accrued decimal.Decimal
	for _, code := range []string{"152", "03", "06", "09", "170", "21", "24", "158", "15", "26"} {
		accrued = accrued.Add(values[code])
	}
	p.box("OUTPUT_TAX_RECORDED", "Recorded output IVA and recargo net of corrections", accrued, "Not a completed return: externally arising liabilities may need adding.")
	p.box("NET_TAX_RECORDED", "Recorded output tax less input tax candidate", accrued.Sub(values["input_tax"]), "Provisional book calculation only; input eligibility, special schemes and carry-forwards are unresolved.")
	p.unknown("27", "Total accrued tax", "Workbook derives this after CA completes acquisition and reverse-charge boxes.")
	p.unknown("45", "Total deductible tax", "Workbook derives this after CA completes deductible tax boxes.")
	p.box("INPUT_BASE_CANDIDATE", "Recorded purchase taxable base", values["input_base"], "Allocate between current/capital, eligible/ineligible and corrections; not a Modelo 303 box.")
	p.box("INPUT_TAX_CANDIDATE", "Recorded purchase tax", values["input_tax"], "CA must determine deductible amount and prorrata; not a Modelo 303 box.")
	for _, code := range []string{"10", "11", "12", "13", "28", "29", "30", "31", "32", "33", "34", "35", "36", "37", "38", "39", "40", "41", "42", "43", "44", "46", "64", "65", "66", "69", "70", "71", "77", "78", "87", "110"} {
		p.unknown(code, "Modelo 303 casilla "+code, "CA completes acquisitions/imports, deductible tax allocation, previous credits, territorial proportion and settlement as applicable. No assumed zero.")
	}
	p.check("REVIEW", "ES_SCHEME", "", "Modelo 303 general-regime workpaper, not an AEAT import file. Confirm deduction eligibility, capital goods, prorrata, rectifications, prior carry-forwards, scheme and transaction dates; no SII/VERI*FACTU or tax return is sent.")
}

func (p *Package) us(docs map[string]domain.AccountantDocument) {
	p.Sources = []string{"https://www.cdtfa.ca.gov/taxes-and-fees/sales-use-tax-returns-filing.htm", "https://www.tax.ny.gov/bus/st/file.htm"}
	type state struct{ gross, taxable, exempt, notcollected, tax decimal.Decimal }
	states := map[string]state{}
	for _, region := range p.Company.USCollectionRegions {
		states[region] = state{}
	}
	for _, l := range p.Data.Lines {
		d := docs[l.DocumentID]
		if d.Direction != "OUTPUT" || d.Status != "FINALIZED" {
			continue
		}
		f := Sign(d).Mul(number(d.Rate))
		s := states[l.Region]
		base := number(l.Taxable).Mul(f)
		s.gross = s.gross.Add(base)
		switch l.Classification {
		case "EXEMPT_CERTIFICATE", "EXEMPT":
			s.exempt = s.exempt.Add(base)
		case "NOT_COLLECTED":
			s.notcollected = s.notcollected.Add(base)
		default:
			s.taxable = s.taxable.Add(base)
		}
		s.tax = s.tax.Add(number(l.Tax).Mul(f))
		states[l.Region] = s
		if l.Region == "" {
			p.check("ERROR", "US_STATE_MISSING", d.Number, "Destination state is missing.")
		}
	}
	t := Table{"US state schedules", []string{"State", "Recorded sales excluding tax", "Taxable or zero-base-rate sales", "Exempt sales", "Not collected sales", "State base tax collected", "Local tax", "Nexus review", "Filing jurisdiction or form"}, nil}
	keys := []string{}
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := states[k]
		if s.gross.IsZero() && s.tax.IsZero() {
			p.check("REVIEW", "US_NO_RECORDED_ACTIVITY", k, "No net sales recorded in this configured state. CA must verify whether a nil return is required.")
		}
		t.Rows = append(t.Rows, []string{k, s.gross.StringFixed(2), s.taxable.StringFixed(2), s.exempt.StringFixed(2), s.notcollected.StringFixed(2), s.tax.StringFixed(2), "", "CA INPUT REQUIRED", "CA INPUT REQUIRED"})
	}
	for _, row := range t.Rows {
		p.Tables = append(p.Tables, Table{"US " + row[0], t.Headers, [][]string{row}})
	}
	p.Tables = append(p.Tables, t)
	p.unknown("LOCAL_TAX", "Local and special-district taxes", "CA must supply city/county/district tax and assign filing jurisdictions.")
	p.unknown("USE_TAX", "Purchases subject to use tax", "Determine use-tax exposure from purchase register and external records.")
	p.check("ERROR", "US_LOCAL_NEXUS", "", "State base-rate schedules alone are not a complete US sales-tax return. CA must resolve local jurisdictions, nexus, product taxability, marketplace sales, exemptions and use tax; no universal US upload file exists.")
}
