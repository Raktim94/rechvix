// Command worker is apps/worker: the composition root for background
// processing — currently the outbox poller driving e-Invoice generation
// (docs/architecture.md §9/§34). Genuinely a separate process from
// apps/server, never sharing its request path, so a government API outage
// or a slow retry loop here can never block or slow down an HTTP request
// (brief Rule 12, Scenario L).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingpg "rechvix/internal/modules/accounting/pg"
	catalogueapp "rechvix/internal/modules/catalogue/app"
	cataloguepg "rechvix/internal/modules/catalogue/pg"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	einvoiceapp "rechvix/internal/modules/einvoice/app"
	einvoicedomain "rechvix/internal/modules/einvoice/domain"
	einvoicepg "rechvix/internal/modules/einvoice/pg"
	mockprovider "rechvix/internal/modules/einvoice/v1/mock"
	sandboxprovider "rechvix/internal/modules/einvoice/v1/sandbox"
	"rechvix/internal/modules/gstindia"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	inventorypg "rechvix/internal/modules/inventory/pg"
	notificationsapp "rechvix/internal/modules/notifications/app"
	notificationspg "rechvix/internal/modules/notifications/pg"
	orgapp "rechvix/internal/modules/organisation/app"
	orgpg "rechvix/internal/modules/organisation/pg"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salespg "rechvix/internal/modules/sales/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxationpg "rechvix/internal/modules/taxation/pg"
	webhooksapp "rechvix/internal/modules/webhooks/app"
	webhookspg "rechvix/internal/modules/webhooks/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/config"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/logging"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.NewDefault(cfg.Logging.Level)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, database.Config{DSN: cfg.Database.DSN, MaxConns: cfg.Database.MaxConns})
	if err != nil {
		return err
	}
	defer pool.Close()

	auditRecorder := audit.NewPGRecorder(pool)
	permissionsChecker := permissions.NewChecker(permissions.NewPGStore(pool), pool)
	numberingSvc := numbering.NewService(pool, numbering.NewPGRepository(pool))
	outboxStore := outbox.NewPGStore(pool)

	orgSvc := orgapp.NewService(pool, orgpg.NewOrganisationRepo(pool), orgpg.NewLegalEntityRepo(pool),
		orgpg.NewBranchRepo(pool), orgpg.NewWarehouseRepo(pool), permissionsChecker, auditRecorder)
	catalogueSvc := catalogueapp.NewService(pool, cataloguepg.NewUnitOfMeasureRepo(pool), cataloguepg.NewUnitConversionRepo(pool),
		cataloguepg.NewCategoryRepo(pool), cataloguepg.NewBrandRepo(pool), cataloguepg.NewProductRepo(pool),
		cataloguepg.NewProductVariantRepo(pool), cataloguepg.NewBarcodeRepo(pool), permissionsChecker, auditRecorder)
	contactsSvc := contactsapp.NewService(pool, contactspg.NewPartyRepo(pool), contactspg.NewAddressRepo(pool),
		contactspg.NewTaxRegistrationRepo(pool), permissionsChecker, auditRecorder)
	pricingSvc := pricingapp.NewService(pool, pricingpg.NewPriceListRepo(pool), pricingpg.NewPriceListItemRepo(pool),
		permissionsChecker, auditRecorder)
	inventorySvc := inventoryapp.NewService(pool, inventorypg.NewStockMovementRepo(pool), inventorypg.NewStockBalanceRepo(pool),
		inventorypg.NewStockReservationRepo(pool), inventorypg.NewStockBatchRepo(pool), inventorypg.NewStockCostLotRepo(pool), inventorypg.NewSerialNumberRepo(pool),
		inventorypg.NewStockPolicyRepo(pool), inventorypg.NewStockTransferRepo(pool), inventorypg.NewStockAdjustmentRepo(pool),
		cataloguepg.NewProductVariantRepo(pool), cataloguepg.NewProductRepo(pool), cataloguepg.NewUnitConversionRepo(pool),
		permissionsChecker, auditRecorder)
	gstRateRepo := gstindiapg.NewTaxRateRepo(pool)
	gstEngine := gstindia.NewEngine(gstRateRepo, gstindiapg.NewStateRepo(pool))
	taxationSvc := taxationapp.NewService(pool, gstEngine, taxationpg.NewTaxDocumentRepo(pool), taxationpg.NewTaxLineRepo(pool),
		taxationpg.NewTaxComponentRepo(pool))
	accountingSvc := accountingapp.NewService(pool, accountingpg.NewAccountRepo(pool), accountingpg.NewJournalRepo(pool),
		accountingpg.NewJournalLineRepo(pool), accountingpg.NewFiscalPeriodRepo(pool), accountingpg.NewBankAccountRepo(pool),
		accountingpg.NewReceiptRepo(pool), accountingpg.NewPaymentRepo(pool), accountingpg.NewReconciliationRepo(pool),
		accountingpg.NewExpenseAttachmentRepo(pool),
		permissionsChecker, auditRecorder)
	salesSvc := salesapp.NewService(pool, salespg.NewDocumentRepo(pool), salespg.NewDocumentLineRepo(pool),
		inventorySvc, taxationSvc, catalogueSvc, contactsSvc, orgSvc, pricingSvc, numberingSvc, accountingSvc, outboxStore,
		permissionsChecker, auditRecorder)

	provider, providerName := buildEInvoiceProvider(logger)

	einvoiceSvc := einvoiceapp.NewService(einvoicepg.NewRecordRepo(pool), provider, providerName,
		salesSvc, taxationSvc, orgSvc, contactsSvc, outboxStore)

	webhooksSvc := webhooksapp.NewService(pool, webhookspg.NewEndpointRepo(pool), webhookspg.NewDeliveryLogRepo(pool),
		outboxStore, permissionsChecker, auditRecorder)
	// No real EmailProvider/SMSProvider/WhatsAppProvider configured here
	// either (apps/server/main.go's doc comment explains why) — this
	// worker still correctly drains queued sends into a documented
	// per-channel permanent failure rather than silently dropping them.
	notificationsSvc := notificationsapp.NewService(pool, notificationspg.NewShareLinkRepo(pool), outboxStore,
		permissionsChecker, auditRecorder, nil, nil, nil)

	poller := outbox.NewPoller(pool, outboxStore, logger)
	poller.Register(einvoiceapp.EventTypeGenerate, einvoiceSvc.Handler())
	// Webhook fan-out (docs/adr/0005): one handler per brief §38 source
	// event this codebase actually enqueues today (sales' "invoice.finalized"
	// and einvoice's "einvoice.generated"/"einvoice.failed" — see those
	// modules' Stage 9 additions). Registering a handler for an event type
	// nothing ever enqueues is harmless (it would simply never be
	// claimed), but only these three are wired end-to-end and verified;
	// invoice.created/cancelled, payment.*, stock.*, customer.created,
	// ewaybill.* remain unwired producer-side, documented in docs/TODO.md
	// rather than silently implied as done.
	poller.Register("invoice.finalized", webhooksSvc.HandlerForSourceEvent("invoice.finalized"))
	poller.Register("einvoice.generated", webhooksSvc.HandlerForSourceEvent("einvoice.generated"))
	poller.Register("einvoice.failed", webhooksSvc.HandlerForSourceEvent("einvoice.failed"))
	poller.Register(webhooksapp.EventTypeDelivery, webhooksSvc.DeliverHandler())
	poller.Register(notificationsapp.EventTypeSend, notificationsSvc.Handler())

	logger.Info("worker started", "einvoice_provider", providerName)
	poller.Run(ctx, 5*time.Second)
	logger.Info("worker shutting down")
	return nil
}

