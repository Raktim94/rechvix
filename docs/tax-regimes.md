# Tax regimes beyond India GST

Added 2026-10-21. Code: `internal/modules/taxregimes`. Migration: `0045_tax_lines_multi_regime`.

`taxregimes.Dispatcher` is the `TaxEngine` used by the server, worker and MCP app. It chooses the engine from the
**supplier legal entity's country**: empty or `IN` → India GST (unchanged), `US` → state sales tax, `ES`, `GB` and the
EU member states → VAT. Any other country returns `ErrRegimeNotSupported`; nothing is guessed.

## What is supported

| Country | Engine | Rates (verified Oct 2026) |
|---|---|---|
| Spain (ES) | IVA | 21 standard, 10 reduced, 4 super-reduced; recargo de equivalencia 5.2 / 1.4 / 0.5 |
| United Kingdom (GB) | VAT | 20 standard, 5 reduced, zero, exempt |
| 26 other EU members | VAT | standard rate per state; reduced rates only where two independent sources agree |
| United States (US) | State sales tax | state base rate for 50 states + DC |

Rates live in `internal/modules/taxregimes/rates.go` with their sources. Documents dated before **2026-01-01** are
refused (historical rates are not modelled). `RatePackVersion` and the regime name are stored on every tax line.

## Line tax categories (the product's / line's tax code)

VAT countries: `STANDARD`, `RATE:<n>` (a verified reduced rate, e.g. `RATE:10`), `REDUCED` (only if the country has exactly
one), `ZERO`, `EXEMPT`. US: `TAXABLE` (or blank) and `EXEMPT`. An unlisted rate is an error, not a fallback.

## Cross-border behaviour (VAT)

* EU → other EU member, customer has a business tax ID: 0%, `INTRA_EU_B2B` (reverse charge).
* EU → other EU member, consumer: **refused** (`ErrCrossBorderB2CUnsupported`, EU OSS destination VAT not implemented).
* EU or UK → outside the supplier's area: 0%, `EXPORT` (goods; evidence of export is the user's responsibility).
* Domestic with `reverse_charge`: 0%, `REVERSE_CHARGE`.
* VAT numbers are **not** validated (no VIES call). Set `customer_tax_registration_id` only for validated numbers.

## US sales tax

State base rate only: **local (city/county/district) taxes are not included.** Tax is charged only in states listed in the
legal entity's `us_collection_regions` (`PUT /legal-entities/{id}/us-collection-regions`, body `{"regions":["US-CA"]}`);
elsewhere the line is `NOT_COLLECTED`. Whether the business has nexus is a legal determination the system does not make.
`customer_tax_exempt` records a resale/exemption certificate. Treat results as estimates until confirmed by a sales-tax
service or advisor.

## Refused on purpose

Canary Islands (IGIC), Ceuta and Melilla (IPSI), Northern Ireland goods, EU consumer cross-border (OSS). These return
`ErrRegionNotSupported` / `ErrCrossBorderB2CUnsupported` at finalize time and the document stays a draft.

## Sales API additions (backward compatible)

`POST /sales/documents` accepts `customer_country`, `supply_region` (ISO 3166-2: `US-CA`, `ES-CN`), `customer_tax_exempt`,
`equivalence_surcharge`. Omitted fields keep the old behaviour.

## Known gaps

* Purchases still call the engine with the old input; input VAT on purchases and reclaim reporting for VAT countries are not done.
* Accounting's tax account is labelled "GST Output Tax Payable" for every country (numerically correct, label is India-specific).
* No VAT return / Modelo 303 / UK MTD / sales-tax filing exports.
* Rates sourced from two secondary publications for the EU; the Commission's TEDB could not be fetched. Re-verify before filing.
* Web UI does not yet expose the new document fields or the US collection-regions setting.
* Rate pack updates are code changes (new `RatePackVersion`); there is no admin UI to edit rates.
