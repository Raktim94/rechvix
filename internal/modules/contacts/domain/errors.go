package domain

import "errors"

var (
	ErrNotFound              = errors.New("contacts: not found")
	ErrDuplicateRegistration = errors.New("contacts: this registration number is already recorded for this party")
	ErrInvalidPartyType      = errors.New("contacts: invalid party_type")
	ErrInvalidAddressType    = errors.New("contacts: invalid address_type")
	ErrLegalNameRequired     = errors.New("contacts: legal_name is required")
	ErrInvalidPhone          = errors.New("contacts: phone must be exactly 10 digits")
	ErrDuplicatePhone        = errors.New("contacts: this phone number is already used by another customer/supplier")
)