// buildEInvoiceProvider defaults to the mock provider — safe, no network
// calls, correct for any deployment that hasn't explicitly configured a
// real e-Invoice provider yet. Setting EINVOICE_PROVIDER=sandbox switches
// to the real NIC-sandbox-calling adapter (internal/modules/einvoice/v1/sandbox),
// reading credentials from EINVOICE_SANDBOX_* env vars.
//
// NOT wired to einvoice_provider_credentials (the encrypted-at-rest table
// migrations/0024 created): loading and decrypting per-legal-entity
// credentials from that table at startup, keyed to whichever legal entity
// a given outbox event's document belongs to, is real remaining work —
// flagged explicitly as a follow-up in the Stage 8 report rather than
// half-wired silently. Env vars are a legitimate, explicit interim path
// for a single-legal-entity self-hosted deployment (this project's
// primary target, per docs/research.md's AGPL/nodedr-pos-style
// distribution decision).
func buildEInvoiceProvider(logger *slog.Logger) (einvoicedomain.EInvoiceProvider, string) {
	if os.Getenv("EINVOICE_PROVIDER") != "sandbox" {
		logger.Info("using mock e-Invoice provider (set EINVOICE_PROVIDER=sandbox to use the real NIC sandbox adapter)")
		return mockprovider.New(), "mock"
	}
	creds := sandboxprovider.Credentials{
		ClientID:     os.Getenv("EINVOICE_SANDBOX_CLIENT_ID"),
		ClientSecret: os.Getenv("EINVOICE_SANDBOX_CLIENT_SECRET"),
		GSTIN:        os.Getenv("EINVOICE_SANDBOX_GSTIN"),
		Username:     os.Getenv("EINVOICE_SANDBOX_USERNAME"),
		Password:     os.Getenv("EINVOICE_SANDBOX_PASSWORD"),
	}
	baseURL := os.Getenv("EINVOICE_SANDBOX_BASE_URL")
	logger.Warn("using the REAL NIC sandbox e-Invoice provider — this makes actual network calls; " +
		"never enable this in an automated test run")
	return sandboxprovider.New(baseURL, creds, nil), "nic-sandbox-v1"
}
