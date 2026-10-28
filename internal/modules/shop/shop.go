// Package shop is the online catalogue, shop and client area. Visitors browse published products and place orders, which
// become draft sales orders for the team to confirm. Customers open a client area from a one-time emailed link to see their
// documents, pay invoices and sign quotations.
package shop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	esignapp "rechvix/internal/modules/esign"
	notificationsapp "rechvix/internal/modules/notifications/app"
	notificationsdomain "rechvix/internal/modules/notifications/domain"
	onlinepayapp "rechvix/internal/modules/onlinepay"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	taxdomain "rechvix/internal/modules/taxation/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid  = errors.New("shop: invalid request")
	ErrNotFound = errors.New("shop: not found")
	ErrLimit    = errors.New("shop: too many requests, try again later")
	ErrNoMail   = errors.New("shop: email is not set up on this server")
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

type Settings struct {
	Enabled       bool       `json:"enabled"`
	Slug          string     `json:"slug"`
	Title         string     `json:"title"`
	Intro         string     `json:"intro"`
	LegalEntityID uuid.UUID  `json:"legal_entity_id"`
	BranchID      uuid.UUID  `json:"branch_id"`
	WarehouseID   uuid.UUID  `json:"warehouse_id"`
	PriceListID   *uuid.UUID `json:"price_list_id,omitempty"`
	Configured    bool       `json:"configured"`
}

type AdminProduct struct {
	VariantID   uuid.UUID `json:"variant_id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	Published   bool      `json:"published"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	Price       string    `json:"price,omitempty"`
}

type PublicProduct struct {
	VariantID   uuid.UUID `json:"variant_id"`
	SKU         string    `json:"sku"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Price       string    `json:"price"`
	Currency    string    `json:"currency"`
	InStock     bool      `json:"in_stock"`
}

type PublicShop struct {
	Title    string          `json:"title"`
	Intro    string          `json:"intro,omitempty"`
	Currency string          `json:"currency"`
	Products []PublicProduct `json:"products"`
}

type OrderLine struct {
	VariantID uuid.UUID `json:"variant_id"`
	Quantity  int       `json:"quantity"`
}

type OrderInput struct {
	Name, Email, Phone, Address, Notes string
	Lines                              []OrderLine
}

type OrderView struct {
	ID         uuid.UUID  `json:"id"`
	Reference  string     `json:"reference"`
	Customer   string     `json:"customer_name"`
	Email      string     `json:"customer_email"`
	Phone      string     `json:"customer_phone,omitempty"`
	Address    string     `json:"delivery_address,omitempty"`
	Notes      string     `json:"notes,omitempty"`
	Total      string     `json:"total_estimate"`
	Currency   string     `json:"currency"`
	Status     string     `json:"status"`
	DocumentID *uuid.UUID `json:"sales_document_id,omitempty"`
	// InvoiceID is the draft (or finalized) invoice made from this order; stock leaves the warehouse when that invoice is finalized.
	InvoiceID *uuid.UUID `json:"invoice_document_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type PortalDoc struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"number"`
	Type     string    `json:"type"`
	Date     string    `json:"date"`
	Total    string    `json:"total"`
	Currency string    `json:"currency"`
	Open     string    `json:"outstanding,omitempty"`
}

type Service struct {
	pool          *database.Pool
	sales         *salesapp.Service
	catalogue     *catalogueapp.Service
	contacts      *contactsapp.Service
	notifications *notificationsapp.Service
	pay           *onlinepayapp.Service
	esign         *esignapp.Service
	renderer      esignapp.Renderer
	permissions   *permissions.Checker
	audit         audit.Recorder
	now           func() time.Time
	limiter       *limiter
}

func NewService(pool *database.Pool, sales *salesapp.Service, cat *catalogueapp.Service, contacts *contactsapp.Service, n *notificationsapp.Service,
	pay *onlinepayapp.Service, es *esignapp.Service, r esignapp.Renderer, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, sales: sales, catalogue: cat, contacts: contacts, notifications: n, pay: pay, esign: es, renderer: r, permissions: p, audit: a, now: time.Now, limiter: newLimiter()}
}

// limiter is a small in-memory fixed-window limiter for the public endpoints (per client address and action).
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

