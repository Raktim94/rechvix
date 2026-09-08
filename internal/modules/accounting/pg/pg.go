// Package pg is the accounting module's PostgreSQL repository
// implementation. Same shape as internal/modules/purchases/pg.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/accounting/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
)

func pgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- accounts ---

type AccountRepo struct{ pool *database.Pool }

func NewAccountRepo(pool *database.Pool) *AccountRepo { return &AccountRepo{pool: pool} }

func (r *AccountRepo) Create(ctx context.Context, a *domain.Account) error {
	const q = `
		INSERT INTO accounts (id, organisation_id, code, name, account_type, normal_balance, parent_account_id, is_system, is_active, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, a.ID, a.OrganisationID, a.Code, a.Name, string(a.AccountType),
		string(a.NormalBalance), a.ParentAccountID, a.IsSystem, a.IsActive, a.CreatedAt)
	if err != nil {
		if pgUniqueViolation(err) {
			return fmt.Errorf("accounting: account code %q already exists: %w", a.Code, err)
		}
		return fmt.Errorf("accounting: inserting account: %w", err)
	}
	return nil
}

const accountCols = `id, organisation_id, code, name, account_type, normal_balance, parent_account_id, is_system, is_active, created_at`

func scanAccount(row pgx.Row) (*domain.Account, error) {
	var a domain.Account
	var accountType, normalBalance string
	if err := row.Scan(&a.ID, &a.OrganisationID, &a.Code, &a.Name, &accountType, &normalBalance,
		&a.ParentAccountID, &a.IsSystem, &a.IsActive, &a.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}
	a.AccountType = domain.AccountType(accountType)
	a.NormalBalance = domain.NormalBalance(normalBalance)
	return &a, nil
}

func (r *AccountRepo) GetByCode(ctx context.Context, orgID uuid.UUID, code string) (*domain.Account, error) {
	q := fmt.Sprintf(`SELECT %s FROM accounts WHERE organisation_id = $1 AND code = $2`, accountCols)
	a, err := scanAccount(r.pool.Q(ctx).QueryRow(ctx, q, orgID, code))
	if err != nil {
		return nil, fmt.Errorf("accounting: getting account by code %q: %w", code, err)
	}
	return a, nil
}

func (r *AccountRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Account, error) {
	q := fmt.Sprintf(`SELECT %s FROM accounts WHERE organisation_id = $1 AND id = $2`, accountCols)
	return scanAccount(r.pool.Q(ctx).QueryRow(ctx, q, orgID, id))
}

func (r *AccountRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.Account, error) {
	q := fmt.Sprintf(`SELECT %s FROM accounts WHERE organisation_id = $1 ORDER BY code`, accountCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing accounts: %w", err)
	}
	defer rows.Close()
	var out []*domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AccountRepo) CountByOrganisation(ctx context.Context, orgID uuid.UUID) (int, error) {
	var n int
	err := r.pool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM accounts WHERE organisation_id = $1`, orgID).Scan(&n)
	return n, err
}

// --- journals / journal_lines ---

type JournalRepo struct{ pool *database.Pool }

func NewJournalRepo(pool *database.Pool) *JournalRepo { return &JournalRepo{pool: pool} }

