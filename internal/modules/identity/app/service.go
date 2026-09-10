// Package app is the identity module's application/use-case layer.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"rechvix/internal/modules/identity/domain"
	orgapp "rechvix/internal/modules/organisation/app"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/crypto"
	"rechvix/internal/platform/database"
	"rechvix/internal/platform/permissions"
)

// OrganisationProvisioner is the narrow cross-module interface identity's
// Bootstrap use case depends on (docs/architecture.md §2 — "Cross-module
// calls go through the other module's application-layer interface").
// *orgapp.Service satisfies it.
type OrganisationProvisioner interface {
	Provision(ctx context.Context, p orgapp.ProvisionParams) (orgapp.ProvisionResult, error)
}

type SessionPolicy struct {
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
}

type Service struct {
	pool           database.Runner
	users          domain.UserRepository
	sessions       domain.SessionRepository
	passwordResets domain.PasswordResetRepository
	mfa            domain.MFARepository
	roles          domain.RoleRepository
	apiKeys        domain.APIKeyRepository
	permissions    *permissions.Checker
	orgs           OrganisationProvisioner
	hasher         *crypto.PasswordHasher
	aead           *crypto.AEAD
	audit          audit.Recorder
	loginLimiter   *loginLimiter
	sessionPolicy  SessionPolicy
	now            func() time.Time
}

func NewService(
	pool database.Runner,
	users domain.UserRepository,
	sessions domain.SessionRepository,
	passwordResets domain.PasswordResetRepository,
	mfa domain.MFARepository,
	roles domain.RoleRepository,
	apiKeys domain.APIKeyRepository,
	checker *permissions.Checker,
	orgs OrganisationProvisioner,
	hasher *crypto.PasswordHasher,
	aead *crypto.AEAD,
	recorder audit.Recorder,
	sessionPolicy SessionPolicy,
) *Service {
	return &Service{
		pool:           pool,
		users:          users,
		sessions:       sessions,
		passwordResets: passwordResets,
		mfa:            mfa,
		roles:          roles,
		apiKeys:        apiKeys,
		permissions:    checker,
		orgs:           orgs,
		hasher:         hasher,
		aead:           aead,
		audit:          recorder,
		loginLimiter:   newLoginLimiter(),
		sessionPolicy:  sessionPolicy,
		now:            time.Now,
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// --- Bootstrap ---

type BootstrapParams struct {
	OrganisationName    string
	DefaultCurrencyCode string
	DefaultTimezone     string
	LegalEntityName     string
	CountryCode         string
	// GSTIN/GSTStateCode are optional (Stage 5b addition) — passed
	// through to orgapp.Service.Provision.
	GSTIN         string
	GSTStateCode  string
	BranchCode    string
	BranchName    string
	WarehouseCode string
	WarehouseName string
	OwnerEmail    string
	OwnerFullName string
	OwnerPassword string
}

type BootstrapResult struct {
	OrganisationID uuid.UUID
	LegalEntityID  uuid.UUID
	BranchID       uuid.UUID
	WarehouseID    uuid.UUID
	OwnerUserID    uuid.UUID
}

// Bootstrap creates a brand-new organisation and its owner user. There is
// no public self-signup for this B2B system (brief §10's bootstrap
// requirement) — this is the one path that creates an organisation, and
// it is the caller's (apps/server's setup flow) responsibility to make
// sure it's reachable only for genuine first-time setup, e.g. gated by a
// one-time setup token or only enabled when no organisations exist yet,
// not exposed as a routine authenticated endpoint.
//
// This runs as two sequential transactions (organisation provisioning,
// then owner-user creation), not one atomic transaction spanning both
// modules — a deliberate trade-off to keep the identity/organisation
// module boundary real (docs/architecture.md §2: cross-module calls go
// through the other module's application interface, not a shared
// transaction reaching into its tables). If owner-user creation fails
// after organisation provisioning succeeds, the result is an orphaned
// organisation with no owner; Bootstrap is a rare, operator-driven,
// one-time-per-organisation action, so the operational remedy (delete the
// orphaned organisation and retry) is acceptable here in a way it would
// not be for a hot path like invoice finalization.
func (s *Service) Bootstrap(ctx context.Context, p BootstrapParams) (BootstrapResult, error) {
	if err := validatePassword(p.OwnerPassword); err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: %w", err)
	}

	provisioned, err := s.orgs.Provision(ctx, orgapp.ProvisionParams{
		OrganisationName:    p.OrganisationName,
		DefaultCurrencyCode: p.DefaultCurrencyCode,
		DefaultTimezone:     p.DefaultTimezone,
		LegalEntityName:     p.LegalEntityName,
		CountryCode:         p.CountryCode,
		GSTIN:               p.GSTIN,
		GSTStateCode:        p.GSTStateCode,
		BranchCode:          p.BranchCode,
		BranchName:          p.BranchName,
		WarehouseCode:       p.WarehouseCode,
		WarehouseName:       p.WarehouseName,
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: provisioning organisation: %w", err)
	}

	passwordHash, err := s.hasher.Hash(p.OwnerPassword)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: hashing owner password: %w", err)
	}

	userID, err := uuid.NewV7()
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: generating user id: %w", err)
	}
	roleID, err := uuid.NewV7()
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: generating role id: %w", err)
	}
	userRoleID, err := uuid.NewV7()
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: generating user_role id: %w", err)
	}
	now := s.now()

	err = s.pool.RunScoped(ctx, provisioned.OrganisationID, func(ctx context.Context) error {
		if err := s.users.Create(ctx, &domain.User{
			ID:                   userID,
			OrganisationID:       provisioned.OrganisationID,
			Email:                normalizeEmail(p.OwnerEmail),
			FullName:             p.OwnerFullName,
			PasswordHash:         passwordHash,
			Status:               domain.UserStatusActive,
			LastPasswordChangeAt: now,
			CreatedAt:            now,
			UpdatedAt:            now,
		}); err != nil {
			return fmt.Errorf("creating owner user: %w", err)
		}

		if err := s.roles.CreateRole(ctx, roleID, provisioned.OrganisationID, "OWNER", "Owner", true, now); err != nil {
			return fmt.Errorf("creating owner role: %w", err)
		}
		if err := s.roles.GrantAllPermissions(ctx, roleID); err != nil {
			return fmt.Errorf("granting owner permissions: %w", err)
		}
		if err := s.roles.AssignUserRole(ctx, userRoleID, provisioned.OrganisationID, userID, roleID, now); err != nil {
			return fmt.Errorf("assigning owner role: %w", err)
		}

		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: provisioned.OrganisationID,
			ActorType:      audit.ActorSystem,
			Action:         "user.created",
			EntityType:     "user",
			EntityID:       &userID,
			AfterState:     map[string]any{"email": normalizeEmail(p.OwnerEmail), "role": "OWNER"},
			At:             now,
		})
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("identity: creating owner user: %w", err)
	}

	return BootstrapResult{
		OrganisationID: provisioned.OrganisationID,
		LegalEntityID:  provisioned.LegalEntityID,
		BranchID:       provisioned.BranchID,
		WarehouseID:    provisioned.WarehouseID,
		OwnerUserID:    userID,
	}, nil
}

