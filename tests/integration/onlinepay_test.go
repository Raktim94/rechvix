//go:build integration

package integration

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"rechvix/internal/modules/onlinepay"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/permissions"
)

func stripeSigned(secret string, payload []byte, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func receiptTotal(t *testing.T, ctx context.Context, fx accountingFixture, docID uuid.UUID) decimal.Decimal {
	t.Helper()
	var s decimal.Decimal
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM receipts WHERE sales_document_id = $1`, docID).Scan(&s)
	})
	return s
}

func TestOnlinePay_StripeAndPayPalRecordReceiptsOnce(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	doc := finalizedSale(t, ctx, salesSvc, fx, "2", "50") // 100 + 21% IVA = 121.00

	var stripeForm url.Values
	var paypalCaptured int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/checkout/sessions":
			if u, _, _ := r.BasicAuth(); u != "sk_test_secret" {
				http.Error(w, `{"error":{"message":"Invalid API Key provided"}}`, 401)
				return
			}
			r.ParseForm()
			stripeForm = r.PostForm
			json.NewEncoder(w).Encode(map[string]string{"id": "cs_test_123", "url": "https://checkout.stripe.test/c/cs_test_123"})
		case r.URL.Path == "/v1/oauth2/token":
			if u, p, _ := r.BasicAuth(); u != "pp-client" || p != "pp-secret" {
				http.Error(w, "{}", 401)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "A21AAtoken"})
		case r.URL.Path == "/v2/checkout/orders":
			json.NewEncoder(w).Encode(map[string]any{"id": "ORDER123", "links": []map[string]string{{"rel": "payer-action", "href": "https://paypal.test/checkoutnow?token=ORDER123"}}})
		case strings.HasSuffix(r.URL.Path, "/capture"):
			paypalCaptured++
			json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED", "purchase_units": []map[string]any{{"payments": map[string]any{"captures": []map[string]any{{"id": "CAP-9", "status": "COMPLETED", "amount": map[string]string{"value": "121.00", "currency_code": "EUR"}}}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	aead, _ := appcrypto.NewAEAD(key)
	svc := onlinepay.NewService(sharedPool, acct, aead, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool)).WithBaseURLs(fake.URL, fake.URL)

	if _, err := svc.CreateLink(ctx, fx.Principal, doc.ID, "STRIPE", "https://shop.example"); err == nil {
		t.Fatal("a provider that is not set up cannot create a link")
	}
	if err := svc.SaveProvider(ctx, fx.Principal, "STRIPE", onlinepay.ProviderInput{Enabled: true, Secret: "sk_test_secret", WebhookSecret: "whsec_test"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProvider(ctx, fx.Principal, "PAYPAL", onlinepay.ProviderInput{Enabled: true, ClientID: "pp-client", Secret: "pp-secret"}); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.Providers(ctx, fx.Principal)
	if !st[0].Enabled || !st[0].HasSecret || !st[0].HasWebhookKey || st[1].ClientID != "pp-client" {
		t.Fatalf("provider status never exposes secrets but shows they exist: %+v", st)
	}
	// Secrets are encrypted at rest.
	var raw []byte
	_ = sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx, `SELECT secret_encrypted FROM payment_providers WHERE provider = 'STRIPE'`).Scan(&raw)
	})
	if strings.Contains(string(raw), "sk_test_secret") || len(raw) == 0 {
		t.Fatal("the secret key must be stored encrypted")
	}

	link, err := svc.CreateLink(ctx, fx.Principal, doc.ID, "STRIPE", "https://shop.example")
	if err != nil {
		t.Fatalf("stripe link: %v", err)
	}
	if link.URL != "https://checkout.stripe.test/c/cs_test_123" || link.Amount != "121.00" {
		t.Fatalf("link: %+v", link)
	}
	if stripeForm.Get("line_items[0][price_data][unit_amount]") != "12100" || stripeForm.Get("line_items[0][price_data][currency]") != "eur" || stripeForm.Get("client_reference_id") != link.ID.String() {
		t.Fatalf("Stripe must be asked for 12100 cents in EUR for this link: %v", stripeForm)
	}

	event := func(amountCents int64, status string) []byte {
		b, _ := json.Marshal(map[string]any{"type": "checkout.session.completed", "data": map[string]any{"object": map[string]any{
			"id": "cs_test_123", "payment_status": status, "amount_total": amountCents, "currency": "eur", "client_reference_id": link.ID.String(), "payment_intent": "pi_777"}}})
		return b
	}
	org := fx.Principal.OrganisationID
	if err := svc.HandleStripeWebhook(ctx, org, event(12100, "paid"), stripeSigned("wrong-secret", event(12100, "paid"), time.Now())); err == nil {
		t.Fatal("a webhook with the wrong signature must be rejected")
	}
	if err := svc.HandleStripeWebhook(ctx, org, event(12100, "paid"), stripeSigned("whsec_test", event(12100, "paid"), time.Now().Add(-time.Hour))); err == nil {
		t.Fatal("a stale webhook must be rejected")
	}
	if err := svc.HandleStripeWebhook(ctx, org, event(5000, "paid"), stripeSigned("whsec_test", event(5000, "paid"), time.Now())); err == nil {
		t.Fatal("a payment for a different amount than the link must not be recorded")
	}
	if !receiptTotal(t, ctx, fx, doc.ID).IsZero() {
		t.Fatal("nothing may be recorded yet")
	}
	if err := svc.HandleStripeWebhook(ctx, org, event(12100, "unpaid"), stripeSigned("whsec_test", event(12100, "unpaid"), time.Now())); err != nil {
		t.Fatal(err)
	}
	if !receiptTotal(t, ctx, fx, doc.ID).IsZero() {
		t.Fatal("an unpaid session records nothing")
	}
	good := event(12100, "paid")
	for i := 0; i < 2; i++ { // Stripe retries: the receipt must exist exactly once
		if err := svc.HandleStripeWebhook(ctx, org, good, stripeSigned("whsec_test", good, time.Now())); err != nil {
			t.Fatalf("webhook %d: %v", i, err)
		}
	}
	if got := receiptTotal(t, ctx, fx, doc.ID); !got.Equal(decimal.RequireFromString("121")) {
		t.Fatalf("one receipt of 121 expected, total %s", got)
	}
	links, _ := svc.Links(ctx, fx.Principal, doc.ID)
	if len(links) != 1 || links[0].Status != "PAID" || links[0].PaidAt == nil {
		t.Fatalf("link must be marked paid: %+v", links)
	}
	if _, err := svc.CreateLink(ctx, fx.Principal, doc.ID, "STRIPE", "https://shop.example"); err == nil {
		t.Fatal("nothing is outstanding any more")
	}

	// PayPal on a second invoice: create the order, then the customer returns and the order is captured once.
	doc2 := finalizedSale(t, ctx, salesSvc, fx, "2", "50")
	pl, err := svc.CreateLink(ctx, fx.Principal, doc2.ID, "PAYPAL", "https://shop.example")
	if err != nil || pl.URL != "https://paypal.test/checkoutnow?token=ORDER123" {
		t.Fatalf("paypal link: %+v %v", pl, err)
	}
	if _, err := svc.CompletePayPal(ctx, uuid.New(), "ORDER123"); err == nil {
		t.Fatal("an unknown link id is refused")
	}
	if _, err := svc.CompletePayPal(ctx, pl.ID, "OTHER-ORDER"); err == nil {
		t.Fatal("an order id that does not belong to the link is refused")
	}
	for i := 0; i < 2; i++ {
		done, err := svc.CompletePayPal(ctx, pl.ID, "ORDER123")
		if err != nil || !done {
			t.Fatalf("capture %d: %v %v", i, done, err)
		}
	}
	if paypalCaptured != 1 {
		t.Fatalf("PayPal must be asked to capture once, was %d times", paypalCaptured)
	}
	if got := receiptTotal(t, ctx, fx, doc2.ID); !got.Equal(decimal.RequireFromString("121")) {
		t.Fatalf("paypal receipt: %s", got)
	}
	_ = fmt.Sprint
}

func TestOnlinePay_RefusesZeroDecimalCurrencies(t *testing.T) {
	// minorUnits is unexported; the public behaviour is that Stripe/PayPal are never asked to charge such currencies wrongly.
	cs := onlinepay.CheckoutRequest{Amount: decimal.NewFromInt(100), Currency: "JPY"}
	if _, err := (onlinepay.Stripe{SecretKey: "x"}).CreateCheckout(context.Background(), cs); err == nil {
		t.Fatal("JPY must be refused")
	}
}
