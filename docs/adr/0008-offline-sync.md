# ADR 0008: Offline-first sync between a desktop install and a central server

Status: Accepted (phase 1 shipped, later phases open)
Date: 2026-10-26

## Context

The Windows app bundles its own server and PostgreSQL, so it works with no network. A business that also runs a central
Rechvix server wants both to hold the same products and contacts, and the desktop to keep working when the line is down.

## Decision

Row-level change log plus push and pull, last write wins per record.

- Migration 0062 adds `sync_changes` (an append-only log), `sync_links` (one row per linked organisation on a device) and a
  trigger, `sync_capture`, on each synced table. The trigger writes the row as JSON on insert or update and the id on delete.
- A device pushes its own changes (`POST /sync/push`) and pulls everyone else's (`GET /sync/pull?after=<seq>`). Each change
  carries its origin device, so the server never sends a device its own changes back, and a device applying what it pulled
  turns the trigger off (`rechvix.sync_skip`) so those changes are not pushed again.
- Authentication is an API key with the scope `sync:readwrite` (permission `sync.manage`). The key is stored encrypted.
  Plain `http` is accepted only for loopback and private addresses.
- The device's organisation id and the server's differ. The sender's `organisation_id` is ignored; every row is written
  into the organisation of the authenticated caller on the receiving side.
- Conflicts: an update applies only if its `updated_at` is newer than the stored row, otherwise it is counted as stale and
  dropped. Tables with no `updated_at` (unit conversions, barcodes, tax registrations) are immutable once written. A change
  that violates a unique constraint (two different records with the same SKU, barcode or unit code) is counted as a
  conflict and shown in Settings; it does not block the rest of the batch. Changes that fail on a missing parent are
  retried once after the rest of the batch.

## Covered in phase 1

Units of measure, unit conversions, categories, brands, products, variants, barcodes, contacts, addresses and contact tax
registrations. Tests: `tests/integration/datasync_test.go` runs a device and a server on two separate databases and covers
push, pull, parents-before-children, no echo, newer edit wins, stale edit loses, delete, and cross-organisation ids.

## Not covered (and why)

- **Sales, purchases, stock and accounting.** These are ledgers. Copying rows would double-post journals, break running stock
  balances, and collide on document numbers (both sides issue `INV/2026-27/000001`). They need event-style sync, per-device
  number ranges and recomputed balances. That is phase 2 and has to be designed on its own.
- **Linking a device that already holds data the server also has** (the same SKU created on both sides) reports
  conflicts instead of merging. Link a fresh install, or rename the duplicates.
- **Clock skew.** Last write wins uses each machine's clock. A device with a wrong clock can win or lose edits it should not.
- **Other settings** (company, tax, users, prices) are per install.
