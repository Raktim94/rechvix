package app

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/identity/domain"
	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	"rechvix/internal/platform/permissions"
)

// fakePermissionStore grants every principal exactly the fixed,
// org-wide set of permission codes it was built with — enough for
// testing identity's own permission-gated methods without pulling in a
// real RBAC catalog.
type fakePermissionStore struct{ codes []string }

func (s fakePermissionStore) Grants(ctx context.Context, userID uuid.UUID) ([]permissions.Grant, error) {
	out := make([]permissions.Grant, len(s.codes))
	for i, code := range s.codes {
		out[i] = permissions.Grant{PermissionCode: code}
	}
	return out, nil
}

type fakeAuditRecorder struct {
	entries []audit.Entry
}

func (f *fakeAuditRecorder) Record(ctx context.Context, e audit.Entry) error {
	f.entries = append(f.entries, e)
	return nil
}

func testHasher(t *testing.T) *crypto.PasswordHasher {
	t.Helper()
	h, err := crypto.NewPasswordHasher(crypto.PasswordParams{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1})
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	return h
}

func testAEAD(t *testing.T) *crypto.AEAD {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	a, err := crypto.NewAEAD(key)
	if err != nil {
		t.Fatalf("NewAEAD: %v", err)
	}
	return a
}

type testFixture struct {
	svc     *Service
	users   *fakeUserRepo
	sess    *fakeSessionRepo
	resets  *fakePasswordResetRepo
	mfa     *fakeMFARepo
	auditor *fakeAuditRecorder
}

func newTestFixture(t *testing.T, sessionPolicy SessionPolicy) *testFixture {
	t.Helper()
	users := newFakeUserRepo()
	sess := newFakeSessionRepo()
	resets := newFakePasswordResetRepo()
	mfa := newFakeMFARepo()
	auditor := &fakeAuditRecorder{}

	checker := permissions.NewChecker(fakePermissionStore{codes: []string{"identity.view_users", "identity.manage_users"}}, fakeRunner{})
	svc := NewService(
		fakeRunner{},
		users, sess, resets, mfa, fakeRoleRepo{}, fakeAPIKeyRepo{}, checker,
		fakeOrgProvisioner{result: orgapp.ProvisionResult{
			OrganisationID: uuid.New(), LegalEntityID: uuid.New(), BranchID: uuid.New(), WarehouseID: uuid.New(),
		}},
		testHasher(t), testAEAD(t), auditor, sessionPolicy,
	)
	return &testFixture{svc: svc, users: users, sess: sess, resets: resets, mfa: mfa, auditor: auditor}
}

func seedActiveUser(t *testing.T, f *testFixture, email, password string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	hash, err := testHasher(t).Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	orgID := uuid.New()
	userID := uuid.New()
	now := time.Now()
	if err := f.users.Create(context.Background(), &domain.User{
		ID: userID, OrganisationID: orgID, Email: normalizeEmail(email), FullName: "Test User",
		PasswordHash: hash, Status: domain.UserStatusActive, LastPasswordChangeAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return orgID, userID
}

func TestLogin_Success(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: 12 * time.Hour})
	orgID, userID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")

	result, err := f.svc.Login(context.Background(), LoginParams{Email: "Owner@Example.com", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.OrganisationID != orgID || result.UserID != userID {
		t.Fatalf("Login returned wrong identity: %+v", result)
	}
	if result.SessionToken == "" {
		t.Fatal("expected a non-empty session token")
	}

	foundLogin := false
	for _, e := range f.auditor.entries {
		if e.Action == "user.login" {
			foundLogin = true
		}
	}
	if !foundLogin {
		t.Error("expected a user.login audit entry")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")

	_, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "wrong password entirely"})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLogin_UnknownEmailReturnsSameErrorAsWrongPassword(t *testing.T) {
	// Enumeration protection (brief §27): the two failure modes must be
	// indistinguishable to the caller.
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")

	_, errUnknown := f.svc.Login(context.Background(), LoginParams{Email: "nobody@example.com", Password: "whatever12345"})
	_, errWrongPw := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "definitely-wrong"})

	if !errors.Is(errUnknown, domain.ErrInvalidCredentials) || !errors.Is(errWrongPw, domain.ErrInvalidCredentials) {
		t.Fatalf("expected both to be ErrInvalidCredentials, got %v / %v", errUnknown, errWrongPw)
	}
}

