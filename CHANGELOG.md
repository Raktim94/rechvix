## 0.15.2

- Dashboard redesigned around one question, "what needs me today?": the period's sales with a comparison to the previous period and an insight headline, a "Needs your attention" list (overdue receivables, overdue CRM follow-ups, low stock, absent staff, tasks), and a ledger strip of today's sales, collections, receivable (with overdue share), payable and stock value. Period selector (7/30/90 days), refresh and freshness note. Headline figures show whole currency units.
- CRM rebuilt: summary strip, search, owner filter, board and list views, drag-to-move with rollback and keyboard stage buttons, call/WhatsApp/email on each deal, a side sheet for the deal and its activity, overdue follow-up highlighting, and a visible error with retry for each fetch (a failed load used to leave a skeleton on screen forever). Adapts Zulivio's lead card, stage strip and follow-up conventions.
- Sales documents show the invoice itself (seller, customer, itemised tax, totals, bank details) via the new `GET /sales/documents/{id}/preview`, which returns the figures computed at finalization; the layout adapts Mavri's paper view. No recalculation happens in the browser.
- GST is loaded and shown only for Indian companies. The GST page no longer requests rates, vehicles or e-Way Bills before the company is known or for a company abroad; the GST line column, GST settings panel, GSTIN labels and the "GST invoice" print label follow the company's country; creating a second company asks for its country instead of assuming India.
- CRM opportunity owner picker and audited assignment, adapting Zulivio's organisation-scoped reference checks. Foreign-tenant customer/owner references are rejected on creation and update.
- CRM quotations carry the selected customer and quotation type into the document workspace.
- Ageing totals retain the organisation's base currency outside the scoped query (fixes GBP/EUR/USD balances falling back to INR).
- Fresh-install quickstart refuses existing directories and no longer installs privileged packages. Generated secrets are private; installation reports ready only after Compose and HTTP readiness succeed.
- Added isolated GitHub-runner fresh-install smoke test. No database migration or new application dependency.

# Changelog

## 0.15.1

- VERI*FACTU is Spain only: a company whose country is not Spain never records, even if the switch was left on (previously it could only be enabled for Spain but kept recording if the country changed afterwards).

## 0.15.0

### Spain: VERI*FACTU record chain (not yet certified)
- Every invoice or credit note finalized for a Spanish company with VERI*FACTU switched on appends a record to an append-only, SHA-256 hash-chained log, in the same transaction; cancelling appends an anulacion record. The database refuses updates, deletes, truncation and a record that does not continue the chain (migration 0049). Fingerprints reproduce AEAT's three published examples; generated XML validates against AEAT's schema.
- The AEAT verification QR and its legend are printed on A4, compact and 58/80 mm thermal layouts. Records are sent to AEAT by the worker over mutual TLS with the company's certificate, honouring AEAT's wait interval; results are shown per record. Records can be exported as XML and the chain re-verified from Settings, Tax.
- NOT claimed: compliance, the declaracion responsable (only the producer can make it), or any run against AEAT's real test portal. See docs/verifactu.md.

