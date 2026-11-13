package application

import (
	"errors"
	"fmt"
)

// Stable error taxonomy. Transports map these to their protocol codes; callers
// inspect them with errors.Is. Adapters must not invent ad-hoc error strings
// for these categories.
var (
	ErrValidation            = errors.New("validation failed")
	ErrNotFound              = errors.New("resource not found")
	ErrConflict              = errors.New("state conflict")
	ErrUnauthorized          = errors.New("unauthenticated")
	ErrForbidden             = errors.New("forbidden")
	ErrPeriodClosed          = errors.New("fiscal period closed")
	ErrDuplicateRequest      = errors.New("duplicate request")
	ErrDependencyUnavailable = errors.New("dependency unavailable")
	ErrInternal              = errors.New("internal error")
)

// Error wraps a taxonomy sentinel with a stable, non-sensitive message and an
// optional underlying cause for logs (never returned to clients).
type Error struct {
	Kind    error
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s", e.Message, e.Cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Kind }

func NewError(kind error, message string, cause error) error {
	return &Error{Kind: kind, Message: message, Cause: cause}
}

// PublicMessage returns the client-safe message without the cause chain.
func (e *Error) PublicMessage() string { return e.Message }
