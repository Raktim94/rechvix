# Rechvix 2.0 — Reuse matrix (Phase 0, preliminary)

**Status: paths below come from the brief and have NOT been verified against the sibling
repositories** (Mavri and Zulivio were not cloned or license-checked in Phase 0). Every row is
therefore a *plan*; the decision column is provisional until Phase-N work opens the source,
checks the license (and transitive deps) and preserves notices in `NOTICE.md`.
Rechvix is AGPL-3.0; sibling code is only reusable if its license is compatible.

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
