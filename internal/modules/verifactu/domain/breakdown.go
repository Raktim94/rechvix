package domain

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// TaxComponent is one tax charged on a line (IVA, or RE for recargo de equivalencia).
type TaxComponent struct {
	Type   string
	Rate   decimal.Decimal
	Amount decimal.Decimal
}

// TaxLine is a taxed invoice line as Rechvix's tax snapshot records it.
type TaxLine struct {
	Classification string
	Taxable        decimal.Decimal
	Components     []TaxComponent
}

type groupKey struct {
	regime, qualification, exempt, rate, surchargeRate string
}

// BuildBreakdown groups taxed lines into AEAT DetalleDesglose entries. sign is 1 for an invoice and -1 for a
// rectifying document (credit note or return), whose amounts AEAT expects as negatives. It returns the breakdown
// and the total tax (quota plus any surcharge).
func BuildBreakdown(lines []TaxLine, sign int64) ([]Breakdown, decimal.Decimal, error) {
	type acc struct {
		b     Breakdown
		base  decimal.Decimal
		quota decimal.Decimal
		sur   decimal.Decimal
	}
	groups := map[groupKey]*acc{}
	var order []groupKey
	mul := decimal.NewFromInt(sign)
	for _, l := range lines {
		var iva, re *TaxComponent
		for i := range l.Components {
			switch l.Components[i].Type {
			case "IVA":
				iva = &l.Components[i]
			case "RE":
				re = &l.Components[i]
			}
		}
		k := groupKey{regime: "01"}
		b := Breakdown{Tax: "01", Regime: "01"}
		switch l.Classification {
		case "EXEMPT":
			k.exempt, b.Exempt = "E1", "E1"
		case "EXPORT":
			k.exempt, b.Exempt = "E2", "E2"
		case "INTRA_EU_B2B":
			k.exempt, b.Exempt = "E5", "E5"
		case "REVERSE_CHARGE":
			k.qualification, b.Qualification = "S2", "S2"
		case "ZERO_RATED":
			k.qualification, b.Qualification = "S1", "S1"
			zero := decimal.Zero
			k.rate, b.Rate = "0.00", &zero
		default:
			if iva == nil {
				return nil, decimal.Zero, fmt.Errorf("verifactu: line with tax class %q has no IVA component", l.Classification)
			}
			k.qualification, b.Qualification = "S1", "S1"
			r := iva.Rate
			k.rate, b.Rate = r.StringFixed(2), &r
			if re != nil {
				k.regime, b.Regime = "18", "18"
				rr := re.Rate
				k.surchargeRate, b.SurchargeRate = rr.StringFixed(2), &rr
			}
		}
		g, ok := groups[k]
		if !ok {
			g = &acc{b: b}
			groups[k] = g
			order = append(order, k)
		}
		g.base = g.base.Add(l.Taxable)
		if iva != nil {
			g.quota = g.quota.Add(iva.Amount)
		}
		if re != nil {
			g.sur = g.sur.Add(re.Amount)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.exempt != b.exempt {
			return a.exempt < b.exempt
		}
		if a.qualification != b.qualification {
			return a.qualification < b.qualification
		}
		return a.rate > b.rate
	})
	var out []Breakdown
	total := decimal.Zero
	for _, k := range order {
		g := groups[k]
		b := g.b
		b.Base = g.base.Mul(mul).Round(2)
		if b.Qualification == "S1" {
			q := g.quota.Mul(mul).Round(2)
			b.Quota = &q
			total = total.Add(q)
			if b.SurchargeRate != nil {
				s := g.sur.Mul(mul).Round(2)
				b.SurchargeQuota = &s
				total = total.Add(s)
			}
		}
		out = append(out, b)
	}
	return out, total, nil
}
