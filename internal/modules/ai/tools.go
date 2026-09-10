// Package ai implements the MCP read-only tool surface (brief §39-40,
// docs/architecture.md §11). Every tool handler in this file calls the
// SAME application-layer service method a REST endpoint for the
// equivalent data would call — never a raw SQL query, never a bypass of
// permissions.Checker. That property holds structurally, not by
// discipline: every app.Service method already used here (GetDocument,
// SearchProducts, GetBalance, GetParty, SalesSummary, ...) begins with
// its own internal s.view(ctx, principal) permission check, so an MCP
// tool that calls one gets exactly the same authorization path a REST
// handler for that data would get, automatically.
//
// The organisation a tool call can see is bound ONCE, to the
// apps/mcp-resolved API key's Principal, at Toolset construction — no
// tool's input schema has an organisation_id field at all, so there is no
// argument for a malicious/buggy MCP client to pass that could even be
// looked at, let alone trusted (brief Rule 5's REST-API version of this
// rule applies identically here, and this makes it structurally
// impossible rather than merely checked).
//
// A caller's scopes (from CreateAPIKeyParams — internal/modules/identity)
// additionally restrict which of these tools succeed, via the exact same
// permissions.WithAPIKeyScopeRestriction mechanism the REST API's
// RequireAuthOrAPIKey middleware uses — Scenario M's "inventory:read-only
// key can read stock but not invoices" is enforced by
// permissions.Checker.Require inside GetDocument/ListDocuments itself,
// not by anything in this file.
//
// Known gap, documented rather than silently missing: per-tool-call rate
// limiting (brief §39's "rate limit" requirement) has no implementation
// here — the only existing rate limiter in this codebase is identity's
// login-attempt limiter (internal/modules/identity/app/ratelimit.go),
// which is login-specific and in-process. A general-purpose, reusable
// rate limiter is real remaining work, not attempted under this stage's
// time budget rather than faked with something that wouldn't actually
// hold under concurrent MCP clients.
package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	inventoryapp "rechvix/internal/modules/inventory/app"
	reportingapp "rechvix/internal/modules/reporting/app"
	reportingdomain "rechvix/internal/modules/reporting/domain"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

type Toolset struct {
	principal  permissions.Principal
	scopeCodes map[string]bool
	catalogue  *catalogueapp.Service
	contacts   *contactsapp.Service
	inventory  *inventoryapp.Service
	sales      *salesapp.Service
	reporting  *reportingapp.Service
	audit      audit.Recorder
}

func NewToolset(
	principal permissions.Principal,
	scopeCodes map[string]bool,
	catalogueSvc *catalogueapp.Service,
	contactsSvc *contactsapp.Service,
	inventorySvc *inventoryapp.Service,
	salesSvc *salesapp.Service,
	reportingSvc *reportingapp.Service,
	recorder audit.Recorder,
) *Toolset {
	return &Toolset{
		principal: principal, scopeCodes: scopeCodes,
		catalogue: catalogueSvc, contacts: contactsSvc, inventory: inventorySvc,
		sales: salesSvc, reporting: reportingSvc, audit: recorder,
	}
}

// scopedCtx attaches this toolset's fixed API-key scope restriction to
// ctx — every tool handler calls this first, before any app.Service call.
func (t *Toolset) scopedCtx(ctx context.Context) context.Context {
	return permissions.WithAPIKeyScopeRestriction(ctx, t.scopeCodes)
}

func (t *Toolset) recordAudit(ctx context.Context, tool, entityType string, entityID *uuid.UUID) {
	_ = t.audit.Record(ctx, audit.Entry{
		OrganisationID: t.principal.OrganisationID, ActorUserID: &t.principal.UserID, ActorType: audit.ActorAPIKey,
		Action: "mcp." + tool, EntityType: entityType, EntityID: entityID,
	})
}

