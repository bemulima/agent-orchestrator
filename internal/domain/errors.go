package domain

import "errors"

// Common domain errors.
var (
	ErrNotFound       = errors.New("not found")
	ErrForbidden      = errors.New("forbidden")
	ErrValidation     = errors.New("validation error")
	ErrConflict       = errors.New("conflict")
	ErrInvalidStatus  = errors.New("invalid status")
	ErrApprovalNeeded = errors.New("approval required")
	ErrWriteScope     = errors.New("write scope violation")
)
