// Package domain holds the reporting module's types: report filters, row
// shapes, and the repository interface reports are read through. This
// module is read-mostly by nature (docs/architecture.md §2) — it queries
// data other modules already own (sales, purchases, inventory, accounting,
// taxation/gstindia) rather than owning new business state. Brief §22's
// filters/report list, brief §8's GSTR-1/3B-oriented (NOT filing) exports,
// and brief §23's dashboard live here.
package domain

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/platform/money"
)

// Filter is the shared query shape every report accepts (brief §22: date,
// financial year, branch, warehouse, GST registration, customer, product,
// HSN, salesperson, tax rate, invoice type, payment status). Not every
// report honors every field — a report that doesn't apply a given filter
// simply ignores it — but every filter that DOES apply flows through this
// one struct instead of a bespoke parameter list per report, so adding a
// filter to a report is "read one more field," not "add a parameter and
// rewrite the call site." OrganisationID is set by the application layer
// from the authenticated Principal, never accepted from the request body
// (brief Rule 5) — repository methods take it as an explicit belt-and-
// braces parameter even though RLS also enforces it, exactly like every
// other module's repositories.
type Filter struct {
	OrganisationID   uuid.UUID
	From             *time.Time
	To               *time.Time
	BranchID         *uuid.UUID
	WarehouseID      *uuid.UUID
	CustomerPartyID  *uuid.UUID
	SupplierPartyID  *uuid.UUID
	ProductVariantID *uuid.UUID
	HSNSACCode       *string
	DocumentType     *string
	Status           *string
	// LegalEntityID is the caller's optional single-company request (set
	// by the HTTP handler from a `legal_entity_id` query param, mirroring
	// BranchID/WarehouseID above) — app.Service.resolvedFilter reads this
	// to produce LegalEntityIDs below; the pg layer never reads it
	// directly.
	LegalEntityID *uuid.UUID
	// LegalEntityIDs is the RESOLVED company filter every report query
	// actually applies — set by app.Service.resolvedFilter, never by a
	// caller directly. nil means no restriction (every company); a
	// non-nil, possibly-empty slice restricts to exactly those legal
	// entities (empty means "matches nothing") — same contract as
	// permissions.ResolveLegalEntityFilter, which is what produces it.
	LegalEntityIDs []uuid.UUID
}

// GroupDimension is a validated allow-list of GROUP BY dimensions a
// summary report can be sliced by. A raw string groupBy parameter is never
// interpolated into SQL directly (brief §62) — the pg layer switches on
// this typed value to pick one of a fixed set of literal GROUP BY
// expressions.
type GroupDimension string

const (
	GroupByDay         GroupDimension = "day"
	GroupByMonth       GroupDimension = "month"
	GroupByCustomer    GroupDimension = "customer"
	GroupBySupplier    GroupDimension = "supplier"
	GroupByProduct     GroupDimension = "product"
	GroupByCategory    GroupDimension = "category"
	GroupBySalesperson GroupDimension = "salesperson"
	GroupByBranch      GroupDimension = "branch"
	GroupByWarehouse   GroupDimension = "warehouse"
)

func ValidGroupDimension(g GroupDimension) bool {
	switch g {
	case GroupByDay, GroupByMonth, GroupByCustomer, GroupBySupplier, GroupByProduct,
		GroupByCategory, GroupBySalesperson, GroupByBranch, GroupByWarehouse:
		return true
	default:
		return false
	}
}

// SummaryRow is one grouped bucket of a sales or purchase summary report —
// the Key's meaning depends on which GroupDimension produced it (a date
// string for day/month, a name for customer/supplier/product/category/
// salesperson/branch/warehouse).
type SummaryRow struct {
	Key            string
	DocumentCount  int
	GrossAmount    money.Money
	DiscountAmount money.Money
	TaxableAmount  money.Money
	TaxAmount      money.Money
	GrandTotal     money.Money
}

// DocumentDetailRow is one row of an invoice/purchase-document detail
// listing.
type DocumentDetailRow struct {
	DocumentID     uuid.UUID
	DocumentType   string
	DocumentNumber string
	PartyName      string
	IssueDate      time.Time
	Status         string
	GrandTotal     money.Money
}

