# Database

PostgreSQL 18. Every table below is created by a plain versioned SQL
migration under `migrations/` (`golang-migrate`, paired `.up.sql`/
`.down.sql`, numbered `0000`–`0030` as of this writing) — this document is
derived directly from those migrations, not a separate hand-maintained
model that can drift from them. If the two ever disagree, the migrations
are the source of truth; re-derive this file from them.

## Cross-cutting rules (apply to nearly every table below)

- **Tenant isolation, two layers.** Every tenant-scoped table carries an
  `organisation_id` column, filtered at the application layer *and* by
  PostgreSQL Row-Level Security — see `docs/architecture.md` and ADR
  reasoning throughout `docs/adr/`. A bug in one layer isn't a cross-tenant
  leak by itself.
- **Money is `NUMERIC`, never `float`.** Amounts use `NUMERIC(20,6)`,
  rates/quantities `NUMERIC(24,12)` per `docs/architecture.md`'s money
  design — see `internal/platform/money`.
- **UUIDv7 primary keys** throughout, time-sortable without a separate
  `created_at`-only sort key.
- **Migrator vs. runtime roles.** `billing_migrator` owns the schema and
  runs migrations; `billing_app` is the runtime role RLS actually applies
  to (migration `0029_runtime_role_grants`, `deploy/compose/postgres-init/
  01-create-runtime-role.sh`) — the app connecting as the schema owner
  would make every RLS policy a silent no-op (Stage 2's own lesson,
  re-caught and fixed for real in Stage 10a).

## Identity & access (`0001`–`0007`)

| Table | Purpose |
|---|---|
| `organisations`, `legal_entities`, `branches`, `warehouses` | The tenant hierarchy — one `organisations` row per customer, one-to-many down to the warehouse a stock movement or sale is actually scoped to. |
| `permissions`, `roles`, `role_permissions`, `user_roles` | RBAC catalog — permission codes are pre-seeded (e.g. `purchase.*`, `accounting.post`), roles are assignable, `user_roles` grants branch/warehouse-scoped role assignments. |
| `users`, `sessions` | Server-managed sessions (HttpOnly cookie, no client-held JWT). |
| `mfa_secrets`, `mfa_recovery_codes` | TOTP MFA + one-time recovery codes. |
| `password_reset_tokens` | Single-use, expiring. |
| `audit_log` | Append-only audit trail written by `internal/platform/audit`, used well beyond auth (e.g. e-Way Bill eligibility/portal steps in Stage 8c). |

## Catalogue, contacts, pricing (`0008`–`0011`)

| Table | Purpose |
|---|---|
| `products`, `product_variants`, `product_barcodes`, `categories`, `brands` | Product catalog; a product is sold as at least one variant. |
| `units_of_measure`, `unit_conversions` | Explicit, auditable conversions (e.g. BOX→PCS) — never an implicit ratio baked into application code. |
| `parties`, `party_addresses`, `party_tax_registrations` | Customers/suppliers/both, multiple address types, GSTIN etc. |
| `price_lists`, `price_list_items` | Multi-currency price fields via `internal/platform/money`. |

## Inventory & purchases (`0012`–`0014`)

| Table | Purpose |
|---|---|
| `stock_movements` | **Append-only** — the single source of truth for all stock change; never edited in place. |
| `stock_balances` | A materialized projection over `stock_movements`, maintained by the application layer (see `docs/adr/0002-stock-balance-maintenance.md` for why app-layer, not a DB trigger). |
| `stock_batches`, `stock_serial_numbers` | Batch/lot (modeled as one concept) and serial tracking, with expiry/manufacturing dates. |
| `stock_reservations`, `stock_policies` | Reservations for in-progress sales; low-stock/reorder/safety-stock thresholds. |
| `stock_adjustments`, `stock_transfers` | Manual adjustment and multi-warehouse transfer records. |
| `purchase_documents`, `purchase_document_lines`, `purchase_document_counters` | Purchase order → GRN → invoice → return → debit note, one document family (`document_type`-parameterized, same pattern as sales). |

## Tax (`0015`–`0016`)

| Table | Purpose |
|---|---|
| `gst_state_codes` | Reference data for place-of-supply logic. |
| `tax_documents`, `tax_lines`, `tax_components` | Generic tax model — not hardcoded `cgst`/`sgst` columns; `IndiaGSTEngine` is a plugin over this, so a second country's tax regime is additive, not a rewrite. |
| `tax_rate_master` | Versioned by `valid_from`/`valid_to` — a rate change never mutates a past calculation's snapshot. |

