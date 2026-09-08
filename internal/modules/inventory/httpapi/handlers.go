// Package httpapi is the inventory module's HTTP transport layer. Mirrors
// internal/modules/catalogue/httpapi's shape.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/inventory/app"
	"rechvix/internal/modules/inventory/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Post("/inventory/opening-stock", h.recordOpeningStock)
	r.Post("/inventory/adjustments", h.recordAdjustment)
	r.Post("/inventory/transfers", h.recordTransfer)
	r.Post("/inventory/reservations", h.reserve)
	r.Post("/inventory/reservations/{id}/release", h.releaseReservation)
	r.Get("/inventory/balances", h.getBalance)
	r.Get("/inventory/cost-lots", h.listCostLots)
	r.Get("/inventory/movements", h.listMovements)
	r.Get("/inventory/low-stock", h.listLowStock)
	r.Put("/inventory/policies", h.setStockPolicy)
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
	case errors.Is(err, domain.ErrInsufficientStock):
		httpx.WriteError(w, r, httpx.NewConflict("INSUFFICIENT_STOCK", "Not enough stock is available for this operation."))
	case errors.Is(err, domain.ErrInvalidMovementType):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_MOVEMENT_TYPE", "That is not a recognized stock movement type."))
	case errors.Is(err, domain.ErrDuplicateBatchCode):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_BATCH_CODE", "That batch code is already in use for this product."))
	case errors.Is(err, domain.ErrDuplicateSerial):
		httpx.WriteError(w, r, httpx.NewConflict("DUPLICATE_SERIAL", "That serial number is already in use for this product."))
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

type recordMovementRequest struct {
	WarehouseID      uuid.UUID `json:"warehouse_id"`
	ProductVariantID uuid.UUID `json:"product_variant_id"`
	UnitID           uuid.UUID `json:"unit_id"`
	Quantity         string    `json:"quantity"`
	UnitCost         *string   `json:"unit_cost"`
	BatchCode        *string   `json:"batch_code"`
	Notes            string    `json:"notes"`
}

func (h *Handlers) recordOpeningStock(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[recordMovementRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	qty, err := decimal.NewFromString(req.Quantity)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_QUANTITY", "quantity must be a decimal string."))
		return
	}
	var unitCost *decimal.Decimal
	if req.UnitCost != nil {
		c, err := decimal.NewFromString(*req.UnitCost)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_UNIT_COST", "unit_cost must be a decimal string."))
			return
		}
		unitCost = &c
	}
	mv, err := h.svc.RecordOpeningStock(r.Context(), principal(r), app.RecordMovementParams{
		WarehouseID: req.WarehouseID, ProductVariantID: req.ProductVariantID, UnitID: req.UnitID,
		Quantity: qty, UnitCost: unitCost, BatchCode: req.BatchCode, Notes: req.Notes,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, mv)
}

type adjustmentLineRequest struct {
	ProductVariantID uuid.UUID `json:"product_variant_id"`
	UnitID           uuid.UUID `json:"unit_id"`
	Quantity         string    `json:"quantity"`
	MovementType     string    `json:"movement_type"`
	BatchCode        *string   `json:"batch_code"`
}

type recordAdjustmentRequest struct {
	WarehouseID uuid.UUID               `json:"warehouse_id"`
	Reason      string                  `json:"reason"`
	Notes       string                  `json:"notes"`
	Lines       []adjustmentLineRequest `json:"lines"`
}

