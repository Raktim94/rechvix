package database

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"rechvix/internal/platform/money"
)

type txContextKey struct{}

// WithTx returns a context carrying an active transaction. Repository
// implementations call TxFromContext (or, more commonly, Pool.Q) to find
// it rather than taking a *pgx.Tx parameter directly, so application-layer
// code never has to thread a transaction handle through every call.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// TxFromContext returns the transaction attached to ctx, if any.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txContextKey{}).(pgx.Tx)
	return tx, ok
}

// Querier is the subset of pgx's query surface shared by *pgxpool.Pool and
// pgx.Tx, letting repository code work identically whether or not it's
// inside an explicit transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// poolQuerier adapts *pgxpool.Pool's Exec (which returns pgconn.CommandTag)
// to the simplified Querier interface above.
type poolQuerier struct{ p *Pool }

func (q poolQuerier) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := q.p.Pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
func (q poolQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.p.Pool.Query(ctx, sql, args...)
}
func (q poolQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.p.Pool.QueryRow(ctx, sql, args...)
}

type txQuerier struct{ tx pgx.Tx }

func (q txQuerier) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := q.tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
func (q txQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return q.tx.Query(ctx, sql, args...)
}
func (q txQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return q.tx.QueryRow(ctx, sql, args...)
}

// Q returns a Querier bound to the active transaction in ctx if one was
// started via RunScoped/Run, otherwise falls back to the bare pool. Every
// repository method should read its Querier from here rather than storing
// a pool/tx reference at construction time — that's what lets one
// repository struct serve both inside-a-transaction and (rare,
// intentionally-unscoped bootstrap) outside-a-transaction callers
// correctly.
func (p *Pool) Q(ctx context.Context) Querier {
	if tx, ok := TxFromContext(ctx); ok {
		return txQuerier{tx: tx}
	}
	return poolQuerier{p: p}
}

// Runner is the subset of *Pool's transaction-orchestration surface that
// application-layer services depend on (RunScoped/Run/SetOrganisationScope).
// Services take this interface, not *Pool, specifically so unit tests can
// substitute a fake that just invokes fn(ctx) directly against in-memory
// fake repositories — no real PostgreSQL required for testing business
// logic (login flow, password validation, permission checks). *Pool
// satisfies this interface as-is; no adapter needed.
type Runner interface {
	RunScoped(ctx context.Context, orgID uuid.UUID, fn TxFunc) error
	Run(ctx context.Context, fn TxFunc) error
	SetOrganisationScope(ctx context.Context, orgID uuid.UUID) error
}

// TxFunc is application/use-case code run inside a transaction opened by
// RunScoped or Run. Returning an error rolls back; returning nil commits.
type TxFunc func(ctx context.Context) error

// RunScoped opens a transaction, sets app.current_organisation_id for its
// duration via set_config(..., true) (the `true` argument makes it
// transaction-local, equivalent to SET LOCAL — and, critically,
// parameterized, so there is no string-concatenation SQL-injection risk
// the way `SET LOCAL app.current_organisation_id = '<value>'` built via
// fmt.Sprintf would be), runs fn, and commits on success / rolls back on
// error or panic. This is how every tenant-scoped Row-Level Security
// policy in migrations/ actually gets its comparison value — see the
// deployment-requirement comment in
// migrations/0001_organisation_hierarchy.up.sql.
func (p *Pool) RunScoped(ctx context.Context, orgID uuid.UUID, fn TxFunc) (err error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: beginning scoped transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err = tx.Exec(ctx, `SELECT set_config('app.current_organisation_id', $1, true)`, orgID.String()); err != nil {
		return fmt.Errorf("database: setting organisation scope: %w", err)
	}

	scoped := WithTx(ctx, tx)
	if cur := p.ledgerCurrency(ctx, tx, orgID); cur != "" {
		scoped = money.WithLedgerCurrency(scoped, cur)
	}
	if err = fn(scoped); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: committing scoped transaction: %w", err)
	}
	return nil
}

// orgCurrencies caches organisations.default_currency_code per organisation
// for the life of the process. The column is set at setup and never changed
// by the application, so a cache with no invalidation is safe; if that ever
// changes, add invalidation here.
var orgCurrencies sync.Map // uuid.UUID -> string

// ledgerCurrency returns the organisation's currency, "" if it cannot be
// read (for example during first-run bootstrap, before the row exists), in
// which case callers fall back to money.DefaultLedgerCurrency.
func (p *Pool) ledgerCurrency(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) string {
	if v, ok := orgCurrencies.Load(orgID); ok {
		return v.(string)
	}
	var code string
	if err := tx.QueryRow(ctx, `SELECT default_currency_code FROM organisations WHERE id = $1`, orgID).Scan(&code); err != nil {
		return ""
	}
	orgCurrencies.Store(orgID, code)
	return code
}

// SetOrganisationScope sets app.current_organisation_id for the remainder
// of the transaction active in ctx (one started via Run or RunScoped).
// Intended for the one bootstrap case RunScoped can't cover by itself:
// creating the first organisation, where the app generates the new
// organisation's UUIDv7 id in Go, opens an unscoped transaction (Run),
// calls this to establish that fresh id as the scope, and only then
// inserts the organisations row — satisfying that table's own RLS
// WITH CHECK before the row exists to reference.
func (p *Pool) SetOrganisationScope(ctx context.Context, orgID uuid.UUID) error {
	_, err := p.Q(ctx).Exec(ctx, `SELECT set_config('app.current_organisation_id', $1, true)`, orgID.String())
	if err != nil {
		return fmt.Errorf("database: setting organisation scope: %w", err)
	}
	return nil
}

// Run opens a transaction with no organisation scope set. Reserved for
// the small, deliberate set of bootstrap paths that must run before an
// organisation is known or created — login/password-reset's
// auth_lookup_user_by_email call, and first-organisation creation (which
// sets its own freshly-generated org ID as the scope from inside fn,
// once it exists). Every other use case should call RunScoped.
func (p *Pool) Run(ctx context.Context, fn TxFunc) (err error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("database: beginning transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(WithTx(ctx, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: committing transaction: %w", err)
	}
	return nil
}
