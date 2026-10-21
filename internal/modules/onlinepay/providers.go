package onlinepay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Default endpoints. Tests point the clients at local servers.
const (
	StripeBase        = "https://api.stripe.com"
	PayPalLiveBase    = "https://api-m.paypal.com"
	PayPalSandboxBase = "https://api-m.sandbox.paypal.com"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// CheckoutRequest is what a provider needs to create a payment page.
type CheckoutRequest struct {
	LinkID      string
	Description string
	Amount      decimal.Decimal
	Currency    string
	SuccessURL  string
	CancelURL   string
}

// Checkout is the payment page the customer is sent to.
type Checkout struct {
	ExternalID string
	URL        string
}

// minorUnits converts an amount to the currency's smallest unit (cents). Zero-decimal currencies are not supported
// here and are refused rather than charged wrongly.
func minorUnits(a decimal.Decimal, currency string) (int64, error) {
	switch strings.ToUpper(currency) {
	case "JPY", "KRW", "VND", "CLP", "ISK", "UGX", "XAF", "XOF", "BIF", "DJF", "GNF", "KMF", "PYG", "RWF", "VUV", "XPF":
		return 0, fmt.Errorf("onlinepay: %s has no minor unit and is not supported for online payment", currency)
	case "BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND":
		return 0, fmt.Errorf("onlinepay: %s has three decimals and is not supported for online payment", currency)
	}
	return a.Mul(decimal.NewFromInt(100)).Round(0).IntPart(), nil
}

// ---- Stripe

type Stripe struct {
	Base      string
	SecretKey string
}

func (s Stripe) base() string {
	if s.Base != "" {
		return s.Base
	}
	return StripeBase
}

func (s Stripe) CreateCheckout(ctx context.Context, r CheckoutRequest) (*Checkout, error) {
	cents, err := minorUnits(r.Amount, r.Currency)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("mode", "payment")
	v.Set("success_url", r.SuccessURL)
	v.Set("cancel_url", r.CancelURL)
	v.Set("client_reference_id", r.LinkID)
	v.Set("metadata[payment_link_id]", r.LinkID)
	v.Set("line_items[0][quantity]", "1")
	v.Set("line_items[0][price_data][currency]", strings.ToLower(r.Currency))
	v.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(cents, 10))
	v.Set("line_items[0][price_data][product_data][name]", r.Description)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base()+"/v1/checkout/sessions", strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(s.SecretKey, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", "rechvix-"+r.LinkID)
	body, status, err := do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &out)
	if status >= 300 || out.ID == "" || out.URL == "" {
		return nil, fmt.Errorf("onlinepay: Stripe refused the payment page (HTTP %d): %s", status, firstNonEmpty(out.Error.Message, snippet(body)))
	}
	return &Checkout{ExternalID: out.ID, URL: out.URL}, nil
}

// StripeEvent is the part of a webhook event the app needs.
type StripeEvent struct {
	Type   string
	Link   string
	ID     string // checkout session id
	Paid   bool
	Amount int64
	Cur    string
	Intent string
}