func (h *Handlers) recordAdjustment(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[recordAdjustmentRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	lines := make([]app.AdjustmentLineParams, 0, len(req.Lines))
	for _, l := range req.Lines {
		qty, err := decimal.NewFromString(l.Quantity)
		if err != nil {
			httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_QUANTITY", "each line's quantity must be a decimal string."))
			return
		}
		lines = append(lines, app.AdjustmentLineParams{
			ProductVariantID: l.ProductVariantID, UnitID: l.UnitID, Quantity: qty,
			MovementType: domain.MovementType(l.MovementType), BatchCode: l.BatchCode,
		})
	}
	adj, movements, err := h.svc.RecordAdjustment(r.Context(), principal(r), app.RecordAdjustmentParams{
		WarehouseID: req.WarehouseID, Reason: req.Reason, Notes: req.Notes, Lines: lines,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"adjustment": adj, "movements": movements})
}

type recordTransferRequest struct {
	FromWarehouseID  uuid.UUID `json:"from_warehouse_id"`
	ToWarehouseID    uuid.UUID `json:"to_warehouse_id"`
	ProductVariantID uuid.UUID `json:"product_variant_id"`
	UnitID           uuid.UUID `json:"unit_id"`
	Quantity         string    `json:"quantity"`
	Notes            string    `json:"notes"`
}

func (h *Handlers) recordTransfer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[recordTransferRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	qty, err := decimal.NewFromString(req.Quantity)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_QUANTITY", "quantity must be a decimal string."))
		return
	}
	t, err := h.svc.RecordTransfer(r.Context(), principal(r), app.RecordTransferParams{
		FromWarehouseID: req.FromWarehouseID, ToWarehouseID: req.ToWarehouseID,
		ProductVariantID: req.ProductVariantID, UnitID: req.UnitID, Quantity: qty, Notes: req.Notes,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, t)
}

type reserveRequest struct {
	WarehouseID      uuid.UUID `json:"warehouse_id"`
	ProductVariantID uuid.UUID `json:"product_variant_id"`
	Quantity         string    `json:"quantity"`
	ReferenceType    string    `json:"reference_type"`
	ReferenceID      uuid.UUID `json:"reference_id"`
}

func (h *Handlers) reserve(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[reserveRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	qty, err := decimal.NewFromString(req.Quantity)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_QUANTITY", "quantity must be a decimal string."))
		return
	}
	res, err := h.svc.Reserve(r.Context(), principal(r), app.ReserveParams{
		WarehouseID: req.WarehouseID, ProductVariantID: req.ProductVariantID, Quantity: qty,
		ReferenceType: req.ReferenceType, ReferenceID: req.ReferenceID,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handlers) releaseReservation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	warehouseID, err := uuid.Parse(r.URL.Query().Get("warehouse_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id query parameter must be a UUID."))
		return
	}
	variantID, err := uuid.Parse(r.URL.Query().Get("product_variant_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VARIANT_ID", "product_variant_id query parameter must be a UUID."))
		return
	}
	if err := h.svc.ReleaseReservation(r.Context(), principal(r), id, warehouseID, variantID); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) getBalance(w http.ResponseWriter, r *http.Request) {
	warehouseID, err := uuid.Parse(r.URL.Query().Get("warehouse_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id query parameter must be a UUID."))
		return
	}
	variantID, err := uuid.Parse(r.URL.Query().Get("product_variant_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VARIANT_ID", "product_variant_id query parameter must be a UUID."))
		return
	}
	bal, err := h.svc.GetBalance(r.Context(), principal(r), warehouseID, variantID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bal)
}

func (h *Handlers) listCostLots(w http.ResponseWriter, r *http.Request) {
	warehouseID, err := uuid.Parse(r.URL.Query().Get("warehouse_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id query parameter must be a UUID."))
		return
	}
	variantID, err := uuid.Parse(r.URL.Query().Get("product_variant_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VARIANT_ID", "product_variant_id query parameter must be a UUID."))
		return
	}
	lots, err := h.svc.ListCostLots(r.Context(), principal(r), warehouseID, variantID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cost_lots": lots})
}

func (h *Handlers) listMovements(w http.ResponseWriter, r *http.Request) {
	warehouseID, err := uuid.Parse(r.URL.Query().Get("warehouse_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id query parameter must be a UUID."))
		return
	}
	variantID, err := uuid.Parse(r.URL.Query().Get("product_variant_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VARIANT_ID", "product_variant_id query parameter must be a UUID."))
		return
	}
	list, err := h.svc.ListMovements(r.Context(), principal(r), warehouseID, variantID, 50)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"movements": list})
}

func (h *Handlers) listLowStock(w http.ResponseWriter, r *http.Request) {
	warehouseID, err := uuid.Parse(r.URL.Query().Get("warehouse_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_WAREHOUSE_ID", "warehouse_id query parameter must be a UUID."))
		return
	}
	list, err := h.svc.ListLowStock(r.Context(), principal(r), warehouseID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"low_stock": list})
}

type setStockPolicyRequest struct {
	WarehouseID        uuid.UUID `json:"warehouse_id"`
	ProductVariantID   uuid.UUID `json:"product_variant_id"`
	ReorderLevel       string    `json:"reorder_level"`
	SafetyStock        string    `json:"safety_stock"`
	AllowNegativeStock bool      `json:"allow_negative_stock"`
}

func (h *Handlers) setStockPolicy(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[setStockPolicyRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	reorder, err := decimal.NewFromString(req.ReorderLevel)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_REORDER_LEVEL", "reorder_level must be a decimal string."))
		return
	}
	safety, err := decimal.NewFromString(req.SafetyStock)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_SAFETY_STOCK", "safety_stock must be a decimal string."))
		return
	}
	err = h.svc.SetStockPolicy(r.Context(), principal(r), domain.StockPolicy{
		WarehouseID: req.WarehouseID, ProductVariantID: req.ProductVariantID,
		ReorderLevel: reorder, SafetyStock: safety, AllowNegativeStock: req.AllowNegativeStock,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
