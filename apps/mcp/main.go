// Command mcp is apps/mcp: the composition root for the MCP server
// (docs/architecture.md §11, brief §39-40). A genuinely separate process
// from apps/server and apps/worker — it never touches the HTTP request
// path or the outbox poller, and it has NO direct database access to any
// table an application-layer service doesn't already expose a method for
// (internal/modules/ai/tools.go's package doc explains exactly how that's
// enforced structurally, not just by convention).
//
// Authentication model: unlike apps/server (which authenticates each HTTP
// request independently, session or API key), this process resolves ONE
// API key — read from MCP_API_KEY — ONCE at startup, and holds that
// single permissions.Principal (plus its scopes) for the process
// lifetime. This is the natural shape for a locally-spawned MCP stdio
// server (an AI client starts one process per session, typically
// configured with one credential, not a multi-tenant server juggling
// many callers' credentials over one connection) — every tool call still
// goes through the exact same permissions.Checker.Require path a
// per-request REST credential would, just resolved once instead of once
// per call.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	accountingapp "rechvix/internal/modules/accounting/app"
	accountingpg "rechvix/internal/modules/accounting/pg"
	"rechvix/internal/modules/ai"
	catalogueapp "rechvix/internal/modules/catalogue/app"
	cataloguepg "rechvix/internal/modules/catalogue/pg"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/gstindia"
	gstindiapg "rechvix/internal/modules/gstindia/pg"
	identityapp "rechvix/internal/modules/identity/app"
	identitypg "rechvix/internal/modules/identity/pg"
	inventoryapp "rechvix/internal/modules/inventory/app"
	inventorypg "rechvix/internal/modules/inventory/pg"
	orgapp "rechvix/internal/modules/organisation/app"
	orgpg "rechvix/internal/modules/organisation/pg"
	pricingapp "rechvix/internal/modules/pricing/app"
	pricingpg "rechvix/internal/modules/pricing/pg"
	reportingapp "rechvix/internal/modules/reporting/app"
	reportingpg "rechvix/internal/modules/reporting/pg"
	salesapp "rechvix/internal/modules/sales/app"
	salespg "rechvix/internal/modules/sales/pg"
	taxationapp "rechvix/internal/modules/taxation/app"
	taxationpg "rechvix/internal/modules/taxation/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/config"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/logging"
	"rechvix/internal/platform/numbering"
	"rechvix/internal/platform/outbox"
	"rechvix/internal/platform/permissions"
)

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
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

	rawKey := os.Getenv("MCP_API_KEY")
	if rawKey == "" {
		return fmt.Errorf("apps/mcp: MCP_API_KEY is required — create one via POST /api/v1/api-keys with only the read scopes this MCP client should have")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, database.Config{DSN: cfg.Database.DSN, MaxConns: cfg.Database.MaxConns})
	if err != nil {
		return err
	}
	defer pool.Close()

	auditRecorder := audit.NewPGRecorder(pool)
	permissionsChecker := permissions.NewChecker(permissions.NewPGStore(pool), pool)

	// identitySvc is used ONLY to resolve MCP_API_KEY at startup — no
	// other identity method (login, sessions, MFA, ...) is reachable from
	// this process, and nothing here mounts an HTTP surface at all.
	hasher, err := appcrypto.NewPasswordHasher(appcrypto.PasswordParams{
		MemoryKiB: cfg.Argon2.MemoryKiB, Iterations: cfg.Argon2.Iterations, Parallelism: cfg.Argon2.Parallelism,
		SaltLength: cfg.Argon2.SaltLength, KeyLength: cfg.Argon2.KeyLength,
	})
	if err != nil {
		return err
	}
	aead, err := appcrypto.NewAEAD(make([]byte, 32)) // never used by ValidateAPIKey; a real key isn't needed for this process
	if err != nil {
		return err
	}
	orgSvc := orgapp.NewService(pool, orgpg.NewOrganisationRepo(pool), orgpg.NewLegalEntityRepo(pool),
		orgpg.NewBranchRepo(pool), orgpg.NewWarehouseRepo(pool), permissionsChecker, auditRecorder)
	identitySvc := identityapp.NewService(pool, identitypg.NewUserRepo(pool), identitypg.NewSessionRepo(pool),
		identitypg.NewPasswordResetRepo(pool), identitypg.NewMFARepo(pool), identitypg.NewRoleRepo(pool),
		identitypg.NewAPIKeyRepo(pool), permissionsChecker, orgSvc, hasher, aead, auditRecorder, identityapp.SessionPolicy{})

	principal, scopes, err := identitySvc.ValidateAPIKey(ctx, rawKey, "")
	if err != nil {
		return fmt.Errorf("apps/mcp: MCP_API_KEY is invalid, expired, or revoked: %w", err)
	}
	scopeStrings := make([]string, len(scopes))
	for i, s := range scopes {
		scopeStrings[i] = string(s)
	}
	scopeCodes := permissions.PermissionsForScopes(scopeStrings)
	logger.Info("mcp server authenticated", "organisation_id", principal.OrganisationID, "scopes", scopeStrings)

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
	accountingSvc := accountingapp.NewService(pool, accountingpg.NewAccountRepo(pool), accountingpg.NewJournalRepo(pool),
		accountingpg.NewJournalLineRepo(pool), accountingpg.NewFiscalPeriodRepo(pool), accountingpg.NewBankAccountRepo(pool),
		accountingpg.NewReceiptRepo(pool), accountingpg.NewPaymentRepo(pool), accountingpg.NewReconciliationRepo(pool),
		accountingpg.NewExpenseAttachmentRepo(pool),
		permissionsChecker, auditRecorder)
	reportingSvc := reportingapp.NewService(pool, reportingpg.NewRepo(pool), accountingSvc, permissionsChecker)

	gstRateRepo := gstindiapg.NewTaxRateRepo(pool)
	gstEngine := gstindia.NewEngine(gstRateRepo, gstindiapg.NewStateRepo(pool))
	taxationSvc := taxationapp.NewService(pool, gstEngine, taxationpg.NewTaxDocumentRepo(pool),
		taxationpg.NewTaxLineRepo(pool), taxationpg.NewTaxComponentRepo(pool))
	numberingSvc := numbering.NewService(pool, numbering.NewPGRepository(pool))
	outboxStore := outbox.NewPGStore(pool)
	salesSvc := salesapp.NewService(pool, salespg.NewDocumentRepo(pool), salespg.NewDocumentLineRepo(pool),
		inventorySvc, taxationSvc, catalogueSvc, contactsSvc, orgSvc, pricingSvc, numberingSvc, accountingSvc, outboxStore,
		permissionsChecker, auditRecorder)

	toolset := ai.NewToolset(principal, scopeCodes, catalogueSvc, contactsSvc, inventorySvc, salesSvc, reportingSvc, auditRecorder)

	server := mcp.NewServer(&mcp.Implementation{Name: "rechvix", Version: "0.1.0"}, nil)
	toolset.Register(server)

	logger.Info("mcp server starting on stdio")
	return server.Run(ctx, &mcp.StdioTransport{})
}
