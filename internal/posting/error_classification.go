package posting

import (
	"context"
	"errors"

	"github.com/osmai4603-collab/accounting_engine/internal/apperrors"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

// ClassifyError maps a posting error to the engine-wide error taxonomy. The
// mapping is exhaustive for the financial-critical sentinel errors and
// defaults to the invariant category so an unexpected error is never reported
// as harmless validation noise.
func ClassifyError(err error) (apperrors.Code, apperrors.Category) {
	if err == nil {
		return "", ""
	}
	if coded, ok := apperrors.AsCoded(err); ok {
		code, category := coded.ErrorCode(), coded.ErrorCategory()
		if code == "" {
			code = apperrors.CodeUnknown
		}
		if !category.Valid() {
			category = apperrors.Invariant
		}
		return code, category
	}
	switch {
	case errors.Is(err, financialtypes.ErrFiscalPeriodClosed),
		errors.Is(err, policy.ErrHardClosedPeriod),
		errors.Is(err, policy.ErrSoftClosedPeriod):
		return apperrors.CodePeriodClosed, apperrors.Conflict
	case errors.Is(err, financialtypes.ErrInvalidPeriodTransition):
		return apperrors.CodePeriodTransition, apperrors.Conflict
	case errors.Is(err, financialtypes.ErrFiscalPeriodNotFound),
		errors.Is(err, masterdata.ErrFiscalPeriodNotFound):
		return apperrors.CodePeriodNotFound, apperrors.Validation
	case errors.Is(err, ErrApprovalRequired), errors.Is(err, ErrAdjustmentDelegationRequired):
		return apperrors.CodeApprovalRequired, apperrors.Authorization
	case errors.Is(err, masterdata.ErrCompanyInactive):
		return apperrors.CodeValidation, apperrors.Authorization
	case errors.Is(err, financialtypes.ErrUnbalancedMove),
		errors.Is(err, ErrFunctionalBalanceMismatch),
		errors.Is(err, financialtypes.ErrZeroAmountLine),
		errors.Is(err, financialtypes.ErrInvalidAmount),
		errors.Is(err, financialtypes.ErrCurrencyMismatch),
		errors.Is(err, financialtypes.ErrSubMinorUnitAmount):
		return apperrors.CodeEntryUnbalanced, apperrors.Validation
	case errors.Is(err, ErrEntryNotFound):
		return apperrors.CodeValidation, apperrors.Validation
	case errors.Is(err, financialtypes.ErrMoveAlreadyPosted),
		errors.Is(err, ErrEntryAlreadyReversed),
		errors.Is(err, ErrIdempotencyConflict),
		errors.Is(err, financialtypes.ErrCannotModifyPostedMove),
		errors.Is(err, ErrEntryNotDraft):
		return apperrors.CodeConflict, apperrors.Conflict
	case errors.Is(err, ErrAtomicPostingOpenLineRequired),
		errors.Is(err, ErrAtomicReversalRequired),
		errors.Is(err, ErrUnsupportedMutationBackend):
		return apperrors.CodeAtomicityViolation, apperrors.Invariant
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return apperrors.CodeInfrastructure, apperrors.Infrastructure
	case errors.Is(err, ErrInvalidEntry),
		errors.Is(err, ErrReversalDateBeforeOriginal),
		errors.Is(err, ErrReversalActorRequired),
		errors.Is(err, ErrEntryHashUnavailable),
		errors.Is(err, ErrAdjustmentPeriodAuthorizationRequired),
		errors.Is(err, ErrAdjustingEntryMetadataRequired),
		errors.Is(err, ErrInvalidAdjustingEntryMetadata),
		errors.Is(err, ErrFunctionalCurrencyMismatch),
		errors.Is(err, ErrCostCenterRequired),
		errors.Is(err, ErrDimensionPolicyUnavailable),
		errors.Is(err, ErrRoundingAccountRequired):
		return apperrors.CodeInvalidEntry, apperrors.Validation
	default:
		return apperrors.CodeUnknown, apperrors.Invariant
	}
}

// CodedError wraps err with a stable taxonomy code while preserving the
// original error for errors.Is/errors.As callers.
func CodedError(err error, code apperrors.Code, category apperrors.Category) error {
	if err == nil {
		return nil
	}
	if _, ok := apperrors.AsCoded(err); ok {
		return err
	}
	return apperrors.Wrap(code, category, err)
}
