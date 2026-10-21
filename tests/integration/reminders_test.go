//go:build integration

package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingdomain "rechvix/internal/modules/accounting/domain"
	notificationsapp "rechvix/internal/modules/notifications/app"
	notificationspg "rechvix/internal/modules/notifications/pg"
	"rechvix/internal/modules/reminders"
	salesapp "rechvix/internal/modules/sales/app"
	salesdomain "rechvix/internal/modules/sales/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

type captureMail struct {
	mu   sync.Mutex
	sent []struct{ To, Subject, Body string }
}

func (c *captureMail) SendEmail(_ context.Context, to, subject, body, _ string, _ []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, struct{ To, Subject, Body string }{to, subject, body})
	return nil
}

func newNotifications(t *testing.T, mail *captureMail) (*notificationsapp.Service, *outbox.PGStore) {
	t.Helper()
	store := outbox.NewPGStore(sharedPool)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	var svc *notificationsapp.Service
	if mail != nil {
		svc = notificationsapp.NewService(sharedPool, notificationspg.NewShareLinkRepo(sharedPool), store, checker, audit.NewPGRecorder(sharedPool), mail, nil, nil)
	} else {
		svc = notificationsapp.NewService(sharedPool, notificationspg.NewShareLinkRepo(sharedPool), store, checker, audit.NewPGRecorder(sharedPool), nil, nil, nil)
	}
	return svc, store
}

func drainOutbox(t *testing.T, ctx context.Context, store *outbox.PGStore, svc *notificationsapp.Service) {
	t.Helper()
	p := outbox.NewPoller(sharedPool, store, nil)
	p.Register(notificationsapp.EventTypeSend, svc.Handler())
	for i := 0; i < 50; i++ {
		ok, err := p.ProcessOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
	}
}

