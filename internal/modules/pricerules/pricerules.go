// Package pricerules keeps selling prices in step with purchase costs: when a purchase is finalized, the default price
// list is updated to cost plus a configured markup.
package pricerules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

var ErrInvalid = errors.New("pricerules: invalid rule")

const (
	ModeRaiseOnly = "RAISE_ONLY" // only ever raise a selling price (a cheaper purchase never lowers it)
	ModeAlways    = "ALWAYS"     // follow the latest cost up and down
)

type Rule struct {
	Enabled       bool            `json:"enabled"`
	MarkupPercent decimal.Decimal `json:"markup_percent"`
	Mode          string          `json:"mode"`
}

// Item is one purchased line: its unit cost in the purchase currency.
type Item struct {
	VariantID uuid.UUID
	UnitID    uuid.UUID
	UnitCost  decimal.Decimal
}

type Service struct {
	pool        *database.Pool
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
}

func NewService(pool *database.Pool, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, permissions: p, audit: a, now: time.Now}
}

func (s *Service) Get(ctx context.Context, principal permissions.Principal) (*Rule, error) {
	if err := s.permissions.HasAny(ctx, principal, "pricing.view"); err != nil {
		return nil, err
	}
	r := &Rule{MarkupPercent: decimal.NewFromInt(30), Mode: ModeRaiseOnly}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		err := s.pool.Q(ctx).QueryRow(ctx, `SELECT enabled, markup_percent, mode FROM purchase_price_rules WHERE organisation_id = $1`, principal.OrganisationID).
			Scan(&r.Enabled, &r.MarkupPercent, &r.Mode)
		if err != nil && err.Error() != "no rows in result set" {
			return err
		}
		return nil
	})
	return r, err
}

func (s *Service) Save(ctx context.Context, principal permissions.Principal, r Rule) error {
	if err := s.permissions.HasAny(ctx, principal, "pricing.manage"); err != nil {
		return err
	}
	if r.MarkupPercent.IsNegative() || r.MarkupPercent.GreaterThan(decimal.NewFromInt(1000)) {
		return fmt.Errorf("%w: markup must be between 0 and 1000 percent", ErrInvalid)
	}
	if r.Mode != ModeRaiseOnly && r.Mode != ModeAlways {
		return fmt.Errorf("%w: mode must be RAISE_ONLY or ALWAYS", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO purchase_price_rules (organisation_id, enabled, markup_percent, mode, updated_by) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (organisation_id) DO UPDATE SET enabled = EXCLUDED.enabled, markup_percent = EXCLUDED.markup_percent, mode = EXCLUDED.mode,
				updated_by = EXCLUDED.updated_by, updated_at = now()`, principal.OrganisationID, r.Enabled, r.MarkupPercent, r.Mode, principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "pricerules.changed", EntityType: "organisation", EntityID: &principal.OrganisationID,
			AfterState: map[string]any{"enabled": r.Enabled, "markup_percent": r.MarkupPercent.String(), "mode": r.Mode}, At: s.now()})
	})
}

// ApplyPurchase updates the default price list from purchased costs. It must run inside the finalizing transaction.
// It does nothing when the rule is off, when there is no active default price list, or when the purchase currency is
// not the price list's currency (a foreign cost is never converted silently).
func (s *Service) ApplyPurchase(ctx context.Context, orgID uuid.UUID, currency string, items []Item) error {
	var rule Rule
	err := s.pool.Q(ctx).QueryRow(ctx, `SELECT enabled, markup_percent, mode FROM purchase_price_rules WHERE organisation_id = $1`, orgID).Scan(&rule.Enabled, &rule.MarkupPercent, &rule.Mode)
	if err != nil || !rule.Enabled {
		return nil
	}
	var listID uuid.UUID
	var listCurrency string
	if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT id, currency_code FROM price_lists WHERE organisation_id = $1 AND is_default AND status = 'ACTIVE' LIMIT 1`, orgID).Scan(&listID, &listCurrency); err != nil {
		return nil
	}
	if listCurrency != currency {
		return nil
	}
	factor := decimal.NewFromInt(1).Add(rule.MarkupPercent.Div(decimal.NewFromInt(100)))
	for _, it := range items {
		if !it.UnitCost.IsPositive() {
			continue
		}
		price := it.UnitCost.Mul(factor).Round(2)
		if rule.Mode == ModeRaiseOnly {
			var existing decimal.Decimal
			err := s.pool.Q(ctx).QueryRow(ctx, `SELECT price_amount FROM price_list_items WHERE organisation_id = $1 AND price_list_id = $2 AND product_variant_id = $3 AND unit_id = $4`,
				orgID, listID, it.VariantID, it.UnitID).Scan(&existing)
			if err == nil && !price.GreaterThan(existing) {
				continue
			}
		}
		id, _ := uuid.NewV7()
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO price_list_items (id, organisation_id, price_list_id, product_variant_id, unit_id, price_amount) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (organisation_id, price_list_id, product_variant_id, unit_id) DO UPDATE SET price_amount = EXCLUDED.price_amount, updated_at = now()`,
			id, orgID, listID, it.VariantID, it.UnitID, price); err != nil {
			return fmt.Errorf("pricerules: updating price: %w", err)
		}
	}
	return nil
}

// Handlers exposes the rule.
type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/pricing/purchase-rule", h.get)
	r.Put("/pricing/purchase-rule", h.put)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_RULE", err.Error()))
	default:
		httpx.WriteError(w, r, err)
	}
}

func (h *Handlers) get(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.PrincipalFromContext(r.Context())
	rule, err := h.svc.Get(r.Context(), p)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rule)
}

func (h *Handlers) put(w http.ResponseWriter, r *http.Request) {
	p, _ := httpx.PrincipalFromContext(r.Context())
	var in struct {
		Enabled       bool   `json:"enabled"`
		MarkupPercent string `json:"markup_percent"`
		Mode          string `json:"mode"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	m, err := decimal.NewFromString(in.MarkupPercent)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_MARKUP", "markup_percent must be a number."))
		return
	}
	if err := h.svc.Save(r.Context(), p, Rule{Enabled: in.Enabled, MarkupPercent: m, Mode: in.Mode}); err != nil {
		fail(w, r, err)
		return
	}
	h.get(w, r)
}
