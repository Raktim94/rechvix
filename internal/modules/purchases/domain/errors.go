package domain

import "errors"

var (
	ErrNotFound             = errors.New("purchases: not found")
	ErrInvalidDocumentType  = errors.New("purchases: invalid document type")
	ErrDocumentNotDraft     = errors.New("purchases: document is not in DRAFT status")
	ErrDocumentNotFinalized = errors.New("purchases: document is not in FINALIZED status")
	ErrEmptyDocument        = errors.New("purchases: document has no lines")
	ErrDuplicateNumber      = errors.New("purchases: document number already in use for this document type")
)