// --- Login / sessions ---

type LoginParams struct {
	Email     string
	Password  string
	MFACode   string
	IP        string
	UserAgent string
}

type LoginResult struct {
	SessionToken      string
	OrganisationID    uuid.UUID
	UserID            uuid.UUID
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

// Login authenticates a user by email+password (+MFA code if enrolled)
// and issues a new session. Every failure path returns
// domain.ErrInvalidCredentials except domain.ErrMFARequired (which
// legitimately must distinguish "need a second factor" from "wrong
// password" for the login UX to work) and domain.ErrRateLimited — this is
// the brief §27 user-enumeration protection: a client cannot tell "no
// such account" apart from "wrong password" from the error alone.
func (s *Service) Login(ctx context.Context, p LoginParams) (LoginResult, error) {
	email := normalizeEmail(p.Email)
	now := s.now()

	if !s.loginLimiter.Allow(email, now) {
		return LoginResult{}, domain.ErrRateLimited
	}

	var result LoginResult
	err := s.pool.Run(ctx, func(ctx context.Context) error {
		lookup, err := s.users.LookupForAuth(ctx, email)
		if err != nil {
			s.loginLimiter.RecordFailure(email, now)
			return domain.ErrInvalidCredentials
		}

		ok, err := crypto.Verify(p.Password, lookup.PasswordHash)
		if err != nil || !ok {
			s.loginLimiter.RecordFailure(email, now)
			return domain.ErrInvalidCredentials
		}

		if lookup.Status != domain.UserStatusActive {
			s.loginLimiter.RecordFailure(email, now)
			return domain.ErrInvalidCredentials
		}

		// From here on we know which organisation this login belongs to;
		// establish RLS scope before touching any tenant-owned table.
		if err := s.pool.SetOrganisationScope(ctx, lookup.OrganisationID); err != nil {
			return err
		}

		if lookup.MFAEnabled {
			if err := s.verifyMFAForLogin(ctx, lookup.ID, p.MFACode, now); err != nil {
				s.loginLimiter.RecordFailure(email, now)
				return err
			}
		}

		token, tokenHash, err := newSessionToken()
		if err != nil {
			return err
		}
		sessionID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generating session id: %w", err)
		}
		idleExpires := now.Add(s.sessionPolicy.IdleTimeout)
		absoluteExpires := now.Add(s.sessionPolicy.AbsoluteTimeout)

		if err := s.sessions.Create(ctx, &domain.Session{
			ID:                sessionID,
			OrganisationID:    lookup.OrganisationID,
			UserID:            lookup.ID,
			TokenHash:         tokenHash,
			CreatedAt:         now,
			LastSeenAt:        now,
			IdleExpiresAt:     idleExpires,
			AbsoluteExpiresAt: absoluteExpires,
			IP:                p.IP,
			UserAgent:         p.UserAgent,
		}); err != nil {
			return fmt.Errorf("creating session: %w", err)
		}

		if err := s.users.UpdateLastLogin(ctx, lookup.ID, now); err != nil {
			return fmt.Errorf("updating last login: %w", err)
		}

		if err := s.audit.Record(ctx, audit.Entry{
			OrganisationID: lookup.OrganisationID,
			ActorUserID:    &lookup.ID,
			ActorType:      audit.ActorUser,
			Action:         "user.login",
			EntityType:     "user",
			EntityID:       &lookup.ID,
			IP:             p.IP,
			UserAgent:      p.UserAgent,
			At:             now,
		}); err != nil {
			return err
		}

		result = LoginResult{
			SessionToken:      token,
			OrganisationID:    lookup.OrganisationID,
			UserID:            lookup.ID,
			IdleExpiresAt:     idleExpires,
			AbsoluteExpiresAt: absoluteExpires,
		}
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}

	s.loginLimiter.RecordSuccess(email)
	return result, nil
}

func newSessionToken() (raw string, hash string, err error) {
	_, encoded, err := crypto.RandomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generating session token: %w", err)
	}
	return encoded, crypto.HashToken(encoded), nil
}

