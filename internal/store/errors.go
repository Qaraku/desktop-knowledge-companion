package store

import "errors"

var (
	ErrDataDirNotAbsolute  = errors.New("data directory must be an absolute path")
	ErrDataDirEmpty        = errors.New("data directory is required")
	ErrInstanceLocked      = errors.New("knowledge core is already running for this data directory")
	ErrNotFound            = errors.New("requested record was not found")
	ErrVersionConflict     = errors.New("record version is no longer current")
	ErrInvalidState        = errors.New("record is not in a valid state for this operation")
	ErrApprovalInvalid     = errors.New("approval is missing, expired, consumed, or does not match the action")
	ErrIdempotencyConflict = errors.New("idempotency key was reused with different parameters")
)