func TestLogin_DisabledUser(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	_, userID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	f.users.byID[userID].Status = domain.UserStatusDisabled

	_, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple"})
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for a disabled user, got %v", err)
	}
}

func TestLogin_RateLimitedAfterRepeatedFailures(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")

	// The limiter allows 5 free failures before locking out (see
	// loginLimiter.RecordFailure's freeAttempts constant); the 6th
	// failed attempt triggers the lockout, so the 7th attempt (even with
	// the correct password) must be rejected as rate-limited rather than
	// re-checked at all.
	var lastErr error
	for i := 0; i < 6; i++ {
		_, lastErr = f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "wrong"})
	}
	if !errors.Is(lastErr, domain.ErrInvalidCredentials) {
		t.Fatalf("expected the 6th failed attempt itself to still report ErrInvalidCredentials, got %v", lastErr)
	}
	_, lastErr = f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple"})
	if !errors.Is(lastErr, domain.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited on the 7th attempt (even with the correct password), got %v", lastErr)
	}
}

func TestValidateSession_ExpiredIdle(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: 24 * time.Hour})
	seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")

	result, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Force the stored session's idle_expires_at into the past to
	// simulate an idle timeout without needing to actually sleep.
	for _, s := range f.sess.byID {
		past := time.Now().Add(-time.Minute)
		s.IdleExpiresAt = past
	}

	if _, err := f.svc.ValidateSession(context.Background(), result.SessionToken); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid for an idle-expired session, got %v", err)
	}
}

func TestValidateSession_RevokedSessionRejected(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	result, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if err := f.svc.LogoutAllDevices(context.Background(), principalFor(result)); err != nil {
		t.Fatalf("LogoutAllDevices: %v", err)
	}

	if _, err := f.svc.ValidateSession(context.Background(), result.SessionToken); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatalf("expected ErrSessionInvalid for a revoked session, got %v", err)
	}
}

func TestChangePassword_WrongCurrentPasswordRejected(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	_, userID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	orgID := f.users.byID[userID].OrganisationID

	err := f.svc.ChangePassword(context.Background(), principalOf(orgID, userID), "totally wrong", "new password 12345", "new password 12345")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestChangePassword_ConfirmationMismatchRejected(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	_, userID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	orgID := f.users.byID[userID].OrganisationID

	err := f.svc.ChangePassword(context.Background(), principalOf(orgID, userID), "correct horse battery staple", "new password aaaa", "new password bbbb")
	if !errors.Is(err, domain.ErrPasswordConfirmMismatch) {
		t.Fatalf("expected ErrPasswordConfirmMismatch, got %v", err)
	}
}

func TestChangePassword_SuccessRevokesExistingSessions(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	_, userID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	orgID := f.users.byID[userID].OrganisationID

	loginResult, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	err = f.svc.ChangePassword(context.Background(), principalOf(orgID, userID), "correct horse battery staple", "brand new password 999", "brand new password 999")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := f.svc.ValidateSession(context.Background(), loginResult.SessionToken); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Fatal("expected the pre-password-change session to be revoked")
	}

	// New password must now work.
	if _, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "brand new password 999"}); err != nil {
		t.Fatalf("expected login with the new password to succeed, got %v", err)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")

	issued, err := f.svc.RequestPasswordReset(context.Background(), "owner@example.com")
	if err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	if issued == nil {
		t.Fatal("expected a token to be issued for a known account")
	}

	if err := f.svc.ResetPassword(context.Background(), issued.Token, "reset password value 1", "reset password value 1"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	// Token must be single-use.
	err = f.svc.ResetPassword(context.Background(), issued.Token, "another password value", "another password value")
	if !errors.Is(err, domain.ErrTokenInvalid) {
		t.Fatalf("expected ErrTokenInvalid on token reuse, got %v", err)
	}

	if _, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "reset password value 1"}); err != nil {
		t.Fatalf("expected login with the reset password to succeed, got %v", err)
	}
}

func TestRequestPasswordReset_UnknownEmailIssuesNothingButNoError(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})

	issued, err := f.svc.RequestPasswordReset(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatalf("expected no error for an unknown email (enumeration protection), got %v", err)
	}
	if issued != nil {
		t.Fatal("expected nil result for an unknown email")
	}
}