// Register binds every read-only tool to server, per brief §39's exact
// list.
func (t *Toolset) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "search_products", Description: "Search products by name, SKU, or barcode text."}, t.searchProducts)
	mcp.AddTool(server, &mcp.Tool{Name: "get_product", Description: "Get a single product by id."}, t.getProduct)
	mcp.AddTool(server, &mcp.Tool{Name: "get_inventory", Description: "Get current stock balance for a product variant at a warehouse."}, t.getInventory)
	mcp.AddTool(server, &mcp.Tool{Name: "get_customer", Description: "Get a single customer/supplier party by id."}, t.getCustomer)
	mcp.AddTool(server, &mcp.Tool{Name: "get_invoice", Description: "Get a single sales document (invoice, quotation, etc.) by id, with its lines."}, t.getInvoice)
	mcp.AddTool(server, &mcp.Tool{Name: "list_invoices", Description: "List sales documents, optionally filtered by document type."}, t.listInvoices)
	mcp.AddTool(server, &mcp.Tool{Name: "get_sales_summary", Description: "Aggregate sales summary, grouped by day/month/customer/product/category/salesperson/branch."}, t.getSalesSummary)
	mcp.AddTool(server, &mcp.Tool{Name: "get_receivables", Description: "Outstanding customer receivables as of now, with ageing."}, t.getReceivables)
	mcp.AddTool(server, &mcp.Tool{Name: "get_stock_summary", Description: "Stock valuation summary across products/warehouses."}, t.getStockSummary)
	mcp.AddTool(server, &mcp.Tool{Name: "get_gst_summary", Description: "HSN-wise GST tax summary (taxable value and tax components)."}, t.getGSTSummary)
}

// --- search_products / get_product ---

type searchProductsIn struct {
	Query string `json:"query" jsonschema:"text to match against product name, SKU, or barcode"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum results to return (default 20)"`
}
type productSummary struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	HSNSACCode string `json:"hsn_sac_code,omitempty"`
	CategoryID string `json:"category_id,omitempty"`
}
type searchProductsOut struct {
	Products []productSummary `json:"products"`
}

func (t *Toolset) searchProducts(ctx context.Context, _ *mcp.CallToolRequest, in searchProductsIn) (*mcp.CallToolResult, searchProductsOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	products, err := t.catalogue.SearchProducts(t.scopedCtx(ctx), t.principal, in.Query, limit)
	if err != nil {
		return nil, searchProductsOut{}, err
	}
	t.recordAudit(ctx, "search_products", "product", nil)
	out := searchProductsOut{Products: make([]productSummary, 0, len(products))}
	for _, p := range products {
		out.Products = append(out.Products, productSummary{ID: p.ID.String(), Name: p.Name, HSNSACCode: p.HSNSACCode, CategoryID: uuidOrEmpty(p.CategoryID)})
	}
	return nil, out, nil
}

type getProductIn struct {
	ProductID string `json:"product_id" jsonschema:"the product's id"`
}
type getProductOut struct {
	Product productSummary `json:"product"`
}

func (t *Toolset) getProduct(ctx context.Context, _ *mcp.CallToolRequest, in getProductIn) (*mcp.CallToolResult, getProductOut, error) {
	id, err := uuid.Parse(in.ProductID)
	if err != nil {
		return nil, getProductOut{}, fmt.Errorf("ai: invalid product_id: %w", err)
	}
	p, err := t.catalogue.GetProduct(t.scopedCtx(ctx), t.principal, id)
	if err != nil {
		return nil, getProductOut{}, err
	}
	t.recordAudit(ctx, "get_product", "product", &id)
	return nil, getProductOut{Product: productSummary{ID: p.ID.String(), Name: p.Name, HSNSACCode: p.HSNSACCode, CategoryID: uuidOrEmpty(p.CategoryID)}}, nil
}

// --- get_inventory ---

type getInventoryIn struct {
	WarehouseID      string `json:"warehouse_id" jsonschema:"the warehouse's id"`
	ProductVariantID string `json:"product_variant_id" jsonschema:"the product variant's id"`
}
type getInventoryOut struct {
	QuantityOnHand   string `json:"quantity_on_hand"`
	QuantityReserved string `json:"quantity_reserved"`
	AverageCost      string `json:"average_cost"`
}

func (t *Toolset) getInventory(ctx context.Context, _ *mcp.CallToolRequest, in getInventoryIn) (*mcp.CallToolResult, getInventoryOut, error) {
	wid, err := uuid.Parse(in.WarehouseID)
	if err != nil {
		return nil, getInventoryOut{}, fmt.Errorf("ai: invalid warehouse_id: %w", err)
	}
	vid, err := uuid.Parse(in.ProductVariantID)
	if err != nil {
		return nil, getInventoryOut{}, fmt.Errorf("ai: invalid product_variant_id: %w", err)
	}
	bal, err := t.inventory.GetBalance(t.scopedCtx(ctx), t.principal, wid, vid)
	if err != nil {
		return nil, getInventoryOut{}, err
	}
	t.recordAudit(ctx, "get_inventory", "stock_balance", &vid)
	return nil, getInventoryOut{
		QuantityOnHand: bal.QuantityOnHand.String(), QuantityReserved: bal.QuantityReserved.String(),
		AverageCost: bal.AverageCost.String(),
	}, nil
}

// --- get_customer ---

