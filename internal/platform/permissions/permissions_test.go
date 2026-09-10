package permissions

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"rechvix/internal/platform/database"
)

type fakeStore struct {
	grants []Grant
}

func (f fakeStore) Grants(ctx context.Context, userID uuid.UUID) ([]Grant, error) {
	return f.grants, nil
}

// fakeRunner runs fn directly with no real transaction, so these unit
// tests exercise Require's grant-matching logic without a database — see
// database.Runner's doc comment for why Checker depends on this interface
// rather than *database.Pool directly.
type fakeRunner struct{}

func (fakeRunner) RunScoped(ctx context.Context, orgID uuid.UUID, fn database.TxFunc) error {
	return fn(ctx)
}
func (fakeRunner) Run(ctx context.Context, fn database.TxFunc) error               { return fn(ctx) }
func (fakeRunner) SetOrganisationScope(ctx context.Context, orgID uuid.UUID) error { return nil }

func uuidPtr(u uuid.UUID) *uuid.UUID { return &u }

func testPrincipal() Principal {
	return Principal{UserID: uuid.New(), OrganisationID: uuid.New()}
}

func TestRequire_OrgWideGrantMatchesAnyScope(t *testing.T) {
	principal := testPrincipal()
	branch := uuid.New()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.finalize"}, // org-wide, all nil
	}}, fakeRunner{})

	err := checker.Require(context.Background(), principal, "sales.finalize", Scope{BranchID: uuidPtr(branch)})
	if err != nil {
		t.Fatalf("expected org-wide grant to match a branch-scoped request, got error: %v", err)
	}
}

func TestRequire_BranchScopedGrantMatchesSameBranch(t *testing.T) {
	principal := testPrincipal()
	branch := uuid.New()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.finalize", BranchID: uuidPtr(branch)},
	}}, fakeRunner{})

	err := checker.Require(context.Background(), principal, "sales.finalize", Scope{BranchID: uuidPtr(branch)})
	if err != nil {
		t.Fatalf("expected branch-scoped grant to match the same branch, got error: %v", err)
	}
}

func TestRequire_BranchScopedGrantDoesNotMatchDifferentBranch(t *testing.T) {
	principal := testPrincipal()
	grantedBranch := uuid.New()
	requestedBranch := uuid.New()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.finalize", BranchID: uuidPtr(grantedBranch)},
	}}, fakeRunner{})

	err := checker.Require(context.Background(), principal, "sales.finalize", Scope{BranchID: uuidPtr(requestedBranch)})
	if err == nil {
		t.Fatal("expected error: grant scoped to a different branch must not match")
	}
	var forbidden *ErrForbidden
	if !errors.As(err, &forbidden) {
		t.Fatalf("expected error to wrap ErrForbidden, got %v", err)
	}
}

func TestRequire_BranchScopedGrantDoesNotMatchUnscopedRequest(t *testing.T) {
	principal := testPrincipal()
	grantedBranch := uuid.New()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.finalize", BranchID: uuidPtr(grantedBranch)},
	}}, fakeRunner{})

	// Requesting with no branch specified must NOT silently match a
	// branch-restricted grant — that would let a narrowly-scoped grant
	// leak into org-wide actions.
	err := checker.Require(context.Background(), principal, "sales.finalize", Scope{})
	if err == nil {
		t.Fatal("expected error: branch-scoped grant must not match an unscoped request")
	}
}

func TestRequire_WrongPermissionCodeFails(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.view"},
	}}, fakeRunner{})

	if err := checker.Require(context.Background(), principal, "sales.finalize", Scope{}); err == nil {
		t.Fatal("expected error: holding sales.view must not satisfy a sales.finalize check")
	}
}

func TestRequire_NoGrantsFails(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{}, fakeRunner{})

	if err := checker.Require(context.Background(), principal, "sales.finalize", Scope{}); err == nil {
		t.Fatal("expected error for a user with zero grants")
	}
}