func TestMFAEnrollAndLoginRequiresCode(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour})
	_, userID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	orgID := f.users.byID[userID].OrganisationID
	principal := principalOf(orgID, userID)

	enrollment, err := f.svc.EnrollMFA(context.Background(), principal, "owner@example.com", "BillingPlatform")
	if err != nil {
		t.Fatalf("EnrollMFA: %v", err)
	}
	if enrollment.Secret == "" {
		t.Fatal("expected a non-empty TOTP secret")
	}

	code, err := totpCodeFor(enrollment.Secret)
	if err != nil {
		t.Fatalf("computing totp code: %v", err)
	}
	recoveryCodes, err := f.svc.VerifyMFAEnroll(context.Background(), principal, code)
	if err != nil {
		t.Fatalf("VerifyMFAEnroll: %v", err)
	}
	if len(recoveryCodes) == 0 {
		t.Fatal("expected recovery codes to be issued")
	}

	// Login without a code must now fail with ErrMFARequired.
	_, err = f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple"})
	if !errors.Is(err, domain.ErrMFARequired) {
		t.Fatalf("expected ErrMFARequired, got %v", err)
	}

	// Login with the correct TOTP code must succeed.
	code2, err := totpCodeFor(enrollment.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple", MFACode: code2}); err != nil {
		t.Fatalf("expected login with a valid TOTP code to succeed, got %v", err)
	}

	// A recovery code also works, exactly once.
	_, err = f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple", MFACode: recoveryCodes[0]})
	if err != nil {
		t.Fatalf("expected login with a recovery code to succeed, got %v", err)
	}
	_, err = f.svc.Login(context.Background(), LoginParams{Email: "owner@example.com", Password: "correct horse battery staple", MFACode: recoveryCodes[0]})
	if !errors.Is(err, domain.ErrMFAInvalid) {
		t.Fatalf("expected a reused recovery code to be rejected, got %v", err)
	}
}

func TestCreateTeamMember_Success(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: 12 * time.Hour})
	orgID, ownerID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	principal := principalOf(orgID, ownerID)

	newUserID, err := f.svc.CreateTeamMember(context.Background(), principal, CreateTeamMemberParams{
		FullName: "New Teammate",
		Email:    "Teammate@Example.com",
		Password: "another very long password",
	})
	if err != nil {
		t.Fatalf("CreateTeamMember: %v", err)
	}

	stored, err := f.users.GetByID(context.Background(), newUserID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Email != "teammate@example.com" {
		t.Fatalf("expected normalized email, got %q", stored.Email)
	}
	if stored.OrganisationID != orgID {
		t.Fatalf("expected new member to belong to the inviting owner's organisation")
	}

	members, err := f.svc.ListTeamMembers(context.Background(), principal)
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 team members (owner + new member), got %d", len(members))
	}

	found := false
	for _, e := range f.auditor.entries {
		if e.Action == "user.created" && e.EntityID != nil && *e.EntityID == newUserID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a user.created audit entry for the new team member")
	}
}

func TestCreateTeamMember_DuplicateEmailRejected(t *testing.T) {
	f := newTestFixture(t, SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: 12 * time.Hour})
	orgID, ownerID := seedActiveUser(t, f, "owner@example.com", "correct horse battery staple")
	principal := principalOf(orgID, ownerID)

	_, err := f.svc.CreateTeamMember(context.Background(), principal, CreateTeamMemberParams{
		FullName: "Duplicate", Email: "owner@example.com", Password: "another very long password",
	})
	if !errors.Is(err, domain.ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
}

func TestCreateTeamMember_RequiresPermission(t *testing.T) {
	// A principal whose role was never granted identity.manage_users —
	// built with its own zero-grant checker rather than newTestFixture's
	// (which grants every identity permission, to keep the rest of this
	// file's tests focused on business logic rather than RBAC).
	users := newFakeUserRepo()
	checker := permissions.NewChecker(fakePermissionStore{}, fakeRunner{})
	svc := NewService(
		fakeRunner{}, users, newFakeSessionRepo(), newFakePasswordResetRepo(), newFakeMFARepo(),
		fakeRoleRepo{}, fakeAPIKeyRepo{}, checker,
		fakeOrgProvisioner{}, testHasher(t), testAEAD(t), &fakeAuditRecorder{},
		SessionPolicy{IdleTimeout: time.Hour, AbsoluteTimeout: time.Hour},
	)
	unprivileged := principalOf(uuid.New(), uuid.New())

	_, err := svc.CreateTeamMember(context.Background(), unprivileged, CreateTeamMemberParams{
		FullName: "Nope", Email: "nope@example.com", Password: "another very long password",
	})
	var forbidden *permissions.ErrForbidden
	if !errors.As(err, &forbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
