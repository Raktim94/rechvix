package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"rechvix/internal/modules/identity/app"
	"rechvix/internal/modules/identity/domain"
	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/logging"
	"rechvix/internal/platform/permissions"
)

type Handlers struct {
	svc          *app.Service
	cookieName   string
	cookieSecure bool
	// postBootstrap runs after a successful Bootstrap, before the HTTP
	// response is written — the layering-safe hook docs/adr/0003-accounting-
	// integration-point.md's point 6 describes: identity (Stage 2,
	// foundational) can't import accounting (Stage 6) without inverting
	// this codebase's module layering, but apps/server's composition root
	// is allowed to depend on every module, so it injects this instead.
	// Nil-guarded — a caller that doesn't wire one still gets a working
	// bootstrap, just without the chained step.
	postBootstrap func(ctx context.Context, orgID, actorUserID uuid.UUID) error
}

func NewHandlers(svc *app.Service, cookieName string, cookieSecure bool) *Handlers {
	return &Handlers{svc: svc, cookieName: cookieName, cookieSecure: cookieSecure}
}

// WithPostBootstrapHook returns a copy of h with fn wired in — same
// WithX-returns-a-copy convention as ewaybill.Service.WithFreePortal and
// sales/httpapi.Handlers.WithEWayBill elsewhere in this codebase.
func (h *Handlers) WithPostBootstrapHook(fn func(ctx context.Context, orgID, actorUserID uuid.UUID) error) *Handlers {
	cp := *h
	cp.postBootstrap = fn
	return &cp
}

// Mount registers identity's routes. bootstrapEnabled gates
// POST /auth/bootstrap — the composition root should only pass true for a
// genuine first-time-setup deployment (e.g. checked against "does any
// organisation exist yet"), never leave it permanently reachable in a
// running production system, since it has no permission check by
// definition (see app.Service.Bootstrap's doc comment).
func (h *Handlers) Mount(r chi.Router, bootstrapEnabled bool) {
	if bootstrapEnabled {
		r.Post("/auth/bootstrap", h.bootstrap)
	}
	r.Post("/auth/login", h.login)
	r.Post("/auth/password-reset/request", h.requestPasswordReset)
	r.Post("/auth/password-reset/complete", h.completePasswordReset)

	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(h.svc, h.cookieName))
		r.Post("/auth/logout", h.logout)
		r.Post("/auth/logout-all", h.logoutAll)
		r.Get("/auth/sessions", h.listSessions)
		r.Post("/auth/change-password", h.changePassword)
		r.Post("/auth/mfa/enroll", h.enrollMFA)
		r.Post("/auth/mfa/verify", h.verifyMFAEnroll)
		r.Post("/auth/mfa/disable", h.disableMFA)
		r.Get("/users", h.listUsers)
		r.Post("/users", h.createUser)
	})
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

// writeServiceError maps known domain/permission errors to the right
// HTTP status; anything else is treated as an internal error (no detail
// leaked to the client, per brief §57).
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidCredentials),
		errors.Is(err, domain.ErrMFAInvalid):
		httpx.WriteError(w, r, httpx.NewUnauthorized("INVALID_CREDENTIALS", "Invalid email or password."))
	case errors.Is(err, domain.ErrMFARequired):
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusUnauthorized, Code: "MFA_REQUIRED", Message: "A verification code is required to complete sign-in."})
	case errors.Is(err, domain.ErrRateLimited):
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Too many attempts. Please try again later."})
	case errors.Is(err, domain.ErrTokenInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("TOKEN_INVALID", "This link is invalid, expired, or already used."))
	case errors.Is(err, domain.ErrPasswordConfirmMismatch):
		httpx.WriteError(w, r, httpx.NewBadRequest("PASSWORD_MISMATCH", "New password and confirmation do not match."))
	case errors.Is(err, domain.ErrSessionInvalid):
		httpx.WriteError(w, r, httpx.NewUnauthorized("SESSION_INVALID", "Your session has expired. Please sign in again."))
	case errors.Is(err, domain.ErrEmptyScopeList):
		httpx.WriteError(w, r, httpx.NewBadRequest("SCOPES_REQUIRED", "An API key requires at least one explicit scope."))
	case errors.Is(err, domain.ErrUnknownScope):
		httpx.WriteError(w, r, httpx.NewBadRequest("UNKNOWN_SCOPE", "One or more requested scopes are not recognized."))
	case errors.Is(err, domain.ErrEmailAlreadyExists):
		httpx.WriteError(w, r, httpx.NewBadRequest("EMAIL_EXISTS", "An account with this email already exists."))
	default:
		var forbidden *permissions.ErrForbidden
		if errors.As(err, &forbidden) {
			httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
			return
		}
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred.", Cause: err})
	}
}

