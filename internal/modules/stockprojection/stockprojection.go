// Package stockprojection answers "how many can I promise?" for a product in a warehouse: the physical balance, less
// what is reserved and committed on open sales orders, plus what is expected on open purchase orders.
package stockprojection

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	httpx "rechvix/internal/platform/http"

	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

type Projection struct {
	OnHand            decimal.Decimal `json:"on_hand"`
	Reserved          decimal.Decimal `json:"reserved"`
	PhysicalAvailable decimal.Decimal `json:"physical_available"`
	CommittedOrders   decimal.Decimal `json:"committed_on_sales_orders"`
	IncomingOrders    decimal.Decimal `json:"incoming_on_purchase_orders"`
	Projected         decimal.Decimal `json:"projected_available"`
}

type Service struct {
	pool        *database.Pool
	permissions *permissions.Checker
}

func NewService(pool *database.Pool, p *permissions.Checker) *Service {
	return &Service{pool: pool, permissions: p}
}

// Get computes the projection. A sales order counts as committed until a delivery note or invoice that references it
// is finalized; a purchase order counts as incoming until a goods receipt or purchase invoice that references it is finalized.
func (s *Service) Get(ctx context.Context, principal permissions.Principal, warehouseID, variantID uuid.UUID) (*Projection, error) {
	if err := s.permissions.HasAny(ctx, principal, "inventory.view"); err != nil {
		return nil, err
	}
	var p Projection
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		q := s.pool.Q(ctx)
		if err := q.QueryRow(ctx, `
			SELECT COALESCE(SUM(quantity_on_hand), 0), COALESCE(SUM(quantity_reserved), 0)
			FROM stock_balances WHERE organisation_id = $1 AND warehouse_id = $2 AND product_variant_id = $3`,
			principal.OrganisationID, warehouseID, variantID).Scan(&p.OnHand, &p.Reserved); err != nil {
			return err
		}
		if err := q.QueryRow(ctx, `
			SELECT COALESCE(SUM(l.quantity), 0) FROM sales_documents d JOIN sales_document_lines l ON l.sales_document_id = d.id
			WHERE d.organisation_id = $1 AND d.warehouse_id = $2 AND l.product_variant_id = $3
			  AND d.document_type = 'SALES_ORDER' AND d.status = 'FINALIZED'
			  AND NOT EXISTS (SELECT 1 FROM sales_documents x WHERE x.reference_document_id = d.id AND x.status = 'FINALIZED'
			                  AND x.document_type IN ('DELIVERY_CHALLAN', 'TAX_INVOICE', 'POS_INVOICE'))`,
			principal.OrganisationID, warehouseID, variantID).Scan(&p.CommittedOrders); err != nil {
			return err
		}
		return q.QueryRow(ctx, `
			SELECT COALESCE(SUM(l.quantity), 0) FROM purchase_documents d JOIN purchase_document_lines l ON l.purchase_document_id = d.id
			WHERE d.organisation_id = $1 AND d.warehouse_id = $2 AND l.product_variant_id = $3
			  AND d.document_type = 'PURCHASE_ORDER' AND d.status = 'FINALIZED'
			  AND NOT EXISTS (SELECT 1 FROM purchase_documents x WHERE x.reference_document_id = d.id AND x.status = 'FINALIZED'
			                  AND x.document_type IN ('GOODS_RECEIPT', 'PURCHASE_INVOICE'))`,
			principal.OrganisationID, warehouseID, variantID).Scan(&p.IncomingOrders)
	})
	if err != nil {
		return nil, err
	}
	p.PhysicalAvailable = p.OnHand.Sub(p.Reserved)
	p.Projected = p.PhysicalAvailable.Sub(p.CommittedOrders).Add(p.IncomingOrders)
	return &p, nil
}

// Handlers exposes GET /inventory/projection.
type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) { r.Get("/inventory/projection", h.get) }

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	wh, err1 := uuid.Parse(r.URL.Query().Get("warehouse_id"))
	v, err2 := uuid.Parse(r.URL.Query().Get("product_variant_id"))
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "warehouse_id and product_variant_id must be UUIDs."))
		return
	}
	p, _ := httpx.PrincipalFromContext(r.Context())
	out, err := h.svc.Get(r.Context(), p, wh, v)
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case err != nil:
		httpx.WriteError(w, r, err)
	default:
		httpx.WriteJSON(w, http.StatusOK, out)
	}
}
