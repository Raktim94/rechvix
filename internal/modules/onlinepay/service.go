// Package onlinepay lets a customer pay an invoice by card (Stripe Checkout) or PayPal through a link. A confirmed payment
// is recorded as a receipt against the invoice automatically.
package onlinepay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

var (
	ErrInvalid       = errors.New("onlinepay: invalid request")
	ErrNotFound      = errors.New("onlinepay: not found")
	ErrNotConfigured = errors.New("onlinepay: that payment provider is not set up")
)

const (
	Stripe_ = "STRIPE"
	PayPal_ = "PAYPAL"
)

type ProviderStatus struct {
	Provider      string `json:"provider"`
	Enabled       bool   `json:"enabled"`
	Live          bool   `json:"live"`
	ClientID      string `json:"client_id,omitempty"`
	HasSecret     bool   `json:"has_secret"`
	HasWebhookKey bool   `json:"has_webhook_secret"`
}

type ProviderInput struct {
	Enabled       bool
	Live          bool
	ClientID      string
	Secret        string // empty keeps the stored secret
	WebhookSecret string // empty keeps the stored secret
}

type Link struct {
	ID        uuid.UUID  `json:"id"`
	Provider  string     `json:"provider"`
	URL       string     `json:"url"`
	Amount    string     `json:"amount"`
	Currency  string     `json:"currency"`
	Status    string     `json:"status"`
	PaidAt    *time.Time `json:"paid_at,omitempty"`
	Note      string     `json:"note,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Service struct {
	pool        *database.Pool
	accounting  *accountingapp.Service
	aead        *appcrypto.AEAD
	permissions *permissions.Checker
	audit       audit.Recorder
	now         func() time.Time
	// test hooks
	stripeBase, paypalBase string
}

func NewService(pool *database.Pool, acct *accountingapp.Service, aead *appcrypto.AEAD, p *permissions.Checker, a audit.Recorder) *Service {
	return &Service{pool: pool, accounting: acct, aead: aead, permissions: p, audit: a, now: time.Now}
}

// WithBaseURLs points the provider clients at other servers (tests only).
func (s *Service) WithBaseURLs(stripe, paypal string) *Service {
	s.stripeBase, s.paypalBase = stripe, paypal
	return s
}

func (s *Service) manage(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "settings.manage", permissions.Scope{})
}
func (s *Service) share(ctx context.Context, p permissions.Principal) error {
	return s.permissions.Require(ctx, p, "notifications.share", permissions.Scope{})
}

func aad(org uuid.UUID, provider, field string) []byte {
	return []byte(org.String() + ":" + provider + ":" + field)
}

func (s *Service) Providers(ctx context.Context, principal permissions.Principal) ([]ProviderStatus, error) {
	if err := s.permissions.Require(ctx, principal, "settings.view", permissions.Scope{}); err != nil {
		return nil, err
	}
	out := []ProviderStatus{{Provider: Stripe_}, {Provider: PayPal_}}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT provider, enabled, live, COALESCE(client_id, ''), secret_encrypted IS NOT NULL, webhook_secret_encrypted IS NOT NULL FROM payment_providers WHERE organisation_id = $1`, principal.OrganisationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x ProviderStatus
			if err := rows.Scan(&x.Provider, &x.Enabled, &x.Live, &x.ClientID, &x.HasSecret, &x.HasWebhookKey); err != nil {
				return err
			}
			for i := range out {
				if out[i].Provider == x.Provider {
					out[i] = x
				}
			}
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) SaveProvider(ctx context.Context, principal permissions.Principal, provider string, in ProviderInput) error {
	if err := s.manage(ctx, principal); err != nil {
		return err
	}
	if provider != Stripe_ && provider != PayPal_ {
		return fmt.Errorf("%w: unknown provider", ErrInvalid)
	}
	var sec, hook []byte
	var err error
	if in.Secret != "" {
		if sec, err = s.aead.Seal([]byte(in.Secret), aad(principal.OrganisationID, provider, "secret")); err != nil {
			return err
		}
	}
	if in.WebhookSecret != "" {
		if hook, err = s.aead.Seal([]byte(in.WebhookSecret), aad(principal.OrganisationID, provider, "webhook")); err != nil {
			return err
		}
	}
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO payment_providers (organisation_id, provider, enabled, live, client_id, secret_encrypted, webhook_secret_encrypted, updated_by)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8)
			ON CONFLICT (organisation_id, provider) DO UPDATE SET enabled = EXCLUDED.enabled, live = EXCLUDED.live,
				client_id = COALESCE(NULLIF(EXCLUDED.client_id, ''), payment_providers.client_id),
				secret_encrypted = COALESCE(EXCLUDED.secret_encrypted, payment_providers.secret_encrypted),
				webhook_secret_encrypted = COALESCE(EXCLUDED.webhook_secret_encrypted, payment_providers.webhook_secret_encrypted),
				updated_by = EXCLUDED.updated_by, updated_at = now()`,
			principal.OrganisationID, provider, in.Enabled, in.Live, in.ClientID, sec, hook, principal.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "onlinepay.provider_changed", EntityType: "payment_provider", AfterState: map[string]any{"provider": provider, "enabled": in.Enabled, "live": in.Live}, At: s.now()})
	})
}

type cfg struct {
	enabled, live   bool
	clientID        string
	secret, webhook string
}

func (s *Service) load(ctx context.Context, org uuid.UUID, provider string) (*cfg, error) {
	var c cfg
	var sec, hook []byte
	var cid *string
	err := s.pool.Q(ctx).QueryRow(ctx, `SELECT enabled, live, client_id, secret_encrypted, webhook_secret_encrypted FROM payment_providers WHERE organisation_id = $1 AND provider = $2`, org, provider).
		Scan(&c.enabled, &c.live, &cid, &sec, &hook)
	if err != nil {
		return nil, ErrNotConfigured
	}
	if cid != nil {
		c.clientID = *cid
	}
	if len(sec) > 0 {
		b, err := s.aead.Open(sec, aad(org, provider, "secret"))
		if err != nil {
			return nil, fmt.Errorf("onlinepay: the stored key cannot be read (was AEAD_ENCRYPTION_KEY changed?): %w", err)
		}
		c.secret = string(b)
	}
	if len(hook) > 0 {
		b, err := s.aead.Open(hook, aad(org, provider, "webhook"))
		if err != nil {
			return nil, err
		}
		c.webhook = string(b)
	}
	return &c, nil
}

type docInfo struct {
	number      string
	partyID     uuid.UUID
	legalEntity uuid.UUID
	currency    string
	baseCur     string
	status      string
	docType     string
	outstanding decimal.Decimal
}

func (s *Service) document(ctx context.Context, id uuid.UUID) (*docInfo, error) {
	var d docInfo
	var total decimal.Decimal
	var paid, credited decimal.Decimal
	err := s.pool.Q(ctx).QueryRow(ctx, `
		SELECT d.document_number, d.customer_party_id, d.legal_entity_id, d.currency_code, d.base_currency_code, d.status, d.document_type, COALESCE(d.grand_total_amount, 0),
		       COALESCE((SELECT SUM(r.amount) FROM receipts r WHERE r.sales_document_id = d.id), 0),
		       COALESCE((SELECT SUM(COALESCE(c.grand_total_amount, 0)) FROM sales_documents c WHERE c.reference_document_id = d.id AND c.status = 'FINALIZED' AND c.document_type IN ('CREDIT_NOTE', 'SALES_RETURN')), 0)
		FROM sales_documents d WHERE d.id = $1`, id).Scan(&d.number, &d.partyID, &d.legalEntity, &d.currency, &d.baseCur, &d.status, &d.docType, &total, &paid, &credited)
	if err != nil {
		return nil, ErrNotFound
	}
	d.outstanding = total.Sub(paid).Sub(credited)
	return &d, nil
}

// CreateLink makes a payment page for what is still owed on an invoice and returns it.
func (s *Service) CreateLink(ctx context.Context, principal permissions.Principal, documentID uuid.UUID, provider, baseURL string) (*Link, error) {
	if err := s.share(ctx, principal); err != nil {
		return nil, err
	}
	if provider != Stripe_ && provider != PayPal_ {
		return nil, fmt.Errorf("%w: choose Stripe or PayPal", ErrInvalid)
	}
	var link *Link
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		d, err := s.document(ctx, documentID)
		if err != nil {
			return err
		}
		if d.status != "FINALIZED" || (d.docType != "TAX_INVOICE" && d.docType != "POS_INVOICE" && d.docType != "DEBIT_NOTE") {
			return fmt.Errorf("%w: only a finalized invoice can be paid online", ErrInvalid)
		}
		if !d.outstanding.IsPositive() {
			return fmt.Errorf("%w: nothing is outstanding on this invoice", ErrInvalid)
		}
		if !strings.EqualFold(d.currency, d.baseCur) {
			return fmt.Errorf("%w: online payment is only available for invoices in the company currency (%s)", ErrInvalid, d.baseCur)
		}
		c, err := s.load(ctx, principal.OrganisationID, provider)
		if err != nil {
			return err
		}
		if !c.enabled || c.secret == "" {
			return ErrNotConfigured
		}
		id, _ := uuid.NewV7()
		req := CheckoutRequest{LinkID: id.String(), Description: "Invoice " + d.number, Amount: d.outstanding.Round(2), Currency: d.currency,
			SuccessURL: strings.TrimRight(baseURL, "/") + "/api/v1/payments/return?link=" + id.String(), CancelURL: strings.TrimRight(baseURL, "/") + "/api/v1/payments/cancelled"}
		var co *Checkout
		switch provider {
		case Stripe_:
			req.SuccessURL = strings.TrimRight(baseURL, "/") + "/api/v1/payments/thanks"
			co, err = Stripe{Base: s.stripeBase, SecretKey: c.secret}.CreateCheckout(ctx, req)
		default:
			co, err = PayPal{Base: s.paypalBase, ClientID: c.clientID, ClientSecret: c.secret, Live: c.live}.CreateCheckout(ctx, req)
		}
		if err != nil {
			return err
		}
		if _, err := s.pool.Q(ctx).Exec(ctx, `
			INSERT INTO payment_links (id, organisation_id, sales_document_id, provider, external_id, url, amount, currency_code, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, principal.OrganisationID, documentID, provider, co.ExternalID, co.URL, req.Amount, d.currency, principal.UserID); err != nil {
			return err
		}
		link = &Link{ID: id, Provider: provider, URL: co.URL, Amount: req.Amount.StringFixed(2), Currency: d.currency, Status: "PENDING", CreatedAt: s.now()}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: principal.OrganisationID, ActorUserID: &principal.UserID, ActorType: audit.ActorUser,
			Action: "onlinepay.link_created", EntityType: "sales_document", EntityID: &documentID, AfterState: map[string]any{"provider": provider, "amount": link.Amount}, At: s.now()})
	})
	return link, err
}

