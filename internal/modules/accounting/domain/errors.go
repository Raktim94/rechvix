package domain

import "errors"

var (
	ErrNotFound = errors.New("accounting: not found")
	// ErrReceiptAlreadyReversed: a receipt can be reversed once; the reversal is itself the audit entry.
	ErrReceiptAlreadyReversed = errors.New("accounting: receipt is already reversed")
	// ErrDuplicateClientRef: a receipt with this client_ref already exists (the caller should read it back, not book again).
	ErrDuplicateClientRef = errors.New("accounting: a receipt with this client_ref already exists")
	ErrUnbalancedJournal  = errors.New("accounting: journal debits and credits do not sum equal")
	ErrEmptyJournal       = errors.New("accounting: journal must have at least two lines")
	ErrAccountNotFound    = errors.New("accounting: account not found")
	ErrPeriodLocked       = errors.New("accounting: fiscal period is locked")
	ErrChartAlreadyExists = errors.New("accounting: chart of accounts already exists for this organisation")
)

// InvalidInput is a problem with what the caller sent (not a server fault): the HTTP layer answers it as 400 with Msg.
type InvalidInput struct{ Msg string }

func (e *InvalidInput) Error() string { return "accounting: " + e.Msg }

// Invalid builds an InvalidInput.
func Invalid(msg string) error { return &InvalidInput{Msg: msg} }