## Sales & documents (`0017`–`0019`)

| Table | Purpose |
|---|---|
| `legal_entities.gstin` (migration `0017`) | GSTIN added to the legal entity for GST-registered sellers. |
| `document_number_counters` | Concurrency-safe numbering, scoped to organisation/branch/document-type/financial-year. |
| `sales_documents`, `sales_document_lines` | Quotation/proforma/sales-order/challan/tax-invoice/POS-invoice/credit-debit-note/sales-return/recurring-invoice — one family via `document_type`, calculation snapshot immutable once finalized. |

## Accounting (`0020`–`0021`)

| Table | Purpose |
|---|---|
| `accounts` | Chart of accounts, idempotently seeded with a sane default set. |
| `journals`, `journal_lines` | Double-entry, enforced at three layers: app-level `validateBalanced`, a Postgres DEFERRED CONSTRAINT TRIGGER re-checking Σdebit=Σcredit at commit, and a BEFORE UPDATE/DELETE trigger making posted lines immutable — a trigger specifically because a table owner bypasses `GRANT`/`REVOKE` the same way it bypasses RLS. |
| `fiscal_periods` | Period locking; `accounting.override_locked_period` is a distinct permission from `accounting.post`. |
| `receipts`, `payments`, `bank_accounts`, `reconciliations` | Cash/bank side of the ledger. |

## Reporting (`0022`)

No new tables — `0022_reporting_indexes` adds the indexes the dashboard and
report queries need (see `docs/adr/0004-dashboard-query-design.md`: live
indexed queries, deliberately not a materialized table).

## Outbox & government integrations (`0023`–`0024`, `0028`, `0030`)

| Table | Purpose |
|---|---|
| `outbox_events` | Generic transactional outbox — one `INSERT` in the same transaction as the triggering write (e.g. sales finalize); `apps/worker` polls and processes it later, never inline with the HTTP request. |
| `einvoice_records`, `einvoice_provider_credentials` | IRN/ack/signed-QR/status/correlation id per record; credentials table exists but `apps/worker` still sources sandbox credentials from env vars, not yet reading+decrypting per-legal-entity from here (known Stage 8 follow-up). |
| `ewaybill_records`, `ewaybill_eligibility_rules`, `ewaybill_portal_schema_versions` | Status machine (DRAFT→…→GENERATED\|FAILED_\*, plus the Free-First-mode states added in Stage 8c); eligibility thresholds are versioned data, never a hardcoded Go literal. |
| `organisations` (migration `0030` adds a column) | `ewaybill_mode` (`FREE_PORTAL` default vs. `AUTOMATIC_API`) — Stage 8c's org-level switch. |

## Logistics (Stage 8c, part of `0028`)

| Table | Purpose |
|---|---|
| `vehicles`, `transporters` | Org-scoped masters for e-Way Bill transport details. |
| `customer_transport_preferences` | Recency-based smart-default resolver — remembers which vehicle/transporter a given customer usually ships with. |

## Integrations (`0025`–`0027`)

| Table | Purpose |
|---|---|
| `api_keys` | High-entropy, shown once, hashed, scoped (brief's 7-scope list), revocable, optional expiry/IP restriction. |
| `webhook_endpoints`, `webhook_deliveries` | HMAC-SHA256 signed events, two-hop outbox fan-out per endpoint, dead-letter after 8 attempts, `webhook_deliveries` as the visibility log. |
| `share_links` (migration `0027`, alongside notification scaffolding) | Signed, expiring, revocable share links — redemption is deliberately unauthenticated. |

## What's deliberately not here

- No `notifications` provider-credential tables — Stage 9 shipped
  interfaces (Email/SMS/WhatsApp) with a mock only; no adapter exists yet,
  so there's nothing to store credentials for.
- No file-storage/object-storage tables — `deploy/compose`'s `minio`
  profile is provisioned but genuinely unwired to any code.
- No `redis`-backed tables (by definition) — same status, provisioned but
  unwired.

## Migration 0044 — `stock_movements.base_unit_cost`

Nullable `numeric(20,6)`, expand-only. Set by `inventory/app.Service.RecordMovement` on every new
movement to the weighted-average cost per **base** unit in force at that instant (receipts use their
own base cost). Gross-profit reporting sums `base_quantity × base_unit_cost` over `SALE` (+) and
`SALE_RETURN` (−) movements of FINALIZED sales documents. Rows older than this migration stay NULL
and are reported as `approximate`; no backfill is done because the true average at that instant was
never stored. Rollback: `0044…down.sql` drops the column (report falls back to approximate for all rows).