func (s *Service) Links(ctx context.Context, principal permissions.Principal, documentID uuid.UUID) ([]Link, error) {
	if err := s.share(ctx, principal); err != nil {
		return nil, err
	}
	out := []Link{}
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		rows, err := s.pool.Q(ctx).Query(ctx, `SELECT id, provider, url, amount::text, currency_code, status, paid_at, COALESCE(note, ''), created_at FROM payment_links WHERE sales_document_id = $1 ORDER BY created_at DESC`, documentID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l Link
			if err := rows.Scan(&l.ID, &l.Provider, &l.URL, &l.Amount, &l.Currency, &l.Status, &l.PaidAt, &l.Note, &l.CreatedAt); err != nil {
				return err
			}
			l.Amount = strings.TrimRight(strings.TrimRight(l.Amount, "0"), ".")
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

type linkRow struct {
	id, doc, createdBy uuid.UUID
	amount             decimal.Decimal
	currency           string
	provider, external string
	status             string
}

func (s *Service) linkByExternal(ctx context.Context, provider, external string) (*linkRow, error) {
	var l linkRow
	err := s.pool.Q(ctx).QueryRow(ctx, `SELECT id, sales_document_id, created_by, amount, currency_code, provider, external_id, status FROM payment_links WHERE provider = $1 AND external_id = $2`, provider, external).
		Scan(&l.id, &l.doc, &l.createdBy, &l.amount, &l.currency, &l.provider, &l.external, &l.status)
	if err != nil {
		return nil, ErrNotFound
	}
	return &l, nil
}

// markPaid records a confirmed payment exactly once: the link moves from PENDING to PAID, then a receipt is booked against
// the invoice. A repeated webhook finds the link already paid and does nothing.
func (s *Service) markPaid(ctx context.Context, orgID uuid.UUID, l *linkRow, paid decimal.Decimal, ref string) error {
	return s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		tag, err := s.pool.Q(ctx).Exec(ctx, `UPDATE payment_links SET status = 'PAID', paid_at = now() WHERE id = $1 AND status = 'PENDING'`, l.id)
		if err != nil {
			return err
		}
		if tag == 0 {
			return nil // already processed
		}
		var party uuid.UUID
		if err := s.pool.Q(ctx).QueryRow(ctx, `SELECT customer_party_id FROM sales_documents WHERE id = $1`, l.doc).Scan(&party); err != nil {
			return err
		}
		method := accountingdomain.MethodCard
		if l.provider == PayPal_ {
			method = accountingdomain.MethodOther
		}
		doc := l.doc
		principal := permissions.Principal{UserID: l.createdBy, OrganisationID: orgID}
		rec, err := s.accounting.RecordReceipt(ctx, principal, accountingapp.RecordReceiptParams{PartyID: party, SalesDocumentID: &doc, Amount: paid, Method: method, ReferenceNumber: ref, ReceivedAt: s.now()})
		note := ""
		if err != nil {
			note = "Paid online, but the receipt could not be recorded automatically (" + err.Error() + "). Record it by hand."
			_, _ = s.pool.Q(ctx).Exec(ctx, `UPDATE payment_links SET note = $2 WHERE id = $1`, l.id, note)
		} else {
			_, _ = s.pool.Q(ctx).Exec(ctx, `UPDATE payment_links SET receipt_id = $2 WHERE id = $1`, l.id, rec.ID)
		}
		return s.audit.Record(ctx, audit.Entry{OrganisationID: orgID, ActorType: audit.ActorSystem, Action: "onlinepay.paid", EntityType: "sales_document", EntityID: &doc,
			AfterState: map[string]any{"provider": l.provider, "amount": paid.StringFixed(2), "reference": ref, "receipt_recorded": err == nil}, At: s.now()})
	})
}

