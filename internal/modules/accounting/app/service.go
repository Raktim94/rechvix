// Package app is the accounting module's application/use-case layer.
// accounting is the ONLY module allowed to write journal_lines (brief §14,
// docs/architecture.md §7) — every other module with a financial effect
// (sales.FinalizeDocument, purchases.FinalizeDocument, a manual payment)
// calls Post or PostTx here rather than constructing journal rows itself.
//
// Post/PostTx enforce Layer 1 of the three-layer double-entry invariant
// (docs/architecture.md §7): summing debits/credits and rejecting a
// mismatch BEFORE attempting any insert. Layers 2 (a deferred DB constraint
// trigger) and 3 (no mutation of posted journal_lines, ever) live in
// migrations/0020_accounting.up.sql — see that file's header comment for
// why they're triggers rather than GRANT/REVOKE (a table owner bypasses
// GRANT restrictions the same way it bypasses RLS; a trigger is the one
// mechanism that holds regardless of which role connects).
package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/accounting/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

type Service struct {
	pool            database.Runner
	accounts        domain.AccountRepository
	journals        domain.JournalRepository
	journalLines    domain.JournalLineRepository
	fiscalPeriods   domain.FiscalPeriodRepository
	bankAccounts    domain.BankAccountRepository
	receipts        domain.ReceiptRepository
	payments        domain.PaymentRepository
	reconciliations domain.ReconciliationRepository
	permissions     *permissions.Checker
	audit           audit.Recorder
	now             func() time.Time
}

func NewService(
	pool database.Runner,
	accounts domain.AccountRepository,
	journals domain.JournalRepository,
	journalLines domain.JournalLineRepository,
	fiscalPeriods domain.FiscalPeriodRepository,
	bankAccounts domain.BankAccountRepository,
	receipts domain.ReceiptRepository,
	payments domain.PaymentRepository,
	reconciliations domain.ReconciliationRepository,
	checker *permissions.Checker,
	recorder audit.Recorder,
) *Service {
	return &Service{pool: pool, accounts: accounts, journals: journals, journalLines: journalLines,
		fiscalPeriods: fiscalPeriods, bankAccounts: bankAccounts, receipts: receipts, payments: payments,
		reconciliations: reconciliations, permissions: checker, audit: recorder, now: time.Now}
}

func (s *Service) view(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "accounting.view", permissions.Scope{})
}
func (s *Service) post(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "accounting.post", permissions.Scope{})
}
func (s *Service) reconcile(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "accounting.reconcile", permissions.Scope{})
}
func (s *Service) overrideLockedPeriod(ctx context.Context, principal permissions.Principal) error {
	return s.permissions.Require(ctx, principal, "accounting.override_locked_period", permissions.Scope{})
}

// --- Chart of accounts ---

type seedAccount struct {
	code    string
	name    string
	typ     domain.AccountType
	balance domain.NormalBalance
}

// defaultChart is brief §14's account list. Not every code is wired into
// an auto-posting flow yet — see docs/adr/0003-accounting-integration-point.md.
var defaultChart = []seedAccount{
	{domain.CodeCash, "Cash", domain.AccountAsset, domain.BalanceDebit},
	{domain.CodeBank, "Bank", domain.AccountAsset, domain.BalanceDebit},
	{domain.CodeAccountsReceivable, "Accounts Receivable", domain.AccountAsset, domain.BalanceDebit},
	{domain.CodeGSTInputTaxCredit, "GST Input Tax Credit", domain.AccountAsset, domain.BalanceDebit},
	{domain.CodeAccountsPayable, "Accounts Payable", domain.AccountLiability, domain.BalanceCredit},
	{domain.CodeGSTOutputTaxPayable, "GST Output Tax Payable", domain.AccountLiability, domain.BalanceCredit},
	{domain.CodeOwnersEquity, "Owner's Equity", domain.AccountEquity, domain.BalanceCredit},
	{domain.CodeOpeningBalanceEquity, "Opening Balance Equity", domain.AccountEquity, domain.BalanceCredit},
	{domain.CodeSales, "Sales", domain.AccountIncome, domain.BalanceCredit},
	{domain.CodeSalesReturns, "Sales Returns", domain.AccountIncome, domain.BalanceDebit}, // contra
	{domain.CodePurchases, "Purchases", domain.AccountExpense, domain.BalanceDebit},
	{domain.CodePurchaseReturns, "Purchase Returns", domain.AccountExpense, domain.BalanceCredit}, // contra
	{domain.CodeFreight, "Freight", domain.AccountExpense, domain.BalanceDebit},
	{domain.CodeDiscounts, "Discounts", domain.AccountExpense, domain.BalanceDebit},
	{domain.CodeRoundOff, "Round-off", domain.AccountExpense, domain.BalanceDebit},
	{domain.CodeGeneralExpenses, "General Expenses", domain.AccountExpense, domain.BalanceDebit},
}