// ValidateSession resolves a bearer token to a Principal. Called by the
// HTTP auth middleware on every authenticated request. Deliberately not
// wrapped in database.Pool.Run/RunScoped — this is a plain read (plus a
// best-effort last_seen_at touch) against the bare pool, consistent with
// sessions having no RLS policy (migrations/0004_sessions.up.sql); the
// resolved OrganisationID is what the caller then uses to open its own
// RunScoped transaction for the rest of the request.
func (s *Service) ValidateSession(ctx context.Context, rawToken string) (permissions.Principal, error) {
	hash := crypto.HashToken(rawToken)
	session, err := s.sessions.GetByTokenHash(ctx, hash)
	if err != nil {
		// Wrapped (not discarded) so a genuine DB/connectivity error is
		// distinguishable from "no such token" in logs, while
		// errors.Is(_, domain.ErrSessionInvalid) still holds for callers
		// that only care about the sentinel (brief §27 — no detail about
		// *why* a session is invalid should ever reach an HTTP client).
		return permissions.Principal{}, fmt.Errorf("%w: %v", domain.ErrSessionInvalid, err)
	}
	now := s.now()
	if session.RevokedAt != nil || now.After(session.IdleExpiresAt) || now.After(session.AbsoluteExpiresAt) {
		return permissions.Principal{}, domain.ErrSessionInvalid
	}

	// Best-effort idle-timeout refresh; a failure to touch the row is not
	// worth failing the request over.
	_ = s.sessions.Touch(ctx, session.ID, now)

	return permissions.Principal{UserID: session.UserID, OrganisationID: session.OrganisationID}, nil
}

func (s *Service) Logout(ctx context.Context, principal permissions.Principal, rawToken string) error {
	hash := crypto.HashToken(rawToken)
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		session, err := s.sessions.GetByTokenHash(ctx, hash)
		if err != nil {
			return nil // already gone; logout is idempotent
		}
		now := s.now()
		if err := s.sessions.Revoke(ctx, session.ID, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "user.logout",
			EntityType:     "session",
			EntityID:       &session.ID,
			At:             now,
		})
	})
}

// ListSessions returns the caller's own active sessions (brief §29
// "device/session list").
func (s *Service) ListSessions(ctx context.Context, principal permissions.Principal) ([]*domain.Session, error) {
	var result []*domain.Session
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		result, err = s.sessions.ListActiveForUser(ctx, principal.UserID)
		return err
	})
	return result, err
}

