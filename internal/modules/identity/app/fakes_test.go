package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/identity/domain"
	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/platform/database"
)

// fakeRunner runs fn directly against the calling goroutine with no real
// transaction — sufficient for unit-testing application-layer business
// logic against in-memory fake repositories, per database.Runner.
type fakeRunner struct{}

func (fakeRunner) RunScoped(ctx context.Context, orgID uuid.UUID, fn database.TxFunc) error {
	return fn(ctx)
}
func (fakeRunner) Run(ctx context.Context, fn database.TxFunc) error               { return fn(ctx) }
func (fakeRunner) SetOrganisationScope(ctx context.Context, orgID uuid.UUID) error { return nil }

type fakeUserRepo struct {
	byID    map[uuid.UUID]*domain.User
	byEmail map[string]*domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: map[uuid.UUID]*domain.User{}, byEmail: map[string]*domain.User{}}
}

func (f *fakeUserRepo) Create(ctx context.Context, u *domain.User) error {
	if _, exists := f.byEmail[u.Email]; exists {
		return domain.ErrEmailAlreadyExists
	}
	cp := *u
	// Both maps deliberately point at the SAME struct so a later mutation
	// (SetMFAEnabled, UpdatePasswordHash, ...) is visible through either
	// lookup path — mirroring "it's one row in one table" in Postgres.
	f.byID[u.ID] = &cp
	f.byEmail[u.Email] = &cp
	return nil
}
func (f *fakeUserRepo) ListByOrganisation(ctx context.Context, organisationID uuid.UUID) ([]*domain.User, error) {
	var out []*domain.User
	for _, u := range f.byID {
		if u.OrganisationID == organisationID {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (f *fakeUserRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}
func (f *fakeUserRepo) LookupForAuth(ctx context.Context, email string) (*domain.AuthLookup, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &domain.AuthLookup{ID: u.ID, OrganisationID: u.OrganisationID, PasswordHash: u.PasswordHash, Status: u.Status, MFAEnabled: u.MFAEnabled}, nil
}
func (f *fakeUserRepo) UpdatePasswordHash(ctx context.Context, userID uuid.UUID, hash string, at time.Time) error {
	u, ok := f.byID[userID]
	if !ok {
		return domain.ErrNotFound
	}
	u.PasswordHash = hash
	u.LastPasswordChangeAt = at
	return nil
}
func (f *fakeUserRepo) UpdateLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	if u, ok := f.byID[userID]; ok {
		u.LastLoginAt = &at
	}
	return nil
}
func (f *fakeUserRepo) SetMFAEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	if u, ok := f.byID[userID]; ok {
		u.MFAEnabled = enabled // byEmail[u.Email] points at the same struct; see Create.
	}
	return nil
}

type fakeSessionRepo struct {
	byID        map[uuid.UUID]*domain.Session
	byTokenHash map[string]*domain.Session
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{byID: map[uuid.UUID]*domain.Session{}, byTokenHash: map[string]*domain.Session{}}
}

func (f *fakeSessionRepo) Create(ctx context.Context, s *domain.Session) error {
	cp := *s
	f.byID[s.ID] = &cp
	f.byTokenHash[s.TokenHash] = &cp
	return nil
}
func (f *fakeSessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, error) {
	s, ok := f.byTokenHash[tokenHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}
func (f *fakeSessionRepo) Touch(ctx context.Context, id uuid.UUID, lastSeenAt time.Time) error {
	if s, ok := f.byID[id]; ok {
		s.LastSeenAt = lastSeenAt
	}
	return nil
}
func (f *fakeSessionRepo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error {
	if s, ok := f.byID[id]; ok {
		s.RevokedAt = &at
	}
	return nil
}
func (f *fakeSessionRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID, at time.Time) error {
	for _, s := range f.byID {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &at
		}
	}
	return nil
}
func (f *fakeSessionRepo) ListActiveForUser(ctx context.Context, userID uuid.UUID) ([]*domain.Session, error) {
	var out []*domain.Session
	for _, s := range f.byID {
		if s.UserID == userID && s.RevokedAt == nil {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out, nil
}

type fakePasswordResetRepo struct {
	byID        map[uuid.UUID]*domain.PasswordResetToken
	byTokenHash map[string]*domain.PasswordResetToken
}

func newFakePasswordResetRepo() *fakePasswordResetRepo {
	return &fakePasswordResetRepo{byID: map[uuid.UUID]*domain.PasswordResetToken{}, byTokenHash: map[string]*domain.PasswordResetToken{}}
}
func (f *fakePasswordResetRepo) Create(ctx context.Context, t *domain.PasswordResetToken) error {
	cp := *t
	f.byID[t.ID] = &cp
	f.byTokenHash[t.TokenHash] = &cp
	return nil
}
func (f *fakePasswordResetRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.PasswordResetToken, error) {
	t, ok := f.byTokenHash[tokenHash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *t
	return &cp, nil
}
func (f *fakePasswordResetRepo) MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	t, ok := f.byID[id]
	if !ok || t.UsedAt != nil {
		return domain.ErrTokenInvalid
	}
	t.UsedAt = &at
	return nil
}

type fakeMFARepo struct {
	secrets        map[uuid.UUID]*domain.MFASecret
	recoveryHashes map[uuid.UUID]map[string]bool // userID -> hash -> used
}

func newFakeMFARepo() *fakeMFARepo {
	return &fakeMFARepo{secrets: map[uuid.UUID]*domain.MFASecret{}, recoveryHashes: map[uuid.UUID]map[string]bool{}}
}
func (f *fakeMFARepo) UpsertSecret(ctx context.Context, secret *domain.MFASecret) error {
	cp := *secret
	f.secrets[secret.UserID] = &cp
	return nil
}
func (f *fakeMFARepo) GetSecret(ctx context.Context, userID uuid.UUID) (*domain.MFASecret, error) {
	s, ok := f.secrets[userID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}
func (f *fakeMFARepo) SetEnabled(ctx context.Context, userID uuid.UUID, enabled bool) error {
	if s, ok := f.secrets[userID]; ok {
		s.Enabled = enabled
	}
	return nil
}
func (f *fakeMFARepo) ReplaceRecoveryCodes(ctx context.Context, orgID, userID uuid.UUID, codeHashes []string) error {
	m := map[string]bool{}
	for _, h := range codeHashes {
		m[h] = false
	}
	f.recoveryHashes[userID] = m
	return nil
}
func (f *fakeMFARepo) ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string, at time.Time) (bool, error) {
	m, ok := f.recoveryHashes[userID]
	if !ok {
		return false, nil
	}
	used, exists := m[codeHash]
	if !exists || used {
		return false, nil
	}
	m[codeHash] = true
	return true, nil
}

// fakeRoleRepo hands out a stable, deterministic role ID per
// (organisationID, code) pair, derived rather than stored, so
// GetIDByCode needs no separate bookkeeping from CreateRole/AssignUserRole
// (which this fake otherwise no-ops).
type fakeRoleRepo struct{}

func (fakeRoleRepo) CreateRole(ctx context.Context, id, organisationID uuid.UUID, code, name string, isSystem bool, at time.Time) error {
	return nil
}
func (fakeRoleRepo) GrantAllPermissions(ctx context.Context, roleID uuid.UUID) error { return nil }
func (fakeRoleRepo) AssignUserRole(ctx context.Context, id, organisationID, userID, roleID uuid.UUID, at time.Time) error {
	return nil
}
func (fakeRoleRepo) GetIDByCode(ctx context.Context, organisationID uuid.UUID, code string) (uuid.UUID, error) {
	return uuid.NewSHA1(organisationID, []byte(code)), nil
}

type fakeAPIKeyRepo struct{}

func (fakeAPIKeyRepo) Create(ctx context.Context, k *domain.APIKey) error { return nil }
func (fakeAPIKeyRepo) GetByHash(ctx context.Context, keyHash string) (*domain.APIKey, error) {
	return nil, domain.ErrNotFound
}
func (fakeAPIKeyRepo) Touch(ctx context.Context, id uuid.UUID, at time.Time) error  { return nil }
func (fakeAPIKeyRepo) Revoke(ctx context.Context, id uuid.UUID, at time.Time) error { return nil }
func (fakeAPIKeyRepo) ListActiveForOrganisation(ctx context.Context, organisationID uuid.UUID) ([]*domain.APIKey, error) {
	return nil, nil
}

type fakeOrgProvisioner struct {
	result orgapp.ProvisionResult
}

func (f fakeOrgProvisioner) Provision(ctx context.Context, p orgapp.ProvisionParams) (orgapp.ProvisionResult, error) {
	return f.result, nil
}