// HandleStripeWebhook verifies and applies a Stripe event for an organisation.
func (s *Service) HandleStripeWebhook(ctx context.Context, orgID uuid.UUID, payload []byte, signature string) error {
	var secret string
	if err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		c, err := s.load(ctx, orgID, Stripe_)
		if err != nil {
			return err
		}
		secret = c.webhook
		return nil
	}); err != nil {
		return err
	}
	ev, err := ParseStripeWebhook(payload, signature, secret, s.now())
	if err != nil {
		return err
	}
	if ev.Type != "checkout.session.completed" && ev.Type != "checkout.session.async_payment_succeeded" {
		return nil
	}
	if !ev.Paid {
		return nil
	}
	var l *linkRow
	if err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		var err error
		l, err = s.linkByExternal(ctx, Stripe_, ev.ID)
		return err
	}); err != nil {
		return err
	}
	paid := decimal.NewFromInt(ev.Amount).Div(decimal.NewFromInt(100))
	if !strings.EqualFold(ev.Cur, l.currency) || !paid.Equal(l.amount) {
		return fmt.Errorf("onlinepay: the paid amount %s %s does not match the link %s %s; not recorded", paid.StringFixed(2), ev.Cur, l.amount.StringFixed(2), l.currency)
	}
	return s.markPaid(ctx, orgID, l, paid, firstNonEmpty(ev.Intent, ev.ID))
}

