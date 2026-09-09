// Package domain holds the accounting module's entity types and
// repository interfaces (docs/architecture.md §7, brief §14). accounting
// is the only module allowed to write journal_lines — every other module
// with a financial effect calls the app layer's Post/PostTx instead of
// constructing journal rows itself. No I/O, no framework imports.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/money"
)

type AccountType string

const (
	AccountAsset     AccountType = "ASSET"
	AccountLiability AccountType = "LIABILITY"
	AccountEquity    AccountType = "EQUITY"
	AccountIncome    AccountType = "INCOME"
	AccountExpense   AccountType = "EXPENSE"
)

type NormalBalance string

const (
	BalanceDebit  NormalBalance = "DEBIT"
	BalanceCredit NormalBalance = "CREDIT"
)

// Account is one chart_of_accounts row. Codes below are the system-seeded
// default chart (EnsureDefaultChartOfAccounts) — brief §14's account list.
// Not every code is wired into an auto-posting flow yet (see the ADR at
// docs/adr/0003-accounting-integration-point.md for what's actually posted
// to today vs. present for completeness/future use).
const (
	CodeCash                 = "1000"
	CodeBank                 = "1010"
	CodeAccountsReceivable   = "1100"
	CodeGSTInputTaxCredit    = "1400"
	CodeAccountsPayable      = "2000"
	CodeGSTOutputTaxPayable  = "2100"
	CodeOwnersEquity         = "3000"
	CodeOpeningBalanceEquity = "3100"
	CodeSales                = "4000"
	CodeSalesReturns         = "4100"
	CodePurchases            = "5000"
	CodePurchaseReturns      = "5100"
	CodeFreight              = "5200"
	CodeDiscounts            = "5300"
	CodeRoundOff             = "5900"
	CodeGeneralExpenses      = "5990"
)

type Account struct {
	ID              uuid.UUID
	OrganisationID  uuid.UUID
	Code            string
	Name            string
	AccountType     AccountType
	NormalBalance   NormalBalance
	ParentAccountID *uuid.UUID
	IsSystem        bool
	IsActive        bool
	CreatedAt       time.Time
}

// Journal is a journals row. Every journal in this schema is created
// already POSTED (journals.status's CHECK constraint enforces this at the
// DB level too) — there is no DRAFT journal to edit later. Correction is
// always a new journal with ReversedJournalID pointing at the one it
// reverses (brief §14).
type Journal struct {
	ID                uuid.UUID
	OrganisationID    uuid.UUID
	SourceType        string
	SourceID          *uuid.UUID
	JournalDate       time.Time
	Description       string
	ReversedJournalID *uuid.UUID
	CreatedBy         uuid.UUID
	CreatedAt         time.Time
}

// JournalLine is a journal_lines row. Exactly one of Debit/Credit is
// positive (CHECK constraints in the migration enforce this at the DB
// level as well as the app-layer check in Post/PostTx).
type JournalLine struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	JournalID      uuid.UUID
	AccountID      uuid.UUID
	PartyID        *uuid.UUID
	Debit          money.Money
	Credit         money.Money
	Description    string
	CreatedAt      time.Time
}

type FiscalPeriod struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	StartDate      time.Time
	EndDate        time.Time
	Label          string
	IsLocked       bool
	LockedAt       *time.Time
	LockedBy       *uuid.UUID
	CreatedAt      time.Time
}

type BankAccountKind string

const (
	BankAccountBank BankAccountKind = "BANK"
	BankAccountCash BankAccountKind = "CASH"
)

type BankAccount struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	LegalEntityID  uuid.UUID
	Name           string
	Kind           BankAccountKind
	AccountNumber  string
	BankName       string
	IFSCCode       string
	CurrencyCode   string
	GLAccountID    uuid.UUID
	IsActive       bool
	CreatedAt      time.Time
}

type PaymentMethod string

const (
	MethodCash         PaymentMethod = "CASH"
	MethodUPI          PaymentMethod = "UPI"
	MethodCard         PaymentMethod = "CARD"
	MethodBankTransfer PaymentMethod = "BANK_TRANSFER"
	MethodCheque       PaymentMethod = "CHEQUE"
	MethodOther        PaymentMethod = "OTHER"
)

