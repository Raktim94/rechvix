package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/reporting/domain"
	"rechvix/internal/platform/export"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/money"
)

const fixed = money.RoundHalfUp

func (h *Handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Dashboard(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func groupDimension(r *http.Request) domain.GroupDimension {
	g := r.URL.Query().Get("group_by")
	if g == "" {
		return domain.GroupByDay
	}
	return domain.GroupDimension(g)
}

func (h *Handlers) salesSummary(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.SalesSummary(r.Context(), principal(r), f, groupDimension(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Sales Summary", Headers: []string{"Key", "Documents", "Taxable", "Tax", "Grand Total"}}
	for _, s := range rows {
		t.Rows = append(t.Rows, []string{s.Key, strconv.Itoa(s.DocumentCount), s.TaxableAmount.StringFixed(fixed), s.TaxAmount.StringFixed(fixed), s.GrandTotal.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) salesInvoices(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.SalesInvoiceDetail(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Sales Invoices", Headers: []string{"Document Number", "Type", "Customer", "Issue Date", "Status", "Grand Total"}}
	for _, d := range rows {
		t.Rows = append(t.Rows, []string{d.DocumentNumber, d.DocumentType, d.PartyName, d.IssueDate.Format("2006-01-02"), d.Status, d.GrandTotal.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) grossProfit(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.GrossProfit(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Gross Profit (approx. COGS — see docs)", Headers: []string{"Product", "SKU", "Qty Sold", "Revenue", "Approx COGS", "Approx Profit"}}
	for _, g := range rows {
		t.Rows = append(t.Rows, []string{g.ProductName, g.SKU, g.QuantitySold, g.Revenue.StringFixed(fixed), g.ApproxCOGS.StringFixed(fixed), g.ApproxProfit.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) purchaseSummary(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.PurchaseSummary(r.Context(), principal(r), f, groupDimension(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Purchase Summary", Headers: []string{"Key", "Documents", "Total"}}
	for _, s := range rows {
		t.Rows = append(t.Rows, []string{s.Key, strconv.Itoa(s.DocumentCount), s.GrandTotal.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) purchaseDocuments(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.PurchaseDetail(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Purchase Documents", Headers: []string{"Document Number", "Type", "Supplier", "Date", "Status", "Total"}}
	for _, d := range rows {
		t.Rows = append(t.Rows, []string{d.DocumentNumber, d.DocumentType, d.PartyName, d.IssueDate.Format("2006-01-02"), d.Status, d.GrandTotal.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) stockValuation(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.StockValuation(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Stock Valuation", Headers: []string{"Warehouse", "Product", "SKU", "Qty On Hand", "Avg Cost", "Total Value"}}
	for _, s := range rows {
		t.Rows = append(t.Rows, []string{s.WarehouseName, s.ProductName, s.SKU, s.QuantityOnHand, s.AverageCost.StringFixed(fixed), s.TotalValue.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) lowStock(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.LowStock(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Low Stock", Headers: []string{"Warehouse", "Product", "SKU", "Qty On Hand", "Reorder Level"}}
	for _, l := range rows {
		t.Rows = append(t.Rows, []string{l.WarehouseName, l.ProductName, l.SKU, l.QuantityOnHand, l.ReorderLevel})
	}
	writeTable(w, r, t)
}

func (h *Handlers) stockMovements(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.StockMovements(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Stock Movements", Headers: []string{"Date", "Warehouse", "Product", "SKU", "Type", "Quantity", "Reference"}}
	for _, m := range rows {
		t.Rows = append(t.Rows, []string{m.OccurredAt.Format(time.RFC3339), m.WarehouseName, m.ProductName, m.SKU, m.MovementType, m.Quantity, m.ReferenceType})
	}
	writeTable(w, r, t)
}

func (h *Handlers) trialBalance(w http.ResponseWriter, r *http.Request) {
	asOf := parseAsOf(r)
	rows, err := h.svc.TrialBalance(r.Context(), principal(r), asOf)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Trial Balance as of " + asOf.Format("2006-01-02"), Headers: []string{"Code", "Account", "Type", "Debit", "Credit"}}
	for _, tb := range rows {
		t.Rows = append(t.Rows, []string{tb.AccountCode, tb.AccountName, tb.AccountType, tb.Debit.StringFixed(fixed), tb.Credit.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) receivables(w http.ResponseWriter, r *http.Request) {
	asOf := parseAsOf(r)
	rows, err := h.svc.ReceivablesSummary(r.Context(), principal(r), asOf)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeAgeingTable(w, r, "Receivables as of "+asOf.Format("2006-01-02"), rows)
}

// receivablesDetailed is the "Receivables (who owes you)" panel's data
// source — same underlying ageing as receivables above, but as plain
// JSON with name/phone/reminder history attached per row instead of the
// generic exportable ageing table.
func (h *Handlers) receivablesDetailed(w http.ResponseWriter, r *http.Request) {
	asOf := parseAsOf(r)
	rows, err := h.svc.ReceivablesDetailed(r.Context(), principal(r), asOf)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"parties": rows})
}

// recordReminder logs that a WhatsApp payment reminder was just sent to
// this party — called right after the frontend opens the wa.me link
// (there's no way to confirm actual delivery, only that the send was
// initiated, same honesty level as every other WhatsApp integration in
// this codebase).
func (h *Handlers) recordReminder(w http.ResponseWriter, r *http.Request) {
	partyID, err := uuid.Parse(chi.URLParam(r, "partyId"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "partyId must be a UUID."))
		return
	}
	rec, err := h.svc.RecordReminderSent(r.Context(), principal(r), partyID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h *Handlers) payables(w http.ResponseWriter, r *http.Request) {
	asOf := parseAsOf(r)
	rows, err := h.svc.PayablesSummary(r.Context(), principal(r), asOf)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeAgeingTable(w, r, "Payables as of "+asOf.Format("2006-01-02"), rows)
}

func writeAgeingTable(w http.ResponseWriter, r *http.Request, title string, rows []domain.PartyOutstandingRow) {
	t := export.Table{Title: title, Headers: []string{"Party ID", "Current", "1-30", "31-60", "61-90", "90+", "Total"}}
	for _, a := range rows {
		t.Rows = append(t.Rows, []string{a.PartyID.String(), a.Current.StringFixed(fixed), a.Days1To30.StringFixed(fixed),
			a.Days31To60.StringFixed(fixed), a.Days61To90.StringFixed(fixed), a.Days90Plus.StringFixed(fixed), a.Total.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) accountLedger(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "Invalid account id."))
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.AccountLedger(r.Context(), principal(r), id, f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Account Ledger", Headers: []string{"Date", "Source", "Description", "Debit", "Credit", "Balance"}}
	for _, a := range rows {
		t.Rows = append(t.Rows, []string{a.JournalDate.Format("2006-01-02"), a.SourceType, a.Description, a.Debit.StringFixed(fixed), a.Credit.StringFixed(fixed), a.RunningBalance.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) hsnSummary(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.HSNSummary(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "HSN Summary", Headers: []string{"HSN/SAC", "Taxable", "CGST", "SGST", "IGST", "UTGST", "CESS", "Total Tax"}}
	for _, hs := range rows {
		t.Rows = append(t.Rows, []string{hs.HSNSACCode, hs.TaxableAmount.StringFixed(fixed), hs.CGST.StringFixed(fixed),
			hs.SGST.StringFixed(fixed), hs.IGST.StringFixed(fixed), hs.UTGST.StringFixed(fixed), hs.CESS.StringFixed(fixed), hs.TotalTax.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) taxRateSummary(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.TaxRateSummary(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "Tax Rate Summary", Headers: []string{"Rate %", "Taxable", "Tax", "Documents"}}
	for _, tr := range rows {
		t.Rows = append(t.Rows, []string{tr.Rate, tr.TaxableAmount.StringFixed(fixed), tr.TaxAmount.StringFixed(fixed), strconv.Itoa(tr.DocumentCount)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) gstr1(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.GSTR1(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "GSTR-1 Preparation (NOT a filing submission)",
		Headers: []string{"Document Number", "Date", "Supply Type", "Customer GSTIN", "Place of Supply", "Taxable", "CGST", "SGST", "IGST", "CESS", "Grand Total"}}
	for _, g := range rows {
		t.Rows = append(t.Rows, []string{g.DocumentNumber, g.IssueDate.Format("2006-01-02"), g.SupplyType, g.CustomerGSTIN, g.PlaceOfSupply,
			g.TaxableAmount.StringFixed(fixed), g.CGST.StringFixed(fixed), g.SGST.StringFixed(fixed), g.IGST.StringFixed(fixed), g.CESS.StringFixed(fixed), g.GrandTotal.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func (h *Handlers) gstr3b(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows, err := h.svc.GSTR3B(r.Context(), principal(r), f)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	t := export.Table{Title: "GSTR-3B Preparation (NOT a filing submission — only the boxes this app has data for)",
		Headers: []string{"Section", "Taxable", "IGST", "CGST", "SGST", "CESS"}}
	for _, g := range rows {
		t.Rows = append(t.Rows, []string{g.Label, g.TaxableAmount.StringFixed(fixed), g.IGST.StringFixed(fixed), g.CGST.StringFixed(fixed), g.SGST.StringFixed(fixed), g.CESS.StringFixed(fixed)})
	}
	writeTable(w, r, t)
}

func parseAsOf(r *http.Request) time.Time {
	if v := r.URL.Query().Get("as_of"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return t
		}
	}
	return time.Now()
}
