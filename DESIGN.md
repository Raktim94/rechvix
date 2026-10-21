# Rechvix 2.0 — DESIGN.md

Version 1.1 · industrial ERP + CRM + Field Service · **design contract, not implementation evidence**

## Revision log
- 1.1 (2026-10-21, Phase 0): imported v1.0 supplied by the owner. Added §10 conflicts with the shipped UI. The Instagram reel referenced could not be viewed and is treated as unverified.

## 1. North star
A calm, precise industrial operations cockpit. The operator must answer: What is due? What needs attention? What is in stock? Where is my technician? What comes next? No decorative dashboard theater. Non-negotiables: keyboard-first high-frequency work; dense readable tables; financial and stock truth; mobile field usability; low-connectivity resilience; explicit operational states; accessibility; low-cost self-hosting. Every clickable control has an executable, authorized server outcome.

## 2. Inspirations (patterns, never pixel-copy)
Linear (navigation, command palette), Stripe Dashboard (transaction legibility, reconciliation), Shopify Polaris (resource tables, empty states), GitHub Primer (accessible forms), Mavri/Zulivio DESIGN.md (document layouts; honest status; role-sensitive UX), the awwwards skill (composition/typography, adapted to productivity), STEL Order (capability inventory only, no layout/content reuse).

## 3. Design language
Personality: credible, quiet, technically adept, utilitarian. Art direction **Slate & Signal**: neutral surfaces, graphite text, teal action accent, amber for attention. No glow gradients, globes, ornamental charts, stock photos or animation in transactional screens.

| Token | Light | Dark | Use |
|---|---|---|---|
| --canvas | #F5F7F8 | #0D151A | page |
| --surface | #FFFFFF | #142029 | panel |
| --surface-raised | #EBF1F3 | #1D2C35 | hover/inset |
| --border | #DCE5E8 | #31424B | dividers |
| --text | #16252E | #F0F5F6 | primary |
| --text-muted | #50636F | #AFBEC7 | secondary |
| --accent | #086B64 | #52D4BD | primary action |
| --accent-on | #FFFFFF | #092622 | on accent |
| --accent-soft | #E2F3EF | #123B37 | selection |
| --warning | #9A570B | #FFCA70 | due soon/review |
| --danger | #B42318 | #FF918A | failed/destructive |
| --success | #177245 | #5AE0A0 | confirmed |
| --info | #2459A8 | #8DB8FF | informational |
| --focus | #2563EB | #93C5FD | keyboard ring |

Semantic color never appears without text and icon. Red is never a brand accent. Every pairing is contrast-tested in code in both themes.

Typography: Inter (OFL, bundled locally, no CDN); numeric/code mono (system or IBM Plex Mono); Noto Sans Devanagari only when needed. Body 14/1.5 desktop, 15–16 mobile; data rows 13–14; page title 24/1.25; section 17–18; micro ≥12. Tabular numerals; right-aligned money; locale-formatted, timezone-aware. Exact decimals come from the backend; formatted strings are never source values.

Spacing 4px scale (4…48). Sidebar 248/64px, header 56–64px. Borders 1px; radii 12 cards / 8 inputs / 14 dialogs. Touch targets ≥44px mobile; focus ring 2–3px; destructive actions visibly separated. Tooltips are never the sole label.

## 4. Information architecture
Nav groups (visibility by permission and enabled module): **Home** (Overview, My tasks, Activity, Calendar) · **Sales** (CRM, Quotes, Orders, Delivery notes, Invoices, Returns, Collections, Recurring) · **Operations** (Catalog, Inventory, Transfers, Warehouses, Purchasing, Receipts, Assemblies) · **Service** (Service desk, Work orders, Dispatch board, Maintenance plans, Customer assets, Work notes, Time & expenses) · **Finance** (Expenses, Banking, Reconciliation, Accounting, Tax, Reports) · **Channels** (Portal, Shop, Inbox, Outgoing, Automations) · **Administration** (Teams & permissions, Integrations/API, Company/branch, Settings, Audit, Backup & health).
No duplicate first-class entries for existing Rechvix domains; keep current navigation stable until replacements are tested. Shell: logo, org/branch switcher, collapsible nav with query-derived badges, header with breadcrumb, search/command palette, notifications, quick-create, help, profile. Phone: compact task-oriented home and bottom actions, not a squeezed sidebar.

## 5. Page blueprints
- **Overview**: role-aware "Needs attention" list; 4–6 KPIs only with verified definitions and drill-down; charts only to answer a comparison, with timeframe, timezone and freshness shown.
- **CRM detail**: three columns only on wide screens (identity · timeline · follow-ups/finance/service). One party ID underlies quotes, orders, invoices, assets, tickets, payments, portal.
- **Quote→Order→Delivery→Invoice**: lists with saved views/filters/bulk safe actions; detail with status, timeline, source→target links, tax, stock effect; composer customer→items→price/tax→shipment→terms→review. Draft/save/issue/cancel are distinct; no silent backdating or edit-after-posting.
- **Inventory**: on hand / reserved / available / incoming with accessible definitions; item drawer with append-only history; transfers have shipping/receiving stages.
- **Field service**: dispatch board (day/week + unassigned queue); job card; technician mobile screen with offline pack, checklist, parts, time, signature, photos. Status transitions validate required fields. Geolocation is opt-in, purpose-limited, role-restricted, visible to affected staff, never continuous by default.
- **Finance**: ledger-centred; persistent warnings where figures are approximate or unreconciled; exports honour filters and authorization; print optimised for paper.
- **Portal/shop**: distinct security principal; own data only; server-side price/tax/stock checks; no arbitrary HTML/JS branding.

