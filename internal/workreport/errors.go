package workreport

import (
	"errors"
	"fmt"
)

// LocalErrorPendingOperation is part of the public package API.
const LocalErrorPendingOperation = "pending-operation"

var (
	// ErrLeaseNotFound is part of the public package API.
	ErrLeaseNotFound = errors.New("work lease not found")
	// ErrLeaseExists is part of the public package API.
	ErrLeaseExists = errors.New("work lease already exists")
	// ErrLeaseExpired is part of the public package API.
	ErrLeaseExpired = errors.New("work lease expired")
	// ErrLeaseNotOwned is part of the public package API.
	ErrLeaseNotOwned = errors.New("work lease not owned by session")
	// ErrLeaseClosing is part of the public package API.
	ErrLeaseClosing = errors.New("work lease is closing")
	// ErrPendingOperation is part of the public package API.
	ErrPendingOperation = errors.New("work lease has a pending operation")
	// ErrLegacyAudience is part of the public package API.
	ErrLegacyAudience = errors.New("legacy work lease has no persisted audience; start a new work item")
	// ErrNoPendingOperation is part of the public package API.
	ErrNoPendingOperation = errors.New("work lease has no pending operation")
	// ErrSequenceConflict is part of the public package API.
	ErrSequenceConflict = errors.New("work lease sequence conflict")
	// ErrClockBackward is part of the public package API.
	ErrClockBackward = errors.New("work lease clock moved backward")
	// ErrInvalidTTL is part of the public package API.
	ErrInvalidTTL = errors.New("work lease ttl outside accepted range")
	// ErrInvalidLease is part of the public package API.
	ErrInvalidLease = errors.New("invalid work lease")
	// ErrLeaseTooLarge is part of the public package API.
	ErrLeaseTooLarge = errors.New("work lease exceeds size limit")
	// ErrInvalidAttempt is part of the public package API.
	ErrInvalidAttempt = errors.New("invalid publication attempt")
	// ErrResultPersistence is part of the public package API.
	ErrResultPersistence = errors.New("publication result could not be persisted")
)

// PendingOperationError preserves the exact resumable operation identity for
// machine callers while remaining compatible with ErrPendingOperation.
// PendingOperationError is part of the public package API.
type PendingOperationError struct {
	OperationKey string
	Action       Action
}

// Error is part of the public package API.
func (e *PendingOperationError) Error() string {
	return fmt.Sprintf("%s: operation_key=%s action=%s", ErrPendingOperation, e.OperationKey, e.Action)
}

// Unwrap is part of the public package API.
func (e *PendingOperationError) Unwrap() error { return ErrPendingOperation }
