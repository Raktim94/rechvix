package permissions

import "context"

// APIScopePermissions maps brief §36's coarse, fixed API-key scopes to the
// specific RBAC permission codes they authorize. Keyed by plain strings
// (not internal/modules/identity/domain.APIScope) deliberately — this
// platform-level package must not import an identity-module type, so
// callers (identity's HTTP middleware, apps/mcp) pass the scope's string
// value, which is exactly what domain.APIScope's underlying type already
// is.
var APIScopePermissions = map[string][]string{
	"products:read":   {"catalogue.view"},
	"inventory:read":  {"inventory.view"},
	"customers:read":  {"contacts.view"},
	"customers:write": {"contacts.view", "contacts.manage"},
	"invoices:read":   {"sales.view"},
	"invoices:write":  {"sales.view", "sales.create", "sales.edit_draft"},
	"reports:read":    {"reports.view"},
	"sync:readwrite":  {"sync.manage"},
}

// PermissionsForScopes expands a list of API-key scope strings into the
// set of permission codes they collectively authorize.
func PermissionsForScopes(scopes []string) map[string]bool {
	out := map[string]bool{}
	for _, sc := range scopes {
		for _, code := range APIScopePermissions[sc] {
			out[code] = true
		}
	}
	return out
}

type apiKeyScopeContextKey struct{}

// WithAPIKeyScopeRestriction attaches a set of permission codes that the
// current request is restricted to, on top of (never instead of) the
// caller's real RBAC grants — Require checks both. Session-authenticated
// requests never call this, so their ctx carries no restriction and
// Require's normal RBAC-only check applies unchanged; this is what lets
// docs/architecture.md §11 say "the Principal shouldn't care which auth
// method produced it" — Principal itself never changes shape, only ctx
// optionally carries an extra narrowing.
func WithAPIKeyScopeRestriction(ctx context.Context, permittedCodes map[string]bool) context.Context {
	return context.WithValue(ctx, apiKeyScopeContextKey{}, permittedCodes)
}

func apiKeyScopeFromContext(ctx context.Context) (map[string]bool, bool) {
	v, ok := ctx.Value(apiKeyScopeContextKey{}).(map[string]bool)
	return v, ok
}