// GrossProfitRow reports revenue against an APPROXIMATE cost of goods
// sold. stock_movements.unit_cost is only populated for inward movements
// (migrations/0012) — a SALE movement does not snapshot the weighted-
// average cost that was in effect at the moment it was recorded. This
// report therefore uses each product variant's CURRENT stock_balances.
// average_cost as the COGS basis for every historical sale of that
// variant, not the true historical cost at each sale's own point in time.
// For a business whose costs are reasonably stable this is a close
// approximation; for one with volatile purchase costs it will drift.
// Getting an exact historical figure needs stock_movements to snapshot
// unit_cost on SALE rows too (a schema change), which is flagged as a
// follow-up, not built in this pass — see this module's ADR.
type GrossProfitRow struct {
	ProductVariantID uuid.UUID
	ProductName      string
	SKU              string
	QuantitySold     string // decimal.Decimal.String() — avoids importing shopspring here just for a display field
	Revenue          money.Money
	ApproxCOGS       money.Money
	ApproxProfit     money.Money
}

type StockValuationRow struct {
	WarehouseID      uuid.UUID
	WarehouseName    string
	ProductVariantID uuid.UUID
	ProductName      string
	SKU              string
	QuantityOnHand   string
	AverageCost      money.Money
	TotalValue       money.Money
}

type LowStockRow struct {
	WarehouseID      uuid.UUID
	WarehouseName    string
	ProductVariantID uuid.UUID
	ProductName      string
	SKU              string
	QuantityOnHand   string
	ReorderLevel     string
}

type StockMovementRow struct {
	MovementID    uuid.UUID
	OccurredAt    time.Time
	WarehouseName string
	ProductName   string
	SKU           string
	MovementType  string
	Quantity      string
	ReferenceType string
}

type TrialBalanceRow struct {
	AccountCode string
	AccountName string
	AccountType string
	Debit       money.Money
	Credit      money.Money
}

// PartyOutstandingRow is one row of the org-wide receivables or payables
// summary (brief §22's "receivables"/"payables" reports) — reuses
// accounting's own per-party ageing math (internal/modules/accounting
// app.Service.GetAgeing), batched across every party with any AR/AP
// activity, rather than reimplementing the FIFO ageing algorithm here.
type PartyOutstandingRow struct {
	PartyID    uuid.UUID
	PartyName  string
	Phone      string
	Current    money.Money
	Days1To30  money.Money
	Days31To60 money.Money
	Days61To90 money.Money
	Days90Plus money.Money
	Total      money.Money
}

// AccountLedgerRow is one row of a cash book / bank book (brief §22) — the
// account-scoped analogue of accounting's party ledger.
type AccountLedgerRow struct {
	JournalID      uuid.UUID
	JournalDate    time.Time
	SourceType     string
	Description    string
	Debit          money.Money
	Credit         money.Money
	RunningBalance money.Money
}

type HSNSummaryRow struct {
	HSNSACCode    string
	TaxableAmount money.Money
	CGST          money.Money
	SGST          money.Money
	IGST          money.Money
	UTGST         money.Money
	CESS          money.Money
	TotalTax      money.Money
}

type TaxRateSummaryRow struct {
	Rate          string
	TaxableAmount money.Money
	TaxAmount     money.Money
	DocumentCount int
}

// GSTR1Line is one B2B/B2C invoice-level row shaped for GSTR-1 preparation
// (brief §8) — NOT a filing submission, a report the business owner or
// their CA reviews/exports for manual filing or separate government-
// integration software (Stage 8, sandbox only).
type GSTR1Line struct {
	DocumentNumber string
	IssueDate      time.Time
	SupplyType     string // B2B/B2C/EXPORT/SEZ, from tax_documents
	CustomerGSTIN  string // empty for B2C
	PlaceOfSupply  string
	TaxableAmount  money.Money
	CGST           money.Money
	SGST           money.Money
	IGST           money.Money
	CESS           money.Money
	GrandTotal     money.Money
}

// GSTR3BLine is one line of the GSTR-3B summary return (brief §8's
// GSTR-1/3B-oriented, NOT filing, export — same non-goal as GSTR1Line
// above). Label matches the official form's own section numbering
// (e.g. "3.1(a)") so a business owner or their CA can map each row
// directly onto the government form, rather than shaping every box the
// full form has — only the boxes this app actually has data for are
// included; see reporting.Repository.GSTR3B's own doc comment for
// exactly which those are and why the rest are deliberately omitted,
// not shown as a guessed zero.
type GSTR3BLine struct {
	Label         string
	TaxableAmount money.Money
	IGST          money.Money
	CGST          money.Money
	SGST          money.Money
	CESS          money.Money
}

