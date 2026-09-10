// Package permissions implements RBAC permission checking
// (docs/architecture.md §10, brief §26). Every protected application-layer
// handler calls Checker.Require before doing anything — authorization is
// never inferred from what the UI happened to render (brief Rule 6).
package permissions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"rechvix/internal/platform/database"
)

// Principal is the authenticated caller of an application-layer method —
// resolved once by the HTTP layer from a validated session (or, later,
// API key) and passed down explicitly rather than pulled from context
// deep inside a module, so a use-case method's signature makes it obvious
// which calls require authentication.
type Principal struct {
	UserID         uuid.UUID
	OrganisationID uuid.UUID
}

// Scope describes how narrowly a requested action is targeted. A nil
// field means "this action isn't tied to that level" (e.g. viewing the
// organisation-wide dashboard has no branch/warehouse). Require matches a
// user's grants against this scope — see Checker.Require for the matching
// rule.
type Scope struct {
	LegalEntityID *uuid.UUID
	BranchID      *uuid.UUID
	WarehouseID   *uuid.UUID
}

// Grant is one row of "this user, through some role, holds this
// permission, optionally restricted to a specific legal entity/branch/
// warehouse." A nil field on a Grant means that grant is NOT restricted
// at that level (it applies regardless of which legal entity/branch/
// warehouse the action targets) — this is the opposite meaning of a nil
// field on Scope, which is why the matching rule in Require handles the
// two independently rather than doing a naive field-by-field equality.
type Grant struct {
	PermissionCode string
	LegalEntityID  *uuid.UUID
	BranchID       *uuid.UUID
	WarehouseID    *uuid.UUID
}

// Store loads a user's effective grants. Implemented against Postgres in
// pg.go; kept as an interface so unit tests can supply an in-memory fake
// without a database.
type Store interface {
	Grants(ctx context.Context, userID uuid.UUID) ([]Grant, error)
}

// ErrForbidden is wrapped into the error Require returns when the
// principal lacks the requested permission. Callers map it to HTTP 403
// (internal/platform/http.NewForbidden) at the transport boundary.
type ErrForbidden struct {
	PermissionCode string
}

func (e *ErrForbidden) Error() string {
	return fmt.Sprintf("permissions: missing %q", e.PermissionCode)
}

// Checker checks a user's permissions against their loaded grants. It
// holds a database.Runner and opens its OWN short RunScoped transaction
// around the grants lookup — deliberately, rather than trusting every
// caller to already be inside one. user_roles and roles (which Store.Grants
// joins across) are RLS-protected tables (migrations/0002_rbac_catalog.up.sql,
// 0003_users.up.sql): a Require call made outside any
// app.current_organisation_id scope would see zero grants and reject
// every request as forbidden, fail-closed but wrongly — Stage 2's own
// integration tests caught exactly this bug when Require was first
// wired up called before the caller's RunScoped block instead of inside
// it. Self-scoping here means every module gets this right automatically,
// instead of every future call site needing to remember the ordering.
type Checker struct {
	store  Store
	runner database.Runner
}

func NewChecker(store Store, runner database.Runner) *Checker {
	return &Checker{store: store, runner: runner}
}

// Require returns nil if principal holds permissionCode at (or covering)
// scope, otherwise a non-nil error wrapping ErrForbidden.
//
// A grant matches a requested scope level-by-level: for each of
// LegalEntityID/BranchID/WarehouseID, either the grant's value at that
// level is nil (unrestricted — applies everywhere) or it equals the
// scope's value at that level. A grant scoped to a specific branch never
// matches a request naming a different branch, but an organisation-wide
// grant (all three nil) matches any scope.
func (c *Checker) Require(ctx context.Context, principal Principal, permissionCode string, scope Scope) error {
	// An API-key-authenticated request carries an additional restriction
	// on top of the underlying user's RBAC grants (checked below) — a key
	// can never exercise more than its own declared scopes allow, even if
	// its owning user's role grants more. A session-authenticated request
	// carries no restriction here at all (Require behaves exactly as
	// before this existed), which is the "Principal shouldn't care which
	// auth method produced it" property docs/architecture.md §11 asks for.
	if restricted, ok := apiKeyScopeFromContext(ctx); ok && !restricted[permissionCode] {
		return fmt.Errorf("permissions: user %s: %w", principal.UserID, &ErrForbidden{PermissionCode: permissionCode})
	}

	grants, err := c.grants(ctx, principal)
	if err != nil {
		return err
	}
	for _, g := range grants {
		if g.PermissionCode != permissionCode {
			continue
		}
		if levelMatches(g.LegalEntityID, scope.LegalEntityID) &&
			levelMatches(g.BranchID, scope.BranchID) &&
			levelMatches(g.WarehouseID, scope.WarehouseID) {
			return nil
		}
	}
	return fmt.Errorf("permissions: user %s: %w", principal.UserID, &ErrForbidden{PermissionCode: permissionCode})
}

