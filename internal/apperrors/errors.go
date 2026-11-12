// Package apperrors defines the single, stable error taxonomy used by the
// accounting engine. Every financial-critical failure carries a stable code
// and one of five categories so that logs, metrics, alerts, and the public API
// can classify it without inspecting free-form error strings.
package apperrors

import (
	"errors"
	"fmt"
)

// Category is the coarse operational classification of an error. The set is
// deliberately small and closed; new categories require an ADR.
type Category string

const (
	// Validation means the caller supplied input that violates a domain rule.
	Validation Category = "validation"
	// Conflict means the request collided with concurrent state or an
	// already-existing resource (optimistic locking, duplicate reversal, ...).
	Conflict Category = "conflict"
	// Authorization means the caller is not permitted to perform the action.
	Authorization Category = "authorization"
	// Infrastructure means a dependency (database, object store, clock) failed.
	Infrastructure Category = "infrastructure"
	// Invariant means the engine detected a state that must never occur and
	// refused to continue (fail-closed).
	Invariant Category = "invariant"
)

// Valid reports whether the category is part of the closed taxonomy.
func (c Category) Valid() bool {
	switch c {
	case Validation, Conflict, Authorization, Infrastructure, Invariant:
		return true
	default:
		return false
	}
}

// Code is a stable, machine-readable identifier. Codes are part of the
// operational contract: they may be added, but never renamed or reused.
type Code string

const (
	CodeValidation            Code = "VALIDATION_FAILED"
	CodeInvalidEntry          Code = "ENTRY_INVALID"
	CodeEntryUnbalanced       Code = "ENTRY_UNBALANCED"
	CodePeriodClosed          Code = "PERIOD_CLOSED"
	CodePeriodTransition      Code = "PERIOD_TRANSITION_INVALID"
	CodePeriodNotFound        Code = "PERIOD_NOT_FOUND"
	CodeApprovalRequired      Code = "APPROVAL_REQUIRED"
	CodeApprovalUnconfigured  Code = "APPROVAL_POLICY_UNCONFIGURED"
	CodeConflict              Code = "CONFLICT"
	CodeVersionConflict       Code = "ENTRY_VERSION_CONFLICT"
	CodeAlreadyPosted         Code = "ENTRY_ALREADY_POSTED"
	CodeCannotModify          Code = "ENTRY_IMMUTABLE"
	CodeAtomicityViolation    Code = "ATOMICITY_VIOLATION"
	CodeTenantScopeViolation  Code = "TENANT_SCOPE_VIOLATION"
	CodeMigrationIncompatible Code = "MIGRATION_INCOMPATIBLE"
	CodeInfrastructure        Code = "INFRASTRUCTURE_FAILURE"
	CodeUnknown               Code = "UNKNOWN_ERROR"
)

// CodedError attaches a stable code and category to an underlying error while
// preserving errors.Is / errors.As behaviour.
type CodedError struct {
	Code     Code
	Category Category
	Message  string
	Err      error
}

func (e *CodedError) Error() string {
	switch {
	case e == nil:
		return ""
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	default:
		return string(e.Code)
	}
}

// Unwrap exposes the wrapped error to errors.Is / errors.As.
func (e *CodedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ErrorCode implements the classifier interface used by CategoryOf and CodeOf.
func (e *CodedError) ErrorCode() Code {
	if e == nil {
		return CodeUnknown
	}
	return e.Code
}

// ErrorCategory implements the classifier interface used by CategoryOf.
func (e *CodedError) ErrorCategory() Category {
	if e == nil || !e.Category.Valid() {
		return Infrastructure
	}
	return e.Category
}

// New builds a coded error without an underlying cause.
func New(code Code, category Category, message string) *CodedError {
	return &CodedError{Code: code, Category: category, Message: message}
}

// Wrap builds a coded error around an existing error.
func Wrap(code Code, category Category, err error) *CodedError {
	return &CodedError{Code: code, Category: category, Err: err}
}

// Wrapf builds a coded error with a formatted message around an existing error.
func Wrapf(code Code, category Category, err error, format string, args ...any) *CodedError {
	return &CodedError{Code: code, Category: category, Message: fmt.Sprintf(format, args...), Err: err}
}

// Coded is implemented by errors that carry an explicit taxonomy code and
// category.
type Coded interface {
	error
	ErrorCode() Code
	ErrorCategory() Category
}

// AsCoded returns the first coded error in err's chain.
func AsCoded(err error) (Coded, bool) {
	var coded Coded
	if errors.As(err, &coded) {
		return coded, true
	}
	return nil, false
}

// CodeOf returns the stable code carried by err, or CodeUnknown.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	if coded, ok := AsCoded(err); ok {
		if coded.ErrorCode() != "" {
			return coded.ErrorCode()
		}
	}
	return CodeUnknown
}

// CategoryOf returns the category carried by err. Unclassified errors are
// treated as infrastructure failures (fail-closed) so they are never silently
// reported as validation noise.
func CategoryOf(err error) Category {
	if err == nil {
		return ""
	}
	if coded, ok := AsCoded(err); ok {
		if category := coded.ErrorCategory(); category.Valid() {
			return category
		}
	}
	return Infrastructure
}

// IsCategory reports whether err belongs to the given category.
func IsCategory(err error, category Category) bool {
	return CategoryOf(err) == category
}
