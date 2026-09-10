// Package httpapi is the pricing module's HTTP transport layer.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/pricing/app"
	"rechvix/internal/modules/pricing/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *app.Service }

func NewHandlers(svc *app.Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/pricing/price-lists", h.listPriceLists)
	r.Post("/pricing/price-lists", h.createPriceList)
	r.Post("/pricing/price-lists/ensure-default", h.ensureDefaultPriceList)
	r.Get("/pricing/price-lists/{id}", h.getPriceList)
	r.Get("/pricing/price-lists/{id}/items", h.listPrices)
	r.Post("/pricing/price-lists/{id}/items", h.setPrice)
	r.Get("/pricing/price-lists/{id}/resolve", h.resolvePrice)
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
	case errors.Is(err, domain.ErrNegativePrice):
		httpx.WriteError(w, r, httpx.NewBadRequest("NEGATIVE_PRICE", "Price must not be negative."))
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

func (h *Handlers) listPriceLists(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListPriceLists(r.Context(), principal(r))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"price_lists": list})
}

type createPriceListRequest struct {
	Name         string `json:"name"`
	CurrencyCode string `json:"currency_code"`
	IsDefault    bool   `json:"is_default"`
}

func (h *Handlers) createPriceList(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createPriceListRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	pl, err := h.svc.CreatePriceList(r.Context(), principal(r), app.CreatePriceListParams{Name: req.Name, CurrencyCode: req.CurrencyCode, IsDefault: req.IsDefault})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, pl)
}

type ensureDefaultPriceListRequest struct {
	CurrencyCode string `json:"currency_code"`
}

// ensureDefaultPriceList is the frontend-facing counterpart to
// catalogue's price-hook (apps/server/main.go) — same
// EnsureDefaultPriceList call, just reachable from the product form
// directly instead of only via CSV import, so "type a price when
// creating/editing a product" works with zero prior Pricing-page setup
// on a fresh install.
func (h *Handlers) ensureDefaultPriceList(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[ensureDefaultPriceListRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if req.CurrencyCode == "" {
		httpx.WriteError(w, r, httpx.NewBadRequest("CURRENCY_CODE_REQUIRED", "currency_code is required."))
		return
	}
	pl, err := h.svc.EnsureDefaultPriceList(r.Context(), principal(r), req.CurrencyCode)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pl)
}

func (h *Handlers) getPriceList(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	pl, err := h.svc.GetPriceList(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pl)
}

func (h *Handlers) listPrices(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	list, err := h.svc.ListPrices(r.Context(), principal(r), id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

type setPriceRequest struct {
	ProductVariantID uuid.UUID `json:"product_variant_id"`
	UnitID           uuid.UUID `json:"unit_id"`
	Amount           string    `json:"amount"`
	CurrencyCode     string    `json:"currency_code"`
}

func (h *Handlers) setPrice(w http.ResponseWriter, r *http.Request) {
	priceListID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	req, err := decodeJSON[setPriceRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	price, err := money.Parse(req.Amount, req.CurrencyCode)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_AMOUNT", "amount/currency_code could not be parsed as money."))
		return
	}
	item, err := h.svc.SetPrice(r.Context(), principal(r), app.SetPriceParams{PriceListID: priceListID, ProductVariantID: req.ProductVariantID, UnitID: req.UnitID, Price: price})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (h *Handlers) resolvePrice(w http.ResponseWriter, r *http.Request) {
	priceListID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	variantID, err := uuid.Parse(r.URL.Query().Get("variant_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_VARIANT_ID", "variant_id query param must be a UUID."))
		return
	}
	unitID, err := uuid.Parse(r.URL.Query().Get("unit_id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_UNIT_ID", "unit_id query param must be a UUID."))
		return
	}
	item, err := h.svc.ResolvePrice(r.Context(), principal(r), priceListID, variantID, unitID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}