func (r *JournalRepo) Create(ctx context.Context, j *domain.Journal) error {
	const q = `
		INSERT INTO journals (id, organisation_id, source_type, source_id, journal_date, description, reversed_journal_id, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, j.ID, j.OrganisationID, j.SourceType, j.SourceID, j.JournalDate,
		nullIfEmpty(j.Description), j.ReversedJournalID, j.CreatedBy, j.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting journal: %w", err)
	}
	return nil
}

const journalCols = `id, organisation_id, source_type, source_id, journal_date, COALESCE(description, ''), reversed_journal_id, created_by, created_at`

func scanJournal(row pgx.Row) (*domain.Journal, error) {
	var j domain.Journal
	if err := row.Scan(&j.ID, &j.OrganisationID, &j.SourceType, &j.SourceID, &j.JournalDate,
		&j.Description, &j.ReversedJournalID, &j.CreatedBy, &j.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &j, nil
}

func (r *JournalRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Journal, error) {
	q := fmt.Sprintf(`SELECT %s FROM journals WHERE organisation_id = $1 AND id = $2`, journalCols)
	return scanJournal(r.pool.Q(ctx).QueryRow(ctx, q, orgID, id))
}

func (r *JournalRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID, limit int) ([]*domain.Journal, error) {
	if limit <= 0 {
		limit = 200
	}
	q := fmt.Sprintf(`SELECT %s FROM journals WHERE organisation_id = $1 ORDER BY journal_date DESC, created_at DESC LIMIT $2`, journalCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing journals: %w", err)
	}
	defer rows.Close()
	var out []*domain.Journal
	for rows.Next() {
		j, err := scanJournal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type JournalLineRepo struct{ pool *database.Pool }

func NewJournalLineRepo(pool *database.Pool) *JournalLineRepo { return &JournalLineRepo{pool: pool} }

func (r *JournalLineRepo) Create(ctx context.Context, l *domain.JournalLine) error {
	const q = `
		INSERT INTO journal_lines (id, organisation_id, journal_id, account_id, party_id, debit_amount, credit_amount, description, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, l.ID, l.OrganisationID, l.JournalID, l.AccountID, l.PartyID,
		l.Debit.Decimal(), l.Credit.Decimal(), nullIfEmpty(l.Description), l.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting journal_line: %w", err)
	}
	return nil
}

func (r *JournalLineRepo) ListByJournal(ctx context.Context, orgID, journalID uuid.UUID) ([]*domain.JournalLine, error) {
	const q = `SELECT jl.id, jl.organisation_id, jl.journal_id, jl.account_id, jl.party_id, jl.debit_amount, jl.credit_amount,
			COALESCE(jl.description, ''), jl.created_at, a.code
		FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		WHERE jl.organisation_id = $1 AND jl.journal_id = $2 ORDER BY jl.created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, journalID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing journal_lines: %w", err)
	}
	defer rows.Close()
	var out []*domain.JournalLine
	for rows.Next() {
		var l domain.JournalLine
		var debit, credit decimal.Decimal
		var accountCode string
		if err := rows.Scan(&l.ID, &l.OrganisationID, &l.JournalID, &l.AccountID, &l.PartyID, &debit, &credit,
			&l.Description, &l.CreatedAt, &accountCode); err != nil {
			return nil, fmt.Errorf("accounting: scanning journal_line: %w", err)
		}
		// journal_lines carries no currency_code column of its own — every
		// amount in this schema is the organisation's base currency
		// (multi-currency journal lines, i.e. posting a foreign-currency
		// transaction's *base*-currency equivalent alongside its original
		// amount, is real brief §17 scope not built in this pass — see the
		// ADR). INR is the only currency exercised end-to-end so far
		// (sales/purchases/tax all default to it); this is a placeholder,
		// not a silent multi-currency claim.
		l.Debit = money.MustNew(debit, "INR")
		l.Credit = money.MustNew(credit, "INR")
		out = append(out, &l)
	}
	return out, rows.Err()
}

func (r *JournalLineRepo) ListByPartyUpTo(ctx context.Context, orgID, partyID uuid.UUID, asOf time.Time) ([]domain.LedgerRow, error) {
	const q = `
		SELECT j.id, j.journal_date, j.source_type, j.source_id, COALESCE(j.description, ''), jl.debit_amount, jl.credit_amount
		FROM journal_lines jl JOIN journals j ON j.id = jl.journal_id
		WHERE jl.organisation_id = $1 AND jl.party_id = $2 AND j.journal_date <= $3
		ORDER BY j.journal_date, j.created_at`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, partyID, asOf)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing party ledger rows: %w", err)
	}
	defer rows.Close()
	var out []domain.LedgerRow
	for rows.Next() {
		var lr domain.LedgerRow
		var debit, credit decimal.Decimal
		if err := rows.Scan(&lr.JournalID, &lr.JournalDate, &lr.SourceType, &lr.SourceID, &lr.Description, &debit, &credit); err != nil {
			return nil, fmt.Errorf("accounting: scanning ledger row: %w", err)
		}
		lr.Debit = money.MustNew(debit, "INR")
		lr.Credit = money.MustNew(credit, "INR")
		out = append(out, lr)
	}
	return out, rows.Err()
}

func (r *JournalLineRepo) ListDebitLinesBySourceType(ctx context.Context, orgID uuid.UUID, sourceType string, limit int) ([]domain.ExpenseEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	const q = `
		SELECT j.id, j.journal_date, a.code, a.name, COALESCE(jl.description, j.description, ''), jl.debit_amount
		FROM journal_lines jl
		JOIN journals j ON j.id = jl.journal_id
		JOIN accounts a ON a.id = jl.account_id
		WHERE jl.organisation_id = $1 AND j.source_type = $2 AND jl.debit_amount > 0
		ORDER BY j.journal_date DESC, j.created_at DESC
		LIMIT $3`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, sourceType, limit)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing debit lines for source type %s: %w", sourceType, err)
	}
	defer rows.Close()
	var out []domain.ExpenseEntry
	for rows.Next() {
		var e domain.ExpenseEntry
		var amount decimal.Decimal
		if err := rows.Scan(&e.JournalID, &e.JournalDate, &e.AccountCode, &e.AccountName, &e.Description, &amount); err != nil {
			return nil, fmt.Errorf("accounting: scanning expense entry row: %w", err)
		}
		e.Amount = money.MustNew(amount, "INR")
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- fiscal_periods ---

type FiscalPeriodRepo struct{ pool *database.Pool }

func NewFiscalPeriodRepo(pool *database.Pool) *FiscalPeriodRepo { return &FiscalPeriodRepo{pool: pool} }

func (r *FiscalPeriodRepo) Create(ctx context.Context, p *domain.FiscalPeriod) error {
	const q = `
		INSERT INTO fiscal_periods (id, organisation_id, start_date, end_date, label, is_locked, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, p.ID, p.OrganisationID, p.StartDate, p.EndDate, p.Label, p.IsLocked, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting fiscal_period: %w", err)
	}
	return nil
}

