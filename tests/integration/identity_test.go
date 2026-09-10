//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"

	identityapp "rechvix/internal/modules/identity/app"
	identitypg "rechvix/internal/modules/identity/pg"
	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/permissions"
)

func newTestIdentityService(t *testing.T) (*identityapp.Service, *orgapp.Service) {
	t.Helper()
	orgSvc := newTestOrgService(t)

	hasher, err := appcrypto.NewPasswordHasher(appcrypto.PasswordParams{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1})
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	aead, err := appcrypto.NewAEAD(key)
	if err != nil {
		t.Fatalf("NewAEAD: %v", err)
	}

	identitySvc := identityapp.NewService(
		sharedPool,
		identitypg.NewUserRepo(sharedPool),
		identitypg.NewSessionRepo(sharedPool),
		identitypg.NewPasswordResetRepo(sharedPool),
		identitypg.NewMFARepo(sharedPool),
		identitypg.NewRoleRepo(sharedPool),
		identitypg.NewAPIKeyRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		orgSvc,
		hasher, aead,
		audit.NewPGRecorder(sharedPool),
		identityapp.SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: 12 * time.Hour},
	)
	return identitySvc, orgSvc
}

func bootstrapTestTenant(t *testing.T, ctx context.Context, svc *identityapp.Service, email, password string) identityapp.BootstrapResult {
	t.Helper()
	unique := uuid.NewString()[:8]
	result, err := svc.Bootstrap(ctx, identityapp.BootstrapParams{
		OrganisationName:    "Integration Test Co " + unique,
		DefaultCurrencyCode: "INR",
		DefaultTimezone:     "Asia/Kolkata",
		LegalEntityName:     "Integration Test Co " + unique + " Pvt Ltd",
		CountryCode:         "IN",
		BranchCode:          "BR-" + unique,
		BranchName:          "Main Branch",
		WarehouseCode:       "WH-" + unique,
		WarehouseName:       "Main Warehouse",
		OwnerEmail:          email,
		OwnerFullName:       "Test Owner",
		OwnerPassword:       password,
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return result
}

// TestLoginSessionAuthenticatedRequestRoundTrip covers Bootstrap -> Login
// -> ValidateSession -> an authenticated, permission-checked,
// organisation-scoped read (GetOrganisation) — end to end against a real
// database, proving the whole RLS + session + RBAC chain actually works
// together, not just each piece in isolation.
func TestLoginSessionAuthenticatedRequestRoundTrip(t *testing.T) {
	ctx := context.Background()
	identitySvc, orgSvc := newTestIdentityService(t)

	email := "roundtrip-" + uuid.NewString()[:8] + "@example.com"
	password := "correct horse battery staple 42"
	boot := bootstrapTestTenant(t, ctx, identitySvc, email, password)

	loginResult, err := identitySvc.Login(ctx, identityapp.LoginParams{Email: email, Password: password, IP: "203.0.113.5", UserAgent: "integration-test"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if loginResult.OrganisationID != boot.OrganisationID || loginResult.UserID != boot.OwnerUserID {
		t.Fatalf("login result identity mismatch: got org=%s user=%s, want org=%s user=%s",
			loginResult.OrganisationID, loginResult.UserID, boot.OrganisationID, boot.OwnerUserID)
	}

	principal, err := identitySvc.ValidateSession(ctx, loginResult.SessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if principal.OrganisationID != boot.OrganisationID || principal.UserID != boot.OwnerUserID {
		t.Fatalf("resolved principal mismatch: %+v", principal)
	}

	// The owner role was granted every permission at bootstrap, so this
	// authenticated, permission-checked, RLS-scoped read must succeed and
	// return exactly this organisation.
	org, err := orgSvc.GetOrganisation(ctx, principal)
	if err != nil {
		t.Fatalf("GetOrganisation as the freshly-logged-in owner: %v", err)
	}
	if org.ID != boot.OrganisationID {
		t.Fatalf("GetOrganisation returned org %s, want %s", org.ID, boot.OrganisationID)
	}

	// An invalid/garbage token must not resolve to any principal.
	if _, err := identitySvc.ValidateSession(ctx, "not-a-real-token"); err == nil {
		t.Fatal("expected ValidateSession to reject a garbage token")
	}
}

// TestConcurrentSessionCreation simulates many simultaneous logins for
// the same user (e.g. several browser tabs racing to sign in) and checks
// every one succeeds with a distinct, independently valid session — no
// unique-constraint violation, no lost update, no corrupted row (brief
// §66).
func TestConcurrentSessionCreation(t *testing.T) {
	ctx := context.Background()
	identitySvc, _ := newTestIdentityService(t)

	email := "concurrent-" + uuid.NewString()[:8] + "@example.com"
	password := "correct horse battery staple 42"
	bootstrapTestTenant(t, ctx, identitySvc, email, password)

	const concurrency = 20
	tokens := make(chan string, concurrency)
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			result, err := identitySvc.Login(ctx, identityapp.LoginParams{Email: email, Password: password})
			if err != nil {
				errs <- err
				return
			}
			tokens <- result.SessionToken
		}()
	}

	seen := map[string]bool{}
	for i := 0; i < concurrency; i++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent login failed: %v", err)
		case tok := <-tokens:
			if seen[tok] {
				t.Fatal("two concurrent logins produced the same session token")
			}
			seen[tok] = true
		}
	}
	if len(seen) != concurrency {
		t.Fatalf("expected %d distinct sessions, got %d", concurrency, len(seen))
	}

	// Every one of them must independently validate.
	for tok := range seen {
		if _, err := identitySvc.ValidateSession(ctx, tok); err != nil {
			t.Fatalf("expected concurrently-created session to validate, got %v", err)
		}
	}
}

// TestCreateTeamMemberRoundTrip covers the account-creation gap Bootstrap
// deliberately leaves open (Stage 12): an Owner adding a second login to
// their own organisation, that new login actually authenticating, and the
// new member landing in the SAME organisation as the inviter — against
// real Postgres, real RLS, and the real permissions.PGStore (so this also
// proves migrations/0033's backfill actually grants identity.manage_users
// to the Owner role bootstrap just created).
func TestCreateTeamMemberRoundTrip(t *testing.T) {
	ctx := context.Background()
	identitySvc, _ := newTestIdentityService(t)

	ownerEmail := "team-owner-" + uuid.NewString()[:8] + "@example.com"
	password := "correct horse battery staple 42"
	boot := bootstrapTestTenant(t, ctx, identitySvc, ownerEmail, password)

	ownerLogin, err := identitySvc.Login(ctx, identityapp.LoginParams{Email: ownerEmail, Password: password})
	if err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	principal, err := identitySvc.ValidateSession(ctx, ownerLogin.SessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}

	memberEmail := "team-member-" + uuid.NewString()[:8] + "@example.com"
	memberPassword := "another very long password 99"
	memberID, err := identitySvc.CreateTeamMember(ctx, principal, identityapp.CreateTeamMemberParams{
		FullName: "Team Member", Email: memberEmail, Password: memberPassword,
	})
	if err != nil {
		t.Fatalf("CreateTeamMember: %v", err)
	}

	members, err := identitySvc.ListTeamMembers(ctx, principal)
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 team members (owner + new member), got %d", len(members))
	}

	memberLogin, err := identitySvc.Login(ctx, identityapp.LoginParams{Email: memberEmail, Password: memberPassword})
	if err != nil {
		t.Fatalf("new team member Login: %v", err)
	}
	if memberLogin.OrganisationID != boot.OrganisationID {
		t.Fatalf("new team member logged into org %s, want %s", memberLogin.OrganisationID, boot.OrganisationID)
	}
	if memberLogin.UserID != memberID {
		t.Fatalf("login resolved user %s, want %s", memberLogin.UserID, memberID)
	}
}