// AllowedLegalEntities answers "which companies can principal act on for
// permissionCode" — the read/list-side counterpart to Require's
// single-record check. unrestricted=true means every legal entity in the
// organisation is allowed (an org-wide grant — the common case for every
// install that has only ever had one company) and ids is meaningless;
// unrestricted=false means only the legal entities in ids are allowed,
// which is empty (not "everything") when the caller holds permissionCode
// nowhere at all — callers MUST treat that as "show nothing", the same
// fail-closed default Require already enforces for a single record.
//
// Deliberately does not consult apiKeyScopeFromContext itself (unlike
// Require and HasAny) — every real call site (ListDocuments and
// siblings) is only reached after that same request already passed a
// HasAny/Require gate check for the same permission code, which already
// rejects an out-of-scope API key before this method's result is ever
// used to build a filter. A caller of THIS method with no preceding gate
// check would not get that protection — don't add one without also
// checking whether such a caller exists.
func (c *Checker) AllowedLegalEntities(ctx context.Context, principal Principal, permissionCode string) (unrestricted bool, ids []uuid.UUID, err error) {
	grants, err := c.grants(ctx, principal)
	if err != nil {
		return false, nil, err
	}
	seen := make(map[uuid.UUID]bool)
	for _, g := range grants {
		if g.PermissionCode != permissionCode {
			continue
		}
		if g.LegalEntityID == nil {
			return true, nil, nil
		}
		if !seen[*g.LegalEntityID] {
			seen[*g.LegalEntityID] = true
			ids = append(ids, *g.LegalEntityID)
		}
	}
	return false, ids, nil
}

// HasAny answers a coarser question than Require: "does principal hold
// permissionCode at ALL, for any company" — unlike Require(ctx, principal,
// code, Scope{}), which only succeeds for a truly org-wide (unrestricted)
// grant and (by design — see TestRequire_BranchScopedGrantDoesNotMatchUnscopedRequest)
// treats a company-scoped-only grant as not matching an unscoped request
// at all. That distinction is exactly right for a single-record action
// (Require with a specific Scope), but wrong for a plain "can this user
// use this feature/module at all" gate: with it, a team member
// restricted to one company via company-scoped grants (see
// identity.Service.CreateTeamMemberParams.LegalEntityIDs) would be
// unable to even list or view their OWN company's data, since every
// grant they hold names a specific legal entity and none is unrestricted.
// HasAny is that gate — it's satisfied by ANY grant for the code,
// org-wide or company-scoped — and should back module-level view/manage
// checks (GetDocument, ListDocuments, and their siblings across sales/
// purchases/inventory/reporting/catalogue), with the actual per-company
// restriction enforced separately by AllowedLegalEntities-based filtering
// (ListDocuments) or a Scope{LegalEntityID: ...}-based Require call at
// the specific record being acted on (document/product creation).
func (c *Checker) HasAny(ctx context.Context, principal Principal, permissionCode string) error {
	// Same API-key restriction as Require's identical first check — an
	// API-key-authenticated request (e.g. an MCP tool call) must never
	// exercise more than its own declared scopes allow, regardless of
	// what the underlying user's RBAC grants say. Missing this let a key
	// with no declared scope for permissionCode succeed anyway, since
	// HasAny otherwise only ever looks at the user's own grants — caught
	// by TestMCP_ScopedAPIKey_AllowsInScopeToolsOnly during this method's
	// own introduction.
	if restricted, ok := apiKeyScopeFromContext(ctx); ok && !restricted[permissionCode] {
		return fmt.Errorf("permissions: user %s: %w", principal.UserID, &ErrForbidden{PermissionCode: permissionCode})
	}
	unrestricted, allowed, err := c.AllowedLegalEntities(ctx, principal, permissionCode)
	if err != nil {
		return err
	}
	if unrestricted || len(allowed) > 0 {
		return nil
	}
	return fmt.Errorf("permissions: user %s: %w", principal.UserID, &ErrForbidden{PermissionCode: permissionCode})
}

// ResolveLegalEntityFilter combines what AllowedLegalEntities reported for
// a permission (unrestricted, allowed) with an optional caller-requested
// company id (e.g. the frontend's currently-selected company) into the
// concrete filter a list/report query should apply.
//
// nil means "no restriction — return everything" (only possible when
// unrestricted and no specific company was requested, since an
// unrestricted caller asking for nothing in particular gets nothing
// filtered out). A non-nil, possibly-empty slice means "restrict to
// exactly these ids" — empty means "matches nothing", never "no
// restriction": a company-restricted caller who's allowed zero companies,
// or who asked for a company they don't hold, must see nothing, not
// everything.
func ResolveLegalEntityFilter(unrestricted bool, allowed []uuid.UUID, requested *uuid.UUID) []uuid.UUID {
	if unrestricted {
		if requested == nil {
			return nil
		}
		return []uuid.UUID{*requested}
	}
	if requested == nil {
		return allowed
	}
	for _, id := range allowed {
		if id == *requested {
			return []uuid.UUID{*requested}
		}
	}
	return []uuid.UUID{}
}

func (c *Checker) grants(ctx context.Context, principal Principal) ([]Grant, error) {
	var grants []Grant
	err := c.runner.RunScoped(ctx, principal.OrganisationID, func(ctx context.Context) error {
		var err error
		grants, err = c.store.Grants(ctx, principal.UserID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("permissions: loading grants: %w", err)
	}
	return grants, nil
}

func levelMatches(grantValue, scopeValue *uuid.UUID) bool {
	if grantValue == nil {
		return true // unrestricted at this level
	}
	if scopeValue == nil {
		// Grant is restricted to a specific entity at this level, but the
		// requested action doesn't specify one — treat as non-matching
		// rather than guessing; callers that need org-wide semantics
		// should hold an org-wide (nil) grant.
		return false
	}
	return *grantValue == *scopeValue
}