func (l *limiter) allow(key string, max int, window time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-window)
	var keep []time.Time
	for _, t := range l.hits[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= max {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	if len(l.hits) > 20000 { // bound memory
		l.hits = map[string][]time.Time{key: l.hits[key]}
	}
	return true
}

func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "settings.manage", permissions.Scope{})
}

// ---- admin

func (s *Service) GetSettings(ctx context.Context, principal permissions.Principal) (*Settings, error) {
	if err := s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{}); err != nil {
		return nil, err
	}
	out := &Settings{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var intro *string
		err := s.pool.Q(ctx).QueryRow(ctx, `SELECT enabled, slug, title, intro, legal_entity_id, branch_id, warehouse_id, price_list_id FROM shop_settings WHERE organisation_id = $1`, principal.OrganisationID).
			Scan(&out.Enabled, &out.Slug, &out.Title, &intro, &out.LegalEntityID, &out.BranchID, &out.WarehouseID, &out.PriceListID)
		if err != nil {
			return nil
		}
		out.Configured = true
		if intro != nil {
			out.Intro = *intro
		}
		return nil
	})
	return out, err
}

func (s *Service) SaveSettings(ctx context.Context, principal permissions.Principal, in Settings) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	switch in.Slug {
	case "settings", "products", "orders":
		return fmt.Errorf("%w: that address is reserved, choose another", ErrInvalid)
	}
	if !slugRe.MatchString(in.Slug) {
		return fmt.Errorf("%w: the address must be 3 to 40 letters, digits or hyphens", ErrInvalid)
	}
	if t := strings.TrimSpace(in.Title); t == "" || len(t) > 80 {
		return fmt.Errorf("%w: a shop name of up to 80 characters is required", ErrInvalid)
	}
	if len(in.Intro) > 1000 {
		return fmt.Errorf("%w: the introduction is too long", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if in.LegalEntityID == uuid.Nil || in.BranchID == uuid.Nil || in.WarehouseID == uuid.Nil {
			return fmt.Errorf("%w: choose the company, branch and warehouse orders belong to", ErrInvalid)
		}
		if in.PriceListID == nil {
			var id uuid.UUID
			if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT id FROM price_lists WHERE organisation_id = $1 AND is_default AND status = 'ACTIVE' LIMIT 1`, principal.OrganisationID).Scan(&id); err == nil {
				in.PriceListID = &id
			}
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO shop_settings (organisation_id, enabled, slug, title, intro, legal_entity_id, branch_id, warehouse_id, price_list_id, acting_user_id)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10)
			ON CONFLICT (organisation_id) DO UPDATE SET enabled = EXCLUDED.enabled, slug = EXCLUDED.slug, title = EXCLUDED.title, intro = EXCLUDED.intro,
				legal_entity_id = EXCLUDED.legal_entity_id, branch_id = EXCLUDED.branch_id, warehouse_id = EXCLUDED.warehouse_id, price_list_id = EXCLUDED.price_list_id,
				acting_user_id = EXCLUDED.acting_user_id, updated_at = now()`,
			principal.OrganisationID, in.Enabled, in.Slug, strings.TrimSpace(in.Title), in.Intro, in.LegalEntityID, in.BranchID, in.WarehouseID, in.PriceListID, principal.UserID); err != nil {
			if strings.Contains(err.Error(), "23505") {
				return fmt.Errorf("%w: that address is taken, choose another", ErrInvalid)
			}
			return fmt.Errorf("%w: unknown company, branch or warehouse", ErrInvalid)
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "shop.settings_changed", EntityType: "organisation", EntityID: &principal.OrganisationID, AfterState: map[string]any{"enabled": in.Enabled, "slug": in.Slug}, At: s.now()})
	})
}

