// Package pg implements internal/modules/reporting/domain.Repository
// against PostgreSQL. Every query is parameterized (brief §62 — no
// string-concatenated user input into SQL); the one place a caller-chosen
// value selects SQL structure rather than a bind parameter is the
// GROUP BY dimension, which is validated against domain.ValidGroupDimension
// and switched to one of a fixed set of literal expressions in groupExpr,
// never interpolated raw.
package pg

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
)

type Repo struct{ pool *database.Pool }

func NewRepo(pool *database.Pool) *Repo { return &Repo{pool: pool} }

var _ domain.Repository = (*Repo)(nil)

// --- shared filter-building helper (docs/TODO.md Stage 7: "a shared
// filter-building helper, not copy-pasted WHERE-clause string
// concatenation in every report function") ---

// whereBuilder accumulates parameterized WHERE clauses and their bind
// arguments together, so a clause and its placeholder number never drift
// apart. Every report function below starts from one of these instead of
// hand-building its own WHERE string.
type whereBuilder struct {
	clauses []string
	args    []any
}

// newWhere seeds the builder with "<alias>.organisation_id = $1". Every
// caller must pass the alias of the table it wants scoped — leaving this
// unprefixed ("organisation_id" bare) breaks the instant any query joins
// a second table that also carries an organisation_id column (nearly
// every tenant table does), which Postgres then reports as an ambiguous
// column reference. This was a real bug caught by
// TestReporting_SalesSummary_GroupedByCustomer_MatchesHandComputed and
// three sibling tests on the first real run — see docs/adr's Stage 7
// notes if one exists, or the reporting fork's report, for the concrete
// failure.
func newWhere(orgID uuid.UUID, alias string) *whereBuilder {
	b := &whereBuilder{}
	b.add(alias+".organisation_id", orgID)
	return b
}

func (b *whereBuilder) add(column string, value any) {
	b.args = append(b.args, value)
	b.clauses = append(b.clauses, fmt.Sprintf("%s = $%d", column, len(b.args)))
}

func (b *whereBuilder) addRange(column string, from, to *time.Time) {
	if from != nil {
		b.args = append(b.args, *from)
		b.clauses = append(b.clauses, fmt.Sprintf("%s >= $%d", column, len(b.args)))
	}
	if to != nil {
		b.args = append(b.args, *to)
		b.clauses = append(b.clauses, fmt.Sprintf("%s <= $%d", column, len(b.args)))
	}
}

func (b *whereBuilder) addOptionalUUID(column string, v *uuid.UUID) {
	if v != nil {
		b.add(column, *v)
	}
}

func (b *whereBuilder) addOptionalString(column string, v *string) {
	if v != nil {
		b.add(column, *v)
	}
}