// TestTeamMemberCompanyAccess covers CreateTeamMemberParams.LegalEntityIDs
// and SetTeamMemberCompanyAccess end to end against a real database:
// restricting a new member to the bootstrap organisation's one company,
// confirming permissions.Checker.AllowedLegalEntities (the mechanism
// Phase 3's list/report filtering will call) reports exactly that
// restriction, then confirming SetTeamMemberCompanyAccess with an empty
// list makes them unrestricted again — the same "empty means everything"
// contract CreateTeamMember's default already relies on.
func TestTeamMemberCompanyAccess(t *testing.T) {
	ctx := context.Background()
	identitySvc, _ := newTestIdentityService(t)
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)

	ownerEmail := "company-access-owner-" + uuid.NewString()[:8] + "@example.com"
	password := "correct horse battery staple 42"
	boot := bootstrapTestTenant(t, ctx, identitySvc, ownerEmail, password)

	ownerLogin, err := identitySvc.Login(ctx, identityapp.LoginParams{Email: ownerEmail, Password: password})
	if err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	principal, err := identitySvc.ValidateSession(ctx, ownerLogin.SessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}

	memberEmail := "restricted-member-" + uuid.NewString()[:8] + "@example.com"
	memberID, err := identitySvc.CreateTeamMember(ctx, principal, identityapp.CreateTeamMemberParams{
		FullName: "Restricted Member", Email: memberEmail, Password: "another very long password 99",
		LegalEntityIDs: []uuid.UUID{boot.LegalEntityID},
	})
	if err != nil {
		t.Fatalf("CreateTeamMember: %v", err)
	}

	members, err := identitySvc.ListTeamMembers(ctx, principal)
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	var restricted *identityapp.TeamMember
	for i := range members {
		if members[i].ID == memberID {
			restricted = &members[i]
		}
	}
	if restricted == nil {
		t.Fatal("restricted member not found in ListTeamMembers")
	}
	if restricted.Unrestricted {
		t.Fatal("expected the new member to be company-restricted, got Unrestricted=true")
	}
	if len(restricted.LegalEntityIDs) != 1 || restricted.LegalEntityIDs[0] != boot.LegalEntityID {
		t.Fatalf("LegalEntityIDs = %v, want exactly [%s]", restricted.LegalEntityIDs, boot.LegalEntityID)
	}

	memberPrincipal := permissions.Principal{UserID: memberID, OrganisationID: boot.OrganisationID}
	unrestricted, ids, err := checker.AllowedLegalEntities(ctx, memberPrincipal, "sales.view")
	if err != nil {
		t.Fatalf("AllowedLegalEntities: %v", err)
	}
	if unrestricted {
		t.Fatal("expected the restricted member's AllowedLegalEntities to be unrestricted=false")
	}
	if len(ids) != 1 || ids[0] != boot.LegalEntityID {
		t.Fatalf("AllowedLegalEntities ids = %v, want exactly [%s]", ids, boot.LegalEntityID)
	}

	// Lift the restriction — empty list means unrestricted, same contract
	// as CreateTeamMember's own default.
	if err := identitySvc.SetTeamMemberCompanyAccess(ctx, principal, memberID, nil); err != nil {
		t.Fatalf("SetTeamMemberCompanyAccess: %v", err)
	}
	unrestrictedAfter, _, err := checker.AllowedLegalEntities(ctx, memberPrincipal, "sales.view")
	if err != nil {
		t.Fatalf("AllowedLegalEntities after reset: %v", err)
	}
	if !unrestrictedAfter {
		t.Fatal("expected the member to be unrestricted after SetTeamMemberCompanyAccess(nil)")
	}
}