func (s *Service) Products(ctx context.Context, principal permissions.Principal) ([]AdminProduct, error) {
	if err := s.permissions.HasAny(ctx, principal, "catalogue.view"); err != nil {
		return nil, err
	}
	out := []AdminProduct{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT v.id, v.sku_code, p.name, COALESCE(sp.published, false), COALESCE(sp.title, ''), COALESCE(sp.description, ''),
			       COALESCE((SELECT i.price_amount::text FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
			                 WHERE i.product_variant_id = v.id AND i.unit_id = p.base_uom_id AND l.is_default AND l.status = 'ACTIVE' LIMIT 1), '')
			FROM product_variants v JOIN products p ON p.id = v.product_id LEFT JOIN shop_products sp ON sp.product_variant_id = v.id AND sp.organisation_id = v.organisation_id
			WHERE v.organisation_id = $1 AND v.status = 'ACTIVE' AND p.status = 'ACTIVE' ORDER BY p.name, v.sku_code LIMIT 2000`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x AdminProduct
			if err := rows.Scan(&x.VariantID, &x.SKU, &x.Name, &x.Published, &x.Title, &x.Description, &x.Price); err != nil {
				return err
			}
			if x.Price != "" {
				if d, err := decimal.NewFromString(x.Price); err == nil {
					x.Price = d.StringFixed(2)
				}
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) SetPublished(ctx context.Context, principal permissions.Principal, variantID uuid.UUID, published bool, title, description string) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	if len(title) > 120 || len(description) > 2000 {
		return fmt.Errorf("%w: title up to 120 and description up to 2000 characters", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var one int
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT 1 FROM product_variants WHERE id = $1`, variantID).Scan(&one); err != nil {
			return ErrNotFound
		}
		_, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO shop_products (organisation_id, product_variant_id, published, title, description) VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''))
			ON CONFLICT (organisation_id, product_variant_id) DO UPDATE SET published = EXCLUDED.published, title = EXCLUDED.title, description = EXCLUDED.description`,
			principal.OrganisationID, variantID, published, strings.TrimSpace(title), strings.TrimSpace(description))
		return err
	})
}

func (s *Service) Orders(ctx context.Context, principal permissions.Principal) ([]OrderView, error) {
	if err := s.permissions.HasAny(ctx, principal, "sales.view"); err != nil {
		return nil, err
	}
	out := []OrderView{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, reference, customer_name, customer_email, COALESCE(customer_phone, ''), COALESCE(delivery_address, ''), COALESCE(notes, ''), total_estimate::text, currency_code, status, sales_document_id, invoice_document_id, created_at
			FROM shop_orders WHERE organisation_id = $1 ORDER BY created_at DESC LIMIT 200`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o OrderView
			if err := rows.Scan(&o.ID, &o.Reference, &o.Customer, &o.Email, &o.Phone, &o.Address, &o.Notes, &o.Total, &o.Currency, &o.Status, &o.DocumentID, &o.InvoiceID, &o.CreatedAt); err != nil {
				return err
			}
			if d, err := decimal.NewFromString(o.Total); err == nil {
				o.Total = d.StringFixed(2)
			}
			out = append(out, o)
		}
		return rows.Err()
	})
	return out, err
}

// orderNext is the fulfilment flow: an order is confirmed, prepared, sent and delivered, and can be cancelled until it has been dispatched.
var orderNext = map[string][]string{
	"NEW":        {"CONFIRMED", "CANCELLED"},
	"CONFIRMED":  {"PROCESSING", "CANCELLED"},
	"PROCESSING": {"DISPATCHED", "CANCELLED"},
	"DISPATCHED": {"DELIVERED"},
}

// ordersMovingTo lists the statuses an order may be in for it to move to status.
func ordersMovingTo(status string) []string {
	var from []string
	for f, tos := range orderNext {
		for _, t := range tos {
			if t == status {
				from = append(from, f)
			}
		}
	}
	return from
}

