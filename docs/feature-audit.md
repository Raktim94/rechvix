# Rechvix 2.0 — Feature audit (Phase 0)

Audited 2026-10-21 against `main` @ `cd054aa`. Status comes from source, migrations and
test files, not from `docs/TODO.md` checkboxes. Legend: **Full** = implemented and
unit-tested; **Partial** = real but incomplete; **Stub** = scaffold only; **Missing** = absent;
**Blocked-cred / Blocked-reg** = needs credentials / regulatory validation.
"Integration" test status could not be re-run in this audit (see `docs/risks-2.0.md` R-01);
it was re-run on 2026-10-21 (285 pass / 0 fail).

## Baseline (reproducible)

| Check | Result |
|---|---|
| `go build ./...` (go 1.27.1) | pass |
| `go vet ./...` | pass |
| `go test ./...` (unit) | pass, 25 packages with tests |
| `apps/web`: `tsc -b` | pass |
| `apps/web`: `npm run lint` (oxlint) | pass, warnings only (`set-state-in-effect` x7, `only-export-components` x1) |
| `go test -tags=integration ./...` | **pass, 285 tests, 0 failures, but the 3 pg_dump-dependent backup tests were skipped in that run**; re-run with `pg_dump` 18 on PATH they pass (2026-10-21, Postgres 18 testcontainers on a remote Docker daemon; see docs/operations/testing-remote-docker.md) |
| Playwright / e2e / screenshots | **not run** — Postgres stack unavailable (needs Docker) |
| staticcheck / govulncheck | not run locally (CI job `go-static` runs them) |

## Reconciliation with README / TODO

- README says stages 0–10b-2 complete / Stage 11 hardening. TODO.md actually contains Stages 12–17
  (branding, contacts detail, CSV import UI, WhatsApp share, backup/restore, bulk e-Way) all shipped.
- Stage 11 is **still open** on: systematic IDOR/RBAC/mass-assignment/API-key-scope audit,
  adversarial concurrent-finalize races, Scenarios A–N in one run, PascalCase-vs-OpenAPI mismatch,
  DB pool default. This is the release gate named in the brief.
- `internal/modules/payments/` and `internal/modules/notifications/` contain a `.gitkeep`;
  notifications has real code, `payments/` is empty (payments tables live in migrations; code is elsewhere — verify in Phase 1).
- No `DESIGN.md`, `docs/roadmap-2.0.md`, `docs/reuse-matrix.md` existed before this phase.

## Audit table

Columns: ID · user outcome · existing location · backend · UI · security · tests · risk · deps · phase · acceptance evidence required.

### A. Identity / configuration
| ID | Outcome | Location | BE | UI | Sec | Tests | Risk | Deps | Ph | Evidence needed |
|---|---|---|---|---|---|---|---|---|---|---|
| A1 | Org/legal entity/branch/warehouse | `modules/organisation`, `CompanySwitcher` | Full | Full | RLS | integ (unverified today) | L | — | 1 | cross-tenant suite green |
| A2 | RBAC + staff | `modules/identity`, `platform/permissions`, `modules/staff` | Full | Partial | route+RLS | unit+integ | M | — | 1 | permission matrix test, view-only role scenario 11 |
| A3 | Audit log | `platform/audit`, `modules/audit` | Full | Partial | RLS | integ | L | — | 1 | audit entries on every mutation |
| A4 | API keys + scopes | `modules/integrations`, MCP | Partial | Stub (bare placeholder page) | scope path unreviewed | integ MCP only | **H** | — | 1/9 | general scope-bypass test |
| A5 | Custom fields/status rules | none | Missing | Missing | — | — | M | — | 2+ | — |
| A6 | Scheduled encrypted backup | `modules/backup` (+ `scheduled.go`, worker loop) | Full: scheduled, verified, retained (opt-in via env) | Manual UI only; no schedule UI | AEAD, 0600 files | unit + integ pass | M | worker | 1 | off-host copy and restore-to-clean-host drill still to do |
| A7 | Accountant read-only invite | none | Missing | Missing | — | — | M | A2 | 9 | — |
| A8 | Upload quotas | `platform/files` | Unknown | — | — | — | M | — | 1 | verify |

### B. Inventory
| ID | Outcome | Location | BE | UI | Sec | Tests | Risk | Ph |
|---|---|---|---|---|---|---|---|---|
| B1 | Catalogue, variants, barcodes, UoM | `modules/catalogue` | Full | Partial (no product edit/delete endpoints) | RLS | unit+integ | M | 1/4 |
| B2 | Append-only movement ledger, balances | `modules/inventory`, mig `stock_movements/stock_balances`, ADR 0002 | Full | Full | RLS, company scope fix 818a12a | unit+integ | L | 4 |
| B3 | Weighted-avg costing | `inventory/domain/costing.go` | Full (WAC only) | — | — | unit | M | 4 |
| B4 | Reservations | `stock_reservations` table, `QuantityReserved`, `ReservationStatus` in domain | **Partial — schema+domain present; verify service/API wiring and concurrency (Scenario 3)** | ? | ? | ? | **H** | 3/4 |
| B5 | Batches/serials/lots | `stock_batches`, `stock_serial_numbers`, `stock_cost_lots` | Partial (schema) | ? | RLS | ? | M | 4 |
| B6 | Transfers, adjustments | `stock_transfers`, `stock_adjustments` | Partial (verify in-transit stage) | Partial | company-scope fixed | integ | M | 4 |
| B7 | Stock count / reorder | `stock_policies` | Partial | Missing | — | — | M | 4 |
| B8 | Virtual locations, BOM/assembly | none | Missing | Missing | — | — | M | 4 |
| B9 | Import/export scaffolding | `platform/importer`, `platform/export`, `ImportPanel` | Partial (CSV/Excel; product/customer/supplier) | Full | — | unit | L | 1 |