// EnsureDefaultChartOfAccounts idempotently seeds orgID's chart of accounts
// with the brief §14 default set, if it doesn't already have one (a
// second call is a silent no-op, not an error — callers don't need to
// track whether they've already called this). NOT wired into
// identity.Bootstrap's org-creation flow — see the ADR for why; a
// production deployment should call this once per new organisation, e.g.
// from apps/server right after a successful bootstrap, or an onboarding
// action.
func (s *Service) EnsureDefaultChartOfAccounts(ctx context.Context, principal permissions.Principal, orgID uuid.UUID) error {
	if err := s.post(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		n, err := s.accounts.CountByOrganisation(ctx, orgID)
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		now := s.now()
		for _, sa := range defaultChart {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("accounting: generating account id: %w", err)
			}
			if err := s.accounts.Create(ctx, &domain.Account{
				ID: id, OrganisationID: orgID, Code: sa.code, Name: sa.name,
				AccountType: sa.typ, NormalBalance: sa.balance, IsSystem: true, IsActive: true, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("accounting: seeding account %s: %w", sa.code, err)
			}
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: orgID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "accounting.chart_of_accounts_seeded", EntityType: "organisation", EntityID: &orgID,
			AfterState: map[string]any{"account_count": len(defaultChart)}, At: now,
		})
	})
}

func (s *Service) ListAccounts(ctx context.Context, principal permissions.Principal) ([]*domain.Account, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.Account
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.accounts.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return out, err
}

// --- Posting ---

type JournalLineRequest struct {
	AccountCode string
	PartyID     *uuid.UUID
	Debit       decimal.Decimal
	Credit      decimal.Decimal
	Description string
}

type JournalRequest struct {
	OrganisationID uuid.UUID
	SourceType     string
	SourceID       *uuid.UUID
	JournalDate    time.Time
	Description    string
	CreatedBy      uuid.UUID
	Lines          []JournalLineRequest
	// ReversedJournalID is set only by ReverseJournalForSourceTx — a
	// direct caller of Post/PostTx has no reversal to link, since
	// reversing IS the dedicated operation for that (see its own doc
	// comment for why this isn't just another field callers fill in
	// themselves).
	ReversedJournalID *uuid.UUID
}

