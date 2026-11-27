# ADR 0002: stock_balances maintained by the application layer, not a DB trigger

Status: Accepted
Date: 2026-09-02

## Context

`docs/architecture.md` §6 left this open: "`stock_balances` is a
materialized projection maintained transactionally in the same DB
transaction as the movement insert (via trigger or explicit
application-layer update inside the same transaction — decided in Stage
2 against real write-volume numbers, not guessed now)."

## Decision

**Application-layer update**, inside the same `RunScoped` transaction as
the `stock_movements` insert (`internal/modules/inventory/app.Service.recordMovement`).

## Reasoning

1. **Costing logic already lives in Go.** Weighted-average cost
   recalculation (`domain.CostingStrategy.OnReceipt`) needs the current
   balance, the movement's quantity/cost, and a formula that's naturally
   expressed as Go arithmetic on `decimal.Decimal`. Re-deriving the same
   formula in PL/pgSQL would mean maintaining the costing rule twice, in
   two languages, with no shared test coverage between them — a real risk
   for a number that ends up in stock valuation reports.
2. **Concurrent movements lock the balance before calculating quantity and cost.**
   `StockBalanceRepository.GetForUpdate` locks the row for the organisation,
   warehouse and variant with `SELECT ... FOR UPDATE`. If the row is missing,
   it inserts a zero balance with `ON CONFLICT DO NOTHING`, then repeats the
   locking read in a new statement. Under Read Committed, a writer that
   waited for another first receipt now sees its committed quantity and
   average cost. The lock lasts through the movement and balance update.
   An ordinary balance lookup still returns zero for an unstocked item
   without creating a row.
3. **Consistency with this codebase's existing pattern.** Every other
   "derive B from A inside the same transaction" case so far (audit log
   entries, session rotation) is application-layer code, not a trigger —
   keeping stock balance maintenance in the same place keeps the mental
   model of "where does side-effect X happen" consistent across modules.
   The one place this codebase *does* use a DB-level mechanism for a
   write-time invariant is RLS policies (a security boundary, not a
   derived-value calculation) — a different category of concern.
4. **Debuggability.** A stack trace and a `slog` call site in Go is easier
   to reason about during an incident than a trigger silently firing
   underneath an `INSERT`. Given `stock_balances` directly drives sale
   eligibility (can this order be fulfilled?) and valuation reporting,
   an engineer diagnosing a discrepancy benefits from the update being a
   readable, steppable function rather than implicit database behavior.

## Consequences

- Every code path that writes a `stock_movements` row **must** go through
  `Service.recordMovement` (directly, or via `RecordMovementForOtherModule`
  for a cross-module caller like `purchases.FinalizeDocument`) — there is
  no database-level backstop that keeps `stock_balances` in sync if a
  future migration or admin script inserts into `stock_movements`
  directly. This is a real risk to document, not hand-wave away: a raw
  `INSERT INTO stock_movements` bypassing the application layer would
  silently desynchronize the materialized balance from the ledger.
- Mitigated by: (a) `stock_balances` is explicitly documented as
  "always re-derivable from `stock_movements` by replaying it" — the
  backup/restore reconciliation check (Scenario N, Stage 11) is exactly
  the safety net for this failure mode, and (b) no application code or
  migration in this repository writes to `stock_movements` any way other
  than through `recordMovement`.
- If a future measured write-volume problem shows this application-layer
  path is a throughput bottleneck (unlikely at the scale this system
  targets — a single warehouse's movement rate, not a high-frequency
  trading ledger), moving the balance-maintenance arithmetic into a
  trigger remains possible without changing the `stock_movements` schema;
  only `internal/modules/inventory/app.Service.recordMovement`'s internals
  would need to change.