func (s *Service) SetOrderStatus(ctx context.Context, principal permissions.Principal, id uuid.UUID, status string) error {
	if err := s.permissions.HasAny(ctx, principal, "sales.view"); err != nil {
		return err
	}
	from := ordersMovingTo(status)
	if len(from) == 0 {
		return fmt.Errorf("%w: status must be confirmed, processing, dispatched, delivered or cancelled", ErrInvalid)
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		// An order that already has an invoice is cancelled by cancelling the invoice, not from here.
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE shop_orders SET status = $2, status_changed_at = now()
			WHERE id = $1 AND status = ANY($3) AND ($2 <> 'CANCELLED' OR invoice_document_id IS NULL)`, id, status, from)
		if err != nil {
			return err
		}
		if tag == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// CreateInvoice turns a confirmed order into a draft tax invoice (finalizing the underlying sales order first when needed) and
// remembers it on the order. It is safe to call twice: the existing invoice is returned. Finalizing the invoice, in the Sales
// screen, is what takes the goods out of stock and posts the accounts, so a person still reviews it once.
func (s *Service) CreateInvoice(ctx context.Context, principal permissions.Principal, id uuid.UUID) (uuid.UUID, error) {
	if err := s.permissions.HasAny(ctx, principal, "sales.view"); err != nil {
		return uuid.Nil, err
	}
	var docID uuid.UUID
	var invoiceID *uuid.UUID
	var status string
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT sales_document_id, invoice_document_id, status FROM shop_orders WHERE id = $1 AND organisation_id = $2 AND sales_document_id IS NOT NULL`,
			id, principal.OrganisationID).Scan(&docID, &invoiceID, &status)
	})
	if err != nil {
		return uuid.Nil, ErrNotFound
	}
	if invoiceID != nil {
		return *invoiceID, nil
	}
	if status == "NEW" || status == "CANCELLED" {
		return uuid.Nil, fmt.Errorf("%w: confirm the order before invoicing it", ErrInvalid)
	}
	order, _, err := s.sales.GetDocument(ctx, principal, docID)
	if err != nil {
		return uuid.Nil, err
	}
	if order.Status == salesdomain.StatusDraft {
		if _, err := s.sales.FinalizeDocument(ctx, principal, docID); err != nil {
			return uuid.Nil, fmt.Errorf("shop: finalizing the sales order: %w", err)
		}
	}
	inv, err := s.sales.ConvertDocument(ctx, principal, docID, salesdomain.DocTaxInvoice, nil)
	if err != nil {
		return uuid.Nil, fmt.Errorf("shop: creating the invoice: %w", err)
	}
	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `UPDATE shop_orders SET invoice_document_id = $2 WHERE id = $1 AND invoice_document_id IS NULL`, id, inv.ID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorType: audit.ActorUser, ActorUserID: &principal.UserID, Action: "shop.order_invoiced",
			EntityType: "sales_document", EntityID: &inv.ID, AfterState: map[string]any{"order_id": id}, At: s.now()})
	})
	if err != nil {
		return uuid.Nil, err
	}
	return inv.ID, nil
}

// ---- public

type resolved struct {
	org, legalEntity, branch, warehouse, acting uuid.UUID
	priceList                                   *uuid.UUID
	title, intro, currency                      string
}

func (s *Service) resolve(ctx context.Context, slug string) (*resolved, error) {
	var org uuid.UUID
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT shop_organisation_by_slug($1)`, strings.ToLower(slug)).Scan(&org)
	}); err != nil || org == uuid.Nil {
		return nil, ErrNotFound
	}
	r := &resolved{org: org}
	err := s.pool.RunScoped(ctx, org, func(ctx context.Context) error {
		var intro *string
		if err := s.pool.Q(ctx).QueryRow(ctx, `
			SELECT st.title, st.intro, st.legal_entity_id, st.branch_id, st.warehouse_id, st.price_list_id, st.acting_user_id, le.base_currency_code
			FROM shop_settings st JOIN legal_entities le ON le.id = st.legal_entity_id WHERE st.organisation_id = $1 AND st.enabled`, org).
			Scan(&r.title, &intro, &r.legalEntity, &r.branch, &r.warehouse, &r.priceList, &r.acting, &r.currency); err != nil {
			return ErrNotFound
		}
		if intro != nil {
			r.intro = *intro
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Catalog returns the published products with their price and whether any is in stock.
func (s *Service) Catalog(ctx context.Context, slug, query string) (*PublicShop, error) {
	r, err := s.resolve(ctx, slug)
	if err != nil {
		return nil, err
	}
	out := &PublicShop{Title: r.title, Intro: r.intro, Currency: r.currency, Products: []PublicProduct{}}
	q := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	err = s.pool.RunScoped(ctx, r.org, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT v.id, v.sku_code, COALESCE(sp.title, p.name), COALESCE(sp.description, p.description, ''), i.price_amount::text,
			       COALESCE((SELECT b.quantity_on_hand - b.quantity_reserved > 0 FROM stock_balances b WHERE b.warehouse_id = $2 AND b.product_variant_id = v.id), false)
			FROM shop_products sp JOIN product_variants v ON v.id = sp.product_variant_id JOIN products p ON p.id = v.product_id
			JOIN price_list_items i ON i.product_variant_id = v.id AND i.unit_id = p.base_uom_id AND i.price_list_id = $3
			WHERE sp.organisation_id = $1 AND sp.published AND v.status = 'ACTIVE' AND p.status = 'ACTIVE'
			  AND ($4 = '%%' OR lower(COALESCE(sp.title, p.name)) LIKE $4 OR lower(v.sku_code) LIKE $4)
			ORDER BY sp.sort_order, COALESCE(sp.title, p.name) LIMIT 500`, r.org, r.warehouse, r.priceList, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x PublicProduct
			var price string
			if err := rows.Scan(&x.VariantID, &x.SKU, &x.Title, &x.Description, &price, &x.InStock); err != nil {
				return err
			}
			if d, err := decimal.NewFromString(price); err == nil {
				x.Price = d.StringFixed(2)
			}
			x.Currency = r.currency
			out.Products = append(out.Products, x)
		}
		return rows.Err()
	})
	return out, err
}