func (b *whereBuilder) sql() string {
	out := ""
	for i, c := range b.clauses {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

// groupExpr maps a validated GroupDimension to a fixed SQL expression and
// join, for the sales/purchase summary queries. tablePrefix is "sd"/"sdl"
// for sales or "pd"/"pdl" for purchases (both document families share the
// same line shape, but sales_documents' date column is issue_date while
// purchase_documents' is document_date — dateColumn carries that).
func groupExpr(g domain.GroupDimension, docAlias, lineAlias, partyJoinAlias, dateColumn string) (selectExpr, groupByExpr, join string) {
	switch g {
	case domain.GroupByDay:
		e := docAlias + "." + dateColumn + "::text"
		return e, e, ""
	case domain.GroupByMonth:
		e := "to_char(" + docAlias + "." + dateColumn + ", 'YYYY-MM')"
		return e, e, ""
	case domain.GroupByCustomer, domain.GroupBySupplier:
		e := "COALESCE(NULLIF(" + partyJoinAlias + ".trade_name, ''), " + partyJoinAlias + ".legal_name)"
		return e, e, ""
	case domain.GroupByProduct:
		return "pr.name", "pr.name", " JOIN product_variants pv ON pv.id = " + lineAlias + ".product_variant_id JOIN products pr ON pr.id = pv.product_id"
	case domain.GroupByCategory:
		return "COALESCE(c.name, 'Uncategorised')", "COALESCE(c.name, 'Uncategorised')",
			" JOIN product_variants pv ON pv.id = " + lineAlias + ".product_variant_id JOIN products pr ON pr.id = pv.product_id LEFT JOIN categories c ON c.id = pr.category_id"
	case domain.GroupBySalesperson:
		return "COALESCE(u.legal_name, 'Unassigned')", "COALESCE(u.legal_name, 'Unassigned')",
			" LEFT JOIN users u ON u.id = " + docAlias + ".salesperson_user_id"
	case domain.GroupByBranch:
		return "br.name", "br.name", " JOIN branches br ON br.id = " + docAlias + ".branch_id"
	case domain.GroupByWarehouse:
		return "wh.name", "wh.name", " JOIN warehouses wh ON wh.id = " + docAlias + ".warehouse_id"
	default:
		// Unreachable if the caller validated via domain.ValidGroupDimension
		// first (app layer does, before this method is ever called).
		e := docAlias + "." + dateColumn + "::text"
		return e, e, ""
	}
}

func inr(d decimal.Decimal) money.Money { return money.MustNew(d, "INR") }

// --- Sales ---

func (r *Repo) SalesSummary(ctx context.Context, f domain.Filter, group domain.GroupDimension) ([]domain.SummaryRow, error) {
	selectExpr, groupByExpr, join := groupExpr(group, "sd", "sdl", "p", "issue_date")
	needsLineJoin := group == domain.GroupByProduct || group == domain.GroupByCategory
	needsPartyJoin := group == domain.GroupByCustomer

	w := newWhere(f.OrganisationID, "sd")
	w.add("sd.status", "FINALIZED")
	w.addRange("sd.issue_date", f.From, f.To)
	w.addOptionalUUID("sd.branch_id", f.BranchID)
	w.addOptionalUUID("sd.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("sd.customer_party_id", f.CustomerPartyID)
	w.addOptionalString("sd.document_type", f.DocumentType)

	from := "sales_documents sd"
	if needsPartyJoin {
		from += " JOIN parties p ON p.id = sd.customer_party_id"
	}
	if needsLineJoin {
		from += " JOIN sales_document_lines sdl ON sdl.sales_document_id = sd.id"
		from += join
	}

	q := fmt.Sprintf(`
		SELECT %s AS key, COUNT(DISTINCT sd.id), COALESCE(SUM(td.total_taxable_amount),0),
		       0::numeric, COALESCE(SUM(td.total_taxable_amount),0), COALESCE(SUM(td.total_tax_amount),0), COALESCE(SUM(sd.grand_total_amount),0)
		FROM %s
		LEFT JOIN tax_documents td ON td.id = sd.tax_document_id
		WHERE %s
		GROUP BY %s
		ORDER BY %s`, selectExpr, from, w.sql(), groupByExpr, groupByExpr)
	return r.scanSummary(ctx, q, w.args)
}

func (r *Repo) scanSummary(ctx context.Context, q string, args []any) ([]domain.SummaryRow, error) {
	rows, err := r.pool.Q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: summary query: %w", err)
	}
	defer rows.Close()
	var out []domain.SummaryRow
	for rows.Next() {
		var s domain.SummaryRow
		var gross, discount, taxable, tax, grand decimal.Decimal
		if err := rows.Scan(&s.Key, &s.DocumentCount, &gross, &discount, &taxable, &tax, &grand); err != nil {
			return nil, fmt.Errorf("reporting: scanning summary row: %w", err)
		}
		s.GrossAmount, s.DiscountAmount, s.TaxableAmount, s.TaxAmount, s.GrandTotal = inr(gross), inr(discount), inr(taxable), inr(tax), inr(grand)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) SalesInvoiceDetail(ctx context.Context, f domain.Filter) ([]domain.DocumentDetailRow, error) {
	w := newWhere(f.OrganisationID, "sd")
	w.addRange("sd.issue_date", f.From, f.To)
	w.addOptionalUUID("sd.branch_id", f.BranchID)
	w.addOptionalUUID("sd.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("sd.customer_party_id", f.CustomerPartyID)
	w.addOptionalString("sd.document_type", f.DocumentType)
	w.addOptionalString("sd.status", f.Status)

	q := fmt.Sprintf(`
		SELECT sd.id, sd.document_type, sd.document_number, COALESCE(NULLIF(p.trade_name,''), p.legal_name),
		       sd.issue_date, sd.status, COALESCE(sd.grand_total_amount, 0)
		FROM sales_documents sd JOIN parties p ON p.id = sd.customer_party_id
		WHERE %s ORDER BY sd.issue_date DESC, sd.document_number DESC`, w.sql())
	return r.scanDetail(ctx, q, w.args)
}

func (r *Repo) scanDetail(ctx context.Context, q string, args []any) ([]domain.DocumentDetailRow, error) {
	rows, err := r.pool.Q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: detail query: %w", err)
	}
	defer rows.Close()
	var out []domain.DocumentDetailRow
	for rows.Next() {
		var d domain.DocumentDetailRow
		var grand decimal.Decimal
		if err := rows.Scan(&d.DocumentID, &d.DocumentType, &d.DocumentNumber, &d.PartyName, &d.IssueDate, &d.Status, &grand); err != nil {
			return nil, fmt.Errorf("reporting: scanning detail row: %w", err)
		}
		d.GrandTotal = inr(grand)
		out = append(out, d)
	}
	return out, rows.Err()
}

// GrossProfit — see domain.GrossProfitRow's doc comment for the
// approximate-COGS caveat (current average_cost, not historical).
func (r *Repo) GrossProfit(ctx context.Context, f domain.Filter) ([]domain.GrossProfitRow, error) {
	w := newWhere(f.OrganisationID, "sd")
	w.add("sd.status", "FINALIZED")
	w.addRange("sd.issue_date", f.From, f.To)
	w.addOptionalUUID("sd.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("sdl.product_variant_id", f.ProductVariantID)

	q := fmt.Sprintf(`
		SELECT pv.id, pr.name, pv.sku_code, SUM(sdl.quantity),
		       SUM(sdl.line_total_amount),
		       COALESCE(SUM(sdl.quantity * sb.average_cost), 0)
		FROM sales_document_lines sdl
		JOIN sales_documents sd ON sd.id = sdl.sales_document_id
		JOIN product_variants pv ON pv.id = sdl.product_variant_id
		JOIN products pr ON pr.id = pv.product_id
		LEFT JOIN stock_balances sb ON sb.organisation_id = sd.organisation_id
		    AND sb.warehouse_id = sd.warehouse_id AND sb.product_variant_id = sdl.product_variant_id
		WHERE %s
		GROUP BY pv.id, pr.name, pv.sku_code
		ORDER BY pr.name`, w.sql())
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: gross profit query: %w", err)
	}
	defer rows.Close()
	var out []domain.GrossProfitRow
	for rows.Next() {
		var g domain.GrossProfitRow
		var qty, revenue, cogs decimal.Decimal
		if err := rows.Scan(&g.ProductVariantID, &g.ProductName, &g.SKU, &qty, &revenue, &cogs); err != nil {
			return nil, fmt.Errorf("reporting: scanning gross profit row: %w", err)
		}
		g.QuantitySold = qty.String()
		g.Revenue = inr(revenue)
		g.ApproxCOGS = inr(cogs)
		g.ApproxProfit = inr(revenue.Sub(cogs))
		out = append(out, g)
	}
	return out, rows.Err()
}

// --- Purchases ---

func (r *Repo) PurchaseSummary(ctx context.Context, f domain.Filter, group domain.GroupDimension) ([]domain.SummaryRow, error) {
	selectExpr, groupByExpr, join := groupExpr(group, "pd", "pdl", "p", "document_date")
	needsLineJoin := group == domain.GroupByProduct || group == domain.GroupByCategory
	needsPartyJoin := group == domain.GroupBySupplier

	w := newWhere(f.OrganisationID, "pd")
	w.add("pd.status", "FINALIZED")
	w.addRange("pd.document_date", f.From, f.To)
	w.addOptionalUUID("pd.branch_id", f.BranchID)
	w.addOptionalUUID("pd.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("pd.supplier_party_id", f.SupplierPartyID)
	w.addOptionalString("pd.document_type", f.DocumentType)

	from := "purchase_documents pd"
	if needsPartyJoin {
		from += " JOIN parties p ON p.id = pd.supplier_party_id"
	}
	if needsLineJoin {
		from += " JOIN purchase_document_lines pdl ON pdl.purchase_document_id = pd.id"
		from += join
	} else {
		from += " JOIN purchase_document_lines pdl ON pdl.purchase_document_id = pd.id"
	}
	// purchases has no per-line tax breakdown yet (Stage 4/6 ADRs — never
	// wired through the tax engine), so taxable/tax columns are 0 here;
	// gross/grand total come from summed line totals.
	q := fmt.Sprintf(`
		SELECT %s AS key, COUNT(DISTINCT pd.id), COALESCE(SUM(pdl.line_total_amount),0),
		       0::numeric, COALESCE(SUM(pdl.line_total_amount),0), 0::numeric, COALESCE(SUM(pdl.line_total_amount),0)
		FROM %s
		WHERE %s
		GROUP BY %s
		ORDER BY %s`, selectExpr, from, w.sql(), groupByExpr, groupByExpr)
	return r.scanSummary(ctx, q, w.args)
}

func (r *Repo) PurchaseDetail(ctx context.Context, f domain.Filter) ([]domain.DocumentDetailRow, error) {
	w := newWhere(f.OrganisationID, "pd")
	w.addRange("pd.document_date", f.From, f.To)
	w.addOptionalUUID("pd.branch_id", f.BranchID)
	w.addOptionalUUID("pd.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("pd.supplier_party_id", f.SupplierPartyID)
	w.addOptionalString("pd.document_type", f.DocumentType)
	w.addOptionalString("pd.status", f.Status)

	q := fmt.Sprintf(`
		SELECT pd.id, pd.document_type, pd.document_number, COALESCE(NULLIF(p.trade_name,''), p.legal_name),
		       pd.document_date, pd.status,
		       COALESCE((SELECT SUM(pdl.line_total_amount) FROM purchase_document_lines pdl WHERE pdl.purchase_document_id = pd.id), 0)
		FROM purchase_documents pd JOIN parties p ON p.id = pd.supplier_party_id
		WHERE %s ORDER BY pd.document_date DESC, pd.document_number DESC`, w.sql())
	return r.scanDetail(ctx, q, w.args)
}

// --- Inventory ---

func (r *Repo) StockValuation(ctx context.Context, f domain.Filter) ([]domain.StockValuationRow, error) {
	w := newWhere(f.OrganisationID, "sb")
	w.addOptionalUUID("sb.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("sb.product_variant_id", f.ProductVariantID)

	q := fmt.Sprintf(`
		SELECT sb.warehouse_id, wh.name, sb.product_variant_id, pr.name, pv.sku_code, sb.quantity_on_hand, sb.average_cost
		FROM stock_balances sb
		JOIN warehouses wh ON wh.id = sb.warehouse_id
		JOIN product_variants pv ON pv.id = sb.product_variant_id
		JOIN products pr ON pr.id = pv.product_id
		WHERE sb.organisation_id = $1 %s
		ORDER BY wh.name, pr.name`,
		condAppend(w))
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: stock valuation query: %w", err)
	}
	defer rows.Close()
	var out []domain.StockValuationRow
	for rows.Next() {
		var s domain.StockValuationRow
		var qty, cost decimal.Decimal
		if err := rows.Scan(&s.WarehouseID, &s.WarehouseName, &s.ProductVariantID, &s.ProductName, &s.SKU, &qty, &cost); err != nil {
			return nil, fmt.Errorf("reporting: scanning stock valuation row: %w", err)
		}
		s.QuantityOnHand = qty.String()
		s.AverageCost = inr(cost)
		s.TotalValue = inr(qty.Mul(cost))
		out = append(out, s)
	}
	return out, rows.Err()
}

// condAppend renders w's non-organisation clauses (index 2+) as an
// "AND ..." suffix for queries built with newWhere+addOptionalUUID where
// the first clause (organisation_id = $1) is already written literally in
// the caller's SQL template (so the alias, e.g. "sb.organisation_id" vs
// bare "organisation_id", can differ per query without whereBuilder
// needing to know the table alias up front).
func condAppend(w *whereBuilder) string {
	if len(w.clauses) <= 1 {
		return ""
	}
	out := ""
	for _, c := range w.clauses[1:] {
		out += " AND " + c
	}
	return out
}

func (r *Repo) LowStock(ctx context.Context, f domain.Filter) ([]domain.LowStockRow, error) {
	w := newWhere(f.OrganisationID, "sb")
	w.addOptionalUUID("sb.warehouse_id", f.WarehouseID)

	q := fmt.Sprintf(`
		SELECT sb.warehouse_id, wh.name, sb.product_variant_id, pr.name, pv.sku_code, sb.quantity_on_hand, sp.reorder_level
		FROM stock_balances sb
		JOIN stock_policies sp ON sp.organisation_id = sb.organisation_id
		    AND sp.warehouse_id = sb.warehouse_id AND sp.product_variant_id = sb.product_variant_id
		JOIN warehouses wh ON wh.id = sb.warehouse_id
		JOIN product_variants pv ON pv.id = sb.product_variant_id
		JOIN products pr ON pr.id = pv.product_id
		WHERE sb.organisation_id = $1 %s AND sb.quantity_on_hand <= sp.reorder_level
		ORDER BY wh.name, pr.name`, condAppend(w))
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: low stock query: %w", err)
	}
	defer rows.Close()
	var out []domain.LowStockRow
	for rows.Next() {
		var l domain.LowStockRow
		var qty, reorder decimal.Decimal
		if err := rows.Scan(&l.WarehouseID, &l.WarehouseName, &l.ProductVariantID, &l.ProductName, &l.SKU, &qty, &reorder); err != nil {
			return nil, fmt.Errorf("reporting: scanning low stock row: %w", err)
		}
		l.QuantityOnHand, l.ReorderLevel = qty.String(), reorder.String()
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repo) StockMovements(ctx context.Context, f domain.Filter) ([]domain.StockMovementRow, error) {
	w := newWhere(f.OrganisationID, "sm")
	w.addRange("sm.created_at", f.From, f.To)
	w.addOptionalUUID("sm.warehouse_id", f.WarehouseID)
	w.addOptionalUUID("sm.product_variant_id", f.ProductVariantID)

	q := fmt.Sprintf(`
		SELECT sm.id, sm.created_at, wh.name, pr.name, pv.sku_code, sm.movement_type, sm.base_quantity, COALESCE(sm.reference_type, '')
		FROM stock_movements sm
		JOIN warehouses wh ON wh.id = sm.warehouse_id
		JOIN product_variants pv ON pv.id = sm.product_variant_id
		JOIN products pr ON pr.id = pv.product_id
		WHERE %s
		ORDER BY sm.created_at DESC
		LIMIT 5000`, w.sql())
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: stock movement query: %w", err)
	}
	defer rows.Close()
	var out []domain.StockMovementRow
	for rows.Next() {
		var s domain.StockMovementRow
		var qty decimal.Decimal
		if err := rows.Scan(&s.MovementID, &s.OccurredAt, &s.WarehouseName, &s.ProductName, &s.SKU, &s.MovementType, &qty, &s.ReferenceType); err != nil {
			return nil, fmt.Errorf("reporting: scanning stock movement row: %w", err)
		}
		s.Quantity = qty.String()
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- Accounting ---

func (r *Repo) TrialBalance(ctx context.Context, orgID uuid.UUID, asOf time.Time) ([]domain.TrialBalanceRow, error) {
	const q = `
		SELECT a.code, a.name, a.account_type,
		       COALESCE(SUM(jl.debit_amount), 0), COALESCE(SUM(jl.credit_amount), 0)
		FROM accounts a
		LEFT JOIN journal_lines jl ON jl.account_id = a.id AND jl.organisation_id = a.organisation_id
		LEFT JOIN journals j ON j.id = jl.journal_id AND j.journal_date <= $2
		WHERE a.organisation_id = $1
		GROUP BY a.code, a.name, a.account_type
		HAVING COALESCE(SUM(jl.debit_amount), 0) <> 0 OR COALESCE(SUM(jl.credit_amount), 0) <> 0
		ORDER BY a.code`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, asOf)
	if err != nil {
		return nil, fmt.Errorf("reporting: trial balance query: %w", err)
	}
	defer rows.Close()
	var out []domain.TrialBalanceRow
	for rows.Next() {
		var t domain.TrialBalanceRow
		var debit, credit decimal.Decimal
		if err := rows.Scan(&t.AccountCode, &t.AccountName, &t.AccountType, &debit, &credit); err != nil {
			return nil, fmt.Errorf("reporting: scanning trial balance row: %w", err)
		}
		t.Debit, t.Credit = inr(debit), inr(credit)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReceivablesSummaryParties/PayablesSummaryParties return the party IDs
// with any AR/AP journal-line activity, for the app layer to batch through
// accounting's existing per-party GetAgeing (docs/architecture.md §2 —
// reporting doesn't reimplement accounting's FIFO ageing algorithm).
func (r *Repo) ReceivablesSummaryParties(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	return r.distinctPartyIDs(ctx, orgID, "1100") // CodeAccountsReceivable
}

func (r *Repo) PayablesSummaryParties(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	return r.distinctPartyIDs(ctx, orgID, "2000") // CodeAccountsPayable
}

func (r *Repo) distinctPartyIDs(ctx context.Context, orgID uuid.UUID, accountCode string) ([]uuid.UUID, error) {
	const q = `
		SELECT DISTINCT jl.party_id FROM journal_lines jl
		JOIN accounts a ON a.id = jl.account_id
		WHERE jl.organisation_id = $1 AND a.code = $2 AND jl.party_id IS NOT NULL`
	rows, err := r.pool.Q(ctx).Query(ctx, q, orgID, accountCode)
	if err != nil {
		return nil, fmt.Errorf("reporting: distinct party ids query: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("reporting: scanning party id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repo) AccountLedger(ctx context.Context, orgID, accountID uuid.UUID, f domain.Filter) ([]domain.AccountLedgerRow, error) {
	w := newWhere(orgID, "jl")
	w.add("jl.account_id", accountID)
	w.addRange("j.journal_date", f.From, f.To)

	q := fmt.Sprintf(`
		SELECT j.id, j.journal_date, j.source_type, COALESCE(j.description,''), jl.debit_amount, jl.credit_amount
		FROM journal_lines jl JOIN journals j ON j.id = jl.journal_id
		WHERE %s
		ORDER BY j.journal_date, j.created_at`, w.sql())
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: account ledger query: %w", err)
	}
	defer rows.Close()
	var out []domain.AccountLedgerRow
	running := decimal.Zero
	for rows.Next() {
		var a domain.AccountLedgerRow
		var debit, credit decimal.Decimal
		if err := rows.Scan(&a.JournalID, &a.JournalDate, &a.SourceType, &a.Description, &debit, &credit); err != nil {
			return nil, fmt.Errorf("reporting: scanning account ledger row: %w", err)
		}
		running = running.Add(debit).Sub(credit)
		a.Debit, a.Credit, a.RunningBalance = inr(debit), inr(credit), inr(running)
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- Tax ---

func (r *Repo) HSNSummary(ctx context.Context, f domain.Filter) ([]domain.HSNSummaryRow, error) {
	w := newWhere(f.OrganisationID, "tl")
	w.addRange("td.document_date", f.From, f.To)
	w.addOptionalString("tl.hsn_sac_code", f.HSNSACCode)

	q := fmt.Sprintf(`
		SELECT tl.hsn_sac_code, SUM(tl.taxable_amount),
		       COALESCE(SUM(tc.amount) FILTER (WHERE tc.component_type = 'CGST'), 0),
		       COALESCE(SUM(tc.amount) FILTER (WHERE tc.component_type = 'SGST'), 0),
		       COALESCE(SUM(tc.amount) FILTER (WHERE tc.component_type = 'IGST'), 0),
		       COALESCE(SUM(tc.amount) FILTER (WHERE tc.component_type = 'UTGST'), 0),
		       COALESCE(SUM(tc.amount) FILTER (WHERE tc.component_type = 'CESS'), 0),
		       SUM(tl.total_tax_amount)
		FROM tax_lines tl
		JOIN tax_documents td ON td.id = tl.tax_document_id
		LEFT JOIN tax_components tc ON tc.tax_line_id = tl.id
		WHERE %s
		GROUP BY tl.hsn_sac_code
		ORDER BY tl.hsn_sac_code`, w.sql())
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: HSN summary query: %w", err)
	}
	defer rows.Close()
	var out []domain.HSNSummaryRow
	for rows.Next() {
		var h domain.HSNSummaryRow
		var taxable, cgst, sgst, igst, utgst, cess, total decimal.Decimal
		if err := rows.Scan(&h.HSNSACCode, &taxable, &cgst, &sgst, &igst, &utgst, &cess, &total); err != nil {
			return nil, fmt.Errorf("reporting: scanning HSN summary row: %w", err)
		}
		h.TaxableAmount, h.CGST, h.SGST, h.IGST, h.UTGST, h.CESS, h.TotalTax = inr(taxable), inr(cgst), inr(sgst), inr(igst), inr(utgst), inr(cess), inr(total)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *Repo) TaxRateSummary(ctx context.Context, f domain.Filter) ([]domain.TaxRateSummaryRow, error) {
	w := newWhere(f.OrganisationID, "tc")
	w.addRange("td.document_date", f.From, f.To)

	q := fmt.Sprintf(`
		SELECT tc.rate::text, SUM(tl.taxable_amount), SUM(tc.amount), COUNT(DISTINCT td.id)
		FROM tax_components tc
		JOIN tax_lines tl ON tl.id = tc.tax_line_id
		JOIN tax_documents td ON td.id = tl.tax_document_id
		WHERE %s AND tc.component_type IN ('CGST','IGST') -- one of the two per line, avoids double-counting the intra-state CGST+SGST split
		GROUP BY tc.rate
		ORDER BY tc.rate`, w.sql())
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: tax rate summary query: %w", err)
	}
	defer rows.Close()
	var out []domain.TaxRateSummaryRow
	for rows.Next() {
		var t domain.TaxRateSummaryRow
		var taxable, tax decimal.Decimal
		if err := rows.Scan(&t.Rate, &taxable, &tax, &t.DocumentCount); err != nil {
			return nil, fmt.Errorf("reporting: scanning tax rate summary row: %w", err)
		}
		t.TaxableAmount, t.TaxAmount = inr(taxable), inr(tax)
		out = append(out, t)
	}
	return out, rows.Err()
}

// GSTR1 — see domain.GSTR1Line's doc comment: preparation only, not filing.
func (r *Repo) GSTR1(ctx context.Context, f domain.Filter) ([]domain.GSTR1Line, error) {
	w := newWhere(f.OrganisationID, "sd")
	w.add("sd.status", "FINALIZED")
	w.addRange("sd.issue_date", f.From, f.To)

	q := fmt.Sprintf(`
		SELECT sd.document_number, sd.issue_date, td.supply_type,
		       COALESCE((SELECT ptr.registration_number FROM party_tax_registrations ptr WHERE ptr.id = sd.customer_tax_registration_id), ''),
		       td.place_of_supply_code, td.total_taxable_amount,
		       COALESCE((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'CGST'), 0),
		       COALESCE((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'SGST'), 0),
		       COALESCE((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'IGST'), 0),
		       COALESCE((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'CESS'), 0),
		       td.grand_total
		FROM sales_documents sd JOIN tax_documents td ON td.id = sd.tax_document_id
		WHERE %s
		ORDER BY sd.issue_date, sd.document_number`, w.sql())
	rows, err := r.pool.Q(ctx).Query(ctx, q, w.args...)
	if err != nil {
		return nil, fmt.Errorf("reporting: GSTR-1 query: %w", err)
	}
	defer rows.Close()
	var out []domain.GSTR1Line
	for rows.Next() {
		var g domain.GSTR1Line
		var taxable, cgst, sgst, igst, cess, grand decimal.Decimal
		if err := rows.Scan(&g.DocumentNumber, &g.IssueDate, &g.SupplyType, &g.CustomerGSTIN, &g.PlaceOfSupply,
			&taxable, &cgst, &sgst, &igst, &cess, &grand); err != nil {
			return nil, fmt.Errorf("reporting: scanning GSTR-1 row: %w", err)
		}
		g.TaxableAmount, g.CGST, g.SGST, g.IGST, g.CESS, g.GrandTotal = inr(taxable), inr(cgst), inr(sgst), inr(igst), inr(cess), inr(grand)
		out = append(out, g)
	}
	return out, rows.Err()
}

// GSTR3B assembles up to 3 rows — see domain.Repository.GSTR3B's own
// doc comment for exactly which boxes these are and which are
// deliberately omitted. Two independent queries (outward from
// sales_documents/tax_documents, ITC from purchase_documents/
// tax_documents) rather than one UNIONed query — the two sides filter
// on different parent tables and different date columns (issue_date vs
// document_date), and there is no shared row shape worth forcing into
// one SELECT.
func (r *Repo) GSTR3B(ctx context.Context, f domain.Filter) ([]domain.GSTR3BLine, error) {
	outward, err := r.gstr3bOutward(ctx, f)
	if err != nil {
		return nil, err
	}
	itc, err := r.gstr3bITC(ctx, f)
	if err != nil {
		return nil, err
	}
	var out []domain.GSTR3BLine
	out = append(out, outward...)
	out = append(out, itc)
	return out, nil
}

func (r *Repo) gstr3bOutward(ctx context.Context, f domain.Filter) ([]domain.GSTR3BLine, error) {
	w := newWhere(f.OrganisationID, "sd")
	w.add("sd.status", "FINALIZED")
	w.addRange("sd.issue_date", f.From, f.To)
	q := fmt.Sprintf(`
		SELECT
			COALESCE(SUM(td.total_taxable_amount) FILTER (WHERE td.supply_type NOT IN ('EXPORT', 'SEZ')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'IGST')) FILTER (WHERE td.supply_type NOT IN ('EXPORT', 'SEZ')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'CGST')) FILTER (WHERE td.supply_type NOT IN ('EXPORT', 'SEZ')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'SGST')) FILTER (WHERE td.supply_type NOT IN ('EXPORT', 'SEZ')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'CESS')) FILTER (WHERE td.supply_type NOT IN ('EXPORT', 'SEZ')), 0),
			COALESCE(SUM(td.total_taxable_amount) FILTER (WHERE td.supply_type IN ('EXPORT', 'SEZ')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'IGST')) FILTER (WHERE td.supply_type IN ('EXPORT', 'SEZ')), 0)
		FROM sales_documents sd JOIN tax_documents td ON td.id = sd.tax_document_id
		WHERE %s`, w.sql())
	var taxable, igst, cgst, sgst, cess, zeroTaxable, zeroIGST decimal.Decimal
	row := r.pool.Q(ctx).QueryRow(ctx, q, w.args...)
	if err := row.Scan(&taxable, &igst, &cgst, &sgst, &cess, &zeroTaxable, &zeroIGST); err != nil {
		return nil, fmt.Errorf("reporting: GSTR-3B outward query: %w", err)
	}
	return []domain.GSTR3BLine{
		{Label: "3.1(a) Outward taxable supplies", TaxableAmount: inr(taxable), IGST: inr(igst), CGST: inr(cgst), SGST: inr(sgst), CESS: inr(cess)},
		{Label: "3.1(b) Outward taxable supplies (zero rated)", TaxableAmount: inr(zeroTaxable), IGST: inr(zeroIGST), CGST: inr(decimal.Zero), SGST: inr(decimal.Zero), CESS: inr(decimal.Zero)},
	}, nil
}

func (r *Repo) gstr3bITC(ctx context.Context, f domain.Filter) (domain.GSTR3BLine, error) {
	w := newWhere(f.OrganisationID, "pd")
	w.add("pd.status", "FINALIZED")
	w.addRange("pd.document_date", f.From, f.To)
	q := fmt.Sprintf(`
		SELECT
			COALESCE(SUM(td.total_taxable_amount), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'IGST')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'CGST')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'SGST')), 0),
			COALESCE(SUM((SELECT SUM(tc.amount) FROM tax_components tc JOIN tax_lines tl ON tl.id = tc.tax_line_id WHERE tl.tax_document_id = td.id AND tc.component_type = 'CESS')), 0)
		FROM purchase_documents pd JOIN tax_documents td ON td.id = pd.tax_document_id
		WHERE %s`, w.sql())
	var taxable, igst, cgst, sgst, cess decimal.Decimal
	row := r.pool.Q(ctx).QueryRow(ctx, q, w.args...)
	if err := row.Scan(&taxable, &igst, &cgst, &sgst, &cess); err != nil {
		return domain.GSTR3BLine{}, fmt.Errorf("reporting: GSTR-3B ITC query: %w", err)
	}
	return domain.GSTR3BLine{Label: "4(A)(5) All other ITC", TaxableAmount: inr(taxable), IGST: inr(igst), CGST: inr(cgst), SGST: inr(sgst), CESS: inr(cess)}, nil
}

// --- Dashboard (docs/adr/0004-dashboard-query-design.md) ---

func (r *Repo) Dashboard(ctx context.Context, orgID uuid.UUID, today time.Time) (domain.DashboardSummary, error) {
	dayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	const q = `
		SELECT
			COALESCE((SELECT SUM(grand_total_amount) FROM sales_documents
				WHERE organisation_id = $1 AND status = 'FINALIZED' AND issue_date = $2::date), 0) AS today_sales,
			COALESCE((SELECT SUM(rc.amount) FROM receipts rc
				WHERE rc.organisation_id = $1 AND rc.received_at >= $3), 0) AS today_collections,
			COALESCE((SELECT SUM(pdl.line_total_amount) FROM purchase_documents pd
				JOIN purchase_document_lines pdl ON pdl.purchase_document_id = pd.id
				WHERE pd.organisation_id = $1 AND pd.status = 'FINALIZED' AND pd.document_date = $2::date), 0) AS today_purchases,
			COALESCE((SELECT SUM(jl.debit_amount) - SUM(jl.credit_amount) FROM journal_lines jl
				JOIN accounts a ON a.id = jl.account_id
				WHERE jl.organisation_id = $1 AND a.code = '1100'), 0) AS outstanding_receivable,
			COALESCE((SELECT SUM(jl.credit_amount) - SUM(jl.debit_amount) FROM journal_lines jl
				JOIN accounts a ON a.id = jl.account_id
				WHERE jl.organisation_id = $1 AND a.code = '2000'), 0) AS outstanding_payable,
			COALESCE((SELECT SUM(quantity_on_hand * average_cost) FROM stock_balances
				WHERE organisation_id = $1), 0) AS current_stock_value,
			COALESCE((SELECT COUNT(*) FROM stock_balances sb JOIN stock_policies sp
				ON sp.organisation_id = sb.organisation_id AND sp.warehouse_id = sb.warehouse_id AND sp.product_variant_id = sb.product_variant_id
				WHERE sb.organisation_id = $1 AND sb.quantity_on_hand <= sp.reorder_level), 0) AS low_stock_count,
			COALESCE((SELECT SUM(sd.grand_total_amount) FROM sales_documents sd
				WHERE sd.organisation_id = $1 AND sd.status = 'FINALIZED' AND sd.due_date IS NOT NULL AND sd.due_date < $2::date), 0) AS overdue_receivable`
	var todaySales, todayCollections, todayPurchases, outstandingRec, outstandingPay, stockValue, overdue decimal.Decimal
	var lowStockCount int
	err := r.pool.Q(ctx).QueryRow(ctx, q, orgID, dayStart, dayStart).Scan(
		&todaySales, &todayCollections, &todayPurchases, &outstandingRec, &outstandingPay, &stockValue, &lowStockCount, &overdue)
	if err != nil {
		return domain.DashboardSummary{}, fmt.Errorf("reporting: dashboard query: %w", err)
	}
	return domain.DashboardSummary{
		TodaySales:            inr(todaySales),
		TodayCollections:      inr(todayCollections),
		TodayPurchases:        inr(todayPurchases),
		OutstandingReceivable: inr(outstandingRec),
		OutstandingPayable:    inr(outstandingPay),
		CurrentStockValue:     inr(stockValue),
		LowStockCount:         lowStockCount,
		OverdueReceivable:     inr(overdue),
	}, nil
}