// LogoutAllDevices revokes every active session for the caller (brief
// §29 "logout all devices").
func (s *Service) LogoutAllDevices(ctx context.Context, principal permissions.Principal) error {
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		now := s.now()
		if err := s.sessions.RevokeAllForUser(ctx, principal.UserID, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "user.logout_all_devices",
			EntityType:     "user",
			EntityID:       &principal.UserID,
			At:             now,
		})
	})
}

// --- Team members ---

// TeamMember is the identity module's own read projection for the
// Settings > Team screen — deliberately narrower than domain.User (no
// password hash) since httpapi would otherwise have to remember to trim
// it on every response.
type TeamMember struct {
	ID          uuid.UUID
	Email       string
	FullName    string
	Status      domain.UserStatus
	MFAEnabled  bool
	LastLoginAt *time.Time
	CreatedAt   time.Time
	// Unrestricted true means this member can act on every company
	// (legal entity) in the organisation — the default, and the only
	// state that existed before company-scoped access — in which case
	// LegalEntityIDs is empty and meaningless. Unrestricted false means
	// LegalEntityIDs is the exact (possibly empty, meaning "none yet
	// assigned") set of companies they're restricted to.
	Unrestricted   bool
	LegalEntityIDs []uuid.UUID
}

// ListTeamMembers lists every user in the caller's organisation
// (identity.view_users), including each member's current company access
// (RoleRepo.ListUserCompanyAccess) so Settings > Team can render and edit
// it. One extra query per member — organisations at this product's scale
// are small (a handful to a few dozen team members), matching this
// module's existing N+1-is-fine style elsewhere rather than adding a
// bulk-fetch variant no other caller needs yet.
func (s *Service) ListTeamMembers(ctx context.Context, principal permissions.Principal) ([]TeamMember, error) {
	if err := s.permissions.Require(ctx, principal, "identity.view_users", permissions.Scope{}); err != nil {
		return nil, err
	}
	var users []*domain.User
	out := make([]TeamMember, 0)
	err := s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		users, err = s.users.ListByOrganisation(ctx, principal.OrganisationID)
		if err != nil {
			return err
		}
		roleID, err := s.roles.GetIDByCode(ctx, principal.OrganisationID, "OWNER")
		if err != nil {
			return fmt.Errorf("looking up owner role: %w", err)
		}
		for _, u := range users {
			unrestricted, legalEntityIDs, err := s.roles.ListUserCompanyAccess(ctx, principal.OrganisationID, u.ID, roleID)
			if err != nil {
				return fmt.Errorf("listing company access for %s: %w", u.ID, err)
			}
			out = append(out, TeamMember{
				ID: u.ID, Email: u.Email, FullName: u.FullName, Status: u.Status,
				MFAEnabled: u.MFAEnabled, LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt,
				Unrestricted: unrestricted, LegalEntityIDs: legalEntityIDs,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type CreateTeamMemberParams struct {
	FullName string
	Email    string
	Password string
	// LegalEntityIDs restricts this member to exactly these companies —
	// empty (the default) means unrestricted, identical to every team
	// member's access before company-scoped grants existed, so a
	// single-company install needs zero changes to keep working exactly
	// as before.
	LegalEntityIDs []uuid.UUID
}

// CreateTeamMember lets an Owner add another login to their own
// organisation (identity.manage_users) — the account-creation path
// bootstrap deliberately doesn't cover, since Bootstrap only ever
// provisions the single first-run owner (see Service.Bootstrap).
//
// v1 has exactly one role per organisation (OWNER, granted every
// permission — see RoleRepo.GrantAllPermissions's doc comment), so every
// team member added this way is a full peer of the person who invited
// them WITHIN whichever companies they're granted, not a restricted
// "staff" account with fewer permissions — company-scoped access
// restricts *which companies*, not *what they can do within them*.
// Curated, lesser roles are a real product decision this pass
// deliberately defers, same as RoleRepo.GrantAllPermissions already
// flags.
func (s *Service) CreateTeamMember(ctx context.Context, principal permissions.Principal, p CreateTeamMemberParams) (uuid.UUID, error) {
	if err := s.permissions.Require(ctx, principal, "identity.manage_users", permissions.Scope{}); err != nil {
		return uuid.UUID{}, err
	}
	if err := validatePassword(p.Password); err != nil {
		return uuid.UUID{}, fmt.Errorf("identity: %w", err)
	}
	if p.FullName == "" {
		return uuid.UUID{}, fmt.Errorf("identity: full name is required")
	}
	email := normalizeEmail(p.Email)
	if email == "" {
		return uuid.UUID{}, fmt.Errorf("identity: email is required")
	}

	passwordHash, err := s.hasher.Hash(p.Password)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("identity: hashing password: %w", err)
	}
	userID, err := uuid.NewV7()
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("identity: generating user id: %w", err)
	}
	accessRows, err := newUserRoleScopeRows(p.LegalEntityIDs)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("identity: %w", err)
	}
	now := s.now()

	err = s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		roleID, err := s.roles.GetIDByCode(ctx, principal.OrganisationID, "OWNER")
		if err != nil {
			return fmt.Errorf("looking up owner role: %w", err)
		}
		if err := s.users.Create(ctx, &domain.User{
			ID:                   userID,
			OrganisationID:       principal.OrganisationID,
			Email:                email,
			FullName:             p.FullName,
			PasswordHash:         passwordHash,
			Status:               domain.UserStatusActive,
			LastPasswordChangeAt: now,
			CreatedAt:            now,
			UpdatedAt:            now,
		}); err != nil {
			return fmt.Errorf("creating team member: %w", err)
		}
		// A brand-new user has no prior user_roles rows to clear — this
		// is the "insert" half of ReplaceUserCompanyAccess's delete-then-
		// insert, reused rather than a second, parallel insert-only path
		// that could drift from it.
		if err := s.roles.ReplaceUserCompanyAccess(ctx, principal.OrganisationID, userID, roleID, accessRows, now); err != nil {
			return fmt.Errorf("assigning role: %w", err)
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "user.created",
			EntityType:     "user",
			EntityID:       &userID,
			AfterState:     map[string]any{"email": email},
			At:             now,
		})
	})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("identity: creating team member: %w", err)
	}
	return userID, nil
}