### C/D. Sales, purchasing, accounting
| ID | Outcome | Location | BE | UI | Sec | Tests | Risk | Ph |
|---|---|---|---|---|---|---|---|---|
| C1 | Parties (customer/supplier) | `modules/contacts` | Full | Full | RLS | unit+integ | L | 2 |
| C2 | Invoices, credit notes, returns | `modules/sales`, `modules/returns` | Full | Full | company scope | unit+integ | M | 3 |
| C3 | Quote→Order→Delivery note chain | none found (only `sales_documents` types — verify types) | **Missing/verify** | Missing | — | — | **H** | 3 |
| C4 | Leads/opportunities/activities | none (`tasks` table exists) | Missing | Missing | — | — | M | 2 |
| C5 | Recurring invoices | none | Missing | Missing | — | — | M | 3 |
| C6 | Share links / WhatsApp share | `share_links`, `ShareLinksPanel` | Full | Full | token | integ | M | 7 |
| C7 | Receivable reminders | mig 0042, `ReceivablesPanel` | Partial (no consent/quiet-hours verified) | Partial | — | ? | M | 6/8 |
| D1 | Purchases (documents, lines) | `modules/purchases` | Full | Full | company scope | integ | M | 4 |
| D2 | Purchase tax / **ITC** | `accounting/app/service.go` | **Partial — GSTR-3B/ITC deliberately not built (TODO l.80)** | none | — | — | **H** | 4 |
| D3 | **COGS / gross profit** | `reporting/domain.GrossProfitRow` | **Approximate: uses current average_cost, not sale-time cost** | Labelled | — | integ | **H** | 4 |
| D4 | Accounting journals | `modules/accounting` (journals, journal_lines, fiscal_periods) | Full | Full | RLS | unit+integ | L | 1 |
| D5 | 3-way matching, landed cost, vendor statements | none | Missing | Missing | — | — | M | 4 |
| D6 | Expenses | `expense_attachments`, `pages/expenses` | Partial | Partial | — | ? | M | 6 |

### E. Field service — **all Missing** (no assets, tickets, work orders, SLA, dispatch, signatures, offline). Phase 5. Only reusable: `tasks`, `staff`, `staff_attendance`, `vehicles`.

### F. Payments / banking
| F1 | Receipts/payments recorded | tables `receipts`, `payments`, `reconciliations`, `bank_accounts`; `PaymentPanel` | Partial (manual) | Partial | — | ? | M | 6 |
| F2 | Allocations across invoices, overpayment | verify | ? | ? | — | — | **H** | 6 |
| F3 | Bank statement import/matching | none | Missing | Missing | — | — | M | 6 |
| F4 | Provider adapters (card/PayPal), webhooks in | none (outbound webhooks exist) | Missing | Missing | — | — | M | 6 |
| F5 | SEPA | none | **Blocked-reg** | — | — | — | — | 9 |

### G. Portal/shop — **Missing**. `share_links` is a token share of a single document, not a portal principal. Phase 7.

### H. Comms/automation/AI
| H1 | Outbox + worker | `platform/outbox`, `apps/worker` | Full (ADR 0005) | — | — | unit+integ | L | 8 |
| H2 | Outbound webhooks (HMAC, timestamp) | `modules/webhooks` | Full; **SSRF allow-listing unverified** | Stub | replay OK | unit | M | 9 |
| H3 | Notifications module | `modules/notifications` | Partial | ? | — | — | M | 8 |
| H4 | Rule engine, inbox | none | Missing | Missing | — | — | M | 8 |
| H5 | OCR | `tesseract.js` in web, `PurchaseScanReviewModal` | Partial (client-side OCR + human review modal) | Partial | — | — | M | 8 |
| H6 | AI/MCP | `modules/ai`, `apps/mcp` | Full read-only | — | scope-checked | integ | L | 8 |

### I/J. API, reports
| I1 | OpenAPI | `api/openapi/openapi.yaml` (2768 lines) | **Wrong for most modules: 14/15 emit PascalCase, spec says snake_case** | — | — | — | **H** | 1/9 |
| I2 | e-Invoice / e-Way (sandbox) | `modules/einvoice`, `ewaybill` | Full sandbox; creds from env not encrypted table | Full | — | unit+integ | M | blocked-cred |
| I3 | Desktop Tauri | `apps/desktop` | Partial (MSIX CI iterating) | — | — | CI | M | 10 |
| I4 | PWA/offline | none | Missing | Missing | — | — | M | 10 |
| J1 | Reports/dashboard | `modules/reporting`, `DashboardPage` | Partial (gross profit approximate; no GSTR-3B) | Full | RLS | unit+integ | M | 10 |
| J2 | Calendar | none | Missing | Missing | — | — | L | 5 |

## Findings that change sequencing

1. **Do not ship a margin/tax feature on D2/D3 until fixed** (brief rule). Fix needs `unit_cost` snapshot on SALE movements (migration expand/migrate/contract + backfill from the `stock_cost_lots`/movement history where derivable).
2. B4 reservations: verify whether sales finalize actually consumes reservations. Phase 3 depends on it.
3. API shape (I1) must be fixed with a **versioned** `/v2` DTO layer, not by retagging structs, because `apps/web` reads PascalCase today (rule 8: preserve public API).
