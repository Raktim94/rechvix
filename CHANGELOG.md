## 0.15.2

- Ubuntu and Debian package (amd64 and arm64): `curl -fsSL https://rechvix.nodedr.com/install-linux.sh | sudo bash` installs PostgreSQL if needed, creates the database and two roles, writes a private configuration with generated secrets, applies migrations and starts the server and worker under systemd. CI installs, uses, reinstalls, removes and purges it on Ubuntu 24.04 (both architectures) and Debian 12. Removing it keeps the database and encryption key.
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

## Unreleased

## 0.23.1

- **"Create Invoice" is one click away.** A prominent **Create Invoice** button now sits in the dashboard header, the top bar of every screen (it replaces "Add sale"), the first dashboard quick tile, and each customer's page, where it opens the invoice form with that customer already selected (hidden for supplier-only contacts). Every button opens the existing invoice form directly, with no page in between. Translated in all eight interface languages. Web only: no server, API, database or invoice-data change, and the mobile app's Home button still reads "New sale". Verified in Chrome against a real server and PostgreSQL (dashboard, top bar from Sales/Contacts/Reports, customer pre-fill, and an unchanged draft, finalized and part-paid invoice), plus a new Playwright spec at 375, 768 and 1440 px (`tests/e2e/specs/create-invoice-button.spec.ts`).

## 0.23.0

- **Mobile 2.3.0 and server fixes (verified on an emulator and a real server).** Mobile: full backup *and restore* (the old backup left out payments and could not be restored), delivery note, bill-of-supply title for unregistered Indian businesses (and TAX INVOICE when the linked company is registered), an HSN guard so a GST item without an HSN code is never uploaded half-set-up, part-returns of discounted lines (the phone checks the server's draft first and refuses to issue it on a server that would over-credit). Server: **dashboard "sales today" no longer counts quotations and now subtracts returns/credit notes; "overdue" is the unpaid part of invoices past due (it used to be their full value)**; **receivables ageing now applies a receipt to the oldest open invoice whatever its date (a receipt dated before its invoice used to vanish)**; new `GET /inventory/policies` and a *Low-stock level* field on the web stock card (the level could not be set from the web before, so low stock never triggered); tests now prove a backup restore preserves invoices, customers, payments and stock. Not built, on purpose: a portal-format GSTR-1 JSON (the server already writes the GST offline utility's B2B/B2CL/B2CS/CDNR/HSN files; a hand-made portal JSON could not be checked against the portal here).

- **Mobile 2.2.1 / server fix.** Server: converting a document into a part-return or credit note now carries only the returned share of a line's discount (it used to copy the whole line's discount, over-crediting a 1-of-4 return). Phone: per-item low-stock level with Low / Out of stock filters and a warning on the bill; "Save as quotation" moved into the bill so the Save bar stays compact; after a goods return the phone's own stock figure is corrected on the synced copy of the item too. Verified end to end against a real server and PostgreSQL 18: the new payment-history integration test and the full integration suite pass, and `apps/mobile/test/live-credit.test.ts` (opt-in) checks returns, credit notes and balances against it.

- **Mobile 2.2.0: quotations, credit notes / returns, keyboard-safe Save bar.** Quotations are kept on the phone and never count as sales; credit notes and returns are issued through the server (sales return puts stock back, credit note is money only), resumable and idempotent, and lower the net bill everywhere; money received beyond the net bill is shown as owed back to the customer. No server migration (phone database v4, additive). Web: *Reverse* on a receipt (with a required reason; the row stays, struck through), *Prices include GST* for Indian bills, and the receivables export has a Party name column.

- **Invoices keep the full total and show every installment.** The PDF, print (A4, compact, 80/58 mm thermal), HTML and on-screen invoice now print the invoice total, each payment received (date, method, reference), total paid, balance due and status, instead of only the grand total. The UPI QR on a part-paid invoice asks for the balance due, not the whole total again. Reversed receipts are not counted. Overpayment shows a zero balance (no refund is invented). `GET /sales/documents/{id}/preview` gains a `payment` block. No migration. Mobile 2.1.3 lists each installment on all three designs; the web payment panel sums in whole cents.

- **Receipts are safe to retry and safe to correct (migration 0065).** `POST /accounting/receipts` accepts an optional `client_ref`: the same value returns the receipt already booked instead of booking it twice (a phone on a flaky connection can retry freely). New `POST /accounting/receipts/{id}/reverse` (with a required `reason`) undoes a receipt entered by mistake without deleting it: a reversing journal is posted, the receipt records who, when and why, and the customer's balance and the invoice's paid amount go back up. Reversed receipts are ignored by every "paid against this invoice" total (invoice lists, reminders, SEPA, online payments, shop, dashboard collections, payment-mode report). Validation problems on receipts now answer 400 `INVALID_INPUT` instead of 500.

