package workreport

import "errors"

var (
	ErrLeaseNotFound      = errors.New("work lease not found")
	ErrLeaseExists        = errors.New("work lease already exists")
	ErrLeaseExpired       = errors.New("work lease expired")
	ErrLeaseNotOwned      = errors.New("work lease not owned by session")
	ErrLeaseClosing       = errors.New("work lease is closing")
	ErrPendingOperation   = errors.New("work lease has a pending operation")
	ErrNoPendingOperation = errors.New("work lease has no pending operation")
	ErrSequenceConflict   = errors.New("work lease sequence conflict")
	ErrClockBackward      = errors.New("work lease clock moved backward")
	ErrInvalidTTL         = errors.New("work lease ttl outside accepted range")
	ErrInvalidLease       = errors.New("invalid work lease")
	ErrLeaseTooLarge      = errors.New("work lease exceeds size limit")
	ErrInvalidAttempt     = errors.New("invalid publication attempt")
	ErrResultPersistence  = errors.New("publication result could not be persisted")
)
