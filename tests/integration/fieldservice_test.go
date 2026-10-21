//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/modules/fieldservice"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func newFieldService(t *testing.T) *fieldservice.Service {
	t.Helper()
	return fieldservice.NewService(sharedPool, permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool), audit.NewPGRecorder(sharedPool), nil)
}

func TestFieldService_AssetIncidentWorkLogAndMaintenance(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	svc := newFieldService(t)
	lat, lng := decimal.RequireFromString("40.416775"), decimal.RequireFromString("-3.703790")
	war := time.Now().AddDate(1, 0, 0)

	if _, err := svc.CreateAsset(ctx, fx.Principal, fieldservice.AssetInput{PartyID: fx.CustomerID, Name: "Chiller 2", Latitude: &lat}); !errors.Is(err, fieldservice.ErrInvalid) {
		t.Fatalf("latitude without longitude is invalid: %v", err)
	}
	asset, err := svc.CreateAsset(ctx, fx.Principal, fieldservice.AssetInput{PartyID: fx.CustomerID, Name: "Chiller 2", Model: "CX-90", SerialNumber: "SN-1", Latitude: &lat, Longitude: &lng, WarrantyUntil: &war})
	if err != nil {
		t.Fatal(err)
	}
	if !asset.InWarranty || asset.Latitude == nil {
		t.Fatalf("warranty and site: %+v", asset)
	}
	if _, err := svc.CreateAsset(ctx, fx.Principal, fieldservice.AssetInput{PartyID: uuid.New(), Name: "x"}); !errors.Is(err, fieldservice.ErrInvalid) {
		t.Fatalf("unknown customer: %v", err)
	}

	inc, err := svc.CreateIncident(ctx, fx.Principal, fieldservice.IncidentInput{AssetID: &asset.ID, Title: "Compressor noise", Priority: "HIGH"})
	if err != nil {
		t.Fatal(err)
	}
	if inc.Number != "INC-000001" || inc.PartyID != fx.CustomerID || inc.Latitude == nil || inc.Status != "OPEN" {
		t.Fatalf("incident inherits owner and site from the asset: %+v", inc)
	}
	second, _ := svc.CreateIncident(ctx, fx.Principal, fieldservice.IncidentInput{PartyID: fx.CustomerID, Title: "Second"})
	if second.Number != "INC-000002" {
		t.Fatalf("numbers count up per organisation: %s", second.Number)
	}
	five, ninety := 5, 90
	if err := svc.Comment(ctx, fx.Principal, inc.ID, "Checked the mounts", &ninety); err != nil {
		t.Fatal(err)
	}
	if err := svc.Comment(ctx, fx.Principal, inc.ID, "Quick call", &five); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := svc.Comment(ctx, fx.Principal, inc.ID, "x", &zero); !errors.Is(err, fieldservice.ErrInvalid) {
		t.Fatalf("zero minutes is invalid: %v", err)
	}
	when := time.Now().Add(24 * time.Hour)
	if err := svc.Assign(ctx, fx.Principal, inc.ID, &fx.Principal.UserID, &when); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStatus(ctx, fx.Principal, inc.ID, "RESOLVED"); err != nil {
		t.Fatal(err)
	}
	got, ups, err := svc.GetIncident(ctx, fx.Principal, inc.ID)
	if err != nil || got.MinutesSpent != 95 || got.ResolvedAt == nil || len(ups) != 4 {
		t.Fatalf("work log: %+v updates=%d %v", got, len(ups), err)
	}
	if err := svc.SetStatus(ctx, fx.Principal, inc.ID, "OPEN"); err != nil {
		t.Fatal(err)
	}
	if reopened, _, _ := svc.GetIncident(ctx, fx.Principal, inc.ID); reopened.ResolvedAt != nil {
		t.Fatal("reopening clears the resolved time")
	}
	if list, _ := svc.ListIncidents(ctx, fx.Principal, fieldservice.IncidentFilter{}); len(list) != 2 || list[0].Priority != "HIGH" {
		t.Fatalf("urgent work first: %+v", list)
	}
	if err := svc.SetStatus(ctx, fx.Principal, second.ID, "CLOSED"); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.ListIncidents(ctx, fx.Principal, fieldservice.IncidentFilter{}); len(list) != 1 {
		t.Fatal("closed incidents leave the default list")
	}

	// Maintenance: a due plan raises one incident and moves forward without drifting; a retired asset raises none.
	if err := svc.CreatePlan(ctx, fx.Principal, fieldservice.PlanInput{AssetID: asset.ID, Title: "Quarterly service", EveryDays: 90, FirstDue: time.Now().UTC().Truncate(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	n, err := svc.RunMaintenance(ctx, fx.Principal.OrganisationID)
	if err != nil || n != 1 {
		t.Fatalf("one maintenance incident: %d %v", n, err)
	}
	if again, _ := svc.RunMaintenance(ctx, fx.Principal.OrganisationID); again != 0 {
		t.Fatalf("nothing is due on a second run, got %d", again)
	}
	plans, _ := svc.ListPlans(ctx, fx.Principal)
	if len(plans) != 1 || plans[0].NextDue != time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 90).Format("2006-01-02") {
		t.Fatalf("next service in 90 days: %+v", plans)
	}
	list, _ := svc.ListIncidents(ctx, fx.Principal, fieldservice.IncidentFilter{AssetID: &asset.ID})
	var fromPlan int
	for _, i := range list {
		if i.Source == "MAINTENANCE" {
			fromPlan++
		}
	}
	if fromPlan != 1 {
		t.Fatalf("the maintenance incident is marked as such: %+v", list)
	}

	other := spainFixture(t, ctx, acct)
	if rows, _ := svc.ListAssets(ctx, other.Principal, nil); len(rows) != 0 {
		t.Fatal("tenant isolation")
	}
	if _, _, err := svc.GetIncident(ctx, other.Principal, inc.ID); !errors.Is(err, fieldservice.ErrNotFound) {
		t.Fatalf("another organisation cannot read the incident: %v", err)
	}
}

func TestFieldService_AddressLocations(t *testing.T) {
	ctx := context.Background()
	_, _, acct, _ := newTestAccountingServices(t)
	fx := spainFixture(t, ctx, acct)
	svc := newFieldService(t)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	contacts := contactsapp.NewService(sharedPool, contactspg.NewPartyRepo(sharedPool), contactspg.NewAddressRepo(sharedPool), contactspg.NewTaxRegistrationRepo(sharedPool), checker, audit.NewPGRecorder(sharedPool))
	a1, err := contacts.AddAddress(ctx, fx.Principal, contactsapp.AddAddressParams{PartyID: fx.CustomerID, AddressType: contactsdomain.AddressShipping, Line1: "Calle Uno 1", City: "Madrid", CountryCode: "ES", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := contacts.AddAddress(ctx, fx.Principal, contactsapp.AddAddressParams{PartyID: fx.CustomerID, AddressType: contactsdomain.AddressShipping, Line1: "Calle Dos 2", City: "Sevilla", CountryCode: "ES"})
	lat, lng := decimal.RequireFromString("40.4168"), decimal.RequireFromString("-3.7038")
	if err := svc.SetAddressLocation(ctx, fx.Principal, a1.ID, &lat, &lng); err != nil {
		t.Fatal(err)
	}
	bad := decimal.RequireFromString("123")
	if err := svc.SetAddressLocation(ctx, fx.Principal, a2.ID, &bad, &lng); !errors.Is(err, fieldservice.ErrInvalid) {
		t.Fatalf("latitude 123 is invalid: %v", err)
	}
	if err := svc.SetAddressLocation(ctx, fx.Principal, uuid.New(), &lat, &lng); !errors.Is(err, fieldservice.ErrNotFound) {
		t.Fatalf("unknown address: %v", err)
	}
	locs, err := svc.AddressLocations(ctx, fx.Principal, fx.CustomerID)
	if err != nil || len(locs) != 1 || locs[0].AddressID != a1.ID {
		t.Fatalf("one pinned address of two: %+v %v", locs, err)
	}
	if err := svc.SetAddressLocation(ctx, fx.Principal, a1.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if locs, _ := svc.AddressLocations(ctx, fx.Principal, fx.CustomerID); len(locs) != 0 {
		t.Fatal("removing the pin clears it")
	}
}
