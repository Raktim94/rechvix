package money

import "context"

type ledgerCurrencyKey struct{}

// DefaultLedgerCurrency is used only when a context carries no ledger
// currency (code that runs outside an organisation-scoped transaction, e.g.
// unit tests). It preserves the behaviour from before multi-currency support.
const DefaultLedgerCurrency = "INR"

// WithLedgerCurrency returns ctx carrying the organisation's ledger currency.
// database.Pool.RunScoped sets it for every scoped transaction.
func WithLedgerCurrency(ctx context.Context, code string) context.Context {
	if code == "" {
		return ctx
	}
	return context.WithValue(ctx, ledgerCurrencyKey{}, code)
}

// LedgerCurrency is the currency amounts in the organisation's books and
// reports are denominated in.
func LedgerCurrency(ctx context.Context) string {
	if c, ok := ctx.Value(ledgerCurrencyKey{}).(string); ok && c != "" {
		return c
	}
	return DefaultLedgerCurrency
}