func randomRef() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "WEB-" + strings.ToUpper(hex.EncodeToString(b))
}

// PlaceOrder turns a visitor's cart into a draft sales order for the team. It never trusts prices from the client: they
// are read from the price list, and only published products can be ordered.
func (s *Service) PlaceOrder(ctx context.Context, slug, ip string, in OrderInput) (*OrderView, error) {
	if !s.limiter.allow("order:"+ip, 10, time.Hour, s.now()) {
		return nil, ErrLimit
	}
	r, err := s.resolve(ctx, slug)
	if err != nil {
		return nil, err
	}
	name, email := strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Email))
	if len([]rune(name)) < 2 || len(name) > 120 {
		return nil, fmt.Errorf("%w: enter your name", ErrInvalid)
	}
	if !strings.Contains(email, "@") || len(email) > 200 {
		return nil, fmt.Errorf("%w: enter a valid email address", ErrInvalid)
	}
	if len(in.Lines) == 0 || len(in.Lines) > 100 {
		return nil, fmt.Errorf("%w: add between 1 and 100 products", ErrInvalid)
	}
	if len(in.Notes) > 1000 || len(in.Address) > 500 || len(in.Phone) > 40 {
		return nil, fmt.Errorf("%w: a field is too long", ErrInvalid)
	}
	principal := permissions.Principal{UserID: r.acting, OrganisationID: r.org}
	type line struct {
		variant, unit uuid.UUID
		qty           decimal.Decimal
		price         decimal.Decimal
		name          string
	}
	var lines []line
	total := decimal.Zero
	merged := map[uuid.UUID]int{}
	for _, l := range in.Lines {
		if l.Quantity < 1 || l.Quantity > 1000 {
			return nil, fmt.Errorf("%w: quantities must be between 1 and 1000", ErrInvalid)
		}
		merged[l.VariantID] += l.Quantity
	}
	err = s.pool.RunScoped(ctx, r.org, func(ctx context.Context) error {
		for vid, qty := range merged {
			var unit uuid.UUID
			var price, title string
			if err := s.pool.Q(ctx).QueryRow(ctx, `
				SELECT p.base_uom_id, i.price_amount::text, COALESCE(sp.title, p.name)
				FROM shop_products sp JOIN product_variants v ON v.id = sp.product_variant_id JOIN products p ON p.id = v.product_id
				JOIN price_list_items i ON i.product_variant_id = v.id AND i.unit_id = p.base_uom_id AND i.price_list_id = $3
				WHERE sp.organisation_id = $1 AND sp.product_variant_id = $2 AND sp.published AND v.status = 'ACTIVE' AND p.status = 'ACTIVE'`, r.org, vid, r.priceList).Scan(&unit, &price, &title); err != nil {
				return fmt.Errorf("%w: a product in your cart is no longer available", ErrInvalid)
			}
			d, _ := decimal.NewFromString(price)
			q := decimal.NewFromInt(int64(qty))
			lines = append(lines, line{variant: vid, unit: unit, qty: q, price: d, name: title})
			total = total.Add(d.Mul(q))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// find or create the customer
	var partyID uuid.UUID
	err = s.pool.RunScoped(ctx, r.org, func(ctx context.Context) error {
		e := s.pool.Q(ctx).QueryRow(ctx, `SELECT id FROM parties WHERE organisation_id = $1 AND lower(email) = $2 AND party_type IN ('CUSTOMER', 'BOTH') AND status = 'ACTIVE' LIMIT 1`, r.org, email).Scan(&partyID)
		if e != nil {
			partyID = uuid.Nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if partyID == uuid.Nil {
		p, err := s.contacts.CreateParty(ctx, principal, contactsapp.CreatePartyParams{PartyType: contactsdomain.PartyCustomer, LegalName: name, Email: email, CurrencyCode: r.currency, Notes: "Created from an online shop order. Phone given: " + strings.TrimSpace(in.Phone)})
		if err != nil {
			return nil, err
		}
		partyID = p.ID
	}

	ref := randomRef()
	notes := "Online order " + ref
	if in.Address != "" {
		notes += "\nDeliver to: " + strings.TrimSpace(in.Address)
	}
	if in.Notes != "" {
		notes += "\nCustomer notes: " + strings.TrimSpace(in.Notes)
	}
	doc, err := s.sales.CreateDocument(ctx, principal, salesapp.CreateDocumentParams{
		LegalEntityID: r.legalEntity, BranchID: r.branch, WarehouseID: r.warehouse, CustomerPartyID: partyID, DocumentType: salesdomain.DocSalesOrder,
		IssueDate: s.now(), CurrencyCode: r.currency, BaseCurrencyCode: r.currency, ExchangeRate: decimal.NewFromInt(1), PricingMode: taxdomain.PricingExclusive,
		CustomerReference: ref, Notes: notes, PriceListID: r.priceList,
	})
	if err != nil {
		return nil, fmt.Errorf("shop: creating the order: %w", err)
	}
	for _, l := range lines {
		if _, err := s.sales.AddLine(ctx, principal, salesapp.AddLineParams{DocumentID: doc.ID, ProductVariantID: l.variant, UnitID: l.unit, Quantity: l.qty, UnitPrice: l.price}); err != nil {
			return nil, fmt.Errorf("shop: adding %s to the order: %w", l.name, err)
		}
	}
	snapshot := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		snapshot = append(snapshot, map[string]any{"variant_id": l.variant, "name": l.name, "quantity": l.qty.IntPart(), "price": l.price.StringFixed(2)})
	}
	id, _ := uuid.NewV7()
	ov := &OrderView{ID: id, Reference: ref, Customer: name, Email: email, Phone: in.Phone, Address: in.Address, Notes: in.Notes, Total: total.StringFixed(2), Currency: r.currency, Status: "NEW", DocumentID: &doc.ID, CreatedAt: s.now()}
	err = s.pool.RunScoped(ctx, r.org, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO shop_orders (id, organisation_id, reference, party_id, sales_document_id, customer_name, customer_email, customer_phone, delivery_address, notes, total_estimate, currency_code, lines)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),$11,$12,$13)`,
			id, r.org, ref, partyID, doc.ID, name, email, strings.TrimSpace(in.Phone), strings.TrimSpace(in.Address), strings.TrimSpace(in.Notes), total, r.currency, snapshot); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: r.org, ActorType: audit.ActorSystem, Action: "shop.order_placed", EntityType: "sales_document", EntityID: &doc.ID,
			AfterState: map[string]any{"reference": ref, "total": total.StringFixed(2)}, At: s.now()})
	})
	if err != nil {
		return nil, err
	}
	return ov, nil
}

// ---- client area

const (
	loginTTL   = 20 * time.Minute
	sessionTTL = 8 * time.Hour
)

// RequestLogin emails a one-time sign-in link to a customer with that email address. It answers the same way whether or not
// the address is known, so it cannot be used to find out who is a customer.
func (s *Service) RequestLogin(ctx context.Context, slug, ip, email, baseURL string) error {
	if !s.limiter.allow("login:"+ip, 8, time.Hour, s.now()) || !s.limiter.allow("loginmail:"+strings.ToLower(email), 4, time.Hour, s.now()) {
		return ErrLimit
	}
	if !s.notifications.EmailConfigured() {
		return ErrNoMail
	}
	r, err := s.resolve(ctx, slug)
	if err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return nil
	}
	var partyID uuid.UUID
	if err := s.pool.RunScoped(ctx, r.org, func(ctx context.Context) error {
		if e := s.pool.Q(ctx).QueryRow(ctx, `SELECT id FROM parties WHERE organisation_id = $1 AND lower(email) = $2 AND party_type IN ('CUSTOMER', 'BOTH') AND status = 'ACTIVE' LIMIT 1`, r.org, email).Scan(&partyID); e != nil {
			partyID = uuid.Nil
		}
		return nil
	}); err != nil || partyID == uuid.Nil {
		return nil
	}
	_, raw, err := crypto.RandomToken(32)
	if err != nil {
		return err
	}
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO portal_login_tokens (token_hash, organisation_id, party_id, expires_at) VALUES ($1,$2,$3,$4)`, crypto.HashToken(raw), r.org, partyID, s.now().Add(loginTTL))
		return err
	}); err != nil {
		return err
	}
	link := strings.TrimRight(baseURL, "/") + "/api/v1/shop/" + strings.ToLower(slug) + "/portal/enter?token=" + raw
	body := "<p>Open your client area:</p><p><a href=\"" + link + "\">Sign in to " + htmlEscape(r.title) + "</a></p><p>The link works once and expires in 20 minutes. If you did not ask for it, ignore this email.</p>"
	principal := permissions.Principal{UserID: r.acting, OrganisationID: r.org}
	return s.notifications.QueueSend(ctx, principal, notificationsapp.SendPayload{Channel: notificationsdomain.ChannelEmail, Recipient: email, DocumentType: "portal", DocumentID: partyID, Subject: "Your sign-in link for " + r.title, BodyHTML: body})
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// Enter exchanges a one-time link token for a session token (to be set as a cookie).
func (s *Service) Enter(ctx context.Context, slug, token string) (string, time.Time, error) {
	r, err := s.resolve(ctx, slug)
	if err != nil {
		return "", time.Time{}, err
	}
	var party uuid.UUID
	err = s.pool.Run(ctx, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `
			UPDATE portal_login_tokens SET used_at = now() WHERE token_hash = $1 AND organisation_id = $2 AND used_at IS NULL AND expires_at > now() RETURNING party_id`,
			crypto.HashToken(token), r.org).Scan(&party)
	})
	if err != nil {
		return "", time.Time{}, ErrNotFound
	}
	_, raw, err := crypto.RandomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	exp := s.now().Add(sessionTTL)
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `INSERT INTO portal_sessions (token_hash, organisation_id, party_id, expires_at) VALUES ($1,$2,$3,$4)`, crypto.HashToken(raw), r.org, party, exp)
		return err
	}); err != nil {
		return "", time.Time{}, err
	}
	return raw, exp, nil
}

