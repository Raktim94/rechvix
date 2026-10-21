# Stage 0 — Research

Status: living document. Updated whenever a version or government schema
assumption is verified or changes.

## Confirmed platform versions (verified via web search, 2026-09-02)

| Component | Version | Status | Source |
|---|---|---|---|
| Go | 1.27.3 | Security release (1.27.2 fixed net/http and http2 advisories, 1.27.3 fixed crypto/x509 GO-2026-6809); originally researched as 1.27.1 on 2026-09-01 | go.dev/doc/devel/release, endoflife.date/go |
| PostgreSQL | 18 (current patch series) | GA since 2025-09-25 | postgresql.org/about/news/postgresql-18-released-3142 |
| GST e-Invoice JSON schema | v1.1 (INV-01) | Current, CBIC/GSTN | einvoice1.gst.gov.in |
| IRP (default) | NIC — einvoice1.gst.gov.in | Sandbox: einv-apisandbox.nic.in | nic.gov.in/project/gst-e-invoice |

**Do not re-trust these numbers indefinitely.** Re-verify Go/Postgres minor
versions before each release build, and re-verify the GST schema version
before every change to `internal/modules/einvoice` or `ewaybill` — GSTN ships
breaking changes with short notice (see below).

## Critical, recently-changed government API behavior

GSTN advisory dated 2026-06-17, deferred from an original 2026-06-15 date and
made **live in production on 2026-08-01**, affecting three APIs simultaneously:
the e-Invoice API, the e-Way Bill by IRN API, and the EWB Closure API.

Two structural changes:

1. **Mandatory Ship-to GSTIN.** Wherever ship-to details are present and an
   e-Way Bill is required, Ship-to GSTIN must now be transmitted. Unregistered
   / not-applicable ship-to party → literal value `URP`.
2. **Voluntary EWB Closure facility.** Supplier, recipient, transporter, or
   even the driver can close an e-Way Bill after delivery (same day or next
   day), via portal login or mobile OTP.

This is a live example of Section 80.13 ("never hard-code government policy
that should be versioned"): had this platform existed with EWB fields
hardcoded, the 2026-08-01 change would have broken production. This is the
concrete justification for `einvoice/v1/`, `einvoice/vNext/` style versioned
adapters from day one (Section 9), and for **not** encoding e-Way Bill closure
as an afterthought — it goes into the state machine now.

Action: the `ewaybill` module's request/response DTOs must include
`ship_to_gstin` (nullable, `URP` sentinel supported) and a `CLOSED` status
distinct from `CANCELLED` from the first migration, even though Stage 8
(actual government wiring) comes later. Getting the schema right now avoids
an awkward migration later.

## Zulivio inspection (Section 27 requirement)

Pending — before implementing `internal/modules/identity`, inspect
`~/Zulivio`'s login UX, change-password UX, password validation, session
handling, rate limiting, and permission model, and record what's reusable
(and what's outdated/not to copy) as an ADR. Zulivio's own memory notes
mention `nest build` diverging from `tsc --noEmit` — irrelevant to Go, but a
reminder to independently verify this new stack's build rather than trust
typecheck-passes-therefore-it-builds.

## Decided (previously open, resolved by explicit instruction, 2026-09-02)

The user has directed autonomous progress with no further clarifying
questions ("keep choosing best option don't ask me"). These are no longer
open — they're settled, recorded here for traceability:

- **License:** AGPL-3.0, matching `nodedr-pos` (same copyright holders:
  Nodedr Infotech Private Limited and Raktim Ranjit). See `LICENSE` and
  `README.md`.
- **Distribution model:** public GitHub repo, downloadable and self-hostable
  like `nodedr-pos` — Docker/CasaOS-friendly, **no external connection
  required** for core operation (see `docs/architecture.md`'s license
  section for exactly which modules are the sole, opt-in exceptions).
- **Working name:** stays `rechvix` — repo is public now, no
  rename planned unless the user names it later.
- **e-Invoice/e-Way Bill provider:** build against the NIC sandbox
  (`einv-apisandbox.nic.in`) behind `EInvoiceProvider`; a real GSP/ASP
  account is a drop-in adapter swap later, by design.
- **First deployment target:** assume CasaOS on debiancasa, consistent with
  the user's other self-hosted projects, until told otherwise.
- **Country scope for v1:** India-only; the generic tax model supports more
  without a schema change, but no second country ships in v1.
- **MFA enforcement timing:** TOTP mandatory-enforced for owners/admins/
  accountants from Stage 2 onward (already built — see `docs/TODO.md`).

## Explicitly deferred (not unresolved — decided, just not v1)

- FIFO costing: architecture permits it (costing strategy is pluggable);
  weighted-average only ships in v1.
- MSIX/Microsoft Store signing: Tauri 2 shell prepared from Stage 10; actual
  store submission only after a working desktop build exists to submit.
- NATS: outbox stays Postgres-based until there's a measured scale reason to
  change it.
- Reverse proxy: **not bundled** in the default Compose/CasaOS deployment.
  See `docs/architecture.md` §12 for the reasoning.
