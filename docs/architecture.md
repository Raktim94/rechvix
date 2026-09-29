# Architecture — Stage 1 First Response

This is the response required by the project brief before any business-logic
code is written. It covers assumptions, architecture, module boundaries, the
domain model, tax/inventory/accounting design, government-integration design,
security architecture, API/MCP design, packaging, testing strategy, threat
model, and milestones — in that order. Nothing described here contradicts
anything else in this document; where a later section constrains an earlier
one (e.g. accounting constrains inventory costing), that's called out.

Companion doc: `docs/research.md` (verified version facts, open questions).

**License and distribution (decided 2026-09-02):** AGPL-3.0, matching this
project's sibling self-hosted product, `nodedr-pos`. Distribution model is
the same too — publicly downloadable, self-hosted via Docker/CasaOS, and
**no external network connection required for core operation** (own
Postgres, no mandatory cloud dependency, no telemetry). This is a real
constraint on every module, not just a README claim: nothing in `sales`,
`inventory`, `accounting`, `catalogue`, `contacts`, `pricing`, or `identity`
may require internet reachability to function. Only the modules that are
inherently external by nature — `einvoice`/`ewaybill` (a government API,
legally required for those specific documents when enabled),
`notifications` (WhatsApp/email), and an optional cloud storage backend —
ever make an outbound call, and only when the deployment operator
explicitly configures/enables them. §12 (Docker/CasaOS) already assumed
self-hosted-friendly packaging; this makes "offline-capable core" an
explicit, checked requirement rather than an implicit assumption.

---

## 1. Assumptions and unresolved questions

Full list lives in `docs/research.md`. Summary of what this architecture
assumes until told otherwise:

- India GST only for v1; generic tax model so a second country is additive,
  not a rewrite.
- Weighted-average costing only for v1; FIFO is a pluggable strategy added
  later without touching the movement ledger.
- e-Invoice/e-Way Bill wired to the NIC sandbox behind a provider interface
  until you confirm a real GSP/ASP account.
- MFA (TOTP) mandatory-enforceable for owners/admins/accountants from Stage 2.
- **No reverse proxy bundled by default** — see §12.
- Deployment target for first real test: your CasaOS host (debiancasa),
  consistent with your other self-hosted projects, pending your confirmation.

## 2. Proposed architecture

**Modular monolith**, not microservices — mandated by the brief and correct
for this domain: invoice finalization must atomically touch inventory,
ledger, tax, and numbering in one transaction. Splitting that across network
boundaries would trade correctness for an architecture style that isn't
needed at this scale.

Layering (strict, one-directional dependency):

```
HTTP/API (apps/server, chi router)
    ↓ DTOs in, DTOs out — no domain types cross this boundary raw
Application / use-case layer (internal/modules/*/app)
    ↓ orchestrates domain + repositories inside a transaction
Domain layer (internal/modules/*/domain)
    ↓ pure business rules, no I/O, no framework imports
Repository interfaces (internal/modules/*/domain, implemented in .../pg)
    ↓ sqlc-generated queries behind hand-written interfaces
PostgreSQL / external adapters (internal/platform/*, internal/modules/*/adapters)
```

Rules that keep this from rotting into a big ball of mud:

- Domain packages **never** import `net/http`, `chi`, or a specific external
  provider SDK. A domain type has no idea it's being served over REST.
- Cross-module calls go through the other module's **application-layer
  interface**, never its repository or domain package directly. `sales`
  calls `inventory.Reserve(ctx, ...)`, not `inventory.repo.LockRow(...)`.
