// Package taxregimes implements tax engines for jurisdictions other than
// India GST: VAT for Spain, the United Kingdom and the EU member states, and
// US state sales tax. Each satisfies taxation/domain.TaxEngine; Dispatcher
// picks one from the supplier's country.
//
// Rates are data, not logic, and are deliberately conservative:
//   - A rate appears here only if it was confirmed from an official source
//     (UK, Spain) or from two independent publications that agree (EU, US
//     state base rates). Where the two EU sources disagreed on a reduced
//     rate, that rate is left out and the line fails loudly instead of using
//     a guess.
//   - Every rate table carries a verification date. Documents dated before
//     RatesEffectiveFrom are refused, because historical rates differ and are
//     not modelled.
//   - Local/regional taxes that this package does not implement (Canary
//     Islands IGIC, Ceuta/Melilla IPSI, Northern Ireland goods, US local
//     sales taxes, EU OSS destination-rate VAT) are refused, not approximated.
package taxregimes

import (
	"time"

	"github.com/shopspring/decimal"
)

// RatePackVersion identifies the dated rate tables below. It is stored with
// every tax snapshot produced from them.
const RatePackVersion = "2026-10-21"

// RatesEffectiveFrom is the earliest document date the tables are valid for
// (rates are as of January 2026 and re-checked October 2026).
var RatesEffectiveFrom = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// vatRates is one country's verified VAT rates, in percent.
type vatRates struct {
	Standard decimal.Decimal
	// Reduced holds every verified reduced or super-reduced rate, ascending.
	Reduced []decimal.Decimal
}

func rates(std string, reduced ...string) vatRates {
	r := vatRates{Standard: d(std)}
	for _, x := range reduced {
		r.Reduced = append(r.Reduced, d(x))
	}
	return r
}

// vatTable covers Spain, the United Kingdom and the 27 EU member states.
//
// Sources (checked 2026-10-21):
//
//	GB: https://www.gov.uk/vat-rates (20 standard, 5 reduced, 0 zero)
//	ES: standard 21 and reduced 10 from both publications below; the 4%
//	    super-reduced rate and the equivalence surcharge pairing (21->5.2,
//	    10->1.4, 4->0.5) from the Agencia Tributaria
//	    (sede.agenciatributaria.gob.es, "Tipos del recargo de equivalencia").
//	EU: standard rates agree in https://taxfoundation.org/data/all/eu/value-added-tax-vat-rates-europe/
//	    (as of January 2026) and https://www.eurofiscalis.com/en/vat-rates-in-ue/
//	    (updated 28 Sept 2026). Reduced rates listed are those present in BOTH.
//	    Omitted where the sources differ: super-reduced/parking rates (only one
//	    source lists them), Luxembourg 14%, Lithuania 9% vs 12%.
//
// The Commission's own table (TEDB) could not be fetched during verification;
// re-verify against it before relying on a rate for a filing.
var vatTable = map[string]vatRates{
	"GB": rates("20", "5"),
	"ES": rates("21", "4", "10"),
	"AT": rates("20", "10", "13"),
	"BE": rates("21", "6", "12"),
	"BG": rates("20", "9"),
	"HR": rates("25", "5", "13"),
	"CY": rates("19", "5", "9"),
	"CZ": rates("21", "12"),
	"DK": rates("25"),
	"EE": rates("24", "9", "13"),
	"FI": rates("25.5", "10", "13.5"),
	"FR": rates("20", "5.5", "10"),
	"DE": rates("19", "7"),
	"GR": rates("24", "6", "13"),
	"HU": rates("27", "5", "18"),
	"IE": rates("23", "9", "13.5"),
	"IT": rates("22", "5", "10"),
	"LV": rates("21", "5", "12"),
	"LT": rates("21", "5"),
	"LU": rates("17", "8"),
	"MT": rates("18", "5", "7"),
	"NL": rates("21", "9"),
	"PL": rates("23", "5", "8"),
	"PT": rates("23", "6", "13"),
	"RO": rates("21", "11"),
	"SK": rates("23", "5", "19"),
	"SI": rates("22", "5", "9.5"),
	"SE": rates("25", "6", "12"),
}

// spainEquivalenceSurcharge maps an IVA rate to its recargo de equivalencia
// rate (Agencia Tributaria). Tobacco's 1.75% surcharge is not modelled.
var spainEquivalenceSurcharge = map[string]decimal.Decimal{
	"21": d("5.2"),
	"10": d("1.4"),
	"4":  d("0.5"),
}

// unsupportedRegions are subdivisions whose indirect-tax rules differ from the
// national VAT pack and are not implemented.
var unsupportedRegions = map[string]string{
	"ES-CN":  "Canary Islands (IGIC applies, not IVA)",
	"ES-CE":  "Ceuta (IPSI applies, not IVA)",
	"ES-ML":  "Melilla (IPSI applies, not IVA)",
	"GB-NIR": "Northern Ireland (goods follow EU VAT rules under the Windsor Framework)",
}

// usStateBaseRates are US state-level base sales tax rates in percent, as of
// the Tax Foundation's 2026 midyear table
// (https://taxfoundation.org/data/all/state/2026-sales-tax-rates-midyear/),
// 50 states plus DC. Local (city/county/district) taxes are NOT included and
// can add several points; combined rates are not computed.
var usStateBaseRates = map[string]string{
	"AL": "4", "AK": "0", "AZ": "5.6", "AR": "6.5", "CA": "7.25", "CO": "2.9", "CT": "6.35", "DE": "0",
	"DC": "6", "FL": "6", "GA": "4", "HI": "4", "ID": "6", "IL": "6.25", "IN": "7", "IA": "6",
	"KS": "6.5", "KY": "6", "LA": "5", "ME": "5.5", "MD": "6", "MA": "6.25", "MI": "6", "MN": "6.88",
	"MS": "7", "MO": "4.23", "MT": "0", "NE": "5.5", "NV": "6.85", "NH": "0", "NJ": "6.63", "NM": "4.88",
	"NY": "4", "NC": "4.75", "ND": "5", "OH": "5.75", "OK": "4.5", "OR": "0", "PA": "6", "RI": "7",
	"SC": "6", "SD": "4.2", "TN": "7", "TX": "6.25", "UT": "6.1", "VT": "6", "VA": "5.3", "WA": "6.5",
	"WV": "6", "WI": "5", "WY": "4",
}