// Receipt is money received from a customer (party_id is the customer).
type Receipt struct {
	ID              uuid.UUID
	OrganisationID  uuid.UUID
	PartyID         uuid.UUID
	SalesDocumentID *uuid.UUID
	Amount          money.Money
	BankAccountID   *uuid.UUID
	Method          PaymentMethod
	ReferenceNumber string
	ReceivedAt      time.Time
	JournalID       uuid.UUID
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
}

// Payment is money paid to a supplier (party_id is the supplier).
type Payment struct {
	ID                 uuid.UUID
	OrganisationID     uuid.UUID
	PartyID            uuid.UUID
	PurchaseDocumentID *uuid.UUID
	Amount             money.Money
	BankAccountID      *uuid.UUID
	Method             PaymentMethod
	ReferenceNumber    string
	PaidAt             time.Time
	JournalID          uuid.UUID
	CreatedBy          uuid.UUID
	CreatedAt          time.Time
}

type Reconciliation struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	BankAccountID  uuid.UUID
	StatementDate  time.Time
	OpeningBalance money.Money
	ClosingBalance money.Money
	IsReconciled   bool
	ReconciledAt   *time.Time
	ReconciledBy   *uuid.UUID
	CreatedAt      time.Time
}

// LedgerEntry is one row of a customer/supplier ledger view — a read model
// built from journal_lines (joined to journals for date/description), NOT
// a separately maintained running-balance column. Running balances that
// live in a mutable column drift from the transaction history they're
// supposed to summarize (a real bug class: see nodedr-pos's Float-`increment`
// balance-drift incident in project memory) — this system derives a
// party's balance by summing signed journal_lines amounts up to a point in
// time, every time, from the immutable ledger.
type LedgerEntry struct {
	JournalID      uuid.UUID
	JournalDate    time.Time
	SourceType     string
	SourceID       *uuid.UUID
	Description    string
	Debit          money.Money
	Credit         money.Money
	RunningBalance money.Money
}

// AgeingBucket is one row of an ageing report (brief §15): how much of a
// party's outstanding balance falls into each age band, computed from the
// same ledger derivation as LedgerEntry, not a separately tracked field.
type AgeingBucket struct {
	Current    money.Money
	Days1To30  money.Money
	Days31To60 money.Money
	Days61To90 money.Money
	Days90Plus money.Money
	Total      money.Money
}

// --- Repository interfaces ---

type AccountRepository interface {
	Create(ctx context.Context, a *Account) error
	GetByCode(ctx context.Context, orgID uuid.UUID, code string) (*Account, error)
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Account, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*Account, error)
	CountByOrganisation(ctx context.Context, orgID uuid.UUID) (int, error)
}

type JournalRepository interface {
	Create(ctx context.Context, j *Journal) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Journal, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID, limit int) ([]*Journal, error)
	// GetBySource returns the most recent journal posted for
	// (sourceType, sourceID) — the one a reversal needs to reverse. In
	// every source type this schema actually posts against a given
	// source more than once for (e.g. "sales_document": one finalize,
	// at most one later cancellation), the caller has already excluded
	// the "already cancelled" case before asking, so "most recent" and
	// "the original" coincide in practice; this does not attempt to
	// disambiguate a source with a longer real correction history.
	GetBySource(ctx context.Context, orgID uuid.UUID, sourceType string, sourceID uuid.UUID) (*Journal, error)
}

type JournalLineRepository interface {
	Create(ctx context.Context, l *JournalLine) error
	ListByJournal(ctx context.Context, orgID, journalID uuid.UUID) ([]*JournalLine, error)
	// ListByPartyUpTo returns every journal_lines row (joined with its
	// parent journal for date/description) for partyID with
	// journals.journal_date <= asOf, oldest first — the raw feed
	// LedgerEntry/AgeingBucket are built from.
	ListByPartyUpTo(ctx context.Context, orgID, partyID uuid.UUID, asOf time.Time) ([]LedgerRow, error)
	// ListDebitLinesBySourceType returns the debit-side line (joined with
	// its account for a display name) of every journal tagged
	// sourceType, newest first — one row per journal, not per line: for
	// a simple two-line entry (debit an expense account, credit
	// Cash/Bank) debit always equals credit, so the credit-side line
	// carries no information a caller like the Expenses page needs and
	// would only double the list if also returned.
	ListDebitLinesBySourceType(ctx context.Context, orgID uuid.UUID, sourceType string, limit int) ([]ExpenseEntry, error)
}