- External government/notification APIs are called only from
  `internal/modules/einvoice|ewaybill|notifications` adapters, never from
  `sales` or `accounting` domain code directly (brief §2, §9 — "Domain logic
  MUST NOT directly call GST/e-Invoice/e-Way Bill HTTP APIs").
- `apps/server` is composition root: it wires concrete repos/adapters into
  application services and mounts HTTP handlers. It contains no business
  logic itself.
- `apps/worker` runs the outbox consumer, scheduled jobs (period-close
  reminders, low-stock checks, e-Invoice/e-Way Bill retry loops), and nothing
  request-scoped.

## 3. Module diagram

```
                          ┌─────────────────────────┐
                          │   apps/web (React/TS)    │
                          │   apps/desktop (Tauri2)  │──thin shell, no logic
                          └────────────┬─────────────┘
                                       │ HTTPS / JSON (REST v1)
                          ┌────────────▼─────────────┐
                          │   apps/server (chi/Go)    │
                          │   AuthN, routing, OpenAPI │
                          └────────────┬─────────────┘
             ┌─────────────────────────┼───────────────────────────┐
             ▼                         ▼                           ▼
   ┌───────────────────┐   ┌────────────────────┐       ┌────────────────────┐
   │ identity/org/RBAC   │   │ sales / purchases   │       │ reporting / MCP     │
   │ locations/contacts  │   │ inventory / returns │       │ (read-mostly)       │
   └─────────┬───────────┘   │ pricing / payments   │      └──────────┬─────────┘
             │                └─────────┬───────────┘                 │
             │                          │                             │
             │               ┌──────────▼───────────┐                 │
             │               │  taxation (interface)  │                │
             │               │   -> gstindia          │                │
             │               │   -> (future countries)│                │
             │               └──────────┬───────────┘                 │
             │                          │                             │
             │               ┌──────────▼───────────┐                 │
             └──────────────▶│    accounting          │◀───────────────┘
                              │ double-entry journal   │
                              └──────────┬─────────────┘
                                         │
                     ┌───────────────────┼───────────────────────┐
                     ▼                   ▼                       ▼
           ┌──────────────────┐ ┌────────────────┐   ┌──────────────────────┐
           │ outbox (Postgres) │ │ einvoice/ewaybill│  │ notifications/webhooks│
           │  → apps/worker     │ │  provider adapters│  │  email/SMS/WhatsApp   │
           └──────────────────┘ └────────────────┘   └──────────────────────┘
                     ▲
                     │
              internal/platform/*  (auth, crypto, files, jobs, http, obs, permissions)
              — used by every module above, owned by none
```

`audit` sits orthogonally: every module's application layer writes through a
single `audit.Recorder` interface (platform-level), so sensitive-action
logging (brief §30) is enforced by code review convention plus a lint rule
(a use-case handler that mutates state and never calls `Recorder.Record` is
flagged), not by trusting every author to remember.

## 4. Database / domain model overview

Tenancy chain (brief §3), enforced structurally, not by convention:

```
Platform
  └─ Organisation (organisations)
      └─ LegalEntity (legal_entities)          — one or more GST registrations
          └─ Branch (branches)                  — invoice numbering scope
              └─ Warehouse (warehouses)         — physical stock scope
```

Every tenant-owned table carries `organisation_id`. Tables scoped tighter
also carry `legal_entity_id` / `branch_id` / `warehouse_id` as applicable —
never inferred by joining up the hierarchy at query time in a hot path.

Core entity groups (full ERD in `docs/architecture/` as this evolves past
this first pass; this is the shape, not the final DDL — DDL is Stage 2):

- **Identity/RBAC**: `users`, `roles`, `permissions`, `role_permissions`,
  `user_roles` (scoped to organisation/legal_entity/branch/warehouse),
  `sessions`, `mfa_secrets`, `mfa_recovery_codes`, `api_keys`.
- **Organisation**: `organisations`, `legal_entities`, `branches`,
  `warehouses`, `document_number_sequences`.
- **Contacts**: `parties` (customer/supplier/both), `party_addresses`,
  `party_tax_registrations`.
- **Catalogue**: `products`, `product_variants`, `skus`, `barcodes`,
  `categories`, `brands`, `units_of_measure`, `unit_conversions`,
  `price_lists`, `price_list_items`.
- **Inventory**: `stock_movements` (append-only, the source of truth),
  `stock_balances` (materialized, rebuildable from movements),
  `stock_reservations`, `batches`, `lots`, `serial_numbers`,
  `stock_adjustments`, `stock_transfers`.
- **Sales/Purchases**: `sales_documents` + `sales_document_lines` (one table
  family parameterized by `document_type` — quotation, proforma, order,
  challan, invoice, credit/debit note, POS — sharing header shape per brief
  §5; same pattern mirrored for `purchase_documents`).
- **Taxation**: `tax_documents`, `tax_lines`, `tax_components` (generic —
  brief §18, never `cgst`/`sgst` columns on the invoice itself),
  `tax_rate_master` (with `valid_from`/`valid_to`), `hsn_sac_codes`.
- **Accounting**: `chart_of_accounts`, `journals`, `journal_lines`,
  `fiscal_periods`, `payments`, `receipts`, `bank_accounts`,
  `reconciliations`.
- **Government integration**: `einvoice_records`, `ewaybill_records` (with
  the `ship_to_gstin`/`CLOSED` fields from `docs/research.md`), each holding
  provider, request hash, response payload, status, correlation id.
- **Platform**: `audit_log`, `outbox_events`, `webhook_endpoints`,
  `webhook_deliveries`, `idempotency_keys`, `files`.

All primary keys: **UUIDv7** (brief §4) — time-ordered, so btree locality on
`id` stays good even though it's random-looking, without leaking a sequential
count the way `bigserial` would over a public API. Business-facing numbers
(`INV/2026-27/000133`) are separate columns generated by
`document_number_sequences`, never derived from the primary key.

## 5. Tax calculation architecture

One authoritative library: `internal/modules/taxation` (generic engine) +
`internal/modules/gstindia` (India plugin). Nothing outside these two
packages contains a tax percentage literal or a rounding call on a monetary
value.

```go
// internal/modules/taxation/domain/engine.go
type TaxEngine interface {
    Calculate(ctx context.Context, in TaxCalculationInput) (TaxCalculationResult, error)
}

type TaxCalculationInput struct {
    Lines        []TaxableLine
    SupplyPlace  PlaceOfSupply
    DocumentDate time.Time // pins which tax_rate_master row is valid
}

type TaxCalculationResult struct {
    Lines []TaxLineResult // each carries []TaxComponent{Type, Rate, Amount}
}
```

`IndiaGSTEngine` implements `TaxEngine`; a future `VATEngine` or
country-specific engine implements the same interface. Which engine runs is
selected by the legal entity's country/tax-regime configuration, resolved
once per request, not sprinkled through call sites.

**Money type**: a dedicated `internal/platform/money` package wrapping
`decimal.Decimal` (or an equivalent arbitrary-precision decimal — never
`float32`/`float64` for anything monetary, per brief §6/§56/Rule 3).
Intermediate tax-inclusive calculations run at full precision; rounding only
happens at the single, documented point the tax engine designates as final —
never inside an intermediate step, never independently in the frontend.
Frontend previews the same formula for UX responsiveness, but the **server
recalculates and is authoritative** on save (brief §56).

Golden tests (brief §67) pin the exact fixture from the brief:
₹90 gross @ 18% inclusive → taxable ≈ 76.271186…, tax ≈ 13.728813…, and cover
0/3/5/12/18/28/40% plus CGST+SGST split (intra-state) vs IGST (inter-state).
These tests are written before the engine's rounding logic is finalized, not
after — they define correctness, not just check it.

## 6. Inventory architecture

Stock balance is **never** a directly-editable number. `stock_movements` is
an append-only ledger (OPENING, PURCHASE_RECEIPT, SALE, TRANSFER_IN/OUT,
ADJUSTMENT_IN/OUT, DAMAGE, EXPIRY, etc. — full list per brief §11).
`stock_balances` is a materialized projection maintained transactionally in
the same DB transaction as the movement insert (via trigger or explicit
application-layer update inside the same transaction — decided in Stage 2
against real write-volume numbers, not guessed now). Either way, balance is
always *derivable* by replaying movements, which is what backup-restore
reconciliation (Scenario N) checks.

Unit conversion (`BOX → 25 PCS`) is explicit rows in `unit_conversions`,
referenced by both catalogue and stock movement lines, so a movement always
records both the transacted unit and the base stock unit with the conversion
factor applied at that point in time (auditable — a later change to the
conversion ratio doesn't reinterpret historical movements).

Costing: `internal/modules/inventory/domain/costing` defines a
`CostingStrategy` interface; `WeightedAverageCostingStrategy` is the only
implementation in v1. Adding FIFO later means adding a second
implementation and a per-product/per-warehouse config flag — not touching
the movement ledger schema.

## 7. Double-entry accounting architecture

`accounting` is the only module allowed to write `journal_lines`. Every
other module that has a financial effect (sales finalizing an invoice,
purchases receiving goods, payments being recorded) calls
`accounting.Post(ctx, JournalRequest)` — it does not construct journal rows
itself.

```go
type JournalRequest struct {
    OrganisationID uuid.UUID
    SourceType     string // "sales_invoice", "purchase_invoice", "payment", ...
    SourceID       uuid.UUID
    Lines          []JournalLineRequest // each: AccountID, Debit or Credit, Amount
    PostedAt       time.Time
}
```

Invariant enforced at three layers, not just one (brief Rule 19 — prefer DB
constraints for invariants):

1. **Application layer**: `accounting.Post` sums debits/credits before
   attempting the insert and rejects a mismatch with a domain error.
2. **Database CHECK-adjacent enforcement**: a deferred constraint trigger on
   `journal_lines` that, at end of the `journals` row's transaction,
   verifies `SUM(debit) = SUM(credit)` per `journal_id` and raises if not —
   so even a future direct-SQL bug (migration script, manual fix) can't
   silently post an unbalanced journal.
3. **No UPDATE path** on posted `journal_lines` at all — the table only
   allows INSERT once `journals.status = 'POSTED'`; correction is always a
   new reversing journal referencing the original (brief §14 — "Do not
   directly edit posted journals").

## 8. India GST architecture

`gstindia` is a plugin implementing `TaxEngine`, plus the India-specific
reporting/master-data tables (`hsn_sac_codes`, `tax_rate_master` rows scoped
to `country = 'IN'`). It knows about GSTIN structure, state codes, place of
supply, CGST/SGST vs IGST selection, cess, reverse charge markers, and
B2B/B2C/export/SEZ classification — none of that logic exists in generic
`sales` or `taxation` code.

Tax rate validity windows (`valid_from`/`valid_to`) mean a finalized
invoice's tax snapshot references the *rate row that was valid on the
invoice date*, resolved once at finalization and stored in
`tax_documents`/`tax_lines` — never recomputed against today's master later
(brief §7 — "Never recalculate an old finalized invoice using today's GST
master"; also brief §55, calculation snapshots).

## 9. e-Invoice / e-Way Bill adapter architecture

```go
type EInvoiceProvider interface {
    Authenticate(ctx context.Context) error
    GenerateIRN(ctx context.Context, req IRNRequest) (IRNResponse, error)
    GetIRN(ctx context.Context, irn string) (IRNResponse, error)
    GetIRNByDocument(ctx context.Context, docType, docNo string, docDate time.Time) (IRNResponse, error)
    CancelIRN(ctx context.Context, irn, reason string) error
    GenerateEWayBillByIRN(ctx context.Context, irn string, transport TransportDetails) (EWBResponse, error)
    CancelEWayBill(ctx context.Context, ewbNo, reason string) error
    GetEWayBillByIRN(ctx context.Context, irn string) (EWBResponse, error)
    GetGSTIN(ctx context.Context, gstin string) (GSTINInfo, error)
    HealthCheck(ctx context.Context) error
}
```

Versioned by directory (`internal/modules/einvoice/v1`, `.../vNext`) so a
government schema change (like the 2026-08-01 Ship-to-GSTIN change
documented in `docs/research.md`) ships as a new adapter version, tested
against fixtures, and cut over deliberately — not a silent field-by-field
patch to a shared struct that risks breaking the previous behavior.

State machine (brief §10) is explicit and persisted, not inferred:
`DRAFT → QUEUED → SUBMITTING → GENERATED | FAILED_RETRYABLE | FAILED_FINAL`,
plus `CANCEL_PENDING → CANCELLED`, plus the newly-relevant `CLOSED` for
e-Way Bill voluntary closure. Submission is **not** in the same DB
transaction as invoice finalization — invoice finalize writes an
`outbox_events` row; `apps/worker` picks it up and drives the state machine
with retries/backoff. A government API outage cannot block or corrupt a
sale (brief Rule 12, Scenario L). Idempotency keys prevent duplicate IRN/EWB
generation on retry or double-click (brief §33, Scenario H).

Credentials (client ID/secret, GSP tokens) live in
`internal/platform/crypto`-encrypted columns, decrypted only inside the
adapter process boundary, never returned through any API response, never
logged (brief §9, §60).

## 9b. Free-first e-Way Bill portal workflow (added 2026-09-03)

**Product principle:** "One invoice. One workflow. Zero duplicate data
entry." — and critically, **API access to e-Way Bill generation must be
optional, never required.** A small business paying nothing for a paid
GSP/API integration must still get a professional, low-friction e-Way Bill
experience. This directly extends §9 above; it does not replace it — the
`EInvoiceProvider`/`EWayBillProvider` interfaces built in Stage 8 remain the
`AUTOMATIC_API` path. This section adds a second, **default**, production
mode that needs no paid API at all.

**Two production modes** (a per-organisation setting, `FREE_PORTAL` is the
default):
- `FREE_PORTAL` — the application prepares the exact upload file the
  official government e-Way Bill portal's bulk-upload feature accepts,
  hands the user a one-click path to the portal, and later imports the
  government's own result (file, PDF, or manual entry) back onto the
  invoice. No API credentials, no paid integration, ever required for this
  path.
- `AUTOMATIC_API` — optional; the existing Stage 8 `EWayBillProvider` flow.
  If it fails at runtime, the user is offered `FREE_PORTAL` as an immediate
  fallback using the *same* already-prepared canonical data — never a
  re-entry of the invoice.
- `MOCK`/`SANDBOX` remain internal, test/dev-only, as built in Stage 8.

**Canonical model, not two parallel schemas.** A single
`CanonicalEWayBill` (supplier, recipient, document, dispatch, delivery,
items, tax, values, transport) is built once from a **finalized invoice's
immutable tax/inventory snapshot** (never from live, possibly-since-edited
product/customer master data — the same immutability principle as §55/§7:
if the prepared file is ever regenerated later, e.g. because a user's local
copy was lost, it must reproduce byte-identical figures from the original
snapshot, not silently drift because someone edited the customer's address
since). Format-specific mappers (`PortalExportProvider`, the existing API
provider, sandbox, mock) all consume the same canonical struct — the
invoice/domain layer never depends on which mode is active.

**Eligibility is a versioned rule engine, not a hardcoded threshold.**
`EvaluateEWayBillRequirement(invoice) -> NOT_REQUIRED | READY |
NEEDS_INFORMATION | REQUIRED`. The ₹50,000 figure (or whatever the current
rule is) lives in versioned rule data with an effective-date range — same
pattern as `tax_rate_master`'s `valid_from`/`valid_to` — never a bare `if
amount > 50000` literal in Go source, because this is exactly the kind of
government-set threshold brief Rule 13 warns against hardcoding.

**Extended status model** (supersedes/extends Stage 8's DRAFT→...→CLOSED
machine specifically for the free-portal path): `NOT_REQUIRED`,
`NEEDS_INFORMATION`, `READY`, `PREPARING`, `PORTAL_FILE_READY`,
`AWAITING_PORTAL_COMPLETION`, `GENERATED`, `CANCELLED`, `EXPIRED`, plus the
API-mode-specific `API_QUEUED`/`API_GENERATING`/`API_NEEDS_ATTENTION`. Still
one persisted, explicit column — not inferred.

**Portal file generation is versioned exactly like the tax/GST schema is**
(`docs/research.md`'s point about GSTN changing schemas with short notice
applies equally to the portal's bulk-upload format): `ewaybill/portal/
{schema,validators,mappers,versions}/`, a `PortalSchemaVersion` row with
`effective_from`/`effective_until`, so a portal format change is a new
mapper version, not a rewrite of invoice logic. Enforce the portal's actual
file-size limit; split into multiple numbered batch files
(`EWB-BATCH-001.json`, `...-002.json`, ...) if a bulk export would exceed
it. Filenames are always human-recognizable
(`EWB-<invoice-number>-<date>.json`), never an opaque hash.

**Hard security constraints (non-negotiable, brief-equivalent Rules
apply):**
- Never store a government-portal username/password.
- Never automate government portal login — no Selenium/Playwright/headless
  browser driving the authenticated production portal, no CAPTCHA/OTP/2FA
  bypass, no session-cookie copying. The user always authenticates to the
  real government site themselves, in their own browser tab, opened by us
  but never proxied or wrapped.
- The portal URL is backend-configured and allowlisted
  (`GovernmentPortalService.GetOfficialEWayBillPortalURL()`), never a
  user-editable arbitrary URL — prevents phishing via a spoofed "government
  portal" link.
- Never claim or promise a permanent authenticated deep-link into a
  protected government page; degrade gracefully to "open the portal,
  instruct the user where to navigate" if no stable deep-link is documented
  as officially supported.

**Imported results are verified before linking**, not trusted blindly: an
imported government result (file or PDF) must match the target invoice's
number/date/GSTIN/document type before being attached; a mismatch blocks
auto-linking and surfaces "this e-Way Bill appears to belong to another
invoice" rather than silently attaching wrong data to the wrong invoice.

**New master data**: `vehicles` and `transporters` (org-scoped, RLS-
protected, standard CRUD), plus "smart defaults" (recently-used vehicle,
customer-preferred transporter/vehicle) resolved as a plain preference
lookup, not a new architectural mechanism.

**Frontend implication, explicitly deferred, not forgotten:** the detailed
UX this section's source spec describes (an EWB card on the invoice screen,
a settings page, a "portal assistant" screen, a pending-tasks center, a
bulk-preparation queue, desktop file-location handling) is real product
scope but cannot be built yet — `apps/web` does not exist as of this
writing (Stage 10). The backend pieces above (canonical model, eligibility
engine, portal file generation, vehicle/transporter masters, import/verify,
audit) are buildable now and are tracked as their own stage in
`docs/TODO.md`; the UI work is tracked under Stage 10/11 so it isn't lost
between now and when the web app exists to hang it on.

## 10. Authentication / security architecture

- **Password storage**: Argon2id. Starting profile: memory ≥ 19 MiB,
  iterations ≥ 2, parallelism ≥ 1 (brief's stated floor) — actually
  benchmarked against target hardware in Stage 2 and raised if the hardware
  budget allows, documented in an ADR with the measured login latency at the
  chosen parameters.
- **Sessions**: server-managed, opaque session ID in a cookie that is
  `Secure; HttpOnly; SameSite=Lax` (or `Strict` for the login endpoint
  itself). Session ID rotates on login, password change, and permission
  escalation. Idle + absolute timeout both enforced server-side. Session
  list + remote revoke per user (brief §29).
- **RBAC**: permissions like `sales.finalize`, `inventory.adjust` checked in
  `internal/platform/permissions`, called from every application-layer
  handler — never inferred from "the UI didn't show the button" (brief
  Rule 6, Scenario F). Role/permission scope can be pinned to organisation,
  legal entity, branch, or warehouse.
- **MFA**: TOTP, recovery codes stored hashed, enforceable per-role,
  step-up re-auth required before sensitive actions (cancel invoice, change
  permissions, reveal API key) even within an active session.
- **Tenant isolation**: `organisation_id` is taken from the authenticated
  session/API-key context, never from a request body/query param, at the
  application layer — and enforced a second time with PostgreSQL Row-Level
  Security policies as defense-in-depth (brief §3, Scenario G), so a bug in
  one layer doesn't equal a cross-tenant leak.
- **Zulivio inspection**: pending, tracked in `docs/research.md` — will
  produce an ADR on what's reused vs. deliberately not reused before
  `identity` is implemented.

## 11. API / MCP architecture

REST, versioned at `/api/v1/`, described by OpenAPI 3.1
(`api/openapi/`, generated from Go types via a schema-first or code-first
tool decided in Stage 2 — either way OpenAPI is the source of truth for
client generation, not hand-maintained docs). Consistent error envelope per
brief §35. Cursor-based pagination for anything unbounded. Explicit request
size limits and rate limits (brief §63) enforced in
`internal/platform/http` middleware, tenant-aware.

**API keys** (brief §36): high-entropy, shown once, stored hashed, scoped
(`invoices:read`, `customers:write`, etc.), revocable, optional expiry/IP
restriction. Never issued with a wildcard scope by default.

**MCP server** (brief §39-40): a separate thin process
(`internal/modules/ai` + a small MCP transport binary) that calls the same
application-layer services as the REST API — it has no direct SQL access,
ever. Default tool set is **read-only**: `search_products`, `get_product`,
`get_inventory`, `get_customer`, `get_invoice`, `list_invoices`,
`get_sales_summary`, `get_receivables`, `get_stock_summary`,
`get_gst_summary`. Every MCP call carries the same auth/organisation/
permission/audit/rate-limit path as a REST call — an MCP session is treated
as an untrusted external actor whose "intent" (however phrased by a
prompt) never substitutes for a permission check. Write tools
(`create_invoice_draft`, `create_customer`, `create_quote`) are a Stage 9
addition behind an explicit policy flag; finalize/cancel/adjust-stock/
post-journal/change-permissions/submit-tax-document remain off the MCP
surface entirely unless you later decide otherwise in writing (brief §39,
Scenario M).

## 12. Docker / CasaOS plan

Two runtime images: `billing-server` (Go binary, embeds the built React SPA
so a self-hoster runs one container, not two) and `billing-worker`. Both
multi-stage builds, non-root user, minimal base (distroless or scratch +
CA certs), read-only root filesystem where the app doesn't need local
writes, `HEALTHCHECK` hitting `/health/live` and `/health/ready`, graceful
shutdown on SIGTERM.

**On the reverse proxy — agreeing with your call, with the reasoning
written down**: the default `docker-compose.yml` and CasaOS manifest ship
**app + postgres only**, no bundled Caddy/Traefik/nginx. Reasons:

- CasaOS already provides its own reverse proxy / port mapping at the host
  level (`x-casaos.port_map`) — bundling a second one would fight it, which
  is exactly the KinetiRx/CasaOS friction pattern already in your project
  memory (root-owned app dirs, don't add another moving part CasaOS already
  owns).
- A cloud/VPS operator running this behind their own existing
  Caddy/Traefik/nginx/ALB (which most already have, per your other
  projects — acweb, KinetiRx, RustFS all sit behind existing infra) doesn't
  want a second proxy fighting theirs for port 443.
- Go's `net/http` server terminates plain HTTP inside the private Docker
  network fine; TLS termination is the host/operator's job, documented
  explicitly in `docs/operations/deployment.md`, not silently assumed away.
- If a given deployment genuinely has nothing in front of it (bare VPS, no
  existing proxy), that's covered by an **optional** Compose profile
  (`--profile reverse-proxy`) documented but off by default — not a
  default dependency every install pays for.

Optional Compose profiles: `redis` (cache/session store for horizontal
scaling), `minio` (S3-compatible object storage for self-hosters without
real S3), `reverse-proxy` (opt-in, per above). `.env.example` provided,
real secrets never committed.

CasaOS manifest (`casaos/docker-compose.yml`) carries the full
`x-casaos` block (id, main, index, port_map, scheme, icon, title, tagline,
description, category, architectures, version) targeting `amd64` + `arm64`
— consistent with the nodedr-pos/OrderRestro CasaOS submissions already in
your project history. `docker compose config -q` runs in CI against both
the generic compose file and the CasaOS one.

## 13. Windows / Microsoft Store plan

`apps/desktop` wraps the same `apps/web` build in Tauri 2. The Tauri shell
is intentionally thin: window chrome, native menu, local file-save dialogs,
maybe a barcode-scanner USB-HID bridge — **zero** tax/inventory/accounting/
permission logic duplicated into Rust or JS; the desktop app talks to the
same Go server. As of v0.2.0.0 the MSIX build bundles that Go server (a
Windows cross-compile of `apps/server`) plus a portable Postgres directly
in the package, and the shell starts both as local child processes on
launch (`apps/desktop/src-tauri/src/backend.rs`) instead of asking the
user which server to connect to — so the app works fully offline with
zero setup, landing first-time users straight on the existing
account-creation ("Set up your business") screen. This also structurally
resolves a real Store certification rejection (policy 10.3.1, "App Is
Testable") that a purely thin-client build hit: a reviewer has no test
account or server to connect to unless the app can stand entirely on its
own. See `apps/desktop/README.md` and `apps/desktop/TESTING.md` for the
packaging/testing details.

## 14. Testing strategy

Mirrors brief §65-71 directly, not reinvented:

- **Unit**: tax engine, money/rounding, discounts, currency conversion,
  invoice state machine, permission checks, number-sequence allocation,
  costing, journal balance invariant, password hashing.
- **Property-based**: "for any valid journal, `Σdebit == Σcredit`"; "for
  inclusive tax, `taxable + tax == total` within rounding tolerance"; "stock
  movement reconciliation == stock balance."
- **Integration** (Testcontainers + real Postgres 18): invoice finalization
  under concurrency, stock reservation races, ledger posting, tenant
  isolation (Scenario G), idempotency (Scenario H), outbox delivery.
- **API tests**: authN/authZ, validation, pagination, rate limits, API key
  scoping, webhook HMAC signatures.
- **E2E (Playwright)**: login → customer → product → purchase stock → GST
  sale (inclusive) → print → payment → ledger → sales return → transfer →
  GST report — the exact flow list in the brief, run against a real
  ephemeral stack, not mocks.
- **Golden tax fixtures**: the ₹90/18%-inclusive fixture, 0/3/5/12/18/28/40%
  rates, intra-state CGST+SGST split, inter-state IGST — checked into
  `tests/fixtures`, referenced by both unit and integration suites so a
  rounding regression fails fast in CI, not in a customer's invoice.
- **Government schema fixtures**: e-Invoice/e-Way Bill request/response
  fixtures against the NIC sandbox schema (IRN, ack, QR payload, EWB
  number/validity/Part-A/Part-B) — sandbox/mock only, **never** production
  credentials in CI (brief Rule 17).
- **Concurrency scenarios** (brief §66): simulated explicitly as integration
  tests, not left to hope — two operators selling last unit, duplicate
  finalize, duplicate number allocation, double-click payment, duplicate
  webhook delivery.
- **Security/static analysis**: `go vet`, `staticcheck`, `govulncheck`,
  dependency/secret/container scanning, frontend audit, and the explicit
  attack classes in brief §69 (IDOR, cross-tenant, RBAC bypass, CSRF, XSS,
  SQLi, mass assignment, rate-limit bypass, session fixation, webhook
  replay, API-key scope bypass, MCP permission bypass) as a named test
  checklist, not a vague "we did a security pass."
- **Performance**: benchmarked against a 100k-product / 1M-invoice-line
  seeded dataset on a schedule, `EXPLAIN ANALYZE`-driven indexing — not on
  every PR (too slow) but as a required periodic CI job.

## 15. Threat model (summary)

| Actor | Threat | Mitigation |
|---|---|---|
| Authenticated user, wrong org | IDOR / cross-tenant read via guessed/enumerated ID | App-layer org-scoped queries + Postgres RLS as defense-in-depth; every repo query takes `organisation_id` as a mandatory parameter, not optional |
| Authenticated user, insufficient role | Client-side-only permission bypass (hidden button, replayed request) | Server-side permission check on every mutating handler, independent of what the UI rendered |
| External attacker | Credential stuffing / brute force login | Argon2id, login rate limiting + backoff, generic "invalid credentials" (no user enumeration), MFA for privileged roles |
| External attacker | Stolen API key used outside expected scope | Scoped keys, optional IP restriction, last-used tracking, instant revocation |
| Malicious/buggy webhook consumer or replayer | Forged or replayed webhook delivery | HMAC-SHA256 signature + timestamp + event ID, consumer-side dedup contract documented |
| Compromised MCP/AI client or prompt injection | Attempt to exfiltrate cross-tenant data, secrets, or trigger a financial mutation via crafted natural-language input | MCP calls go through the same authz path as REST; read-only tool surface by default; finalize/cancel/post/permission-change tools withheld entirely pending explicit policy; tool outputs never include credentials |
| Insider (privileged user) | Silent edit of a posted journal / finalized invoice / historical stock movement to hide fraud or error | No UPDATE path on posted financial rows at the DB grant level; correction only via reversal/credit-note/adjustment, all attributed and audit-logged |
| Any actor | Double-submit causing duplicate invoice number, duplicate payment, duplicate government submission | Idempotency keys persisted server-side; unique constraints as the final backstop, not the only line of defense |
| Ops/infra | Backup exists but doesn't actually restore | Scheduled restore-verification job (brief §42), not just "backups run" |

Full threat model (STRIDE per module, attacker/asset table) grows in
`docs/security/` as modules are implemented — this table is the Stage 1
shape, not the final artifact.

## 16. Development milestones

Following the brief's staged process exactly (§77) — each stage ends with
passing tests before the next starts, per the Definition of Done (§78):

| Stage | Scope | Gate to proceed |
|---|---|---|
| 0 (this doc + research.md) | Research, version verification | You review and answer open questions |
| 1 (this doc) | Architecture, ERD shape, threat model, ADRs | You confirm no contradictions before Stage 2 code starts |
| 2 | Config, DB, migrations, logging, observability, auth, RBAC, audit, org/branch/warehouse | Unit + integration tests pass |
| 3 | Catalogue, units, contacts, GST fields | Tests pass |
| 4 | Purchases, stock movement, warehouses, lots, transfers, adjustments | Tests + concurrency scenarios (Scenario D) pass |
| 5 | Sales/GST invoice, inclusive/exclusive tax, CGST/SGST/IGST, HSN, printing | Golden tax fixtures + Scenarios A/B/C pass |
| 6 | Accounting: journals, ledgers, payments, receivables/payables | Journal-balance property tests + Scenario E pass |
| 7 | Reports (sales/inventory/accounting/GST) + dashboard | Query performance checked against seeded scale |
| 8 | e-Invoice/e-Way Bill adapters, sandbox only | Fixture tests + Scenarios J/K pass, no prod credentials in CI |
| 9 | WhatsApp/email, API keys, webhooks, MCP (read-only) | Scenario L (async resilience) + Scenario M (MCP scope) pass |
| 10 | Docker, Compose, CasaOS, Tauri, Windows packaging prep | `docker compose config -q` in CI, CasaOS manifest validated |
| 11 | Hardening: security review, load test, concurrency test, a11y review, backup/restore test, migration test | All Scenario A-N pass end-to-end |

I will not start Stage 2 implementation until you've confirmed this
document and the open questions in `docs/research.md`.

## 17. Exact repository structure

Created (empty skeleton, `.gitkeep` placeholders) at `~/rechvix`:

```
apps/{server,worker,web,desktop}
internal/platform/{auth,database,cache,crypto,files,http,jobs,logging,observability,permissions,validation}
internal/modules/{identity,organisation,locations,contacts,catalogue,pricing,inventory,purchases,sales,returns,payments,accounting,taxation,gstindia,einvoice,ewaybill,reporting,notifications,integrations,webhooks,ai,audit}
api/openapi
migrations
deploy/{docker,compose,casaos}
docs/{architecture,adr,api,operations,security}
tests/{integration,e2e,fixtures}
```

Matches the brief's proposed layout exactly (brief §2), since it's already
well-suited to the layering described in §2 above — each `internal/modules/*`
package will get its own `domain/`, `app/`, `pg/`, and (where applicable)
`adapters/` subdirectories once Stage 2 starts.
