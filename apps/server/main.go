// Command server is apps/server: the composition root for the billing
// platform's HTTP API. It wires concrete repositories and adapters into
// application services and mounts HTTP handlers — it contains no business
// logic itself (docs/architecture.md §2).
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountinghttp "rechvix/internal/modules/accounting/httpapi"
	accountingpg "rechvix/internal/modules/accounting/pg"
	catalogueapp "rechvix/internal/modules/catalogue/app"
	cataloguehttp "rechvix/internal/modules/catalogue/httpapi"
	cataloguepg "rechvix/internal/modules/catalogue/pg"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactshttp "rechvix/internal/modules/contacts/httpapi"
	contactspg "rechvix/internal/modules/contacts/pg"
	einvoicemock "rechvix/internal/modules/einvoice/v1/mock"
	ewaybillapp "rechvix/internal/modules/ewaybill/app"
	"rechvix/internal/modules/ewaybill/eligibility"
	"rechvix/internal/modules/ewaybill/govportal"
	ewaybillhttp "rechvix/internal/modules/ewaybill/httpapi"
	ewaybillpg "rechvix/internal/modules/ewaybill/pg"
	portalv1 "rechvix/internal/modules/ewaybill/portal/v1"
	"rechvix/internal/modules/gstindia"
	gstindiaapp "rechvix/internal/modules/gstindia/app"
	gstindiahttp "rechvix/internal/modules/gstindia/httpapi"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	identityapp "rechvix/internal/modules/identity/app"
	identityhttp "rechvix/internal/modules/identity/httpapi"
	identitypg "rechvix/internal/modules/identity/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	inventoryhttp "rechvix/internal/modules/inventory/httpapi"
	inventorypg "rechvix/internal/modules/inventory/pg"
	logisticsapp "rechvix/internal/modules/logistics/app"
	logisticshttp "rechvix/internal/modules/logistics/httpapi"
	logisticspg "rechvix/internal/modules/logistics/pg"
	notificationsapp "rechvix/internal/modules/notifications/app"
	notificationshttp "rechvix/internal/modules/notifications/httpapi"
	notificationspg "rechvix/internal/modules/notifications/pg"
	orgapp "rechvix/internal/modules/organisation/app"
	orghttp "rechvix/internal/modules/organisation/httpapi"
	orgpg "rechvix/internal/modules/organisation/pg"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricinghttp "rechvix/internal/modules/pricing/httpapi"
	pricingpg "rechvix/internal/modules/pricing/pg"
	purchasesapp "rechvix/internal/modules/purchases/app"
	purchaseshttp "rechvix/internal/modules/purchases/httpapi"
	purchasespg "rechvix/internal/modules/purchases/pg"
	reportingapp "rechvix/internal/modules/reporting/app"
	reportinghttp "rechvix/internal/modules/reporting/httpapi"
	reportingpg "rechvix/internal/modules/reporting/pg"
	salesapp "rechvix/internal/modules/sales/app"
	saleshttp "rechvix/internal/modules/sales/httpapi"
	salespg "rechvix/internal/modules/sales/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxationpg "rechvix/internal/modules/taxation/pg"
	webhooksapp "rechvix/internal/modules/webhooks/app"
	webhookshttp "rechvix/internal/modules/webhooks/httpapi"
	webhookspg "rechvix/internal/modules/webhooks/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/config"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/logging"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/observability"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
	"rechvix/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// -migrate: apply pending migrations, then exit — no HTTP server, no
	// other setup. Exists so a self-hosted deployment can run migrations
	// once as the schema-owning role and then run apps/server/apps/worker
	// as a separate, non-owning runtime role (docs/architecture.md §10;
	// see the DEPLOYMENT REQUIREMENT comment in
	// migrations/0001_organisation_hierarchy.up.sql and
	// database.WarnIfRuntimeRoleOwnsTenantTables below) — connecting as
	// the table owner for ordinary request traffic silently bypasses every
	// RLS policy, which defeats the tenant-isolation defense-in-depth this
	// whole schema is built around. DATABASE_AUTO_MIGRATE stays a
	// separate, independent setting for deployments that don't need this
	// split (e.g. local development).
	migrateOnly := flag.Bool("migrate", false, "apply pending database migrations, then exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.NewDefault(cfg.Logging.Level)
	slog.SetDefault(logger)

	if *migrateOnly {
		logger.Info("applying database migrations (-migrate)")
		return database.Migrate(cfg.Database.DSN, migrations.FS)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := observability.Setup(ctx, observability.Config{
		ServiceName:  cfg.Observability.ServiceName,
		OTLPEndpoint: cfg.Observability.OTLPEndpoint,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutdownCtx)
	}()

	pool, err := database.NewPool(ctx, database.Config{DSN: cfg.Database.DSN, MaxConns: cfg.Database.MaxConns})
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.Database.AutoMigrate {
		logger.Info("applying database migrations")
		if err := database.Migrate(cfg.Database.DSN, migrations.FS); err != nil {
			return err
		}
	}
	if err := pool.WarnIfRuntimeRoleOwnsTenantTables(ctx, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Warn("startup RLS-ownership check failed", "error", err)
	}

	hasher, err := appcrypto.NewPasswordHasher(appcrypto.PasswordParams{
		MemoryKiB:   cfg.Argon2.MemoryKiB,
		Iterations:  cfg.Argon2.Iterations,
		Parallelism: cfg.Argon2.Parallelism,
		SaltLength:  cfg.Argon2.SaltLength,
		KeyLength:   cfg.Argon2.KeyLength,
	})
	if err != nil {
		return err
	}

	aeadKey, err := loadOrGenerateAEADKey(logger)
	if err != nil {
		return err
	}
	aead, err := appcrypto.NewAEAD(aeadKey)
	if err != nil {
		return err
	}

	auditRecorder := audit.NewPGRecorder(pool)
	permissionsChecker := permissions.NewChecker(permissions.NewPGStore(pool), pool)

	orgSvc := orgapp.NewService(
		pool,
		orgpg.NewOrganisationRepo(pool),
		orgpg.NewLegalEntityRepo(pool),
		orgpg.NewBranchRepo(pool),
		orgpg.NewWarehouseRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	catalogueSvc := catalogueapp.NewService(
		pool,
		cataloguepg.NewUnitOfMeasureRepo(pool),
		cataloguepg.NewUnitConversionRepo(pool),
		cataloguepg.NewCategoryRepo(pool),
		cataloguepg.NewBrandRepo(pool),
		cataloguepg.NewProductRepo(pool),
		cataloguepg.NewProductVariantRepo(pool),
		cataloguepg.NewBarcodeRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	contactsSvc := contactsapp.NewService(
		pool,
		contactspg.NewPartyRepo(pool),
		contactspg.NewAddressRepo(pool),
		contactspg.NewTaxRegistrationRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	pricingSvc := pricingapp.NewService(
		pool,
		pricingpg.NewPriceListRepo(pool),
		pricingpg.NewPriceListItemRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	inventorySvc := inventoryapp.NewService(
		pool,
		inventorypg.NewStockMovementRepo(pool),
		inventorypg.NewStockBalanceRepo(pool),
		inventorypg.NewStockReservationRepo(pool),
		inventorypg.NewStockBatchRepo(pool),
		inventorypg.NewSerialNumberRepo(pool),
		inventorypg.NewStockPolicyRepo(pool),
		inventorypg.NewStockTransferRepo(pool),
		inventorypg.NewStockAdjustmentRepo(pool),
		cataloguepg.NewProductVariantRepo(pool),
		cataloguepg.NewProductRepo(pool),
		cataloguepg.NewUnitConversionRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	accountingSvc := accountingapp.NewService(
		pool,
		accountingpg.NewAccountRepo(pool),
		accountingpg.NewJournalRepo(pool),
		accountingpg.NewJournalLineRepo(pool),
		accountingpg.NewFiscalPeriodRepo(pool),
		accountingpg.NewBankAccountRepo(pool),
		accountingpg.NewReceiptRepo(pool),
		accountingpg.NewPaymentRepo(pool),
		accountingpg.NewReconciliationRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	reportingSvc := reportingapp.NewService(pool, reportingpg.NewRepo(pool), accountingSvc, permissionsChecker)

	purchasesSvc := purchasesapp.NewService(
		pool,
		purchasespg.NewDocumentRepo(pool),
		purchasespg.NewDocumentLineRepo(pool),
		inventorySvc,
		accountingSvc,
		permissionsChecker,
		auditRecorder,
	)

	gstRateRepo := gstindiapg.NewTaxRateRepo(pool)
	gstindiaSvc := gstindiaapp.NewService(pool, gstRateRepo, gstindiapg.NewStateRepo(pool), permissionsChecker, auditRecorder)
	// gstindia.Engine is the TaxEngine implementation taxationSvc drives —
	// taxation has no HTTP surface of its own (it's a cross-module
	// library, not an end-user-facing API — docs/architecture.md §5), so
	// only gstindia's admin rate-configuration API is mounted below;
	// sales.FinalizeDocument is taxationSvc's real caller (Stage 5b).
	gstEngine := gstindia.NewEngine(gstRateRepo, gstindiapg.NewStateRepo(pool))
	taxationSvc := taxationapp.NewService(
		pool, gstEngine,
		taxationpg.NewTaxDocumentRepo(pool),
		taxationpg.NewTaxLineRepo(pool),
		taxationpg.NewTaxComponentRepo(pool),
	)

	numberingSvc := numbering.NewService(pool, numbering.NewPGRepository(pool))
	outboxStore := outbox.NewPGStore(pool)

	salesSvc := salesapp.NewService(
		pool,
		salespg.NewDocumentRepo(pool),
		salespg.NewDocumentLineRepo(pool),
		inventorySvc,
		taxationSvc,
		catalogueSvc,
		contactsSvc,
		orgSvc,
		pricingSvc,
		numberingSvc,
		accountingSvc,
		outboxStore,
		permissionsChecker,
		auditRecorder,
	)

	logisticsSvc := logisticsapp.NewService(
		pool,
		logisticspg.NewVehicleRepo(pool),
		logisticspg.NewTransporterRepo(pool),
		logisticspg.NewPreferenceRepo(pool),
		permissionsChecker,
		auditRecorder,
	)

	// ewaybillSvc's AUTOMATIC_API path (einvoicemock.New()) is wired but not
	// exposed via any httpapi route in this pass — only the FREE_PORTAL
	// flow (docs/architecture.md §9b, this codebase's default and only
	// currently reachable production mode) is mounted below. A real
	// EWayBillProvider swap-in for AUTOMATIC_API is unchanged future work,
	// same as einvoice's own sandbox/real-GSP swap (docs/research.md).
	ewaybillSvc := ewaybillapp.NewService(ewaybillpg.NewRecordRepo(pool), einvoicemock.New(), salesSvc).
		WithFreePortal(orgSvc, contactsSvc, taxationSvc, eligibility.NewPGRepository(pool), portalv1.New(), auditRecorder)
	govPortalSvc := govportal.NewService()

	identitySvc := identityapp.NewService(
		pool,
		identitypg.NewUserRepo(pool),
		identitypg.NewSessionRepo(pool),
		identitypg.NewPasswordResetRepo(pool),
		identitypg.NewMFARepo(pool),
		identitypg.NewRoleRepo(pool),
		identitypg.NewAPIKeyRepo(pool),
		permissionsChecker,
		orgSvc,
		hasher,
		aead,
		auditRecorder,
		identityapp.SessionPolicy{
			IdleTimeout:     cfg.Session.IdleTimeout,
			AbsoluteTimeout: cfg.Session.AbsoluteTimeout,
		},
	)

	webhooksSvc := webhooksapp.NewService(
		pool,
		webhookspg.NewEndpointRepo(pool),
		webhookspg.NewDeliveryLogRepo(pool),
		outboxStore,
		permissionsChecker,
		auditRecorder,
	)

	// No EmailProvider/SMSProvider/WhatsAppProvider is wired by default —
	// none has real credentials in a fresh self-hosted install (brief §20
	// explicitly forbids a WhatsApp Web-scraping stand-in). QueueSend
	// correctly returns a permanent per-channel failure until an operator
	// configures one; see internal/modules/notifications/app/service.go.
	notificationsSvc := notificationsapp.NewService(
		pool,
		notificationspg.NewShareLinkRepo(pool),
		outboxStore,
		permissionsChecker,
		auditRecorder,
		nil, nil, nil,
	)

	router := httpx.NewRouter(httpx.RouterConfig{AllowedOrigins: cfg.Server.AllowedOrigins, Logger: logger})
	httpx.MountReady(router, pool)

	bootstrapEnabled := os.Getenv("ENABLE_BOOTSTRAP") == "true"
	if bootstrapEnabled {
		// Auto-close bootstrap once an organisation already exists, on top
		// of the env-var gate above — belt and suspenders against an
		// operator forgetting to flip ENABLE_BOOTSTRAP off post-setup,
		// since that endpoint has no permission check by design. Fails
		// open (leaves bootstrapEnabled true) on a check error rather than
		// blocking a genuine fresh install over a transient DB hiccup at
		// startup — identical risk to today's env-var-only behavior, never
		// worse.
		if exists, err := orgSvc.Exists(ctx); err != nil {
			logger.Warn("could not check whether an organisation already exists; leaving bootstrap enabled per ENABLE_BOOTSTRAP", "error", err)
		} else if exists {
			bootstrapEnabled = false
		}
	}
	if bootstrapEnabled {
		logger.Warn("POST /api/v1/auth/bootstrap is enabled — this endpoint creates a new organisation with " +
			"no permission check by design (see identity/app.Service.Bootstrap). Disable ENABLE_BOOTSTRAP once " +
			"initial setup is done.")
	}

	router.Route("/api/v1", func(r chi.Router) {
		identityHandlers := identityhttp.NewHandlers(identitySvc, cfg.Session.CookieName, cfg.Session.Secure).
			WithPostBootstrapHook(func(ctx context.Context, orgID, actorUserID uuid.UUID) error {
				// The layering-safe wiring docs/adr/0003-accounting-
				// integration-point.md's point 6 describes: identity can't
				// import accounting directly, but this composition root can.
				// The bootstrap Owner role holds every permission (Stage 2),
				// so this principal is authorized the same way a real owner
				// calling POST /accounting/accounts/ensure-default-chart
				// themselves would be.
				owner := permissions.Principal{UserID: actorUserID, OrganisationID: orgID}
				return accountingSvc.EnsureDefaultChartOfAccounts(ctx, owner, orgID)
			})
		identityHandlers.Mount(r, bootstrapEnabled)
		// Share-link redemption is deliberately UNAUTHENTICATED (brief
		// §21 — the whole point is a recipient with no session/API key);
		// it does NOT go in the RequireAuthOrAPIKey group below.
		notificationshttp.NewHandlers(notificationsSvc).MountPublic(r)

		r.Group(func(r chi.Router) {
			r.Use(identityhttp.RequireAuthOrAPIKey(identitySvc, cfg.Session.CookieName))
			identityHandlers.MountAPIKeys(r)
			orghttp.NewHandlers(orgSvc).Mount(r)
			cataloguehttp.NewHandlers(catalogueSvc).Mount(r)
			contactshttp.NewHandlers(contactsSvc).Mount(r)
			pricinghttp.NewHandlers(pricingSvc).Mount(r)
			inventoryhttp.NewHandlers(inventorySvc).Mount(r)
			purchaseshttp.NewHandlers(purchasesSvc).Mount(r)
			gstindiahttp.NewHandlers(gstindiaSvc).Mount(r)
			saleshttp.NewHandlers(salesSvc).WithEWayBill(pool, ewaybillSvc).Mount(r)
			accountinghttp.NewHandlers(accountingSvc).Mount(r)
			reportinghttp.NewHandlers(reportingSvc).Mount(r)
			webhookshttp.NewHandlers(webhooksSvc).Mount(r)
			notificationshttp.NewHandlers(notificationsSvc).Mount(r)
			logisticshttp.NewHandlers(logisticsSvc).Mount(r)
			ewaybillhttp.NewHandlers(ewaybillSvc, pool, permissionsChecker, govPortalSvc).Mount(r)
		})
	})

	// Serves apps/web's built SPA (if present — see WebDistDir's doc
	// comment) for everything that isn't an /api/* or /health/* route
	// registered above. Must come after every r.Route/r.Get above: chi's
	// NotFound handler is whatever was last set, and this is deliberately
	// the catch-all.
	httpx.MountSPA(router, cfg.Server.WebDistDir)

	server := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Server.HTTPPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "port", cfg.Server.HTTPPort)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serveErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// loadOrGenerateAEADKey reads a base64-encoded 32-byte key from
// AEAD_ENCRYPTION_KEY. In production this must be set from secrets
// management (brief §60) — generating an ephemeral key is only
// acceptable for local development, where losing already-encrypted MFA
// secrets on restart is a non-issue, and this path logs loudly so it's
// never silently relied on in a real deployment.
func loadOrGenerateAEADKey(logger *slog.Logger) ([]byte, error) {
	if encoded := os.Getenv("AEAD_ENCRYPTION_KEY"); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("config: AEAD_ENCRYPTION_KEY is not valid base64")
		}
		if len(key) != 32 {
			return nil, errors.New("config: AEAD_ENCRYPTION_KEY must decode to exactly 32 bytes")
		}
		return key, nil
	}
	logger.Warn("AEAD_ENCRYPTION_KEY not set — generating an EPHEMERAL key for this process only. " +
		"Any MFA secret encrypted with it becomes unreadable on restart. Set AEAD_ENCRYPTION_KEY " +
		"(32 random bytes, base64-encoded) before running this in production.")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}