### Customer communication
- Server-side email (SMTP): `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, `SMTP_TLS`. Email a document as a secure link; opens are recorded as a read receipt on the invoice (migration 0050).
- Automatic overdue-invoice reminders (days after due, repeat interval, maximum, your own wording).
- Electronic signature requests with a public signing page, typed or drawn signature, consent text and evidence (document fingerprint, time, address, browser); signed documents carry a "signed by" line (migration 0057). A simple electronic signature, not a qualified one.
- Online card and PayPal payment links (Stripe Checkout and PayPal orders); a confirmed payment records a receipt automatically; secrets encrypted at rest (migration 0058). Tested against local fakes of both providers, not against the live services.

### Sales and stock
- Recurring invoices: schedules clone a finalized invoice weekly, monthly, quarterly or yearly, as drafts or finalized and emailed, with catch-up and pause (migration 0051).
- Custom workflow statuses on sales documents (migration 0052).
- Bill several delivery notes in one invoice (migration 0053). Fixed: billing a delivery note took its goods out of stock a second time; the invoice no longer posts stock for notes that already did, and cancelling it releases the notes.
- Projected stock availability from open sales and purchase orders; optional automatic selling-price updates from purchase costs (migration 0054).
- Phone numbers accept 7 to 15 digits (they had to be exactly 10, which rejected Spanish numbers) and ignore separators.

### New modules
- CRM: pipeline stages, opportunities, activities, follow-up tasks, lead to customer (migration 0055).
- Field service: customer equipment, incidents with a work log and time spent, maintenance plans that raise incidents on schedule, site locations (migrations 0056, 0061).
- SEPA Core direct debit: customer mandates (IBAN encrypted), ISO 20022 pain.008.001.02 collection files validated against the ISO schema, collection results recorded as receipts (migration 0059). The file is for your bank; sending it is up to you.
- Online shop, client area and public catalogue: publish products, take orders as draft sales orders, one-time emailed sign-in links for customers to open, pay and sign documents (migration 0060).

### Operations
- New environment variables: `PUBLIC_BASE_URL`, `SMTP_*`, `VERIFACTU_PRODUCER_NAME`, `VERIFACTU_PRODUCER_COUNTRY`, `VERIFACTU_PRODUCER_ID_TYPE`, `VERIFACTU_PRODUCER_ID`, `VERIFACTU_SYSTEM_ID`. Everything is off until configured. See docs/operations/deployment.md.
- Migrations 0049 to 0061 are additive.

## 0.14.24 and earlier changes

### Accountant handoff packages
- India, Spain, UK and US tax workpapers, source registers, review checks and downloadable ZIP/XLSX; scoped permissions and reconciliation.
- Spain IVA/recargo corrections and Modelo 303 workpapers; UK VAT boxes; eligible Indian offline-tool CSVs; separate US state schedules. Unknown statutory inputs stay blank for the accountant.
- No government submission credentials required for exports. See docs/accountant-exports.md for supported cases and remaining accountant work.


### Invoice language, currency and credit-note follow-up
- Settings: interface language, company invoice-language default, and scoped browser preference for new-invoice currency.
- Invoice screen: explicit language/currency/rate controls; exact-decimal conversion of catalogue prices at the invoice rate.
- Hindi browser-print invoices with embedded Devanagari font, safe HTML and scoped CSP; migration 0048 enables Hindi. Print-copy overrides do not change saved choices.
- Sales summaries subtract credit notes/returns; product/category grouping no longer repeats document totals.
- Compliance limits remain explicit; no return filing, VERI*FACTU, UK MTD submission, local US tax/nexus or automatic FX revaluation added.


### Added
- Tax: Spain (IVA incl. recargo de equivalencia), United Kingdom and all 27 EU member states (VAT, intra-EU B2B reverse charge, exports), and US state sales tax, behind a country dispatcher; India GST is unchanged (docs/tax-regimes.md). Migrations 0044-0046.
- All 165 ISO 4217 currencies and 250 countries; reference endpoints; first-run setup chooses country, currency and timezone.
- Eight UI languages (en, es, fr, de, it, pt, nl, hi) with locale-aware money/date formatting; locale parity check in CI.
- Invoice PDFs outside India itemise tax, show tax IDs, the currency and legal notes; Unicode text support.
- Scheduled, verified, encrypted backups with retention (worker, opt-in).
- Historical cost snapshots on stock movements (`stock_movements.base_unit_cost`) for accurate gross profit.

### Added (tax readiness for India, Spain, UK, US)
- Input VAT on domestic purchases (Spain, UK, EU); credit notes, tax summary report and Tax page; country-specific tax account names; readable tax errors; tax-inclusive pricing; invoice labels, tax names, dates and legal notes in the seller's language; customer address and tax ID on invoices; IBAN/BIC; country-aware contacts, inventory and settings screens. See docs/tax-regimes.md for what is and is not ready per country.

### Fixed
- Accounting and reports labelled every amount "INR" regardless of the company's currency.
- Gross profit counted quotations/orders/challans as revenue and added credit notes instead of subtracting them.
- Invoice PDFs printed accented letters and the euro sign incorrectly.
- Mobile layout: the page scrolled sideways below ~1196px; topbar and 320px reflow; table scroll regions are keyboard-focusable; contrast of sidebar group labels.
- Purchases silently skipped tax for any company outside India, so input VAT was never recorded.
- Tax-rule errors reached users as "An unexpected error occurred" (HTTP 500).
- Setup country select could show a different country from the one the form held.
- Dependency vulnerabilities: excelize, vite, and the Go toolchain/standard library (1.27.1 -> 1.27.3, covering net/http, net/http/internal/http2 and crypto/x509 advisories) plus golang.org/x/net. CI's govulncheck job had been failing since 9 October; it now reports 0 affecting vulnerabilities.

Staged per `docs/architecture.md` §16 / `docs/TODO.md`. A stage is listed
here once it has real passing unit *and* integration tests — see
`docs/TODO.md` for exactly what's built vs. in progress within a stage.

## Stage 15 — Catalogue pagination, a11y regression suite, backup/MSIX docs (2026-09-19)
- **Fixed a real scale bug**: the product catalogue list loaded every
  product for an organisation into the browser on every visit, with no
  server-side limit. `ProductRepo.ListByOrganisation` now takes
  `limit`/`offset` and returns a total count; `CataloguePage` sends
  paginated requests (50/page) with Previous/Next controls. Proven with
  a new integration test (`TestCatalogue_ListProducts_Paginates`), not
  just a UI change — this matters for any customer with a large SKU
  count, which this platform explicitly targets (distributors,
  multi-branch retailers).
- Added a real, CI-wired accessibility regression suite
  (`tests/e2e`, Playwright + axe-core): an axe scan of the login page
  (zero serious/critical violations — two pre-existing moderate
  landmark issues are flagged, not silently hidden) and a keyboard-only
  tab-order smoke test. Runs on every PR and push to `main` alongside
  the existing Go/frontend/Docker jobs. `tests/e2e` was previously an
  empty placeholder.
- Documented backup/restore properly for the first time
  (`docs/operations/backup-restore.md`): the feature itself already
  existed and was CI-tested, but had no operator-facing runbook. Added a
  concrete restore-drill procedure (spin up a scratch Postgres, restore
  into it, verify row counts and the trial-balance invariant), a
  scheduled-export script, and the real upgrade/migration mechanism.
  Also surfaced a real gap while writing it: API keys have no
  `backup.manage` scope mapping, so only session-cookie auth can
  actually call export/restore today.
- Drafted `apps/desktop/msix/TERMS_AND_PRIVACY.md` for the Microsoft
  Store submission (Store ID `9NMPSP7CR5RW`) — none existed, and
  Partner Center requires one. Marked as a draft pending legal review.
- Fixed `gofmt` drift in `apps/server/main.go`,
  `ewaybill/portal/v1/mapper.go`, and `organisation/domain/domain.go`
  that had been silently failing CI's go-static job across several
  prior commits.
- Known, not fixed this stage: `govulncheck` now flags
  `github.com/xuri/excelize/v2` (GO-2026-6452, XLSX import path) —
  a newly-disclosed third-party CVE, unrelated to this stage's changes;
  and 5 pre-existing `ewaybill_free_portal_test.go` integration test
  failures (HSN/PIN-code eligibility) predate this stage too, confirmed
  against the unmodified baseline.

## Stage 14 — Phone validation/search, WhatsApp PDF share, receivables reminders (2026-09-13)
- Party (customer/supplier) phone numbers must now be exactly 10 digits
  and unique within an organisation (DB partial unique index + app-level
  check); the sale-counter/Contacts search box, which already claimed to
  search "by name or phone", now actually matches on phone too.
- Sales documents can be shared via WhatsApp as the real PDF file (native
  OS/browser share sheet), alongside the existing signed-link share.
- The "Receivables (who owes you)" report gained a proper panel: customer
  name, phone, a one-click WhatsApp reminder button, and first/last
  reminder-sent tracking (`receivable_reminders` table) — previously the
  report only rendered a raw party UUID with no name at all.
- Bulk e-Way Bill preparation now shows each invoice's amount.
- The Tax Rates screen gained a CSV import that bulk-creates HSN/SAC
  rates and, given a `sku_code` column, can also update that product's
  HSN/SAC code and stock on hand in the same pass — catalogue's existing
  product importer only ever creates brand-new products, never updates
  one that already exists.

## Stage 13 — First-run activation flow (2026-09-07)
- A fresh install landed on a bare Sign-in page with no discoverable path
  to `/setup`, even though the full bootstrap form already existed there —
  the root route always bounced an unauthenticated visitor straight to
  `/login`, and nothing linked to setup. Added `GET /auth/bootstrap`
  (unauthenticated, mirrors the existing `POST`), returning whether
  first-run setup is still open; the Sign-in page now checks it on load
  and shows a persistent "New to Rechvix? Set up your business" link
  when it's open, the same always-visible-link pattern this project's
  sibling `nodedr-restaurant-pos` already uses on its own login page
  ("New restaurant? Create an account"), rather than a silent redirect
  away from the login form.
- Bootstrap availability now also auto-closes once any organisation
  exists, on top of the existing `ENABLE_BOOTSTRAP` env gate — belt and
  suspenders against an operator forgetting to disable it post-setup,
  since that endpoint has no permission check by design. Added
  `organisation.Service.Exists`, checked once at server startup.
- `BootstrapPage` now redirects to `/login?created=true` on success,
  which shows a "Business created — sign in to continue" banner instead
  of silently dropping the new owner back at a bare form.
- Verified end-to-end against a real Postgres container (not just unit
  tests): fresh DB reports `{"available":true}`, a real bootstrap call
  succeeds, and restarting the server against the now-non-empty database
  correctly reports `{"available":false}` and 405s `POST /auth/bootstrap`.

## Stage 12 — Team members, forgot-password UI, real brand assets (2026-09-06)
- Owner/Admin can add additional logins to their own organisation
  (`POST /users`, `identity.manage_users`/`identity.view_users`) — the
  account-creation gap Bootstrap deliberately never covered, since it only
  ever provisions the single first-run owner. Settings > Team lists
  members and adds new ones.
- Wired the frontend to the password-reset endpoints that already
  existed on the backend but had no screen (`/forgot-password`,
  `/reset-password`), plus a "Forgot password?" link on the login page.
- Replaced the placeholder rupee-glyph mark (in-app header/login, favicon,
  CasaOS icon) with the real Rechvix logo.

## Stage 10b-2 — Frontend feature screens (2026-09-03)
- Sales screen (barcode/keyboard-driven billing, list, detail), e-Way Bill
  card, inventory/purchases/contacts/catalogue/accounting/GST/reports
  screens, global search + quick-create, code-split routing
- Fixed nil-slice list endpoints crashing fresh-organisation list screens
  (Go `nil` slice serializes as JSON `null`, not `[]`)
- WCAG 2.2 AA accessibility pass: contrast-ratio fixes, ARIA role
  corrections, keyboard/Escape handling, skip link, table header scope,
  chart text alternative

## Stage 10a — Docker / Compose / CasaOS packaging (2026-09-03)
- Multi-stage server/worker Dockerfiles, docker-compose with migrate/app/
  worker services, CasaOS manifest
- Split migrator vs. runtime Postgres roles so RLS isn't silently bypassed
  by table ownership

## Stage 10b-1 — Frontend foundation (2026-09-03)
- `apps/web` scaffolded (Vite, React 19, TypeScript strict, TanStack Query/
  Router, React Hook Form, Zod, ECharts), design system, auth flow, app
  shell, dashboard wired to real reporting endpoints

## Stage 9 — Integrations (2026-09-03)
- Notification provider interfaces (email/SMS/WhatsApp), signed share
  links, scoped API keys, HMAC-signed webhooks with outbox-backed retry,
  read-only MCP server (10 tools)

## Stage 8c — Free-first e-Way Bill portal workflow (2026-09-03)
- Canonical e-Way Bill snapshot model, versioned eligibility rules, portal
  export mapper, logistics module (vehicles/transporters), manual
  import/verify flow, API-failure → free-portal fallback

## Stage 8 — Government integrations, sandbox only (2026-09-03)
- Transactional outbox + `apps/worker`, e-Invoice provider interface (mock
  + NIC sandbox adapter), e-Way Bill generate/retrieve/cancel/Part-B

## Stage 7 — Reports / dashboard (2026-09-03)
- Sales/purchases/inventory/accounting/tax reports, 8-card dashboard,
  CSV/XLSX/JSON/PDF export

## Stage 6 — Accounting (2026-09-03)
- Chart of accounts, double-entry journals enforced at 3 layers, auto-
  posting from sales/purchases, customer/supplier ledger + ageing, fiscal
  period locking

## Stage 5b — Sales documents & printing (2026-09-03)
- Quotation/proforma/order/challan/invoice/credit-debit-note/return
  document family, document numbering, PDF templates

## Stage 5a — Tax engine (2026-09-03)
- Generic `TaxEngine` interface + `IndiaGSTEngine` plugin, golden tax
  fixtures across GST rate slabs

## Stage 4 — Inventory & purchases (2026-09-03)
- Append-only stock ledger, weighted-average costing, batches/serials,
  purchase order → GRN → invoice → return flow

## Stage 3 — Catalogue, contacts, pricing (2026-09-02)
- Products/variants/SKUs, units of measure with conversions, parties,
  price lists, CSV/XLSX import scaffolding

## Stage 0-2 — Research, architecture, foundation (2026-09-02)
- Verified Go/PostgreSQL/GST API version facts, module architecture and
  ERD shape, `internal/platform` cross-cutting packages, identity +
  organisation modules, Argon2id-based auth with TOTP MFA
