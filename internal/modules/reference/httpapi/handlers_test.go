package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func get(t *testing.T, path string, into any) {
	t.Helper()
	r := chi.NewRouter()
	NewHandlers().Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}

func TestCountriesAndCurrencies(t *testing.T) {
	var cs []countryDTO
	get(t, "/reference/countries", &cs)
	if len(cs) != 250 {
		t.Fatalf("countries = %d, want 250", len(cs))
	}
	by := map[string]countryDTO{}
	for _, c := range cs {
		by[c.Code] = c
	}
	if es := by["ES"]; !es.EUMember || !es.TaxPack || es.DefaultCurrency != "EUR" {
		t.Errorf("ES = %+v", es)
	}
	if jp := by["JP"]; jp.TaxPack {
		t.Errorf("JP has no tax pack: %+v", jp)
	}
	if !by["IN"].TaxPack || !by["US"].TaxPack || !by["GB"].TaxPack {
		t.Error("IN, US and GB must report a tax pack")
	}
	var cur []currencyDTO
	get(t, "/reference/currencies", &cur)
	if len(cur) < 150 {
		t.Fatalf("currencies = %d", len(cur))
	}
}

func TestTaxCategories(t *testing.T) {
	var es struct {
		Supported  bool `json:"supported"`
		Categories []struct{ Code, Rate, Kind string }
	}
	get(t, "/reference/tax?country=es", &es)
	codes := map[string]string{}
	for _, c := range es.Categories {
		codes[c.Code] = c.Rate
	}
	if !es.Supported || codes["STANDARD"] != "21" || codes["RATE:10"] != "10" || codes["RATE:4"] != "4" {
		t.Fatalf("ES categories = %+v", es)
	}
	var us struct {
		Rates map[string]string `json:"state_base_rates"`
	}
	get(t, "/reference/tax?country=US", &us)
	if us.Rates["US-CA"] != "7.25" || len(us.Rates) != 51 {
		t.Fatalf("US rates: CA=%s n=%d", us.Rates["US-CA"], len(us.Rates))
	}
	var jp struct {
		Supported bool `json:"supported"`
	}
	get(t, "/reference/tax?country=JP", &jp)
	if jp.Supported {
		t.Error("JP must not be reported as supported")
	}
}