// Post is the standalone entry point (permission-checked, opens its own
// transaction) for a caller not already inside a RunScoped block — e.g. a
// manual adjustment journal posted directly from an accounting screen.
func (s *Service) Post(ctx context.Context, principal permissions.Principal, req JournalRequest) (*domain.Journal, error) {
	if err := s.post(ctx, principal); err != nil {
		return nil, err
	}
	var j *domain.Journal
	err := s.pool.RunScoped(ctx, req.OrganisationID, func(ctx context.Context) error {
		var err error
		j, err = s.doPost(ctx, principal, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return j, nil
}

// PostTx is the nested-transaction-safe entry point — the same shape as
// taxation.Service.CalculateAndSnapshotTx and
// inventory.Service.RecordMovementForOtherModule (docs/architecture.md §2):
// it does NOT open its own RunScoped, so sales.FinalizeDocument and
// purchases.FinalizeDocument can call it from inside their own already-open
// transaction, giving true single-transaction atomicity between a
// document's finalize state change and its accounting effect. Unlike those
// two examples, PostTx DOES take a principal and re-checks fiscal-period
// locking against it — period locking is accounting's own cross-cutting
// authorization concern (brief §52: "users without special permission
// cannot modify transactions inside locked periods"), not something a
// caller's own permission check (e.g. sales.finalize) can stand in for; a
// fully-permissioned salesperson finalizing an invoice must still be
// blocked from posting into a period their role hasn't been granted
// accounting.override_locked_period for. The caller MUST already be inside
// a transaction scoped to req.OrganisationID (same misuse class documented
// on RecordMovementForOtherModule and CalculateAndSnapshotTx).
func (s *Service) PostTx(ctx context.Context, principal permissions.Principal, req JournalRequest) (*domain.Journal, error) {
	return s.doPost(ctx, principal, req)
}

// validateBalanced is Layer 1 of the three-layer double-entry invariant
// (docs/architecture.md §7): every line must be exactly one of debit/credit
// (never both, never neither), and the totals must sum equal. Pure — no
// I/O — so it's directly unit-testable, including as a property test
// ("for any valid journal, Σdebit == Σcredit").
func validateBalanced(lines []JournalLineRequest) (totalDebit, totalCredit decimal.Decimal, err error) {
	totalDebit, totalCredit = decimal.Zero, decimal.Zero
	for _, l := range lines {
		if l.Debit.IsPositive() == l.Credit.IsPositive() {
			return decimal.Zero, decimal.Zero, fmt.Errorf("accounting: line for account %s must be either a debit or a credit, not both/neither", l.AccountCode)
		}
		totalDebit = totalDebit.Add(l.Debit)
		totalCredit = totalCredit.Add(l.Credit)
	}
	if !totalDebit.Equal(totalCredit) {
		return decimal.Zero, decimal.Zero, fmt.Errorf("%w: debit=%s credit=%s", domain.ErrUnbalancedJournal, totalDebit, totalCredit)
	}
	return totalDebit, totalCredit, nil
}

func (s *Service) doPost(ctx context.Context, principal permissions.Principal, req JournalRequest) (*domain.Journal, error) {
	if len(req.Lines) < 2 {
		return nil, domain.ErrEmptyJournal
	}

	// Layer 1: application-layer balance check, before any insert is
	// attempted (docs/architecture.md §7). Extracted as a pure function
	// (validateBalanced, below) so it's unit-testable — including as a
	// property test — with no database/permission fakes needed.
	totalDebit, totalCredit, err := validateBalanced(req.Lines)
	if err != nil {
		return nil, err
	}

	journalDate := req.JournalDate
	if journalDate.IsZero() {
		journalDate = s.now()
	}
	if period, err := s.fiscalPeriods.FindContaining(ctx, req.OrganisationID, journalDate); err == nil && period.IsLocked {
		if err := s.overrideLockedPeriod(ctx, principal); err != nil {
			return nil, fmt.Errorf("%w: period %q", domain.ErrPeriodLocked, period.Label)
		}
	}
	// FindContaining returning domain.ErrNotFound means no fiscal period is
	// configured for this date — treated as unlocked (brief §52: period
	// locking is opt-in configuration, not a precondition for posting).

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("accounting: generating journal id: %w", err)
	}
	now := s.now()
	j := &domain.Journal{
		ID: id, OrganisationID: req.OrganisationID, SourceType: req.SourceType, SourceID: req.SourceID,
		JournalDate: journalDate, Description: req.Description, ReversedJournalID: req.ReversedJournalID,
		CreatedBy: req.CreatedBy, CreatedAt: now,
	}
	if err := s.journals.Create(ctx, j); err != nil {
		return nil, err
	}
	for _, l := range req.Lines {
		acct, err := s.accounts.GetByCode(ctx, req.OrganisationID, l.AccountCode)
		if err != nil {
			return nil, fmt.Errorf("accounting: resolving account %s (has EnsureDefaultChartOfAccounts run for this organisation?): %w", l.AccountCode, err)
		}
		lineID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("accounting: generating journal_line id: %w", err)
		}
		debit, err := money.New(l.Debit, "INR")
		if err != nil {
			return nil, err
		}
		credit, err := money.New(l.Credit, "INR")
		if err != nil {
			return nil, err
		}
		if err := s.journalLines.Create(ctx, &domain.JournalLine{
			ID: lineID, OrganisationID: req.OrganisationID, JournalID: id, AccountID: acct.ID, PartyID: l.PartyID,
			Debit: debit, Credit: credit, Description: l.Description, CreatedAt: now,
		}); err != nil {
			return nil, err
		}
	}
	if err := s.audit.Record(ctx, audit.Entry{
		OrganisationID: req.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
		Action: "accounting.journal_posted", EntityType: "journal", EntityID: &id,
		AfterState: map[string]any{"source_type": req.SourceType, "line_count": len(req.Lines),
			"total_debit": totalDebit.String(), "total_credit": totalCredit.String()}, At: now,
	}); err != nil {
		return nil, err
	}
	return j, nil
}

// ReverseJournalForSourceTx posts a new journal that exactly reverses
// the most recent journal already posted for (lookupSourceType,
// sourceID) — every line's Debit/Credit swapped, same accounts and
// parties, linked back via ReversedJournalID (domain.Journal's own doc
// comment has always described this as the correction mechanism;
// nothing had implemented it before this). The reversal itself is
// posted under reversalSourceType (not lookupSourceType) — deliberately
// a separate value, so a party's ledger (or anything else reading
// SourceType) can tell a reversal apart from the original entry instead
// of the two being indistinguishable rows with the same source. journals/
// journal_lines are otherwise immutable by DB trigger (brief §7) — a
// correction is always a new, fully independent entry, never an edit of
// the original. Nested-transaction-safe (PostTx's own shape/doc comment
// applies identically here): the caller must already be inside a
// transaction scoped to principal's organisation. Returns
// Returns (nil, nil) — not an error — if no journal was ever posted for
// that source: FinalizeDocument-style callers only post a journal when
// there's a non-zero amount to post (both sales and purchases guard
// this, though not identically — sales rejects a zero-value document
// outright at finalize time via ErrZeroValueDocument, purchases simply
// skips posting), so "finalized but nothing was ever booked" is a real,
// legitimate state to cancel out of, not a caller error.
func (s *Service) ReverseJournalForSourceTx(ctx context.Context, principal permissions.Principal, lookupSourceType string, sourceID uuid.UUID, reversalSourceType string, description string) (*domain.Journal, error) {
	original, err := s.journals.GetBySource(ctx, principal.OrganisationID, lookupSourceType, sourceID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines, err := s.journalLines.ListByJournal(ctx, principal.OrganisationID, original.ID)
	if err != nil {
		return nil, err
	}
	reqLines := make([]JournalLineRequest, 0, len(lines))
	for _, l := range lines {
		acct, err := s.accounts.GetByID(ctx, principal.OrganisationID, l.AccountID)
		if err != nil {
			return nil, fmt.Errorf("accounting: resolving account for reversal: %w", err)
		}
		reqLines = append(reqLines, JournalLineRequest{
			AccountCode: acct.Code, PartyID: l.PartyID,
			Debit: l.Credit.Decimal(), Credit: l.Debit.Decimal(), // swapped
			Description: description,
		})
	}
	reversalID := original.ID
	return s.doPost(ctx, principal, JournalRequest{
		OrganisationID: principal.OrganisationID, SourceType: reversalSourceType, SourceID: &sourceID,
		JournalDate: s.now(), Description: description, CreatedBy: principal.UserID,
		Lines: reqLines, ReversedJournalID: &reversalID,
	})
}

func (s *Service) GetJournal(ctx context.Context, principal permissions.Principal, id uuid.UUID) (*domain.Journal, []*domain.JournalLine, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, nil, err
	}
	var j *domain.Journal
	var lines []*domain.JournalLine
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		j, err = s.journals.GetByID(ctx, principal.OrganisationID, id)
		if err != nil {
			return err
		}
		lines, err = s.journalLines.ListByJournal(ctx, principal.OrganisationID, id)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return j, lines, nil
}

// ListJournals is the "browse the general ledger" read path — the
// Expenses page (a thin UI over the same manual-journal capability Post
// already exists for, per its own doc comment) filters this list down to
// SourceType == "manual_expense" client-side rather than needing a
// dedicated server-side filter, the same "return everything, filter in
// the page" pattern ContactsPage/PurchasesPage already use at this scale.
func (s *Service) ListJournals(ctx context.Context, principal permissions.Principal, limit int) ([]*domain.Journal, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.Journal
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.journals.ListByOrganisation(ctx, principal.OrganisationID, limit)
		return err
	})
	return out, err
}

