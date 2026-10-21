package domain

import "errors"

var ErrNotFound = errors.New("organisation: not found")

// ErrInvalidInput marks a request the caller can fix (bad code, bad format).
var ErrInvalidInput = errors.New("organisation: invalid input")