func TestShare_EmailDocumentSendsLinkAndRecordsReadReceipt(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	doc := finalizedSale(t, ctx, salesSvc, fx, "1", "100")
	mail := &captureMail{}
	svc, store := newNotifications(t, mail)

	if err := svc.EmailDocument(ctx, fx.Principal, "https://billing.example.com", "sales_document", doc.ID, "buyer@example.org", "Invoice "+doc.DocumentNumber, "Thanks <b>a lot</b>"); err != nil {
		t.Fatalf("EmailDocument: %v", err)
	}
	drainOutbox(t, ctx, store, svc)
	if len(mail.sent) != 1 || mail.sent[0].To != "buyer@example.org" {
		t.Fatalf("one email expected, got %+v", mail.sent)
	}
	body := mail.sent[0].Body
	if strings.Contains(body, "<b>") || !strings.Contains(body, "&lt;b&gt;") {
		t.Fatalf("the message must be HTML-escaped: %s", body)
	}
	i := strings.Index(body, "https://billing.example.com/api/v1/share/")
	if i < 0 {
		t.Fatalf("the email must link to the share URL: %s", body)
	}
	token := body[i+len("https://billing.example.com/api/v1/share/"):]
	token = token[:strings.Index(token, "/pdf")]

	link, err := svc.RedeemShareLink(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if link.ViewCount != 0 || link.FirstViewedAt != nil {
		t.Fatal("a link nobody opened has no views")
	}
	svc.RecordView(ctx, link)
	svc.RecordView(ctx, link)
	links, err := svc.ListShareLinksForDocument(ctx, fx.Principal, "sales_document", doc.ID)
	if err != nil || len(links) != 1 || links[0].ViewCount != 2 || links[0].FirstViewedAt == nil {
		t.Fatalf("read receipt not recorded: %+v %v", links, err)
	}
}

func TestShare_EmailRefusedWhenNoMailTransport(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	doc := finalizedSale(t, ctx, salesSvc, fx, "1", "100")
	svc, _ := newNotifications(t, nil)
	if err := svc.EmailDocument(ctx, fx.Principal, "https://x", "sales_document", doc.ID, "a@b.co", "s", "m"); err == nil {
		t.Fatal("email without a configured transport must be refused up front")
	}
}

func finalizedSale(t *testing.T, ctx context.Context, svc *salesapp.Service, fx accountingFixture, qty, price string) *salesdomain.Document {
	t.Helper()
	d, err := svc.CreateDocument(ctx, fx.Principal, salesapp.CreateDocumentParams{
		LegalEntityID: fx.LegalEntityID, BranchID: fx.BranchID, WarehouseID: fx.WarehouseID, CustomerPartyID: fx.CustomerID,
		DocumentType: salesdomain.DocTaxInvoice, CurrencyCode: "EUR", BaseCurrencyCode: "EUR", ExchangeRate: mustDecimal(t, "1"),
		IssueDate: time.Now().AddDate(0, 0, -30),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddLine(ctx, fx.Principal, salesapp.AddLineParams{DocumentID: d.ID, ProductVariantID: fx.VariantID, UnitID: fx.PCS, Quantity: mustDecimal(t, qty), UnitPrice: mustDecimal(t, price)}); err != nil {
		t.Fatal(err)
	}
	out, err := svc.FinalizeDocument(ctx, fx.Principal, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReminders_OverdueInvoicesAreRemindedOnceThenRespectRepeatAndPayment(t *testing.T) {
	ctx := context.Background()
	t.Setenv("PUBLIC_BASE_URL", "https://billing.example.com")
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	// the customer needs an email address
	if err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE parties SET email = 'customer@example.org' WHERE id = $1`, fx.CustomerID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	doc := finalizedSale(t, ctx, salesSvc, fx, "2", "100")
	mail := &captureMail{}
	notif, store := newNotifications(t, mail)
	rem := reminders.NewService(sharedPool, notif, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool), nil)

	if err := rem.Save(ctx, fx.Principal, reminders.Settings{Enabled: true, DaysAfterDue: 3, RepeatEveryDays: 7, MaxReminders: 2, Subject: "Reminder {{number}}", Message: "Pay {{amount}} due {{due_date}}"}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	n, err := rem.RunOnce(ctx, fx.Principal.OrganisationID)
	if err != nil || n != 1 {
		t.Fatalf("first run must queue one reminder: %d %v", n, err)
	}
	drainOutbox(t, ctx, store, notif)
	if len(mail.sent) != 1 || !strings.Contains(mail.sent[0].Subject, doc.DocumentNumber) || !strings.Contains(mail.sent[0].Body, "Pay ") {
		t.Fatalf("reminder email wrong: %+v", mail.sent)
	}
	if n, _ := rem.RunOnce(ctx, fx.Principal.OrganisationID); n != 0 {
		t.Fatalf("a second run inside the repeat window must not remind again, queued %d", n)
	}
	log, err := rem.Log(ctx, fx.Principal)
	if err != nil || len(log) != 1 || log[0].ReminderNumber != 1 {
		t.Fatalf("log: %+v %v", log, err)
	}

	// Paying the invoice in full stops reminders even after the repeat window passes.
	if _, err := acct.RecordReceipt(ctx, fx.Principal, accountingapp.RecordReceiptParams{PartyID: fx.CustomerID, SalesDocumentID: &doc.ID,
		Amount: mustDecimal(t, doc.GrandTotalAmount.Decimal().String()), Method: accountingdomain.MethodCash}); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if err := sharedPool.RunScoped(ctx, fx.Principal.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `UPDATE reminder_log SET queued_at = now() - interval '10 days' WHERE sales_document_id = $1`, doc.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := rem.RunOnce(ctx, fx.Principal.OrganisationID); n != 0 {
		t.Fatalf("a paid invoice must not be reminded, queued %d", n)
	}
}

func TestReminders_CannotBeEnabledWithoutEmail(t *testing.T) {
	ctx := context.Background()
	salesSvc, _, acct, _ := newTestAccountingServices(t)
	_ = salesSvc
	fx := spainFixture(t, ctx, acct)
	notif, _ := newNotifications(t, nil)
	rem := reminders.NewService(sharedPool, notif, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool), nil)
	err := rem.Save(ctx, fx.Principal, reminders.Settings{Enabled: true, DaysAfterDue: 3, RepeatEveryDays: 7, MaxReminders: 3, Subject: "s", Message: "m"})
	if err == nil {
		t.Fatal("enabling reminders with no mail transport must be refused")
	}
	_ = uuid.Nil
}

// spainFixture is a Spanish company in euros with the chart of accounts, so invoices finalize without GST state data.
func spainFixture(t *testing.T, ctx context.Context, acct *accountingapp.Service) accountingFixture {
	t.Helper()
	fx := setupRegimeFixture(t, ctx, "ES", "EUR", "STANDARD")
	if err := acct.EnsureDefaultChartOfAccounts(ctx, fx.Principal, fx.Principal.OrganisationID); err != nil {
		t.Fatal(err)
	}
	return fx
}
