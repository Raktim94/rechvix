package domain

import "errors"

var (
	ErrNotFound             = errors.New("sales: not found")
	ErrInvalidDocumentType  = errors.New("sales: invalid document type")
	ErrDocumentNotDraft     = errors.New("sales: document is not in DRAFT status")
	ErrDocumentNotFinalized = errors.New("sales: reference document is not FINALIZED")
	ErrEmptyDocument        = errors.New("sales: document has no lines")
	ErrDuplicateNumber      = errors.New("sales: document number already in use for this document type")
	// ErrZeroValueDocument is returned when a document with a positive
	// grand total is required (revenue-affecting types post an accounting
	// journal, whose Layer-1 double-entry check — every line is a debit or
	// a credit, never neither — a zero-total document can never satisfy)
	// but every line priced out to zero. Caught here, before tax
	// calculation's snapshot and the accounting post are even attempted,
	// so the caller gets one clear, specific reason instead of a
	// generic 500 surfaced from deep inside the accounting layer.
	ErrZeroValueDocument = errors.New("sales: document grand total is zero")
	// ErrReturnQuantityExceedsSource guards ConvertDocument's optional
	// partial-quantity override: a return/credit note can never claim
	// more of a line than the source document actually sold.
	ErrReturnQuantityExceedsSource = errors.New("sales: return quantity exceeds the quantity on the source document line")
)