// newUserRoleScopeRows builds the rows ReplaceUserCompanyAccess should
// write for a given "restrict to these companies" request: one
// unrestricted row (LegalEntityID nil) when legalEntityIDs is empty —
// the default, unrestricted case — otherwise one row per company.
// Deliberately never returns a zero-row slice: ReplaceUserCompanyAccess
// deletes-then-inserts, and zero rows in is zero rows in the table after,
// which Require's fail-closed matching reads as "holds this permission
// nowhere at all" rather than "unrestricted" — the opposite of what an
// empty LegalEntityIDs list is supposed to mean here.
func newUserRoleScopeRows(legalEntityIDs []uuid.UUID) ([]domain.UserRoleScope, error) {
	if len(legalEntityIDs) == 0 {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating user_role id: %w", err)
		}
		return []domain.UserRoleScope{{ID: id}}, nil
	}
	rows := make([]domain.UserRoleScope, len(legalEntityIDs))
	for i := range legalEntityIDs {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("generating user_role id: %w", err)
		}
		rows[i] = domain.UserRoleScope{ID: id, LegalEntityID: &legalEntityIDs[i]}
	}
	return rows, nil
}

// SetTeamMemberCompanyAccess changes which companies an EXISTING team
// member can access — the edit-time counterpart to CreateTeamMember's
// LegalEntityIDs, sharing the exact same ReplaceUserCompanyAccess write
// path (see that method's doc comment for why "add" and "edit" must not
// drift into two different code paths).
func (s *Service) SetTeamMemberCompanyAccess(ctx context.Context, principal permissions.Principal, userID uuid.UUID, legalEntityIDs []uuid.UUID) error {
	if err := s.permissions.Require(ctx, principal, "identity.manage_users", permissions.Scope{}); err != nil {
		return err
	}
	accessRows, err := newUserRoleScopeRows(legalEntityIDs)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	now := s.now()
	return s.pool.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		roleID, err := s.roles.GetIDByCode(ctx, principal.OrganisationID, "OWNER")
		if err != nil {
			return fmt.Errorf("looking up owner role: %w", err)
		}
		if err := s.roles.ReplaceUserCompanyAccess(ctx, principal.OrganisationID, userID, roleID, accessRows, now); err != nil {
			return fmt.Errorf("setting company access: %w", err)
		}
		return s.audit.Record(ctx, audit.Entry{
			OrganisationID: principal.OrganisationID,
			ActorUserID:    &principal.UserID,
			ActorType:      audit.ActorUser,
			Action:         "user.company_access_changed",
			EntityType:     "user",
			EntityID:       &userID,
			AfterState:     map[string]any{"legal_entity_ids": legalEntityIDs},
			At:             now,
		})
	})
}
