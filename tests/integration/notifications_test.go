//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	notificationsapp "rechvix/internal/modules/notifications/app"
	notificationsdomain "rechvix/internal/modules/notifications/domain"
	notificationspg "rechvix/internal/modules/notifications/pg"
	mockprovider "rechvix/internal/modules/notifications/v1/mock"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

// newTestNotificationsService returns the service plus a handlers map for
// tests/integration/outbox_helpers_test.go's processNextForOrg —
// organisation-scoped claiming, not outbox.Poller's cross-organisation
// claim (see that file's doc comment for why this matters for tests).
func newTestNotificationsService(t *testing.T, email *mockprovider.Provider) (*notificationsapp.Service, *outbox.PGStore, map[string]outbox.Handler) {
	t.Helper()
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)
	outboxStore := outbox.NewPGStore(sharedPool)
	var emailProvider notificationsdomain.EmailProvider
	if email != nil {
		emailProvider = email
	}
	svc := notificationsapp.NewService(sharedPool, notificationspg.NewShareLinkRepo(sharedPool), outboxStore,
		checker, recorder, emailProvider, nil, nil)
	handlers := map[string]outbox.Handler{notificationsapp.EventTypeSend: svc.Handler()}
	return svc, outboxStore, handlers
}

func TestNotifications_ShareLink_CreateRedeemRevoke(t *testing.T) {
	ctx := context.Background()
	identitySvc, _ := newTestIdentityService(t)
	boot := bootstrapTestTenant(t, ctx, identitySvc, "share-"+uuid.NewString()[:8]+"@example.com", "correct horse battery staple 42")
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}

	svc, _, _ := newTestNotificationsService(t, nil)
	docID := uuid.Must(uuid.NewV7())

	token, err := svc.CreateShareLink(ctx, principal, "sales_document", docID)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty share token")
	}

	// Redemption is UNAUTHENTICATED by design (brief §21) — no Principal
	// passed here at all, matching notifications/httpapi's public redeem
	// route.
	got, err := svc.RedeemShareLink(ctx, token)
	if err != nil {
		t.Fatalf("RedeemShareLink: %v", err)
	}
	if got.DocumentType != "sales_document" || got.DocumentID != docID {
		t.Fatalf("RedeemShareLink = (%s, %s), want (sales_document, %s)", got.DocumentType, got.DocumentID, docID)
	}
	if got.OrganisationID != principal.OrganisationID || got.CreatedBy != principal.UserID {
		t.Fatalf("RedeemShareLink OrganisationID/CreatedBy = (%s, %s), want (%s, %s) — httpapi.DocumentRenderer's authorization depends on these", got.OrganisationID, got.CreatedBy, principal.OrganisationID, principal.UserID)
	}

	// An unguessable token: a garbage token must fail closed, not panic
	// or leak whether ANY link exists.
	if _, err := svc.RedeemShareLink(ctx, "not-a-real-token"); err == nil {
		t.Fatal("expected RedeemShareLink to reject a garbage token")
	}

	// Find the created link's ID for revocation — no ListShareLinks
	// exposed on Service yet, so this test queries directly.
	var linkID uuid.UUID
	if err := sharedPool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx,
			`SELECT id FROM share_links WHERE organisation_id = $1 AND document_id = $2`,
			principal.OrganisationID, docID).Scan(&linkID)
	}); err != nil {
		t.Fatalf("looking up share link id: %v", err)
	}

	if err := svc.RevokeShareLink(ctx, principal, linkID); err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}
	if _, err := svc.RedeemShareLink(ctx, token); err == nil {
		t.Fatal("expected RedeemShareLink to reject a revoked link")
	}
}

func TestNotifications_QueueSend_DeliversThroughOutboxToMockEmailProvider(t *testing.T) {
	ctx := context.Background()
	identitySvc, _ := newTestIdentityService(t)
	boot := bootstrapTestTenant(t, ctx, identitySvc, "notify-"+uuid.NewString()[:8]+"@example.com", "correct horse battery staple 42")
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}

	email := mockprovider.New()
	svc, _, handlers := newTestNotificationsService(t, email)

	docID := uuid.Must(uuid.NewV7())
	if err := svc.QueueSend(ctx, principal, notificationsapp.SendPayload{
		Channel: notificationsdomain.ChannelEmail, Recipient: "customer@example.com",
		DocumentType: "sales_document", DocumentID: docID, Subject: "Your invoice", BodyHTML: "<p>hi</p>",
	}); err != nil {
		t.Fatalf("QueueSend: %v", err)
	}

	// QueueSend must return immediately without having sent anything
	// inline (brief Rule 12) — nothing is delivered until the poller
	// actually processes the queued event.
	if len(email.Sent()) != 0 {
		t.Fatal("QueueSend delivered inline instead of going through the outbox")
	}

	if !processNextForOrg(t, ctx, principal.OrganisationID, handlers) {
		t.Fatal("expected an event to be claimable, found none")
	}

	sent := email.Sent()
	if len(sent) != 1 {
		t.Fatalf("mock provider received %d sends, want 1", len(sent))
	}
	if sent[0].Channel != "EMAIL" || sent[0].Recipient != "customer@example.com" {
		t.Fatalf("unexpected send record: %+v", sent[0])
	}
}

// TestNotifications_UnconfiguredProvider_FailsPermanentlyNotRetried proves
// a deployment with no email/SMS/WhatsApp provider configured (the
// default, per apps/server/main.go's comment) fails a queued send
// cleanly rather than retrying forever against a provider that will
// never exist.
func TestNotifications_UnconfiguredProvider_FailsPermanentlyNotRetried(t *testing.T) {
	ctx := context.Background()
	identitySvc, _ := newTestIdentityService(t)
	boot := bootstrapTestTenant(t, ctx, identitySvc, "notify-noprov-"+uuid.NewString()[:8]+"@example.com", "correct horse battery staple 42")
	principal := permissions.Principal{UserID: boot.OwnerUserID, OrganisationID: boot.OrganisationID}

	svc, _, handlers := newTestNotificationsService(t, nil) // no email provider configured

	if err := svc.QueueSend(ctx, principal, notificationsapp.SendPayload{
		Channel: notificationsdomain.ChannelEmail, Recipient: "x@example.com",
		DocumentType: "sales_document", DocumentID: uuid.Must(uuid.NewV7()),
	}); err != nil {
		t.Fatalf("QueueSend: %v", err)
	}
	if !processNextForOrg(t, ctx, principal.OrganisationID, handlers) {
		t.Fatal("expected an event to be claimable, found none")
	}

	var status string
	if err := sharedPool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		return sharedPool.Q(ctx).QueryRow(ctx,
			`SELECT status FROM outbox_events WHERE organisation_id = $1 AND event_type = $2 ORDER BY created_at DESC LIMIT 1`,
			principal.OrganisationID, notificationsapp.EventTypeSend).Scan(&status)
	}); err != nil {
		t.Fatalf("querying outbox_events: %v", err)
	}
	if status != "FAILED" {
		t.Fatalf("status = %s, want FAILED (permanent — no provider configured, retrying can't fix that)", status)
	}
}
