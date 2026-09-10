package app

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"rechvix/internal/modules/pricing/domain"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/money"
	"rechvix/internal/platform/permissions"
)

// fakeRunner runs fn directly with no real transaction — same shape as
// internal/modules/identity/app's fakeRunner, duplicated locally since Go
// has no shared-test-helper import across module packages without an
// exported (and therefore production-reachable) test double.
type fakeRunner struct{}

func (fakeRunner) RunScoped(ctx context.Context, orgID uuid.UUID, fn database.TxFunc) error {
	return fn(ctx)
}
func (fakeRunner) Run(ctx context.Context, fn database.TxFunc) error               { return fn(ctx) }
func (fakeRunner) SetOrganisationScope(ctx context.Context, orgID uuid.UUID) error { return nil }

// allowAllStore grants every permission unrestricted — sufficient for
// testing SetPrice's own validation logic without also exercising RBAC.
type allowAllStore struct{}

func (allowAllStore) Grants(ctx context.Context, userID uuid.UUID) ([]permissions.Grant, error) {
	return []permissions.Grant{{PermissionCode: "pricing.manage"}, {PermissionCode: "pricing.view"}}, nil
}

type noopAudit struct{}

func (noopAudit) Record(ctx context.Context, e audit.Entry) error { return nil }

type fakePriceListItemRepo struct {
	upserted *domain.PriceListItem
}

func (f *fakePriceListItemRepo) Upsert(ctx context.Context, item *domain.PriceListItem) error {
	f.upserted = item
	return nil
}
func (f *fakePriceListItemRepo) ListByPriceList(ctx context.Context, priceListID uuid.UUID) ([]*domain.PriceListItem, error) {
	return nil, nil
}
func (f *fakePriceListItemRepo) Resolve(ctx context.Context, priceListID, variantID, unitID uuid.UUID) (*domain.PriceListItem, error) {
	return nil, domain.ErrNotFound
}
func (f *fakePriceListItemRepo) DeleteByVariant(ctx context.Context, orgID, variantID uuid.UUID) error {
	return nil
}

type fakePriceListRepo struct{}

func (fakePriceListRepo) Create(ctx context.Context, pl *domain.PriceList) error { return nil }
func (fakePriceListRepo) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.PriceList, error) {
	return nil, domain.ErrNotFound
}
func (fakePriceListRepo) ListByOrganisation(ctx context.Context, orgID uuid.UUID) ([]*domain.PriceList, error) {
	return nil, nil
}

func newTestService(items domain.PriceListItemRepository) *Service {
	checker := permissions.NewChecker(allowAllStore{}, fakeRunner{})
	return NewService(fakeRunner{}, fakePriceListRepo{}, items, checker, noopAudit{})
}

func TestSetPrice_RejectsNegativeAmount(t *testing.T) {
	svc := newTestService(&fakePriceListItemRepo{})
	principal := permissions.Principal{UserID: uuid.New(), OrganisationID: uuid.New()}

	negative, err := money.Parse("-5.00", "INR")
	if err != nil {
		t.Fatalf("money.Parse: %v", err)
	}

	_, err = svc.SetPrice(context.Background(), principal, SetPriceParams{
		PriceListID: uuid.New(), ProductVariantID: uuid.New(), UnitID: uuid.New(), Price: negative,
	})
	if !errors.Is(err, domain.ErrNegativePrice) {
		t.Fatalf("expected ErrNegativePrice, got %v", err)
	}
}

func TestSetPrice_AcceptsZeroAndPositiveAmounts(t *testing.T) {
	for _, amount := range []string{"0.00", "76.27"} {
		items := &fakePriceListItemRepo{}
		svc := newTestService(items)
		principal := permissions.Principal{UserID: uuid.New(), OrganisationID: uuid.New()}

		price, err := money.Parse(amount, "INR")
		if err != nil {
			t.Fatalf("money.Parse(%q): %v", amount, err)
		}

		item, err := svc.SetPrice(context.Background(), principal, SetPriceParams{
			PriceListID: uuid.New(), ProductVariantID: uuid.New(), UnitID: uuid.New(), Price: price,
		})
		if err != nil {
			t.Fatalf("SetPrice(%q): unexpected error %v", amount, err)
		}
		if items.upserted == nil {
			t.Fatalf("SetPrice(%q): expected the repository to receive an upsert", amount)
		}
		if item.Price.Decimal().String() != price.Decimal().String() {
			t.Errorf("SetPrice(%q): stored price %s, want %s", amount, item.Price, price)
		}
	}
}