type getCustomerIn struct {
	PartyID string `json:"party_id" jsonschema:"the customer/supplier party's id"`
}
type getCustomerOut struct {
	ID        string `json:"id"`
	LegalName string `json:"legal_name"`
	PartyType string `json:"party_type"`
}

func (t *Toolset) getCustomer(ctx context.Context, _ *mcp.CallToolRequest, in getCustomerIn) (*mcp.CallToolResult, getCustomerOut, error) {
	id, err := uuid.Parse(in.PartyID)
	if err != nil {
		return nil, getCustomerOut{}, fmt.Errorf("ai: invalid party_id: %w", err)
	}
	p, err := t.contacts.GetParty(t.scopedCtx(ctx), t.principal, id)
	if err != nil {
		return nil, getCustomerOut{}, err
	}
	t.recordAudit(ctx, "get_customer", "party", &id)
	return nil, getCustomerOut{ID: p.ID.String(), LegalName: p.LegalName, PartyType: string(p.PartyType)}, nil
}

// --- get_invoice / list_invoices ---

type getInvoiceIn struct {
	DocumentID string `json:"document_id" jsonschema:"the sales document's id"`
}
type invoiceLineOut struct {
	LineNumber int    `json:"line_number"`
	Quantity   string `json:"quantity"`
	UnitPrice  string `json:"unit_price"`
}
type getInvoiceOut struct {
	ID             string           `json:"id"`
	DocumentNumber string           `json:"document_number"`
	DocumentType   string           `json:"document_type"`
	Status         string           `json:"status"`
	GrandTotal     string           `json:"grand_total,omitempty"`
	Lines          []invoiceLineOut `json:"lines"`
}

func (t *Toolset) getInvoice(ctx context.Context, _ *mcp.CallToolRequest, in getInvoiceIn) (*mcp.CallToolResult, getInvoiceOut, error) {
	id, err := uuid.Parse(in.DocumentID)
	if err != nil {
		return nil, getInvoiceOut{}, fmt.Errorf("ai: invalid document_id: %w", err)
	}
	doc, lines, err := t.sales.GetDocument(t.scopedCtx(ctx), t.principal, id)
	if err != nil {
		return nil, getInvoiceOut{}, err
	}
	t.recordAudit(ctx, "get_invoice", "sales_document", &id)
	out := getInvoiceOut{ID: doc.ID.String(), DocumentNumber: doc.DocumentNumber, DocumentType: string(doc.DocumentType), Status: string(doc.Status)}
	if doc.GrandTotalAmount != nil {
		out.GrandTotal = doc.GrandTotalAmount.StringFixed(money.RoundHalfUp)
	}
	for _, l := range lines {
		out.Lines = append(out.Lines, invoiceLineOut{LineNumber: l.LineNumber, Quantity: l.Quantity.String(), UnitPrice: l.UnitPrice.Decimal().String()})
	}
	return nil, out, nil
}

type listInvoicesIn struct {
	DocumentType string `json:"document_type,omitempty" jsonschema:"optional document type filter, e.g. TAX_INVOICE"`
}
type listInvoicesOut struct {
	Documents []getInvoiceOut `json:"documents"`
}

func (t *Toolset) listInvoices(ctx context.Context, _ *mcp.CallToolRequest, in listInvoicesIn) (*mcp.CallToolResult, listInvoicesOut, error) {
	var docType *salesdomain.DocumentType
	if in.DocumentType != "" {
		dt := salesdomain.DocumentType(in.DocumentType)
		docType = &dt
	}
	docs, err := t.sales.ListDocuments(t.scopedCtx(ctx), t.principal, docType, nil)
	if err != nil {
		return nil, listInvoicesOut{}, err
	}
	t.recordAudit(ctx, "list_invoices", "sales_document", nil)
	out := listInvoicesOut{Documents: make([]getInvoiceOut, 0, len(docs))}
	for _, doc := range docs {
		row := getInvoiceOut{ID: doc.ID.String(), DocumentNumber: doc.DocumentNumber, DocumentType: string(doc.DocumentType), Status: string(doc.Status)}
		if doc.GrandTotalAmount != nil {
			row.GrandTotal = doc.GrandTotalAmount.StringFixed(money.RoundHalfUp)
		}
		out.Documents = append(out.Documents, row)
	}
	return nil, out, nil
}

// --- get_sales_summary / get_receivables / get_stock_summary / get_gst_summary ---

type getSalesSummaryIn struct {
	GroupBy string `json:"group_by,omitempty" jsonschema:"day, month, customer, product, category, salesperson, or branch (default day)"`
}
type summaryRowOut struct {
	Key           string `json:"key"`
	DocumentCount int    `json:"document_count"`
	GrandTotal    string `json:"grand_total"`
}
type getSalesSummaryOut struct {
	Rows []summaryRowOut `json:"rows"`
}

