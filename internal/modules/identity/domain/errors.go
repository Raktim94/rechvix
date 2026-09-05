package domain

import "errors"

var (
	ErrNotFound                = errors.New("identity: not found")
	ErrInvalidCredentials      = errors.New("identity: invalid credentials")
	ErrUserDisabled            = errors.New("identity: user disabled")
	ErrMFARequired             = errors.New("identity: mfa code required")
	ErrMFAInvalid              = errors.New("identity: mfa code invalid")
	ErrSessionInvalid          = errors.New("identity: session invalid or expired")
	ErrTokenInvalid            = errors.New("identity: token invalid, expired, or already used")
	ErrRateLimited             = errors.New("identity: too many attempts, try again later")
	ErrPasswordConfirmMismatch = errors.New("identity: new password and confirmation do not match")
	ErrAPIKeyInvalid           = errors.New("identity: api key invalid, expired, or revoked")
	ErrEmptyScopeList          = errors.New("identity: an api key must have at least one explicit scope")
	ErrUnknownScope            = errors.New("identity: unrecognized api key scope")
	ErrEmailAlreadyExists      = errors.New("identity: an account with this email already exists")
)