// ListExpenseEntries is the Expenses page's read path — one row per
// manual-expense journal (the debit side only; see
// domain.JournalLineRepository.ListDebitLinesBySourceType's doc comment
// for why the credit side isn't also returned).
func (s *Service) ListExpenseEntries(ctx context.Context, principal permissions.Principal, limit int) ([]domain.ExpenseEntry, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []domain.ExpenseEntry
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.journalLines.ListDebitLinesBySourceType(ctx, principal.OrganisationID, "manual_expense", limit)
		return err
	})
	return out, err
}

// --- Receipts / Payments ---

type RecordReceiptParams struct {
	PartyID         uuid.UUID
	SalesDocumentID *uuid.UUID
	Amount          decimal.Decimal
	BankAccountID   *uuid.UUID
	Method          domain.PaymentMethod
	ReferenceNumber string
	ReceivedAt      time.Time
}

// RecordReceipt books money received from a customer: Dr Bank/Cash (or the
// bank account's own GL account), Cr Accounts Receivable (party-tagged),
// in one transaction with the receipts row itself and the journal it posts
// (Scenario E's second half — see tests/integration for the end-to-end
// ₹10,000 invoice / ₹4,000 receipt / ₹6,000 outstanding proof).
func (s *Service) RecordReceipt(ctx context.Context, principal permissions.Principal, p RecordReceiptParams) (*domain.Receipt, error) {
	if err := s.post(ctx, principal); err != nil {
		return nil, err
	}
	if !p.Amount.IsPositive() {
		return nil, fmt.Errorf("accounting: receipt amount must be positive")
	}
	now := s.now()
	receivedAt := p.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = now
	}
	debitAccountCode := domain.CodeCash
	var rec *domain.Receipt
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if p.BankAccountID != nil {
			ba, err := s.bankAccounts.GetByID(ctx, principal.OrganisationID, *p.BankAccountID)
			if err != nil {
				return fmt.Errorf("accounting: resolving bank account: %w", err)
			}
			acct, err := s.accounts.GetByID(ctx, principal.OrganisationID, ba.GLAccountID)
			if err != nil {
				return err
			}
			debitAccountCode = acct.Code
		}
		j, err := s.PostTx(ctx, principal, JournalRequest{
			OrganisationID: principal.OrganisationID, SourceType: "receipt", JournalDate: receivedAt,
			Description: fmt.Sprintf("Receipt from party %s", p.PartyID), CreatedBy: principal.UserID,
			Lines: []JournalLineRequest{
				{AccountCode: debitAccountCode, Debit: p.Amount, Description: "Receipt"},
				{AccountCode: domain.CodeAccountsReceivable, PartyID: &p.PartyID, Credit: p.Amount, Description: "Receipt applied to AR"},
			},
		})
		if err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("accounting: generating receipt id: %w", err)
		}
		amount, err := money.New(p.Amount, "INR")
		if err != nil {
			return err
		}
		rec = &domain.Receipt{
			ID: id, OrganisationID: principal.OrganisationID, PartyID: p.PartyID, SalesDocumentID: p.SalesDocumentID,
			Amount: amount, BankAccountID: p.BankAccountID, Method: p.Method, ReferenceNumber: p.ReferenceNumber,
			ReceivedAt: receivedAt, JournalID: j.ID, CreatedBy: principal.UserID, CreatedAt: now,
		}
		return s.receipts.Create(ctx, rec)
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// ListReceiptsForSalesDocument is the invoice-detail screen's read path
// — "how much has actually been paid against THIS invoice", not just
// the customer's overall on-account balance.
func (s *Service) ListReceiptsForSalesDocument(ctx context.Context, principal permissions.Principal, salesDocumentID uuid.UUID) ([]*domain.Receipt, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.Receipt
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.receipts.ListBySalesDocument(ctx, principal.OrganisationID, salesDocumentID)
		return err
	})
	return out, err
}