type session struct {
	org, party uuid.UUID
	r          *resolved
}

func (s *Service) session(ctx context.Context, slug, token string) (*session, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	r, err := s.resolve(ctx, slug)
	if err != nil {
		return nil, err
	}
	var party uuid.UUID
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT party_id FROM portal_sessions WHERE token_hash = $1 AND organisation_id = $2 AND expires_at > now()`, crypto.HashToken(token), r.org).Scan(&party)
	}); err != nil {
		return nil, ErrNotFound
	}
	return &session{org: r.org, party: party, r: r}, nil
}

// Signout ends a session.
func (s *Service) Signout(ctx context.Context, token string) {
	_ = s.pool.Run(ctx, func(ctx context.Context) error {
		_, err := s.pool.Q(ctx).Exec(ctx, `DELETE FROM portal_sessions WHERE token_hash = $1`, crypto.HashToken(token))
		return err
	})
}

type Portal struct {
	Customer  string      `json:"customer"`
	Shop      string      `json:"shop"`
	Documents []PortalDoc `json:"documents"`
	CanPay    bool        `json:"can_pay"`
}

// Portal lists the signed-in customer's finalized documents.
func (s *Service) Portal(ctx context.Context, slug, token string) (*Portal, error) {
	sess, err := s.session(ctx, slug, token)
	if err != nil {
		return nil, err
	}
	out := &Portal{Shop: sess.r.title, Documents: []PortalDoc{}}
	err = s.pool.RunScoped(ctx, sess.org, func(ctx context.Context) error {
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT legal_name FROM parties WHERE id = $1`, sess.party).Scan(&out.Customer); err != nil {
			return err
		}
		rows, err := s.pool.Q(ctx).Query(ctx, `
			SELECT d.id, d.document_number, d.document_type, to_char(d.issue_date, 'YYYY-MM-DD'), COALESCE(d.grand_total_amount, 0)::text, d.currency_code,
			       CASE WHEN d.document_type IN ('TAX_INVOICE', 'POS_INVOICE', 'DEBIT_NOTE')
			            THEN (COALESCE(d.grand_total_amount, 0) - COALESCE((SELECT SUM(r.amount) FROM receipts r WHERE r.sales_document_id = d.id), 0))::text ELSE '' END
			FROM sales_documents d WHERE d.organisation_id = $1 AND d.customer_party_id = $2 AND d.status = 'FINALIZED'
			  AND d.document_type IN ('QUOTATION', 'PROFORMA_INVOICE', 'SALES_ORDER', 'DELIVERY_CHALLAN', 'TAX_INVOICE', 'POS_INVOICE', 'CREDIT_NOTE', 'DEBIT_NOTE')
			ORDER BY d.issue_date DESC, d.document_number DESC LIMIT 200`, sess.org, sess.party)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d PortalDoc
			if err := rows.Scan(&d.ID, &d.Number, &d.Type, &d.Date, &d.Total, &d.Currency, &d.Open); err != nil {
				return err
			}
			for _, p := range []*string{&d.Total, &d.Open} {
				if v, err := decimal.NewFromString(*p); err == nil {
					*p = v.StringFixed(2)
				}
			}
			if d.Open == "0.00" {
				d.Open = ""
			}
			out.Documents = append(out.Documents, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	out.CanPay = s.pay != nil
	return out, nil
}