// ParseStripeWebhook verifies the Stripe-Signature header (HMAC-SHA256 over "timestamp.payload", with a five-minute
// tolerance) and returns the event. A bad signature or stale timestamp is an error.
func ParseStripeWebhook(payload []byte, header, secret string, now time.Time) (*StripeEvent, error) {
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == "" || len(sigs) == 0 || secret == "" {
		return nil, errors.New("onlinepay: missing webhook signature")
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, errors.New("onlinepay: bad webhook timestamp")
	}
	if d := now.Sub(time.Unix(t, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return nil, errors.New("onlinepay: webhook timestamp outside the allowed window")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	ok := false
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			ok = true
		}
	}
	if !ok {
		return nil, errors.New("onlinepay: webhook signature does not match")
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID                string            `json:"id"`
				PaymentStatus     string            `json:"payment_status"`
				AmountTotal       int64             `json:"amount_total"`
				Currency          string            `json:"currency"`
				ClientReferenceID string            `json:"client_reference_id"`
				PaymentIntent     string            `json:"payment_intent"`
				Metadata          map[string]string `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, fmt.Errorf("onlinepay: unreadable webhook: %w", err)
	}
	o := ev.Data.Object
	link := firstNonEmpty(o.ClientReferenceID, o.Metadata["payment_link_id"])
	return &StripeEvent{Type: ev.Type, Link: link, ID: o.ID, Paid: o.PaymentStatus == "paid", Amount: o.AmountTotal, Cur: strings.ToUpper(o.Currency), Intent: o.PaymentIntent}, nil
}

// ---- PayPal

type PayPal struct {
	Base         string
	ClientID     string
	ClientSecret string
	Live         bool
}

func (p PayPal) base() string {
	switch {
	case p.Base != "":
		return p.Base
	case p.Live:
		return PayPalLiveBase
	default:
		return PayPalSandboxBase
	}
}

func (p PayPal) token(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base()+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(p.ClientID, p.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, status, err := do(req)
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(body, &out)
	if status >= 300 || out.AccessToken == "" {
		return "", fmt.Errorf("onlinepay: PayPal rejected the credentials (HTTP %d)", status)
	}
	return out.AccessToken, nil
}

func (p PayPal) CreateCheckout(ctx context.Context, r CheckoutRequest) (*Checkout, error) {
	if _, err := minorUnits(r.Amount, r.Currency); err != nil {
		return nil, err
	}
	tok, err := p.token(ctx)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []map[string]any{{
			"reference_id": r.LinkID, "custom_id": r.LinkID, "description": r.Description,
			"amount": map[string]string{"currency_code": strings.ToUpper(r.Currency), "value": r.Amount.StringFixed(2)},
		}},
		"payment_source": map[string]any{"paypal": map[string]any{"experience_context": map[string]string{
			"return_url": r.SuccessURL, "cancel_url": r.CancelURL, "user_action": "PAY_NOW",
		}}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base()+"/v2/checkout/orders", strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PayPal-Request-Id", "rechvix-"+r.LinkID)
	body, status, err := do(req)
	if err != nil {
		return nil, err
	}
	var out struct {
		ID    string `json:"id"`
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	_ = json.Unmarshal(body, &out)
	var href string
	for _, l := range out.Links {
		if l.Rel == "payer-action" || l.Rel == "approve" {
			href = l.Href
		}
	}
	if status >= 300 || out.ID == "" || href == "" {
		return nil, fmt.Errorf("onlinepay: PayPal refused the order (HTTP %d): %s", status, snippet(body))
	}
	return &Checkout{ExternalID: out.ID, URL: href}, nil
}

// Capture takes the payment for an approved order and reports whether it completed and the captured amount.
func (p PayPal) Capture(ctx context.Context, orderID string) (completed bool, amount decimal.Decimal, currency, captureID string, err error) {
	tok, err := p.token(ctx)
	if err != nil {
		return false, decimal.Zero, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base()+"/v2/checkout/orders/"+url.PathEscape(orderID)+"/capture", strings.NewReader("{}"))
	if err != nil {
		return false, decimal.Zero, "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	body, status, err := do(req)
	if err != nil {
		return false, decimal.Zero, "", "", err
	}
	var out struct {
		Status        string `json:"status"`
		PurchaseUnits []struct {
			Payments struct {
				Captures []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Amount struct {
						Value    string `json:"value"`
						Currency string `json:"currency_code"`
					} `json:"amount"`
				} `json:"captures"`
			} `json:"payments"`
		} `json:"purchase_units"`
	}
	_ = json.Unmarshal(body, &out)
	if status >= 300 && !strings.Contains(string(body), "ORDER_ALREADY_CAPTURED") {
		return false, decimal.Zero, "", "", fmt.Errorf("onlinepay: PayPal capture failed (HTTP %d): %s", status, snippet(body))
	}
	for _, pu := range out.PurchaseUnits {
		for _, c := range pu.Payments.Captures {
			if c.Status == "COMPLETED" {
				a, _ := decimal.NewFromString(c.Amount.Value)
				return true, a, c.Amount.Currency, c.ID, nil
			}
		}
	}
	return false, decimal.Zero, "", "", nil
}

func do(req *http.Request) ([]byte, int, error) {
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("onlinepay: contacting the payment provider: %w", err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return b, res.StatusCode, err
}

func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