type RecordPaymentParams struct {
	PartyID            uuid.UUID
	PurchaseDocumentID *uuid.UUID
	Amount             decimal.Decimal
	BankAccountID      *uuid.UUID
	Method             domain.PaymentMethod
	ReferenceNumber    string
	PaidAt             time.Time
}

// RecordPayment books money paid to a supplier: Dr Accounts Payable
// (party-tagged), Cr Bank/Cash — the mirror of RecordReceipt.
func (s *Service) RecordPayment(ctx context.Context, principal permissions.Principal, p RecordPaymentParams) (*domain.Payment, error) {
	if err := s.post(ctx, principal); err != nil {
		return nil, err
	}
	if !p.Amount.IsPositive() {
		return nil, fmt.Errorf("accounting: payment amount must be positive")
	}
	now := s.now()
	paidAt := p.PaidAt
	if paidAt.IsZero() {
		paidAt = now
	}
	creditAccountCode := domain.CodeCash
	var pay *domain.Payment
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if p.BankAccountID != nil {
			ba, err := s.bankAccounts.GetByID(ctx, principal.OrganisationID, *p.BankAccountID)
			if err != nil {
				return fmt.Errorf("accounting: resolving bank account: %w", err)
			}
			acct, err := s.accounts.GetByID(ctx, principal.OrganisationID, ba.GLAccountID)
			if err != nil {
				return err
			}
			creditAccountCode = acct.Code
		}
		j, err := s.PostTx(ctx, principal, JournalRequest{
			OrganisationID: principal.OrganisationID, SourceType: "payment", JournalDate: paidAt,
			Description: fmt.Sprintf("Payment to party %s", p.PartyID), CreatedBy: principal.UserID,
			Lines: []JournalLineRequest{
				{AccountCode: domain.CodeAccountsPayable, PartyID: &p.PartyID, Debit: p.Amount, Description: "Payment applied to AP"},
				{AccountCode: creditAccountCode, Credit: p.Amount, Description: "Payment"},
			},
		})
		if err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("accounting: generating payment id: %w", err)
		}
		amount, err := money.New(p.Amount, "INR")
		if err != nil {
			return err
		}
		pay = &domain.Payment{
			ID: id, OrganisationID: principal.OrganisationID, PartyID: p.PartyID, PurchaseDocumentID: p.PurchaseDocumentID,
			Amount: amount, BankAccountID: p.BankAccountID, Method: p.Method, ReferenceNumber: p.ReferenceNumber,
			PaidAt: paidAt, JournalID: j.ID, CreatedBy: principal.UserID, CreatedAt: now,
		}
		return s.payments.Create(ctx, pay)
	})
	if err != nil {
		return nil, err
	}
	return pay, nil
}

