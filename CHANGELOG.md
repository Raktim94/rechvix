# Changelog

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
