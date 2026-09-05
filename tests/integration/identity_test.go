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