// --- Ledger / ageing ---

// GetPartyLedger returns partyID's chronological ledger up to asOf, with a
// running balance — derived fresh from journal_lines every call, never
// read from a mutable stored balance column (see domain.LedgerEntry's doc
// ListPaymentsForPurchaseDocument mirrors
// ListReceiptsForSalesDocument for the supplier side.
func (s *Service) ListPaymentsForPurchaseDocument(ctx context.Context, principal permissions.Principal, purchaseDocumentID uuid.UUID) ([]*domain.Payment, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.Payment
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.payments.ListByPurchaseDocument(ctx, principal.OrganisationID, purchaseDocumentID)
		return err
	})
	return out, err
}

// comment on why: a separately-maintained running total drifts from the
// transaction history it's supposed to summarize).
//
// RunningBalance is cumulative(debit) - cumulative(credit): for a customer
// (AR-context) party this IS the outstanding receivable directly (a sale
// debits AR, a receipt credits it). For a supplier (AP-context) party the
// sign is inverted — a purchase credits AP, a payment debits it — so the
// outstanding payable is -RunningBalance; this method doesn't special-case
// customer vs. supplier (accounting doesn't know a party's type, that's
// contacts' concern), so the caller interprets the sign per which side of
// the relationship they're asking about.
func (s *Service) GetPartyLedger(ctx context.Context, principal permissions.Principal, partyID uuid.UUID, asOf time.Time) ([]domain.LedgerEntry, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var rows []domain.LedgerRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		rows, err = s.journalLines.ListByPartyUpTo(ctx, principal.OrganisationID, partyID, asOf)
		return err
	})
	if err != nil {
		return nil, err
	}
	running := decimal.Zero
	entries := make([]domain.LedgerEntry, 0, len(rows))
	for _, r := range rows {
		running = running.Add(r.Debit.Decimal()).Sub(r.Credit.Decimal())
		balance, err := money.New(running, "INR")
		if err != nil {
			return nil, err
		}
		entries = append(entries, domain.LedgerEntry{
			JournalID: r.JournalID, JournalDate: r.JournalDate, SourceType: r.SourceType, SourceID: r.SourceID,
			Description: r.Description, Debit: r.Debit, Credit: r.Credit, RunningBalance: balance,
		})
	}
	return entries, nil
}

