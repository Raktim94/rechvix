# Rechvix 2.0 — Roadmap

Status markers: `[ ]` untouched · `[~]` in progress · `[x]` complete **with test evidence** · `[!]` blocked.
Each phase is one mergeable milestone; its detailed file/migration/endpoint plan is written
at the start of that phase, not now. Nothing is marked done without acceptance tests.

## Revised phase order (differs from the brief on purpose)

1. **Phase 0** — discovery (this).
2. **Phase 1** — hardening + **financial-truth fixes** (move the COGS snapshot, API DTO versioning, scheduled backups here: they are release gates, and CRM/FSM must not build on wrong margins).
3. Phase 2 CRM/tasks → Phase 3 sales chain + reservations → Phase 4 inventory/purchasing/ITC (as brief).
4. **Payments (6) before FSM (5)**: allocations/idempotency are prerequisites for job invoicing and portal pay. FSM follows.
5. Phases 7–10 as in the brief (portal, comms/OCR, API/accountant, mobile/reporting/release).

## Phases

- [~] **0 Discovery/baseline** — docs produced; baseline: build/vet/unit/tsc/lint pass; integration, e2e, screenshots **not run** (R-01). Gate: operator unblocks Docker.
- [~] **1 Hardening + foundation** — done so far: service-layer permission audit (AST test `internal/modules/authz_audit_test.go`: 0 ungated methods beyond 9 allowlisted self-service/internal ones); pool sizing documented (R-08); scheduled verified encrypted backups with retention (worker, opt-in; unit + integration tests pass); integration baseline 285 pass/0 fail on remote Docker. Remaining: route×permission×tenant matrix test; API-key scope audit; off-host backup copy + clean-host restore drill; `/v2` DTO layer + OpenAPI fix; sale-cost snapshot migration (expand→backfill→contract); design token reconciliation.
- [ ] **2 CRM + tasks** — extend `parties`/`tasks`; leads, opportunities, activities, dedupe.
- [ ] **3 Sales chain + recurring** — quote→order→delivery→invoice; wire/verify reservations; recurring engine.
- [ ] **4 Inventory/purchasing/ITC** — warehouses/virtual locations, receipts, landed cost, ITC, historical COGS reports.
- [ ] **6 Payments/banking** (moved up) — allocations, bank import, provider adapter + webhook idempotency.
- [ ] **5 Field service** — assets, tickets, work orders, dispatch, offline sync, signatures.
- [ ] **7 Portal/shop** — separate principal and permission boundary.
- [ ] **8 Comms/rules/OCR**.
- [ ] **9 API/accountant/integrations** (SEPA blocked-reg).
- [ ] **10 Mobile/reporting/release**.

## Open decisions for the operator (only genuine blockers)
1. Grant this user Docker access (or confirm CI is the system of record for integration/e2e).
2. Tax jurisdiction scope for ITC beyond India GST.
3. Approval to apply the sale-cost snapshot migration to prod data (after tested backfill + rollback).
No deploys are authorized or performed.