func (s *Service) owns(ctx context.Context, sess *session, docID uuid.UUID) (string, error) {
	var typ string
	err := s.pool.RunScoped(ctx, sess.org, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT document_type FROM sales_documents WHERE id = $1 AND customer_party_id = $2 AND status = 'FINALIZED'`, docID, sess.party).Scan(&typ)
	})
	if err != nil {
		return "", ErrNotFound
	}
	return typ, nil
}

// Document renders one of the signed-in customer's documents.
func (s *Service) Document(ctx context.Context, slug, token string, docID uuid.UUID) ([]byte, string, string, error) {
	sess, err := s.session(ctx, slug, token)
	if err != nil {
		return nil, "", "", err
	}
	if _, err := s.owns(ctx, sess, docID); err != nil {
		return nil, "", "", err
	}
	return s.renderer.Render(ctx, sess.org, sess.r.acting, "sales_document", docID)
}

// PayLink creates a card or PayPal payment link for one of the customer's open invoices.
func (s *Service) PayLink(ctx context.Context, slug, token, ip string, docID uuid.UUID, provider, baseURL string) (string, error) {
	if !s.limiter.allow("pay:"+ip, 20, time.Hour, s.now()) {
		return "", ErrLimit
	}
	sess, err := s.session(ctx, slug, token)
	if err != nil {
		return "", err
	}
	if _, err := s.owns(ctx, sess, docID); err != nil {
		return "", err
	}
	l, err := s.pay.CreateLink(ctx, permissions.Principal{UserID: sess.r.acting, OrganisationID: sess.org}, docID, strings.ToUpper(provider), baseURL)
	if err != nil {
		return "", err
	}
	return l.URL, nil
}

// SignLink opens a signature request for one of the customer's documents and returns the link to sign it.
func (s *Service) SignLink(ctx context.Context, slug, token, ip string, docID uuid.UUID, baseURL string) (string, error) {
	if !s.limiter.allow("sign:"+ip, 20, time.Hour, s.now()) {
		return "", ErrLimit
	}
	sess, err := s.session(ctx, slug, token)
	if err != nil {
		return "", err
	}
	if _, err := s.owns(ctx, sess, docID); err != nil {
		return "", err
	}
	var name string
	_ = s.pool.RunScoped(ctx, sess.org, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT legal_name FROM parties WHERE id = $1`, sess.party).Scan(&name)
	})
	_, raw, err := s.esign.Create(ctx, permissions.Principal{UserID: sess.r.acting, OrganisationID: sess.org}, "sales_document", docID, name, "", "")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(baseURL, "/") + "/sign/" + raw, nil
}