// GetAgeing buckets partyID's OPEN debit amounts by age as of asOf (brief
// §15: current/1-30/31-60/61-90/90+). Since this schema doesn't store an
// explicit invoice-to-payment allocation link, open amounts are derived by
// a simple oldest-first (FIFO) consumption of credits against debits —
// each credit closes the oldest still-open debit(s) first, which is the
// standard simple ageing algorithm when there's no explicit allocation
// (true invoice-specific allocation, where a receipt can be earmarked
// against a particular invoice rather than FIFO-applied, is a larger
// feature flagged in the ADR as a follow-up, not built here).
func (s *Service) GetAgeing(ctx context.Context, principal permissions.Principal, partyID uuid.UUID, asOf time.Time) (domain.AgeingBucket, error) {
	if err := s.view(ctx, principal); err != nil {
		return domain.AgeingBucket{}, err
	}
	var rows []domain.LedgerRow
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		rows, err = s.journalLines.ListByPartyUpTo(ctx, principal.OrganisationID, partyID, asOf)
		return err
	})
	if err != nil {
		return domain.AgeingBucket{}, err
	}

	type openDebit struct {
		date   time.Time
		amount decimal.Decimal
	}
	var open []openDebit
	for _, r := range rows {
		if r.Debit.Decimal().IsPositive() {
			open = append(open, openDebit{date: r.JournalDate, amount: r.Debit.Decimal()})
			continue
		}
		credit := r.Credit.Decimal()
		for i := range open {
			if credit.IsZero() {
				break
			}
			if open[i].amount.IsZero() {
				continue
			}
			consume := decimal.Min(open[i].amount, credit)
			open[i].amount = open[i].amount.Sub(consume)
			credit = credit.Sub(consume)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].date.Before(open[j].date) })

	zero, err := money.Zero("INR")
	if err != nil {
		return domain.AgeingBucket{}, err
	}
	bucket := domain.AgeingBucket{Current: zero, Days1To30: zero, Days31To60: zero, Days61To90: zero, Days90Plus: zero, Total: zero}
	for _, od := range open {
		if od.amount.IsZero() {
			continue
		}
		days := int(asOf.Sub(od.date).Hours() / 24)
		amt, err := money.New(od.amount, "INR")
		if err != nil {
			return domain.AgeingBucket{}, err
		}
		switch {
		case days <= 0:
			bucket.Current, _ = bucket.Current.Add(amt)
		case days <= 30:
			bucket.Days1To30, _ = bucket.Days1To30.Add(amt)
		case days <= 60:
			bucket.Days31To60, _ = bucket.Days31To60.Add(amt)
		case days <= 90:
			bucket.Days61To90, _ = bucket.Days61To90.Add(amt)
		default:
			bucket.Days90Plus, _ = bucket.Days90Plus.Add(amt)
		}
		bucket.Total, _ = bucket.Total.Add(amt)
	}
	return bucket, nil
}

// --- Fiscal periods ---

type CreateFiscalPeriodParams struct {
	StartDate time.Time
	EndDate   time.Time
	Label     string
}

func (s *Service) CreateFiscalPeriod(ctx context.Context, principal permissions.Principal, p CreateFiscalPeriodParams) (*domain.FiscalPeriod, error) {
	if err := s.post(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("accounting: generating fiscal_period id: %w", err)
	}
	fp := &domain.FiscalPeriod{ID: id, OrganisationID: principal.OrganisationID, StartDate: p.StartDate, EndDate: p.EndDate, Label: p.Label, CreatedAt: s.now()}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.fiscalPeriods.Create(ctx, fp)
	})
	if err != nil {
		return nil, err
	}
	return fp, nil
}

