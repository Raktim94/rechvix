// Package httpapi is the catalogue module's HTTP transport layer. Mirrors
// internal/modules/organisation/httpapi's shape.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/catalogue/app"
	"rechvix/internal/modules/catalogue/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/importer"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/catalogue/units", h.listUnits)
	r.Post("/catalogue/units/ensure-default", h.ensureDefaultUnits)
	r.Post("/catalogue/units", h.createUnit)
	r.Post("/catalogue/unit-conversions", h.createUnitConversion)
	r.Get("/catalogue/categories", h.listCategories)
	r.Post("/catalogue/categories", h.createCategory)
	r.Get("/catalogue/brands", h.listBrands)
	r.Post("/catalogue/brands", h.createBrand)
	r.Get("/catalogue/products", h.listOrSearchProducts)
	r.Post("/catalogue/products", h.createProduct)
	r.Get("/catalogue/products/{id}", h.getProduct)
	r.Put("/catalogue/products/{id}", h.updateProduct)
	r.Delete("/catalogue/products/{id}", h.deleteProduct)
	r.Post("/catalogue/products/{id}/restore", h.restoreProduct)
	r.Post("/catalogue/products/bulk-delete", h.bulkDeleteProducts)
	r.Post("/catalogue/products/bulk-restore", h.bulkRestoreProducts)
	r.Get("/catalogue/products/{id}/variants", h.listVariants)
	r.Post("/catalogue/variants", h.createVariant)
	r.Post("/catalogue/barcodes", h.addBarcode)
	r.Get("/catalogue/barcodes/{code}", h.lookupBarcode)
	r.Post("/catalogue/products/import", h.importProducts)
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "The requested resource was not found."))
	case errors.Is(err, domain.ErrDuplicateSKU):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_SKU", "That SKU code is already in use."))
	case errors.Is(err, domain.ErrDuplicateCode):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_CODE", "That code is already in use."))
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	default:
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

// --- Units of measure ---

func (h *Handlers) listUnits(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListUnitsOfMeasure(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"units_of_measure": list})
}

func (h *Handlers) ensureDefaultUnits(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.EnsureDefaultUnits(r.Context(), principal(r)); err != nil {
		writeServiceError(w, r, err)
		return
	}
	list, err := h.svc.ListUnitsOfMeasure(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"units_of_measure": list})
}

type createUnitRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func (h *Handlers) createUnit(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createUnitRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	u, err := h.svc.CreateUnitOfMeasure(r.Context(), principal(r), app.CreateUnitOfMeasureParams{Code: req.Code, Name: req.Name})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, u)
}

type createUnitConversionRequest struct {
	FromUnitID uuid.UUID `json:"from_unit_id"`
	ToUnitID   uuid.UUID `json:"to_unit_id"`
	Factor     string    `json:"factor"`
}

func (h *Handlers) createUnitConversion(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createUnitConversionRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	factor, err := decimal.NewFromString(req.Factor)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FACTOR", "factor must be a decimal string."))
		return
	}
	c, err := h.svc.CreateUnitConversion(r.Context(), principal(r), app.CreateUnitConversionParams{FromUnitID: req.FromUnitID, ToUnitID: req.ToUnitID, Factor: factor})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

// --- Categories ---

func (h *Handlers) listCategories(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListCategories(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"categories": list})
}

type createCategoryRequest struct {
	Name     string     `json:"name"`
	ParentID *uuid.UUID `json:"parent_id"`
}

func (h *Handlers) createCategory(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createCategoryRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), principal(r), req.Name, req.ParentID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

// --- Brands ---

func (h *Handlers) listBrands(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListBrands(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"brands": list})
}

type createBrandRequest struct {
	Name string `json:"name"`
}

func (h *Handlers) createBrand(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createBrandRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	b, err := h.svc.CreateBrand(r.Context(), principal(r), req.Name)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, b)
}

// --- Products ---

// productListDTO embeds domain.Product (its fields flatten into the JSON
// object, same shape callers already expect) plus DefaultVariantID — the
// first/only variant most products have exactly one of (the same
// assumption CataloguePage's manual "add product" flow, barcode field,
// etc. already make). Added so the frontend can join this against a
// price-list-items fetch to show/edit each product's price without an
// N+1 round trip per product from the browser — catalogue deliberately
// doesn't import the pricing module itself (SetPriceHookFunc's own doc
// comment explains why), so the actual price is a frontend-side join,
// not fetched here.
type productListDTO struct {
	*domain.Product
	DefaultVariantID *uuid.UUID `json:"DefaultVariantID,omitempty"`
}

func (h *Handlers) withDefaultVariants(ctx context.Context, p permissions.Principal, products []*domain.Product) []productListDTO {
	out := make([]productListDTO, len(products))
	for i, prod := range products {
		out[i] = productListDTO{Product: prod}
		variants, err := h.svc.ListVariantsByProduct(ctx, p, prod.ID)
		if err == nil && len(variants) > 0 {
			out[i].DefaultVariantID = &variants[0].ID
		}
	}
	return out
}

func (h *Handlers) listOrSearchProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q != "" {
		list, err := h.svc.SearchProducts(r.Context(), principal(r), q, 20)
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"products": h.withDefaultVariants(r.Context(), principal(r), list)})
		return
	}
	list, err := h.svc.ListProducts(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"products": h.withDefaultVariants(r.Context(), principal(r), list)})
}