func (t *Toolset) getSalesSummary(ctx context.Context, _ *mcp.CallToolRequest, in getSalesSummaryIn) (*mcp.CallToolResult, getSalesSummaryOut, error) {
	group := reportingdomain.GroupByDay
	if in.GroupBy != "" {
		group = reportingdomain.GroupDimension(in.GroupBy)
	}
	rows, err := t.reporting.SalesSummary(t.scopedCtx(ctx), t.principal, reportingdomain.Filter{}, group)
	if err != nil {
		return nil, getSalesSummaryOut{}, err
	}
	t.recordAudit(ctx, "get_sales_summary", "sales_summary", nil)
	out := getSalesSummaryOut{Rows: make([]summaryRowOut, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, summaryRowOut{Key: r.Key, DocumentCount: r.DocumentCount, GrandTotal: r.GrandTotal.StringFixed(money.RoundHalfUp)})
	}
	return nil, out, nil
}

type getReceivablesOut struct {
	Rows []receivableRowOut `json:"rows"`
}
type receivableRowOut struct {
	PartyID    string `json:"party_id"`
	PartyName  string `json:"party_name,omitempty"`
	Current    string `json:"current"`
	Days1To30  string `json:"days_1_30"`
	Days31To60 string `json:"days_31_60"`
	Days61To90 string `json:"days_61_90"`
	Days90Plus string `json:"days_90_plus"`
	Total      string `json:"total"`
}

func (t *Toolset) getReceivables(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getReceivablesOut, error) {
	rows, err := t.reporting.ReceivablesSummary(t.scopedCtx(ctx), t.principal, time.Now())
	if err != nil {
		return nil, getReceivablesOut{}, err
	}
	t.recordAudit(ctx, "get_receivables", "receivables_summary", nil)
	out := getReceivablesOut{Rows: make([]receivableRowOut, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, receivableRowOut{
			PartyID: r.PartyID.String(), PartyName: r.PartyName,
			Current: r.Current.StringFixed(money.RoundHalfUp), Days1To30: r.Days1To30.StringFixed(money.RoundHalfUp),
			Days31To60: r.Days31To60.StringFixed(money.RoundHalfUp), Days61To90: r.Days61To90.StringFixed(money.RoundHalfUp),
			Days90Plus: r.Days90Plus.StringFixed(money.RoundHalfUp), Total: r.Total.StringFixed(money.RoundHalfUp),
		})
	}
	return nil, out, nil
}

type getStockSummaryOut struct {
	Rows []stockRowOut `json:"rows"`
}
type stockRowOut struct {
	ProductVariantID string `json:"product_variant_id"`
	WarehouseID      string `json:"warehouse_id"`
	QuantityOnHand   string `json:"quantity_on_hand"`
	TotalValue       string `json:"total_value"`
}

func (t *Toolset) getStockSummary(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getStockSummaryOut, error) {
	rows, err := t.reporting.StockValuation(t.scopedCtx(ctx), t.principal, reportingdomain.Filter{})
	if err != nil {
		return nil, getStockSummaryOut{}, err
	}
	t.recordAudit(ctx, "get_stock_summary", "stock_valuation", nil)
	out := getStockSummaryOut{Rows: make([]stockRowOut, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, stockRowOut{
			ProductVariantID: r.ProductVariantID.String(), WarehouseID: r.WarehouseID.String(),
			QuantityOnHand: r.QuantityOnHand, TotalValue: r.TotalValue.StringFixed(money.RoundHalfUp),
		})
	}
	return nil, out, nil
}

type getGSTSummaryOut struct {
	Rows []gstRowOut `json:"rows"`
}
type gstRowOut struct {
	HSNSACCode    string `json:"hsn_sac_code"`
	TaxableAmount string `json:"taxable_amount"`
	TotalTax      string `json:"total_tax"`
}

func (t *Toolset) getGSTSummary(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getGSTSummaryOut, error) {
	rows, err := t.reporting.HSNSummary(t.scopedCtx(ctx), t.principal, reportingdomain.Filter{})
	if err != nil {
		return nil, getGSTSummaryOut{}, err
	}
	t.recordAudit(ctx, "get_gst_summary", "hsn_summary", nil)
	out := getGSTSummaryOut{Rows: make([]gstRowOut, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, gstRowOut{HSNSACCode: r.HSNSACCode, TaxableAmount: r.TaxableAmount.StringFixed(money.RoundHalfUp), TotalTax: r.TotalTax.StringFixed(money.RoundHalfUp)})
	}
	return nil, out, nil
}

func uuidOrEmpty(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