func (s *Service) ListFiscalPeriods(ctx context.Context, principal permissions.Principal) ([]*domain.FiscalPeriod, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.FiscalPeriod
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.fiscalPeriods.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return out, err
}

// SetPeriodLock locks or unlocks a fiscal period. Locking requires
// accounting.post (an accountant closes their own books); unlocking a
// period that's currently locked additionally requires
// accounting.override_locked_period — reopening a closed period is a more
// sensitive action than closing one.
func (s *Service) SetPeriodLock(ctx context.Context, principal permissions.Principal, id uuid.UUID, locked bool) error {
	if err := s.post(ctx, principal); err != nil {
		return err
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		existing, err := s.fiscalPeriods.GetByID(ctx, principal.OrganisationID, id)
		if err != nil {
			return err
		}
		if existing.IsLocked && !locked {
			if err := s.overrideLockedPeriod(ctx, principal); err != nil {
				return err
			}
		}
		now := s.now()
		var lockedBy *uuid.UUID
		var lockedAt *time.Time
		if locked {
			lockedBy = &principal.UserID
			lockedAt = &now
		}
		if err := s.fiscalPeriods.SetLocked(ctx, principal.OrganisationID, id, locked, lockedBy, lockedAt); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "accounting.fiscal_period_lock_changed", EntityType: "fiscal_period", EntityID: &id,
			AfterState: map[string]any{"locked": locked}, At: now,
		})
	})
}

// --- Bank accounts / reconciliation ---

type CreateBankAccountParams struct {
	LegalEntityID uuid.UUID
	Name          string
	Kind          domain.BankAccountKind
	AccountNumber string
	BankName      string
	IFSCCode      string
	CurrencyCode  string
	GLAccountCode string
}

func (s *Service) CreateBankAccount(ctx context.Context, principal permissions.Principal, p CreateBankAccountParams) (*domain.BankAccount, error) {
	if err := s.post(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("accounting: generating bank_account id: %w", err)
	}
	b := &domain.BankAccount{ID: id, OrganisationID: principal.OrganisationID, LegalEntityID: p.LegalEntityID, Name: p.Name,
		Kind: p.Kind, AccountNumber: p.AccountNumber, BankName: p.BankName, IFSCCode: p.IFSCCode, CurrencyCode: p.CurrencyCode,
		IsActive: true, CreatedAt: s.now()}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		glCode := p.GLAccountCode
		if glCode == "" {
			if p.Kind == domain.BankAccountCash {
				glCode = domain.CodeCash
			} else {
				glCode = domain.CodeBank
			}
		}
		acct, err := s.accounts.GetByCode(ctx, principal.OrganisationID, glCode)
		if err != nil {
			return fmt.Errorf("accounting: resolving bank account's GL account %s: %w", glCode, err)
		}
		b.GLAccountID = acct.ID
		return s.bankAccounts.Create(ctx, b)
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) ListBankAccounts(ctx context.Context, principal permissions.Principal) ([]*domain.BankAccount, error) {
	if err := s.view(ctx, principal); err != nil {
		return nil, err
	}
	var out []*domain.BankAccount
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		out, err = s.bankAccounts.ListByOrganisation(ctx, principal.OrganisationID)
		return err
	})
	return out, err
}

type RecordReconciliationParams struct {
	BankAccountID  uuid.UUID
	StatementDate  time.Time
	OpeningBalance decimal.Decimal
	ClosingBalance decimal.Decimal
}

func (s *Service) RecordReconciliation(ctx context.Context, principal permissions.Principal, p RecordReconciliationParams) (*domain.Reconciliation, error) {
	if err := s.reconcile(ctx, principal); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("accounting: generating reconciliation id: %w", err)
	}
	opening, err := money.New(p.OpeningBalance, "INR")
	if err != nil {
		return nil, err
	}
	closing, err := money.New(p.ClosingBalance, "INR")
	if err != nil {
		return nil, err
	}
	rec := &domain.Reconciliation{ID: id, OrganisationID: principal.OrganisationID, BankAccountID: p.BankAccountID,
		StatementDate: p.StatementDate, OpeningBalance: opening, ClosingBalance: closing, IsReconciled: true, CreatedAt: s.now()}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.reconciliations.Create(ctx, rec)
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}