// CompletePayPal captures an approved order (the customer was sent back to us) and records the payment.
func (s *Service) CompletePayPal(ctx context.Context, linkID uuid.UUID, orderID string) (bool, error) {
	var orgID uuid.UUID
	if err := s.pool.Run(ctx, func(ctx context.Context) error {
		return s.pool.Q(ctx).QueryRow(ctx, `SELECT payment_link_organisation($1, 'PAYPAL', $2)`, linkID, orderID).Scan(&orgID)
	}); err != nil {
		return false, ErrNotFound
	}
	var l *linkRow
	var c *cfg
	if err := s.pool.RunScoped(ctx, orgID, func(ctx context.Context) error {
		var err error
		if l, err = s.linkByExternal(ctx, PayPal_, orderID); err != nil {
			return err
		}
		c, err = s.load(ctx, orgID, PayPal_)
		return err
	}); err != nil {
		return false, err
	}
	if l.status == "PAID" {
		return true, nil
	}
	done, amount, cur, capID, err := PayPal{Base: s.paypalBase, ClientID: c.clientID, ClientSecret: c.secret, Live: c.live}.Capture(ctx, orderID)
	if err != nil || !done {
		return false, err
	}
	if !strings.EqualFold(cur, l.currency) || !amount.Equal(l.amount) {
		return false, fmt.Errorf("onlinepay: the captured amount does not match the link; not recorded")
	}
	return true, s.markPaid(ctx, orgID, l, amount, capID)
}
