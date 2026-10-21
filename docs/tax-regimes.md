# Tax regimes beyond India GST

Added 2026-10-21. Code: `internal/modules/taxregimes`. Migration: `0045_tax_lines_multi_regime`.

`taxregimes.Dispatcher` is the `TaxEngine` used by the server, worker and MCP app. It chooses the engine from the
**supplier legal entity's country**: empty or `IN` → India GST (unchanged), `US` → state sales tax, `ES`, `GB` and the
EU member states → VAT. Any other country returns `ErrRegimeNotSupported`; nothing is guessed.

## Readiness by country (2026-10-21)

"Ready" means calculated, invoiced, posted to the ledger, reversible by credit note, summarised, and exercised in an automated
test **and** in a real browser run. It does not mean filed: nothing here submits a return to a tax authority.

| | India | Spain | United Kingdom | United States |
|---|---|---|---|---|
| Sales tax calculation, invoice, PDF | ready | ready | ready | ready (state base rate) |
| Credit notes / returns | ready | ready | ready | ready |
| Purchases | ITC ready (needs supplier GSTIN) | input IVA ready (domestic supplier) | input VAT ready (domestic supplier) | tax paid is part of cost (no split) |
| Ledger accounts | GST Input/Output | IVA soportado/repercutido | Input/Output VAT | Sales Tax Paid/Payable |
| Tax summary for preparing a return | GSTR-1 / GSTR-3B (partial boxes) | Tax summary (not the Modelo 303 layout) | Tax summary (not the 9-box layout) | Tax by state |
| Invoice language / date format | English, 21-Oct-2026 | Spanish, 21/10/2026 | English, 21-Oct-2026 | English, 10/21/2026 |
| Tax-inclusive (retail) pricing | n/a in UI | yes | yes | yes |

