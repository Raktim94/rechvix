//go:build integration

package integration

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"rechvix/internal/platform/database"
	"rechvix/migrations"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	catalogueapp "rechvix/internal/modules/catalogue/app"
	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	"rechvix/internal/modules/datasync"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

// centralServer fronts the sync endpoints for one organisation, standing in for the API-key middleware.
func centralServer(t *testing.T, svc *datasync.Service, central permissions.Principal) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), central)))
		})
	})
	r.Route("/api/v1", func(r chi.Router) { datasync.NewHandlers(svc).Mount(r) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// secondDatabase stands up a separate, fully migrated database: the central server and a desktop install never share
// one, and row ids are global primary keys, so sharing would hide exactly the bugs this test is for.
func secondDatabase(t *testing.T, ctx context.Context) *database.Pool {
	t.Helper()
	admin, err := database.NewPool(ctx, database.Config{DSN: sharedMigratorDSN, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "billing_central_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Pool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s OWNER billing_migrator`, name)); err != nil {
		t.Fatal(err)
	}
	dsn := strings.Replace(sharedMigratorDSN, "/billing_test", "/"+name, 1)
	if err := database.Migrate(dsn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	if err := provisionAppRole(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := database.NewPool(ctx, database.Config{DSN: strings.Replace(dsn, "billing_migrator:billing_migrator@", "billing_app:billing_app_pw@", 1), MaxConns: 5})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// onPool runs fn with the shared test pool pointed at p, so the existing fixture helpers build against it.
func onPool(p *database.Pool, fn func()) {
	saved := sharedPool
	sharedPool = p
	defer func() { sharedPool = saved }()
	fn()
}

func partyName(t *testing.T, ctx context.Context, pool *database.Pool, org uuid.UUID, id uuid.UUID) (string, bool) {
	t.Helper()
	var name string
	found := false
	err := pool.RunScoped(ctx, org, func(ctx context.Context) error {
		rows, err := pool.Q(ctx).Query(ctx, `SELECT legal_name FROM parties WHERE id = $1 AND organisation_id = $2`, id, org)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			found = true
			return rows.Scan(&name)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return name, found
}

func TestDataSync_TwoWayMasterData(t *testing.T) {
	ctx := context.Background()
	centralPool := secondDatabase(t, ctx)

	var central permissions.Principal
	var centralContacts *contactsapp.Service
	onPool(centralPool, func() {
		central = bootstrapOwnerPrincipal(t, ctx)
		centralContacts = newTestContactsService(t)
	})
	device := bootstrapOwnerPrincipal(t, ctx)
	contacts := newTestContactsService(t)
	catalogue := newTestCatalogueService(t)

	aead, err := crypto.NewAEAD(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mk := func(pool *database.Pool) *datasync.Service {
		return datasync.NewService(pool, permissions.NewChecker(permissions.NewPGStore(pool), pool), audit.NewPGRecorder(pool), aead, logger)
	}
	centralSvc, deviceSvc := mk(centralPool), mk(sharedPool)
	srv := centralServer(t, centralSvc, central)

	// A contact and a product (with the unit and variant it depends on) made on the device.
	alpha, err := contacts.CreateParty(ctx, device, contactsapp.CreatePartyParams{PartyType: contactsdomain.PartyCustomer, LegalName: "Alpha Traders", CurrencyCode: "INR"})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := catalogue.CreateUnitOfMeasure(ctx, device, catalogueapp.CreateUnitOfMeasureParams{Code: "KG", Name: "Kilogram"})
	if err != nil {
		t.Fatal(err)
	}
	prod, err := catalogue.CreateProduct(ctx, device, catalogueapp.CreateProductParams{BaseUOMID: unit.ID, Name: "Basmati rice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.CreateVariant(ctx, device, catalogueapp.CreateVariantParams{ProductID: prod.ID, SKUCode: "RICE-" + uuid.NewString()[:6]}); err != nil {
		t.Fatal(err)
	}

	if _, err := deviceSvc.Link(ctx, device, srv.URL, "test-key"); err != nil {
		t.Fatalf("Link: %v", err)
	}
	st, err := deviceSvc.SyncNow(ctx, device)
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if st.LastError != "" || st.LastConflict != 0 || st.Waiting != 0 || st.LastPushed != 4 {
		t.Fatalf("first sync should push all 4 rows cleanly, got %+v", st)
	}
	if name, ok := partyName(t, ctx, centralPool, central.OrganisationID, alpha.ID); !ok || name != "Alpha Traders" {
		t.Fatalf("contact did not reach the server: %q %v", name, ok)
	}
	var variants int
	if err := centralPool.RunScoped(ctx, central.OrganisationID, func(ctx context.Context) error {
		return centralPool.Q(ctx).QueryRow(ctx, `SELECT count(*) FROM product_variants WHERE organisation_id = $1`, central.OrganisationID).Scan(&variants)
	}); err != nil {
		t.Fatal(err)
	}
	if variants != 1 {
		t.Fatalf("the variant (child of product, child of unit) should have arrived once, got %d", variants)
	}

	// A contact made on the server shows up on the device, and applying it is not queued as a local change.
	var beta *contactsdomain.Party
	onPool(centralPool, func() {
		beta, err = centralContacts.CreateParty(ctx, central, contactsapp.CreatePartyParams{PartyType: contactsdomain.PartySupplier, LegalName: "Beta Supplies", CurrencyCode: "INR"})
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err = deviceSvc.SyncNow(ctx, device)
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := partyName(t, ctx, sharedPool, device.OrganisationID, beta.ID); !ok || name != "Beta Supplies" {
		t.Fatalf("server contact did not reach the device: %q %v", name, ok)
	}
	if st.Waiting != 0 {
		t.Fatalf("pulled rows must not be queued to push back, %d waiting", st.Waiting)
	}
	// The server's feed includes the 4 rows this device pushed, but they are filtered out: only Beta is pulled.
	if st.LastPulled != 1 {
		t.Fatalf("expected exactly the server's one new row to be pulled, got %d", st.LastPulled)
	}

	// An edit on the server (newer updated_at) wins on the device.
	time.Sleep(10 * time.Millisecond)
	if err := centralPool.RunScoped(ctx, central.OrganisationID, func(ctx context.Context) error {
		_, err := centralPool.Q(ctx).Exec(ctx, `UPDATE parties SET legal_name = 'Alpha Traders Pvt Ltd', updated_at = now() WHERE id = $1`, alpha.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceSvc.SyncNow(ctx, device); err != nil {
		t.Fatal(err)
	}
	if name, _ := partyName(t, ctx, sharedPool, device.OrganisationID, alpha.ID); name != "Alpha Traders Pvt Ltd" {
		t.Fatalf("server edit did not reach the device: %q", name)
	}

	// An older edit loses to a newer copy, and a change naming another organisation lands in the caller's own.
	stale := datasync.Change{Table: "parties", RowID: alpha.ID, Op: "U", ChangedAt: time.Now(),
		Row: []byte(`{"id":"` + alpha.ID.String() + `","organisation_id":"` + uuid.NewString() + `","party_type":"CUSTOMER","legal_name":"Old Name","currency_code":"INR","status":"ACTIVE","created_at":"2020-01-01T00:00:00Z","updated_at":"2020-01-01T00:00:00Z"}`)}
	res, err := centralSvc.Push(ctx, central, "other-device", []datasync.Change{stale})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stale != 1 || res.Applied != 0 {
		t.Fatalf("a stale change must lose, got %+v", res)
	}
	if name, _ := partyName(t, ctx, centralPool, central.OrganisationID, alpha.ID); name != "Alpha Traders Pvt Ltd" {
		t.Fatalf("stale change overwrote newer data: %q", name)
	}

	// A delete on the device reaches the server.
	if err := sharedPool.RunScoped(ctx, device.OrganisationID, func(ctx context.Context) error {
		_, err := sharedPool.Q(ctx).Exec(ctx, `DELETE FROM parties WHERE id = $1`, alpha.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := deviceSvc.SyncNow(ctx, device); err != nil {
		t.Fatal(err)
	}
	if _, found := partyName(t, ctx, centralPool, central.OrganisationID, alpha.ID); found {
		t.Fatal("a contact deleted on the device is still on the server")
	}

	if err := deviceSvc.Unlink(ctx, device); err != nil {
		t.Fatal(err)
	}
	if st, _ := deviceSvc.Status(ctx, device); st.Linked {
		t.Fatal("still linked after Unlink")
	}
}
