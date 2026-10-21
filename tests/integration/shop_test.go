//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	esignapp "rechvix/internal/modules/esign"
	onlinepayapp "rechvix/internal/modules/onlinepay"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	"rechvix/internal/modules/shop"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

func priceEUR(t *testing.T, v string) money.Money {
	t.Helper()
	m, err := money.New(decimal.RequireFromString(v), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestShop_CatalogOrderAndClientArea(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	rec := audit.NewPGRecorder(sharedPool)
	pricing := pricingapp.NewService(sharedPool, pricingpg.NewPriceListRepo(sharedPool), pricingpg.NewPriceListItemRepo(sharedPool), checker, rec)
	list, err := pricing.EnsureDefaultPriceList(ctx, fx.Principal, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pricing.SetPrice(ctx, fx.Principal, pricingapp.SetPriceParams{PriceListID: list.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS, Price: priceEUR(t, "25.00")}); err != nil {
		t.Fatal(err)
	}
	mail := &captureMail{}
	notif, store := newNotifications(t, mail)
	contacts := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool), checker, rec)
	rend := &fakeRenderer{version: "v1"}
	es := esignapp.NewService(sharedPool, rend, nil, checker, rec)
	svc := shop.NewService(sharedPool, salesSvc, newTestCatalogueService(t), contacts, notif, onlinepayapp.NewService(sharedPool, acct, nil, checker, rec), es, rend, checker, rec)
	_ = catalogueapp.NewService

	if _, err := svc.Catalog(ctx, "nope-shop", ""); !errors.Is(err, shop.ErrNotFound) {
		t.Fatalf("an unknown or disabled shop is not found: %v", err)
	}
	set := shop.Settings{Enabled: true, Slug: "settings", Title: "Demo", LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID}
	if err := svc.SaveSettings(ctx, fx.Principal, set); !errors.Is(err, shop.ErrInvalid) {
		t.Fatalf("reserved address: %v", err)
	}
	slug := "demo-" + strings.ToLower(uuid.NewString()[:8])
	set.Slug = slug
	if err := svc.SaveSettings(ctx, fx.Principal, set); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.Catalog(ctx, slug, ""); len(c.Products) != 0 {
		t.Fatal("nothing is published yet")
	}
	if err := svc.SetPublished(ctx, fx.Principal, fx.VariantID, true, "Pump 25", "A reliable pump"); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Catalog(ctx, slug, "")
	if err != nil || len(c.Products) != 1 || c.Products[0].Price != "25.00" || !c.Products[0].InStock || c.Products[0].Title != "Pump 25" || c.Currency != "EUR" {
		t.Fatalf("catalog: %+v %v", c, err)
	}
	if c2, _ := svc.Catalog(ctx, slug, "zzz-no-match"); len(c2.Products) != 0 {
		t.Fatal("search filters the catalogue")
	}

	// An order: price comes from the price list, never from the visitor; the order is a draft for the team.
	o, err := svc.PlaceOrder(ctx, slug, "198.51.100.7", shop.OrderInput{Name: "Marta Gil", Email: "Marta@Example.org", Phone: "600111222", Address: "Calle Mayor 1, Madrid", Notes: "Call first",
		Lines: []shop.OrderLine{{VariantID: fx.VariantID, Quantity: 2}, {VariantID: fx.VariantID, Quantity: 1}}})
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if o.Total != "75.00" || !strings.HasPrefix(o.Reference, "WEB-") || o.DocumentID == nil {
		t.Fatalf("3 x 25.00 = 75.00: %+v", o)
	}
	doc, lines, err := salesSvc.GetDocument(ctx, fx.Principal, *o.DocumentID)
	if err != nil || doc.DocumentType != "SALES_ORDER" || doc.Status != "DRAFT" || len(lines) != 1 || !lines[0].Quantity.Equal(decimal.NewFromInt(3)) || !strings.Contains(doc.Notes, "Calle Mayor 1") {
		t.Fatalf("draft order: %+v lines=%d %v", doc, len(lines), err)
	}
	orders, _ := svc.Orders(ctx, fx.Principal)
	if len(orders) != 1 || orders[0].Email != "marta@example.org" {
		t.Fatalf("staff see the order: %+v", orders)
	}
	if _, err := svc.PlaceOrder(ctx, slug, "198.51.100.8", shop.OrderInput{Name: "X Y", Email: "x@y.co", Lines: []shop.OrderLine{{VariantID: uuid.New(), Quantity: 1}}}); !errors.Is(err, shop.ErrInvalid) {
		t.Fatalf("an unpublished or unknown product cannot be ordered: %v", err)
	}
	if _, err := svc.PlaceOrder(ctx, slug, "198.51.100.8", shop.OrderInput{Name: "X Y", Email: "bad", Lines: []shop.OrderLine{{VariantID: fx.VariantID, Quantity: 1}}}); !errors.Is(err, shop.ErrInvalid) {
		t.Fatalf("a bad email: %v", err)
	}
	if _, err := svc.PlaceOrder(ctx, slug, "198.51.100.8", shop.OrderInput{Name: "X Y", Email: "x@y.co", Lines: []shop.OrderLine{{VariantID: fx.VariantID, Quantity: 5000}}}); !errors.Is(err, shop.ErrInvalid) {
		t.Fatalf("an absurd quantity: %v", err)
	}
	// A second order by the same email reuses the customer.
	o2, err := svc.PlaceOrder(ctx, slug, "198.51.100.7", shop.OrderInput{Name: "Marta Gil", Email: "marta@example.org", Lines: []shop.OrderLine{{VariantID: fx.VariantID, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	d2, _, _ := salesSvc.GetDocument(ctx, fx.Principal, *o2.DocumentID)
	if d2.CustomerPartyID != doc.CustomerPartyID {
		t.Fatal("the same email must map to the same customer")
	}
	// Rate limiting: ten orders an hour per address.
	var limited bool
	for i := 0; i < 12; i++ {
		if _, err := svc.PlaceOrder(ctx, slug, "203.0.113.50", shop.OrderInput{Name: "Spam Bot", Email: "bot@example.org", Lines: []shop.OrderLine{{VariantID: fx.VariantID, Quantity: 1}}}); errors.Is(err, shop.ErrLimit) {
			limited = true
		}
	}
	if !limited {
		t.Fatal("the public order endpoint must be rate limited")
	}

	// Client area: sign-in by emailed one-time link.
	if err := svc.RequestLogin(ctx, slug, "198.51.100.7", "unknown@example.org", "https://shop.example"); err != nil {
		t.Fatalf("an unknown address gets the same quiet answer: %v", err)
	}
	drainOutbox(t, ctx, store, notif)
	if len(mail.sent) != 0 {
		t.Fatal("no email may be sent to an address that is not a customer")
	}
	if err := svc.RequestLogin(ctx, slug, "198.51.100.7", "marta@example.org", "https://shop.example"); err != nil {
		t.Fatal(err)
	}
	drainOutbox(t, ctx, store, notif)
	if len(mail.sent) != 1 || mail.sent[0].To != "marta@example.org" {
		t.Fatalf("one sign-in email expected: %+v", mail.sent)
	}
	body := mail.sent[0].Body
	i := strings.Index(body, "enter?token=")
	tok := body[i+len("enter?token="):]
	tok = tok[:strings.Index(tok, `"`)]
	sess, _, err := svc.Enter(ctx, slug, tok)
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	if _, _, err := svc.Enter(ctx, slug, tok); !errors.Is(err, shop.ErrNotFound) {
		t.Fatalf("a sign-in link works once: %v", err)
	}
	if _, err := svc.Portal(ctx, slug, "forged"); err == nil {
		t.Fatal("a forged session is refused")
	}
	p, err := svc.Portal(ctx, slug, sess)
	if err != nil || p.Customer != "Marta Gil" || len(p.Documents) != 0 {
		t.Fatalf("no finalized documents yet: %+v %v", p, err)
	}
	// Finalize the order and an unrelated customer's invoice: only the customer's own document appears.
	if _, err := salesSvc.FinalizeDocument(ctx, fx.Principal, *o.DocumentID); err != nil {
		t.Fatal(err)
	}
	foreign := finalizedSale(t, ctx, salesSvc, fx, "1", "10")
	p, _ = svc.Portal(ctx, slug, sess)
	if len(p.Documents) != 1 || p.Documents[0].ID != *o.DocumentID || p.Documents[0].Type != "SALES_ORDER" {
		t.Fatalf("the customer sees their own order: %+v", p.Documents)
	}
	if data, _, _, err := svc.Document(ctx, slug, sess, *o.DocumentID); err != nil || !strings.Contains(string(data), "v1") {
		t.Fatalf("own document renders: %v", err)
	}
	if _, _, _, err := svc.Document(ctx, slug, sess, foreign.ID); !errors.Is(err, shop.ErrNotFound) {
		t.Fatalf("another customer's document must not be reachable: %v", err)
	}
	link, err := svc.SignLink(ctx, slug, sess, "198.51.100.7", *o.DocumentID, "https://shop.example")
	if err != nil || !strings.HasPrefix(link, "https://shop.example/sign/") {
		t.Fatalf("sign link: %q %v", link, err)
	}
	if _, err := svc.SignLink(ctx, slug, sess, "198.51.100.7", foreign.ID, "https://shop.example"); !errors.Is(err, shop.ErrNotFound) {
		t.Fatalf("cannot start a signature on someone else's document: %v", err)
	}
	// A session for one shop does not open another.
	other := spainFixture(t, ctx, acct)
	slug2 := "other-" + strings.ToLower(uuid.NewString()[:8])
	if err := svc.SaveSettings(ctx, other.Principal, shop.Settings{Enabled: true, Slug: slug2, Title: "Other", LegalEntityID: other.LegalEntityID, BranchID: other.BranchID, WarehouseID: other.WarehouseID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Portal(ctx, slug2, sess); err == nil {
		t.Fatal("a session must not work across shops")
	}
	if err := svc.SaveSettings(ctx, other.Principal, shop.Settings{Enabled: true, Slug: slug, Title: "Clash", LegalEntityID: other.LegalEntityID, BranchID: other.BranchID, WarehouseID: other.WarehouseID}); !errors.Is(err, shop.ErrInvalid) {
		t.Fatalf("addresses are unique: %v", err)
	}
	if err := svc.SetOrderStatus(ctx, fx.Principal, o.ID, "CONFIRMED"); err != nil {
		t.Fatal(err)
	}
}