// DashboardSummary is brief §23's card set, assembled from a small,
// deliberately bounded set of indexed queries (docs/adr/0004-dashboard-query-design.md)
// rather than one query per card fired independently from the frontend.
type DashboardSummary struct {
	TodaySales            money.Money
	TodayCollections      money.Money
	TodayPurchases        money.Money
	OutstandingReceivable money.Money
	OutstandingPayable    money.Money
	CurrentStockValue     money.Money
	LowStockCount         int
	OverdueReceivable     money.Money
}

// Repository is the read-only cross-module query surface reports run
// against. Implemented in pg/ with plain parameterized SQL (brief §62) —
// no ORM, matching every other module's data-access convention.
type Repository interface {
	SalesSummary(ctx context.Context, f Filter, group GroupDimension) ([]SummaryRow, error)
	SalesInvoiceDetail(ctx context.Context, f Filter) ([]DocumentDetailRow, error)
	GrossProfit(ctx context.Context, f Filter) ([]GrossProfitRow, error)
	PurchaseSummary(ctx context.Context, f Filter, group GroupDimension) ([]SummaryRow, error)
	PurchaseDetail(ctx context.Context, f Filter) ([]DocumentDetailRow, error)
	StockValuation(ctx context.Context, f Filter) ([]StockValuationRow, error)
	LowStock(ctx context.Context, f Filter) ([]LowStockRow, error)
	StockMovements(ctx context.Context, f Filter) ([]StockMovementRow, error)
	TrialBalance(ctx context.Context, orgID uuid.UUID, asOf time.Time) ([]TrialBalanceRow, error)
	ReceivablesSummaryParties(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
	PayablesSummaryParties(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
	AccountLedger(ctx context.Context, orgID, accountID uuid.UUID, f Filter) ([]AccountLedgerRow, error)
	HSNSummary(ctx context.Context, f Filter) ([]HSNSummaryRow, error)
	TaxRateSummary(ctx context.Context, f Filter) ([]TaxRateSummaryRow, error)
	GSTR1(ctx context.Context, f Filter) ([]GSTR1Line, error)
	// GSTR3B covers only Section 3.1(a)/(b) (outward taxable/zero-rated
	// supplies, from sales' tax_documents — the same data GSTR1 already
	// reads) and Section 4(A)(5) "All other ITC" (from purchases'
	// tax_documents, migrations/0038). Every other box on the real
	// government form — 3.1(c) nil-rated/exempt, 3.1(d) inward reverse
	// charge, 3.1(e) non-GST supplies, 4(A)(1-4) import/ISD/RCM ITC,
	// 4(B) ITC reversed, 4(D) ineligible ITC — has no corresponding data
	// anywhere in this schema (no exempt/nil classification, no reverse-
	// charge or import tracking) and is deliberately not included as a
	// row here rather than shown as a guessed zero that could be
	// mistaken for a confirmed one.
	GSTR3B(ctx context.Context, f Filter) ([]GSTR3BLine, error)
	Dashboard(ctx context.Context, orgID uuid.UUID, today time.Time) (DashboardSummary, error)
	// RecordReminderSent upserts receivable_reminders (migrations/0042):
	// FirstSentAt is set only the first time this (org, party) pair is
	// seen, LastSentAt always moves to sentAt, SentCount always
	// increments. Returns the row as it stands after the upsert.
	RecordReminderSent(ctx context.Context, orgID, partyID uuid.UUID, sentAt time.Time) (ReminderRecord, error)
	// RemindersByParty batch-loads reminder history for the given
	// parties in one query (used by ReceivablesDetailed) — a party with
	// no row yet simply doesn't appear in the returned map.
	RemindersByParty(ctx context.Context, orgID uuid.UUID, partyIDs []uuid.UUID) (map[uuid.UUID]ReminderRecord, error)
}

// ReminderRecord is one receivable_reminders row (migrations/0042).
type ReminderRecord struct {
	PartyID     uuid.UUID
	FirstSentAt time.Time
	LastSentAt  time.Time
	SentCount   int
}

// PartyOutstandingWithContact is ReceivablesDetailed's row shape: a
// PartyOutstandingRow (name/phone/total already resolved) plus this
// party's reminder history, if any has been sent.
type PartyOutstandingWithContact struct {
	PartyOutstandingRow
	FirstReminderSentAt *time.Time
	LastReminderSentAt  *time.Time
	ReminderCount       int
}