const fiscalPeriodCols = `id, organisation_id, start_date, end_date, label, is_locked, locked_at, locked_by, created_at`

func scanFiscalPeriod(row pgx.Row) (*domain.FiscalPeriod, error) {
	var p domain.FiscalPeriod
	if err := row.Scan(&p.ID, &p.OrganisationID, &p.StartDate, &p.EndDate, &p.Label, &p.IsLocked, &p.LockedAt, &p.LockedBy, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *FiscalPeriodRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.FiscalPeriod, error) {
	q := fmt.Sprintf(`SELECT %s FROM fiscal_periods WHERE organisation_id = $1 AND id = $2`, fiscalPeriodCols)
	return scanFiscalPeriod(r.pool.Q(ctx).QueryRow(ctx, q, orgID, id))
}

func (r *FiscalPeriodRepo) FindContaining(ctx context.Context, orgID uuid.UUID, d time.Time) (*domain.FiscalPeriod, error) {
	q := fmt.Sprintf(`SELECT %s FROM fiscal_periods WHERE organisation_id = $1 AND start_date <= $2 AND end_date >= $2`, fiscalPeriodCols)
	return scanFiscalPeriod(r.pool.Q(ctx).QueryRow(ctx, q, orgID, d))
}

func (r *FiscalPeriodRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.FiscalPeriod, error) {
	q := fmt.Sprintf(`SELECT %s FROM fiscal_periods WHERE organisation_id = $1 ORDER BY start_date`, fiscalPeriodCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing fiscal_periods: %w", err)
	}
	defer rows.Close()
	var out []*domain.FiscalPeriod
	for rows.Next() {
		p, err := scanFiscalPeriod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *FiscalPeriodRepo) SetLocked(ctx context.Context, orgID, id uuid.UUID, locked bool, lockedBy *uuid.UUID, lockedAt *time.Time) error {
	const q = `UPDATE fiscal_periods SET is_locked = $3, locked_by = $4, locked_at = $5 WHERE organisation_id = $1 AND id = $2`
	rowsAffected, err := r.pool.Q(ctx).Exec(ctx, q, orgID, id, locked, lockedBy, lockedAt)
	if err != nil {
		return fmt.Errorf("accounting: updating fiscal_period lock state: %w", err)
	}
	if rowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// --- bank_accounts ---

type BankAccountRepo struct{ pool *database.Pool }

func NewBankAccountRepo(pool *database.Pool) *BankAccountRepo { return &BankAccountRepo{pool: pool} }

func (r *BankAccountRepo) Create(ctx context.Context, b *domain.BankAccount) error {
	const q = `
		INSERT INTO bank_accounts (id, organisation_id, legal_entity_id, name, account_kind, account_number, bank_name, ifsc_code, currency_code, gl_account_id, is_active, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, b.ID, b.OrganisationID, b.LegalEntityID, b.Name, string(b.Kind),
		nullIfEmpty(b.AccountNumber), nullIfEmpty(b.BankName), nullIfEmpty(b.IFSCCode), b.CurrencyCode, b.GLAccountID, b.IsActive, b.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting bank_account: %w", err)
	}
	return nil
}

const bankAccountCols = `id, organisation_id, legal_entity_id, name, account_kind, COALESCE(account_number, ''), COALESCE(bank_name, ''), COALESCE(ifsc_code, ''), currency_code, gl_account_id, is_active, created_at`

func scanBankAccount(row pgx.Row) (*domain.BankAccount, error) {
	var b domain.BankAccount
	var kind string
	if err := row.Scan(&b.ID, &b.OrganisationID, &b.LegalEntityID, &b.Name, &kind, &b.AccountNumber,
		&b.BankName, &b.IFSCCode, &b.CurrencyCode, &b.GLAccountID, &b.IsActive, &b.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	b.Kind = domain.BankAccountKind(kind)
	return &b, nil
}

func (r *BankAccountRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.BankAccount, error) {
	q := fmt.Sprintf(`SELECT %s FROM bank_accounts WHERE organisation_id = $1 AND id = $2`, bankAccountCols)
	return scanBankAccount(r.pool.Q(ctx).QueryRow(ctx, q, orgID, id))
}

func (r *BankAccountRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.BankAccount, error) {
	q := fmt.Sprintf(`SELECT %s FROM bank_accounts WHERE organisation_id = $1 ORDER BY name`, bankAccountCols)
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing bank_accounts: %w", err)
	}
	defer rows.Close()
	var out []*domain.BankAccount
	for rows.Next() {
		b, err := scanBankAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- receipts / payments ---

type ReceiptRepo struct{ pool *database.Pool }

func NewReceiptRepo(pool *database.Pool) *ReceiptRepo { return &ReceiptRepo{pool: pool} }

func (r *ReceiptRepo) Create(ctx context.Context, rec *domain.Receipt) error {
	const q = `
		INSERT INTO receipts (id, organisation_id, party_id, sales_document_id, amount, currency_code, bank_account_id, payment_method, reference_number, received_at, journal_id, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, rec.ID, rec.OrganisationID, rec.PartyID, rec.SalesDocumentID, rec.Amount.Decimal(),
		rec.Amount.Currency(), rec.BankAccountID, string(rec.Method), nullIfEmpty(rec.ReferenceNumber), rec.ReceivedAt,
		rec.JournalID, rec.CreatedBy, rec.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting receipt: %w", err)
	}
	return nil
}

func (r *ReceiptRepo) ListByParty(ctx context.Context, orgID, partyID uuid.UUID) ([]*domain.Receipt, error) {
	const q = `SELECT id, organisation_id, party_id, sales_document_id, amount, currency_code, bank_account_id, payment_method,
			COALESCE(reference_number, ''), received_at, journal_id, created_by, created_at
		FROM receipts WHERE organisation_id = $1 AND party_id = $2 ORDER BY received_at DESC`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, partyID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing receipts: %w", err)
	}
	defer rows.Close()
	var out []*domain.Receipt
	for rows.Next() {
		var rec domain.Receipt
		var amount decimal.Decimal
		var currency, method string
		if err := rows.Scan(&rec.ID, &rec.OrganisationID, &rec.PartyID, &rec.SalesDocumentID, &amount, &currency,
			&rec.BankAccountID, &method, &rec.ReferenceNumber, &rec.ReceivedAt, &rec.JournalID, &rec.CreatedBy, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("accounting: scanning receipt: %w", err)
		}
		rec.Amount = money.MustNew(amount, currency)
		rec.Method = domain.PaymentMethod(method)
		out = append(out, &rec)
	}
	return out, rows.Err()
}

type PaymentRepo struct{ pool *database.Pool }

func NewPaymentRepo(pool *database.Pool) *PaymentRepo { return &PaymentRepo{pool: pool} }

func (r *PaymentRepo) Create(ctx context.Context, p *domain.Payment) error {
	const q = `
		INSERT INTO payments (id, organisation_id, party_id, purchase_document_id, amount, currency_code, bank_account_id, payment_method, reference_number, paid_at, journal_id, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, p.ID, p.OrganisationID, p.PartyID, p.PurchaseDocumentID, p.Amount.Decimal(),
		p.Amount.Currency(), p.BankAccountID, string(p.Method), nullIfEmpty(p.ReferenceNumber), p.PaidAt,
		p.JournalID, p.CreatedBy, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting payment: %w", err)
	}
	return nil
}

func (r *PaymentRepo) ListByParty(ctx context.Context, orgID, partyID uuid.UUID) ([]*domain.Payment, error) {
	const q = `SELECT id, organisation_id, party_id, purchase_document_id, amount, currency_code, bank_account_id, payment_method,
			COALESCE(reference_number, ''), paid_at, journal_id, created_by, created_at
		FROM payments WHERE organisation_id = $1 AND party_id = $2 ORDER BY paid_at DESC`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, partyID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing payments: %w", err)
	}
	defer rows.Close()
	var out []*domain.Payment
	for rows.Next() {
		var p domain.Payment
		var amount decimal.Decimal
		var currency, method string
		if err := rows.Scan(&p.ID, &p.OrganisationID, &p.PartyID, &p.PurchaseDocumentID, &amount, &currency,
			&p.BankAccountID, &method, &p.ReferenceNumber, &p.PaidAt, &p.JournalID, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("accounting: scanning payment: %w", err)
		}
		p.Amount = money.MustNew(amount, currency)
		p.Method = domain.PaymentMethod(method)
		out = append(out, &p)
	}
	return out, rows.Err()
}

// --- reconciliations ---

type ReconciliationRepo struct{ pool *database.Pool }

func NewReconciliationRepo(pool *database.Pool) *ReconciliationRepo {
	return &ReconciliationRepo{pool: pool}
}

func (r *ReconciliationRepo) Create(ctx context.Context, rec *domain.Reconciliation) error {
	const q = `
		INSERT INTO reconciliations (id, organisation_id, bank_account_id, statement_date, opening_balance, closing_balance, is_reconciled, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	_, err := r.pool.Q(ctx).Exec(ctx, q, rec.ID, rec.OrganisationID, rec.BankAccountID, rec.StatementDate,
		rec.OpeningBalance.Decimal(), rec.ClosingBalance.Decimal(), rec.IsReconciled, rec.CreatedAt)
	if err != nil {
		return fmt.Errorf("accounting: inserting reconciliation: %w", err)
	}
	return nil
}

func (r *ReconciliationRepo) ListByBankAccount(ctx context.Context, orgID, bankAccountID uuid.UUID) ([]*domain.Reconciliation, error) {
	const q = `SELECT id, organisation_id, bank_account_id, statement_date, opening_balance, closing_balance, is_reconciled, reconciled_at, reconciled_by, created_at
		FROM reconciliations WHERE organisation_id = $1 AND bank_account_id = $2 ORDER BY statement_date DESC`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, bankAccountID)
	if err != nil {
		return nil, fmt.Errorf("accounting: listing reconciliations: %w", err)
	}
	defer rows.Close()
	var out []*domain.Reconciliation
	for rows.Next() {
		var rec domain.Reconciliation
		var opening, closing decimal.Decimal
		if err := rows.Scan(&rec.ID, &rec.OrganisationID, &rec.BankAccountID, &rec.StatementDate, &opening, &closing,
			&rec.IsReconciled, &rec.ReconciledAt, &rec.ReconciledBy, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("accounting: scanning reconciliation: %w", err)
		}
		rec.OpeningBalance = money.MustNew(opening, "INR")
		rec.ClosingBalance = money.MustNew(closing, "INR")
		out = append(out, &rec)
	}
	return out, rows.Err()
}
