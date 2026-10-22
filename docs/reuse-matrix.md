# Rechvix 2.0 — Reuse matrix (Phase 0, preliminary)

**Status (2026-10-21): paths verified** against local clones of `Raktim94/mavri` and
`Raktim94/zulivio` (all listed files/dirs exist). Both are AGPL-3.0, identical to Rechvix, and are
owned by the same maintainer, so licensing does not block reuse. Still required: keep source
attribution in `NOTICE.md` for copied files, and review transitive npm/Go dependencies before
adding any. Decisions below remain provisional until each phase opens the specific source and
confirms the semantics fit Postgres/Go.

Decision key: Copy (verbatim, notice kept) · Adapt (port idea+UI, rewrite for Postgres/Go) · Reimplement · Do-not-use.

| # | Source (unverified) | Target | Decision | Justification | Compatibility risk | Validation |
|---|---|---|---|---|---|---|
| 1 | Mavri `web/src/pages/DocumentEditor.tsx` | `apps/web/src/pages/sales` composer | Adapt | document-first composer UX | state/model differs (SQLite API) | Playwright composer flow; a11y |
| 2 | Mavri `web/src/pages/PublicDocument.tsx`, `ShareDialog` | existing `ShareLinksPanel` | Reimplement | Rechvix already has share_links | none if skipped | — |
| 3 | Mavri `RecurringDialog`, workflow migrations | Phase 3 recurring engine | Adapt (concepts) | recurrence semantics | SQLite SQL must not be pasted | DST/recurrence tests |
| 4 | Mavri `RecordPayment`, `ReconcileCard`, idempotency migrations | Phase 6 allocations | Adapt (concepts) | idempotency keys, reconcile UX | schema differs | duplicate-webhook test |
| 5 | Mavri `templates.ts/css` | print templates | Adapt | print clarity | CSP: no inline styles | print/PDF test |
| 6 | Mavri `TaxGuidance` | tax hints | Do-not-use | no verified country rules (design gate) | legal claims | — |
| 7 | Zulivio `leads`, `assignments`, `assignment-rules` | Phase 2 CRM (Go) | Reimplement | process semantics only | NestJS/Prisma not allowed | lead→opp→quote e2e |
| 8 | Zulivio `attendance`, `employees` | field-tech availability | Reimplement | reuse concepts; Rechvix has `staff_attendance` | PII/location consent | permission tests |
| 9 | Zulivio `api-keys`, `audit` | Phase 9 | Do-not-use | Rechvix has own `api_keys`, `audit_log` | duplicates | — |
| 10 | Zulivio `backup`, `import-export` | existing `modules/backup`, `platform/importer` | Do-not-use | already present | duplicates | — |
| 11 | claude-skill-awwwards SKILL.md | design process | Adapt (guidance only) | no executable dependency installed | n/a | — |
| 12 | Competitor (STEL) features page | backlog only | Do-not-use content | trademarks/text not reusable | legal | — |

Rule: no Prisma/SQLite schema, no NestJS dependency, no copied competitor assets.

## 22 October verification (supersedes the stale Phase 0 missing-module assumptions)

- Existing Rechvix implementations inspected: `internal/modules/crm`, `recurring`, `fieldservice`, `esign`, `onlinepay`, `shop`, `staff`, and the sales composer. Their database acceptance tests are in `tests/integration/*_test.go`; baseline 201 tests passed on a fresh local PostgreSQL 18.6.
- Zulivio `assignments.service.ts` organisation/owner reference checks adapted to CRM Create/Update and audited Assign. Added `/crm/owners`, `/crm/opportunities/{id}/assign`, active owner UI and foreign-reference regression tests. No dependencies or migrations added. Attribution retained in NOTICE.md.
- CRM→quotation now carries the selected customer and QUOTATION type into the existing document workspace, rather than opening an empty tax invoice.
- Mavri document composer/share/recurring concepts already have corresponding Rechvix modules. No SQLite/Prisma/NestJS schema or duplicate money model is introduced.
- Still missing: Zulivio hierarchy-aware assignment and automatic rules, breaks/shifts; full Mavri template/workflow parity is not claimed. See audit-2026-10-22.md for evidence and limits.
