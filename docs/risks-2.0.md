# Rechvix 2.0 — Risk register (Phase 0)

Severity: H/M/L. Status: Open unless stated. Evidence refers to this audit (2026-10-21).

| ID | Risk | Sev | Evidence | Mitigation / owner phase |
|---|---|---|---|---|
| R-01 | RESOLVED for integration: ran 285/285 via remote Docker. e2e/Playwright still not run | M | `docker ps` → permission denied | Operator adds user to `docker` group (or provides CI run). Until then every Phase 1 gate relies on CI. **Needs operator action.** |
| R-02 | Stage 11 security review incomplete: IDOR beyond org scoping, RBAC bypass, mass assignment, general API-key scope bypass | H | TODO l.162-170 | Phase 1: systematic route×permission×tenant matrix test, generated from router |
| R-03 | Gross profit uses current average cost, not sale-time cost; misleading margin | H | `reporting/domain.GrossProfitRow` comment | Phase 4: snapshot `unit_cost` on SALE movements; expand/migrate/contract; keep "approximate" banner until backfill verified |
| R-04 | Purchase GST input credit / GSTR-3B not built; tax reports one-sided | H | TODO l.80 | Phase 4; tax claims need jurisdiction rules verified (India GST) |
| R-05 | OpenAPI documents snake_case, server emits PascalCase for 14/15 modules | H | TODO Stage 11 | Add versioned DTO layer (`/v2`), contract tests; do not break `apps/web` |
| R-06 | Scheduled backups now exist (verify+retention, opt-in); still no off-host copy or schedule UI | M | `backup/app/scheduled.go` | Document + drill restore on clean host; add off-host sync |
| R-07 | Reservation wiring/concurrency unverified (Scenario 3) | H | `stock_reservations` schema + domain exist; service use not confirmed | Phase 3/4: lock-based reserve test with concurrent clerks |
| R-08 | DB pool default 20 saturates at 20 concurrent readers (p95 45→731ms) | L | TODO Stage 11 | Already configurable; sizing guidance added to docs/operations/deployment.md (done) |
| R-09 | Webhook outbound SSRF defense unverified | M | `modules/webhooks` | Phase 9: allowlist/deny private ranges, test |
| R-10 | Go 1.27.1 pinned; toolchain download needed on fresh machines; dependabot branch pending | L | go.mod | Document in CONTRIBUTING |
| R-11 | e-Invoice sandbox creds from env, encrypted table unused | M | TODO Stage 8 | Blocked on credentials; wire table before production |
| R-12 | Reuse of Mavri/Zulivio code: licenses verified (AGPL-3.0, same owner); transitive deps and semantics still to review per phase | L | — | See `reuse-matrix.md`; attribute in NOTICE.md |
| R-13 | Design contract conflicts with shipped UI (fonts IBM Plex/Space Grotesk vs Inter; token names) | L | `apps/web/package.json` | Phase 1 reconciles; no restyle without tested nav stability |
| R-14 | Instagram reference unviewable | L | Not accessible | Treated as unverified; DESIGN.md governs |
| R-15 | Scope: brief spans ~10 products' worth of work | H | — | Phase gates; one feature dossier at a time; no "Coming soon" UIs |
| R-16 | Local WIP repo (`~/rechvix`) was freshly cloned; user's home git repo is unrelated | L | `git status` at `~` | Work only inside `~/rechvix` |