func TestRequire_MultiLevelScopeAllMustMatch(t *testing.T) {
	principal := testPrincipal()
	legalEntity := uuid.New()
	branch := uuid.New()
	otherBranch := uuid.New()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "inventory.adjust", LegalEntityID: uuidPtr(legalEntity), BranchID: uuidPtr(branch)},
	}}, fakeRunner{})

	// Same legal entity, wrong branch -> must fail.
	err := checker.Require(context.Background(), principal, "inventory.adjust",
		Scope{LegalEntityID: uuidPtr(legalEntity), BranchID: uuidPtr(otherBranch)})
	if err == nil {
		t.Fatal("expected error: branch mismatch even though legal entity matches")
	}

	// Both match -> must succeed.
	err = checker.Require(context.Background(), principal, "inventory.adjust",
		Scope{LegalEntityID: uuidPtr(legalEntity), BranchID: uuidPtr(branch)})
	if err != nil {
		t.Fatalf("expected success when legal entity and branch both match, got %v", err)
	}
}

// TestRequire_ScopesGrantsLookupToPrincipalOrganisation verifies Require
// itself opens a RunScoped transaction around the grants lookup (using
// principal.OrganisationID), rather than depending on the caller to have
// already done so — the bug this guards against made every permission
// check fail closed as "forbidden" even for a fully-permissioned owner,
// because the grants query ran with no organisation scope set at all.
// See the Checker doc comment.
func TestRequire_ScopesGrantsLookupToPrincipalOrganisation(t *testing.T) {
	principal := testPrincipal()
	var scopedOrgID uuid.UUID
	recordingRunner := recordingRunner{inner: fakeRunner{}, onRunScoped: func(orgID uuid.UUID) { scopedOrgID = orgID }}

	checker := NewChecker(fakeStore{grants: []Grant{{PermissionCode: "sales.view"}}}, recordingRunner)
	if err := checker.Require(context.Background(), principal, "sales.view", Scope{}); err != nil {
		t.Fatalf("Require: %v", err)
	}
	if scopedOrgID != principal.OrganisationID {
		t.Fatalf("expected Require to scope its grants lookup to %s, got %s", principal.OrganisationID, scopedOrgID)
	}
}

func TestAllowedLegalEntities_NoGrantsIsRestrictedToNothing(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{}, fakeRunner{})

	unrestricted, ids, err := checker.AllowedLegalEntities(context.Background(), principal, "sales.view")
	if err != nil {
		t.Fatalf("AllowedLegalEntities: %v", err)
	}
	if unrestricted {
		t.Fatal("expected unrestricted=false for a user with zero grants — callers must show nothing, not everything")
	}
	if len(ids) != 0 {
		t.Fatalf("expected zero allowed legal entities, got %v", ids)
	}
}

func TestAllowedLegalEntities_OrgWideGrantIsUnrestricted(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.view"}, // org-wide, all nil
	}}, fakeRunner{})

	unrestricted, ids, err := checker.AllowedLegalEntities(context.Background(), principal, "sales.view")
	if err != nil {
		t.Fatalf("AllowedLegalEntities: %v", err)
	}
	if !unrestricted {
		t.Fatalf("expected unrestricted=true for an org-wide grant, got ids=%v", ids)
	}
}

func TestAllowedLegalEntities_CompanyScopedGrantsAreCollectedAndDeduped(t *testing.T) {
	principal := testPrincipal()
	companyA := uuid.New()
	companyB := uuid.New()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.view", LegalEntityID: uuidPtr(companyA)},
		{PermissionCode: "sales.view", LegalEntityID: uuidPtr(companyB)},
		// Same company via a second (e.g. branch-scoped) grant row — must
		// not appear twice.
		{PermissionCode: "sales.view", LegalEntityID: uuidPtr(companyA), BranchID: uuidPtr(uuid.New())},
		// Different permission code entirely — must not leak in.
		{PermissionCode: "purchase.view", LegalEntityID: uuidPtr(uuid.New())},
	}}, fakeRunner{})

	unrestricted, ids, err := checker.AllowedLegalEntities(context.Background(), principal, "sales.view")
	if err != nil {
		t.Fatalf("AllowedLegalEntities: %v", err)
	}
	if unrestricted {
		t.Fatal("expected unrestricted=false when every grant is company-scoped")
	}
	got := map[uuid.UUID]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(got) != 2 || !got[companyA] || !got[companyB] {
		t.Fatalf("expected exactly [companyA, companyB] (deduped), got %v", ids)
	}
}