type bootstrapRequest struct {
	OrganisationName    string `json:"organisation_name"`
	DefaultCurrencyCode string `json:"default_currency_code"`
	DefaultTimezone     string `json:"default_timezone"`
	LegalEntityName     string `json:"legal_entity_name"`
	CountryCode         string `json:"country_code"`
	// GSTIN/GSTStateCode were added to app.BootstrapParams in Stage 5b
	// but never threaded through this request struct — a real bug found
	// by actually running a fresh bootstrap through the real HTTP API,
	// not just calling the service layer directly the way the
	// integration test fixture does. Without these, GSTStateCode stays
	// permanently empty with no way to set it afterward either (no
	// PUT/PATCH existed for legal entities until this same pass added
	// one), so every real signup's first invoice finalize failed with a
	// tax_documents_supplier_state_code_fkey violation — the single most
	// core feature of a billing app, broken for every real user.
	GSTIN         string `json:"gstin,omitempty"`
	GSTStateCode  string `json:"gst_state_code,omitempty"`
	BranchCode    string `json:"branch_code"`
	BranchName    string `json:"branch_name"`
	WarehouseCode string `json:"warehouse_code"`
	WarehouseName string `json:"warehouse_name"`
	OwnerEmail    string `json:"owner_email"`
	OwnerFullName string `json:"owner_full_name"`
	OwnerPassword string `json:"owner_password"`
}

