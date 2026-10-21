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

	// ErrInvalidInvoiceLanguage means the requested invoice language is not one of the supported ones.
	ErrInvalidInvoiceLanguage = errors.New("sales: unsupported invoice language")

	// ErrExchangeRateRequired means a document in a currency other than the company's was
	// created without an exchange rate; defaulting it to 1 would book foreign money as if
	// it were company currency.
	ErrExchangeRateRequired = errors.New("sales: an exchange rate is required for a foreign-currency document")
	// ErrUnknownCurrency means the document currency is not a known ISO 4217 currency.
	ErrUnknownCurrency = errors.New("sales: unknown currency code")
	// ErrBaseCurrencyMismatch means the document names a base currency other than the company's.
	ErrBaseCurrencyMismatch = errors.New("sales: the base currency must be the company's currency")
)