Everyday-use polish across billing, purchasing, reporting and phones. No database migration. API changes are additive.

- **Fixed: customer search missed name fragments.** Typing the first word of a customer ("Gupta") found nothing for "Gupta Engineering Works", so the billing box offered only "+ New customer". Search now matches substrings of the name, trade name, email and phone (case-insensitive, wildcards escaped), ranks prefix matches first and keeps trigram matching for typos. Integration test included.
- **Item names everywhere.** `GET /sales/documents/{id}` and `GET /purchases/documents/{id}` also return `line_names` (variant id to product name). Resumed drafts no longer show "Item 1", the finalized invoice page and saved purchase bills gain an Item column, and a saved purchase shows its bill number as the heading.
- **Collect payment faster.** "Record payment" leads the invoice actions, opens the form with the full balance filled in, and disappears once the invoice is paid in full. Rarely used panels (payment links, e-signature, recurring, email, share links) sit under one disclosure.
- **Sales list.** All / Invoices / Drafts / Quotations / Orders views with counts, an empty state with a New sale button, secondary filters behind "More filters".
- **Billing screen.** Language, currency and exchange rate fold into "More options"; Start sale explains why it is disabled; the catalogue list no longer pushes the bill off screen once items are added; stock counts show as plain text unless low or out.
- **Inventory** opens on a stock-on-hand table instead of an empty search. **Reports** gain a period selector (last 30 days, this month, last month, quarter, financial year, custom) applied to every dated report, chart and export. **GST / Tax** leads with filing and accountant files.
- **Dashboard "Get started" checklist** driven by real data (business state, items, first customer, first invoice) that hides itself once complete.
- **Phones.** A bottom bar (Home, Sales, Catalogue, Contacts, More) with a full-menu sheet replaces the 16-item scrolling strip; sales, catalogue, contacts and purchases lists become stacked rows instead of tables that hide their totals off screen.
- **Design.** No gradients or glow shadows, no endlessly looping skeleton shimmer, no card-inside-card (empty states, billing total, nested import panel), real SVG icons instead of text glyphs and emoji, status badges only where they say something, concentric radii where boxes still nest.
- **Accessibility.** Defined the missing `.sr-only` class (screen-reader-only captions were rendering visibly), labelled the calendar assignee select, per-section browser tab titles. axe-core (WCAG 2.1 AA and best practice) reports no violations across all 20 app routes at 1440 and 390 px.
- **Deployment.** Optional `HTTP_BIND` in the Compose file to publish the app on 127.0.0.1 behind a same-host TLS proxy; the default is unchanged.
- **Purchases list total and sales payment status.** `GET /purchases/documents` rows carry a read-only `LinesTotal` (sum of line totals before tax) and `GET /sales/documents` rows a read-only `PaidAmount` (sum of receipts against the document); both are computed on read, so there is still no migration. The purchases list gains a Total column. The sales list shows Paid, Part paid, Overdue or Unpaid on finalized invoices and has Unpaid and Overdue views. Integration tests included.
- **Fixed: GST reports.** The HSN summary counted every intra-state line twice (taxable value and total tax), and finalized quotations, orders and challans were listed in GSTR-1, GSTR-3B and the HSN and tax-rate summaries as if they were supplies. Both are fixed; the summary now reconciles with GST payable in the ledger. Regression test included.
- **Fixed from a real-data walk-through.** Field service lists incidents first with aligned fields and readable priorities; the CRM board and the invoice preview no longer show empty or mismatched blocks; calendar tasks have an accessible label; the reports chart legend no longer overlaps its axis and report tables show status and type as words; the setup form proposes a first branch and warehouse. `LineProductNames` is permission-gated. A `.dockerignore` keeps host `node_modules` out of image builds.
- **Verified on a real stack.** The production Docker Compose stack (built from the Dockerfiles) was brought up with the setup wizard, sign-in and a backup download; backup and restore were exercised against real data. The accountant package accepts 2026 only (the verified mapping year), so it needs the 2027 mappings before January 2027.
- **Languages.** The Sales list and the Billing counter are now fully translated in all eight languages (English, Spanish, French, German, Italian, Portuguese, Dutch, Hindi), including plurals, validation and error messages. Credit notes and sales returns are now subtracted in GSTR-3B, the HSN summary and the tax-rate summary, and listed as negative rows in GSTR-1. The accountant-files date picker defaults to the supported year instead of one the server rejects. `seroval` is updated to 1.7.7 (security advisories).
- **Not yet done:** about 900 more strings across 67 screens (Contacts, Purchases, Catalogue, Settings, CRM, GST, Import and others) still use English literals outside the eight-language files.

