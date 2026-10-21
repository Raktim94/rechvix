// Package httpapi serves read-only reference data (countries, currencies, tax
// categories) from the same tables the backend validates against, so the web
// app never keeps a second copy that can drift.
package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"rechvix/internal/modules/taxregimes"
	"rechvix/internal/platform/geo"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/money"
)

type Handlers struct{}

func NewHandlers() *Handlers { return &Handlers{} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/reference/countries", h.countries)
	r.Get("/reference/currencies", h.currencies)
	r.Get("/reference/tax", h.tax)
}

type countryDTO struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	DefaultCurrency string `json:"default_currency,omitempty"`
	EUMember        bool   `json:"eu_member"`
	TaxPack         bool   `json:"tax_pack"`
}

func (h *Handlers) countries(w http.ResponseWriter, r *http.Request) {
	packs := map[string]bool{"IN": true}
	for _, c := range taxregimes.SupportedCountries() {
		packs[c] = true
	}
	out := make([]countryDTO, 0, len(geo.Countries))
	for _, c := range geo.Countries {
		out = append(out, countryDTO{Code: c.Code, Name: c.Name, DefaultCurrency: c.DefaultCurrency, EUMember: geo.IsEUMember(c.Code), TaxPack: packs[c.Code]})
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, out)
}

type currencyDTO struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Numeric     string `json:"numeric"`
	MinorDigits int32  `json:"minor_digits"`
}

func (h *Handlers) currencies(w http.ResponseWriter, r *http.Request) {
	out := make([]currencyDTO, 0, len(money.ISO4217))
	for _, c := range money.ISO4217 {
		out = append(out, currencyDTO{c.Code, c.Name, c.Numeric, c.MinorDigits})
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// tax returns the tax categories for ?country=XX and the rate-pack metadata.
// India uses the HSN/SAC rate master instead, so its category list is empty.
func (h *Handlers) tax(w http.ResponseWriter, r *http.Request) {
	country := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country")))
	body := map[string]any{
		"country":           country,
		"rate_pack_version": taxregimes.RatePackVersion,
		"effective_from":    taxregimes.RatesEffectiveFrom.Format("2006-01-02"),
		"categories":        taxregimes.Categories(country),
		"supported":         country == "IN" || taxregimes.Categories(country) != nil,
	}
	if country == "US" {
		body["state_base_rates"] = taxregimes.USStateRates()
		body["note"] = "State base rates only; local taxes are not included."
	}
	cacheable(w)
	httpx.WriteJSON(w, http.StatusOK, body)
}

func cacheable(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, max-age=3600") }