**Dependencies you must plan for (not implemented):**
* **India:** e-Invoice (IRP) and e-Way Bill run against the government *sandbox* or the mock provider until real credentials are configured; GST returns are filed on the GST portal, not from here; reverse charge, ITC reversal, ineligible ITC and TCS/TDS are not modelled.
* **Spain:** VERI*FACTU records, QR and AEAT submission are implemented but NOT certified (see [verifactu.md](verifactu.md)); do not describe Rechvix as compliant. AEAT's approved-deadline note lists 1 January 2027 for corporate-income-tax taxpayers and 1 July 2027 for the other taxpayers in scope. However, the Ministry's 5 October 2026 notice proposes aligning outstanding obligations to October 2028; it says the alignment is pending approval. Do not treat that proposal as enacted law or describe Rechvix as Spanish-compliant before any deadline. Recheck the official sources before release: [AEAT approved-deadline note](https://sede.agenciatributaria.gob.es/Sede/iva/sistemas-informaticos-facturacion-verifactu/nota-informativa-ampliacion-plazo-adaptacion-facturacion.html), [Ministry proposal](https://www.hacienda.gob.es/sgt/gabsehacienda/nota-informativa-verifactu.pdf). RRSIF compliance and VERI*FACTU submission mode are distinct concepts. Modelo 303/390, SII, FacturaE/Peppol, IGIC and IPSI are not implemented.
* **United Kingdom:** returns for VAT-registered businesses must be submitted through Making Tax Digital compatible software with digital links; this system only prepares the figures. Northern Ireland goods (Windsor Framework), flat-rate and cash-accounting schemes are not implemented.
* **United States:** no local (city/county/district) tax, no nexus determination, no product-specific taxability by state, no marketplace-facilitator rules, no filing. You choose the states where you collect.
* **EU (all 27 states):** sales to consumers in another member state (OSS), reverse-charge purchases and import VAT are refused with a clear message.
* **All:** the dated rate tables must be re-verified and updated by a release when rates change; documents dated before 1 January 2026 are refused.

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

## Purchases, credit notes, accounts and the tax summary

* **Purchases (VAT countries):** a finalized purchase invoice from a *domestic* supplier calculates input VAT at the tax category of
  each line, posts it to the input-tax account and records a snapshot. A supplier registered in another country is refused
  (`ErrCrossBorderPurchaseUnsupported`) because reverse charge / import VAT is not implemented; it is never silently dropped.
  Purchase returns and debit notes reverse it. For US and other countries tax paid on a purchase is booked as part of its cost.
* **Credit notes and returns** copy the original invoice's tax context (country, region, exemption, surcharge) and reverse the
  tax proportionally; the ledger and the printed document both show it.
* **Chart of accounts:** accounts 1400 and 2100 are named for the country (see the table above); a company's customised names are never overwritten.
* **Tax summary** (`GET /reports/tax/summary`, page *Tax*): output tax on sales and input tax on purchases by tax and rate, with
  credit notes and returns subtracted, US sales by state, and zero-tax lines (exempt, zero-rated, reverse-charge, export,
  not collected) shown with their bases. Net = output − input. Preparation data, not a return.
* **Errors:** every refused case reaches the user as a readable 400 (`TAX_CASE_NOT_SUPPORTED` / `TAX_DETAILS_INVALID`) instead of a generic 500.

## Invoices outside India

* **Layout:** for a legal entity whose country is not India the PDF switches to a generic layout: `Tax category | Qty |
  Rate | Taxable | Tax % | Tax | Total` per line, one summary line per tax and rate ("IVA 21%: 42.00"), and
  "Grand Total (EUR)". India keeps the GST layout (HSN, CGST/SGST/IGST, "TAX INVOICE") unchanged.
* **IDs:** the seller's tax ID (`PUT /legal-entities/{id}/tax-id`, Settings → tax details) and the customer's selected tax
  registration print as "Tax ID". The ID is stored as typed (trimmed, upper-cased) and is **not validated** (no
  national checksum, no VIES).
* **Legal notes** print when they apply: reverse charge, intra-Community supply, export, exempt, zero-rated, US
  exemption certificate, US not registered to collect, US state without sales tax.
* **Numbering:** `INV/<calendar year>/000001` outside India; India keeps `INV/2026-27/000001` (April-March).
* **Language and dates:** labels, document headings, tax names and legal notes are printed in the seller's language (Spanish, French, German, Italian, Portuguese, Dutch; English otherwise) and dates in the country's format. Labels were drafted by an AI model and need review by a native speaker and an accountant.
* **Parties:** the customer's default billing address and tax ID print under "Bill To"; a date of supply prints when it differs from the issue date. Bank details print as IBAN and BIC/SWIFT outside India.
* **Text:** Windows-1252 text (all Western European languages and the euro sign) uses the built-in fonts and every theme
  looks as before; any other text (for example Polish, Czech, Hungarian, Greek, Cyrillic) switches that document to an
  embedded Unicode font. Devanagari uses an embedded-font browser-print HTML path; CJK and Arabic remain unsupported by the server PDF renderer.

## Known gaps

* Accounting's tax account is labelled "GST Output Tax Payable" for every country (numerically correct, label is India-specific).
* Invoice language precedence is one-copy print override → document setting → company setting → seller country. No per-customer language default yet.
* A Spanish invoice must also carry items this system does not capture (for example the date of the transaction when it
  differs from the issue date, and the customer's address and NIF on full invoices to businesses); review the layout with
  your gestor. SII, VERI*FACTU, FacturaE/Peppol and other electronic-invoicing and real-time reporting regimes are not implemented.
* Credit notes outside India reuse the same document types and layout; the legal wording for rectifying invoices is not added.
* No VAT return / Modelo 303 / UK MTD / sales-tax filing exports.
* Rates sourced from two secondary publications for the EU; the Commission's TEDB could not be fetched. Re-verify before filing.
* The UI covers the sale and set-up path (setup, product tax category, invoice tax details, Settings tax ID and US collection states). Purchases, returns/credit notes, contacts' detail pages and reports are still India-shaped in places (HSN/GST wording).
* Rate pack updates are code changes (new `RatePackVersion`); there is no admin UI to edit rates.

## Release boundary for language/currency work

No feature in this release files a tax return. UK VAT figures need separate MTD-compatible submission software ([HMRC](https://www.gov.uk/submit-vat-return/how-to-send-vat-return)); Rechvix does not implement that submission. India adapters remain sandbox/mock unless an operator deliberately configures and verifies a supported live provider; this work does not configure credentials or enable live submission. US coverage remains state base rates only, without local taxes or nexus determination.

Foreign-currency invoice posting is available at an explicit stored rate, but automated FX revaluation is not. The sales summary now subtracts credit notes/returns and attributes product/category totals to snapshot lines. It is an operational document summary, not a tax return.


## Accountant exports (2026-10-21)

Company-scoped tax workpapers and audit registers are now available for India, Spain, UK and US. Accountant inputs and unresolved cases remain explicit; these are not universally ready-to-upload returns. See [scope, country mappings and acceptance](accountant-exports.md).