## 0.22.0

CRM, brought up to the everyday standard of a sales team's tools (adapting Zulivio's lead scoring, My Day queue, funnel and bulk-assignment conventions). No database migration and no new endpoints; everything below is drawn from the leads already in the CRM.

- **Three sections: Pipeline, My day, Insights.** The board and list stay in Pipeline; the other two answer "who do I call?" and "how is the team doing?".
- **Lead score.** Every open lead shows HOT, WARM or COLD with a 0 to 100 score from six visible facts (phone, email, deal value, a close date still ahead, a follow-up booked, how far along the pipeline it is). The lead sheet lists every point, so nobody has to trust a black box. Filter the board and list by score and by source.
- **My day** lists who needs you, most urgent first: overdue follow-ups, today's, deals past their close date, then hot leads with nothing scheduled, with call, WhatsApp and email on each. **Call next lead** dials the first one. The tab shows how many are overdue or due today.
- **Why a lead was lost.** Moving a lead to Lost asks for a reason (price, no response, competitor, not interested and so on) and keeps it on the lead's timeline.
- **Insights**: open pipeline, a weighted forecast (open value times the chance of winning at each stage), win rate, leads added in the last 14 days, a funnel by stage, where leads come from with conversion per source, and a table by owner.
- **Bulk actions in the list**: select leads (or all shown), assign them to one owner or unassign in one step, and export the selection. **Export CSV** downloads what is on screen; cells that start with =, +, - or @ are neutralised so a spreadsheet never runs them as formulas.

## 0.21.0

- **Import leads into the CRM from a spreadsheet.** On the CRM screen, **Import leads** takes a CSV or Excel file (no key or account needed), matches your columns for you (Lead Name, Company, Mobile No., E-mail, Amount, Lead Source, Status and similar headers from other CRMs are recognised, and any match can be changed), shows a preview of every row, and writes nothing until you confirm. Each row can carry a company, contact person, phone, email, deal value (₹ and thousands separators are cleaned), currency, source, stage, owner (by name or email) and an expected close date (day-first dates work). A row without any name is reported as an error; a row whose phone, email or name and company already exists in the CRM, or earlier in the same file, is skipped as a duplicate, so a file can be re-run safely. The same panel is step 5 of the Import page. New `POST /crm/opportunities/import` and `/inspect`. No database migration.

## 0.20.0

- **New purchase is now a full bill grid.** Pick the supplier (search by name or phone, or **+ Add party** without leaving), enter the supplier's bill number and date, then type items down the grid: item, quantity, unit, rate, discount % and amount, with a running total. Enter walks item → quantity → rate → discount → the next row, a new row appears by itself, Ctrl+Enter saves, and **Save as draft** keeps it for later. The item list shows sale price and stock as you type, and Enter always looks up exactly what was typed (so a barcode scanner or fast typist never gets a stale match). The discount is applied to the unit price. Leaving a half-filled bill asks first.
- **Add new item** in purchase and sales: item name, sale rate, MRP, HSN code, tax rate (GST %) and unit in one small form. Enter moves to the next field and saves from the last; **Save & add another** keeps the form open so several items can be created in a row. The item is saved to the catalogue (searchable and reusable straight away), its sale rate goes to the price list, and the tax rate is recorded for its HSN code if none is on file. On the sales screen it is added to the bill at its sale rate, and searching or scanning something that does not exist offers to create it.
- **Tally day-book import**: Sales and Purchase vouchers in a Tally XML export become past invoices (original number, date, party and total, tax included). Cancelled and optional vouchers are skipped; receipts and payments are separate vouchers in Tally, so invoices arrive fully due.
- **Imported invoices show what is still owed and for how long**: an "Unpaid only" filter, an age in days on every invoice, and an ageing table (0-30, 31-60, 61-90, over 90 days) for customers and suppliers. Still a reference view; the books' balances come from the opening balances.
- **Install on a phone**: the web app has an install manifest and icons, so "Add to Home screen" opens Rechvix full screen like an app. It is a web app, not a store app.
- No database migration.

## 0.19.0