## 6. Interaction contracts
Primary CTA upper-right (sticky bottom on mobile). Every mutation has preconditions, pending state, result and a compensating path; financial postings are never undone by deletion. Inline field errors plus summary banner; one-step empty states; optimistic UI only for reversible non-financial actions. Autosave drafts only, with visible state and version-conflict handling. Confirm irreversible issuance, posting, bulk sends, write-offs. Never send or charge on preview. Keys: `/` search, `N` new, `Esc` close. Tables: sticky headers, stable cursors, column controls, URL-persisted filters, CSV-injection mitigation. Offline/out-of-sync indicators are never hidden.

## 7. Motion
150ms hover/press, 220ms panel, ≤300ms dialog; opacity/transform only; `prefers-reduced-motion` removes non-essential motion; no CDN fonts/icons; Lucide (ISC) or equivalent; no WebGL on work screens.

## 8. Accessibility and quality
WCAG 2.2 AA. Test 360/390/768 and 1280/1440/1920, 200% zoom, screen-reader smoke tests for composer, dispatch, dialogs, tables; print/PDF tested separately.

## 9. Deliverables and acceptance gate
Per UI phase: route map, roles/journeys, wireframes, permissions matrix, components/tokens, mobile flow, empty/loading/error/offline states; afterwards screenshots, keyboard test, WCAG review, performance check. No dead buttons, fake counters, alert-based CRUD, fake signature/GPS/read-receipt/payment/OCR certainty, or tax/legal claims without configured rules. Sign-off requires a tested end-to-end workflow.

## 10. Decisions on conflicts with the shipped UI (resolved 2026-10-21)
- **Type: keep the shipped IBM Plex Sans / IBM Plex Mono (+ Space Grotesk for headings/wordmark), not Inter.** They are already self-hosted, give true tabular figures for money columns and passed the Stage 11 contrast audit. Changing family would cost a full visual regression pass for no user benefit. §3 "Inter" is superseded.
- **Color: keep the shipped ledger-green accent (`--color-accent #0f6e5c`) and token names** in `apps/web/src/styles/tokens.css`; the Slate & Signal table in §3 is the reference for *new* tokens only. Added `--color-info` / `--color-info-soft` (light and both dark blocks; contrast 5.9–7.8:1) for quote, pipeline and service statuses. Amber stays the "needs attention" color (`--color-warning`).
- **Navigation:** adopt §4 groups incrementally as modules ship; current shell stays stable until then.

## 11. Art direction (awwwards skill process, adapted for work screens)
The skill (`~/.claude/skills/awwwards`, read and vetted 2026-10-21: Markdown only) is built for showcase sites. Its *process* applies; its spectacle does not. Answers to its six questions:
1. **Emotion on first load:** calm confidence — "my books and stock are under control." Not excitement.
2. **Vertical:** industrial distribution, workshops and field service. Language: ledger paper, spec sheets, stamped job cards. Not SaaS gradients.
3. **Archetype:** *Warm editorial restraint* in the app (paper surfaces, one accent, strong numerals); the public marketing site may be bolder. No dark-immersive or 3D in work screens.
4. **Typography (90% of the design):** Plex for data, Space Grotesk only for titles, tabular figures everywhere money or quantity appears, right-aligned. Fluid `clamp()` sizing applies to marketing/portal pages only; dense app screens use the fixed scale in §3.
5. **Layout rhythm:** vary density deliberately — dense tables for work, generous whitespace on the Overview and document previews. Avoid identical card rows; the one signature element is the **document stamp** (status chip + document number + timeline rail) reused on quote, order, delivery, invoice and job card so a document looks the same wherever it appears.
6. **Motion:** only motion that answers "what changed": row insert/status change fades (150ms), panel slide (220ms). Honour `prefers-reduced-motion`. No scroll-jacking, parallax, custom cursor, or smooth-scroll libraries on app screens.
Skill anti-patterns we adopt: no stock photography, no auto-rotating carousels, no hover-only affordances on touch, no pure black/white backgrounds, one animation system only.

## Invoice language and currency follow-up
Reuse the existing form tokens and responsive billing grid. Show language/currency/rate in the same workspace before draft creation, then show the persisted values read-only. Settings separates interface language, company document language, and a browser-only invoice-currency preference. The book currency is not relabelled. Rate labels state the direction explicitly. Failed settings saves stay inline. Hindi uses a plain, embedded-font browser-print layout, repeated table headings and non-splitting totals; server PDF themes remain unchanged. Verify controls at 375/768/1440px and Devanagari shaping in browser print. Native translation review and real-device verification remain separate acceptance tasks.


## Accountant handoff update (2026-10-21)

Tax pages now provide company-scoped 2026 accountant ZIP/XLSX packages for India, Spain, UK and US. Calculated records, unknown statutory inputs and review issues are distinct; no return is submitted. This supersedes older statements that only raw tax figures can be exported. It does not change VERI*FACTU, MTD submission, Indian live e-invoicing or US local-tax/nexus limitations. See [accountant export contract](docs/accountant-exports.md).