type createProductRequest struct {
	CategoryID  *uuid.UUID `json:"category_id"`
	BrandID     *uuid.UUID `json:"brand_id"`
	BaseUOMID   uuid.UUID  `json:"base_uom_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	HSNSACCode  string     `json:"hsn_sac_code"`
}

func (h *Handlers) createProduct(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createProductRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	p, err := h.svc.CreateProduct(r.Context(), principal(r), app.CreateProductParams{
		CategoryID: req.CategoryID, BrandID: req.BrandID, BaseUOMID: req.BaseUOMID,
		Name: req.Name, Description: req.Description, HSNSACCode: req.HSNSACCode,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *Handlers) getProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	p, err := h.svc.GetProduct(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handlers) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[createProductRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	p, err := h.svc.UpdateProduct(r.Context(), principal(r), id, app.UpdateProductParams{
		CategoryID: req.CategoryID, BrandID: req.BrandID, BaseUOMID: req.BaseUOMID,
		Name: req.Name, Description: req.Description, HSNSACCode: req.HSNSACCode,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handlers) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	if err := h.svc.SetProductStatus(r.Context(), principal(r), id, domain.StatusInactive); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) restoreProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	if err := h.svc.SetProductStatus(r.Context(), principal(r), id, domain.StatusActive); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bulkDeleteProductsRequest struct {
	IDs []uuid.UUID `json:"ids"`
}

// bulkDeleteProducts reports which ids were actually removed vs. which
// fell back to deactivation (still referenced by real history) — see
// app.Service.DeleteProductsIfUnused's own doc comment.
func (h *Handlers) bulkDeleteProducts(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[bulkDeleteProductsRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteError(w, r, httpx.NewBadRequest("IDS_REQUIRED", "ids must contain at least one product id."))
		return
	}
	result, err := h.svc.DeleteProductsIfUnused(r.Context(), principal(r), req.IDs)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	// DeleteOutcome's slices are nil (not empty) whenever every id landed
	// in the other bucket — a Go nil slice marshals to JSON `null`, which
	// broke CataloguePage.tsx's `.length` reads on whichever field stayed
	// empty (e.g. deleting only ever-unused products left `deactivated`
	// as literal `null`). Normalize both to `[]` so the wire shape is
	// always an array, never null.
	hardDeleted := result.HardDeleted
	if hardDeleted == nil {
		hardDeleted = []uuid.UUID{}
	}
	deactivated := result.Deactivated
	if deactivated == nil {
		deactivated = []uuid.UUID{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"hard_deleted": hardDeleted,
		"deactivated":  deactivated,
	})
}

type bulkRestoreProductsRequest struct {
	IDs []uuid.UUID `json:"ids"`
}

func (h *Handlers) bulkRestoreProducts(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[bulkRestoreProductsRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteError(w, r, httpx.NewBadRequest("IDS_REQUIRED", "ids must contain at least one product id."))
		return
	}
	if err := h.svc.BulkSetProductStatus(r.Context(), principal(r), req.IDs, domain.StatusActive); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) listVariants(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	list, err := h.svc.ListVariantsByProduct(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"variants": list})
}

type createVariantRequest struct {
	ProductID  uuid.UUID      `json:"product_id"`
	SKUCode    string         `json:"sku_code"`
	Attributes map[string]any `json:"attributes"`
}

func (h *Handlers) createVariant(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createVariantRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	v, err := h.svc.CreateVariant(r.Context(), principal(r), app.CreateVariantParams{ProductID: req.ProductID, SKUCode: req.SKUCode, Attributes: req.Attributes})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, v)
}

// --- Barcodes ---

type addBarcodeRequest struct {
	VariantID uuid.UUID `json:"variant_id"`
	UnitID    uuid.UUID `json:"unit_id"`
	Barcode   string    `json:"barcode"`
}

func (h *Handlers) addBarcode(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[addBarcodeRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	b, err := h.svc.AddBarcode(r.Context(), principal(r), app.AddBarcodeParams{VariantID: req.VariantID, UnitID: req.UnitID, Barcode: req.Barcode})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *Handlers) lookupBarcode(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	b, err := h.svc.LookupBarcode(r.Context(), principal(r), code)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

// importProducts bulk-imports products from an uploaded CSV or XLSX file
// (brief §53). Query params: format=csv|xlsx (required), dry_run=true|false
// (default false). The request body is the raw file content.
func (h *Handlers) importProducts(w http.ResponseWriter, r *http.Request) {
	rows, ok := parseImportBody(w, r)
	if !ok {
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	report, err := h.svc.ImportProducts(r.Context(), principal(r), rows, dryRun)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

// parseImportBody reads and parses r.Body per the "format" query
// parameter, writing an error response and returning ok=false on
// failure. Same shape as contacts/httpapi's identical helper — kept
// per-package rather than shared in internal/platform/http because it's
// three lines of routing glue, not enough to justify a new cross-module
// dependency for either module.
func parseImportBody(w http.ResponseWriter, r *http.Request) ([]importer.Row, bool) {
	switch r.URL.Query().Get("format") {
	case "csv":
		rows, err := importer.ParseCSV(r.Body)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_CSV", "Could not parse the uploaded file as CSV: "+err.Error()))
			return nil, false
		}
		return rows, true
	case "xlsx":
		rows, err := importer.ParseXLSX(r.Body)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_XLSX", "Could not parse the uploaded file as XLSX: "+err.Error()))
			return nil, false
		}
		return rows, true
	default:
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_FORMAT", `format query parameter must be "csv" or "xlsx".`))
		return nil, false
	}
}