// TestRestrictedMember_SeesOnlyGrantedCompaniesInSwitcher covers a real
// bug this session found via manual smoke testing (not unit tests) and
// fixed the same session: organisation.app.Service.ListLegalEntities/
// ListBranches/GetOrganisation used a coarse, unscoped
// Require(ctx, principal, "settings.view", Scope{}) check — which, like
// every other "view" gate before this session's HasAny fix, rejects a
// company-restricted member outright (their grants are ALL
// company-scoped, none unrestricted, and Require's Scope{} only matches
// an unrestricted grant). Since apps/web's useOrgContext/CompanySwitcher
// call exactly these three endpoints on every page load, this bug broke
// the ENTIRE frontend for any restricted member, not just the switcher —
// worse than the sales/purchase HasAny bug this same pattern caused
// earlier, since there was no page a restricted member could load at
// all. Fixed by converting GetOrganisation to HasAny and — since
// ListLegalEntities/ListBranches are exactly the place a restricted
// member's allowed companies need to determine what's even offered as
// an option, not just gate a coarse yes/no — filtering their results by
// AllowedLegalEntities, the same pattern ListDocuments already used.
func TestRestrictedMember_SeesOnlyGrantedCompaniesInSwitcher(t *testing.T) {
	ctx := context.Background()
	identitySvc, orgSvc := newTestIdentityService(t)

	ownerEmail := "switcher-owner-" + uuid.NewString()[:8] + "@example.com"
	password := "correct horse battery staple 42"
	boot := bootstrapTestTenant(t, ctx, identitySvc, ownerEmail, password)

	ownerLogin, err := identitySvc.Login(ctx, identityapp.LoginParams{Email: ownerEmail, Password: password})
	if err != nil {
		t.Fatalf("owner Login: %v", err)
	}
	principal, err := identitySvc.ValidateSession(ctx, ownerLogin.SessionToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}

	unique := uuid.NewString()[:8]
	companyB, err := orgSvc.CreateLegalEntity(ctx, principal, orgapp.CreateLegalEntityParams{
		LegalName: "Switcher Company B " + unique, CountryCode: "IN", BaseCurrencyCode: "INR",
		GSTIN: "27DDDDD0000D1Z5", GSTStateCode: "27",
	})
	if err != nil {
		t.Fatalf("CreateLegalEntity (company B): %v", err)
	}
	if _, err := orgSvc.CreateBranch(ctx, principal, orgapp.CreateBranchParams{
		LegalEntityID: companyB.ID, Code: "SWB-" + unique, Name: "Company B Branch",
	}); err != nil {
		t.Fatalf("CreateBranch (company B): %v", err)
	}

	memberID, err := identitySvc.CreateTeamMember(ctx, principal, identityapp.CreateTeamMemberParams{
		FullName: "Switcher Test Member", Email: "switcher-member-" + unique + "@example.com", Password: "another very long password 99",
		LegalEntityIDs: []uuid.UUID{boot.LegalEntityID}, // company A only, NOT company B
	})
	if err != nil {
		t.Fatalf("CreateTeamMember: %v", err)
	}
	memberPrincipal := permissions.Principal{UserID: memberID, OrganisationID: boot.OrganisationID}

	// GetOrganisation must succeed at all (the pre-fix bug rejected this
	// outright) — every page in the app calls this via useOrgContext.
	if _, err := orgSvc.GetOrganisation(ctx, memberPrincipal); err != nil {
		t.Fatalf("GetOrganisation (restricted member): %v", err)
	}

	legalEntities, err := orgSvc.ListLegalEntities(ctx, memberPrincipal)
	if err != nil {
		t.Fatalf("ListLegalEntities (restricted member): %v", err)
	}
	if len(legalEntities) != 1 || legalEntities[0].ID != boot.LegalEntityID {
		t.Fatalf("ListLegalEntities (restricted member) = %d entities, want exactly [company A] (company B must not appear)", len(legalEntities))
	}

	branches, err := orgSvc.ListBranches(ctx, memberPrincipal)
	if err != nil {
		t.Fatalf("ListBranches (restricted member): %v", err)
	}
	for _, br := range branches {
		if br.LegalEntityID == companyB.ID {
			t.Fatalf("ListBranches (restricted member) leaked company B's branch %s", br.ID)
		}
	}
	if len(branches) != 1 || branches[0].LegalEntityID != boot.LegalEntityID {
		t.Fatalf("ListBranches (restricted member) = %d branches, want exactly [company A's branch]", len(branches))
	}

	// The unrestricted owner must still see both.
	ownerLegalEntities, err := orgSvc.ListLegalEntities(ctx, principal)
	if err != nil {
		t.Fatalf("ListLegalEntities (owner): %v", err)
	}
	if len(ownerLegalEntities) != 2 {
		t.Fatalf("ListLegalEntities (owner) = %d entities, want 2 (both companies)", len(ownerLegalEntities))
	}
}