- **Import your data** (new menu item, `/import`): one guided page that takes a business over from Vyapar, Tally, Marg or a spreadsheet in the right order: customers and suppliers, items with opening stock, opening balances, then past invoices. Each step has the column-matching preview and never writes until confirmed.
- **Opening balances** for customers and suppliers become real journal entries (a receivable as Dr Accounts Receivable / Cr Opening Balance Equity, a payable as Dr Opening Balance Equity / Cr Accounts Payable, tagged to the party), so ledgers, ageing and the dashboard are right from the first day. One balance per contact, so a file can be re-run safely; a contact that is both customer and supplier must say RECEIVABLE or PAYABLE; the books still balance. New `POST /migration/opening-balances/import` and `/inspect`.
- **Past invoices** (sales and purchases) are kept in an imported-history register with their original numbers, dates, totals and amounts paid, searchable on the Import page. They are reference records only and never change stock, tax returns or the accounts, so nothing is counted twice. Day-first dates (25/12/2025, 25-Dec-2025) are understood. New `POST /migration/invoices/import`, `/inspect` and `GET /migration/invoices`. Migration 0064 (additive).
- **Dashboard "How customers paid"**: cash, UPI, card, bank transfer, cheque and other for the chosen period, from the receipts already recorded, with amounts and percentages. New `GET /reports/sales/payment-modes`.

## 0.18.0

- Online shop orders now follow a fulfilment flow: New, Confirmed, Processing, Dispatched, Delivered, or Cancelled until dispatch. The orders list in Settings has a tab per stage with counts, the next step on every order, a WhatsApp message to the customer, and **Create invoice**, which makes a draft tax invoice from the order (once; asking again opens the same one) and opens it. Finalizing that invoice in Sales is what takes the goods out of stock. An order that has an invoice is cancelled by cancelling the invoice. New `POST /shop/orders/{id}/invoice`; migration 0063 is additive (wider status list, `invoice_document_id`, `status_changed_at`).

## 0.17.1

- Fixed the dashboard (and any long page) scrolling past its end and exposing a white strip: content positioned absolutely inside a page, such as screen-reader tables and chart internals, was escaping the scroll area and making the whole document taller than the window. The scroll area now contains it, the shell height follows a phone browser's collapsing toolbar, and scrolling no longer chains to the page.
- Dashboard: "Collect payments" (largest balances with a one-tap WhatsApp reminder) and "Running low" (items under their reorder level, with a link to create a purchase). The Receivable vs payable chart uses compact figures so labels are no longer cut off on phones, and a sales change above 999% reads ">999%".

## 0.17.0

- Bulk import of products and contacts now has a "Match your columns" step: headers from Vyapar, Tally, Marg or a hand-made sheet (Party Name, Phone No., Item Name, HSN, Sale Price, Tax Rate, Opening Stock and so on) are matched to Rechvix fields automatically and can be changed, skipped or set to one fixed value for every row (for example party type). Rupee signs, thousands separators (including 1,23,456.50) and a trailing % are cleaned from numeric columns. Files whose headers already use the old column names import exactly as before. New `POST /contacts/parties/import/inspect` and `POST /catalogue/products/import/inspect`; the import endpoints accept an optional `mapping` query parameter. No migration.
- Tally Prime / ERP 9 master exports (.xml) import directly: Sundry Debtors and Creditors become customers and suppliers, stock items become products with unit, HSN, GST rate and opening quantity. Tally's UTF-16 files and stray control characters are handled. Choose the .xml file in the same import panel.
- Retail mode (Settings, per browser): hides CRM, Service, Accounting, SEPA and Integrations from the menu and command palette for counter-billing shops. Nothing is removed and the switch is reversible.

## 0.16.0

- Scanning a bill now works: the OCR engine (worker, WASM core, English language data) is served by the app itself instead of a CDN that the server's content security policy blocked. Photos are downscaled and contrast-stretched before reading. The online Puter option is removed (it sent bills to a third party and could not run under the policy).
- Invoice PDFs redesigned: two-column header with logo, title and document details, two-column parties, item rows that grow to fit long descriptions (they were cut off) and no longer overflow the page margin on Indian invoices, a totals box with a highlighted grand total, and a payment block with bank details and, for Indian invoices with a UPI id, a scannable UPI QR for the amount. "Modern" is the default design. Adapts Mavri's invoice layout.
- New-sale screen: customer row first, aligned fields, keyboard hints hidden on phones; the mobile tab bar scrolls with a fade edge and keeps the current tab in view.
- Two-way sync of products and contacts between a desktop install and a central server (Settings, Sync with your server; ADR 0008). Migration 0062, API scope `sync:readwrite`. Sales, stock and accounting are not synced yet.
- Windows package 0.3.0.0; the install test now also checks that the OCR files are served and the policy allows them.

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
