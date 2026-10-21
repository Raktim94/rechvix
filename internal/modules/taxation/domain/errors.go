package domain

import (
	"errors"
	"strings"
)

var (
	ErrNotFound = errors.New("taxation: not found")
	// ErrNoLines is returned when a TaxCalculationInput has zero lines —
	// calculating tax for an empty document is almost certainly a caller
	// bug, not a valid zero-tax result.
	ErrNoLines = errors.New("taxation: at least one line is required")
	// ErrRateNotConfigured is the regime-agnostic sentinel a TaxEngine
	// implementation wraps its own not-configured error with (e.g.
	// gstindia.ErrRateNotConfigured), so callers above taxation in the
	// module layering (sales, purchases, ...) can recognize this specific,
	// user-actionable case via errors.Is without importing a regime plugin
	// directly — see docs/architecture.md §2's layering rule.
	ErrRateNotConfigured = errors.New("taxation: no tax rate configured for this line's HSN/SAC code on this date")

	// ErrUnsupportedTaxCase marks a sale or purchase the tax packs deliberately
	// refuse (a region or cross-border case they do not implement, a document date
	// before the verified rates). It is a user-facing condition, not a server fault:
	// callers map it to a 4xx with UserMessage.
	ErrUnsupportedTaxCase = errors.New("taxation: this tax case is not supported")
	// ErrInvalidTaxInput marks tax input the user can correct (a missing or
	// unknown tax category, a missing destination state).
	ErrInvalidTaxInput = errors.New("taxation: the tax details are incomplete or invalid")
)

// TaxCaseError is a tax-pack error that belongs to one of the two user-facing
// kinds above. errors.Is(err, Kind) is true for it, and for anything wrapping it.
type TaxCaseError struct {
	Kind error
	Msg  string
}

func (e *TaxCaseError) Error() string        { return "taxregimes: " + e.Msg }
func (e *TaxCaseError) Is(target error) bool { return target == e.Kind }

// UserMessage extracts the human-readable part of a tax-pack error, without the
// "calculating tax: calculating:" prefixes added as it travels up the call stack.
func UserMessage(err error) string {
	s := err.Error()
	if i := strings.Index(s, "taxregimes: "); i >= 0 {
		return s[i+len("taxregimes: "):]
	}
	return s
}
