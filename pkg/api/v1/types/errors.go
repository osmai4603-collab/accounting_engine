package types

import "errors"

var (
	ErrCurrencyMismatch          = errors.New("currency mismatch")
	ErrInvalidCurrency           = errors.New("invalid currency code")
	ErrInvalidAmount             = errors.New("invalid monetary amount")
	ErrAmountExceedsPrecision    = errors.New("monetary amount exceeds 28 digits")
	ErrSubMinorUnitAmount        = errors.New("amount has fractions beyond the currency minor unit")
	ErrNegativeAmountProhibited  = errors.New("negative amount prohibited")
	ErrZeroAmountLine            = errors.New("zero amount on accounting line")
	ErrUnbalancedMove            = errors.New("unbalanced accounting move")
	ErrFiscalPeriodClosed        = errors.New("fiscal period closed")
	ErrFiscalPeriodNotFound      = errors.New("fiscal period not found")
	ErrInvalidPeriodTransition   = errors.New("invalid fiscal period transition")
	ErrCannotPostToParentAccount = errors.New("cannot post to parent account")
	ErrAccountNotFound           = errors.New("account not found")
	ErrDuplicateAccountCode      = errors.New("duplicate account code")
	ErrMoveAlreadyPosted         = errors.New("accounting move already posted")
	ErrCannotModifyPostedMove    = errors.New("cannot modify posted move")
	ErrMakerCheckerViolation     = errors.New("maker-checker violation")
	ErrCapabilityNotImplemented  = errors.New("capability not implemented")
)