// ExpenseEntry is ListDebitLinesBySourceType's row shape — currently
// only consumed by the Expenses page (source type "manual_expense"), but
// not named "ExpenseJournalLine" since nothing about the query is
// actually expense-specific beyond the sourceType a caller passes in.
type ExpenseEntry struct {
	JournalID   uuid.UUID
	JournalDate time.Time
	AccountCode string
	AccountName string
	Description string
	Amount      money.Money
}

// LedgerRow is JournalLineRepository.ListByPartyUpTo's raw row shape — the
// repository layer's concern (a straight join), turned into the richer
// LedgerEntry (with running balance) by the application layer.
type LedgerRow struct {
	JournalID   uuid.UUID
	JournalDate time.Time
	SourceType  string
	SourceID    *uuid.UUID
	Description string
	Debit       money.Money
	Credit      money.Money
}

type FiscalPeriodRepository interface {
	Create(ctx context.Context, p *FiscalPeriod) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*FiscalPeriod, error)
	// FindContaining returns the fiscal period covering date d, or
	// ErrNotFound if none is configured — accounting.Post treats "no
	// period configured" as unlocked (a business that hasn't set up fiscal
	// periods yet isn't blocked from posting; period locking is opt-in).
	FindContaining(ctx context.Context, orgID uuid.UUID, d time.Time) (*FiscalPeriod, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*FiscalPeriod, error)
	SetLocked(ctx context.Context, orgID, id uuid.UUID, locked bool, lockedBy *uuid.UUID, lockedAt *time.Time) error
}

type BankAccountRepository interface {
	Create(ctx context.Context, b *BankAccount) error
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*BankAccount, error)
	ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*BankAccount, error)
}

type ReceiptRepository interface {
	Create(ctx context.Context, r *Receipt) error
	ListByParty(ctx context.Context, orgID, partyID uuid.UUID) ([]*Receipt, error)
	// ListBySalesDocument answers "how much has actually been received
	// against THIS invoice" — RecordReceiptParams.SalesDocumentID was
	// accepted and stored on the Receipt row from day one, but nothing
	// ever read it back out this way; every payment screen could only
	// show the party's total on-account balance, never a given invoice's
	// own paid/outstanding split.
	ListBySalesDocument(ctx context.Context, orgID, salesDocumentID uuid.UUID) ([]*Receipt, error)
}

type PaymentRepository interface {
	Create(ctx context.Context, p *Payment) error
	ListByParty(ctx context.Context, orgID, partyID uuid.UUID) ([]*Payment, error)
	// ListByPurchaseDocument is ListBySalesDocument's mirror for the
	// supplier side.
	ListByPurchaseDocument(ctx context.Context, orgID, purchaseDocumentID uuid.UUID) ([]*Payment, error)
}

type ReconciliationRepository interface {
	Create(ctx context.Context, r *Reconciliation) error
	ListByBankAccount(ctx context.Context, orgID, bankAccountID uuid.UUID) ([]*Reconciliation, error)
}

// ExpenseAttachment is a receipt/bill photo or PDF attached to a manual
// expense (ExpensesPage) — a manual expense has no dedicated entity of
// its own (it's just a POSTED journals row, source_type='manual_expense'
// per Service.Post), so this points at the journal directly. FileData is
// only populated by Get, never by List — a receipt table row has no
// business carrying every attachment's full bytes just to render a list.
type ExpenseAttachment struct {
	ID             uuid.UUID
	OrganisationID uuid.UUID
	JournalID      uuid.UUID
	Filename       string
	ContentType    string
	FileData       []byte
	FileSizeBytes  int64
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
}

type ExpenseAttachmentRepository interface {
	Create(ctx context.Context, a *ExpenseAttachment) error
	// ListByJournal never populates FileData (see ExpenseAttachment's own
	// doc comment) — Get does, for the one-attachment download path.
	ListByJournal(ctx context.Context, orgID, journalID uuid.UUID) ([]*ExpenseAttachment, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (*ExpenseAttachment, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}