func (h *Handlers) bootstrap(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[bootstrapRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	result, err := h.svc.Bootstrap(r.Context(), app.BootstrapParams{
		OrganisationName:    req.OrganisationName,
		DefaultCurrencyCode: req.DefaultCurrencyCode,
		DefaultTimezone:     req.DefaultTimezone,
		LegalEntityName:     req.LegalEntityName,
		CountryCode:         req.CountryCode,
		GSTIN:               req.GSTIN,
		GSTStateCode:        req.GSTStateCode,
		BranchCode:          req.BranchCode,
		BranchName:          req.BranchName,
		WarehouseCode:       req.WarehouseCode,
		WarehouseName:       req.WarehouseName,
		OwnerEmail:          req.OwnerEmail,
		OwnerFullName:       req.OwnerFullName,
		OwnerPassword:       req.OwnerPassword,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.postBootstrap != nil {
		if err := h.postBootstrap(r.Context(), result.OrganisationID, result.OwnerUserID); err != nil {
			// The organisation/owner were genuinely created — surfacing
			// this as a hard failure here would tell the caller bootstrap
			// failed when it actually mostly succeeded. Same "degrade,
			// don't block the thing that already happened" principle as
			// sales/httpapi's enrichWithEWayBill. Logged loudly (not
			// silently swallowed) since the fallback — the operator/owner
			// manually calling POST /accounting/accounts/ensure-default-chart
			// (docs/adr/0003) — only works if someone actually knows this
			// failed.
			logging.FromContext(r.Context()).ErrorContext(r.Context(), "post-bootstrap hook failed",
				"organisation_id", result.OrganisationID, "error", err)
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"organisation_id": result.OrganisationID,
		"legal_entity_id": result.LegalEntityID,
		"branch_id":       result.BranchID,
		"warehouse_id":    result.WarehouseID,
		"owner_user_id":   result.OwnerUserID,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code"`
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[loginRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	result, err := h.svc.Login(r.Context(), app.LoginParams{
		Email:     req.Email,
		Password:  req.Password,
		MFACode:   req.MFACode,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.setSessionCookie(w, result.SessionToken, result.AbsoluteExpiresAt)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"organisation_id":     result.OrganisationID,
		"user_id":             result.UserID,
		"idle_expires_at":     result.IdleExpiresAt,
		"absolute_expires_at": result.AbsoluteExpiresAt,
	})
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	cookie, err := r.Cookie(h.cookieName)
	if err == nil {
		_ = h.svc.Logout(r.Context(), principal, cookie.Value)
	}
	h.clearSessionCookie(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *Handlers) logoutAll(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	if err := h.svc.LogoutAllDevices(r.Context(), principal); err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.clearSessionCookie(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "all_sessions_revoked"})
}

func (h *Handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	sessions, err := h.svc.ListSessions(r.Context(), principal)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	type sessionDTO struct {
		ID         string    `json:"id"`
		CreatedAt  time.Time `json:"created_at"`
		LastSeenAt time.Time `json:"last_seen_at"`
		IP         string    `json:"ip,omitempty"`
		UserAgent  string    `json:"user_agent,omitempty"`
	}
	out := make([]sessionDTO, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionDTO{ID: s.ID.String(), CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, IP: s.IP, UserAgent: s.UserAgent})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (h *Handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[changePasswordRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if err := h.svc.ChangePassword(r.Context(), principal, req.CurrentPassword, req.NewPassword, req.ConfirmPassword); err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.clearSessionCookie(w)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "password_changed"})
}

type requestPasswordResetRequest struct {
	Email string `json:"email"`
}

func (h *Handlers) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[requestPasswordResetRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	// Result (including whether an account was found) is intentionally
	// discarded from the response — see app.Service.RequestPasswordReset.
	// A real deployment wires the *app.PasswordResetIssued into
	// notifications (Stage 9); Stage 2 has no notifications module yet,
	// so it is dropped here rather than emailed.
	_, _ = h.svc.RequestPasswordReset(r.Context(), req.Email)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "if_account_exists_reset_link_sent",
	})
}

type completePasswordResetRequest struct {
	Token           string `json:"token"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (h *Handlers) completePasswordReset(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[completePasswordResetRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req.Token, req.NewPassword, req.ConfirmPassword); err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
}

type enrollMFARequest struct {
	AccountName string `json:"account_name"`
	Issuer      string `json:"issuer"`
}

func (h *Handlers) enrollMFA(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[enrollMFARequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	result, err := h.svc.EnrollMFA(r.Context(), principal, req.AccountName, req.Issuer)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"secret":           result.Secret,
		"provisioning_uri": result.ProvisioningURI,
	})
}

type verifyMFARequest struct {
	Code string `json:"code"`
}

func (h *Handlers) verifyMFAEnroll(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[verifyMFARequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	codes, err := h.svc.VerifyMFAEnroll(r.Context(), principal, req.Code)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

type disableMFARequest struct {
	CurrentPassword string `json:"current_password"`
}

func (h *Handlers) disableMFA(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[disableMFARequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if err := h.svc.DisableMFA(r.Context(), principal, req.CurrentPassword); err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "mfa_disabled"})
}

type teamMemberDTO struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	FullName    string     `json:"full_name"`
	Status      string     `json:"status"`
	MFAEnabled  bool       `json:"mfa_enabled"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	members, err := h.svc.ListTeamMembers(r.Context(), principal)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	out := make([]teamMemberDTO, 0, len(members))
	for _, m := range members {
		out = append(out, teamMemberDTO{
			ID: m.ID.String(), Email: m.Email, FullName: m.FullName, Status: string(m.Status),
			MFAEnabled: m.MFAEnabled, LastLoginAt: m.LastLoginAt, CreatedAt: m.CreatedAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": out})
}

type createUserRequest struct {
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handlers) createUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFromContext(r.Context())
	req, err := decodeJSON[createUserRequest](r)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	userID, err := h.svc.CreateTeamMember(r.Context(), principal, app.CreateTeamMemberParams{
		FullName: req.FullName,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user_id": userID})
}

func (h *Handlers) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		Secure:   h.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handlers) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   h.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clientIP(r *http.Request) string {
	// internal/platform/http/router.go deliberately does NOT mount
	// chi/middleware.RealIP (client-spoofable via X-Forwarded-For/
	// X-Real-IP with no trusted-proxy allowlist) — RemoteAddr is the raw
	// TCP peer address, the one thing here that isn't attacker-controlled.
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}