func TestHasAny_UnrestrictedGrantSucceeds(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{grants: []Grant{{PermissionCode: "sales.view"}}}, fakeRunner{})
	if err := checker.HasAny(context.Background(), principal, "sales.view"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestHasAny_CompanyScopedGrantSucceeds(t *testing.T) {
	// This is the exact case Require(ctx, principal, code, Scope{}) gets
	// wrong for a company-restricted user — HasAny must succeed here.
	principal := testPrincipal()
	checker := NewChecker(fakeStore{grants: []Grant{
		{PermissionCode: "sales.view", LegalEntityID: uuidPtr(uuid.New())},
	}}, fakeRunner{})
	if err := checker.HasAny(context.Background(), principal, "sales.view"); err != nil {
		t.Fatalf("expected success for a company-scoped grant, got %v", err)
	}
}

func TestHasAny_APIKeyRestrictionOverridesUnderlyingGrant(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{grants: []Grant{{PermissionCode: "sales.view"}}}, fakeRunner{})
	ctx := WithAPIKeyScopeRestriction(context.Background(), map[string]bool{"purchase.view": true}) // NOT sales.view
	if err := checker.HasAny(ctx, principal, "sales.view"); err == nil {
		t.Fatal("expected error: an API key with no sales.view scope must not succeed via the user's own unrestricted grant")
	}
}

func TestHasAny_NoGrantsFails(t *testing.T) {
	principal := testPrincipal()
	checker := NewChecker(fakeStore{}, fakeRunner{})
	if err := checker.HasAny(context.Background(), principal, "sales.view"); err == nil {
		t.Fatal("expected error for a user with zero grants")
	}
}

func TestResolveLegalEntityFilter(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	// Unrestricted, no specific request -> no restriction at all.
	if got := ResolveLegalEntityFilter(true, nil, nil); got != nil {
		t.Fatalf("unrestricted+no request: got %v, want nil (no restriction)", got)
	}
	// Unrestricted, requested one company -> exactly that one.
	if got := ResolveLegalEntityFilter(true, nil, &a); len(got) != 1 || got[0] != a {
		t.Fatalf("unrestricted+requested a: got %v, want [%s]", got, a)
	}
	// Restricted, no specific request -> exactly the allowed set.
	if got := ResolveLegalEntityFilter(false, []uuid.UUID{a, b}, nil); len(got) != 2 {
		t.Fatalf("restricted+no request: got %v, want [%s %s]", got, a, b)
	}
	// Restricted, requested an allowed company -> exactly that one.
	if got := ResolveLegalEntityFilter(false, []uuid.UUID{a, b}, &a); len(got) != 1 || got[0] != a {
		t.Fatalf("restricted+requested allowed a: got %v, want [%s]", got, a)
	}
	// Restricted, requested a company NOT in the allowed set -> matches
	// nothing, never silently falls back to "everything".
	got := ResolveLegalEntityFilter(false, []uuid.UUID{a, b}, &c)
	if got == nil || len(got) != 0 {
		t.Fatalf("restricted+requested disallowed c: got %v, want empty-but-non-nil (matches nothing)", got)
	}
	// Restricted with zero allowed companies -> matches nothing.
	got = ResolveLegalEntityFilter(false, nil, nil)
	if len(got) != 0 {
		t.Fatalf("restricted+zero allowed: got %v, want empty", got)
	}
}

type recordingRunner struct {
	inner       database.Runner
	onRunScoped func(orgID uuid.UUID)
}

func (r recordingRunner) RunScoped(ctx context.Context, orgID uuid.UUID, fn database.TxFunc) error {
	r.onRunScoped(orgID)
	return r.inner.RunScoped(ctx, orgID, fn)
}
func (r recordingRunner) Run(ctx context.Context, fn database.TxFunc) error {
	return r.inner.Run(ctx, fn)
}
func (r recordingRunner) SetOrganisationScope(ctx context.Context, orgID uuid.UUID) error {
	return r.inner.SetOrganisationScope(ctx, orgID)
}
