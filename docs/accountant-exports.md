# Accountant handoff exports (2026 mappings)

Tax → Files for your accountant exports one legal entity and inclusive document-date range. Check the package, then download the ZIP or workbook. Supported countries: India, Spain, UK and US. No return is submitted and no government API credentials are required for this export. Accountant filing software/portal access remains the accountant's responsibility.

## Deliverables and review

The ZIP contains accountant.xlsx, spreadsheet-safe CSV registers, exact-decimal raw-records.json, review.json, README.txt and a SHA-256 manifest. Registers include sales, purchases, credit/debit notes, tax components, source IDs, tax snapshot versions and journal reconciliation. Drafts and cancellations are retained for review but excluded from tax calculations. Taxable bases are counted once per line, even with multiple tax components. Credits reverse amounts. Book-currency conversions use the saved document rate.

The workbook separates calculated amounts, editable accountant inputs/adjustments and formula totals. Unknown statutory fields remain blank; zero is not assumed. Recorded purchase tax is a candidate, not a determination of deductibility. Check ERROR and REVIEW items before filing. A blocked package still supplies source records, not an assurance of filing readiness. Party/product master descriptions are current values, not immutable historical identity snapshots. External transactions, tax points, scheme eligibility and opening/carry-forward balances require accountant confirmation.

## Spain

2026 general-regime Modelo 303 workpaper: ordinary IVA bases/tax at 0%, 4%, 10%, 21%; recargo at 0.5%, 1.4%, 5.2%, 1.75%; IVA corrections in 14/15 and recargo corrections in 25/26. Intra-community supplies, exports and domestic reverse-charge outward supplies have separate fields. Recorded output tax and provisional output less purchase-tax candidate are provided. Deductible current/capital tax, imports/acquisitions, prorrata and settlement/carry-forward fields need accountant input. Workbook formulas calculate 27, 45 and 46 only when required inputs are completed. This is not an AEAT upload file, SII or VERI*FACTU implementation. Exporting workpapers does not remove statutory invoicing obligations.

Sources checked: [AEAT 2026 instructions](https://sede.agenciatributaria.gob.es/Sede/todas-gestiones/impuestos-tasas/iva/modelo-303-iva-autoliquidacion_/instrucciones-2026.html) and [February–December/Q2–Q4 instructions](https://sede.agenciatributaria.gob.es/Sede/todas-gestiones/impuestos-tasas/iva/modelo-303-iva-autoliquidacion_/instrucciones-2026/instrucciones-02-12-2t-4t-2026.html).

## UK

Nine-box standard invoice-basis VAT workpaper. Recorded output VAT, input-tax candidate and net sales/purchases populate 1/4/6/7. NI acquisitions/dispatches remain accountant inputs. Box 3 sums 1+2; box 5 is the absolute difference of 3 and 4 with a separate PAYABLE/RECLAIM indicator. Special schemes and external adjustments need review. The accountant must use suitable MTD software/digital links; Rechvix does not submit MTD returns.

Source: [HMRC VAT Notice 700/12](https://www.gov.uk/guidance/how-to-fill-in-and-submit-your-vat-return-vat-notice-70012).

## India

GSTR-1 and GSTR-3B workpapers, plus eligible offline-tool CSVs for B2B/B2CL/B2CS, registered notes and HSN summaries. Native CSVs require a complete calendar month or quarter, INR, valid structural identifiers/POS/HSN/UQC and reconciling records. Repeated invoice/rate rows are consolidated. Any ERROR withholds all native CSVs together, preventing a silently partial import. Export/SEZ, reverse charge, unregistered-note original classification, nil/exempt supplies and unsupported units need explicit review/correction; no facts are invented. Document-series completeness and omitted statutory sections must be completed by the CA. ITC eligibility requires GSTR-2B and other checks.

Headers are pinned to the [official offline utility ZIP](https://tutorial.gst.gov.in/offlineutilities/returns/gst_offline_tool.zip), retrieved 2026-10-21, SHA-256 `f0a7d6f66bf71b30e9ecd045a664dda428c8fac239f51127d656e6faac60e312`. Structural/header tests do not certify acceptance by the Windows utility or GST portal. The CA imports CSVs into the current utility, validates/completes missing sections and generates portal JSON there. [Official manual](https://tutorial.gst.gov.in/downloads/invoiceuploadofflineutility.pdf).

New Indian invoice identifiers use the compact FY display (`INV/26-27/000001`) to meet the 16-character limit, retaining the full existing FY counter key. Previously issued identifiers are unchanged and flagged if incompatible. Recurring prefix RINV displays as RIN.

## US

Separate schedules for each recorded/configured state, including sales excluding tax, taxable/exempt/not-collected sales and recorded state base tax. Source destinations support accountant jurisdiction review. Local/district taxes, nexus, marketplace rules, use tax and state-specific filing layouts remain unresolved accountant inputs. These schedules are not complete state returns or universal upload files. Zero net activity requires nil-return review. No claim of state filing certification is made.

## API, security and operations

`GET /api/v1/reports/tax/accountant-package?legal_entity_id=UUID&from=2026-07-01&to=2026-09-30&format=zip`

`format=json` (default) previews status, checks and calculated boxes; `xlsx` downloads only the workbook. Both reports.view and reports.export must cover the requested legal entity. Tenant RLS and explicit company/date filters protect a single-statement consistent source snapshot. Other query filters are rejected; exports cannot silently omit branches/customers. Responses are private/no-store. Literal workbook text and escaped CSV text prevent spreadsheet formula injection. The complete file is prepared before response, avoiding partial archives.

Verified mapping year is explicitly restricted to 2026; maximum 20,000 documents/100,000 lines per package. Narrow the period if exceeded. No database migration, new service or credentials are required for this feature. Deploy the matching server and web build together using the existing deployment process. Retain backups, smoke-test a scoped download and review checks after deployment. Revert server/web together to roll back; no schema rollback needed.

Acceptance: exact-decimal unit and formula tests; real PostgreSQL sale/credit/recargo/purchase reconciliation and country isolation/permissions tests; mobile/tablet/desktop browser checks, keyboard/accessibility checks, changed-period download invalidation, and existing CI. Official filing utility acceptance and production deployment are separate checks, not implied by these tests.
